// SPDX-License-Identifier: MIT

package localai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// The MCP prompts and resources endpoints work only against sessions THIS
// process holds, and in distributed mode it holds none. That is a hole older
// than the removal of the message bus: tools and discovery had a carrier to an
// agent worker and these two never did, and nothing in this programme gives
// them one.
//
// What these specs pin is that the hole is now HONEST. The old answer was 200
// with an empty list, which reads as "this model has no prompts": a client
// cannot act on it, and an operator reading it has no reason to look further.
//
// Each endpoint is asserted separately on purpose. The refusal is one function
// but four CALLS, and a call site that lost its call would still leave the
// other three green.
var _ = Describe("MCP prompts and resources in distributed mode", func() {
	distributed := &config.ApplicationConfig{}
	distributed.Distributed.Enabled = true

	standalone := &config.ApplicationConfig{}

	// loader with no models at all. Every endpoint below looks a model up, and
	// the refusal has to come FIRST: a 404 for an unknown model would be a
	// different, and wrong, story about the same request.
	emptyLoader := config.NewModelConfigLoader("")

	call := func(h echo.HandlerFunc, method, path, body string, params map[string]string) *httptest.ResponseRecorder {
		GinkgoHelper()
		e := echo.New()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		names := make([]string, 0, len(params))
		values := make([]string, 0, len(params))
		for k, v := range params {
			names = append(names, k)
			values = append(values, v)
		}
		c.SetParamNames(names...)
		c.SetParamValues(values...)
		Expect(h(c)).To(Succeed())
		return rec
	}

	// assertRefused checks the STATUS and the BODY. A status-only assertion
	// cannot tell a 501 with an empty body from the empty 200 this replaces,
	// and the body is the whole reason the answer is worth giving.
	assertRefused := func(rec *httptest.ResponseRecorder, surface string) {
		GinkgoHelper()
		Expect(rec.Code).To(Equal(http.StatusNotImplemented))
		var body map[string]string
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body["error"]).To(ContainSubstring(surface))
		Expect(body["error"]).To(ContainSubstring("distributed mode"))
		Expect(body["error"]).To(ContainSubstring("agent worker"))
	}

	It("refuses to list prompts rather than answering with an empty list", func() {
		assertRefused(call(MCPPromptsEndpoint(emptyLoader, distributed),
			http.MethodGet, "/v1/mcp/prompts/m", "", map[string]string{"model": "m"}), "prompts")
	})

	It("refuses to expand a prompt", func() {
		assertRefused(call(MCPGetPromptEndpoint(emptyLoader, distributed),
			http.MethodPost, "/v1/mcp/prompts/m/p", `{"arguments":{}}`,
			map[string]string{"model": "m", "prompt": "p"}), "prompts")
	})

	It("refuses to list resources rather than answering with an empty list", func() {
		assertRefused(call(MCPResourcesEndpoint(emptyLoader, distributed),
			http.MethodGet, "/v1/mcp/resources/m", "", map[string]string{"model": "m"}), "resources")
	})

	It("refuses to read a resource", func() {
		assertRefused(call(MCPReadResourceEndpoint(emptyLoader, distributed),
			http.MethodPost, "/v1/mcp/resources/m/read", `{"uri":"file:///x"}`,
			map[string]string{"model": "m"}), "resources")
	})

	It("does not refuse in a standalone deployment, where the sessions are here", func() {
		// The negative control. A guard that fired unconditionally would pass
		// every assertion above and break MCP prompts for every single-binary
		// deployment, which is where they actually work.
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/v1/mcp/prompts/m", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("model")
		c.SetParamValues("m")

		err := MCPPromptsEndpoint(emptyLoader, standalone)(c)
		// The model does not exist, so this fails on the lookup. What matters
		// is that it got PAST the guard: a 501 here would mean the guard fired
		// with distributed mode off.
		Expect(err).To(HaveOccurred())
		Expect(rec.Code).ToNot(Equal(http.StatusNotImplemented))
	})
})
