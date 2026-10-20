// SPDX-License-Identifier: MIT
package policy

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/packages"
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
	// down makes the account store unavailable, which is not a denial.
	down bool
}

func (a *account) check(_ context.Context, actor contract.Actor) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.down {
		return fmt.Errorf("account store down")
	}
	if a.blocked[actor.UserID] {
		return fmt.Errorf("account paused: %w", gateway.ErrForbidden)
	}
	return nil
}
func (a *account) block(id string) { a.mu.Lock(); a.blocked[id] = true; a.mu.Unlock() }
func (a *account) setDown(v bool)  { a.mu.Lock(); a.down = v; a.mu.Unlock() }

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
		refused := New(f.store, Options{Revalidate: f.accounts.check, CreateAuthority: func(context.Context, contract.Actor, string) error { return fmt.Errorf("no: %w", gateway.ErrForbidden) }})
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
		Expect(g.policy.Reconcile(context.Background(), failing)).To(Succeed())
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

var _ = Describe("retry-safe downgrades", func() {
	// stuckEditor admits a write of the editor that ends only when release closes.
	stuckEditor := func(f *fixture, key ResourceKey) (release chan struct{}, ended *atomic.Bool, done chan struct{}) {
		entered := make(chan struct{})
		release, done, ended = make(chan struct{}), make(chan struct{}), &atomic.Bool{}
		go func() {
			defer close(done)
			defer GinkgoRecover()
			_ = f.policy.Admit(context.Background(), named("editor"), key, ActionWrite, func(context.Context) error {
				close(entered)
				<-release
				ended.Store(true)
				return nil
			})
		}()
		<-entered
		return
	}

	It("does not acknowledge a retried downgrade while the lowered user's write still runs", func() {
		f := newFixture(ModeNamedUser)
		f.opts.DrainTimeout = 30 * time.Millisecond
		f.policy = New(f.store, f.opts)
		owner := named("owner")
		key := f.create(owner)
		ctx := context.Background()
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		release, ended, done := stuckEditor(f, key)
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleViewer)).To(MatchError(ErrDrainPending))
		// The lowered grant is durable now; the identical retry must still wait.
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleViewer)).To(MatchError(ErrDrainPending))
		Expect(ended.Load()).To(BeFalse())
		close(release)
		<-done
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleViewer)).To(Succeed())
		Expect(ended.Load()).To(BeTrue())
		// Once acknowledged the marker is gone: the next identical call is a no-op.
		calls := len(f.prop.calls)
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleViewer)).To(Succeed())
		Expect(f.prop.calls).To(HaveLen(calls))
	})

	It("retries a failed proposal invalidation after the lowered grant is already durable", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		ctx := context.Background()
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		f.prop.err = errors.New("store down")
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleViewer)).To(MatchError(ErrDrainPending))
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleViewer)).To(MatchError(ErrDrainPending))
		f.prop.err = nil
		before := len(f.prop.calls)
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleViewer)).To(Succeed())
		Expect(f.prop.calls).To(HaveLen(before + 1))
	})

	It("keeps the drain owed across a restart", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "policy")
		f := open(ModeNamedUser, dir)
		owner := named("owner")
		key := f.create(owner)
		ctx := context.Background()
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		f.prop.err = errors.New("store down")
		Expect(f.policy.Revoke(ctx, owner, key, "editor")).To(MatchError(ErrDrainPending))
		Expect(f.store.Close()).To(Succeed())
		g := open(ModeNamedUser, dir)
		Expect(g.policy.Revoke(ctx, owner, key, "editor")).To(Succeed())
		Expect(g.prop.calls).To(Equal([]string{key.ResourceID + "/editor"}))
	})
})

var _ = Describe("drain epochs", func() {
	// start admits a write of user and returns once it entered. The work ends
	// when release closes, or when it is cancelled unless it is stubborn like a
	// host call that ignores cancellation. ctxErr is the context error the work
	// saw when it ended.
	type work struct {
		release chan struct{}
		done    chan struct{}
		ctxErr  atomic.Value
	}
	start := func(p *Policy, user string, key ResourceKey, stubborn ...bool) *work {
		w := &work{release: make(chan struct{}), done: make(chan struct{})}
		entered := make(chan struct{})
		go func() {
			defer close(w.done)
			defer GinkgoRecover()
			_ = p.Admit(context.Background(), named(user), key, ActionWrite, func(c context.Context) error {
				close(entered)
				if len(stubborn) > 0 && stubborn[0] {
					<-w.release
				} else {
					select {
					case <-w.release:
					case <-c.Done():
					}
				}
				w.ctxErr.Store(fmt.Sprint(c.Err()))
				return nil
			})
		}()
		<-entered
		return w
	}
	cancelled := func(w *work) bool { v, _ := w.ctxErr.Load().(string); return v != "<nil>" && v != "" }

	It("does not cancel work admitted under a later grant when a lowering is retried", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		ctx := context.Background()
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		f.prop.err = errors.New("store down")
		Expect(f.policy.Revoke(ctx, owner, key, "editor")).To(MatchError(ErrDrainPending))
		// The regrant is durable, but the drain of the earlier revocation is still owed.
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(MatchError(ErrDrainPending))
		fresh := start(f.policy, "editor", key)
		f.prop.err = nil
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		close(fresh.release)
		<-fresh.done
		Expect(cancelled(fresh)).To(BeFalse(), "work admitted after the regrant must keep running")
		Expect(allowed(f, named("editor"), key, ActionWrite)).To(BeTrue())
	})

	It("still waits for and cancels work admitted before the lowering when the regrant is retried", func() {
		f := newFixture(ModeNamedUser)
		f.opts.DrainTimeout = 30 * time.Millisecond
		f.policy = New(f.store, f.opts)
		owner := named("owner")
		key := f.create(owner)
		ctx := context.Background()
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		old := start(f.policy, "editor", key, true)
		Expect(f.policy.Revoke(ctx, owner, key, "editor")).To(MatchError(ErrDrainPending))
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(MatchError(ErrDrainPending))
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(MatchError(ErrDrainPending))
		close(old.release)
		<-old.done
		Expect(cancelled(old)).To(BeTrue(), "work from before the revocation is stopped")
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
	})

	It("stops work admitted after a regrant when the role is lowered again", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		ctx := context.Background()
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		f.prop.err = errors.New("store down")
		Expect(f.policy.Revoke(ctx, owner, key, "editor")).To(MatchError(ErrDrainPending))
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(MatchError(ErrDrainPending))
		fresh := start(f.policy, "editor", key)
		f.prop.err = nil
		// A new lowering is a new epoch and covers the work admitted since.
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleViewer)).To(Succeed())
		close(fresh.release)
		<-fresh.done
		Expect(cancelled(fresh)).To(BeTrue())
	})

	It("keeps the epoch of the owed drain across a restart", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "policy")
		f := open(ModeNamedUser, dir)
		owner := named("owner")
		key := f.create(owner)
		ctx := context.Background()
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		f.prop.err = errors.New("store down")
		Expect(f.policy.Revoke(ctx, owner, key, "editor")).To(MatchError(ErrDrainPending))
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(MatchError(ErrDrainPending))
		Expect(f.store.Close()).To(Succeed())

		g := open(ModeNamedUser, dir)
		fresh := start(g.policy, "editor", key)
		Expect(g.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		close(fresh.release)
		<-fresh.done
		Expect(cancelled(fresh)).To(BeFalse(), "an admission after the restart is newer than the persisted mark")
		Expect(g.prop.calls).To(Equal([]string{key.ResourceID + "/editor"}))
		disk, err := os.ReadFile(filepath.Join(dir, fileName))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(disk)).NotTo(ContainSubstring("drain"))
		// The counter survives the cleared mark: the next lowering still outranks
		// the work admitted before it.
		again := start(g.policy, "editor", key)
		Expect(g.policy.Revoke(ctx, owner, key, "editor")).To(Succeed())
		close(again.release)
		<-again.done
		Expect(cancelled(again)).To(BeTrue())
	})

	It("keeps a drain mark that a concurrent lowering replaced while a retry was waiting", func() {
		f := newFixture(ModeNamedUser)
		f.opts.DrainTimeout = 5 * time.Second
		f.policy = New(f.store, f.opts)
		owner := named("owner")
		key := f.create(owner)
		ctx := context.Background()
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		old := start(f.policy, "editor", key, true)
		first := make(chan error, 1)
		go func() { first <- f.policy.SetGrant(ctx, owner, key, "editor", RoleViewer) }()
		// The downgrade cancels the run and waits for it to end.
		calls := func() int { f.prop.mu.Lock(); defer f.prop.mu.Unlock(); return len(f.prop.calls) }
		Eventually(calls).Should(Equal(1))
		f.prop.err = errors.New("store down")
		Expect(f.policy.Revoke(ctx, owner, key, "editor")).To(MatchError(ErrDrainPending))
		close(old.release)
		<-old.done
		Expect(<-first).To(Or(Succeed(), MatchError(ErrDrainPending)))
		// Revoke left its own drain owed; the first call must not have cleared it.
		f.prop.err = nil
		before := calls()
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleViewer)).To(Succeed())
		Expect(calls()).To(Equal(before + 1))
	})
})

var _ = Describe("account store failures", func() {
	It("maps a store outage to unavailable and a disabled account to forbidden", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		outage := fmt.Errorf("db down: %w", gateway.ErrUnavailable)
		f.opts.Revalidate = func(_ context.Context, a contract.Actor) error {
			if a.UserID == "owner" {
				return outage
			}
			return fmt.Errorf("account disabled: %w", gateway.ErrForbidden)
		}
		p := New(f.store, f.opts)
		ran := false
		err := p.Admit(context.Background(), owner, key, ActionRead, func(context.Context) error { ran = true; return nil })
		Expect(errors.Is(err, gateway.ErrUnavailable)).To(BeTrue())
		Expect(gateway.StatusFor(err)).To(Equal(503))
		err = p.Authorize(context.Background(), owner, key, ActionRead)
		Expect(gateway.StatusFor(err)).To(Equal(503))
		_, err = p.Register(context.Background(), owner, "fixture")
		Expect(gateway.StatusFor(err)).To(Equal(503))
		err = p.SetGrant(context.Background(), owner, key, "x", RoleViewer)
		Expect(gateway.StatusFor(err)).To(Equal(503))
		err = p.Admit(context.Background(), named("gone"), key, ActionRead, func(context.Context) error { ran = true; return nil })
		Expect(gateway.StatusFor(err)).To(Equal(403))
		Expect(ran).To(BeFalse())
	})
})

var _ = Describe("unclassified host check failures", func() {
	It("reports an unclassified account or creation check error as unavailable and a definite denial as forbidden", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		ctx := context.Background()
		plain := errors.New("backend answered something unexpected")
		denial := fmt.Errorf("account paused: %w", gateway.ErrForbidden)
		for name, row := range map[string]struct {
			err  error
			want int
		}{"plain": {plain, 503}, "denial": {denial, 403}} {
			f.opts.Revalidate = func(context.Context, contract.Actor) error { return row.err }
			p := New(f.store, f.opts)
			ran := false
			err := p.Admit(ctx, owner, key, ActionRead, func(context.Context) error { ran = true; return nil })
			Expect(gateway.StatusFor(err)).To(Equal(row.want), name+" admit")
			Expect(ran).To(BeFalse(), name)
			Expect(gateway.StatusFor(p.Authorize(ctx, owner, key, ActionRead))).To(Equal(row.want), name+" authorize")
			_, err = p.Register(ctx, owner, "fixture")
			Expect(gateway.StatusFor(err)).To(Equal(row.want), name+" register")
			if row.want == 503 {
				Expect(errors.Is(err, ErrUnavailable)).To(BeTrue(), name)
			}
		}
		f.opts.Revalidate = f.accounts.check
		for name, row := range map[string]struct {
			err  error
			want int
		}{"plain": {plain, 503}, "denial": {denial, 403}} {
			f.opts.CreateAuthority = func(context.Context, contract.Actor, string) error { return row.err }
			_, err := New(f.store, f.opts).Register(ctx, owner, "fixture")
			Expect(gateway.StatusFor(err)).To(Equal(row.want), name+" creation authority")
		}
	})
})

var _ = Describe("creation limits", func() {
	It("caps pending and committed registrations per user and in total", func() {
		f := newFixture(ModeNamedUser)
		f.opts.MaxPendingPerUser, f.opts.MaxResourcesPerUser, f.opts.MaxResources = 2, 3, 4
		p := New(f.store, f.opts)
		ctx := context.Background()
		alice, bob := named("alice"), named("bob")
		a1, err := p.Register(ctx, alice, "fixture")
		Expect(err).NotTo(HaveOccurred())
		_, err = p.Register(ctx, alice, "fixture")
		Expect(err).NotTo(HaveOccurred())
		_, err = p.Register(ctx, alice, "fixture")
		Expect(errors.Is(err, ErrQuota)).To(BeTrue())
		Expect(gateway.StatusFor(err)).To(Equal(403))
		// Committing frees pending slots but still counts toward the resource cap.
		Expect(p.Commit(ctx, alice, a1)).To(Succeed())
		_, err = p.Register(ctx, alice, "fixture")
		Expect(err).NotTo(HaveOccurred())
		_, err = p.Register(ctx, alice, "fixture")
		Expect(errors.Is(err, ErrQuota)).To(BeTrue())
		// Another user has own allowance until the total cap (4) is reached.
		_, err = p.Register(ctx, bob, "fixture")
		Expect(err).NotTo(HaveOccurred())
		_, err = p.Register(ctx, bob, "fixture")
		Expect(errors.Is(err, ErrQuota)).To(BeTrue())
		// Abandoning releases a slot.
		k, err := p.Register(ctx, alice, "fixture")
		Expect(errors.Is(err, ErrQuota)).To(BeTrue())
		_ = k
	})

	It("applies finite defaults when no limit is configured", func() {
		f := newFixture(ModeNamedUser)
		Expect(f.policy.opts.MaxPendingPerUser).To(Equal(DefaultMaxPendingPerUser))
		Expect(f.policy.opts.MaxResourcesPerUser).To(Equal(DefaultMaxResourcesPerUser))
		Expect(f.policy.opts.MaxResources).To(Equal(DefaultMaxResources))
		Expect(DefaultMaxPendingPerUser).To(BeNumerically(">", 0))
	})

	It("denies creation as unavailable when the authority cannot tell", func() {
		f := newFixture(ModeNamedUser)
		f.opts.CreateAuthority = func(context.Context, contract.Actor, string) error {
			return fmt.Errorf("manager down: %w", gateway.ErrUnavailable)
		}
		p := New(f.store, f.opts)
		_, err := p.Register(context.Background(), named("alice"), "fixture")
		Expect(gateway.StatusFor(err)).To(Equal(503))
	})
})

var _ = Describe("admission ordering", func() {
	It("does not start the work when revocation cancels the admission before run", func() {
		f := newFixture(ModeNamedUser)
		owner, editor := named("owner"), named("editor")
		key := f.create(owner)
		ctx := context.Background()
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		var calls atomic.Int32
		revoked := make(chan error, 1)
		// This host check ignores cancellation: it waits for it and still says yes.
		f.opts.Revalidate = func(c context.Context, a contract.Actor) error {
			if a.UserID == "editor" && calls.Add(1) == 2 {
				go func() { revoked <- f.policy.Revoke(ctx, owner, key, "editor") }()
				<-c.Done()
			}
			return nil
		}
		p := New(f.store, f.opts)
		f.policy = p
		ran := false
		err := p.Admit(ctx, editor, key, ActionRead, func(context.Context) error { ran = true; return nil })
		Expect(err).To(HaveOccurred())
		Expect(ran).To(BeFalse())
		Expect(<-revoked).To(Succeed())
	})
})

var _ = Describe("store integrity", func() {
	reopen := func(contents string) error {
		dir := filepath.Join(GinkgoT().TempDir(), "policy")
		Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, fileName), []byte(contents), 0o600)).To(Succeed())
		s, err := OpenStore(dir, ModeNamedUser)
		if err == nil {
			_ = s.Close()
			return nil
		}
		b, rerr := os.ReadFile(filepath.Join(dir, fileName))
		Expect(rerr).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal(contents), "a refused store is never reset")
		return err
	}

	It("rejects persisted unknown roles and trailing bytes without resetting the store", func() {
		good := `{"version":1,"mode":"named-user","resources":{"r":{"addonId":"a","owner":"o","state":"active","grants":{"u":"editor"}}}}`
		Expect(reopen(good)).To(Succeed())
		Expect(reopen(`{"version":1,"mode":"named-user","resources":{"r":{"addonId":"a","owner":"o","state":"active","grants":{"u":"root"}}}}`)).To(HaveOccurred())
		Expect(reopen(`{"version":1,"mode":"named-user","resources":{"r":{"addonId":"a","owner":"o","state":"active","grants":{"u":"owner"}}}}`)).To(HaveOccurred())
		Expect(reopen(`{"version":1,"mode":"named-user","resources":{"r":{"addonId":"a","owner":"o","state":"active","grants":{"o":"viewer"}}}}`)).To(HaveOccurred())
		Expect(reopen(good + `{"version":1}`)).To(HaveOccurred())
		marked := `{"version":1,"mode":"named-user","resources":{"r":{"addonId":"a","owner":"o","state":"active","epoch":2,"drain":{"u":{"writes":2}}}}}`
		Expect(reopen(marked)).To(Succeed())
		Expect(reopen(strings.Replace(marked, `"writes":2`, `"writes":3`, 1))).To(HaveOccurred())
		Expect(reopen(strings.Replace(marked, `"writes":2`, `"writes":0`, 1))).To(HaveOccurred())
		Expect(reopen(good + "\n  \n")).To(Succeed())
	})

	It("removes stale temporary files on open", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "policy")
		f := open(ModeNamedUser, dir)
		f.create(named("owner"))
		Expect(f.store.Close()).To(Succeed())
		stale := filepath.Join(dir, ".policy-123456")
		Expect(os.WriteFile(stale, []byte("partial"), 0o600)).To(Succeed())
		other := filepath.Join(dir, "keep.txt")
		Expect(os.WriteFile(other, []byte("x"), 0o600)).To(Succeed())
		open(ModeNamedUser, dir)
		Expect(stale).NotTo(BeAnExistingFile())
		Expect(other).To(BeAnExistingFile())
	})

	It("stops accepting writes after a failed directory sync and keeps the applied change", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "policy")
		f := open(ModeNamedUser, dir)
		owner := named("owner")
		key := f.create(owner)
		ctx := context.Background()
		Expect(f.policy.SetGrant(ctx, owner, key, "viewer", RoleViewer)).To(Succeed())
		fail := true
		old := syncDirectory
		syncDirectory = func(d *os.File) error {
			if fail {
				return errors.New("sync failed")
			}
			return old(d)
		}
		DeferCleanup(func() { syncDirectory = old })
		err := f.policy.Revoke(ctx, owner, key, "viewer")
		Expect(err).To(HaveOccurred())
		Expect(gateway.StatusFor(err)).To(Equal(503))
		// The file on disk already holds the revocation, so memory must not
		// disagree with it.
		Expect(allowed(f, named("viewer"), key, ActionRead)).To(BeFalse())
		disk, rerr := os.ReadFile(filepath.Join(dir, fileName))
		Expect(rerr).NotTo(HaveOccurred())
		Expect(string(disk)).NotTo(ContainSubstring(`"viewer":"viewer"`))
		// Later writes are refused even when the directory works again.
		fail = false
		Expect(f.policy.SetGrant(ctx, owner, key, "viewer", RoleViewer)).To(MatchError(ErrUnavailable))
		_, err = f.policy.Register(ctx, owner, "fixture")
		Expect(gateway.StatusFor(err)).To(Equal(503))
		Expect(f.store.Close()).To(Succeed())
		g := open(ModeNamedUser, dir)
		Expect(allowed(g, named("viewer"), key, ActionRead)).To(BeFalse())
		Expect(g.policy.SetGrant(ctx, owner, key, "viewer", RoleViewer)).To(Succeed())
	})
})

var _ = Describe("real manager lifecycle", func() {
	It("retains resources and grants across a real manager uninstall and restart", func() {
		root := GinkgoT().TempDir()
		DeferCleanup(func() {
			_ = filepath.Walk(root, func(p string, i os.FileInfo, e error) error {
				if e == nil && i.IsDir() {
					_ = os.Chmod(p, 0o700)
				}
				return nil
			})
		})
		manifest, err := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(err).NotTo(HaveOccurred())
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		for name, data := range map[string][]byte{"manifest.json": manifest, "bin/sample": []byte("binary")} {
			Expect(tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))})).To(Succeed())
			_, err = tw.Write(data)
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(tw.Close()).To(Succeed())
		artifact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(b.Bytes()) }))
		DeferCleanup(artifact.Close)
		release := catalog.Release{AddonID: "sample", Name: "Sample", Version: "1.2.3", Artifact: artifact.URL + "/package.tar", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(b.Bytes())), OS: "linux", Arch: "amd64"}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = catalog.WriteIndex(w, []catalog.Release{release})
		}))
		DeferCleanup(server.Close)
		admin := named("admin")
		check := func(_ context.Context, a contract.Actor) error {
			if a != admin {
				return errors.New("denied")
			}
			return nil
		}
		ctx := context.Background()
		sources, err := catalog.NewSources(filepath.Join(root, "sources.json"), check, catalog.CredentialResolver{})
		Expect(err).NotTo(HaveOccurred())
		Expect(sources.Save(ctx, admin, catalog.Source{ID: "test", Name: "Test", URL: server.URL, Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
		_, err = sources.Refresh(ctx, "test")
		Expect(err).NotTo(HaveOccurred())
		cat := catalog.Catalog{Sources: sources, TargetOS: "linux", TargetArch: "amd64"}
		cfg := addons.OwnerConfig{InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "disk", Root: filepath.Join(root, "owner")}
		inst := &packages.Installer{Root: filepath.Join(cfg.Root, "packages"), Catalog: cat, Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}}
		m, err := addons.NewManager(ctx, cfg, cat, inst, check)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = m.Close() })
		cmd := func(id, action string) addons.LifecycleCommand {
			return addons.LifecycleCommand{OperationID: id, AddonID: "sample", Action: action, SourceID: "test", Version: "1.2.3"}
		}
		_, err = m.Submit(ctx, admin, cmd("one", "install"))
		Expect(err).NotTo(HaveOccurred())

		dir := filepath.Join(cfg.Root, "policy")
		f := open(ModeNamedUser, dir)
		owner := named("owner")
		key, err := f.policy.Register(ctx, owner, "sample")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.policy.Commit(ctx, owner, key)).To(Succeed())
		Expect(f.policy.SetGrant(ctx, owner, key, "viewer", RoleViewer)).To(Succeed())

		_, err = m.Submit(ctx, admin, cmd("two", "uninstall"))
		Expect(err).NotTo(HaveOccurred())
		status, err := m.Status(ctx, "sample")
		Expect(err).NotTo(HaveOccurred())
		Expect(status.Installed).To(BeFalse())
		Expect(allowed(f, named("viewer"), key, ActionRead)).To(BeTrue())
		Expect(allowed(f, owner, key, ActionSchema)).To(BeTrue())
		Expect(f.store.Close()).To(Succeed())
		Expect(m.Close()).To(Succeed())

		g := open(ModeNamedUser, dir)
		Expect(allowed(g, named("viewer"), key, ActionRead)).To(BeTrue())
		Expect(allowed(g, named("viewer"), key, ActionWrite)).To(BeFalse())
		Expect(allowed(g, owner, key, ActionSchema)).To(BeTrue())
	})
})
