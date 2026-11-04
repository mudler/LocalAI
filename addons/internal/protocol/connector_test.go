// SPDX-License-Identifier: MIT
package protocol_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/mudler/LocalAI/addons/internal/idle"
	. "github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Handler and the connector route", func() {
	startup := Startup{
		Registration: Registration{AddonID: "telegram", Generation: 1, Incarnation: "inc", Protocol: 1},
		Credential:   "secret", Limits: contract.DefaultLimits(),
	}
	post := func(h http.Handler, bearer string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/connector", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("X-LocalAI-Incarnation", "inc")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	It("serves /connector for a runtime that implements ConnectorRuntime, behind the incarnation credential", func() {
		h := Handler(startup, echoConnector{})
		rec := post(h, "secret", contract.ConnectorRequest{Op: "me", Kind: "telegram", Token: "t"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring(`"botUsername":"me"`))
		Expect(post(h, "wrong", contract.ConnectorRequest{Op: "me"}).Code).To(Equal(http.StatusUnauthorized))
	})
	It("answers a refusal of the add-on as a bad request", func() {
		rec := post(Handler(startup, echoConnector{}), "secret", contract.ConnectorRequest{Op: "nope", Kind: "telegram", Token: "t"})
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})
	It("has no /connector for a runtime that carries no connector", func() {
		Expect(post(Handler(startup, idle.Runtime{}), "secret", contract.ConnectorRequest{Op: "me"}).Code).To(Equal(http.StatusNotFound))
	})
})

type echoConnector struct{ idle.Runtime }

func (echoConnector) Connector(_ context.Context, r contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	if r.Op != "me" {
		return contract.ConnectorResponse{}, contract.ErrRefused
	}
	return contract.ConnectorResponse{BotUsername: "me"}, nil
}
