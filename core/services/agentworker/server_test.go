package agentworker_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/agentworker"
	mcpremote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
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
	var work *agentworker.Work

	serve := func(cfg agentworker.Config) *httptest.Server {
		GinkgoHelper()
		srv := httptest.NewServer(agentworker.Handler(cfg, work))
		DeferCleanup(srv.Close)
		return srv
	}

	BeforeEach(func() { work = agentworker.NewWork() })

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

// envelopes reads the lines of a streaming response.
func envelopes(resp *http.Response) []workerctl.Envelope {
	GinkgoHelper()
	var out []workerctl.Envelope
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var env workerctl.Envelope
		Expect(json.Unmarshal(sc.Bytes(), &env)).To(Succeed(), sc.Text())
		out = append(out, env)
	}
	return out
}

var _ = Describe("The runs of an agent worker", func() {
	var (
		work *agentworker.Work
		srv  *httptest.Server
	)

	BeforeEach(func() {
		work = agentworker.NewWork()
		srv = httptest.NewServer(agentworker.Handler(agentworker.Config{}, work))
		DeferCleanup(srv.Close)
	})

	consume := func(kind messaging.WorkKind, max int, h messaging.WorkHandler) messaging.Subscription {
		GinkgoHelper()
		sub, err := work.Consume(GinkgoT().Context(), kind, max, h)
		Expect(err).ToNot(HaveOccurred())
		return sub
	}

	It("hands the payload and a publisher to the handler of the kind, and streams what it publishes", func() {
		var payload []byte
		consume(messaging.WorkAgentRun, 0, func(_ context.Context, p []byte, events messaging.Publisher) error {
			payload = p
			Expect(events.Publish("agent.helper.events.alice", map[string]string{"event_type": "json_message"})).To(Succeed())
			Expect(events.Publish("agent.helper.events.alice", map[string]string{"event_type": "json_message_status"})).To(Succeed())
			return nil
		})
		resp := post(srv, workerctl.VerbAgentExecute, `{"agent_name":"helper"}`)
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Type")).To(Equal(workerctl.ContentTypeStream))
		lines := envelopes(resp)
		Expect(lines).To(HaveLen(3))
		Expect(lines[0].Subject).To(Equal("agent.helper.events.alice"))
		Expect(string(lines[0].Progress)).To(MatchJSON(`{"event_type":"json_message"}`))
		Expect(string(lines[1].Progress)).To(MatchJSON(`{"event_type":"json_message_status"}`))
		Expect(lines[2].Reply).ToNot(BeNil(), "the reply is the last line")
		Expect(string(payload)).To(Equal(`{"agent_name":"helper"}`))
	})

	It("takes the reply line of an MCP CI job from the result it published, and still streams that result", func() {
		consume(messaging.WorkMCPCI, 1, func(_ context.Context, _ []byte, events messaging.Publisher) error {
			Expect(events.Publish(messaging.SubjectJobProgress("j1"), map[string]string{"job_id": "j1", "status": "running"})).To(Succeed())
			Expect(events.Publish(messaging.SubjectJobResult("j1"), map[string]string{"job_id": "j1", "status": "completed", "result": "42"})).To(Succeed())
			return nil
		})
		lines := envelopes(post(srv, workerctl.VerbMCPCIRun, `{"job_id":"j1"}`))
		Expect(lines).To(HaveLen(3))
		Expect(lines[1].Subject).To(Equal(messaging.SubjectJobResult("j1")))
		Expect(string(lines[2].Reply)).To(MatchJSON(`{"job_id":"j1","status":"completed","result":"42"}`))
	})

	It("ends a run that published no result with an empty reply, which is still a reply", func() {
		consume(messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error { return nil })
		lines := envelopes(post(srv, workerctl.VerbAgentExecute, `{}`))
		Expect(lines).To(HaveLen(1))
		Expect(string(lines[0].Reply)).To(MatchJSON(`{}`))
	})

	It("answers 500 and no reply when the handler cannot serve the run before it published anything", func() {
		consume(messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error { return errors.New("cannot serve") })
		resp := post(srv, workerctl.VerbAgentExecute, `{}`)
		Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
		Expect(readBody(resp)).To(ContainSubstring("cannot serve"))
	})

	It("ends the body with no reply line when the handler fails after it published, so the caller learns nothing", func() {
		consume(messaging.WorkAgentRun, 0, func(_ context.Context, _ []byte, events messaging.Publisher) error {
			_ = events.Publish("agent.a.events.u", map[string]string{"k": "v"})
			return errors.New("lost the plot")
		})
		resp := post(srv, workerctl.VerbAgentExecute, `{}`)
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		lines := envelopes(resp)
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Reply).To(BeNil())
	})

	It("survives a handler that panics, and ends that run with no reply", func() {
		consume(messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error { panic("bug") })
		resp := post(srv, workerctl.VerbAgentExecute, `{}`)
		Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
		consume(messaging.WorkMCPCI, 0, func(context.Context, []byte, messaging.Publisher) error { return nil })
		Expect(post(srv, workerctl.VerbMCPCIRun, `{}`).StatusCode).To(Equal(http.StatusOK))
	})

	It("refuses a body that cannot be a payload with 400 before the handler runs", func() {
		// An empty body is no payload: the handler decodes the rest.
		called := atomic.Bool{}
		consume(messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error { called.Store(true); return nil })
		resp, err := http.Get(srv.URL + workerctl.PathOf(workerctl.VerbAgentExecute))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(resp.Body.Close)
		Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
		Expect(called.Load()).To(BeFalse())
	})

	Describe("the limit on concurrent runs", func() {
		It("answers busy when the limit is reached, and takes a run again once one ends", func() {
			started := make(chan struct{}, 4)
			release := make(chan struct{})
			consume(messaging.WorkMCPCI, 1, func(context.Context, []byte, messaging.Publisher) error {
				started <- struct{}{}
				<-release
				return nil
			})
			first := make(chan *http.Response, 1)
			go func() {
				resp, err := http.Post(srv.URL+workerctl.PathOf(workerctl.VerbMCPCIRun), "application/json", strings.NewReader(`{}`))
				Expect(err).ToNot(HaveOccurred())
				first <- resp
			}()
			Eventually(started).Should(Receive())

			resp := post(srv, workerctl.VerbMCPCIRun, `{}`)
			Expect(resp.StatusCode).To(Equal(http.StatusServiceUnavailable))
			Expect(workerctl.IsBusy(resp.StatusCode, readBody(resp))).To(BeTrue())

			close(release)
			var done *http.Response
			Eventually(first).Should(Receive(&done))
			_ = envelopes(done)
			Expect(post(srv, workerctl.VerbMCPCIRun, `{}`).StatusCode).To(Equal(http.StatusOK))
		})

		It("runs as many as asked at once, and any number for 0", func() {
			for _, max := range []int{3, 0} {
				work := agentworker.NewWork()
				local := httptest.NewServer(agentworker.Handler(agentworker.Config{}, work))
				var running, peak atomic.Int32
				release := make(chan struct{})
				_, err := work.Consume(GinkgoT().Context(), messaging.WorkAgentRun, max, func(context.Context, []byte, messaging.Publisher) error {
					n := running.Add(1)
					for {
						p := peak.Load()
						if n <= p || peak.CompareAndSwap(p, n) {
							break
						}
					}
					<-release
					running.Add(-1)
					return nil
				})
				Expect(err).ToNot(HaveOccurred())
				var wg sync.WaitGroup
				for range 3 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						resp, err := http.Post(local.URL+workerctl.PathOf(workerctl.VerbAgentExecute), "application/json", strings.NewReader(`{}`))
						if err == nil {
							_, _ = io.Copy(io.Discard, resp.Body)
							_ = resp.Body.Close()
						}
					}()
				}
				Eventually(running.Load, "5s").Should(BeEquivalentTo(3), "max=%d", max)
				close(release)
				wg.Wait()
				local.Close()
			}
		})

		It("answers busy for a kind that nothing consumes", func() {
			resp := post(srv, workerctl.VerbAgentExecute, `{}`)
			Expect(resp.StatusCode).To(Equal(http.StatusServiceUnavailable))
			Expect(workerctl.IsBusy(resp.StatusCode, readBody(resp))).To(BeTrue())
		})
	})

	Describe("the context of a run", func() {
		It("ends when the caller leaves", func() {
			started := make(chan struct{})
			ended := make(chan error, 1)
			consume(messaging.WorkAgentRun, 0, func(ctx context.Context, _ []byte, _ messaging.Publisher) error {
				close(started)
				<-ctx.Done()
				ended <- ctx.Err()
				return ctx.Err()
			})
			ctx, cancel := context.WithCancel(context.Background())
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+workerctl.PathOf(workerctl.VerbAgentExecute), bytes.NewReader([]byte(`{}`)))
			go func() { _, _ = http.DefaultClient.Do(req) }()
			Eventually(started).Should(BeClosed())
			cancel()
			Eventually(ended, "5s").Should(Receive(MatchError(context.Canceled)))
		})

		It("ends when the worker stops consuming", func() {
			started := make(chan struct{})
			ended := make(chan error, 1)
			ctx, stop := context.WithCancel(context.Background())
			_, err := work.Consume(ctx, messaging.WorkAgentRun, 0, func(ctx context.Context, _ []byte, _ messaging.Publisher) error {
				close(started)
				<-ctx.Done()
				ended <- ctx.Err()
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			go func() {
				resp, err := http.Post(srv.URL+workerctl.PathOf(workerctl.VerbAgentExecute), "application/json", strings.NewReader(`{}`))
				if err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}()
			Eventually(started).Should(BeClosed())
			stop()
			Eventually(ended, "5s").Should(Receive(MatchError(context.Canceled)))
		})
	})

	Describe("a subscription", func() {
		It("refuses a kind that no verb serves", func() {
			_, err := work.Consume(GinkgoT().Context(), messaging.WorkTask, 0, func(context.Context, []byte, messaging.Publisher) error { return nil })
			Expect(err).To(HaveOccurred())
			_, err = work.Consume(GinkgoT().Context(), "nonsense", 0, func(context.Context, []byte, messaging.Publisher) error { return nil })
			Expect(err).To(HaveOccurred())
		})

		It("refuses a second consumer of the same kind", func() {
			consume(messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error { return nil })
			_, err := work.Consume(GinkgoT().Context(), messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error { return nil })
			Expect(err).To(HaveOccurred())
		})

		It("stops taking runs on Unsubscribe and waits for the runs in flight", func() {
			started := make(chan struct{})
			release := make(chan struct{})
			sub := consume(messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error {
				close(started)
				<-release
				return nil
			})
			go func() {
				resp, err := http.Post(srv.URL+workerctl.PathOf(workerctl.VerbAgentExecute), "application/json", strings.NewReader(`{}`))
				if err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}()
			Eventually(started).Should(BeClosed())

			unsubscribed := make(chan error, 1)
			go func() { unsubscribed <- sub.Unsubscribe() }()
			Consistently(unsubscribed, "100ms").ShouldNot(Receive(), "the run in flight is waited for")
			Expect(post(srv, workerctl.VerbAgentExecute, `{}`).StatusCode).To(Equal(http.StatusServiceUnavailable), "no new run is taken")
			close(release)
			Eventually(unsubscribed).Should(Receive(Succeed()))
		})
	})
})
