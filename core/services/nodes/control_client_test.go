package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// The whole of this task is the table controlFailure implements: every way a
// control RPC can fail lands in exactly one of four conditions, and none of
// them may be reported as another. The rows, and what each permits:
//
//   - the WORKER'S OWN ANSWER, which a reap guard may act on;
//   - an UNREACHABLE PEER or a lost route, which nobody may act on;
//   - a SPENT BUDGET, which nobody may act on;
//   - the worker answering that it is OLDER than this frontend, which only the
//     legacy upgrade fallback may act on.
var _ = Describe("ControlClient", func() {
	Context("error mapping", func() {
		dialerReturning := func(err error) WorkerNetDialerFor {
			return func(string) func(context.Context, string, string) (net.Conn, error) {
				return func(context.Context, string, string) (net.Conn, error) { return nil, err }
			}
		}

		It("keeps a worker's own refusal matchable and does NOT wrap it as unroutable", func() {
			c := NewControlClient(dialerReturning(
				fmt.Errorf("%w: no such process", cluster.ErrStreamTargetUnavailable)), "tok")
			err := c.Call(context.Background(), "n1", workerctl.PathModelsRunning, struct{}{}, &struct{}{})
			Expect(cluster.IsWorkerAnswer(err)).To(BeTrue())
			Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeFalse())
		})

		DescribeTable("reports every non-answer as unroutable and never as a worker verdict",
			func(cause error) {
				c := NewControlClient(dialerReturning(cause), "tok")
				err := c.Call(context.Background(), "n1", workerctl.PathModelsRunning, struct{}{}, &struct{}{})
				Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeTrue())
				Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
			},
			Entry("no route", fmt.Errorf("reaching node: %w", cluster.ErrNoRoute)),
			Entry("no connection recorded", cluster.ErrNoConnection),
			Entry("peer unreachable", cluster.ErrPeerUnreachable),
			Entry("no relay path", cluster.ErrNoRelayPath),
			// The fourth refusal code. The worker DID send it, and it still
			// must not be a verdict: it is what a worker says when it learned
			// nothing, and those clear on their own.
			Entry("the worker learned nothing", fmt.Errorf("%w: late frame", cluster.ErrStreamNotServed)),
			Entry("a reply code this frontend does not know", errors.New(`tunnel stream refused with unrecognised code "from-the-future": x`)),
		)

		It("refuses without a dialer rather than reaching for an address", func() {
			c := NewControlClient(nil, "tok")
			err := c.Call(context.Background(), "n1", workerctl.PathModelsRunning, struct{}{}, &struct{}{})
			Expect(err).To(MatchError(ErrNoWorkerDialer))
			Expect(err).To(MatchError(ErrWorkerUnroutable))
		})

		It("refuses when the dialer has nothing for that node", func() {
			c := NewControlClient(func(string) func(context.Context, string, string) (net.Conn, error) {
				return nil
			}, "tok")
			err := c.Call(context.Background(), "n1", workerctl.PathModelsRunning, struct{}{}, &struct{}{})
			Expect(err).To(MatchError(ErrNoWorkerDialer))
		})

		It("reports a spent budget as unroutable, so nothing acts on an expiry", func() {
			expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()

			c := NewControlClient(dialerReturning(errors.New("never reached")), "tok")
			err := c.Call(expired, "n1", workerctl.PathModelsRunning, struct{}{}, &struct{}{})

			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
			Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeTrue())
			Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
		})

		// A timeout is not a verdict, and this is the ordering that enforces
		// it. Phase 2's final defect was a worker refusal that arrived in the
		// instant the budget expired and was reported as the worker's
		// non-transient answer, which reaps a row.
		//
		// Asserted on the mapping function directly, and deliberately so.
		// Nothing orders the two timers, so a spec that drove this through Call
		// could only ever produce one of the two orderings by luck: with a
		// context already spent, the HTTP client returns before the dialler is
		// even asked, and the refusal this is about never happens. Reaching for
		// the collapse through the public call was tried and left the suite
		// green under the mutation that removes the guard.
		DescribeTable("decides between a worker's refusal and a spent budget by the BUDGET first",
			func(spent bool, wantAnswer bool) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if spent {
					var stop context.CancelFunc
					ctx, stop = context.WithDeadline(ctx, time.Now().Add(-time.Second))
					defer stop()
				}
				refusal := fmt.Errorf("%w: no such process", cluster.ErrStreamTargetUnavailable)
				err := controlFailure(ctx, "n1", refusal)

				Expect(cluster.IsWorkerAnswer(err)).To(Equal(wantAnswer))
				Expect(errors.Is(err, context.DeadlineExceeded)).To(Equal(spent))
			},
			// The negative control: with budget left, the very same refusal is
			// the worker speaking and a reap guard may act on it. Without it
			// this table would pass on a mapping that never reports an answer.
			Entry("budget left: the worker spoke", false, true),
			Entry("budget spent: the caller's own timeout", true, false),
		)
	})

	Context("against a worker's HTTP answers", func() {
		var (
			srv     *httptest.Server
			handler http.HandlerFunc
			client  *ControlClient
		)

		BeforeEach(func() {
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handler(w, r)
			}))
			DeferCleanup(srv.Close)
			addr := srv.Listener.Addr().String()
			client = NewControlClient(func(string) func(context.Context, string, string) (net.Conn, error) {
				return func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "tcp", addr)
				}
			}, "tok")
		})

		It("addresses the node in the URL host and carries the bearer token", func() {
			var gotHost, gotAuth, gotPath, gotMethod string
			handler = func(w http.ResponseWriter, r *http.Request) {
				gotHost, gotAuth, gotPath, gotMethod = r.Host, r.Header.Get("Authorization"), r.URL.Path, r.Method
				_, _ = w.Write([]byte(`{}`))
			}
			Expect(client.Call(context.Background(), "n1", workerctl.PathModelsRunning, struct{}{}, &struct{}{})).To(Succeed())

			Expect(gotHost).To(Equal("n1" + unroutableHostSuffix))
			Expect(gotAuth).To(Equal("Bearer tok"))
			Expect(gotPath).To(Equal(workerctl.PathModelsRunning))
			// POST, because a control verb is a command: the worker refuses a
			// GET so a probe cannot fire one, and a client that sent GET would
			// be refused rather than served.
			Expect(gotMethod).To(Equal(http.MethodPost))
		})

		It("hands the worker's own reply back with its Error field intact", func() {
			// A verb's own failure is a 200 with Error set. The CALLER reads
			// it; this is not an error here, and reporting it as one would put
			// the worker's verdict in the bucket reserved for a broken link.
			handler = func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"success":false,"error":"disk full"}`))
			}
			var reply messaging.BackendDeleteReply
			Expect(client.Call(context.Background(), "n1", workerctl.PathBackendDelete, struct{}{}, &reply)).To(Succeed())
			Expect(reply.Success).To(BeFalse())
			Expect(reply.Error).To(Equal("disk full"))
		})

		It("accepts a 204 for the verbs that answer nothing", func() {
			handler = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
			Expect(client.Call(context.Background(), "n1", workerctl.PathNodeStop, struct{}{}, nil)).To(Succeed())
		})

		It("reports an unknown control verb as unsupported, and not as absence", func() {
			handler = func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "unknown worker control path "+r.URL.Path, http.StatusNotFound)
			}
			err := client.Call(context.Background(), "n1", workerctl.Prefix+"invented", struct{}{}, &struct{}{})
			Expect(err).To(MatchError(ErrWorkerControlUnsupported))
			Expect(errors.Is(err, cluster.ErrNoRoute)).To(BeFalse())
			Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
		})

		DescribeTable("reports a worker that failed to SERVE the request as unroutable, never as a verdict",
			func(status int) {
				handler = func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", status) }
				err := client.Call(context.Background(), "n1", workerctl.PathBackendList, struct{}{}, &struct{}{})
				Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeTrue())
				Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
				Expect(errors.Is(err, ErrWorkerControlUnsupported)).To(BeFalse())
			},
			Entry("the handler failed", http.StatusInternalServerError),
			Entry("the body could not be read", http.StatusBadRequest),
			Entry("the method was refused", http.StatusMethodNotAllowed),
			Entry("a proxy in the path", http.StatusBadGateway),
		)

		It("reports a reply it cannot decode as unroutable, not as an empty answer", func() {
			// An empty ModelsRunningReply says "this worker is running nothing",
			// which the reconciler acts on. It must never be manufactured from
			// a body that would not parse.
			handler = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`not json`)) }
			var reply messaging.ModelsRunningReply
			err := client.Call(context.Background(), "n1", workerctl.PathModelsRunning, struct{}{}, &reply)
			Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeTrue())
			Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
		})

		It("streams install progress in order and returns the terminal reply", func() {
			handler = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", workerctl.ContentTypeStream)
				enc := json.NewEncoder(w)
				_ = enc.Encode(workerctl.Envelope{Progress: json.RawMessage(`{"percentage":50}`)})
				_ = enc.Encode(workerctl.Envelope{Progress: json.RawMessage(`{"percentage":100}`)})
				_ = enc.Encode(workerctl.Envelope{Reply: json.RawMessage(`{"success":true}`)})
			}
			var seen []float64
			var reply messaging.BackendInstallReply
			err := client.CallStreaming(context.Background(), "n1", workerctl.PathBackendInstall,
				messaging.BackendInstallRequest{Backend: "mock", OpID: "op-1"}, &reply,
				func(ev messaging.BackendInstallProgressEvent) { seen = append(seen, ev.Percentage) })
			Expect(err).NotTo(HaveOccurred())
			Expect(seen).To(Equal([]float64{50, 100}))
			Expect(reply.Success).To(BeTrue())
		})

		It("stops reading at the reply line, so nothing after it can be taken for progress", func() {
			handler = func(w http.ResponseWriter, _ *http.Request) {
				enc := json.NewEncoder(w)
				_ = enc.Encode(workerctl.Envelope{Reply: json.RawMessage(`{"success":true}`)})
				_ = enc.Encode(workerctl.Envelope{Progress: json.RawMessage(`{"percentage":10}`)})
			}
			var seen []float64
			var reply messaging.BackendInstallReply
			Expect(client.CallStreaming(context.Background(), "n1", workerctl.PathBackendInstall,
				struct{}{}, &reply,
				func(ev messaging.BackendInstallProgressEvent) { seen = append(seen, ev.Percentage) })).To(Succeed())
			Expect(seen).To(BeEmpty())
			Expect(reply.Success).To(BeTrue())
		})

		It("reports a stream that ends before its reply line as unroutable, not as a failed install", func() {
			// A tunnel that dies mid-install must not be read as the worker
			// saying the install failed. This is the collapse the phase exists
			// to prevent, one layer up from where phase 2 fixed it.
			handler = func(w http.ResponseWriter, _ *http.Request) {
				enc := json.NewEncoder(w)
				_ = enc.Encode(workerctl.Envelope{Progress: json.RawMessage(`{"percentage":50}`)})
				hijackAndClose(w)
			}
			var reply messaging.BackendInstallReply
			err := client.CallStreaming(context.Background(), "n1", workerctl.PathBackendInstall,
				struct{}{}, &reply, nil)
			Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeTrue())
			Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
			Expect(reply.Success).To(BeFalse())
		})

		It("reports a stream with no lines at all as unroutable", func() {
			handler = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
			var reply messaging.BackendInstallReply
			err := client.CallStreaming(context.Background(), "n1", workerctl.PathBackendInstall,
				struct{}{}, &reply, nil)
			Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeTrue())
			Expect(errors.Is(err, io.ErrUnexpectedEOF)).To(BeTrue())
		})

		It("keeps going past a progress line it cannot read, since progress is transient", func() {
			handler = func(w http.ResponseWriter, _ *http.Request) {
				enc := json.NewEncoder(w)
				_ = enc.Encode(workerctl.Envelope{Progress: json.RawMessage(`{"percentage":"not a number"}`)})
				_ = enc.Encode(workerctl.Envelope{Progress: json.RawMessage(`{"percentage":70}`)})
				_ = enc.Encode(workerctl.Envelope{Reply: json.RawMessage(`{"success":true}`)})
			}
			var seen []float64
			var reply messaging.BackendInstallReply
			Expect(client.CallStreaming(context.Background(), "n1", workerctl.PathBackendInstall,
				struct{}{}, &reply,
				func(ev messaging.BackendInstallProgressEvent) { seen = append(seen, ev.Percentage) })).To(Succeed())
			Expect(seen).To(Equal([]float64{70}))
			Expect(reply.Success).To(BeTrue())
		})

		It("reports a streaming 404 as unsupported rather than as a truncated stream", func() {
			handler = func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "unknown worker control path "+r.URL.Path, http.StatusNotFound)
			}
			var reply messaging.BackendUpgradeReply
			err := client.CallStreaming(context.Background(), "n1", workerctl.PathBackendUpgrade, struct{}{}, &reply, nil)
			Expect(err).To(MatchError(ErrWorkerControlUnsupported))
		})
	})
})

// hijackAndClose ends a response mid-body without the chunked terminator, which
// is what a tunnel dying under an in-flight verb looks like to the reader.
func hijackAndClose(w http.ResponseWriter) {
	GinkgoHelper()
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	hj, ok := w.(http.Hijacker)
	Expect(ok).To(BeTrue(), "the test server must support hijacking")
	conn, buf, err := hj.Hijack()
	Expect(err).NotTo(HaveOccurred())
	_ = buf.Flush()
	_ = conn.Close()
}
