// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPolicy(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Addon policy") }

func named(id string) contract.Actor {
	return contract.Actor{UserID: id, CredentialID: "key:" + id, Mode: "named-user"}
}

type account struct {
	mu      sync.Mutex
	blocked map[string]bool
}

func (a *account) check(_ context.Context, actor contract.Actor) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.blocked[actor.UserID] {
		return errors.New("account paused")
	}
	return nil
}
func (a *account) block(id string) { a.mu.Lock(); a.blocked[id] = true; a.mu.Unlock() }

type proposals struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (p *proposals) InvalidateProposals(_ context.Context, k ResourceKey, user string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, k.ResourceID+"/"+user)
	return p.err
}

type fixture struct {
	dir      string
	store    *Store
	policy   *Policy
	accounts *account
	prop     *proposals
	opts     Options
}

func open(mode Mode, dir string) *fixture {
	f := &fixture{dir: dir, accounts: &account{blocked: map[string]bool{}}, prop: &proposals{}}
	store, err := OpenStore(dir, mode)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = store.Close() })
	f.store = store
	f.opts = Options{
		Revalidate:      f.accounts.check,
		CreateAuthority: func(context.Context, contract.Actor, string) error { return nil },
		Proposals:       f.prop,
		DrainTimeout:    2 * time.Second,
	}
	f.policy = New(store, f.opts)
	return f
}

func newFixture(mode Mode) *fixture { return open(mode, filepath.Join(GinkgoT().TempDir(), "policy")) }

// create registers and commits a resource owned by actor.
func (f *fixture) create(actor contract.Actor) ResourceKey { return f.create2(f.policy, actor) }

func (f *fixture) create2(p *Policy, actor contract.Actor) ResourceKey {
	key, err := p.Register(context.Background(), actor, "fixture")
	Expect(err).NotTo(HaveOccurred())
	Expect(p.Commit(context.Background(), actor, key)).To(Succeed())
	return key
}

func allowed(f *fixture, a contract.Actor, k ResourceKey, action Action) bool {
	return f.policy.Authorize(context.Background(), a, k, action) == nil
}

var _ = Describe("roles", func() {
	It("enforces the viewer editor owner matrix on fixture resources", func() {
		f := newFixture(ModeNamedUser)
		owner, editor, viewer, other := named("owner"), named("editor"), named("viewer"), named("other")
		key := f.create(owner)
		Expect(f.policy.SetGrant(context.Background(), owner, key, "editor", RoleEditor)).To(Succeed())
		Expect(f.policy.SetGrant(context.Background(), owner, key, "viewer", RoleViewer)).To(Succeed())
		matrix := []struct {
			name   string
			actor  contract.Actor
			action Action
			want   bool
		}{
			{"owner reads", owner, ActionRead, true}, {"owner writes", owner, ActionWrite, true},
			{"owner changes schema", owner, ActionSchema, true}, {"owner shares", owner, ActionShare, true},
			{"editor reads", editor, ActionRead, true}, {"editor outputs", editor, ActionOutput, true},
			{"editor writes", editor, ActionWrite, true}, {"editor changes schema", editor, ActionSchema, false},
			{"editor shares", editor, ActionShare, false}, {"editor deletes", editor, ActionDelete, false},
			{"viewer reads", viewer, ActionRead, true}, {"viewer outputs", viewer, ActionOutput, true},
			{"viewer writes", viewer, ActionWrite, false}, {"viewer changes schema", viewer, ActionSchema, false},
			{"viewer shares", viewer, ActionShare, false},
			{"other reads", other, ActionRead, false}, {"other writes", other, ActionWrite, false},
			{"unknown action needs the owner", editor, ActionUnknown, false},
			{"unknown action by owner", owner, ActionUnknown, true},
		}
		for _, row := range matrix {
			Expect(allowed(f, row.actor, key, row.action)).To(Equal(row.want), row.name)
		}
		err := f.policy.SetGrant(context.Background(), editor, key, "other", RoleViewer)
		Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue())
		Expect(allowed(f, other, key, ActionRead)).To(BeFalse())
	})

	It("denies installation administrators and unrelated users any access to user data", func() {
		f := newFixture(ModeNamedUser)
		// The host account check passes for administrators: the policy has no
		// administrator concept, so the check alone must never open data.
		admin := named("admin")
		key := f.create(named("alice"))
		for _, action := range []Action{ActionRead, ActionOutput, ActionWrite, ActionSchema, ActionShare, ActionDelete} {
			err := f.policy.Authorize(context.Background(), admin, key, action)
			Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue(), string(action))
		}
		ran := false
		err := f.policy.Admit(context.Background(), admin, key, ActionRead, func(context.Context) error { ran = true; return nil })
		Expect(err).To(HaveOccurred())
		Expect(ran).To(BeFalse())
		Expect(f.policy.SetGrant(context.Background(), admin, key, "admin", RoleEditor)).NotTo(Succeed())
		_, err = f.policy.Resolve(context.Background(), admin, contract.Request{AddonID: "fixture", ResourceID: key.ResourceID})
		Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue())
	})

	It("does not leak resource existence and treats unknown actions as owner mutations", func() {
		f := newFixture(ModeNamedUser)
		key := f.create(named("alice"))
		missing := ResourceKey{AddonID: "fixture", ResourceID: "nope"}
		a := f.policy.Authorize(context.Background(), named("bob"), key, ActionRead)
		b := f.policy.Authorize(context.Background(), named("bob"), missing, ActionRead)
		Expect(a).To(MatchError(b))
		Expect(ParseAction("whatever")).To(Equal(ActionUnknown))
		Expect(ActionUnknown.Mutation()).To(BeTrue())
		Expect(ParseAction("read").Mutation()).To(BeFalse())
		wrongAddon := ResourceKey{AddonID: "other", ResourceID: key.ResourceID}
		Expect(f.policy.Authorize(context.Background(), named("alice"), wrongAddon, ActionRead)).NotTo(Succeed())
	})

	It("never grants the owner role or a grant to the owner and the synthetic principals", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		ctx := context.Background()
		Expect(f.policy.SetGrant(ctx, owner, key, "friend", RoleOwner)).NotTo(Succeed())
		Expect(f.policy.SetGrant(ctx, owner, key, "owner", RoleViewer)).NotTo(Succeed())
		Expect(f.policy.Revoke(ctx, owner, key, "owner")).NotTo(Succeed())
		for _, id := range []string{"", "legacy-api-key", "anonymous", "local-owner"} {
			Expect(f.policy.SetGrant(ctx, owner, key, id, RoleViewer)).NotTo(Succeed(), id)
		}
		Expect(allowed(f, owner, key, ActionSchema)).To(BeTrue())
	})
})

var _ = Describe("identity", func() {
	It("rejects anonymous and legacy synthetic principals in named mode", func() {
		f := newFixture(ModeNamedUser)
		key := f.create(named("alice"))
		for _, a := range []contract.Actor{
			{}, {UserID: "legacy-api-key", CredentialID: "key:1", Mode: "named-user"},
			{UserID: "anonymous", CredentialID: "key:1", Mode: "named-user"},
			{UserID: "alice", Mode: "named-user"},
			{UserID: "alice", CredentialID: "key:alice", Mode: "local-owner"},
			{UserID: "alice", CredentialID: "key:alice"},
		} {
			Expect(f.policy.Authorize(context.Background(), a, key, ActionRead)).NotTo(Succeed(), a.UserID+"/"+a.Mode)
			_, err := f.policy.Register(context.Background(), a, "fixture")
			Expect(err).To(HaveOccurred())
		}
		// A resource that a legacy principal could somehow own is still refused.
		legacy := contract.Actor{UserID: "legacy-api-key", CredentialID: "key:1", Mode: "named-user"}
		Expect(f.policy.SetGrant(context.Background(), named("alice"), key, legacy.UserID, RoleViewer)).NotTo(Succeed())
		Expect(allowed(f, named("alice"), key, ActionRead)).To(BeTrue())
	})

	It("checks credential and account status on every admission", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		runs := 0
		run := func(context.Context) error { runs++; return nil }
		Expect(f.policy.Admit(context.Background(), owner, key, ActionRead, run)).To(Succeed())
		f.accounts.block("owner")
		err := f.policy.Admit(context.Background(), owner, key, ActionRead, run)
		Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue())
		Expect(f.policy.Admit(context.Background(), owner, key, ActionWrite, run)).NotTo(Succeed())
		Expect(runs).To(Equal(1))
		// Without a host account check nothing is admitted, not even the owner.
		bare := New(f.store, Options{})
		Expect(bare.Admit(context.Background(), owner, key, ActionRead, run)).NotTo(Succeed())
		Expect(runs).To(Equal(1))
	})

	It("keeps no current-user state shared between concurrent admissions", func() {
		f := newFixture(ModeNamedUser)
		owner, viewer := named("owner"), named("viewer")
		key := f.create(owner)
		Expect(f.policy.SetGrant(context.Background(), owner, key, "viewer", RoleViewer)).To(Succeed())
		hold, entered := make(chan struct{}), make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer GinkgoRecover()
			Expect(f.policy.Admit(context.Background(), owner, key, ActionRead, func(context.Context) error {
				close(entered)
				<-hold
				return nil
			})).To(Succeed())
		}()
		<-entered
		// The viewer, admitted while the owner's work runs, gets viewer rights.
		Expect(f.policy.Admit(context.Background(), viewer, key, ActionWrite, func(context.Context) error { return nil })).NotTo(Succeed())
		Expect(f.policy.Admit(context.Background(), viewer, key, ActionRead, func(context.Context) error { return nil })).To(Succeed())
		close(hold)
		wg.Wait()
	})
})

var _ = Describe("modes", func() {
	It("disables sharing in explicit local-owner mode and lets every local caller own the resource", func() {
		f := newFixture(ModeLocalOwner)
		local := contract.Actor{UserID: "alice", CredentialID: "local", Mode: "local-owner"}
		peer := contract.Actor{UserID: "someone-else", CredentialID: "local2", Mode: "local-owner"}
		key := f.create(local)
		Expect(f.policy.SetGrant(context.Background(), local, key, "bob", RoleViewer)).NotTo(Succeed())
		Expect(f.policy.Revoke(context.Background(), local, key, "bob")).NotTo(Succeed())
		Expect(allowed(f, peer, key, ActionSchema)).To(BeTrue())
		// A named principal is not a local-owner caller.
		Expect(f.policy.Authorize(context.Background(), named("alice"), key, ActionRead)).NotTo(Succeed())
		Expect(f.policy.Authorize(context.Background(), contract.Actor{CredentialID: "x", Mode: "local-owner"}, key, ActionRead)).NotTo(Succeed())
	})

	It("refuses a mode switch of populated data without a stopped backed-up migration", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "policy")
		f := open(ModeLocalOwner, dir)
		local := contract.Actor{UserID: "me", CredentialID: "local", Mode: "local-owner"}
		key := f.create(local)
		// Opening in the other mode never silently reinterprets owners.
		_, err := OpenStore(dir, ModeNamedUser)
		Expect(err).To(HaveOccurred())
		// A running host holds the store: migration refuses.
		backup := filepath.Join(GinkgoT().TempDir(), "backup")
		Expect(MigrateMode(dir, ModeNamedUser, backup, "alice")).To(MatchError(ErrStoreBusy))
		Expect(f.store.Close()).To(Succeed())
		Expect(MigrateMode(dir, ModeNamedUser, "", "alice")).NotTo(Succeed())
		Expect(MigrateMode(dir, ModeNamedUser, backup, "legacy-api-key")).NotTo(Succeed())
		Expect(MigrateMode(dir, ModeNamedUser, backup, "alice")).To(Succeed())
		_, statErr := os.Stat(filepath.Join(backup, fileName))
		Expect(statErr).NotTo(HaveOccurred())
		g := open(ModeNamedUser, dir)
		Expect(allowed(g, named("alice"), key, ActionSchema)).To(BeTrue())
		Expect(allowed(g, named("mallory"), key, ActionRead)).To(BeFalse())
		// Shared named data can never go back to local-owner mode.
		Expect(g.policy.SetGrant(context.Background(), named("alice"), key, "bob", RoleViewer)).To(Succeed())
		Expect(g.store.Close()).To(Succeed())
		err = MigrateMode(dir, ModeLocalOwner, filepath.Join(GinkgoT().TempDir(), "b2"), "")
		Expect(err).To(MatchError(ErrDowngradeUnsupported))
	})

	It("retains resources and grants across a restart and an uninstall of the package", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "policy")
		packages := filepath.Join(GinkgoT().TempDir(), "packages")
		Expect(os.MkdirAll(filepath.Join(packages, "fixture"), 0o700)).To(Succeed())
		f := open(ModeNamedUser, dir)
		owner := named("owner")
		key := f.create(owner)
		Expect(f.policy.SetGrant(context.Background(), owner, key, "viewer", RoleViewer)).To(Succeed())
		// Uninstalling replaces or removes the package tree only; the policy is
		// stored apart from it and nothing in the package tree names a grant.
		Expect(os.RemoveAll(packages)).To(Succeed())
		Expect(f.store.Close()).To(Succeed())
		g := open(ModeNamedUser, dir)
		Expect(allowed(g, named("viewer"), key, ActionRead)).To(BeTrue())
		Expect(allowed(g, owner, key, ActionSchema)).To(BeTrue())
		Expect(dir).NotTo(HavePrefix(packages))
		info, err := os.Stat(filepath.Join(dir, fileName))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		backup := filepath.Join(GinkgoT().TempDir(), "copy.json")
		Expect(g.store.Backup(backup)).To(Succeed())
		Expect(g.store.Backup(backup)).NotTo(Succeed())
		_, err = os.Stat(backup)
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses a corrupt store and a second opener", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "policy")
		f := open(ModeNamedUser, dir)
		_, err := OpenStore(dir, ModeNamedUser)
		Expect(err).To(MatchError(ErrStoreBusy))
		Expect(f.store.Close()).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, fileName), []byte(`{"version":1,"mode":"named-user","resources":{"x":{"addonId":"a"}}}`), 0o600)).To(Succeed())
		_, err = OpenStore(dir, ModeNamedUser)
		Expect(err).To(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dir, fileName), []byte(`not json`), 0o600)).To(Succeed())
		_, err = OpenStore(dir, ModeNamedUser)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("admission serialization", func() {
	It("stops admitted work of a revoked user and acknowledges only after it ended", func() {
		f := newFixture(ModeNamedUser)
		owner, editor := named("owner"), named("editor")
		key := f.create(owner)
		Expect(f.policy.SetGrant(context.Background(), owner, key, "editor", RoleEditor)).To(Succeed())
		entered := make(chan struct{})
		var finished atomic.Bool
		result := make(chan error, 1)
		go func() {
			result <- f.policy.Admit(context.Background(), editor, key, ActionWrite, func(ctx context.Context) error {
				close(entered)
				<-ctx.Done()
				finished.Store(true)
				return ctx.Err()
			})
		}()
		<-entered
		Expect(f.policy.Revoke(context.Background(), owner, key, "editor")).To(Succeed())
		Expect(finished.Load()).To(BeTrue())
		Expect(<-result).To(MatchError(context.Canceled))
		ran := false
		err := f.policy.Admit(context.Background(), editor, key, ActionRead, func(context.Context) error { ran = true; return nil })
		Expect(err).To(HaveOccurred())
		Expect(ran).To(BeFalse())
		Expect(f.prop.calls).To(Equal([]string{key.ResourceID + "/editor"}))
	})

	It("lets a racing revocation win against read and write admissions", func() {
		f := newFixture(ModeNamedUser)
		owner, editor := named("owner"), named("editor")
		for round := 0; round < 20; round++ {
			key := f.create(owner)
			Expect(f.policy.SetGrant(context.Background(), owner, key, "editor", RoleEditor)).To(Succeed())
			var inside atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 6; i++ {
				action := ActionRead
				if i%2 == 0 {
					action = ActionWrite
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					_ = f.policy.Admit(context.Background(), editor, key, action, func(ctx context.Context) error {
						inside.Add(1)
						defer inside.Add(-1)
						<-ctx.Done()
						return nil
					})
				}()
			}
			Expect(f.policy.Revoke(context.Background(), owner, key, "editor")).To(Succeed())
			// After the acknowledgement nothing of the editor is running and
			// nothing new is admitted.
			Expect(inside.Load()).To(BeZero())
			err := f.policy.Admit(context.Background(), editor, key, ActionRead, func(context.Context) error { inside.Add(1); return nil })
			Expect(err).To(HaveOccurred())
			Expect(inside.Load()).To(BeZero())
			wg.Wait()
		}
	})

	It("serializes writes per resource while reads run concurrently", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		other := f.create(owner)
		var inside, peak, otherInside atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				Expect(f.policy.Admit(context.Background(), owner, key, ActionWrite, func(context.Context) error {
					n := inside.Add(1)
					for {
						p := peak.Load()
						if n <= p || peak.CompareAndSwap(p, n) {
							break
						}
					}
					time.Sleep(time.Millisecond)
					inside.Add(-1)
					return nil
				})).To(Succeed())
			}()
		}
		wg.Wait()
		Expect(peak.Load()).To(Equal(int32(1)))
		// A held write on one resource does not block another resource or a read.
		hold, entered := make(chan struct{}), make(chan struct{})
		go func() {
			defer GinkgoRecover()
			_ = f.policy.Admit(context.Background(), owner, key, ActionWrite, func(context.Context) error { close(entered); <-hold; return nil })
		}()
		<-entered
		Expect(f.policy.Admit(context.Background(), owner, other, ActionWrite, func(context.Context) error { otherInside.Add(1); return nil })).To(Succeed())
		Expect(f.policy.Admit(context.Background(), owner, key, ActionRead, func(context.Context) error { otherInside.Add(1); return nil })).To(Succeed())
		close(hold)
		Expect(otherInside.Load()).To(Equal(int32(2)))
	})

	It("bounds the revocation wait and does not acknowledge a stuck drain", func() {
		f := newFixture(ModeNamedUser)
		f.opts.DrainTimeout = 30 * time.Millisecond
		f.policy = New(f.store, f.opts)
		owner, editor := named("owner"), named("editor")
		key := f.create2(f.policy, owner)
		Expect(f.policy.SetGrant(context.Background(), owner, key, "editor", RoleEditor)).To(Succeed())
		entered, release := make(chan struct{}), make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer GinkgoRecover()
			_ = f.policy.Admit(context.Background(), editor, key, ActionWrite, func(context.Context) error { close(entered); <-release; return nil })
		}()
		<-entered
		err := f.policy.Revoke(context.Background(), owner, key, "editor")
		Expect(err).To(MatchError(ErrDrainPending))
		Expect(gateway.StatusFor(err)).To(Equal(503))
		// The revocation itself is already effective.
		Expect(allowed(f, editor, key, ActionRead)).To(BeFalse())
		close(release)
		<-done
	})

	It("invalidates pending proposals on revoke and downgrade and fails closed when it cannot", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		ctx := context.Background()
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		Expect(f.prop.calls).To(BeEmpty())
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleViewer)).To(Succeed())
		Expect(f.prop.calls).To(Equal([]string{key.ResourceID + "/editor"}))
		// Raising a role invalidates nothing.
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		Expect(f.prop.calls).To(HaveLen(1))
		f.prop.err = errors.New("store down")
		err := f.policy.Revoke(ctx, owner, key, "editor")
		Expect(err).To(HaveOccurred())
		Expect(gateway.StatusFor(err)).To(Equal(503))
		Expect(allowed(f, named("editor"), key, ActionRead)).To(BeFalse())
	})
})

var _ = Describe("resource registration", func() {
	It("requires explicit creation authority and keeps pending registrations inaccessible", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		none := New(f.store, Options{Revalidate: f.accounts.check})
		_, err := none.Register(context.Background(), owner, "fixture")
		Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue())
		refused := New(f.store, Options{Revalidate: f.accounts.check, CreateAuthority: func(context.Context, contract.Actor, string) error { return errors.New("no") }})
		_, err = refused.Register(context.Background(), owner, "fixture")
		Expect(err).To(HaveOccurred())
		key, err := f.policy.Register(context.Background(), owner, "fixture")
		Expect(err).NotTo(HaveOccurred())
		Expect(key.ResourceID).To(HaveLen(32))
		for _, action := range []Action{ActionRead, ActionWrite, ActionSchema} {
			Expect(allowed(f, owner, key, action)).To(BeFalse())
		}
		// Only the creator may commit.
		Expect(f.policy.Commit(context.Background(), named("mallory"), key)).NotTo(Succeed())
		Expect(f.policy.Commit(context.Background(), owner, key)).To(Succeed())
		Expect(allowed(f, owner, key, ActionSchema)).To(BeTrue())
		Expect(allowed(f, named("mallory"), key, ActionRead)).To(BeFalse())
		// An orphan database with no record is never reachable.
		Expect(allowed(f, owner, ResourceKey{AddonID: "fixture", ResourceID: "orphan"}, ActionRead)).To(BeFalse())
		abandoned, err := f.policy.Register(context.Background(), owner, "fixture")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.policy.Abandon(context.Background(), owner, abandoned)).To(Succeed())
		Expect(f.policy.Commit(context.Background(), owner, abandoned)).NotTo(Succeed())
	})

	It("reconciles a registration interrupted by a crash from the durable store", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "policy")
		f := open(ModeNamedUser, dir)
		owner := named("owner")
		created, err := f.policy.Register(context.Background(), owner, "fixture")
		Expect(err).NotTo(HaveOccurred())
		missing, err := f.policy.Register(context.Background(), owner, "fixture")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.store.Close()).To(Succeed())
		g := open(ModeNamedUser, dir)
		Expect(allowed(g, owner, created, ActionRead)).To(BeFalse())
		failing := func(context.Context, ResourceKey) (bool, error) { return false, errors.New("probe failed") }
		Expect(g.policy.Reconcile(context.Background(), failing)).NotTo(Succeed())
		Expect(allowed(g, owner, created, ActionRead)).To(BeFalse())
		Expect(g.policy.Reconcile(context.Background(), func(_ context.Context, k ResourceKey) (bool, error) {
			return k.ResourceID == created.ResourceID, nil
		})).To(Succeed())
		Expect(allowed(g, owner, created, ActionSchema)).To(BeTrue())
		Expect(allowed(g, named("mallory"), created, ActionRead)).To(BeFalse())
		Expect(allowed(g, owner, missing, ActionRead)).To(BeFalse())
		Expect(g.policy.Commit(context.Background(), owner, missing)).NotTo(Succeed())
	})
})

type clientFixture struct{ contract.Runtime }

func (clientFixture) Read(_ context.Context, e contract.Envelope) (contract.Result, error) {
	return contract.Result{Payload: []byte(`"` + e.Request.ResourceID + `"`)}, nil
}

type runtimeFixture struct{}

func (runtimeFixture) Runtime(context.Context, string) (addonruntime.Instance, error) {
	return addonruntime.Instance{Generation: 1, Incarnation: "current", Client: clientFixture{}}, nil
}

var _ = Describe("gateway integration", func() {
	It("maps opaque ids to the trusted addon and owner and keeps an absent policy denying", func() {
		f := newFixture(ModeNamedUser)
		owner, viewer, stranger := named("owner"), named("viewer"), named("stranger")
		key := f.create(owner)
		Expect(f.policy.SetGrant(context.Background(), owner, key, "viewer", RoleViewer)).To(Succeed())
		provider := f.policy.Gateway()
		g := &gateway.Gateway{Runtime: runtimeFixture{}, Revalidate: f.accounts.check, Admission: provider, Resolver: provider}
		read := contract.Request{AddonID: "fixture", ResourceID: key.ResourceID, OperationID: "op", Generation: 1, Action: "read", Arguments: []byte(`{}`)}
		for _, a := range []contract.Actor{owner, viewer} {
			out, err := g.Dispatch(context.Background(), a, read)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(out.Payload)).To(Equal(`"` + key.ResourceID + `"`))
		}
		_, err := g.Dispatch(context.Background(), stranger, read)
		Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue())
		forged := read
		forged.AddonID = "other-addon"
		_, err = g.Dispatch(context.Background(), owner, forged)
		Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue())
		forged = read
		forged.ResourceID = "unknown"
		_, err = g.Dispatch(context.Background(), owner, forged)
		Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue())
		// An artifact store is a later integration; outputs stay denied.
		_, err = provider.Output(context.Background(), owner, read, "handle")
		Expect(err).To(HaveOccurred())
		// Without the policy the same gateway admits nothing.
		bare := &gateway.Gateway{Runtime: runtimeFixture{}, Revalidate: f.accounts.check}
		_, err = bare.Dispatch(context.Background(), owner, read)
		Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue())
		// The admin-only unknown gateway action name is an owner mutation.
		Expect(provider.Admit(context.Background(), viewer, key, "frobnicate", func(context.Context) error { return nil })).NotTo(Succeed())
		Expect(provider.Admit(context.Background(), owner, key, "frobnicate", func(context.Context) error { return nil })).To(Succeed())
	})
})
