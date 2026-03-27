package distributed_test

import (
	"context"
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

	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
)

var _ = Describe("MCP NATS Routing", Label("Distributed"), func() {
	var (
		ctx           context.Context
		natsContainer *tcnats.NATSContainer
		nc            *messaging.Client
	)

	BeforeEach(func() {
		ctx = context.Background()

		var err error
		natsContainer, err = tcnats.Run(ctx, "nats:2-alpine")
		Expect(err).ToNot(HaveOccurred())

		natsURL, err := natsContainer.ConnectionString(ctx)
		Expect(err).ToNot(HaveOccurred())

		nc, err = messaging.New(natsURL)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		if nc != nil {
			nc.Close()
		}
		if natsContainer != nil {
			natsContainer.Terminate(ctx)
		}
	})

	Context("MCP Tool Execution via NATS", func() {
		It("should execute MCP tool call via NATS request-reply", func() {
			// Mock worker: subscribe to tool execute requests
			sub, err := nc.QueueSubscribeReply(messaging.SubjectMCPToolExecute, messaging.QueueAgentWorkers, func(data []byte, reply func([]byte)) {
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

			time.Sleep(100 * time.Millisecond)

			// Frontend side: set NATS client and call remote
			mcpTools.SetMCPNATSClient(nc)
			defer mcpTools.SetMCPNATSClient(nil)

			result, err := mcpTools.ExecuteMCPToolCallRemote(
				ctx,
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
			sub, err := nc.QueueSubscribeReply(messaging.SubjectMCPToolExecute, messaging.QueueAgentWorkers, func(data []byte, reply func([]byte)) {
				resp, _ := json.Marshal(mcpRemote.MCPToolResponse{
					Error: "tool 'unknown' not found",
				})
				reply(resp)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			mcpTools.SetMCPNATSClient(nc)
			defer mcpTools.SetMCPNATSClient(nil)

			_, err = mcpTools.ExecuteMCPToolCallRemote(
				ctx,
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
			sub, err := nc.QueueSubscribeReply(messaging.SubjectMCPDiscovery, messaging.QueueAgentWorkers, func(data []byte, reply func([]byte)) {
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

			time.Sleep(100 * time.Millisecond)

			mcpTools.SetMCPNATSClient(nc)
			defer mcpTools.SetMCPNATSClient(nil)

			result, err := mcpTools.DiscoverMCPToolsRemote(
				ctx,
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

	Context("Distributed Mode Detection", func() {
		It("should report distributed mode based on NATS client", func() {
			// Before setting NATS client
			mcpTools.SetMCPNATSClient(nil)
			Expect(mcpTools.IsDistributed()).To(BeFalse())

			// After setting NATS client
			mcpTools.SetMCPNATSClient(nc)
			Expect(mcpTools.IsDistributed()).To(BeTrue())

			// Cleanup
			mcpTools.SetMCPNATSClient(nil)
			Expect(mcpTools.IsDistributed()).To(BeFalse())
		})
	})

	Context("QueueSubscribeReply", func() {
		It("should support queue subscribe with request-reply round-trip", func() {
			// Subscribe with queue group
			sub, err := nc.QueueSubscribeReply("test.echo", "echo-workers", func(data []byte, reply func([]byte)) {
				// Echo back the request data with a prefix
				reply(append([]byte("echo:"), data...))
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			// Send request and wait for reply
			replyData, err := nc.Request("test.echo", []byte("hello"), 5*time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(replyData)).To(Equal("echo:hello"))
		})

		It("should load-balance requests across queue subscribers", func() {
			var worker1Count, worker2Count atomic.Int32

			sub1, _ := nc.QueueSubscribeReply("test.lb", "lb-workers", func(data []byte, reply func([]byte)) {
				worker1Count.Add(1)
				reply([]byte("w1"))
			})
			defer sub1.Unsubscribe()

			sub2, _ := nc.QueueSubscribeReply("test.lb", "lb-workers", func(data []byte, reply func([]byte)) {
				worker2Count.Add(1)
				reply([]byte("w2"))
			})
			defer sub2.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			// Send multiple requests
			for i := 0; i < 10; i++ {
				_, err := nc.Request("test.lb", []byte("req"), 5*time.Second)
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
