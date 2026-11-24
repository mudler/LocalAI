// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var guardActions = []Action{ActionRead, ActionOutput, ActionWrite, ActionSchema, ActionShare, ActionDelete, ActionResolve, ActionUnknown}

var _ = Describe("Kind role tables", func() {
	bg := context.Background()
	noBacking := func(context.Context, Ref) (bool, error) { return true, nil }

	It("refuses at registration a table that returns no valid role for some action", func() {
		for _, hole := range guardActions {
			f := newFixture(ModeNamedUser)
			err := f.policy.RegisterKind("notes:holey", KindSpec{
				Backing:   noBacking,
				Grantable: notesGrantable,
				Roles: func(a Action) []Role {
					if a == hole {
						return []Role{""}
					}
					return notesRoles(a)
				},
			})
			Expect(err).To(HaveOccurred(), "hole at %q", hole)
		}
		f := newFixture(ModeNamedUser)
		Expect(f.policy.RegisterKind("notes:odd", KindSpec{Backing: noBacking, Grantable: notesGrantable, Roles: func(Action) []Role { return []Role{"admin"} }})).NotTo(Succeed())
	})

	It("denies a caller with no grant even if a table that returns no role is injected", func() {
		f := newFixture(ModeNamedUser)
		Expect(f.policy.RegisterKind(notesKind, KindSpec{
			Grantable:       notesGrantable,
			Roles:           notesRoles,
			Backing:         noBacking,
			CreateAuthority: func(context.Context, contract.Actor) error { return nil },
		})).To(Succeed())
		ref, err := f.policy.CreateRef(bg, named("owner"), notesKind)
		Expect(err).NotTo(HaveOccurred())
		f.policy.mu.Lock()
		f.policy.kinds[notesKind] = KindSpec{Grantable: notesGrantable, Roles: func(Action) []Role { return []Role{""} }}
		f.policy.mu.Unlock()
		Expect(f.policy.AuthorizeRef(bg, named("stranger"), ref, ActionRead)).To(MatchError(ErrForbidden))
		Expect(f.policy.AuthorizeRef(bg, named("stranger"), ref, ActionUnknown)).To(MatchError(ErrForbidden))
	})

	It("refuses a kind without a backing check", func() {
		f := newFixture(ModeNamedUser)
		Expect(f.policy.RegisterKind(notesKind, KindSpec{Grantable: notesGrantable, Roles: notesRoles})).NotTo(Succeed())
	})

	It("does not count a pending record of an unregistered kind against the quota", func() {
		f := newFixture(ModeNamedUser)
		Expect(f.store.mutate(func(d *document) error {
			for i := 0; i < DefaultMaxPendingPerUser; i++ {
				d.Resources["rghost"+string(rune('a'+i))] = &record{Kind: "gone:kind", Owner: "owner", State: statePend, Grants: map[string]Role{}}
			}
			return nil
		})).To(Succeed())
		Expect(f.policy.Reconcile(bg, nil)).To(Succeed())
		_, err := f.policy.Register(bg, named("owner"), "fixture")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.store.pending()).To(HaveLen(DefaultMaxPendingPerUser + 1))
	})
})

var _ = Describe("Export of an add-on", func() {
	It("never reports a resource that is not of the add-on", func() {
		f := newFixture(ModeNamedUser)
		Expect(f.policy.RegisterKind(notesKind, KindSpec{
			Grantable:       notesGrantable,
			Roles:           notesRoles,
			Backing:         func(context.Context, Ref) (bool, error) { return true, nil },
			CreateAuthority: func(context.Context, contract.Actor) error { return nil },
		})).To(Succeed())
		ref, err := f.policy.CreateRef(context.Background(), named("owner"), notesKind)
		Expect(err).NotTo(HaveOccurred())
		key := f.create(named("owner"))

		snap := f.store.exportAddon("", func() time.Time { return time.Now() })
		Expect(snap.Resources).To(BeEmpty())
		snap = f.store.exportAddon("other", func() time.Time { return time.Now() })
		Expect(snap.Resources).To(BeEmpty())
		snap = f.store.exportAddon(key.AddonID, func() time.Time { return time.Now() })
		Expect(snap.Resources).To(HaveLen(1))
		Expect(snap.Resources).NotTo(HaveKey(ref.ResourceID))

		port := &RecoveryPort{Policy: f.policy}
		_, err = port.Export(context.Background(), "")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Store validation of kinds", func() {
	open := func(doc string) error {
		dir := filepath.Join(GinkgoT().TempDir(), "policy")
		Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, fileName), []byte(doc), 0o600)).To(Succeed())
		s, err := OpenStore(dir, ModeNamedUser)
		if err == nil {
			_ = s.Close()
		}
		return err
	}
	wrap := func(section string) string {
		return `{"version":1,"mode":"named-user","resources":{},` + section + `}`
	}

	DescribeTable("rejects as corrupt",
		func(doc string) { Expect(open(doc)).To(HaveOccurred()) },
		Entry("a revocation whose kind and add-on disagree",
			wrap(`"revoked":[{"kind":"addon:a","addonId":"b","resourceId":"r","userId":"u","at":"2026-01-01T00:00:00Z"}]`)),
		Entry("a revocation of another kind that names an add-on",
			wrap(`"revoked":[{"kind":"notes:doc","addonId":"b","resourceId":"r","userId":"u","at":"2026-01-01T00:00:00Z"}]`)),
		Entry("a revocation of the kind addon: with no ID",
			wrap(`"revoked":[{"kind":"addon:","resourceId":"r","userId":"u","at":"2026-01-01T00:00:00Z"}]`)),
		Entry("a tombstone whose kind and add-on disagree",
			wrap(`"deleted":{"r":{"kind":"addon:a","addonId":"b","owner":"o","op":"x","at":"2026-01-01T00:00:00Z"}}`)),
		Entry("a tombstone of the kind addon: with no ID",
			wrap(`"deleted":{"r":{"kind":"addon:","owner":"o","op":"x","at":"2026-01-01T00:00:00Z"}}`)),
		Entry("a record of the kind addon: with no ID",
			`{"version":1,"mode":"named-user","resources":{"r":{"kind":"addon:","owner":"o","state":"active"}}}`),
	)

	It("accepts a well-formed new revocation and tombstone", func() {
		Expect(open(wrap(`"revoked":[{"kind":"addon:a","resourceId":"r","userId":"u","at":"2026-01-01T00:00:00Z"}],"deleted":{"r":{"kind":"addon:a","addonId":"a","owner":"o","op":"x","at":"2026-01-01T00:00:00Z"}}`))).To(Succeed())
	})
})
