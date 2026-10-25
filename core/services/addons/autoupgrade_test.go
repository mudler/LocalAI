// SPDX-License-Identifier: MIT
package addons

import (
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("automatic upgrade of add-ons", func() {
	It("upgrades an installed add-on through the same path as a manual upgrade", func() {
		r := newUpdateRig(true)
		s := r.installChannel()
		installed := s.Generations[0].Receipt
		next := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})

		res := r.m.AutoUpgrade(ctx)
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(HaveOccurred())
		Expect(res[0].Upgraded).To(BeTrue())
		Expect(res[0].AddonID).To(Equal("sample"))

		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(2)))
		// The result is the one a manual upgrade leaves: same library, same
		// channel, the candidate's digest, the old generation kept.
		got := s.Generations[1].Receipt
		Expect(got.Digest).To(Equal(next.Digest))
		Expect(got.SourceID).To(Equal(installed.SourceID))
		Expect(got.Selection).To(Equal("development"))
		Expect(s.Generations[0].Receipt.Directory).To(BeADirectory())
		// It was recorded as a pinned upgrade of the owner's own.
		r.m.mu.Lock()
		defer r.m.mu.Unlock()
		found := false
		for _, op := range r.m.journal.Operations {
			if op.Command.Action == "upgrade" {
				found = true
				Expect(op.Actor).To(Equal(AutoUpgradeActor))
				Expect(op.Command.ExpectedDigest).To(Equal(installed.Digest))
				Expect(op.Command.CandidateDigest).To(Equal(next.Digest))
				Expect(op.Status.Phase).To(Equal("completed"))
			}
		}
		Expect(found).To(BeTrue())
	})

	It("does nothing when no newer version is listed", func() {
		r := newUpdateRig(true)
		r.installChannel()
		before := r.journalOps()
		Expect(r.m.AutoUpgrade(ctx)).To(BeEmpty())
		Expect(r.journalOps()).To(Equal(before))
		s, _ := r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(1)))
	})

	It("refuses a candidate whose package no longer matches its listed digest and replaces nothing", func() {
		r := newUpdateRig(true)
		s := r.installChannel()
		installed := s.Generations[0].Receipt
		next := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		r.lib.mu.Lock()
		for name := range r.lib.archives {
			if r.lib.archives[name] != nil && next.Artifact == r.lib.server.URL+name {
				r.lib.archives[name] = []byte("corrupt")
			}
		}
		r.lib.mu.Unlock()

		res := r.m.AutoUpgrade(ctx)
		Expect(res).To(HaveLen(1))
		Expect(res[0].Upgraded).To(BeFalse())
		Expect(res[0].Err).To(MatchError(ErrOperationFailed))
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(1)))
		Expect(s.Generations).To(HaveLen(1))
		Expect(s.Generations[0].Receipt.Digest).To(Equal(installed.Digest))
		Expect(s.Generations[0].Receipt.Directory).To(BeADirectory())

		// The same bad candidate is not tried again and adds no record.
		before := r.journalOps()
		res = r.m.AutoUpgrade(ctx)
		Expect(res).To(HaveLen(1))
		Expect(res[0].SkippedAsFailure).To(BeTrue())
		Expect(r.journalOps()).To(Equal(before))
	})

	It("refuses a candidate that fails the signature policy of its library and replaces nothing", func() {
		r := newUpdateRig(true)
		s := r.installChannel()
		installed := s.Generations[0].Receipt
		r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		// The library now requires a signature. An archive with only a pinned
		// digest cannot satisfy it, so the staging fails closed.
		Expect(r.lib.sources.Save(ctx, actor, catalog.Source{ID: "test", Name: "Test", URL: r.lib.server.URL + "/library.yaml", Enabled: true, AllowLoopbackHTTP: true,
			Policy: catalog.VerificationPolicy{RequireSignature: true, Issuer: "https://issuer.example", Identity: "signer@example.org"}})).To(Succeed())

		res := r.m.AutoUpgrade(ctx)
		Expect(res).To(HaveLen(1))
		Expect(res[0].Upgraded).To(BeFalse())
		Expect(res[0].Err).To(HaveOccurred())
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(1)))
		Expect(s.Generations).To(HaveLen(1))
		Expect(s.Generations[0].Receipt.Digest).To(Equal(installed.Digest))
	})

	It("does not upgrade to a release the host cannot run", func() {
		r := newUpdateRig(true)
		s := r.installChannel()
		installed := s.Generations[0].Receipt
		r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{SDK: contract.VersionRange{Min: 9, Max: 9}})
		before := r.journalOps()
		Expect(r.m.AutoUpgrade(ctx)).To(BeEmpty())
		Expect(r.journalOps()).To(Equal(before))
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generations[0].Receipt.Digest).To(Equal(installed.Digest))
		Expect(s.Generation).To(Equal(uint64(1)))
	})

	It("does nothing without an update checker", func() {
		r := newUpdateRig(false)
		r.installChannel()
		r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		Expect(r.m.AutoUpgrade(ctx)).To(BeNil())
		s, _ := r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(1)))
	})

	It("keeps the manual upgrade closed to a caller who is not an administrator", func() {
		r := newUpdateRig(true)
		s := r.installChannel()
		installed := s.Generations[0].Receipt
		next := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		_, e := r.m.Submit(ctx, AutoUpgradeActor, upgrade("not-admin", installed.Digest, next.Digest))
		Expect(e).To(MatchError(ErrForbidden))
	})
})
