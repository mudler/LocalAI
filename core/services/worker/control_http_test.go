package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// natsSubjectOf maps a verb onto the subject the NATS server uses for it, so a
// spec can ask both carriers the same question.
func natsSubjectOf(v string, node string) string {
	subject, err := newNATSControlServer(newRecordingBus(), node).subject(controlVerb(v))
	Expect(err).ToNot(HaveOccurred(), v)
	return subject
}

var _ = Describe("Worker control verbs over HTTP", func() {
	var (
		sigCh chan os.Signal
		s     *backendSupervisor
		ctl   *httpControlServer
		srv   *httptest.Server
	)

	BeforeEach(func() {
		sigCh = make(chan os.Signal, 1)
		s = newLifecycleTestSupervisor(sigCh)
		ctl = newHTTPControlServer()
		Expect(s.registerLifecycleVerbs(ctl)).To(Succeed())
		srv = httptest.NewServer(ctl)
		DeferCleanup(srv.Close)
	})

	post := func(verb string, body string) (*http.Response, string) {
		GinkgoHelper()
		resp, err := http.Post(srv.URL+workerctl.PathOf(verb), "application/json", strings.NewReader(body))
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		raw, err := io.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())
		return resp, strings.TrimSpace(string(raw))
	}

	Describe("the vocabulary", func() {
		It("gives every verb a path on this server and a subject on the NATS server", func() {
			for _, verb := range workerctl.AllVerbs() {
				fresh := newHTTPControlServer()
				Expect(fresh.handle(controlVerb(verb), func(context.Context, []byte) (any, error) { return nil, nil })).To(Succeed(), verb)
				Expect(natsSubjectOf(verb, "n1")).ToNot(BeEmpty(), verb)
			}
		})

		It("knows every verb constant that the control server declares", func() {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "control_server.go", nil, 0)
			Expect(err).ToNot(HaveOccurred())
			var declared []string
			ast.Inspect(f, func(n ast.Node) bool {
				vs, ok := n.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || !strings.HasPrefix(vs.Names[0].Name, "verb") || len(vs.Values) != 1 {
					return true
				}
				sel, ok := vs.Values[0].(*ast.SelectorExpr)
				if !ok {
					return true
				}
				declared = append(declared, sel.Sel.Name)
				return true
			})
			Expect(declared).To(HaveLen(len(workerctl.AllVerbs())),
				"a verb constant that is not in workerctl.AllVerbs has no path; found %v", declared)
		})

		It("refuses a verb outside the vocabulary and a verb served twice, naming the verb", func() {
			fresh := newHTTPControlServer()
			err := fresh.handle("backend.explode", func(context.Context, []byte) (any, error) { return nil, nil })
			Expect(err).To(MatchError(ContainSubstring("backend.explode")))

			Expect(fresh.handle(verbBackendList, func(context.Context, []byte) (any, error) { return nil, nil })).To(Succeed())
			err = fresh.handle(verbBackendList, func(context.Context, []byte) (any, error) { return nil, nil })
			Expect(err).To(MatchError(ContainSubstring("backend.list")))
		})

		It("serves the lifecycle verbs and nothing else when no file verb is registered", func() {
			for _, verb := range []string{
				workerctl.VerbBackendInstall, workerctl.VerbBackendUpgrade, workerctl.VerbBackendStop,
				workerctl.VerbBackendDelete, workerctl.VerbBackendList, workerctl.VerbModelsRunning,
				workerctl.VerbModelUnload, workerctl.VerbModelStop, workerctl.VerbModelDelete,
				workerctl.VerbModelOp, workerctl.VerbNodeStop,
			} {
				resp, _ := post(verb, malformedBody)
				Expect(resp.StatusCode).ToNot(Equal(http.StatusNotFound), verb)
			}
			for _, verb := range []string{workerctl.VerbFilesEnsure, workerctl.VerbFilesStage, workerctl.VerbFilesTemp, workerctl.VerbFilesListDir, workerctl.VerbFilesRelease} {
				resp, body := post(verb, malformedBody)
				Expect(resp.StatusCode).To(Equal(http.StatusNotFound), verb)
				Expect(body).To(ContainSubstring("unknown worker control path"), "the 404 must say what happened")
			}
		})
	})

	Describe("the shape of an answer", func() {
		It("answers a verb with 200 and its JSON reply", func() {
			resp, body := post(workerctl.VerbModelsRunning, "")
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
			Expect(body).To(Equal(`{"models":[],"reports_operations":true}`))
		})

		It("answers the verb that sends no reply with 204 and signals shutdown, and never blocks on a repeat", func() {
			resp, body := post(workerctl.VerbNodeStop, "")
			Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
			Expect(body).To(BeEmpty())
			Expect(sigCh).To(Receive(Equal(syscall.SIGTERM)))

			sigCh <- syscall.SIGINT
			resp, _ = post(workerctl.VerbNodeStop, "")
			Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
			Expect(sigCh).To(Receive(Equal(syscall.SIGINT)))
		})

		It("gives a worker's own refusal as a 200, so it is an answer and not a fault", func() {
			resp, body := post(workerctl.VerbModelOp, `{"renew":["nobody-knows-this"]}`)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var reply workerctl.OperationReply
			Expect(json.Unmarshal([]byte(body), &reply)).To(Succeed())
			Expect(reply.Unknown).To(ConsistOf("nobody-knows-this"))
		})

		It("sends the same bytes as the NATS server for the same request", func() {
			bus := newRecordingBus()
			natsSup := newLifecycleTestSupervisor(make(chan os.Signal, 1))
			Expect(registerLifecycleForTest(natsSup, bus)).To(Succeed())

			for _, c := range []struct{ verb, body string }{
				{workerctl.VerbModelsRunning, ``},
				{workerctl.VerbBackendList, ``},
				{workerctl.VerbModelStop, `{"model_name":"m","process_key":"m#0","operation_id":"op"}`},
				{workerctl.VerbModelOp, `{"renew":["a"],"complete":["b"]}`},
				{workerctl.VerbModelUnload, `{"model_name":"m","address":"127.0.0.1:1"}`},
				{workerctl.VerbModelDelete, `{"model_name":"m"}`},
				{workerctl.VerbBackendStop, `{"backend":"nothing-runs"}`},
			} {
				var viaNATS string
				Eventually(bus.deliver(natsSubjectOf(c.verb, "n1"), []byte(c.body))).Should(Receive(&viaNATS), c.verb)
				_, viaHTTP := post(c.verb, c.body)
				Expect(viaHTTP).To(Equal(viaNATS), c.verb)
			}
		})
	})

	Describe("a request the worker cannot read", func() {
		DescribeTable("is a 400 and not an answer",
			func(verb string) {
				resp, body := post(verb, malformedBody)
				Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
				Expect(body).To(ContainSubstring("invalid " + verb + " request"))
			},
			Entry("backend.install", workerctl.VerbBackendInstall),
			Entry("backend.upgrade", workerctl.VerbBackendUpgrade),
			Entry("backend.stop", workerctl.VerbBackendStop),
			Entry("backend.delete", workerctl.VerbBackendDelete),
			Entry("model.unload", workerctl.VerbModelUnload),
			Entry("model.stop", workerctl.VerbModelStop),
			Entry("model.op", workerctl.VerbModelOp),
			Entry("model.delete", workerctl.VerbModelDelete),
		)

		It("still answers a verb that ignores its body", func() {
			for _, verb := range []string{workerctl.VerbBackendList, workerctl.VerbModelsRunning} {
				resp, _ := post(verb, malformedBody)
				Expect(resp.StatusCode).To(Equal(http.StatusOK), verb)
			}
		})

		It("refuses a method other than POST with 405, and does not run the verb", func() {
			resp, err := http.Get(srv.URL + workerctl.PathOf(workerctl.VerbNodeStop))
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
			Expect(sigCh).ToNot(Receive())
		})

		It("refuses a body above the bound with 400", func() {
			big := strings.NewReader(strings.Repeat("x", workerctl.MaxRequestBytes+1))
			resp, err := http.Post(srv.URL+workerctl.PathOf(workerctl.VerbModelDelete), "application/json", big)
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		})

		It("answers a path under the prefix that no verb claims with 404", func() {
			resp, body := post("backend.explode", "{}")
			Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
			Expect(body).To(ContainSubstring("unknown worker control path"))
		})
	})

	Describe("the verbs that run long", func() {
		readLines := func(resp *http.Response) []workerctl.Envelope {
			GinkgoHelper()
			var lines []workerctl.Envelope
			sc := bufio.NewScanner(resp.Body)
			sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
			for sc.Scan() {
				var env workerctl.Envelope
				Expect(json.Unmarshal(sc.Bytes(), &env)).To(Succeed(), sc.Text())
				lines = append(lines, env)
			}
			Expect(sc.Err()).ToNot(HaveOccurred())
			return lines
		}

		It("streams the progress of an install and ends with exactly one reply line", func() {
			s.installFn = func(_ workerctl.BackendInstallRequest, _ bool, onDownload func(string, string, string, float64)) (string, error) {
				onDownload("backend.tar", "1 MB", "2 MB", 50)
				onDownload("backend.tar", "2 MB", "2 MB", 100)
				return "127.0.0.1:50051", nil
			}
			body, _ := json.Marshal(workerctl.BackendInstallRequest{Backend: "vllm", OpID: "op1"})
			resp, err := http.Post(srv.URL+workerctl.PathOf(workerctl.VerbBackendInstall), "application/json", bytes.NewReader(body))
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get("Content-Type")).To(Equal(workerctl.ContentTypeStream))

			lines := readLines(resp)
			Expect(len(lines)).To(BeNumerically(">=", 2))
			for _, l := range lines[:len(lines)-1] {
				Expect(l.Reply).To(BeNil())
				var ev workerctl.BackendInstallProgressEvent
				Expect(json.Unmarshal(l.Progress, &ev)).To(Succeed())
				Expect(ev.OpID).To(Equal("op1"))
			}
			last := lines[len(lines)-1]
			Expect(last.Progress).To(BeNil())
			var reply workerctl.BackendInstallReply
			Expect(json.Unmarshal(last.Reply, &reply)).To(Succeed())
			Expect(reply.Success).To(BeTrue())
			Expect(reply.Address).To(HaveSuffix(":50051"))
			var progressAfterReply int
			for i, l := range lines {
				if l.Reply != nil && i != len(lines)-1 {
					progressAfterReply++
				}
			}
			Expect(progressAfterReply).To(BeZero(), "the reply is the last line")
		})

		It("streams the answer of an upgrade the same way", func() {
			s.upgradeFn = func(_ workerctl.BackendUpgradeRequest, _ func(string, string, string, float64)) ([]string, error) {
				return []string{"m#0"}, nil
			}
			body, _ := json.Marshal(workerctl.BackendUpgradeRequest{Backend: "vllm"})
			resp, err := http.Post(srv.URL+workerctl.PathOf(workerctl.VerbBackendUpgrade), "application/json", bytes.NewReader(body))
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			lines := readLines(resp)
			Expect(lines).ToNot(BeEmpty())
			var reply workerctl.BackendUpgradeReply
			Expect(json.Unmarshal(lines[len(lines)-1].Reply, &reply)).To(Succeed())
			Expect(reply.Success).To(BeTrue())
			Expect(reply.StoppedProcessKeys).To(Equal([]string{"m#0"}))
		})

		It("does not hold up a second request while an install runs", func() {
			release := make(chan struct{})
			started := make(chan struct{})
			s.installFn = func(workerctl.BackendInstallRequest, bool, func(string, string, string, float64)) (string, error) {
				close(started)
				<-release
				return "127.0.0.1:1", nil
			}
			body, _ := json.Marshal(workerctl.BackendInstallRequest{Backend: "vllm"})
			done := make(chan struct{})
			go func() {
				defer close(done)
				resp, err := http.Post(srv.URL+workerctl.PathOf(workerctl.VerbBackendInstall), "application/json", bytes.NewReader(body))
				if err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}()
			Eventually(started).Should(BeClosed())

			resp, _ := post(workerctl.VerbModelsRunning, "")
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			close(release)
			Eventually(done).Should(BeClosed())
		})

		It("lets an install finish when the caller goes away", func() {
			finished := make(chan struct{})
			release := make(chan struct{})
			s.installFn = func(workerctl.BackendInstallRequest, bool, func(string, string, string, float64)) (string, error) {
				<-release
				close(finished)
				return "127.0.0.1:1", nil
			}
			body, _ := json.Marshal(workerctl.BackendInstallRequest{Backend: "vllm"})
			ctx, cancel := context.WithCancel(context.Background())
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+workerctl.PathOf(workerctl.VerbBackendInstall), bytes.NewReader(body))
			go func() {
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					_ = resp.Body.Close()
				}
			}()
			time.Sleep(100 * time.Millisecond)
			cancel()
			close(release)
			Eventually(finished).Should(BeClosed())
		})
	})

	Describe("handlers that several requests reach at once", func() {
		It("serves renewals, completions and the watchdog without a data race", func() {
			var wg sync.WaitGroup
			for i := range 8 {
				wg.Go(func() {
					id := "op-" + strconv.Itoa(i)
					for range 50 {
						s.beginOperation(workerctl.BackendInstallRequest{ModelID: "m", ReplicaIndex: int32(i), OperationID: id, DeadlineMs: 60_000})
						body, _ := json.Marshal(workerctl.OperationRequest{Renew: []string{id}, Complete: []string{id}})
						resp, err := http.Post(srv.URL+workerctl.PathOf(workerctl.VerbModelOp), "application/json", bytes.NewReader(body))
						Expect(err).ToNot(HaveOccurred())
						_ = resp.Body.Close()
						s.expireOperations(time.Now())
					}
				})
			}
			wg.Wait()
			Expect(slices.Collect(func(yield func(string) bool) {
				s.mu.Lock()
				defer s.mu.Unlock()
				for id := range s.operations {
					if !yield(id) {
						return
					}
				}
			})).To(BeEmpty(), "every operation was completed")
		})
	})
})

var _ = Describe("Worker control verbs: both servers on one handler", func() {
	// The handler that a carrier registers is the same function. A spec that ran
	// only against NATS would not notice a verb that one carrier cannot serve.
	It("registers the same set of verbs on both servers", func() {
		bus := newRecordingBus()
		s := newLifecycleTestSupervisor(make(chan os.Signal, 1))
		Expect(registerLifecycleForTest(s, bus)).To(Succeed())

		http1 := newHTTPControlServer()
		Expect(newLifecycleTestSupervisor(make(chan os.Signal, 1)).registerLifecycleVerbs(http1)).To(Succeed())
		var onHTTP []string
		for v := range http1.served {
			onHTTP = append(onHTTP, natsSubjectOf(string(v), "n1"))
		}
		Expect(bus.subscribed()).To(ConsistOf(onHTTP))
		_ = messaging.SubjectNodeStop
	})
})

var _ = Describe("Worker file staging verbs over HTTP", func() {
	var (
		cfg      *Config
		cacheDir string
		s3       *httptest.Server
		srv      *httptest.Server
		getGate  chan struct{}
		getCount int32
		countMu  sync.Mutex
	)

	BeforeEach(func() {
		getGate = make(chan struct{})
		getCount = 0
		s3 = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPut:
				w.WriteHeader(http.StatusOK)
			case http.MethodGet:
				countMu.Lock()
				getCount++
				countMu.Unlock()
				select {
				case <-getGate:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Length", "4")
				_, _ = w.Write([]byte("data"))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		DeferCleanup(s3.Close)
		root := canonicalWorkerTempDir()
		cfg = &Config{
			ModelsPath:       filepath.Join(root, "models"),
			StorageURL:       s3.URL,
			StorageBucket:    "bucket",
			StorageAccessKey: "key",
			StorageSecretKey: "secret",
		}
		Expect(os.MkdirAll(cfg.ModelsPath, 0750)).To(Succeed())
		cacheDir = filepath.Join(root, "cache")
		ctl := newHTTPControlServer()
		Expect(cfg.registerFileStagingVerbs(ctl, nil)).To(Succeed())
		srv = httptest.NewServer(ctl)
		DeferCleanup(srv.Close)
	})

	post := func(verb, body string) string {
		GinkgoHelper()
		resp, err := http.Post(srv.URL+workerctl.PathOf(verb), "application/json", strings.NewReader(body))
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusOK), verb)
		raw, err := io.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())
		return strings.TrimSpace(string(raw))
	}

	It("serves the five file verbs", func() {
		for _, verb := range []string{workerctl.VerbFilesRelease, workerctl.VerbFilesEnsure, workerctl.VerbFilesStage, workerctl.VerbFilesTemp, workerctl.VerbFilesListDir} {
			resp, err := http.Post(srv.URL+workerctl.PathOf(verb), "application/json", strings.NewReader(malformedBody))
			Expect(err).ToNot(HaveOccurred())
			_ = resp.Body.Close()
			Expect(resp.StatusCode).ToNot(Equal(http.StatusNotFound), verb)
		}
	})

	It("stages an allowed path and refuses one outside the allowed directories", func() {
		path := filepath.Join(cfg.ModelsPath, "m.gguf")
		Expect(os.WriteFile(path, []byte("x"), 0640)).To(Succeed())
		Expect(post(workerctl.VerbFilesStage, fmt.Sprintf(`{"local_path":%q,"key":"models/m.gguf"}`, path))).To(Equal(`{"key":"models/m.gguf"}`))
		Expect(post(workerctl.VerbFilesStage, `{"local_path":"/etc/passwd","key":"k"}`)).To(Equal(`{"error":"path outside allowed directories"}`))
	})

	It("allocates a temp file in the cache", func() {
		got := post(workerctl.VerbFilesTemp, `{}`)
		Expect(got).To(MatchRegexp(`^\{"local_path":"` + filepath.Join(cacheDir, "staging-tmp") + `/localai-staging-[0-9]+\.tmp"\}$`))
	})

	It("lets a download that two callers share end when the first caller leaves", func() {
		type result struct {
			body string
			err  error
		}
		ensure := func(ctx context.Context) result {
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+workerctl.PathOf(workerctl.VerbFilesEnsure), strings.NewReader(`{"key":"models/shared.gguf"}`))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return result{err: err}
			}
			defer func() { _ = resp.Body.Close() }()
			raw, _ := io.ReadAll(resp.Body)
			return result{body: string(raw)}
		}

		firstCtx, leave := context.WithCancel(context.Background())
		first := make(chan result, 1)
		go func() { first <- ensure(firstCtx) }()
		Eventually(func() int32 { countMu.Lock(); defer countMu.Unlock(); return getCount }).Should(BeNumerically(">=", 1))

		second := make(chan result, 1)
		go func() { second <- ensure(context.Background()) }()
		time.Sleep(200 * time.Millisecond)

		leave()
		Eventually(first).Should(Receive())
		close(getGate)

		var got result
		Eventually(second, "20s").Should(Receive(&got))
		Expect(got.err).ToNot(HaveOccurred())
		Expect(got.body).To(ContainSubstring(`"local_path"`), "the second caller must get the file: %s", got.body)
	})
})
