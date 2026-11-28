// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestUIFixtures(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "UI fixture controls")
}

type fixtureArtifactStub struct {
	status func(context.Context, contract.Actor, string) (gateway.Artifact, error)
	cancel func(context.Context, contract.Actor, string) (gateway.Artifact, error)
}

func (s fixtureArtifactStub) Status(ctx context.Context, actor contract.Actor, id string) (gateway.Artifact, error) {
	return s.status(ctx, actor, id)
}

func (s fixtureArtifactStub) Cancel(ctx context.Context, actor contract.Actor, id string) (gateway.Artifact, error) {
	return s.cancel(ctx, actor, id)
}

var _ = Describe("artifact fixture settlement", func() {
	var f *addonsFixture
	var store fixtureArtifactStub
	var complete gateway.Artifact
	var actor contract.Actor
	var cancelled bool
	request := func() *http.Request {
		return httptest.NewRequest(http.MethodPost, "/_fixture/artifacts/settle", strings.NewReader(`{"user":"editor","id":"a0123456789abcdef0123456789abcdef"}`))
	}
	BeforeEach(func() {
		actor = contract.Actor{UserID: "editor", CredentialID: "key:editor", Mode: "named-user"}
		complete = gateway.Artifact{ID: "a0123456789abcdef0123456789abcdef", State: gateway.ArtifactComplete}
		cancelled = false
		store = fixtureArtifactStub{
			status: func(_ context.Context, got contract.Actor, id string) (gateway.Artifact, error) {
				Expect(got).To(Equal(actor))
				Expect(id).To(Equal(complete.ID))
				return complete, nil
			},
			cancel: func(context.Context, contract.Actor, string) (gateway.Artifact, error) {
				cancelled = true
				return complete, nil
			},
		}
		f = &addonsFixture{artifactControl: &fixtureArtifactControl{store: &store, actors: map[string]contract.Actor{"editor": actor}}}
	})

	It("waits for a complete file's worker before reporting settlement", func() {
		entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		DeferCleanup(unblock)
		store.cancel = func(ctx context.Context, got contract.Actor, id string) (gateway.Artifact, error) {
			Expect(got).To(Equal(actor))
			Expect(id).To(Equal(complete.ID))
			_, bounded := ctx.Deadline()
			Expect(bounded).To(BeTrue())
			close(entered)
			<-release
			return complete, nil
		}
		response := httptest.NewRecorder()
		go func() { defer GinkgoRecover(); defer close(done); f.settleArtifact(response, request()) }()
		Eventually(entered).Should(BeClosed())
		select {
		case <-done:
			Fail("settlement returned before the worker left")
		default:
		}
		unblock()
		Eventually(done).Should(BeClosed())
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(ContainSubstring(`"state":"complete"`))
	})

	It("refuses unfinished and failed files without cancelling them", func() {
		for _, state := range []gateway.ArtifactState{gateway.ArtifactTransferring, gateway.ArtifactFailed, gateway.ArtifactCancelled} {
			complete.State = state
			response := httptest.NewRecorder()
			f.settleArtifact(response, request())
			Expect(response.Code).To(Equal(http.StatusConflict))
			Expect(cancelled).To(BeFalse())
		}
	})

	It("refuses a missing fixture, unknown principal and malformed control request", func() {
		for _, body := range []string{`{"user":"other","id":"x"}`, `{"user":"editor","id":"x","extra":true}`, `{`} {
			response := httptest.NewRecorder()
			f.settleArtifact(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
			Expect(response.Code).To(BeNumerically(">=", 400))
			Expect(cancelled).To(BeFalse())
		}
		response := httptest.NewRecorder()
		f.settleArtifact(response, httptest.NewRequest(http.MethodGet, "/", nil))
		Expect(response.Code).To(Equal(http.StatusMethodNotAllowed))
		f.artifactControl = nil
		response = httptest.NewRecorder()
		f.settleArtifact(response, request())
		Expect(response.Code).To(Equal(http.StatusServiceUnavailable))
	})

	It("fails when the store cannot settle the file or no longer returns complete", func() {
		for _, failStatus := range []bool{true, false} {
			broken := func(context.Context, contract.Actor, string) (gateway.Artifact, error) {
				return gateway.Artifact{}, errors.New("store unavailable")
			}
			if failStatus {
				store.status = broken
			} else {
				store.status = func(context.Context, contract.Actor, string) (gateway.Artifact, error) { return complete, nil }
				store.cancel = broken
			}
			response := httptest.NewRecorder()
			f.settleArtifact(response, request())
			Expect(response.Code).To(Equal(http.StatusConflict))
		}
		store.cancel = func(context.Context, contract.Actor, string) (gateway.Artifact, error) {
			return gateway.Artifact{State: gateway.ArtifactCancelled}, nil
		}
		response := httptest.NewRecorder()
		f.settleArtifact(response, request())
		Expect(response.Code).To(Equal(http.StatusConflict))
	})

	It("stops waiting when the control request is cancelled", func() {
		entered, done := make(chan struct{}), make(chan struct{})
		store.cancel = func(ctx context.Context, _ contract.Actor, _ string) (gateway.Artifact, error) {
			close(entered)
			<-ctx.Done()
			return gateway.Artifact{}, ctx.Err()
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		response := httptest.NewRecorder()
		go func() {
			defer GinkgoRecover()
			defer close(done)
			f.settleArtifact(response, request().WithContext(ctx))
		}()
		Eventually(entered).Should(BeClosed())
		cancel()
		Eventually(done, time.Second).Should(BeClosed())
		Expect(response.Code).To(Equal(http.StatusConflict))
	})
})
