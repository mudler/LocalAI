// SPDX-License-Identifier: MIT
package openresponses

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The PII request middleware is HTTP middleware, so messages arriving over
// the Responses WebSocket never pass through it. These specs drive the real
// socket loop and prove a PII-enabled model is refused there instead of
// being served unfiltered.
var _ = Describe("WebSocket Responses PII guard", func() {
	// dial runs the real connection loop behind a test server with the given
	// policy and returns the first event answering one prewarm request
	// (generate=false, so no backend is needed).
	dial := func(policy pii.PolicyResolver) schema.ORStreamEvent {
		GinkgoHelper()
		dir := GinkgoT().TempDir()
		appCfg := config.NewApplicationConfig()
		appCfg.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
		cl := config.NewModelConfigLoader(dir)
		m := config.ModelConfig{Name: "m", Backend: "test"}
		m.Model = "m.gguf"
		cl.ReplaceModelConfigs([]config.ModelConfig{m})

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ws, err := wsUpgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer ws.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			handleWebSocketConnection(ctx, &lockedConn{Conn: ws}, "owner", cl, nil, nil, appCfg, policy)
		}))
		DeferCleanup(srv.Close)

		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(conn.Close)
		Expect(conn.SetReadDeadline(time.Now().Add(10 * time.Second))).To(Succeed())
		Expect(conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"type":"response.create","generate":false,"store":false,"model":"m","input":"my key is sk-secret"}`))).To(Succeed())

		var ev schema.ORStreamEvent
		Expect(conn.ReadJSON(&ev)).To(Succeed())
		return ev
	}

	It("refuses a model whose PII policy is enabled", func() {
		ev := dial(func(any) (bool, []string) { return true, []string{"det"} })
		Expect(ev.Type).To(Equal("error"))
		Expect(ev.Error).ToNot(BeNil())
		Expect(ev.Error.Type).To(Equal("pii_unsupported_transport"))
		Expect(ev.Error.Message).ToNot(ContainSubstring("sk-secret"), "the refusal must not echo the request content")
	})

	It("serves a model without a PII policy", func() {
		ev := dial(func(any) (bool, []string) { return false, nil })
		Expect(ev.Type).To(Equal("response.created"))
	})

	It("serves a PII-enabled model with no detectors, as the HTTP middleware does", func() {
		ev := dial(func(any) (bool, []string) { return true, nil })
		Expect(ev.Type).To(Equal("response.created"))
	})
})
