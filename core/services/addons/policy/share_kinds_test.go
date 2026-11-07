// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// boardKind is a kind with the use and manage roles. A person with use may
// read and run it, a person with manage may also change it, and only the
// owner shares or deletes it.
const boardKind = "board:item"

func boardRoles(a Action) []Role {
	switch a {
	case ActionRead, ActionOutput:
		return []Role{RoleUse, RoleManage}
	case ActionWrite:
		return []Role{RoleManage}
	}
	return nil
}

// boardSpec is the registration the tests start from. eligible decides who
// may receive a grant.
func boardSpec(eligible func(context.Context, contract.Actor, string) (bool, error)) KindSpec {
	return KindSpec{
		Grantable:       []Role{RoleUse, RoleManage},
		Roles:           boardRoles,
		Everyone:        true,
		Eligible:        eligible,
		CreateAuthority: func(context.Context, contract.Actor) error { return nil },
		Backing:         func(context.Context, Ref) (bool, error) { return true, nil },
	}
}

func anyone(context.Context, contract.Actor, string) (bool, error) { return true, nil }

var _ = Describe("Roles as capability sets", func() {
	bg := context.Background()

	setup := func() (*fixture, Ref) {
		f := newFixture(ModeNamedUser)
		f.opts.DrainTimeout = 2 * time.Second
		f.policy = New(f.store, f.opts)
		Expect(f.policy.RegisterKind(boardKind, boardSpec(anyone))).To(Succeed())
		ref, err := f.policy.CreateRef(bg, named("owner"), boardKind)
		Expect(err).NotTo(HaveOccurred())
		return f, ref
	}

	It("runs each action only for the roles the table of the kind lists", func() {
		f, ref := setup()
		owner := named("owner")
		Expect(f.policy.SetGrantRef(bg, owner, ref, "user-u", RoleUse)).To(Succeed())
		Expect(f.policy.SetGrantRef(bg, owner, ref, "user-m", RoleManage)).To(Succeed())
		can := func(id string, a Action) bool { return f.policy.AuthorizeRef(bg, named(id), ref, a) == nil }
		for _, a := range []Action{ActionRead, ActionOutput, ActionWrite, ActionSchema, ActionShare, ActionDelete, ActionResolve, ActionUnknown} {
			Expect(can("owner", a)).To(BeTrue(), "owner %s", a)
		}
		Expect(can("user-u", ActionRead)).To(BeTrue())
		Expect(can("user-u", ActionWrite)).To(BeFalse())
		Expect(can("user-m", ActionWrite)).To(BeTrue())
		for _, a := range []Action{ActionSchema, ActionShare, ActionDelete, ActionResolve, ActionUnknown} {
			Expect(can("user-m", a)).To(BeFalse(), "manage %s", a)
		}
		Expect(can("stranger", ActionRead)).To(BeFalse())
		// The roles of another kind, and the owner, cannot be granted here.
		for _, r := range []Role{RoleViewer, RoleEditor, RoleOwner, "", "admin"} {
			Expect(f.policy.SetGrantRef(bg, owner, ref, "user-x", r)).To(MatchError(ErrForbidden), "role %q", r)
		}
		Expect(f.policy.SetGrantRef(bg, owner, ref, "owner", RoleUse)).To(MatchError(ErrForbidden))
	})

	It("cancels the work a lowering takes away and keeps the reads it leaves", func() {
		f, ref := setup()
		owner := named("owner")
		Expect(f.policy.SetGrantRef(bg, owner, ref, "user-m", RoleManage)).To(Succeed())
		write, read := runHeld(f, named("user-m"), ref, ActionWrite), runHeld(f, named("user-m"), ref, ActionRead)
		Expect(f.policy.SetGrantRef(bg, owner, ref, "user-m", RoleUse)).To(Succeed())
		Expect(write.cancelled()).To(BeTrue())
		Expect(read.cancelled()).To(BeFalse())
		read.finish()
		rec, _ := f.store.get(ref.ResourceID)
		Expect(rec.Epoch).To(Equal(uint64(1)))
		Expect(rec.Drain).To(BeEmpty())
		// Raising a role takes nothing away and starts no epoch.
		Expect(f.policy.SetGrantRef(bg, owner, ref, "user-m", RoleManage)).To(Succeed())
		rec, _ = f.store.get(ref.ResourceID)
		Expect(rec.Epoch).To(Equal(uint64(1)))
	})

	It("keeps the add-on roles: viewer and editor only, as before", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		Expect(f.policy.SetGrant(bg, owner, key, "bob", RoleUse)).To(MatchError(ErrForbidden))
		Expect(f.policy.SetGrant(bg, owner, key, "bob", RoleManage)).To(MatchError(ErrForbidden))
		Expect(f.policy.SetGrant(bg, owner, key, Everyone, RoleViewer)).To(MatchError(ErrForbidden))
		Expect(f.policy.SetGrant(bg, owner, key, "bob", RoleEditor)).To(Succeed())
		Expect(allowed(f, named("bob"), key, ActionWrite)).To(BeTrue())
		Expect(allowed(f, named("bob"), key, ActionSchema)).To(BeFalse())
		// Revoking a principal that holds nothing is the no-op removal it was.
		Expect(f.policy.Revoke(bg, owner, key, Everyone)).To(Succeed())
		Expect(allowed(f, named("bob"), key, ActionWrite)).To(BeTrue())
	})
})

var _ = Describe("Role tables with the new roles", func() {
	bg := context.Background()
	backing := func(context.Context, Ref) (bool, error) { return true, nil }

	DescribeTable("refuses at registration",
		func(kind string, spec KindSpec) {
			f := newFixture(ModeNamedUser)
			spec.Backing = backing
			Expect(f.policy.RegisterKind(kind, spec)).NotTo(Succeed())
		},
		Entry("a table that gives a role the kind does not grant", "board:bad",
			KindSpec{Grantable: []Role{RoleUse}, Roles: func(Action) []Role { return []Role{RoleManage} }}),
		Entry("a table that gives the empty role", "board:bad",
			KindSpec{Grantable: []Role{RoleUse}, Roles: func(Action) []Role { return []Role{""} }}),
		Entry("a table that gives an unknown role", "board:bad",
			KindSpec{Grantable: []Role{RoleUse}, Roles: func(Action) []Role { return []Role{"admin"} }}),
		Entry("a table that lists the owner, who is implicit", "board:bad",
			KindSpec{Grantable: []Role{RoleUse}, Roles: func(Action) []Role { return []Role{RoleOwner} }}),
		Entry("a grantable owner role", "board:bad",
			KindSpec{Grantable: []Role{RoleUse, RoleOwner}, Roles: boardRoles}),
		Entry("a grantable unknown role", "board:bad",
			KindSpec{Grantable: []Role{RoleUse, "admin"}, Roles: boardRoles}),
		Entry("no grantable role", "board:bad",
			KindSpec{Roles: boardRoles}),
		Entry("no table", "board:bad",
			KindSpec{Grantable: []Role{RoleUse}}),
		Entry("a kind name that cannot be part of a path", "Board/Item",
			KindSpec{Grantable: []Role{RoleUse}, Roles: func(Action) []Role { return nil }}),
	)

	It("denies a stored role the kind does not grant, and a table injected to return an unknown role", func() {
		f := newFixture(ModeNamedUser)
		Expect(f.policy.RegisterKind(boardKind, boardSpec(anyone))).To(Succeed())
		ref, err := f.policy.CreateRef(bg, named("owner"), boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.store.mutate(func(d *document) error {
			d.Resources[ref.ResourceID].Grants["user-v"] = RoleViewer
			return nil
		})).To(Succeed())
		Expect(f.policy.AuthorizeRef(bg, named("user-v"), ref, ActionRead)).To(MatchError(ErrForbidden))

		Expect(f.policy.SetGrantRef(bg, named("owner"), ref, "user-u", RoleUse)).To(Succeed())
		f.policy.mu.Lock()
		spec := f.policy.kinds[boardKind]
		spec.Roles = func(Action) []Role { return []Role{"admin", ""} }
		f.policy.kinds[boardKind] = spec
		f.policy.mu.Unlock()
		Expect(f.policy.AuthorizeRef(bg, named("user-u"), ref, ActionRead)).To(MatchError(ErrForbidden))
		Expect(f.policy.AuthorizeRef(bg, named("stranger"), ref, ActionRead)).To(MatchError(ErrForbidden))
		Expect(f.policy.AuthorizeRef(bg, named("owner"), ref, ActionShare)).To(Succeed())
	})
})

var _ = Describe("Grants to everyone", func() {
	bg := context.Background()

	setup := func() (*fixture, Ref) {
		f := newFixture(ModeNamedUser)
		f.opts.DrainTimeout = 2 * time.Second
		f.policy = New(f.store, f.opts)
		Expect(f.policy.RegisterKind(boardKind, boardSpec(anyone))).To(Succeed())
		ref, err := f.policy.CreateRef(bg, named("owner"), boardKind)
		Expect(err).NotTo(HaveOccurred())
		return f, ref
	}

	It("reaches every named account, later ones included, and lowers and revokes with a drain", func() {
		f, ref := setup()
		owner := named("owner")
		Expect(f.policy.SetGrantRef(bg, owner, ref, Everyone, RoleManage)).To(Succeed())
		// Nobody was granted by name: any named account, even one created
		// after the grant, holds the role.
		Expect(f.policy.AuthorizeRef(bg, named("signed-up-later"), ref, ActionWrite)).To(Succeed())
		Expect(f.policy.AuthorizeRef(bg, named("signed-up-later"), ref, ActionShare)).To(MatchError(ErrForbidden))

		// bob also holds manage by name; carol only through everyone. Writes
		// run one at a time, so each phase holds one write.
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleManage)).To(Succeed())
		ownerRead := runHeld(f, owner, ref, ActionRead)
		carol, bobRead := runHeld(f, named("carol"), ref, ActionWrite), runHeld(f, named("bob"), ref, ActionRead)

		// Lowering everyone stops the writes it alone allowed.
		Expect(f.policy.SetGrantRef(bg, owner, ref, Everyone, RoleUse)).To(Succeed())
		Expect(carol.cancelled()).To(BeTrue())
		Expect(bobRead.cancelled()).To(BeFalse())
		Expect(f.policy.AuthorizeRef(bg, named("carol"), ref, ActionWrite)).To(MatchError(ErrForbidden))
		Expect(f.policy.AuthorizeRef(bg, named("carol"), ref, ActionRead)).To(Succeed())
		Expect(f.policy.AuthorizeRef(bg, named("bob"), ref, ActionWrite)).To(Succeed())
		bobRead.finish()

		// Revoking everyone stops carol's read; bob's own grant stays, and so
		// does the write it allows.
		bob, carolRead := runHeld(f, named("bob"), ref, ActionWrite), runHeld(f, named("carol"), ref, ActionRead)
		Expect(f.policy.RevokeRef(bg, owner, ref, Everyone)).To(Succeed())
		Expect(carolRead.cancelled()).To(BeTrue())
		Expect(bob.cancelled()).To(BeFalse())
		Expect(ownerRead.cancelled()).To(BeFalse())
		Expect(f.policy.AuthorizeRef(bg, named("carol"), ref, ActionRead)).To(MatchError(ErrForbidden))
		Expect(f.policy.AuthorizeRef(bg, named("bob"), ref, ActionWrite)).To(Succeed())
		bob.finish()
		ownerRead.finish()

		rec, _ := f.store.get(ref.ResourceID)
		Expect(rec.Epoch).To(Equal(uint64(2)))
		Expect(rec.Drain).To(BeEmpty())
		Expect(rec.Grants).To(Equal(map[string]Role{"bob": RoleManage}))
		f.store.mu.Lock()
		log := append([]revocation(nil), f.store.doc.RevokedKinds...)
		f.store.mu.Unlock()
		Expect(log).To(HaveLen(1))
		Expect(log[0].UserID).To(Equal(Everyone))
		Expect(log[0].Ceiling).To(BeEmpty())
	})

	It("keeps a per-user grant and the grant to everyone apart", func() {
		f, ref := setup()
		owner := named("owner")
		Expect(f.policy.SetGrantRef(bg, owner, ref, Everyone, RoleUse)).To(Succeed())
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleManage)).To(Succeed())
		// The higher role wins.
		Expect(f.policy.AuthorizeRef(bg, named("bob"), ref, ActionWrite)).To(Succeed())

		// Revoking bob leaves him what everyone holds; his read keeps running.
		read, write := runHeld(f, named("bob"), ref, ActionRead), runHeld(f, named("bob"), ref, ActionWrite)
		Expect(f.policy.RevokeRef(bg, owner, ref, "bob")).To(Succeed())
		Expect(write.cancelled()).To(BeTrue())
		Expect(read.cancelled()).To(BeFalse())
		read.finish()
		Expect(f.policy.AuthorizeRef(bg, named("bob"), ref, ActionRead)).To(Succeed())
		Expect(f.policy.AuthorizeRef(bg, named("bob"), ref, ActionWrite)).To(MatchError(ErrForbidden))

		grants, err := f.policy.GrantsRef(bg, owner, ref)
		Expect(err).NotTo(HaveOccurred())
		Expect(grants).To(Equal(map[string]Role{Everyone: RoleUse}))
	})

	It("is refused on a kind that does not allow it, and is no account", func() {
		f := newFixture(ModeNamedUser)
		spec := boardSpec(anyone)
		spec.Everyone = false
		Expect(f.policy.RegisterKind(boardKind, spec)).To(Succeed())
		ref, err := f.policy.CreateRef(bg, named("owner"), boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.policy.SetGrantRef(bg, named("owner"), ref, Everyone, RoleUse)).To(MatchError(ErrForbidden))
		// A grant to everyone left in the store by an older registration is
		// not honored once the kind stops allowing it.
		Expect(f.store.mutate(func(d *document) error {
			d.Resources[ref.ResourceID].Grants[Everyone] = RoleUse
			return nil
		})).To(Succeed())
		Expect(f.policy.AuthorizeRef(bg, named("carol"), ref, ActionRead)).To(MatchError(ErrForbidden))
		// Nobody can act as the principal.
		Expect(f.policy.AuthorizeRef(bg, contract.Actor{UserID: Everyone, CredentialID: "key:x", Mode: "named-user"}, ref, ActionRead)).To(MatchError(ErrForbidden))
	})
})

var _ = Describe("Who may receive a grant", func() {
	bg := context.Background()

	It("asks the kind about the actor and the target and denies without a rule", func() {
		f := newFixture(ModeNamedUser)
		var asked []string
		down := false
		Expect(f.policy.RegisterKind(boardKind, boardSpec(func(_ context.Context, a contract.Actor, user string) (bool, error) {
			asked = append(asked, a.UserID+">"+user)
			if down {
				return false, errors.New("directory down")
			}
			return user == "bob", nil
		}))).To(Succeed())
		noRule := boardSpec(nil)
		Expect(f.policy.RegisterKind("board:closed", noRule)).To(Succeed())
		owner := named("owner")
		ref, err := f.policy.CreateRef(bg, owner, boardKind)
		Expect(err).NotTo(HaveOccurred())
		closed, err := f.policy.CreateRef(bg, owner, "board:closed")
		Expect(err).NotTo(HaveOccurred())

		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
		Expect(f.policy.SetGrantRef(bg, owner, ref, "carol", RoleUse)).To(MatchError(ErrForbidden))
		Expect(asked).To(Equal([]string{"owner>bob", "owner>carol"}))
		// A stranger is refused before the rule is asked.
		Expect(f.policy.SetGrantRef(bg, named("stranger"), ref, "bob", RoleUse)).To(MatchError(ErrForbidden))
		Expect(asked).To(HaveLen(2))
		down = true
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleManage)).To(MatchError(ErrUnavailable))
		ok, err := f.policy.EligibleRef(bg, owner, ref, "bob")
		Expect(err).To(HaveOccurred())
		Expect(ok).To(BeFalse())

		Expect(f.policy.SetGrantRef(bg, owner, closed, "bob", RoleUse)).To(MatchError(ErrForbidden))
		// Everyone is the kind's own switch, not a person the rule judges.
		Expect(f.policy.SetGrantRef(bg, owner, closed, Everyone, RoleUse)).To(Succeed())
	})
})

var _ = Describe("Sharing listing of a kind", func() {
	bg := context.Background()

	It("lists what the actor owns and what was shared, with the actor's role", func() {
		f := newFixture(ModeNamedUser)
		Expect(f.policy.RegisterKind(boardKind, boardSpec(anyone))).To(Succeed())
		owner := named("owner")
		mine, err := f.policy.CreateRef(bg, owner, boardKind)
		Expect(err).NotTo(HaveOccurred())
		open, err := f.policy.CreateRef(bg, owner, boardKind)
		Expect(err).NotTo(HaveOccurred())
		_, err = f.policy.CreateRef(bg, named("other"), boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.policy.SetGrantRef(bg, owner, mine, "bob", RoleManage)).To(Succeed())
		Expect(f.policy.SetGrantRef(bg, owner, open, Everyone, RoleUse)).To(Succeed())
		Expect(f.policy.SetGrantRef(bg, owner, open, "bob", RoleUse)).To(Succeed())
		f.create(owner)

		views, err := f.policy.ListingKind(bg, owner, boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(views).To(HaveLen(2))
		for _, v := range views {
			Expect(v.Role).To(Equal(RoleOwner))
			Expect(v.Shared).To(Equal(map[string]int{mine.ResourceID: 1, open.ResourceID: 2}[v.Ref.ResourceID]))
		}
		views, err = f.policy.ListingKind(bg, named("bob"), boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(views).To(HaveLen(2))
		byID := map[string]ShareView{}
		for _, v := range views {
			byID[v.Ref.ResourceID] = v
		}
		Expect(byID[mine.ResourceID].Role).To(Equal(RoleManage))
		Expect(byID[mine.ResourceID].OwnerID).To(Equal("owner"))
		Expect(byID[open.ResourceID].Role).To(Equal(RoleUse))
		Expect(byID[open.ResourceID].Everyone).To(BeFalse())
		views, err = f.policy.ListingKind(bg, named("carol"), boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(views).To(HaveLen(1))
		Expect(views[0].Ref).To(Equal(open))
		Expect(views[0].Role).To(Equal(RoleUse))
		Expect(views[0].Everyone).To(BeTrue())

		_, err = f.policy.ListingKind(bg, owner, "board:ghost")
		Expect(err).To(MatchError(ErrForbidden))
		_, err = f.policy.ListingKind(bg, owner, "addon:fixture")
		Expect(err).To(MatchError(ErrForbidden))
	})
})

// fakeProvider registers the kinds it was given.
type fakeProvider map[string]KindSpec

func (p fakeProvider) RegisterKinds(r KindRegistrar) error {
	for k, s := range p {
		if err := r.RegisterKind(k, s); err != nil {
			return err
		}
	}
	return nil
}

var _ = Describe("Kind providers", func() {
	It("registers the kinds of every provider and stops at the first error", func() {
		f := newFixture(ModeNamedUser)
		Expect(RegisterKinds(f.policy, fakeProvider{boardKind: boardSpec(anyone)})).To(Succeed())
		rules, ok := f.policy.Shareable(boardKind)
		Expect(ok).To(BeTrue())
		Expect(rules.Grantable).To(Equal([]Role{RoleUse, RoleManage}))
		Expect(rules.Everyone).To(BeTrue())
		Expect(RegisterKinds(f.policy, fakeProvider{boardKind: boardSpec(anyone)})).NotTo(Succeed())
		Expect(RegisterKinds(f.policy, nil)).NotTo(Succeed())
		_, ok = f.policy.Shareable("addon:fixture")
		Expect(ok).To(BeFalse())
		_, ok = f.policy.Shareable("board:ghost")
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("Store of the new roles", func() {
	It("loads use, manage and everyone on a kind and refuses them on an add-on", func() {
		load := func(doc string) error {
			dir := filepath.Join(GinkgoT().TempDir(), "policy")
			Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(dir, fileName), []byte(doc), 0o600)).To(Succeed())
			s, err := OpenStore(dir, ModeNamedUser)
			if err == nil {
				_ = s.Close()
			}
			return err
		}
		Expect(load(`{"version":1,"mode":"named-user","resources":{"r":{"kind":"board:item","owner":"o","state":"active","grants":{"u":"use","@everyone":"manage"}}},` +
			`"revoked":[{"kind":"board:item","resourceId":"r","userId":"@everyone","ceiling":"use","at":"2026-01-01T00:00:00Z"}]}`)).To(Succeed())
		Expect(load(`{"version":1,"mode":"named-user","resources":{"r":{"kind":"addon:a","addonId":"a","owner":"o","state":"active","grants":{"u":"use"}}}}`)).NotTo(Succeed())
		Expect(load(`{"version":1,"mode":"named-user","resources":{"r":{"kind":"board:item","owner":"o","state":"active","grants":{"u":"admin"}}}}`)).NotTo(Succeed())
		Expect(load(`{"version":1,"mode":"named-user","resources":{"r":{"kind":"board:item","owner":"o","state":"active","grants":{"u":"owner"}}}}`)).NotTo(Succeed())
	})
})

// held is admitted work that waits until it is cancelled or finished.
type held struct {
	stop     chan struct{}
	canceled chan struct{}
	done     chan struct{}
}

func (h *held) cancelled() bool {
	select {
	case <-h.canceled:
		return true
	default:
		return false
	}
}

func (h *held) finish() {
	select {
	case <-h.stop:
	default:
		close(h.stop)
	}
	<-h.done
}

// runHeld admits work for the actor and returns once it runs. The work ends
// when it is cancelled or finished.
func runHeld(f *fixture, actor contract.Actor, ref Ref, action Action) *held {
	h := &held{stop: make(chan struct{}), canceled: make(chan struct{}), done: make(chan struct{})}
	entered := make(chan struct{})
	go func() {
		defer GinkgoRecover()
		defer close(h.done)
		err := f.policy.AdmitRef(context.Background(), actor, ref, action, func(ctx context.Context) error {
			close(entered)
			select {
			case <-ctx.Done():
				close(h.canceled)
			case <-h.stop:
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
	}()
	Eventually(entered).Should(BeClosed())
	return h
}
