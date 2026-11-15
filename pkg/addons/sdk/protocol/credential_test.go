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

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/idle"
	. "github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type rejectingRuntime struct{ idle.Runtime }

func (rejectingRuntime) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, fmt.Errorf("service: %w", contract.ErrCredentialRejected)
}

var _ = Describe("Handler and a credential the service rejected", func() {
	It("answers 401 credential_rejected, the answer the host renews the credential for", func() {
		startup := Startup{Registration: Registration{AddonID: "sample", Generation: 1, Incarnation: "inc", Protocol: 1}, Credential: "secret", Limits: contract.DefaultLimits()}
		env := contract.Envelope{
			Request:   contract.Request{AddonID: "sample", OperationID: "op", Generation: 1, Action: "read", Arguments: json.RawMessage(`{}`), Credential: "access"},
			Authority: contract.Authority{Actor: contract.Actor{UserID: "u", CredentialID: "c", Mode: "named-user"}, Scope: "read", ExpiresAt: time.Now().Add(time.Minute)},
		}
		b, _ := json.Marshal(env)
		req := httptest.NewRequest(http.MethodPost, "/read", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("X-LocalAI-Incarnation", "inc")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		Handler(startup, rejectingRuntime{}).ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		Expect(rec.Body.String()).To(Equal("credential_rejected\n"))
	})
})
