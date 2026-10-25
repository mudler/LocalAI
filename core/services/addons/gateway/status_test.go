// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type erroringRuntime struct{ err error }

func (r erroringRuntime) Runtime(context.Context, string) (addonruntime.Instance, error) {
	return addonruntime.Instance{}, r.err
}

var _ = Describe("typed owner failure status", func() {
	ok := func(context.Context, contract.Actor) error { return nil }
	owner := func(g *Gateway) *fixtureEnv {
		return carrierFixtureWith(RelayLimits{}, func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(g)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
	}
	file := contract.Request{AddonID: "sample", OperationID: "op", Generation: 1, Action: "read", Arguments: json.RawMessage(`{}`)}
	relay := func(env *fixtureEnv, front string) (*httptest.ResponseRecorder, error) {
		w := httptest.NewRecorder()
		e := env.carriers[front].Relay(context.Background(), frontActor, RelayRequest{Request: file, Kind: "file", Handle: fakeArtifactID}, w)
		return w, e
	}
	runtimeFailure := func(err error) *Gateway {
		return &Gateway{Revalidate: ok, Admission: &admitted{}, Resolver: resolved{}, Runtime: erroringRuntime{err}, Artifacts: &fakeArtifacts{err: err}}
	}

	It("reports a runtime that cannot serve as unavailable through both frontends", func() {
		for name, cause := range map[string]error{
			"runtime":       addonruntime.ErrRuntime,
			"not owner":     addons.ErrNotOwner,
			"recovery":      addons.ErrRecoveryRequired,
			"owner busy":    addons.ErrOwnerBusy,
			"wrapped cause": fmt.Errorf("start: %w", addonruntime.ErrRuntime),
		} {
			env := owner(runtimeFailure(cause))
			for _, front := range []string{"front-a", "front-b"} {
				w, e := relay(env, front)
				Expect(e).To(Equal(ErrUnavailable), name+" relay via "+front)
				Expect(w.Body.Len()).To(BeZero(), name)
				_, e = env.carriers[front].Dispatch(context.Background(), frontActor, file)
				Expect(e).To(Equal(ErrUnavailable), name+" dispatch via "+front)
			}
			// The owner's own in-process frontend classifies the same way.
			Expect(StatusFor(cause)).To(Equal(http.StatusServiceUnavailable), name)
		}
	})
	It("reports an output that ends before its first byte as unavailable", func() {
		env := owner(&Gateway{Revalidate: ok, Artifacts: &fakeArtifacts{err: io.ErrUnexpectedEOF}})
		w, e := relay(env, "front-a")
		Expect(e).To(Equal(ErrUnavailable))
		Expect(w.Body.Len()).To(BeZero())
		Expect(StatusFor(io.ErrUnexpectedEOF)).To(Equal(http.StatusServiceUnavailable))
	})
	It("fails closed as unavailable for an unclassified owner error", func() {
		env := owner(runtimeFailure(errors.New("unexpected internal failure")))
		w, e := relay(env, "front-b")
		Expect(e).To(Equal(ErrUnavailable))
		Expect(w.Body.Len()).To(BeZero())
		Expect(StatusFor(errors.New("unexpected internal failure"))).To(Equal(http.StatusServiceUnavailable))
		Expect(StatusFor(nil)).To(Equal(http.StatusServiceUnavailable))
	})
	It("keeps authorization and policy denials forbidden", func() {
		for name, cause := range map[string]error{
			"forwarding denied": ErrForbidden,
			"administration":    addons.ErrForbidden,
			"source admin":      catalog.ErrForbidden,
			"conflict":          addons.ErrConflict,
			"wrapped denial":    fmt.Errorf("resolve: %w", ErrForbidden),
		} {
			Expect(StatusFor(cause)).To(Equal(http.StatusForbidden), name)
		}
		env := owner(runtimeFailure(ErrForbidden))
		_, e := relay(env, "front-a")
		Expect(e).To(Equal(ErrForbidden))
	})
	// countingOwner counts the mutations that reach the owner so a test can prove
	// the frontend did not dispatch the same work again.
	countingOwner := func(dispatches *atomic.Int32, g *Gateway) *fixtureEnv {
		return carrierFixtureWith(RelayLimits{}, func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(g)
			Expect(e).NotTo(HaveOccurred())
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/addons/forward" {
					dispatches.Add(1)
				}
				h.ServeHTTP(w, r)
			})
		})
	}
	expectUnknown := func(e error, id string) {
		var unknown *OutcomeUnknown
		ExpectWithOffset(1, errors.As(e, &unknown)).To(BeTrue(), "got %v", e)
		ExpectWithOffset(1, unknown.OperationID).To(Equal(id))
	}
	It("keeps a mutation unknown when the runtime fails after the owner applied it", func() {
		if goruntime.GOOS != "linux" {
			Skip("the add-on runtime exists on Linux only")
		}
		actor := frontActor
		m := ownerManager(actor, true)
		s, e := addonruntime.NewSupervisor(addonruntime.Config{StateRoot: GinkgoT().TempDir(), Limits: contract.DefaultLimits(), ReadyTimeout: time.Second, StableHealth: time.Second})
		Expect(e).NotTo(HaveOccurred())
		Expect(m.AttachRuntime(context.Background(), s)).To(Succeed())
		// A stopped supervisor cannot start the addon, so enable is journaled and
		// applied first and only then fails with a runtime error.
		Expect(s.Close(context.Background())).To(Succeed())
		var dispatches atomic.Int32
		env := countingOwner(&dispatches, &Gateway{Manager: m, Revalidate: ok})
		enable := contract.Request{AddonID: "sample", OperationID: "enable-op", Action: "enable", Arguments: []byte(`{}`)}
		_, e = env.carriers["front-a"].Dispatch(context.Background(), actor, enable)
		expectUnknown(e, "enable-op")
		Expect(dispatches.Load()).To(Equal(int32(1)))
		// The operation is durable on the owner: the work ran before the failure.
		out, e := env.carriers["front-b"].Dispatch(context.Background(), actor, contract.Request{AddonID: "sample", OperationID: "enable-op", Action: "operation-status", Arguments: []byte(`{}`)})
		Expect(e).NotTo(HaveOccurred())
		Expect(string(out.Payload)).To(ContainSubstring("completed"))
	})
	It("keeps a mutation unknown when the owner reports recovery required for its operation", func() {
		actor := frontActor
		root := filepath.Join(GinkgoT().TempDir(), "owner")
		m, e := addons.NewManager(context.Background(), addons.OwnerConfig{InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "disk", Root: root}, catalog.Catalog{}, &packages.Installer{Root: filepath.Join(root, "packages")}, func(context.Context, contract.Actor) error { return nil })
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(m.Close)
		_, e = m.Submit(context.Background(), actor, addons.LifecycleCommand{AddonID: "sample", OperationID: "seed", Action: "install", SourceID: "absent", Version: "1.0.0"})
		Expect(e).To(HaveOccurred())
		journal := filepath.Join(root, "journal.json")
		Expect(os.Rename(journal, journal+".saved")).To(Succeed())
		Expect(os.Mkdir(journal, 0700)).To(Succeed())
		var dispatches atomic.Int32
		env := countingOwner(&dispatches, &Gateway{Manager: m, Revalidate: ok})
		install := contract.Request{AddonID: "sample", OperationID: "second", Action: "install", Arguments: []byte(`{"sourceId":"absent","version":"1.0.0"}`)}
		// The journal write fails after the owner recorded the intent in memory.
		_, e = env.carriers["front-a"].Dispatch(context.Background(), actor, install)
		expectUnknown(e, "second")
		Expect(os.Remove(journal)).To(Succeed())
		Expect(os.Rename(journal+".saved", journal)).To(Succeed())
		// The replay finds its own journaled operation and asks for recovery.
		_, e = env.carriers["front-b"].Dispatch(context.Background(), actor, install)
		expectUnknown(e, "second")
		Expect(dispatches.Load()).To(Equal(int32(2)))
	})
	It("reports a mutation as unknown when the owner cannot serve it at all", func() {
		// The owner has no lifecycle manager. Without a proof of non-execution the
		// frontend keeps the conservative reading and the original identifier.
		var dispatches atomic.Int32
		env := countingOwner(&dispatches, &Gateway{Revalidate: ok})
		install := contract.Request{AddonID: "sample", OperationID: "install", Action: "install", Arguments: []byte(`{"sourceId":"s","version":"1.0.0"}`)}
		_, e := env.carriers["front-a"].Dispatch(context.Background(), frontActor, install)
		expectUnknown(e, "install")
		Expect(dispatches.Load()).To(Equal(int32(1)))
	})
	It("keeps a mutation unknown on the owner's own frontend and leaves reads and denials alone", func() {
		if goruntime.GOOS != "linux" {
			Skip("the add-on runtime exists on Linux only")
		}
		actor := frontActor
		m := ownerManager(actor, true)
		s, e := addonruntime.NewSupervisor(addonruntime.Config{StateRoot: GinkgoT().TempDir(), Limits: contract.DefaultLimits(), ReadyTimeout: time.Second, StableHealth: time.Second})
		Expect(e).NotTo(HaveOccurred())
		Expect(m.AttachRuntime(context.Background(), s)).To(Succeed())
		Expect(s.Close(context.Background())).To(Succeed())
		// No forwarding carrier: this is the owner serving its own callers.
		local := &Gateway{Manager: m, Revalidate: ok}
		enable := contract.Request{AddonID: "sample", OperationID: "local-enable", Action: "enable", Arguments: []byte(`{}`)}
		_, e = local.Dispatch(context.Background(), actor, enable)
		expectUnknown(e, "local-enable")
		Expect(StatusFor(e)).To(Equal(http.StatusServiceUnavailable))
		Expect(errors.Is(e, ErrUnavailable)).To(BeTrue())
		// A mutation that fails before any owner exists is unknown as well, as it
		// is through a forwarding frontend.
		_, e = (&Gateway{Revalidate: ok}).Dispatch(context.Background(), actor, enable)
		expectUnknown(e, "local-enable")
		// Reads stay plain unavailability.
		var unknown *OutcomeUnknown
		_, e = (&Gateway{Revalidate: ok}).Dispatch(context.Background(), actor, contract.Request{AddonID: "sample", OperationID: "poll", Action: "operation-status", Arguments: []byte(`{}`)})
		Expect(e).To(Equal(ErrUnavailable))
		Expect(errors.As(e, &unknown)).To(BeFalse())
		// Denials stay denials, whether or not the action is a mutation.
		_, e = local.Dispatch(context.Background(), actor, contract.Request{AddonID: "sample", OperationID: "bad", Action: "enable", Arguments: []byte(`{"unknown":1}`)})
		Expect(e).To(Equal(ErrForbidden))
		_, e = local.Dispatch(context.Background(), contract.Actor{UserID: "other", CredentialID: "k", Mode: "named-user"}, enable)
		Expect(errors.As(e, &unknown)).To(BeFalse())
		Expect(StatusFor(e)).To(Equal(http.StatusForbidden))
	})
	It("lets unavailability win when an error wraps both a denial and an unavailable cause", func() {
		both := fmt.Errorf("%w: %w", addons.ErrForbidden, addonruntime.ErrRuntime)
		swapped := fmt.Errorf("%w: %w", addonruntime.ErrRuntime, addons.ErrForbidden)
		for name, cause := range map[string]error{"denial first": both, "cause first": swapped, "joined": errors.Join(ErrForbidden, addons.ErrOwnerBusy)} {
			Expect(StatusFor(cause)).To(Equal(http.StatusServiceUnavailable), name)
		}
		// A pure denial stays forbidden.
		Expect(StatusFor(fmt.Errorf("x: %w", addons.ErrForbidden))).To(Equal(http.StatusForbidden))
	})
	It("does not leak attachment headers into an error when the first read fails", func() {
		env := carrierFixtureWith(RelayLimits{}, func(*Carrier) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/addons/audience" {
					_, _ = w.Write([]byte(`{"audience":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`))
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Disposition", `attachment; filename="r.bin"`)
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(200)
				_ = http.NewResponseController(w).Flush()
				panic(http.ErrAbortHandler)
			})
		})
		w, e := relay(env, "front-a")
		Expect(e).To(HaveOccurred())
		Expect(w.Body.Len()).To(BeZero())
		Expect(w.Header().Get("Content-Disposition")).To(BeEmpty())
		Expect(w.Header().Get("Content-Type")).To(BeEmpty())
	})
})
