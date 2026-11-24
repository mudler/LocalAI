// SPDX-License-Identifier: MIT
package cosignverify

import (
	"context"
	"encoding/json"
	"github.com/mudler/LocalAI/tests/addons/fixtures/signing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sigstore/sigstore-go/pkg/root"
	"net/http/httptest"
)

var _ = Describe("offline cryptographic verification", func() {
	It("verifies trusted signatures and rejects identity digest and trust mismatches offline", func() {
		authority, e := signing.New()
		Expect(e).NotTo(HaveOccurred())
		reg, digest := signedByCosignWithoutReferrersAPI(nil)
		for _, scenario := range []string{"valid", "identity", "digest", "trust", "signature", "tlog", "sct"} {
			id := "builder@example.invalid"
			signedDigest := digest[7:]
			if scenario == "identity" {
				id = "attacker@example.invalid"
			}
			if scenario == "digest" {
				signedDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			}
			blob, e := authority.Bundle(signedDigest, id, "https://issuer.invalid")
			Expect(e).NotTo(HaveOccurred())
			if scenario == "signature" || scenario == "tlog" {
				var doc map[string]any
				Expect(json.Unmarshal(blob, &doc)).To(Succeed())
				if scenario == "signature" {
					doc["messageSignature"].(map[string]any)["signature"] = "AAAA"
				} else {
					doc["verificationMaterial"].(map[string]any)["tlogEntries"] = []any{}
				}
				blob, e = json.Marshal(doc)
				Expect(e).NotTo(HaveOccurred())
			}
			reg, _ = signedByCosignWithoutReferrersAPI(blob)
			server := httptest.NewServer(reg)
			DeferCleanup(server.Close)
			trust := authority
			if scenario == "trust" {
				trust, e = signing.New()
				Expect(e).NotTo(HaveOccurred())
			}
			var material root.TrustedMaterial = trust
			if scenario == "sct" {
				material = noCTTrust{trust}
			}
			v, e := NewVerifierWithTrustedMaterial(Policy{Issuer: "https://issuer.invalid", Identity: "builder@example.invalid"}, nil, nil, material)
			Expect(e).NotTo(HaveOccurred())
			e = v.VerifyImage(context.Background(), server.Listener.Addr().String()+"/image@"+digest)
			if scenario == "valid" {
				Expect(e).To(Succeed())
			} else {
				Expect(e).To(MatchError(ContainSubstring(ErrPolicyRejected.Error())))
			}
		}
	})
})

type noCTTrust struct{ root.TrustedMaterial }

func (noCTTrust) CTLogs() map[string]*root.TransparencyLog { return nil }
