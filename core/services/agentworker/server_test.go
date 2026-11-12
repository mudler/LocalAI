package agentworker_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/agentworker"
	mcpremote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

func post(srv *httptest.Server, verb string, body string) *http.Response {
	GinkgoHelper()
	resp, err := http.Post(srv.URL+workerctl.PathOf(verb), "application/json", strings.NewReader(body))
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(resp.Body.Close)
	return resp
}

func readBody(resp *http.Response) string {
	GinkgoHelper()
	b, err := io.ReadAll(resp.Body)
	Expect(err).ToNot(HaveOccurred())
	return string(b)
}

var _ = Describe("The control plane of an agent worker", func() {

	serve := func(cfg agentworker.Config) *httptest.Server {
		GinkgoHelper()
		srv := httptest.NewServer(agentworker.Handler(cfg))
		DeferCleanup(srv.Close)
		return srv
	}

	Describe("the unary verbs", func() {
		It("answers a tool call with the reply of the handler on a 200", func() {
			var got mcpremote.MCPToolRequest
			srv := serve(agentworker.Config{MCPTool: agentworker.Unary(func(_ context.Context, req mcpremote.MCPToolRequest) mcpremote.MCPToolResponse {
				got = req
				return mcpremote.MCPToolResponse{Result: "done"}
			})})
			resp := post(srv, workerctl.VerbMCPToolExecute, `{"model_name":"m","tool_name":"t"}`)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(readBody(resp)).To(MatchJSON(`{"result":"done"}`))
			Expect(got.ModelName).To(Equal("m"))
			Expect(got.ToolName).To(Equal("t"))
		})

		It("answers a discovery with the reply of the handler", func() {
			srv := serve(agentworker.Config{MCPDiscovery: agentworker.Unary(func(_ context.Context, req mcpremote.MCPDiscoveryRequest) mcpremote.MCPDiscoveryResponse {
				return mcpremote.MCPDiscoveryResponse{Servers: []mcpremote.MCPServerInfo{{Name: req.ModelName}}}
			})})
			resp := post(srv, workerctl.VerbMCPDiscovery, `{"model_name":"m"}`)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(readBody(resp)).To(ContainSubstring(`"name":"m"`))
		})

		It("keeps the error of a tool in the reply, on a 200, because it is the answer of the worker", func() {
			srv := serve(agentworker.Config{MCPTool: agentworker.Unary(func(context.Context, mcpremote.MCPToolRequest) mcpremote.MCPToolResponse {
				return mcpremote.MCPToolResponse{Error: "the tool failed"}
			})})
			resp := post(srv, workerctl.VerbMCPToolExecute, `{}`)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(readBody(resp)).To(MatchJSON(`{"error":"the tool failed"}`))
		})

		It("refuses a body that does not decode with 400, and never with a reply", func() {
			called := false
			srv := serve(agentworker.Config{MCPTool: agentworker.Unary(func(context.Context, mcpremote.MCPToolRequest) mcpremote.MCPToolResponse {
				called = true
				return mcpremote.MCPToolResponse{}
			})})
			resp := post(srv, workerctl.VerbMCPToolExecute, `not json`)
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
			Expect(called).To(BeFalse())
		})

		It("refuses anything but a POST", func() {
			srv := serve(agentworker.Config{MCPTool: agentworker.Unary(func(context.Context, mcpremote.MCPToolRequest) mcpremote.MCPToolResponse {
				return mcpremote.MCPToolResponse{}
			})})
			resp, err := http.Get(srv.URL + workerctl.PathOf(workerctl.VerbMCPToolExecute))
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(resp.Body.Close)
			Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
		})

		It("answers 404 with a body that says so for a verb it does not serve", func() {
			srv := serve(agentworker.Config{})
			for _, verb := range []string{workerctl.VerbMCPToolExecute, workerctl.VerbMCPDiscovery, workerctl.VerbBackendInstall, workerctl.VerbModelStop} {
				resp := post(srv, verb, `{}`)
				Expect(resp.StatusCode).To(Equal(http.StatusNotFound), verb)
				Expect(readBody(resp)).To(ContainSubstring("unknown worker control path"), verb)
			}
		})
	})

	Describe("the stop of a backend", func() {
		It("answers 204 with no body, as a backend worker does, and drops the sessions of the backend", func() {
			var stopped []string
			srv := serve(agentworker.Config{BackendStop: func(_ context.Context, req workerctl.BackendStopRequest) error {
				stopped = append(stopped, req.Backend)
				return nil
			}})
			resp := post(srv, workerctl.VerbBackendStop, `{"backend":"llama"}`)
			Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
			Expect(readBody(resp)).To(BeEmpty())
			Expect(stopped).To(Equal([]string{"llama"}))
		})

		It("finishes the cleanup when the caller leaves", func() {
			ctxErr := make(chan error, 1)
			srv := serve(agentworker.Config{BackendStop: func(ctx context.Context, _ workerctl.BackendStopRequest) error {
				time.Sleep(50 * time.Millisecond)
				ctxErr <- ctx.Err()
				return nil
			}})
			ctx, cancel := context.WithCancel(context.Background())
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+workerctl.PathOf(workerctl.VerbBackendStop), strings.NewReader(`{"backend":"b"}`))
			go func() { time.Sleep(10 * time.Millisecond); cancel() }()
			_, _ = http.DefaultClient.Do(req)
			Eventually(ctxErr).Should(Receive(BeNil()))
		})

		It("refuses a body that does not decode with 400", func() {
			srv := serve(agentworker.Config{BackendStop: func(context.Context, workerctl.BackendStopRequest) error { return nil }})
			Expect(post(srv, workerctl.VerbBackendStop, `{`).StatusCode).To(Equal(http.StatusBadRequest))
		})

		It("answers 500 when the cleanup fails, and not a reply", func() {
			srv := serve(agentworker.Config{BackendStop: func(context.Context, workerctl.BackendStopRequest) error { return errors.New("boom") }})
			resp := post(srv, workerctl.VerbBackendStop, `{"backend":"b"}`)
			Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
		})
	})
})
