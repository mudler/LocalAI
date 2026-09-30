package nodes

import (
	"context"
	"errors"
	"time"

	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	mcpremote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
)

// The frontend reads ErrNoRoute as "no agent worker could be offered this", so
// a slow worker (timeout) or a worker's own error reply must never look like
// one. The timeout rules pin today's behaviour: only the deadline bounds the
// wait, and a client that goes away does not abort a tool call in flight.
var _ = Describe("NATS agent control", func() {
	var (
		mc *scriptedMessagingClient
		ac *NATSAgentControl
	)

	BeforeEach(func() {
		mc = newScriptedMessagingClient()
		ac = NewNATSAgentControl(mc)
	})

	lastTimeout := func() time.Duration {
		mc.mu.Lock()
		defer mc.mu.Unlock()
		Expect(mc.calls).ToNot(BeEmpty())
		return mc.calls[len(mc.calls)-1].Timeout
	}

	type verb struct {
		subject    string
		fallback   time.Duration
		call       func(ctx context.Context) (string, error)
		errorReply any
	}

	verbs := []struct {
		name string
		get  func() verb
	}{
		{"tool execution", func() verb {
			return verb{
				subject:    messaging.SubjectMCPToolExecute,
				fallback:   config.DefaultMCPToolTimeout,
				errorReply: mcpremote.MCPToolResponse{Error: "tool 'x' not found"},
				call: func(ctx context.Context) (string, error) {
					reply, err := ac.ExecuteMCPTool(ctx, mcpremote.MCPToolRequest{ModelName: "m", ToolName: "x"})
					if reply == nil {
						return "", err
					}
					return reply.Error, err
				},
			}
		}},
		{"discovery", func() verb {
			return verb{
				subject:    messaging.SubjectMCPDiscovery,
				fallback:   config.DefaultMCPDiscoveryTimeout,
				errorReply: mcpremote.MCPDiscoveryResponse{Error: "no MCP servers"},
				call: func(ctx context.Context) (string, error) {
					reply, err := ac.DiscoverMCPTools(ctx, mcpremote.MCPDiscoveryRequest{ModelName: "m"})
					if reply == nil {
						return "", err
					}
					return reply.Error, err
				},
			}
		}},
	}

	for _, v := range verbs {
		Context(v.name, func() {
			var vb verb
			BeforeEach(func() { vb = v.get() })

			It("reports no responders as ErrNoRoute without the carrier sentinel", func() {
				mc.scriptNoResponders(vb.subject)
				_, err := vb.call(context.Background())
				Expect(errors.Is(err, ErrNoRoute)).To(BeTrue(), "got %v", err)
				Expect(errors.Is(err, nats.ErrNoResponders)).To(BeFalse())
			})

			It("does not report a timeout as ErrNoRoute", func() {
				mc.scriptErr(vb.subject, nats.ErrTimeout)
				_, err := vb.call(context.Background())
				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, ErrNoRoute)).To(BeFalse())
			})

			It("returns a reply carrying the worker's error with a nil error", func() {
				mc.scriptReply(vb.subject, vb.errorReply)
				workerErr, err := vb.call(context.Background())
				Expect(err).ToNot(HaveOccurred())
				Expect(workerErr).ToNot(BeEmpty())
			})

			It("bounds the request by the context deadline", func() {
				mc.scriptReply(vb.subject, vb.errorReply)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, err := vb.call(ctx)
				Expect(err).ToNot(HaveOccurred())
				t := lastTimeout()
				Expect(t).To(BeNumerically(">", 0))
				Expect(t).To(BeNumerically("<=", 5*time.Second))
			})

			It("falls back to the verb's default budget without a deadline", func() {
				mc.scriptReply(vb.subject, vb.errorReply)
				_, err := vb.call(context.Background())
				Expect(err).ToNot(HaveOccurred())
				Expect(lastTimeout()).To(Equal(vb.fallback))
			})

			It("still issues the request when the context is cancelled but time is left", func() {
				mc.scriptReply(vb.subject, vb.errorReply)
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				cancel()
				_, err := vb.call(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(lastTimeout()).To(BeNumerically(">", 0))
				mc.mu.Lock()
				defer mc.mu.Unlock()
				Expect(mc.calls[len(mc.calls)-1].Subject).To(Equal(vb.subject))
			})
		})
	}
})
