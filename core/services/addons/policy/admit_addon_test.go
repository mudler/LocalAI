// SPDX-License-Identifier: MIT
package policy

import (
	"context"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The real provider is what the gateway asks for a read without a resource.
var _ gateway.AddonAdmission = (*GatewayProvider)(nil)

var _ = Describe("admission of a read that names no resource", func() {
	bg := context.Background()
	ran := func(n *int) func(context.Context) error {
		return func(context.Context) error { *n++; return nil }
	}

	It("runs the work for a current account", func() {
		f := newFixture(ModeNamedUser)
		n := 0
		Expect(f.policy.AdmitAddon(bg, named("owner"), "fixture", ran(&n))).To(Succeed())
		Expect(n).To(Equal(1))
	})

	It("refuses a stale account and does not run the work", func() {
		f := newFixture(ModeNamedUser)
		f.accounts.block("owner")
		n := 0
		Expect(f.policy.AdmitAddon(bg, named("owner"), "fixture", ran(&n))).To(MatchError(ErrForbidden))
		Expect(n).To(Equal(0))
	})

	It("refuses an identity that does not fit the mode", func() {
		f := newFixture(ModeNamedUser)
		n := 0
		bad := named("owner")
		bad.CredentialID = ""
		Expect(f.policy.AdmitAddon(bg, bad, "fixture", ran(&n))).To(MatchError(ErrForbidden))
		Expect(n).To(Equal(0))
	})

	It("refuses a held add-on as unavailable, and serves it again after the release", func() {
		f := newFixture(ModeNamedUser)
		release, err := f.policy.HoldAddon(bg, "fixture")
		Expect(err).NotTo(HaveOccurred())
		n := 0
		Expect(f.policy.AdmitAddon(bg, named("owner"), "fixture", ran(&n))).To(MatchError(gateway.ErrUnavailable))
		Expect(n).To(Equal(0))
		// Another add-on is not held.
		Expect(f.policy.AdmitAddon(bg, named("owner"), "other", ran(&n))).To(Succeed())
		release()
		Expect(f.policy.AdmitAddon(bg, named("owner"), "fixture", ran(&n))).To(Succeed())
		Expect(n).To(Equal(2))
	})

	It("refuses an empty add-on ID and a nil work function", func() {
		f := newFixture(ModeNamedUser)
		n := 0
		Expect(f.policy.AdmitAddon(bg, named("owner"), "", ran(&n))).To(MatchError(ErrForbidden))
		Expect(f.policy.AdmitAddon(bg, named("owner"), "fixture", nil)).To(MatchError(ErrForbidden))
		Expect(n).To(Equal(0))
	})

	It("is reached through the gateway provider", func() {
		f := newFixture(ModeNamedUser)
		var adm gateway.Admission = f.policy.Gateway()
		aa, ok := adm.(gateway.AddonAdmission)
		Expect(ok).To(BeTrue())
		n := 0
		Expect(aa.AdmitAddon(bg, named("owner"), "fixture", ran(&n))).To(Succeed())
		f.accounts.block("owner")
		Expect(aa.AdmitAddon(bg, named("owner"), "fixture", ran(&n))).To(MatchError(ErrForbidden))
		Expect(n).To(Equal(1))
	})
})
