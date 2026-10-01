package distributed_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/functions"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("MCP NATS Routing", Label("Distributed"), func() {
	var (
		infra *TestInfra
	)

	BeforeEach(func() {
		infra = SetupNATSOnly()
	})

	Context("MCP Tool Execution via NATS", func() {
		It("should execute MCP tool call via NATS request-reply", func() {
			// Mock worker: subscribe to tool execute requests
			sub, err := infra.NC.QueueSubscribeReply(messaging.SubjectMCPToolExecute, messaging.QueueAgentWorkers, func(data []byte, reply func([]byte)) {
				var req mcpRemote.MCPToolRequest
				Expect(json.Unmarshal(data, &req)).To(Succeed())
				Expect(req.ModelName).To(Equal("test-model"))
				Expect(req.ToolName).To(Equal("weather"))
				Expect(req.Arguments).To(HaveKeyWithValue("city", "London"))

				resp, _ := json.Marshal(mcpRemote.MCPToolResponse{
					Result: "Weather in London: 15°C, cloudy",
				})
				reply(resp)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			FlushNATS(infra.NC)

			// Frontend side: pass NATS client and call remote
			result, err := mcpTools.ExecuteMCPToolCallRemote(
				infra.Ctx,
				nodes.NewNATSAgentControl(infra.NC),
				"test-model",
				config.MCPGenericConfig[config.MCPRemoteServers]{},
				config.MCPGenericConfig[config.MCPSTDIOServers]{},
				"weather",
				`{"city": "London"}`,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("Weather in London: 15°C, cloudy"))
		})

		It("should propagate remote MCP tool errors", func() {
			sub, err := infra.NC.QueueSubscribeReply(messaging.SubjectMCPToolExecute, messaging.QueueAgentWorkers, func(data []byte, reply func([]byte)) {
				resp, _ := json.Marshal(mcpRemote.MCPToolResponse{
					Error: "tool 'unknown' not found",
				})
				reply(resp)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			FlushNATS(infra.NC)

			_, err = mcpTools.ExecuteMCPToolCallRemote(
				infra.Ctx,
				nodes.NewNATSAgentControl(infra.NC),
				"test-model",
				config.MCPGenericConfig[config.MCPRemoteServers]{},
				config.MCPGenericConfig[config.MCPSTDIOServers]{},
				"unknown",
				"{}",
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tool 'unknown' not found"))
		})
	})

	Context("MCP Discovery via NATS", func() {
		It("should discover MCP servers via NATS request-reply", func() {
			sub, err := infra.NC.QueueSubscribeReply(messaging.SubjectMCPDiscovery, messaging.QueueAgentWorkers, func(data []byte, reply func([]byte)) {
				var req mcpRemote.MCPDiscoveryRequest
				Expect(json.Unmarshal(data, &req)).To(Succeed())
				Expect(req.ModelName).To(Equal("discovery-model"))

				resp, _ := json.Marshal(mcpRemote.MCPDiscoveryResponse{
					Servers: []mcpRemote.MCPServerInfo{
						{Name: "weather-server", Type: "remote", Tools: []string{"get_weather", "get_forecast"}},
						{Name: "db-server", Type: "stdio", Tools: []string{"query_db"}},
					},
					Tools: []mcpRemote.MCPToolDef{
						{ServerName: "weather-server", ToolName: "get_weather", Function: functions.Function{Name: "get_weather", Description: "Get weather"}},
						{ServerName: "weather-server", ToolName: "get_forecast", Function: functions.Function{Name: "get_forecast", Description: "Get forecast"}},
						{ServerName: "db-server", ToolName: "query_db", Function: functions.Function{Name: "query_db", Description: "Query database"}},
					},
				})
				reply(resp)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			FlushNATS(infra.NC)

			result, err := mcpTools.DiscoverMCPToolsRemote(
				infra.Ctx,
				nodes.NewNATSAgentControl(infra.NC),
				"discovery-model",
				config.MCPGenericConfig[config.MCPRemoteServers]{},
				config.MCPGenericConfig[config.MCPSTDIOServers]{},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Servers).To(HaveLen(2))
			Expect(result.Servers[0].Name).To(Equal("weather-server"))
			Expect(result.Servers[0].Tools).To(ConsistOf("get_weather", "get_forecast"))
			Expect(result.Tools).To(HaveLen(3))
			Expect(result.Tools[2].ToolName).To(Equal("query_db"))
		})
	})

	Context("Agent RPC server", func() {
		It("round trips tool and discovery requests through the agent RPC server", func() {
			toolReqs := make(chan mcpRemote.MCPToolRequest, 2)
			discoveryReqs := make(chan mcpRemote.MCPDiscoveryRequest, 1)

			srv := nodes.NewNATSAgentRPCServer(infra.NC, "e2e-agent-node")
			Expect(srv.ServeMCPTool(func(_ context.Context, req mcpRemote.MCPToolRequest) mcpRemote.MCPToolResponse {
				toolReqs <- req
				return mcpRemote.MCPToolResponse{Result: "ran " + req.ToolName}
			})).To(Succeed())
			Expect(srv.ServeMCPDiscovery(func(_ context.Context, req mcpRemote.MCPDiscoveryRequest) mcpRemote.MCPDiscoveryResponse {
				discoveryReqs <- req
				return mcpRemote.MCPDiscoveryResponse{
					Servers: []mcpRemote.MCPServerInfo{{Name: "weather-server", Type: "remote", Tools: []string{"get_weather"}}},
				}
			})).To(Succeed())
			FlushNATS(infra.NC)

			control := nodes.NewNATSAgentControl(infra.NC)

			toolResp, err := control.ExecuteMCPTool(infra.Ctx, mcpRemote.MCPToolRequest{
				ModelName: "rpc-model",
				ToolName:  "get_weather",
				Arguments: map[string]any{"city": "Rome"},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(toolResp.Result).To(Equal("ran get_weather"))
			Expect(toolResp.Error).To(BeEmpty())
			var gotTool mcpRemote.MCPToolRequest
			Eventually(toolReqs).Should(Receive(&gotTool))
			Expect(gotTool.ModelName).To(Equal("rpc-model"))
			Expect(gotTool.ToolName).To(Equal("get_weather"))
			Expect(gotTool.Arguments).To(HaveKeyWithValue("city", "Rome"))

			discoveryResp, err := control.DiscoverMCPTools(infra.Ctx, mcpRemote.MCPDiscoveryRequest{ModelName: "rpc-model"})
			Expect(err).ToNot(HaveOccurred())
			Expect(discoveryResp.Servers).To(HaveLen(1))
			Expect(discoveryResp.Servers[0].Name).To(Equal("weather-server"))
			Expect(discoveryResp.Servers[0].Tools).To(ConsistOf("get_weather"))
			var gotDiscovery mcpRemote.MCPDiscoveryRequest
			Eventually(discoveryReqs).Should(Receive(&gotDiscovery))
			Expect(gotDiscovery.ModelName).To(Equal("rpc-model"))

			// AgentControl only sends valid JSON, so the undecodable body goes
			// on the wire directly: the server must still answer, or the
			// requester would wait out its whole budget.
			raw, err := infra.NC.Request(messaging.SubjectMCPToolExecute, []byte("{not json"), 5*time.Second)
			Expect(err).ToNot(HaveOccurred())
			var refused mcpRemote.MCPToolResponse
			Expect(json.Unmarshal(raw, &refused)).To(Succeed())
			Expect(strings.HasPrefix(refused.Error, "unmarshal error: ")).To(BeTrue(), "got %q", refused.Error)
			Expect(refused.Result).To(BeEmpty())
			Consistently(toolReqs, 200*time.Millisecond).ShouldNot(Receive())
		})
	})

	Context("QueueSubscribeReply", func() {
		It("should support queue subscribe with request-reply round-trip", func() {
			// Subscribe with queue group
			sub, err := infra.NC.QueueSubscribeReply(messaging.SubjectNodeBackendList("e2e-echo"), "echo-workers", func(data []byte, reply func([]byte)) {
				// Echo back the request data with a prefix
				reply(append([]byte("echo:"), data...))
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			FlushNATS(infra.NC)

			// Send request and wait for reply
			replyData, err := infra.NC.Request(messaging.SubjectNodeBackendList("e2e-echo"), []byte("hello"), 5*time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(replyData)).To(Equal("echo:hello"))
		})

		It("should load-balance requests across queue subscribers", func() {
			var worker1Count, worker2Count atomic.Int32

			sub1, _ := infra.NC.QueueSubscribeReply(messaging.SubjectNodeBackendList("e2e-lb"), "lb-workers", func(data []byte, reply func([]byte)) {
				worker1Count.Add(1)
				reply([]byte("w1"))
			})
			defer sub1.Unsubscribe()

			sub2, _ := infra.NC.QueueSubscribeReply(messaging.SubjectNodeBackendList("e2e-lb"), "lb-workers", func(data []byte, reply func([]byte)) {
				worker2Count.Add(1)
				reply([]byte("w2"))
			})
			defer sub2.Unsubscribe()

			FlushNATS(infra.NC)

			// Send multiple requests
			for range 10 {
				_, err := infra.NC.Request(messaging.SubjectNodeBackendList("e2e-lb"), []byte("req"), 5*time.Second)
				Expect(err).ToNot(HaveOccurred())
			}

			// Both workers should have handled some requests
			total := worker1Count.Load() + worker2Count.Load()
			Expect(total).To(Equal(int32(10)))
			// NATS typically distributes evenly, but we just check both got work
			Expect(worker1Count.Load()).To(BeNumerically(">", 0))
			Expect(worker2Count.Load()).To(BeNumerically(">", 0))
		})
	})
})
