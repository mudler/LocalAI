//go:build auth

package auth_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	testOIDCIssuer   = "https://issuer.example.test"
	testOIDCClientID = "localai-test-client"
)

// signES256IDToken builds a minimal ES256-signed JWT in JOSE compact form. The
// signature is the raw R||S encoding (32 bytes each for P-256) that OIDC
// verifiers expect, not ASN.1/DER.
func signES256IDToken(key *ecdsa.PrivateKey, claims map[string]any) string {
	b64 := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	header := b64([]byte(`{"alg":"ES256","typ":"JWT"}`))
	payloadJSON, err := json.Marshal(claims)
	Expect(err).NotTo(HaveOccurred())
	signingInput := header + "." + b64(payloadJSON)

	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	Expect(err).NotTo(HaveOccurred())
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])

	return signingInput + "." + b64(sig)
}

var _ = Describe("OIDC ID-token signing algorithms", func() {
	var (
		ctx     context.Context
		key     *ecdsa.PrivateKey
		idToken string
		keySet  *oidc.StaticKeySet
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		idToken = signES256IDToken(key, map[string]any{
			"iss":   testOIDCIssuer,
			"sub":   "user-1",
			"aud":   testOIDCClientID,
			"email": "user@example.test",
			"iat":   time.Now().Unix(),
			"exp":   time.Now().Add(time.Hour).Unix(),
		})
		keySet = &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{key.Public()}}
	})

	It("accepts an EC (ES256) signed ID token with the configured algorithms", func() {
		verifier := oidc.NewVerifier(testOIDCIssuer, keySet, &oidc.Config{
			ClientID:             testOIDCClientID,
			SupportedSigningAlgs: auth.OIDCSupportedSigningAlgs(),
		})
		tok, err := verifier.Verify(ctx, idToken)
		Expect(err).NotTo(HaveOccurred())
		Expect(tok.Subject).To(Equal("user-1"))
	})

	It("rejects the same token under go-oidc's RS256-only default (the pre-fix behavior)", func() {
		// This reproduces #10677: without a broadened SupportedSigningAlgs the
		// verifier only accepts RS256 and rejects EC-signed tokens.
		verifier := oidc.NewVerifier(testOIDCIssuer, keySet, &oidc.Config{
			ClientID: testOIDCClientID,
		})
		_, err := verifier.Verify(ctx, idToken)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("ES256"))
	})

	It("advertises the common asymmetric algorithms and excludes the symmetric HS256", func() {
		algs := auth.OIDCSupportedSigningAlgs()
		Expect(algs).To(ContainElements(
			oidc.RS256, oidc.RS384, oidc.RS512,
			oidc.ES256, oidc.ES384, oidc.ES512,
			oidc.PS256, oidc.EdDSA,
		))
		Expect(algs).NotTo(ContainElement("HS256"))
	})
})
