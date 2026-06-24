package localai

import (
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/onsi/gomega"
)

func TestUnavailableMCPServersIncludesEveryConfiguredServer(t *testing.T) {
	g := gomega.NewWithT(t)
	remote := config.MCPGenericConfig[config.MCPRemoteServers]{
		Servers: config.MCPRemoteServers{
			"zeta":  {URL: "http://zeta/mcp"},
			"alpha": {URL: "http://alpha/mcp"},
		},
	}
	stdio := config.MCPGenericConfig[config.MCPSTDIOServers]{
		Servers: config.MCPSTDIOServers{
			"worker": {Command: "mcp-worker"},
		},
	}

	servers := unavailableMCPServers(remote, stdio, "remote discovery failed: timeout")
	g.Expect(servers).To(gomega.HaveLen(3))
	g.Expect([]string{
		servers[0].Type + ":" + servers[0].Name,
		servers[1].Type + ":" + servers[1].Name,
		servers[2].Type + ":" + servers[2].Name,
	}).To(gomega.Equal([]string{"remote:alpha", "remote:zeta", "stdio:worker"}))
	for _, server := range servers {
		g.Expect(server.Error).To(gomega.Equal("remote discovery failed: timeout"))
	}
}
