package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	mcpremote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
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

// failingSubscribeBus refuses every subscription, so a spec can check that a
// server passes the bus error up and names the verb that failed to start.
type failingSubscribeBus struct {
	*testutil.FakeBus
	err error
}

func (f *failingSubscribeBus) Subscribe(string, func([]byte)) (messaging.Subscription, error) {
	return nil, f.err
}

func (f *failingSubscribeBus) QueueSubscribeReply(string, string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return nil, f.err
}

// The worker half must answer exactly as the agent worker always has: the
// frontend reads an undecodable request's reply text, the queue group decides
// which workers compete, and the handler context must outlive a shutdown.
var _ = Describe("NATS agent RPC server", func() {
	var (
		bus *testutil.FakeBus
		srv *NATSAgentRPCServer
	)

	BeforeEach(func() {
		bus = testutil.NewFakeBus()
		srv = NewNATSAgentRPCServer(bus, "n1")
	})

	type verb struct {
		subject string
		// serve registers a handler that records its context and decoded
		// request, then returns reply (blocking on gate when it is non-nil).
		serve func(reply any, gate <-chan struct{}, seen chan<- any, ctxs chan<- context.Context) error
		// request is a decodable request and reply a response the handler
		// returns; unmarshalErr decodes bad bytes the way the server must.
		request      any
		reply        any
		unmarshalErr func(bad []byte) string
		decodeReply  func(data []byte) (string, error)
	}

	verbs := []struct {
		name string
		v    verb
	}{
		{"mcp tool", verb{
			subject: messaging.SubjectMCPToolExecute,
			serve: func(reply any, gate <-chan struct{}, seen chan<- any, ctxs chan<- context.Context) error {
				return srv.ServeMCPTool(func(ctx context.Context, req mcpremote.MCPToolRequest) mcpremote.MCPToolResponse {
					ctxs <- ctx
					seen <- req
					if gate != nil {
						<-gate
					}
					return reply.(mcpremote.MCPToolResponse)
				})
			},
			request: mcpremote.MCPToolRequest{ModelName: "m", ToolName: "t", Arguments: map[string]any{"a": "b"}},
			reply:   mcpremote.MCPToolResponse{Result: "done", Error: "partial"},
			unmarshalErr: func(bad []byte) string {
				var r mcpremote.MCPToolRequest
				return json.Unmarshal(bad, &r).Error()
			},
			decodeReply: func(data []byte) (string, error) {
				var r mcpremote.MCPToolResponse
				err := json.Unmarshal(data, &r)
				return r.Error, err
			},
		}},
		{"mcp discovery", verb{
			subject: messaging.SubjectMCPDiscovery,
			serve: func(reply any, gate <-chan struct{}, seen chan<- any, ctxs chan<- context.Context) error {
				return srv.ServeMCPDiscovery(func(ctx context.Context, req mcpremote.MCPDiscoveryRequest) mcpremote.MCPDiscoveryResponse {
					ctxs <- ctx
					seen <- req
					if gate != nil {
						<-gate
					}
					return reply.(mcpremote.MCPDiscoveryResponse)
				})
			},
			request: mcpremote.MCPDiscoveryRequest{ModelName: "m"},
			reply: mcpremote.MCPDiscoveryResponse{
				Servers: []mcpremote.MCPServerInfo{{Name: "s", Type: "remote", Tools: []string{"t"}}},
				Tools:   []mcpremote.MCPToolDef{{ServerName: "s", ToolName: "t"}},
			},
			unmarshalErr: func(bad []byte) string {
				var r mcpremote.MCPDiscoveryRequest
				return json.Unmarshal(bad, &r).Error()
			},
			decodeReply: func(data []byte) (string, error) {
				var r mcpremote.MCPDiscoveryResponse
				err := json.Unmarshal(data, &r)
				return r.Error, err
			},
		}},
	}

	for _, entry := range verbs {
		v := entry.v
		Context(entry.name, func() {
			var (
				seen chan any
				ctxs chan context.Context
			)

			BeforeEach(func() {
				seen = make(chan any, 4)
				ctxs = make(chan context.Context, 4)
			})

			It("answers undecodable bytes with the unmarshal error and does not call the handler", func() {
				Expect(v.serve(v.reply, nil, seen, ctxs)).To(Succeed())
				bad := []byte("{not json")
				data, ok := bus.DeliverReply(v.subject, bad)
				Expect(ok).To(BeTrue(), "a bad request must still be answered")
				errText, err := v.decodeReply(data)
				Expect(err).ToNot(HaveOccurred())
				Expect(errText).To(HavePrefix("unmarshal error: "))
				Expect(errText).To(Equal("unmarshal error: " + v.unmarshalErr(bad)))
				Expect(seen).To(BeEmpty())
			})

			It("calls the handler with the decoded request and sends its reply verbatim", func() {
				Expect(v.serve(v.reply, nil, seen, ctxs)).To(Succeed())
				body, err := json.Marshal(v.request)
				Expect(err).ToNot(HaveOccurred())
				data, ok := bus.DeliverReply(v.subject, body)
				Expect(ok).To(BeTrue())
				Expect(seen).To(Receive(Equal(v.request)))
				want, err := json.Marshal(v.reply)
				Expect(err).ToNot(HaveOccurred())
				Expect(data).To(Equal(want))
			})

			It("joins the agent-workers queue group", func() {
				Expect(v.serve(v.reply, nil, seen, ctxs)).To(Succeed())
				Expect(bus.QueueGroups()).To(HaveKeyWithValue(v.subject, messaging.QueueAgentWorkers))
				Expect(messaging.QueueAgentWorkers).To(Equal("agent-workers"))
			})

			It("runs the handler on a context no parent can cancel", func() {
				Expect(v.serve(v.reply, nil, seen, ctxs)).To(Succeed())
				body, err := json.Marshal(v.request)
				Expect(err).ToNot(HaveOccurred())
				_, ok := bus.DeliverReply(v.subject, body)
				Expect(ok).To(BeTrue())
				var ctx context.Context
				Expect(ctxs).To(Receive(&ctx))
				Expect(ctx.Done()).To(BeNil(), "a cancellable context would abort an in-flight call on shutdown")
				Expect(ctx.Err()).ToNot(HaveOccurred())
				_, hasDeadline := ctx.Deadline()
				Expect(hasDeadline).To(BeFalse())
			})

			It("handles deliveries one at a time on the delivery goroutine", func() {
				gate := make(chan struct{})
				Expect(v.serve(v.reply, gate, seen, ctxs)).To(Succeed())
				body, err := json.Marshal(v.request)
				Expect(err).ToNot(HaveOccurred())

				// NATS delivers one subscription's messages in sequence on a
				// single goroutine; this loop stands in for it.
				var replies atomic.Int32
				done := make(chan struct{})
				go func() {
					defer GinkgoRecover()
					defer close(done)
					for range 2 {
						if _, ok := bus.DeliverReply(v.subject, body); ok {
							replies.Add(1)
						}
					}
				}()

				Eventually(seen).Should(Receive())
				Consistently(seen, 200*time.Millisecond).ShouldNot(Receive(), "the second request started before the first returned")
				Expect(replies.Load()).To(BeZero(), "the reply must be sent by the delivery call itself")

				gate <- struct{}{}
				Eventually(seen).Should(Receive())
				Expect(replies.Load()).To(Equal(int32(1)))
				gate <- struct{}{}
				Eventually(done).Should(BeClosed())
				Expect(replies.Load()).To(Equal(int32(2)))
			})

			It("returns a subscribe error naming the verb", func() {
				boom := errors.New("permission denied")
				failing := NewNATSAgentRPCServer(&failingSubscribeBus{FakeBus: bus, err: boom}, "n1")
				var err error
				if v.subject == messaging.SubjectMCPToolExecute {
					err = failing.ServeMCPTool(func(context.Context, mcpremote.MCPToolRequest) mcpremote.MCPToolResponse {
						return mcpremote.MCPToolResponse{}
					})
				} else {
					err = failing.ServeMCPDiscovery(func(context.Context, mcpremote.MCPDiscoveryRequest) mcpremote.MCPDiscoveryResponse {
						return mcpremote.MCPDiscoveryResponse{}
					})
				}
				Expect(err).To(MatchError(boom))
				Expect(err.Error()).To(ContainSubstring(entry.name))
			})
		})
	}

	Context("backend stop", func() {
		It("listens on the node's backend stop subject and never replies", func() {
			var got []string
			Expect(srv.ServeBackendStop(func(backend string) { got = append(got, backend) })).To(Succeed())

			subject := messaging.SubjectNodeBackendStop("n1")
			Expect(bus.Publish(subject, workerctl.BackendStopRequest{Backend: "llama"})).To(Succeed())
			Expect(got).To(Equal([]string{"llama"}))

			// A plain subscription has no reply to send: the frontend reads the
			// resulting timeout as success, so a reply would change its outcome.
			_, replied := bus.DeliverReply(subject, []byte(`{"backend":"llama"}`))
			Expect(replied).To(BeFalse())
			Expect(bus.QueueGroups()).ToNot(HaveKey(subject))
		})

		It("ignores a body it cannot decode", func() {
			called := false
			Expect(srv.ServeBackendStop(func(string) { called = true })).To(Succeed())
			Expect(bus.Publish(messaging.SubjectNodeBackendStop("n1"), "not an object")).To(Succeed())
			Expect(called).To(BeFalse())
		})

		It("does not hear another node's stop", func() {
			called := false
			Expect(srv.ServeBackendStop(func(string) { called = true })).To(Succeed())
			Expect(bus.Publish(messaging.SubjectNodeBackendStop("n2"), workerctl.BackendStopRequest{Backend: "llama"})).To(Succeed())
			Expect(called).To(BeFalse())
		})

		It("returns a subscribe error naming the verb", func() {
			boom := errors.New("permission denied")
			failing := NewNATSAgentRPCServer(&failingSubscribeBus{FakeBus: bus, err: boom}, "n1")
			err := failing.ServeBackendStop(func(string) {})
			Expect(err).To(MatchError(boom))
			Expect(err.Error()).To(ContainSubstring("backend stop"))
		})
	})
})
