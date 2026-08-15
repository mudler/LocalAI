package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("MCP server status discovery", func() {
	It("keeps configured servers visible when connection fails and retries them", func() {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		}))
		DeferCleanup(server.Close)

		modelName := "unavailable-mcp-status-test"
		DeferCleanup(func() { CloseMCPSessions(modelName) })
		remote := config.MCPGenericConfig[config.MCPRemoteServers]{
			Servers: config.MCPRemoteServers{
				"ordino": {URL: server.URL},
			},
		}

		sessions, err := NamedSessionsFromMCPConfig(modelName, remote, config.MCPGenericConfig[config.MCPSTDIOServers]{}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(sessions).To(HaveLen(1))
		Expect(sessions[0].Name).To(Equal("ordino"))
		Expect(sessions[0].Type).To(Equal("remote"))
		Expect(sessions[0].Session).To(BeNil())
		Expect(sessions[0].Error).To(ContainSubstring("connection failed"))

		servers, err := ListMCPServers(context.Background(), sessions)
		Expect(err).NotTo(HaveOccurred())
		Expect(servers).To(HaveLen(1))
		Expect(servers[0].Name).To(Equal("ordino"))
		Expect(servers[0].Error).To(ContainSubstring("connection failed"))

		tools, err := DiscoverMCPTools(context.Background(), sessions)
		Expect(err).NotTo(HaveOccurred())
		Expect(tools).To(BeEmpty())

		_, err = NamedSessionsFromMCPConfig(modelName, remote, config.MCPGenericConfig[config.MCPSTDIOServers]{}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(requests.Load()).To(BeNumerically(">=", 2), "failed sessions should be retried instead of cached forever")
	})
})
