// SPDX-License-Identifier: MIT
package protocol_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/internal/idle"
	. "github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Handler and action tools", func() {
	startup := func() Startup {
		return Startup{
			Registration: Registration{AddonID: "sandbox", Generation: 1, Incarnation: "inc", Protocol: 1, Capabilities: contract.Capabilities{Actions: true}},
			Credential:   "secret", Limits: contract.DefaultLimits(),
		}
	}
	post := func(h http.Handler, path, scope, action string) *httptest.ResponseRecorder {
		env := contract.Envelope{
			Request: contract.Request{AddonID: "sandbox", OperationID: "op1", Generation: 1, Action: action, Arguments: json.RawMessage(`{"command":"echo"}`)},
			Authority: contract.Authority{Actor: contract.Actor{UserID: "alice", CredentialID: "k", Mode: "named-user"}, Scope: scope,
				ExpiresAt: time.Now().Add(time.Minute)},
		}
		b, _ := json.Marshal(env)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("X-LocalAI-Incarnation", "inc")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	It("serves /act for a runtime that implements ActRuntime, under the act scope only", func() {
		h := Handler(startup(), actingRuntime{})
		rec := post(h, "/act", "act", "act")
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("alice"))
		Expect(post(h, "/act", "read", "act").Code).To(BeNumerically(">=", 400))
		Expect(post(h, "/act", "act", "read").Code).To(BeNumerically(">=", 400))
	})
	It("has no /act for a runtime that does not act", func() {
		h := Handler(startup(), idle.Runtime{})
		Expect(post(h, "/act", "act", "act").Code).To(Equal(http.StatusNotFound))
	})
})

var _ = Describe("Handler and refusals", func() {
	read := func(err error) *httptest.ResponseRecorder {
		st := Startup{Registration: Registration{AddonID: "sandbox", Generation: 1, Incarnation: "inc", Protocol: 1}, Credential: "secret", Limits: contract.DefaultLimits()}
		env := contract.Envelope{
			Request:   contract.Request{AddonID: "sandbox", OperationID: "op1", Generation: 1, Action: "read", Arguments: json.RawMessage(`{}`)},
			Authority: contract.Authority{Actor: contract.Actor{UserID: "alice", CredentialID: "k", Mode: "named-user"}, Scope: "read", ExpiresAt: time.Now().Add(time.Minute)},
		}
		b, _ := json.Marshal(env)
		req := httptest.NewRequest(http.MethodPost, "/read", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("X-LocalAI-Incarnation", "inc")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		Handler(st, failingRuntime{err: err}).ServeHTTP(rec, req)
		return rec
	}
	It("sends the reason of a bad request on, so the owner can tell the person what to fix", func() {
		rec := read(fmt.Errorf("%w: %w: quantization engine llama-cpp-quantization is not installed", ErrBadRequest, ErrBadRequest))
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("quantization engine llama-cpp-quantization is not installed"))
	})
	It("keeps the plain answer for a bad request without a reason", func() {
		rec := read(ErrBadRequest)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(strings.TrimSpace(rec.Body.String())).To(Equal("invalid request"))
	})
})

type failingRuntime struct {
	idle.Runtime
	err error
}

func (f failingRuntime) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, f.err
}

// actingRuntime answers an act call with the user it ran for.
type actingRuntime struct{ idle.Runtime }

func (actingRuntime) Act(_ context.Context, e contract.Envelope) (contract.Result, error) {
	b, _ := json.Marshal(map[string]string{"ranFor": e.Authority.Actor.UserID})
	return contract.Result{Payload: b}, nil
}
