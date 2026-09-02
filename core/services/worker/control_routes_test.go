package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/system"
)

// lastReplyOf drains an NDJSON control body and decodes the single terminal
// reply line into out. It fails the spec when the body carries no reply line,
// which is the shape the frontend cannot recover from: it would sit reading a
// body that already ended.
func lastReplyOf(body io.Reader, out any) error {
	GinkgoHelper()
	dec := json.NewDecoder(body)
	var reply json.RawMessage
	for {
		var env workerctl.Envelope
		err := dec.Decode(&env)
		if errors.Is(err, io.EOF) {
			break
		}
		Expect(err).NotTo(HaveOccurred())
		if env.Reply != nil {
			reply = env.Reply
		}
	}
	Expect(reply).NotTo(BeNil(), "the control body carried no terminal reply line")
	return json.Unmarshal(reply, out)
}

// envelopeKindsOf reports the order of progress/reply lines in an NDJSON body.
func envelopeKindsOf(body io.Reader) []string {
	GinkgoHelper()
	var kinds []string
	dec := json.NewDecoder(body)
	for {
		var env workerctl.Envelope
		err := dec.Decode(&env)
		if errors.Is(err, io.EOF) {
			break
		}
		Expect(err).NotTo(HaveOccurred())
		if env.Reply != nil {
			kinds = append(kinds, "reply")
			continue
		}
		Expect(env.Progress).NotTo(BeNil(), "an envelope carried neither progress nor reply")
		kinds = append(kinds, "progress")
	}
	return kinds
}

var _ = Describe("worker control routes", func() {
	var (
		sup   *backendSupervisor
		srv   *httptest.Server
		sigCh chan os.Signal
	)

	BeforeEach(func() {
		sigCh = make(chan os.Signal, 1)
		sup = &backendSupervisor{
			cfg:       &Config{},
			nodeID:    "node-under-test",
			sigCh:     sigCh,
			processes: map[string]*backendProcess{},
		}
		mux := http.NewServeMux()
		sup.RegisterControlRoutes(mux)
		srv = httptest.NewServer(mux)
		DeferCleanup(srv.Close)
	})

	// post marshals, POSTs, and hands back the response.
	post := func(path string, body any) *http.Response {
		GinkgoHelper()
		buf, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(buf))
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")
		resp, err := srv.Client().Do(req)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = resp.Body.Close() })
		return resp
	}

	It("answers models.running with the worker's process table", func() {
		resp := post(workerctl.PathModelsRunning, messaging.ModelsRunningRequest{})
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply messaging.ModelsRunningReply
		Expect(json.NewDecoder(resp.Body).Decode(&reply)).To(Succeed())
		Expect(reply.Models).To(BeEmpty())
	})

	It("refuses a GET on a control route, so a probe cannot fire a command", func() {
		resp, err := srv.Client().Get(srv.URL + workerctl.PathNodeStop)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = resp.Body.Close() })
		Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
		Expect(sigCh).NotTo(Receive(), "a GET must not have signalled shutdown")
	})

	It("refuses a GET on the streaming install route too", func() {
		resp, err := srv.Client().Get(srv.URL + workerctl.PathBackendInstall)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = resp.Body.Close() })
		Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
	})

	It("answers an unknown control path with 404 and a body that names the prefix", func() {
		resp := post(workerctl.Prefix+"no-such-verb", struct{}{})
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		// A mixed-version deployment has to be diagnosable from one line in a
		// log, not from a bare 404 that looks like a proxy.
		Expect(string(body)).To(ContainSubstring("control"))
	})

	It("bounds the unknown path it echoes back, so a long URL cannot be reflected wholesale", func() {
		long := strings.Repeat("a", 4096)
		resp := post(workerctl.Prefix+long, struct{}{})
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(body)).To(BeNumerically("<", 512))
	})

	It("reports a malformed request body as 400 and does not touch the process table", func() {
		resp, err := srv.Client().Post(srv.URL+workerctl.PathModelStop, "application/json",
			strings.NewReader("{not json"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = resp.Body.Close() })
		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
	})

	It("refuses a request body larger than the control bound", func() {
		// The body is DELIBERATELY well-formed JSON for the verb it is sent to.
		// A body of garbage would be rejected by the decoder whether or not the
		// bound exists, so a spec built on one is green with the bound removed
		// and pins nothing. This one is refused only because of the bound.
		oversized := []byte(`{"process_key":"` + strings.Repeat("a", maxControlRequestBytes) + `"}`)
		Expect(len(oversized)).To(BeNumerically(">", maxControlRequestBytes))
		resp, err := srv.Client().Post(srv.URL+workerctl.PathModelStop, "application/json",
			bytes.NewReader(oversized))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = resp.Body.Close() })
		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
	})

	It("accepts a large but in-bound body, so the bound is a ceiling and not a shape", func() {
		// BackendInstallRequest.BackendGalleries is a serialized gallery list of
		// a few hundred kilobytes on a real cluster, so the bound has to sit
		// well above that rather than at the size of a typical request.
		sup.installFn = func(context.Context, messaging.BackendInstallRequest, bool,
			func(messaging.BackendInstallProgressEvent)) (string, error) {
			return "127.0.0.1:1", nil
		}
		big := []byte(`{"backend":"mock","backend_galleries":"` + strings.Repeat("a", 1<<20) + `"}`)
		Expect(len(big)).To(BeNumerically("<", maxControlRequestBytes))
		resp, err := srv.Client().Post(srv.URL+workerctl.PathBackendInstall, "application/json",
			bytes.NewReader(big))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = resp.Body.Close() })
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
	})

	It("answers backend.stop with 204, the shape a fire-and-forget verb takes", func() {
		resp := post(workerctl.PathBackendStop, messaging.BackendStopRequest{Backend: "no-such-backend"})
		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
	})

	It("answers node.stop with 204 and signals shutdown", func() {
		resp := post(workerctl.PathNodeStop, struct{}{})
		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
		Eventually(sigCh).Should(Receive(Equal(os.Signal(syscall.SIGTERM))))
	})

	It("answers model.unload with the worker's own reply rather than a transport error", func() {
		resp := post(workerctl.PathModelUnload, messaging.ModelUnloadRequest{ModelName: "m"})
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply messaging.ModelUnloadReply
		Expect(json.NewDecoder(resp.Body).Decode(&reply)).To(Succeed())
		Expect(reply.Success).To(BeTrue())
	})

	It("answers model.stop for an unknown process with the worker's verdict, not a 5xx", func() {
		// The whole invariant of the phase: "that backend is not there" is the
		// WORKER answering, and it must never arrive as the status code a
		// frontend reads as "I could not reach that worker".
		resp := post(workerctl.PathModelStop, messaging.ModelStopRequest{ProcessKey: "ghost#0"})
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply messaging.ModelStopReply
		Expect(json.NewDecoder(resp.Body).Decode(&reply)).To(Succeed())
		Expect(reply.Matched).To(BeFalse())
		Expect(reply.ProcessKey).To(Equal("ghost#0"))
	})

	Context("streaming install", func() {
		It("writes every progress line before the single terminal reply line", func() {
			sup.installFn = func(_ context.Context, _ messaging.BackendInstallRequest, _ bool,
				progress func(messaging.BackendInstallProgressEvent)) (string, error) {
				progress(messaging.BackendInstallProgressEvent{Percentage: 50})
				progress(messaging.BackendInstallProgressEvent{Percentage: 100})
				return "127.0.0.1:50051", nil
			}
			resp := post(workerctl.PathBackendInstall, messaging.BackendInstallRequest{Backend: "mock", OpID: "op-1"})
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get("Content-Type")).To(Equal(workerctl.ContentTypeStream))
			Expect(envelopeKindsOf(resp.Body)).To(Equal([]string{"progress", "progress", "reply"}))
		})

		It("carries the address the backend actually listens on back in the reply", func() {
			sup.installFn = func(_ context.Context, _ messaging.BackendInstallRequest, _ bool,
				_ func(messaging.BackendInstallProgressEvent)) (string, error) {
				return "127.0.0.1:50099", nil
			}
			resp := post(workerctl.PathBackendInstall, messaging.BackendInstallRequest{Backend: "mock"})
			var reply messaging.BackendInstallReply
			Expect(lastReplyOf(resp.Body, &reply)).To(Succeed())
			Expect(reply.Success).To(BeTrue())
			Expect(reply.WorkerLocalAddress).To(Equal("127.0.0.1:50099"))
		})

		It("still writes a terminal reply line when the install fails", func() {
			sup.installFn = func(_ context.Context, _ messaging.BackendInstallRequest, _ bool,
				_ func(messaging.BackendInstallProgressEvent)) (string, error) {
				return "", errors.New("boom")
			}
			resp := post(workerctl.PathBackendInstall, messaging.BackendInstallRequest{Backend: "mock", OpID: "op-2"})
			// 200 with a failed reply, not 500: the WORKER answered, and a 5xx
			// is what the frontend reads as the worker not answering at all.
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var reply messaging.BackendInstallReply
			Expect(lastReplyOf(resp.Body, &reply)).To(Succeed())
			Expect(reply.Success).To(BeFalse())
			Expect(reply.Error).To(ContainSubstring("boom"))
		})

		It("passes the request's Force flag through, which is the legacy upgrade path", func() {
			forced := make(chan bool, 1)
			sup.installFn = func(_ context.Context, _ messaging.BackendInstallRequest, force bool,
				_ func(messaging.BackendInstallProgressEvent)) (string, error) {
				forced <- force
				return "127.0.0.1:1", nil
			}
			post(workerctl.PathBackendInstall, messaging.BackendInstallRequest{Backend: "mock", Force: true})
			Expect(forced).To(Receive(BeTrue()))
		})

		It("gives the install the caller's context, so a frontend that gave up stops the work", func() {
			started := make(chan struct{})
			observed := make(chan error, 1)
			// abandon releases the handler if the context never arrives, so a
			// build where the caller's budget was dropped fails this spec
			// instead of parking a goroutine until the suite times out.
			abandon := make(chan struct{})
			DeferCleanup(func() { close(abandon) })
			sup.installFn = func(ctx context.Context, _ messaging.BackendInstallRequest, _ bool,
				_ func(messaging.BackendInstallProgressEvent)) (string, error) {
				close(started)
				select {
				case <-ctx.Done():
				case <-abandon:
				}
				observed <- ctx.Err()
				return "", ctx.Err()
			}
			ctx, cancel := context.WithCancel(context.Background())
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				srv.URL+workerctl.PathBackendInstall, strings.NewReader(`{"backend":"mock"}`))
			Expect(err).NotTo(HaveOccurred())
			go func() {
				defer GinkgoRecover()
				resp, derr := srv.Client().Do(req)
				if derr == nil {
					_ = resp.Body.Close()
				}
			}()
			Eventually(started).Should(BeClosed())
			cancel()
			Eventually(observed).Should(Receive(MatchError(context.Canceled)))
		})

		It("reports a malformed install body as 400 rather than as a failed install", func() {
			resp, err := srv.Client().Post(srv.URL+workerctl.PathBackendInstall, "application/json",
				strings.NewReader("{not json"))
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = resp.Body.Close() })
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		})
	})

	Context("streaming upgrade", func() {
		It("writes progress before the terminal reply and reports what it stopped", func() {
			sup.upgradeFn = func(_ context.Context, _ messaging.BackendUpgradeRequest,
				progress func(messaging.BackendInstallProgressEvent)) ([]string, error) {
				progress(messaging.BackendInstallProgressEvent{Percentage: 10})
				return []string{"m#0"}, nil
			}
			resp := post(workerctl.PathBackendUpgrade, messaging.BackendUpgradeRequest{Backend: "mock", OpID: "op-3"})
			Expect(resp.Header.Get("Content-Type")).To(Equal(workerctl.ContentTypeStream))
			body, err := io.ReadAll(resp.Body)
			Expect(err).NotTo(HaveOccurred())
			Expect(envelopeKindsOf(bytes.NewReader(body))).To(Equal([]string{"progress", "reply"}))
			var reply messaging.BackendUpgradeReply
			Expect(lastReplyOf(bytes.NewReader(body), &reply)).To(Succeed())
			Expect(reply.Success).To(BeTrue())
			Expect(reply.StoppedProcessKeys).To(Equal([]string{"m#0"}))
			Expect(reply.ReportsStoppedProcesses).To(BeTrue())
		})

		It("reports the processes it stopped even when the upgrade then failed", func() {
			// stopped is meaningful on the error path: those ports are already
			// recycled, so the controller must drop their rows regardless.
			sup.upgradeFn = func(_ context.Context, _ messaging.BackendUpgradeRequest,
				_ func(messaging.BackendInstallProgressEvent)) ([]string, error) {
				return []string{"m#0"}, errors.New("upgrade boom")
			}
			resp := post(workerctl.PathBackendUpgrade, messaging.BackendUpgradeRequest{Backend: "mock"})
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var reply messaging.BackendUpgradeReply
			Expect(lastReplyOf(resp.Body, &reply)).To(Succeed())
			Expect(reply.Success).To(BeFalse())
			Expect(reply.Error).To(ContainSubstring("upgrade boom"))
			Expect(reply.StoppedProcessKeys).To(Equal([]string{"m#0"}))
			Expect(reply.ReportsStoppedProcesses).To(BeTrue())
		})
	})
})

// These specs pin the PRODUCTION wiring rather than the handler. Every defect
// this branch has shipped in the last two tasks was a call site no spec
// touched, and "the control plane is mounted, on the same listener, behind the
// same token" is exactly that kind of fact: the handlers can be perfect and a
// worker that never mounts them answers 404 to every command while looking
// healthy on /healthz.
var _ = Describe("the worker's HTTP server", func() {
	const token = "worker-token"

	var (
		srv  *http.Server
		base string
		sup  *backendSupervisor
	)

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		st, err := system.GetSystemState(
			system.WithModelPath(dir),
			system.WithBackendPath(filepath.Join(dir, "backends")),
			system.WithBackendSystemPath(filepath.Join(dir, "backends-system")),
		)
		Expect(err).NotTo(HaveOccurred())
		sup = &backendSupervisor{
			cfg:         &Config{ModelsPath: dir},
			systemState: st,
			nodeID:      "node-under-test",
			sigCh:       make(chan os.Signal, 1),
			processes:   map[string]*backendProcess{},
			// This Describe is about MOUNTING, so the two verbs that would
			// otherwise reach a gallery are scripted. Everything else runs its
			// real body against an empty worker.
			installFn: func(context.Context, messaging.BackendInstallRequest, bool,
				func(messaging.BackendInstallProgressEvent)) (string, error) {
				return "127.0.0.1:50051", nil
			},
			upgradeFn: func(context.Context, messaging.BackendUpgradeRequest,
				func(messaging.BackendInstallProgressEvent)) ([]string, error) {
				return nil, nil
			},
		}
		srv, err = startWorkerHTTPServer("127.0.0.1:0", filepath.Join(dir, "staging"), dir,
			filepath.Join(dir, "data"), token, &nodes.WorkerReadiness{}, sup, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { nodes.ShutdownFileTransferServer(srv) })
		Expect(srv.Addr).NotTo(BeEmpty(), "the worker HTTP server must report the address it bound")
		base = "http://" + srv.Addr
	})

	postCtl := func(path, bearer string) *http.Response {
		GinkgoHelper()
		req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader("{}"))
		Expect(err).NotTo(HaveOccurred())
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = resp.Body.Close() })
		return resp
	}

	It("serves the control plane on the same listener as the file routes", func() {
		resp := postCtl(workerctl.PathModelsRunning, token)
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply messaging.ModelsRunningReply
		Expect(json.NewDecoder(resp.Body).Decode(&reply)).To(Succeed())
		Expect(reply.Models).To(BeEmpty())
	})

	It("puts the control plane behind the registration token", func() {
		Expect(postCtl(workerctl.PathModelsRunning, "").StatusCode).To(Equal(http.StatusUnauthorized))
		Expect(postCtl(workerctl.PathModelsRunning, "wrong").StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("mounts every control verb, not just the one this spec reads", func() {
		for _, p := range workerctl.AllPaths() {
			if p == workerctl.PathNodeStop {
				// Firing it would tear down the worker under the other specs.
				continue
			}
			resp := postCtl(p, token)
			Expect(resp.StatusCode).NotTo(Equal(http.StatusNotFound), "control verb %q is not mounted", p)
			Expect(resp.StatusCode).NotTo(Equal(http.StatusUnauthorized), "control verb %q rejected a valid token", p)
		}
	})
})

// lockedRecorder is a ResponseWriter safe for concurrent writes. httptest's
// recorder is not, and the concurrency spec below is about what ndjsonStream
// serializes, not about what the recorder does.
type lockedRecorder struct {
	mu  sync.Mutex
	buf bytes.Buffer
	hdr http.Header
}

func newLockedRecorder() *lockedRecorder { return &lockedRecorder{hdr: http.Header{}} }

func (l *lockedRecorder) Header() http.Header { return l.hdr }

func (l *lockedRecorder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedRecorder) WriteHeader(int) {}

func (l *lockedRecorder) body() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.buf.Bytes()...)
}

var _ = Describe("the NDJSON control stream", func() {
	It("drops a progress line that arrives after the reply, so the reply stays last", func() {
		// The debounce timer emits from its own goroutine and can fire after
		// the install has already returned. Appending that line would break the
		// one thing the frontend relies on to stop reading.
		rec := httptest.NewRecorder()
		stream := newNDJSONStream(rec)
		stream.progress(messaging.BackendInstallProgressEvent{Percentage: 10})
		stream.reply(messaging.BackendInstallReply{Success: true})
		stream.progress(messaging.BackendInstallProgressEvent{Percentage: 100})

		Expect(envelopeKindsOf(bytes.NewReader(rec.Body.Bytes()))).To(Equal([]string{"progress", "reply"}))
	})

	It("writes at most one reply line even when reply is called twice", func() {
		rec := httptest.NewRecorder()
		stream := newNDJSONStream(rec)
		stream.reply(messaging.BackendInstallReply{Success: true})
		stream.reply(messaging.BackendInstallReply{Success: false, Error: "second"})

		Expect(envelopeKindsOf(bytes.NewReader(rec.Body.Bytes()))).To(Equal([]string{"reply"}))
		Expect(rec.Body.String()).NotTo(ContainSubstring("second"))
	})

	It("names the streaming content type and refuses to let it be sniffed", func() {
		rec := httptest.NewRecorder()
		newNDJSONStream(rec)
		Expect(rec.Header().Get("Content-Type")).To(Equal(workerctl.ContentTypeStream))
		Expect(rec.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"))
	})

	It("serializes concurrent progress against the reply, so no line is torn", func() {
		rec := newLockedRecorder()
		stream := newNDJSONStream(rec)

		const writers = 16
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(writers)
		for i := 0; i < writers; i++ {
			go func(n int) {
				defer GinkgoRecover()
				defer wg.Done()
				<-start
				stream.progress(messaging.BackendInstallProgressEvent{Percentage: float64(n)})
			}(i)
		}
		close(start)
		stream.reply(messaging.BackendInstallReply{Success: true})
		wg.Wait()

		kinds := envelopeKindsOf(bytes.NewReader(rec.body()))
		Expect(kinds).NotTo(BeEmpty())
		Expect(kinds[len(kinds)-1]).To(Equal("reply"), "the reply line must be the last line on the body")
		for _, k := range kinds[:len(kinds)-1] {
			Expect(k).To(Equal("progress"))
		}
	})
})
