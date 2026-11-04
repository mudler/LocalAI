// SPDX-License-Identifier: MIT
package protocol_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/mudler/LocalAI/addons/internal/idle"
	. "github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type limitedRuntime struct{ idle.Runtime }

func (limitedRuntime) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, fmt.Errorf("service: %w", &contract.ErrRateLimited{RetryAfter: 61})
}

var _ = Describe("Handler and a rate limited read", func() {
	It("answers 429 with the wait in Retry-After and a fixed body", func() {
		startup := Startup{Registration: Registration{AddonID: "sample", Generation: 1, Incarnation: "inc", Protocol: 1}, Credential: "secret", Limits: contract.DefaultLimits()}
		env := contract.Envelope{
			Request:   contract.Request{AddonID: "sample", OperationID: "op", Generation: 1, Action: "read", Arguments: json.RawMessage(`{}`)},
			Authority: contract.Authority{Actor: contract.Actor{UserID: "u", CredentialID: "c", Mode: "named-user"}, Scope: "read", ExpiresAt: time.Now().Add(time.Minute)},
		}
		b, _ := json.Marshal(env)
		req := httptest.NewRequest(http.MethodPost, "/read", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("X-LocalAI-Incarnation", "inc")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		Handler(startup, limitedRuntime{}).ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusTooManyRequests))
		Expect(rec.Header().Get("Retry-After")).To(Equal("61"))
		Expect(rec.Body.String()).To(Equal("rate_limited\n"))
	})
})
