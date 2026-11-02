// SPDX-License-Identifier: MIT
package hostsettings

import (
	"context"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/egresspolicy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Wildcard host policy validation", func() {
	It("accepts patterns only in AllowedHosts and preserves policy strings across replay and reopen", func() {
		ctx := context.Background()
		path := filepath.Join(GinkgoT().TempDir(), "policy.json")
		migration := legacy()
		migration.Candidates[0].AllowedHosts = "API.Example.COM., 93.184.216.34"
		s, err := Open(path, migration)
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(s.Close()).To(Succeed()) }()
		original, err := s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(original.Policy).To(Equal(migration.Candidates[0]))
		raw := "*.BÜCHER.Example., *:8443, API.Example.COM."
		patch := Patch{AllowedHosts: ptr(raw)}
		snap, err := s.Update(ctx, patch, 1, "patterns")
		Expect(err).NotTo(HaveOccurred())
		// Policy and receipt strings are intentionally lossless, as for existing
		// exact entries. Consumers use the shared parser for canonical matching.
		Expect(snap.Policy.AllowedHosts).To(Equal(raw))
		entries, err := egresspolicy.ParseEgressAllowedHosts(snap.Policy.AllowedHosts, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(Equal([]egresspolicy.EgressAllowedHost{{Host: "*.xn--bcher-kva.example"}, {Host: "*", Port: 8443}, {Host: "api.example.com"}}))
		for _, p := range []string{"*", "*.example.com"} {
			_, err = s.Update(ctx, Patch{ExtraRegistries: ptr(p)}, 2, "extra-"+p)
			Expect(err).To(MatchError(ContainSubstring(ErrInvalid.Error())))
		}
		after, err := s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(Equal(snap))
		Expect(s.Close()).To(Succeed())
		s, err = Open(path, nil)
		Expect(err).NotTo(HaveOccurred())
		reopened, err := s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(reopened).To(Equal(snap))
		replay, err := s.Update(ctx, patch, 1, "patterns")
		Expect(err).NotTo(HaveOccurred())
		Expect(replay).To(Equal(snap))
		_, err = s.Update(ctx, Patch{AllowedHosts: ptr("*.xn--bcher-kva.example,*:8443,api.example.com")}, 1, "patterns")
		Expect(err).To(MatchError(ErrOperationMismatch))
	})
})
