// SPDX-License-Identifier: MIT
// Package signing builds hermetic cryptographic fixtures, never production trust.
package signing

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"time"

	"encoding/pem"
	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	ct "github.com/google/certificate-transparency-go"
	"github.com/google/certificate-transparency-go/tls"
	ctx509util "github.com/google/certificate-transparency-go/x509util"
	"github.com/sigstore/sigstore-go/pkg/root"
)

type Authority struct {
	root.BaseTrustedMaterial
	parent                *x509.Certificate
	key, logKey, rekorKey *ecdsa.PrivateKey
	logID, rekorID        [32]byte
}

func New() (*Authority, error) {
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, e
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture root"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	raw, e := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if e != nil {
		return nil, e
	}
	cert, e := x509.ParseCertificate(raw)
	if e != nil {
		return nil, e
	}
	rekorKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, e
	}
	rekorDER, e := x509.MarshalPKIXPublicKey(rekorKey.Public())
	if e != nil {
		return nil, e
	}
	logKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, e
	}
	der, e := x509.MarshalPKIXPublicKey(logKey.Public())
	if e != nil {
		return nil, e
	}
	return &Authority{parent: cert, key: key, logKey: logKey, rekorKey: rekorKey, logID: sha256.Sum256(der), rekorID: sha256.Sum256(rekorDER)}, nil
}
func (a *Authority) FulcioCertificateAuthorities() []root.CertificateAuthority {
	return []root.CertificateAuthority{&root.FulcioCertificateAuthority{Root: a.parent}}
}
func (a *Authority) CTLogs() map[string]*root.TransparencyLog {
	return map[string]*root.TransparencyLog{hex.EncodeToString(a.logID[:]): {PublicKey: a.logKey.Public()}}
}

// Bundle signs a manifest digest and embeds a real SCT and Rekor signed entry
// timestamp (the inclusion promise supported by Sigstore bundle v0.1).
func (a *Authority) Bundle(digest, identity, issuer string) ([]byte, error) {
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, e
	}
	now := time.Now().Add(-time.Second)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), EmailAddresses: []string{identity}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}, ExtraExtensions: []pkix.Extension{{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}, Value: []byte(issuer)}}}
	der, e := x509.CreateCertificate(rand.Reader, tmpl, a.parent, key.Public(), a.key)
	if e != nil {
		return nil, e
	}
	pre, e := x509.ParseCertificate(der)
	if e != nil {
		return nil, e
	}
	sct := ct.SignedCertificateTimestamp{SCTVersion: ct.V1, LogID: ct.LogID{KeyID: a.logID}, Timestamp: uint64(now.UnixMilli())}
	entry := ct.LogEntry{Leaf: ct.MerkleTreeLeaf{Version: ct.V1, LeafType: ct.TimestampedEntryLeafType, TimestampedEntry: &ct.TimestampedEntry{Timestamp: sct.Timestamp, EntryType: ct.PrecertLogEntryType, PrecertEntry: &ct.PreCert{IssuerKeyHash: sha256.Sum256(a.parent.RawSubjectPublicKeyInfo), TBSCertificate: pre.RawTBSCertificate}}}}
	input, e := ct.SerializeSCTSignatureInput(sct, entry)
	if e != nil {
		return nil, e
	}
	hash := sha256.Sum256(input)
	sig, e := a.logKey.Sign(rand.Reader, hash[:], crypto.SHA256)
	if e != nil {
		return nil, e
	}
	sct.Signature = ct.DigitallySigned{Algorithm: tls.SignatureAndHashAlgorithm{Hash: tls.SHA256, Signature: tls.ECDSA}, Signature: sig}
	list, e := ctx509util.MarshalSCTsIntoSCTList([]*ct.SignedCertificateTimestamp{&sct})
	if e != nil {
		return nil, e
	}
	raw, e := tls.Marshal(*list)
	if e != nil {
		return nil, e
	}
	raw, e = asn1.Marshal(raw)
	if e != nil {
		return nil, e
	}
	tmpl.ExtraExtensions = append(tmpl.ExtraExtensions, pkix.Extension{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}, Value: raw})
	der, e = x509.CreateCertificate(rand.Reader, tmpl, a.parent, key.Public(), a.key)
	if e != nil {
		return nil, e
	}
	cert, e := x509.ParseCertificate(der)
	if e != nil {
		return nil, e
	}
	digestBytes, e := hex.DecodeString(digest)
	if e != nil {
		return nil, e
	}
	sig, e = key.Sign(rand.Reader, digestBytes, crypto.SHA256)
	if e != nil {
		return nil, e
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	body, e := json.Marshal(map[string]any{"apiVersion": "0.0.1", "kind": "hashedrekord", "spec": map[string]any{"data": map[string]any{"hash": map[string]string{"algorithm": "sha256", "value": digest}}, "signature": map[string]any{"content": sig, "publicKey": map[string]any{"content": certPEM}}}})
	if e != nil {
		return nil, e
	}
	body, e = jsoncanonicalizer.Transform(body)
	if e != nil {
		return nil, e
	}
	payload, e := json.Marshal(map[string]any{"body": body, "integratedTime": now.Unix(), "logIndex": 0, "logID": hex.EncodeToString(a.rekorID[:])})
	if e != nil {
		return nil, e
	}
	payload, e = jsoncanonicalizer.Transform(payload)
	if e != nil {
		return nil, e
	}
	hash = sha256.Sum256(payload)
	set, e := a.rekorKey.Sign(rand.Reader, hash[:], crypto.SHA256)
	if e != nil {
		return nil, e
	}
	return json.Marshal(map[string]any{"mediaType": "application/vnd.dev.sigstore.bundle+json;version=0.1", "messageSignature": map[string]any{"messageDigest": map[string]any{"algorithm": "SHA2_256", "digest": digestBytes}, "signature": sig}, "verificationMaterial": map[string]any{"x509CertificateChain": map[string]any{"certificates": []any{map[string]any{"rawBytes": base64.StdEncoding.EncodeToString(der)}}}, "tlogEntries": []any{map[string]any{"logIndex": "0", "logId": map[string]any{"keyId": a.rekorID[:]}, "kindVersion": map[string]string{"kind": "hashedrekord", "version": "0.0.1"}, "integratedTime": now.Unix(), "inclusionPromise": map[string]any{"signedEntryTimestamp": set}, "canonicalizedBody": body}}}})
}
func (a *Authority) RekorLogs() map[string]*root.TransparencyLog {
	return map[string]*root.TransparencyLog{hex.EncodeToString(a.rekorID[:]): {PublicKey: a.rekorKey.Public(), ValidityPeriodStart: time.Now().Add(-time.Hour)}}
}
