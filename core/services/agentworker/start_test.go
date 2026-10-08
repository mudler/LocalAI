package agentworker_test

import (
	"context"
	"net"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/agentworker"
	mcpremote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

var _ = Describe("Starting the control plane of an agent worker", func() {
	start := func(token string) *agentworker.Runtime {
		GinkgoHelper()
		rt, err := agentworker.Start(GinkgoT().Context(), agentworker.Options{
			// Nothing listens here. A frontend that is away is not an error of
			// Start: the tunnel retries with a backoff.
			FrontendURL:  "http://127.0.0.1:1",
			NodeID:       "node-1",
			TunnelToken:  func() string { return "tunnel-credential" },
			ControlToken: token,
			Handler: agentworker.Handler(agentworker.Config{
				MCPTool: agentworker.Unary(func(context.Context, mcpremote.MCPToolRequest) mcpremote.MCPToolResponse {
					return mcpremote.MCPToolResponse{Result: "ok"}
				}),
			}, nil),
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(rt.Close)
		return rt
	}

	call := func(rt *agentworker.Runtime, bearer string) *http.Response {
		GinkgoHelper()
		req, err := http.NewRequest(http.MethodPost, "http://"+rt.Addr()+workerctl.PathOf(workerctl.VerbMCPToolExecute), strings.NewReader(`{}`))
		Expect(err).ToNot(HaveOccurred())
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(resp.Body.Close)
		return resp
	}

	It("binds loopback only, on a port it picks", func() {
		rt := start("secret")
		host, port, err := net.SplitHostPort(rt.Addr())
		Expect(err).ToNot(HaveOccurred())
		Expect(net.ParseIP(host).IsLoopback()).To(BeTrue())
		Expect(port).ToNot(Equal("0"))
	})

	It("serves a verb behind the bearer token and refuses a request without it", func() {
		rt := start("secret")
		Expect(call(rt, "secret").StatusCode).To(Equal(http.StatusOK))
		Expect(call(rt, "wrong").StatusCode).To(Equal(http.StatusUnauthorized))
		Expect(call(rt, "").StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("reports a tunnel that is not connected as not connected, and does not fail to start", func() {
		rt := start("secret")
		Expect(rt.Connected()).To(BeFalse())
	})

	It("stops serving on Close, and Close is safe on nil and twice", func() {
		rt := start("secret")
		addr := rt.Addr()
		Expect(rt.Close()).To(Succeed())
		_, err := net.Dial("tcp", addr)
		Expect(err).To(HaveOccurred())
		var none *agentworker.Runtime
		Expect(none.Close()).To(Succeed())
		Expect(none.Connected()).To(BeFalse())
	})

	It("refuses to start with no handler or no token function", func() {
		_, err := agentworker.Start(GinkgoT().Context(), agentworker.Options{FrontendURL: "http://127.0.0.1:1", NodeID: "n", TunnelToken: func() string { return "t" }, ControlToken: "secret"})
		Expect(err).To(HaveOccurred())
		_, err = agentworker.Start(GinkgoT().Context(), agentworker.Options{FrontendURL: "http://127.0.0.1:1", NodeID: "n", Handler: http.NotFoundHandler(), ControlToken: "secret"})
		Expect(err).To(HaveOccurred())
	})

	It("refuses to start with no control token, because the loopback port would be open to any process of the host", func() {
		_, err := agentworker.Start(GinkgoT().Context(), agentworker.Options{
			FrontendURL: "http://127.0.0.1:1", NodeID: "n", TunnelToken: func() string { return "t" },
			Handler: http.NotFoundHandler(),
		})
		Expect(err).To(MatchError(ContainSubstring("LOCALAI_REGISTRATION_TOKEN")))
	})

	It("refuses a frontend URL that cannot be dialled", func() {
		_, err := agentworker.Start(GinkgoT().Context(), agentworker.Options{
			FrontendURL: "ftp://frontend", NodeID: "n", TunnelToken: func() string { return "t" }, Handler: http.NotFoundHandler(),
			ControlToken: "secret",
		})
		Expect(err).To(HaveOccurred())
	})
})
