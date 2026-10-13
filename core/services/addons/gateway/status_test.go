// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type erroringRuntime struct{ err error }

func (r erroringRuntime) Runtime(context.Context, string) (addonruntime.Instance, error) {
	return addonruntime.Instance{}, r.err
}

type shortOutput struct{ resolved }

func (shortOutput) Output(context.Context, contract.Actor, contract.Request, string) (contract.OutputDescription, error) {
	return contract.OutputDescription{Handle: "private", Name: "r.bin", MediaType: "application/octet-stream", Size: 8}, nil
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
	file := contract.Request{AddonID: "sample", OperationID: "op", Generation: 1, Action: "read"}
	relay := func(env *fixtureEnv, front string) (*httptest.ResponseRecorder, error) {
		w := httptest.NewRecorder()
		e := env.carriers[front].Relay(context.Background(), frontActor, RelayRequest{Request: file, Kind: "file", Handle: "h"}, w)
		return w, e
	}
	runtimeFailure := func(err error) *Gateway {
		return &Gateway{Revalidate: ok, Admission: &admitted{}, Resolver: resolved{}, Runtime: erroringRuntime{err}}
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
		admission := &admitted{}
		client := runtimeFixture{admission: admission, open: func(context.Context) io.ReadCloser { return io.NopCloser(strings.NewReader("")) }}
		env := owner(&Gateway{Revalidate: ok, Admission: admission, Resolver: shortOutput{}, Runtime: currentFixture{client}})
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
	It("reports a mutation refused by the owner as unavailable, not as an unknown outcome", func() {
		// The owner has no lifecycle manager, so it refuses before any execution.
		env := owner(&Gateway{Revalidate: ok})
		install := contract.Request{AddonID: "sample", OperationID: "install", Action: "install", Arguments: []byte(`{"sourceId":"s","version":"1.0.0"}`)}
		_, e := env.carriers["front-a"].Dispatch(context.Background(), frontActor, install)
		Expect(e).To(Equal(ErrUnavailable))
		var unknown *OutcomeUnknown
		Expect(errors.As(e, &unknown)).To(BeFalse())
	})
	It("does not leak attachment headers into an error when the first read fails", func() {
		env := carrierFixtureWith(RelayLimits{}, func(*Carrier) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/addons/audience" {
					w.Write([]byte(`{"audience":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`))
					return
				}
				io.Copy(io.Discard, r.Body)
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
