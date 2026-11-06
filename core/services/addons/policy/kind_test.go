// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/recovery"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// notesKind is a resource kind that is not an add-on. Its table differs from
// the add-on one on purpose: an editor may change the schema.
const notesKind = "notes:doc"

// notesGrantable are the roles the notes kind grants.
var notesGrantable = []Role{RoleViewer, RoleEditor}

func notesRoles(a Action) []Role {
	switch a {
	case ActionRead, ActionOutput:
		return []Role{RoleViewer, RoleEditor}
	case ActionWrite, ActionSchema:
		return []Role{RoleEditor}
	}
	return nil
}

// notesEligible lets every named account receive a grant of the notes kind.
func notesEligible(context.Context, contract.Actor, string) (bool, error) { return true, nil }

var _ = Describe("Resource kinds", func() {
	bg := context.Background()

	register := func(f *fixture) {
		Expect(f.policy.RegisterKind(notesKind, KindSpec{
			Grantable:       notesGrantable,
			Roles:           notesRoles,
			Eligible:        notesEligible,
			CreateAuthority: func(context.Context, contract.Actor) error { return nil },
			Backing:         func(context.Context, Ref) (bool, error) { return true, nil },
		})).To(Succeed())
	}

	It("grants, lowers and revokes on a kind that is not an add-on", func() {
		f := newFixture(ModeNamedUser)
		f.opts.DrainTimeout = 30 * time.Millisecond
		f.policy = New(f.store, f.opts)
		register(f)
		owner := named("owner")
		ref, err := f.policy.CreateRef(bg, owner, notesKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(ref.Kind).To(Equal(notesKind))

		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleEditor)).To(Succeed())
		// The table of the kind decides: an editor changes the schema here.
		Expect(f.policy.AuthorizeRef(bg, named("bob"), ref, ActionSchema)).To(Succeed())
		Expect(f.policy.AuthorizeRef(bg, named("bob"), ref, ActionShare)).To(MatchError(ErrForbidden))

		// Admitted work of the lowered user is cancelled, and a drain that
		// does not finish within the bound is reported and stays owed.
		entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		cancelled := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			_ = f.policy.AdmitRef(bg, named("bob"), ref, ActionWrite, func(ctx context.Context) error {
				close(entered)
				<-ctx.Done()
				close(cancelled)
				<-release
				return nil
			})
		}()
		<-entered
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleViewer)).To(MatchError(ErrDrainPending))
		<-cancelled
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleViewer)).To(MatchError(ErrDrainPending))
		rec, _ := f.store.get(ref.ResourceID)
		Expect(rec.Epoch).To(Equal(uint64(1)))
		Expect(rec.Drain).To(HaveKey("bob"))
		close(release)
		<-done
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleViewer)).To(Succeed())
		rec, _ = f.store.get(ref.ResourceID)
		Expect(rec.Drain).To(BeEmpty())
		Expect(f.policy.AuthorizeRef(bg, named("bob"), ref, ActionWrite)).To(MatchError(ErrForbidden))
		Expect(f.policy.AuthorizeRef(bg, named("bob"), ref, ActionRead)).To(Succeed())

		Expect(f.policy.RevokeRef(bg, owner, ref, "bob")).To(Succeed())
		Expect(f.policy.AuthorizeRef(bg, named("bob"), ref, ActionRead)).To(MatchError(ErrForbidden))
		rec, _ = f.store.get(ref.ResourceID)
		Expect(rec.Epoch).To(Equal(uint64(2)))

		// The revocation log of the kinds carries the kind, and the add-on
		// proposal broker is never asked about a kind that has no proposals.
		f.store.mu.Lock()
		log := append([]revocation(nil), f.store.doc.RevokedKinds...)
		Expect(f.store.doc.Revoked).To(BeEmpty())
		f.store.mu.Unlock()
		Expect(log).To(HaveLen(1))
		Expect(log[0].Kind).To(Equal(notesKind))
		Expect(f.prop.calls).To(BeEmpty())
	})

	It("denies an unreachable resource like a missing one", func() {
		f := newFixture(ModeNamedUser)
		register(f)
		owner := named("owner")
		ref, err := f.policy.CreateRef(bg, owner, notesKind)
		Expect(err).NotTo(HaveOccurred())
		addonKey := f.create(owner)

		// An ID that does not exist, a kind nobody registered, an add-on
		// kind for a notes resource and a notes kind for an add-on resource.
		Expect(f.policy.AuthorizeRef(bg, owner, Ref{Kind: notesKind, ResourceID: "rmissing"}, ActionRead)).To(MatchError(ErrForbidden))
		Expect(f.policy.AuthorizeRef(bg, owner, Ref{Kind: "notes:ghost", ResourceID: ref.ResourceID}, ActionRead)).To(MatchError(ErrForbidden))
		Expect(f.policy.AuthorizeRef(bg, owner, Ref{Kind: "addon:fixture", ResourceID: ref.ResourceID}, ActionRead)).To(MatchError(ErrForbidden))
		Expect(f.policy.AuthorizeRef(bg, owner, Ref{Kind: notesKind, ResourceID: addonKey.ResourceID}, ActionRead)).To(MatchError(ErrForbidden))
		Expect(f.policy.Authorize(bg, owner, ResourceKey{AddonID: "notes", ResourceID: ref.ResourceID}, ActionRead)).To(MatchError(ErrForbidden))
		Expect(f.policy.AuthorizeRef(bg, named("stranger"), ref, ActionRead)).To(MatchError(ErrForbidden))
		_, err = f.policy.CreateRef(bg, owner, "notes:ghost")
		Expect(err).To(MatchError(ErrForbidden))
		Expect(f.policy.SetGrantRef(bg, owner, Ref{Kind: "notes:ghost", ResourceID: ref.ResourceID}, "bob", RoleViewer)).To(MatchError(ErrForbidden))
	})

	It("registers a kind once and refuses a kind without a table", func() {
		f := newFixture(ModeNamedUser)
		noBacking := func(context.Context, Ref) (bool, error) { return true, nil }
		register(f)
		Expect(f.policy.RegisterKind(notesKind, KindSpec{Grantable: notesGrantable, Roles: notesRoles, Backing: noBacking})).NotTo(Succeed())
		Expect(f.policy.RegisterKind("notes:other", KindSpec{Grantable: notesGrantable, Backing: noBacking})).NotTo(Succeed())
		Expect(f.policy.RegisterKind("", KindSpec{Grantable: notesGrantable, Roles: notesRoles, Backing: noBacking})).NotTo(Succeed())
		// The add-on family is taken by the policy itself.
		Expect(f.policy.RegisterKind("addon:x", KindSpec{Grantable: notesGrantable, Roles: notesRoles, Backing: noBacking})).NotTo(Succeed())
	})

	It("settles a pending resource of the kind through its backing hook", func() {
		f := newFixture(ModeNamedUser)
		answering := false
		Expect(f.policy.RegisterKind(notesKind, KindSpec{
			Grantable: notesGrantable,
			Roles:     notesRoles,
			Backing: func(context.Context, Ref) (bool, error) {
				if !answering {
					return false, ErrUnavailable
				}
				return true, nil
			},
		})).To(Succeed())
		id, release, err := f.policy.newPending(named("owner"), notesKind, "")
		Expect(err).NotTo(HaveOccurred())
		release()
		ref := Ref{Kind: notesKind, ResourceID: id}
		// An add-on existence check is never asked about this kind, and a
		// backing that cannot answer leaves the record pending.
		asked := false
		Expect(f.policy.Reconcile(bg, func(context.Context, ResourceKey) (bool, error) { asked = true; return true, nil })).To(Succeed())
		Expect(asked).To(BeFalse())
		Expect(f.policy.AuthorizeRef(bg, named("owner"), ref, ActionRead)).To(MatchError(ErrForbidden))
		answering = true
		Expect(f.policy.Reconcile(bg, nil)).To(Succeed())
		Expect(f.policy.AuthorizeRef(bg, named("owner"), ref, ActionRead)).To(Succeed())
	})
})

// The file a build before kinds wrote: no kind anywhere, add-on identity only.
const oldPolicyFile = `{
 "version": 1,
 "mode": "named-user",
 "resources": {
  "rold": {"addonId": "fixture", "owner": "owner", "state": "active", "grants": {"bob": "editor", "carol": "editor"}, "epoch": 2},
  "rother": {"addonId": "other", "owner": "owner", "state": "active"}
 },
 "deleted": {"rgone": {"addonId": "fixture", "owner": "owner", "op": "op1", "at": "2026-01-01T00:00:00Z"}},
 "revoked": [
  {"addonId": "fixture", "resourceId": "rold", "userId": "bob", "at": "2026-03-01T00:00:00Z"},
  {"addonId": "fixture", "resourceId": "rold", "userId": "carol", "ceiling": "viewer", "at": "2026-03-01T00:00:00Z"},
  {"addonId": "other", "resourceId": "rother", "userId": "bob", "at": "2026-03-01T00:00:00Z"}
 ]
}`

var _ = Describe("Migration of a store written before kinds", func() {
	bg := context.Background()

	It("gives records, tombstones and the revocation log the add-on kind and rewrites the file", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "policy")
		Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, fileName), []byte(oldPolicyFile), 0o600)).To(Succeed())
		s, err := OpenStore(dir, ModeNamedUser)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		r, ok := s.get("rold")
		Expect(ok).To(BeTrue())
		Expect(r.Kind).To(Equal("addon:fixture"))
		Expect(r.AddonID).To(Equal("fixture"))
		Expect(r.Grants).To(HaveLen(2))
		Expect(r.Epoch).To(Equal(uint64(2)))
		o, _ := s.get("rother")
		Expect(o.Kind).To(Equal("addon:other"))
		s.mu.Lock()
		Expect(s.doc.Deleted["rgone"].Kind).To(Equal("addon:fixture"))
		Expect(s.doc.Revoked).To(HaveLen(3))
		for _, v := range s.doc.Revoked {
			Expect(v.Kind).To(HavePrefix("addon:"))
			Expect(v.AddonID).To(BeEmpty())
		}
		s.mu.Unlock()

		// The new form is on disk, so the next open reads it unchanged.
		raw, err := os.ReadFile(filepath.Join(dir, fileName))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"kind":"addon:fixture"`))
		Expect(s.Close()).To(Succeed())
		again, err := OpenStore(dir, ModeNamedUser)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = again.Close() })
		r2, _ := again.get("rold")
		Expect(r2).To(Equal(r))

		// And the add-on API reaches the migrated record.
		f := &fixture{accounts: &account{blocked: map[string]bool{}}}
		p := New(again, Options{Revalidate: f.accounts.check, DrainTimeout: time.Second})
		Expect(p.Authorize(bg, named("bob"), ResourceKey{AddonID: "fixture", ResourceID: "rold"}, ActionWrite)).To(Succeed())
		Expect(p.Authorize(bg, named("bob"), ResourceKey{AddonID: "other", ResourceID: "rold"}, ActionWrite)).To(MatchError(ErrForbidden))
	})

	It("still applies a lowering to a restore from a backup older than it", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "policy")
		Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, fileName), []byte(oldPolicyFile), 0o600)).To(Succeed())
		s, err := OpenStore(dir, ModeNamedUser)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = s.Close() })

		// The backup is older than the lowerings of 2026-03-01.
		snap := addonSnapshot{Version: 1, AddonID: "fixture", Mode: ModeNamedUser, Created: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
			Resources: map[string]snapshotResource{"rold": {Owner: "owner", Grants: map[string]Role{"bob": RoleEditor, "carol": RoleEditor, "dave": RoleEditor}}}}
		rep, err := s.restoreAddon(bg, snap, recovery.RestoreOptions{
			Since:   snap.Created,
			Account: func(context.Context, string) error { return nil },
		})
		Expect(err).NotTo(HaveOccurred())
		got, _ := s.get("rold")
		Expect(got.Kind).To(Equal("addon:fixture"))
		Expect(got.Grants).NotTo(HaveKey("bob"))
		Expect(got.Grants["carol"]).To(Equal(RoleViewer))
		Expect(got.Grants["dave"]).To(Equal(RoleEditor))
		Expect(rep.Revoked).To(Equal(2))
		// The lowering of another add-on does not touch this one.
		o, _ := s.get("rother")
		Expect(o.Kind).To(Equal("addon:other"))
	})
})
