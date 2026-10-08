package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// controlWorker is a worker whose control plane a spec scripts, reached through
// a dialer that a spec can break.
type controlWorker struct {
	srv *httptest.Server

	mu       sync.Mutex
	handlers map[string]http.HandlerFunc
	dials    atomic.Int32
	headers  []http.Header
	hosts    []string
	dialErr  func(ctx context.Context) error
}

func newControlWorker() *controlWorker {
	w := &controlWorker{handlers: map[string]http.HandlerFunc{}}
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		h := w.handlers[r.URL.Path]
		w.headers = append(w.headers, r.Header.Clone())
		w.hosts = append(w.hosts, r.Host)
		w.mu.Unlock()
		// The body is read first, as the real worker does: net/http watches the
		// connection for a hang-up only after the handler has read the body, and a
		// handler that waits for the request context needs that.
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if h == nil {
			workerctl.WriteUnknownPath(rw, r)
			return
		}
		h(rw, r)
	}))
	DeferCleanup(w.srv.Close)
	return w
}

func (w *controlWorker) on(verb string, h http.HandlerFunc) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handlers[workerctl.PathOf(verb)] = h
}

func (w *controlWorker) replyJSON(verb string, reply any) {
	w.on(verb, func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(reply)
	})
}

func (w *controlWorker) dialerFor() WorkerNetDialerFor {
	return func(string) func(context.Context, string, string) (net.Conn, error) {
		return func(ctx context.Context, _, _ string) (net.Conn, error) {
			w.dials.Add(1)
			w.mu.Lock()
			fail := w.dialErr
			w.mu.Unlock()
			if fail != nil {
				if err := fail(ctx); err != nil {
					return nil, err
				}
			}
			var d net.Dialer
			return d.DialContext(ctx, "tcp", w.srv.Listener.Addr().String())
		}
	}
}

func (w *controlWorker) failDial(f func(ctx context.Context) error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dialErr = f
}

var _ = Describe("The tunnel carrier of the control verbs", func() {
	const node = "99999999-8888-7777-6666-555555555555"

	var (
		worker  *controlWorker
		control *TunnelControl
		link    *httpLink
		traced  map[string]time.Duration
		tracedM sync.Mutex
		locator *fakeModelLocator
	)

	BeforeEach(func() {
		worker = newControlWorker()
		locator = &fakeModelLocator{}
		control = NewTunnelControl(locator, NewControlClient(worker.dialerFor(), "registration-token"), 3*time.Minute, 15*time.Minute)
		link = control.link.(*httpLink)
		traced = map[string]time.Duration{}
		link.trace = func(verb string, timeout time.Duration) {
			tracedM.Lock()
			defer tracedM.Unlock()
			traced[verb] = timeout
		}
	})

	timeoutOf := func(verb string) time.Duration {
		tracedM.Lock()
		defer tracedM.Unlock()
		return traced[verb]
	}

	Describe("the timeout of each verb", func() {
		It("is the one that NATS gives it", func() {
			worker.replyJSON(workerctl.VerbModelStop, workerctl.ModelStopReply{Matched: true})
			worker.replyJSON(workerctl.VerbModelOp, workerctl.OperationReply{})
			worker.replyJSON(workerctl.VerbModelUnload, workerctl.ModelUnloadReply{Success: true})
			worker.replyJSON(workerctl.VerbBackendList, workerctl.BackendListReply{})
			worker.replyJSON(workerctl.VerbModelsRunning, workerctl.ModelsRunningReply{})
			worker.replyJSON(workerctl.VerbBackendStop, workerctl.BackendStopReply{Success: true})
			worker.replyJSON(workerctl.VerbBackendDelete, workerctl.BackendDeleteReply{Success: true})
			worker.replyJSON(workerctl.VerbModelDelete, workerctl.ModelDeleteReply{Success: true})
			worker.on(workerctl.VerbBackendInstall, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"reply":{"success":true}}`+"\n")
			})
			worker.on(workerctl.VerbBackendUpgrade, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"reply":{"success":true}}`+"\n")
			})

			_, err := control.StopLoadOperation(context.Background(), node, workerctl.ModelStopRequest{ProcessKey: "m#0", OperationID: "op"})
			Expect(err).ToNot(HaveOccurred())
			_, err = control.OperationControl(node, workerctl.OperationRequest{Renew: []string{"op"}})
			Expect(err).ToNot(HaveOccurred())
			Expect(control.UnloadReplica(node, NodeModel{ModelName: "m", Address: "127.0.0.1:1"})).To(Succeed())
			_, err = control.ListBackends(node)
			Expect(err).ToNot(HaveOccurred())
			_, err = control.ListRunningModels(node)
			Expect(err).ToNot(HaveOccurred())
			Expect(control.StopBackend(node, "b")).To(Succeed())
			_, err = control.DeleteBackend(node, "b")
			Expect(err).ToNot(HaveOccurred())
			_, err = control.InstallBackend(node, "b", "m", "", "", "", "", 0, "", nil)
			Expect(err).ToNot(HaveOccurred())
			_, err = control.UpgradeBackend(node, "b", "", "", "", "", 0, "", nil)
			Expect(err).ToNot(HaveOccurred())

			Expect(timeoutOf(workerctl.VerbModelOp)).To(Equal(5*time.Second), "a renewal must fit in its cadence")
			Expect(timeoutOf(workerctl.VerbModelStop)).To(Equal(10 * time.Second))
			Expect(timeoutOf(workerctl.VerbModelUnload)).To(Equal(30 * time.Second))
			Expect(timeoutOf(workerctl.VerbBackendList)).To(Equal(30*time.Second), "the last call of this verb was the list")
			Expect(timeoutOf(workerctl.VerbModelsRunning)).To(Equal(10 * time.Second))
			Expect(timeoutOf(workerctl.VerbBackendStop)).To(Equal(15 * time.Second))
			Expect(timeoutOf(workerctl.VerbBackendDelete)).To(Equal(2 * time.Minute))
			Expect(timeoutOf(workerctl.VerbBackendUpgrade)).To(Equal(15 * time.Minute))
			Expect(timeoutOf(workerctl.VerbBackendInstall)).To(Equal(3 * time.Minute))
		})
	})

	Describe("the shape of a request", func() {
		It("is a POST to the path of the verb with the registration token, to a host that names the node", func() {
			var method, contentType string
			worker.on(workerctl.VerbModelsRunning, func(w http.ResponseWriter, r *http.Request) {
				method, contentType = r.Method, r.Header.Get("Content-Type")
				_, _ = io.WriteString(w, `{"models":[]}`)
			})
			_, err := control.ListRunningModels(node)
			Expect(err).ToNot(HaveOccurred())

			Expect(method).To(Equal(http.MethodPost))
			Expect(contentType).To(Equal("application/json"))
			worker.mu.Lock()
			defer worker.mu.Unlock()
			Expect(worker.headers[0].Get("Authorization")).To(Equal("Bearer registration-token"))
			Expect(worker.hosts[0]).To(Equal(node + ".worker.invalid"))
		})

		It("reuses the stream of the last request, and dials again after the node is forgotten", func() {
			worker.replyJSON(workerctl.VerbModelsRunning, workerctl.ModelsRunningReply{})
			for range 3 {
				_, err := control.ListRunningModels(node)
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(worker.dials.Load()).To(Equal(int32(1)))

			control.link.(*httpLink).client.ForgetNode(node)
			_, err := control.ListRunningModels(node)
			Expect(err).ToNot(HaveOccurred())
			Expect(worker.dials.Load()).To(Equal(int32(2)))
		})

		It("sends a nil client's calls nowhere", func() {
			var none *ControlClient
			none.ForgetNode(node)
			err := none.Call(context.Background(), node, workerctl.VerbModelsRunning, struct{}{}, nil)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, ErrNoRoute)).To(BeFalse(), "a missing dialer is a bug of the wiring, not a route")
		})
	})

	Describe("a worker that is too old", func() {
		It("turns the 404 of an upgrade into ErrNoRoute, so that the caller falls back to the install it understands", func() {
			_, err := control.UpgradeBackend(node, "b", "", "", "", "", 0, "", nil)
			Expect(errors.Is(err, ErrNoRoute)).To(BeTrue(), "%v", err)
		})

		It("does not turn the 404 of another verb into ErrNoRoute", func() {
			_, err := control.OperationControl(node, workerctl.OperationRequest{Renew: []string{"op"}})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, ErrNoRoute)).To(BeFalse(), "the worker answered; it is present")
			Expect(errors.Is(err, errVerbNotServed)).To(BeTrue())
		})

		It("answers PingNode from a verb that the worker serves when the other one is missing", func() {
			worker.replyJSON(workerctl.VerbModelsRunning, workerctl.ModelsRunningReply{})
			Expect(control.PingNode(node)).To(Succeed())
		})
	})

	Describe("a link that cannot carry the request", func() {
		It("reports a missing tunnel as ErrNoRoute for every verb, and PingNode with it", func() {
			worker.failDial(func(context.Context) error { return fmt.Errorf("%w: no tunnel is held", ErrNoRoute) })
			_, err := control.OperationControl(node, workerctl.OperationRequest{Renew: []string{"op"}})
			Expect(errors.Is(err, ErrNoRoute)).To(BeTrue())
			Expect(errors.Is(control.PingNode(node), ErrNoRoute)).To(BeTrue())
			_, err = control.ListBackends(node)
			Expect(errors.Is(err, ErrNoRoute)).To(BeTrue())
		})

		It("does not report an unreachable peer as ErrNoRoute, and keeps its error for the caller", func() {
			peerGone := errors.New("the replica that holds the tunnel does not answer")
			worker.failDial(func(context.Context) error { return peerGone })
			_, err := control.OperationControl(node, workerctl.OperationRequest{Renew: []string{"op"}})
			Expect(errors.Is(err, peerGone)).To(BeTrue())
			Expect(errors.Is(err, ErrNoRoute)).To(BeFalse(), "an unreachable peer is not a verdict about the worker")
			Expect(control.PingNode(node)).To(Succeed(), "a node is not proven gone by a peer that did not answer")
		})

		It("keeps the refusal that a worker gave while the stream was opened", func() {
			refusal := errors.New("the worker refused the stream")
			worker.failDial(func(context.Context) error { return refusal })
			_, err := control.ListBackends(node)
			Expect(errors.Is(err, refusal)).To(BeTrue())
			Expect(errors.Is(err, ErrNoRoute)).To(BeFalse())
		})

		It("gives a renewal no more than its cadence when the stream does not open", func() {
			link.scale = 0.02
			worker.failDial(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
			begun := time.Now()
			_, err := control.OperationControl(node, workerctl.OperationRequest{Renew: []string{"op"}})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
			Expect(errors.Is(err, ErrNoRoute)).To(BeFalse(), "a slow tunnel is not an absent one")
			Expect(time.Since(begun)).To(BeNumerically("<", time.Second))
		})

		It("blames the caller and not the tunnel when its own deadline ends the attempt", func() {
			worker.on(workerctl.VerbModelStop, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			_, err := control.StopLoadOperation(ctx, node, workerctl.ModelStopRequest{ProcessKey: "m#0", OperationID: "op"})
			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
			Expect(errors.Is(err, ErrNoRoute)).To(BeFalse())
		})
	})

	Describe("a worker that fails to serve a request", func() {
		DescribeTable("is not an answer about a backend, and not a route",
			func(status int) {
				worker.on(workerctl.VerbBackendList, func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "something the worker could not do", status)
				})
				_, err := control.ListBackends(node)
				Expect(err).To(MatchError(ContainSubstring(fmt.Sprintf("HTTP %d", status))))
				Expect(errors.Is(err, ErrNoRoute)).To(BeFalse())
				Expect(errors.Is(err, errVerbNotServed)).To(BeFalse())
			},
			Entry("400, a body it could not read", http.StatusBadRequest),
			Entry("401, a token it does not accept", http.StatusUnauthorized),
			Entry("405, a method it refuses", http.StatusMethodNotAllowed),
			Entry("500, a handler that failed", http.StatusInternalServerError),
		)

		It("does not read a reply it cannot decode as the answer of the worker", func() {
			worker.on(workerctl.VerbBackendList, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"backends":`) })
			_, err := control.ListBackends(node)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, ErrNoRoute)).To(BeFalse())
		})
	})

	Describe("a backend stop", func() {
		It("never reads silence as a stop that was done", func() {
			link.scale = 0.005
			worker.on(workerctl.VerbBackendStop, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
			err := control.StopBackend(node, "b")
			Expect(err).To(HaveOccurred(), "NATS may read a missing acknowledgement as an older worker; HTTP must not")
			Expect(errors.Is(err, ErrNoRoute)).To(BeFalse())
		})

		It("reports the refusal of the worker as an error with its text", func() {
			worker.replyJSON(workerctl.VerbBackendStop, workerctl.BackendStopReply{Success: false, Error: "backend is busy"})
			Expect(control.StopBackend(node, "b")).To(MatchError(ContainSubstring("backend is busy")))
		})
	})

	Describe("an install", func() {
		It("hands every progress event to the caller, in order, before the reply", func() {
			worker.on(workerctl.VerbBackendInstall, func(w http.ResponseWriter, _ *http.Request) {
				for i := range 3 {
					ev, _ := json.Marshal(workerctl.BackendInstallProgressEvent{OpID: "op1", Percentage: float64(i * 40)})
					_, _ = fmt.Fprintf(w, "{\"progress\":%s}\n", ev)
					w.(http.Flusher).Flush()
				}
				_, _ = io.WriteString(w, `{"reply":{"success":true,"address":"127.0.0.1:9"}}`+"\n")
			})
			var seen []float64
			reply, err := control.InstallBackend(node, "b", "m", "", "", "", "", 0, "op1", func(ev workerctl.BackendInstallProgressEvent) {
				seen = append(seen, ev.Percentage)
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(reply.Success).To(BeTrue())
			Expect(seen).To(Equal([]float64{0, 40, 80}))
		})

		It("sends no progress to a caller that gave no operation id", func() {
			worker.on(workerctl.VerbBackendInstall, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"progress":{"op_id":"x"}}`+"\n"+`{"reply":{"success":true}}`+"\n")
			})
			called := false
			_, err := control.InstallBackend(node, "b", "m", "", "", "", "", 0, "", func(workerctl.BackendInstallProgressEvent) { called = true })
			Expect(err).ToNot(HaveOccurred())
			Expect(called).To(BeFalse())
		})

		It("says the worker may still be installing when the wait runs out", func() {
			link.scale = 0.0005
			worker.on(workerctl.VerbBackendInstall, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
			_, err := control.InstallBackend(node, "b", "m", "", "", "", "", 0, "", nil)
			Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeTrue(), "%v", err)
			Expect(errors.Is(err, ErrNoRoute)).To(BeFalse())
		})

		It("does not say that the worker may still be installing when the stream ends without a reply", func() {
			worker.on(workerctl.VerbBackendInstall, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"progress":{"op_id":"x"}}`+"\n")
			})
			_, err := control.InstallBackend(node, "b", "m", "", "", "", "", 0, "", nil)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, io.ErrUnexpectedEOF)).To(BeTrue())
			Expect(errors.Is(err, galleryop.ErrWorkerStillInstalling)).To(BeFalse(), "the link broke; nothing was learned about the install")
			Expect(errors.Is(err, ErrNoRoute)).To(BeFalse())
		})

		It("hands a refusal of the worker back in the reply", func() {
			worker.on(workerctl.VerbBackendInstall, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"reply":{"success":false,"error":"no space left"}}`+"\n")
			})
			reply, err := control.InstallBackend(node, "b", "m", "", "", "", "", 0, "", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(reply.Error).To(Equal("no space left"))
		})
	})

	Describe("an upgrade of a backend on an older worker, after the fallback", func() {
		It("sends the legacy install with Force set", func() {
			var got workerctl.BackendInstallRequest
			worker.on(workerctl.VerbBackendInstall, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&got)
				_, _ = io.WriteString(w, `{"reply":{"success":true}}`+"\n")
			})
			_, err := control.InstallBackendForce(node, "b", "", "", "", "", 0, "op", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Force).To(BeTrue())
		})
	})

	Describe("stopping a model on the nodes that hold it", func() {
		It("stops each node once, removes its replicas and reports a node it could not reach", func() {
			locator.nodes = []BackendNode{{ID: node, Name: "one"}, {ID: node, Name: "dup"}}
			var stops atomic.Int32
			worker.on(workerctl.VerbBackendStop, func(w http.ResponseWriter, _ *http.Request) {
				stops.Add(1)
				_, _ = io.WriteString(w, `{"success":true,"stopped_process_keys":["m#0"],"reports_stopped_processes":true}`)
			})
			Expect(control.UnloadRemoteModel("m")).To(Succeed())
			Expect(stops.Load()).To(Equal(int32(1)))

			worker.failDial(func(context.Context) error { return fmt.Errorf("%w: gone", ErrNoRoute) })
			control.link.(*httpLink).client.ForgetNode(node)
			err := control.UnloadRemoteModel("m")
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, ErrNoRoute)).To(BeTrue())
		})
	})
})
