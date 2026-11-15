// SPDX-License-Identifier: MIT
package tools_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/contacts/internal/store"
	"github.com/mudler/LocalAI/addons/contacts/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

// The runtime behind the real protocol handler: the envelope checks, the
// routes of read and act, and the owner taken from the authority.
var _ = Describe("contacts over the add-on protocol", func() {
	var srv *httptest.Server
	post := func(path string, e contract.Envelope) (int, map[string]any) {
		GinkgoHelper()
		body, err := json.Marshal(e)
		Expect(err).NotTo(HaveOccurred())
		req, _ := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("X-LocalAI-Incarnation", "inc")
		res, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = res.Body.Close() }()
		var out struct {
			Payload map[string]any `json:"payload"`
		}
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out.Payload
	}
	BeforeEach(func() {
		st, err := store.Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = st.Close() })
		startup := protocol.Startup{
			Registration: protocol.Registration{AddonID: "contacts", Generation: 1, Incarnation: "inc", Protocol: 1},
			Credential:   "secret", Limits: contract.DefaultLimits(),
		}
		srv = httptest.NewServer(protocol.Handler(startup, tools.New(st)))
		DeferCleanup(srv.Close)
	})

	It("adds a contact on /act and reads it back on /read, for that user only", func() {
		code, out := post("/act", env("ann", "act", "act", map[string]any{"tool": "contact-create", "name": "Marco Rossi"}))
		Expect(code).To(Equal(http.StatusOK))
		Expect(out["ok"]).To(BeTrue())
		find := func(user string) []any {
			code, out := post("/read", env(user, "read", "read", map[string]any{"tool": "contacts-search", "query": "marco"}))
			Expect(code).To(Equal(http.StatusOK))
			return out["contacts"].([]any)
		}
		Expect(find("ann")).To(HaveLen(1))
		Expect(find("bob")).To(BeEmpty())
	})

	It("refuses a write sent on the read route and a read sent on the act route", func() {
		code, _ := post("/read", env("ann", "read", "read", map[string]any{"tool": "contact-delete", "id": "x"}))
		Expect(code).To(Equal(http.StatusBadRequest))
		code, _ = post("/act", env("ann", "act", "act", map[string]any{"tool": "contacts-search"}))
		Expect(code).To(Equal(http.StatusBadRequest))
	})

	It("needs the credential of the incarnation", func() {
		body, _ := json.Marshal(env("ann", "read", "read", map[string]any{"tool": "contacts-search"}))
		res, err := http.Post(srv.URL+"/read", "application/json", bytes.NewReader(body))
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = res.Body.Close() }()
		Expect(res.StatusCode).To(Equal(http.StatusUnauthorized))
	})
})
