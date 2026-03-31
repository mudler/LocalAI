package distributed_test

import (
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
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
				infra.NC,
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
				infra.NC,
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
				infra.NC,
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

	Context("QueueSubscribeReply", func() {
		It("should support queue subscribe with request-reply round-trip", func() {
			// Subscribe with queue group
			sub, err := infra.NC.QueueSubscribeReply("test.echo", "echo-workers", func(data []byte, reply func([]byte)) {
				// Echo back the request data with a prefix
				reply(append([]byte("echo:"), data...))
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			FlushNATS(infra.NC)

			// Send request and wait for reply
			replyData, err := infra.NC.Request("test.echo", []byte("hello"), 5*time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(replyData)).To(Equal("echo:hello"))
		})

		It("should load-balance requests across queue subscribers", func() {
			var worker1Count, worker2Count atomic.Int32

			sub1, _ := infra.NC.QueueSubscribeReply("test.lb", "lb-workers", func(data []byte, reply func([]byte)) {
				worker1Count.Add(1)
				reply([]byte("w1"))
			})
			defer sub1.Unsubscribe()

			sub2, _ := infra.NC.QueueSubscribeReply("test.lb", "lb-workers", func(data []byte, reply func([]byte)) {
				worker2Count.Add(1)
				reply([]byte("w2"))
			})
			defer sub2.Unsubscribe()

			FlushNATS(infra.NC)

			// Send multiple requests
			for range 10 {
				_, err := infra.NC.Request("test.lb", []byte("req"), 5*time.Second)
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
