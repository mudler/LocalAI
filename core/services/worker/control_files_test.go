package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// The four file-staging verbs on the worker's own control plane.
//
// They used to be NATS request-reply subjects, and the size of a listdir reply
// was a property of the carrier: a directory with enough files in it produced a
// payload the bus was not comfortable with. Over HTTP it is a response body,
// which is why one of the specs below asserts a listing far past any payload
// cap rather than asserting a cap of its own.
var _ = Describe("worker file-staging control routes", func() {
	var (
		cfg       *Config
		srv       *httptest.Server
		fm        *storage.FileManager
		store     *storage.FilesystemStore
		modelsDir string
		cacheDir  string
	)

	// post issues one control verb the way the frontend does and hands back the
	// raw response, so a spec can assert the STATUS as well as the body. The
	// two carry different meanings and a helper that decoded only the body
	// would hide the one this file exists to pin.
	post := func(path string, body any) *http.Response {
		GinkgoHelper()
		raw, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(raw))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = resp.Body.Close() })
		return resp
	}

	decode := func(resp *http.Response, out any) {
		GinkgoHelper()
		Expect(json.NewDecoder(resp.Body).Decode(out)).To(Succeed())
	}

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		modelsDir = filepath.Join(dir, "models")
		Expect(os.MkdirAll(modelsDir, 0o750)).To(Succeed())
		cacheDir = filepath.Join(dir, "cache")

		var err error
		store, err = storage.NewFilesystemStore(filepath.Join(dir, "objectstore"))
		Expect(err).NotTo(HaveOccurred())
		fm, err = storage.NewFileManager(store, cacheDir)
		Expect(err).NotTo(HaveOccurred())

		cfg = &Config{ModelsPath: modelsDir}
		mux := http.NewServeMux()
		cfg.RegisterFileControlRoutes(mux, fm)
		srv = httptest.NewServer(mux)
		DeferCleanup(srv.Close)
	})

	// The on-disk layout, written out BY HAND rather than derived from the
	// helpers under test. What these directories ARE is an operator-facing
	// contract: a deployment mounts a volume per directory and sizes it, so a
	// join that moved would put staged bytes on a volume nobody provisioned.
	// Deriving the expectation from the helper would pin nothing.
	DescribeTable("derives its directories from the models path, once each",
		func(got, want string) { Expect(got).To(Equal(want)) },
		Entry("cache", (&Config{ModelsPath: "/srv/localai/models"}).stagingCacheDir(), "/srv/localai/cache"),
		Entry("data", (&Config{ModelsPath: "/srv/localai/models"}).stagingDataDir(), "/srv/localai/data"),
	)

	It("allocates a temp path and returns it", func() {
		resp := post(workerctl.PathFilesTemp, struct{}{})
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply struct {
			LocalPath string `json:"local_path"`
			Error     string `json:"error"`
		}
		decode(resp, &reply)
		Expect(reply.Error).To(BeEmpty())
		Expect(reply.LocalPath).To(BeAnExistingFile())
	})

	It("downloads a key the store already holds and reports where it landed", func() {
		key := storage.ModelKey("ensure-me.gguf")
		Expect(store.Put(context.Background(), key, strings.NewReader("weights"))).To(Succeed())

		resp := post(workerctl.PathFilesEnsure, map[string]string{"key": key})
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply struct {
			LocalPath string `json:"local_path"`
			Error     string `json:"error"`
		}
		decode(resp, &reply)
		Expect(reply.Error).To(BeEmpty())
		Expect(reply.LocalPath).To(BeAnExistingFile())
		Expect(os.ReadFile(reply.LocalPath)).To(Equal([]byte("weights")))
	})

	It("uploads a file under an allowed directory and answers with its key", func() {
		local := filepath.Join(modelsDir, "staged.bin")
		Expect(os.WriteFile(local, []byte("output"), 0o600)).To(Succeed())

		resp := post(workerctl.PathFilesStage, map[string]string{"local_path": local, "key": "data/out.bin"})
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply struct {
			Key   string `json:"key"`
			Error string `json:"error"`
		}
		decode(resp, &reply)
		Expect(reply.Error).To(BeEmpty())
		Expect(reply.Key).To(Equal("data/out.bin"))
		exists, err := store.Exists(context.Background(), "data/out.bin")
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("returns a listing longer than a NATS payload would have carried", func() {
		// 4000 files at ~40 bytes of name each is ~160 KB, twenty times the
		// NOTIFY cap and well past what the old carrier was comfortable with.
		// The point of the spec is that size is no longer a property of the
		// transport.
		bigDir := filepath.Join(modelsDir, "big")
		Expect(os.MkdirAll(bigDir, 0o750)).To(Succeed())
		for i := range 4000 {
			name := fmt.Sprintf("shard-%030d.safetensors", i)
			Expect(os.WriteFile(filepath.Join(bigDir, name), []byte("x"), 0o600)).To(Succeed())
		}

		resp := post(workerctl.PathFilesListDir, map[string]string{"key_prefix": "models/big"})
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply struct {
			Files []string `json:"files"`
			Error string   `json:"error"`
		}
		decode(resp, &reply)
		Expect(reply.Error).To(BeEmpty())
		Expect(reply.Files).To(HaveLen(4000))
	})

	// The rule these four pin is ONE rule stated at four handlers: a verb's own
	// failure is the worker's ANSWER and travels as a 200 with the error field
	// set, never as a 5xx. A 5xx is what the frontend maps onto "this frontend
	// could not reach that worker", which nothing may act on, so a handler that
	// answered 500 would move its own verdict into the bucket reserved for a
	// broken link. Each handler is pinned separately because each writes the
	// rule out for itself.
	It("reports a staging failure as a 200 with an error field, not as a 5xx", func() {
		resp := post(workerctl.PathFilesStage, map[string]string{"local_path": "/nope", "key": "k"})
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply struct {
			Error string `json:"error"`
		}
		decode(resp, &reply)
		Expect(reply.Error).NotTo(BeEmpty())
	})

	It("reports an upload that failed as a 200 with an error field, not as a 5xx", func() {
		// The path is INSIDE an allowed directory and the file is simply not
		// there, so this reaches the upload rather than stopping at the
		// allow-list. The two are separate statements of the same rule inside
		// one handler, and a spec that only ever reaches the allow-list leaves
		// the upload free to answer 500 for the worker's own verdict.
		missing := filepath.Join(modelsDir, "was-never-written.bin")
		resp := post(workerctl.PathFilesStage, map[string]string{"local_path": missing, "key": "data/x"})
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply struct {
			Key   string `json:"key"`
			Error string `json:"error"`
		}
		decode(resp, &reply)
		Expect(reply.Error).NotTo(BeEmpty())
		Expect(reply.Error).NotTo(ContainSubstring("outside allowed directories"))
		Expect(reply.Key).To(BeEmpty())
	})

	It("reports an ensure of a key the store does not hold as a 200 with an error field", func() {
		resp := post(workerctl.PathFilesEnsure, map[string]string{"key": "models/absent.gguf"})
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply struct {
			LocalPath string `json:"local_path"`
			Error     string `json:"error"`
		}
		decode(resp, &reply)
		Expect(reply.Error).NotTo(BeEmpty())
		Expect(reply.LocalPath).To(BeEmpty())
	})

	It("reports a listdir of a directory that is not there as a 200 with an error field", func() {
		resp := post(workerctl.PathFilesListDir, map[string]string{"key_prefix": "models/never-created"})
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply struct {
			Files []string `json:"files"`
			Error string   `json:"error"`
		}
		decode(resp, &reply)
		Expect(reply.Error).NotTo(BeEmpty())
		Expect(reply.Files).To(BeEmpty())
	})

	It("reports a temp allocation it cannot make as a 200 with an error field", func() {
		// A regular file where the staging directory must go: MkdirAll cannot
		// create through it, for root as much as for anyone else, so the verb
		// fails for a reason that is entirely this worker's own.
		Expect(os.MkdirAll(cacheDir, 0o750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(cacheDir, "staging-tmp"), []byte("not a dir"), 0o600)).To(Succeed())

		resp := post(workerctl.PathFilesTemp, struct{}{})
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply struct {
			LocalPath string `json:"local_path"`
			Error     string `json:"error"`
		}
		decode(resp, &reply)
		Expect(reply.Error).NotTo(BeEmpty())
		Expect(reply.LocalPath).To(BeEmpty())
	})

	It("reports a temp file it cannot create as a 200 with an error field", func() {
		// The staging directory ALREADY EXISTS and is unwritable, so MkdirAll
		// succeeds and CreateTemp is the branch that fails. It is a second
		// statement of the same rule inside the same handler as the MkdirAll
		// spec above, and a spec that only ever reaches MkdirAll leaves this
		// one free to answer a non-2xx for the worker's own verdict.
		Expect(os.MkdirAll(filepath.Join(cacheDir, "staging-tmp"), 0o500)).To(Succeed())

		resp := post(workerctl.PathFilesTemp, struct{}{})
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply struct {
			LocalPath string `json:"local_path"`
			Error     string `json:"error"`
		}
		decode(resp, &reply)
		Expect(reply.Error).NotTo(BeEmpty())
		Expect(reply.LocalPath).To(BeEmpty())
	})

	It("refuses a key prefix that climbs out of the directories it serves", func() {
		resp := post(workerctl.PathFilesListDir, map[string]string{"key_prefix": "../../../etc"})
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply struct {
			Files []string `json:"files"`
			Error string   `json:"error"`
		}
		decode(resp, &reply)
		Expect(reply.Error).To(ContainSubstring("invalid key prefix"))
		Expect(reply.Files).To(BeEmpty())
	})

	It("refuses to upload a path outside the directories it serves", func() {
		outside := filepath.Join(GinkgoT().TempDir(), "secret")
		Expect(os.WriteFile(outside, []byte("nope"), 0o600)).To(Succeed())

		resp := post(workerctl.PathFilesStage, map[string]string{"local_path": outside, "key": "data/leak"})
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var reply struct {
			Error string `json:"error"`
		}
		decode(resp, &reply)
		Expect(reply.Error).To(ContainSubstring("outside allowed directories"))
	})

	// The bounded, POST-only entry point, pinned at every one of the four
	// paths rather than at one of them. A route that skipped it would be a
	// second, unbounded door onto a boundary this worker serves, and it would
	// also let a liveness probe or an address bar run a command.
	DescribeTable("refuses a method that is not POST",
		func(path string) {
			resp, err := http.Get(srv.URL + path)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = resp.Body.Close() })
			Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
		},
		Entry("ensure", workerctl.PathFilesEnsure),
		Entry("stage", workerctl.PathFilesStage),
		Entry("temp", workerctl.PathFilesTemp),
		Entry("listdir", workerctl.PathFilesListDir),
	)

	DescribeTable("refuses a body past the control cap, and as a rejected request rather than an answer",
		func(path string) {
			// VALID JSON past the cap, deliberately. A body of filler is
			// refused by the decoder whether or not anything bounds it, so a
			// spec written that way passes for a reason that has nothing to do
			// with the bound and would keep passing after the bound was
			// removed. This one can only be refused by the bound.
			oversized := append([]byte(`{"key":"`), bytes.Repeat([]byte("a"), maxControlRequestBytes+1)...)
			oversized = append(oversized, []byte(`"}`)...)
			resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(oversized))
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = resp.Body.Close() })
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		},
		Entry("ensure", workerctl.PathFilesEnsure),
		Entry("stage", workerctl.PathFilesStage),
		Entry("temp", workerctl.PathFilesTemp),
		Entry("listdir", workerctl.PathFilesListDir),
	)

	// The other direction of the same bound, kept beside the refusals so the
	// pair is local rather than spread across the suite.
	//
	// The two entries hold DIFFERENT halves and both are needed. The
	// cap-relative body holds that the boundary is inclusive, so the bound is a
	// ceiling and not an off-by-one; it moves with the cap and therefore says
	// nothing about the cap's size. The absolute body holds that the cap is
	// large enough for real traffic: BackendInstallRequest.BackendGalleries is
	// a serialized gallery list of a few hundred kilobytes, so a cap tightened
	// below a megabyte would start refusing ordinary requests, and only an
	// entry written in absolute bytes notices that.
	DescribeTable("serves a body that fits inside the control cap",
		func(path string, size int) {
			body := append([]byte(`{"key":"`), bytes.Repeat([]byte("a"), size-len(`{"key":""}`))...)
			body = append(body, []byte(`"}`)...)
			Expect(body).To(HaveLen(size))

			resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(body))
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = resp.Body.Close() })
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		},
		Entry("ensure, exactly at the cap", workerctl.PathFilesEnsure, maxControlRequestBytes),
		Entry("stage, exactly at the cap", workerctl.PathFilesStage, maxControlRequestBytes),
		Entry("temp, exactly at the cap", workerctl.PathFilesTemp, maxControlRequestBytes),
		Entry("listdir, exactly at the cap", workerctl.PathFilesListDir, maxControlRequestBytes),
		Entry("ensure, a megabyte of real traffic", workerctl.PathFilesEnsure, 1<<20),
		Entry("stage, a megabyte of real traffic", workerctl.PathFilesStage, 1<<20),
		Entry("temp, a megabyte of real traffic", workerctl.PathFilesTemp, 1<<20),
		Entry("listdir, a megabyte of real traffic", workerctl.PathFilesListDir, 1<<20),
	)

	// A body this worker could not PARSE is the frontend's request being wrong,
	// not this worker's verdict about a file. The two live in opposite buckets:
	// a non-2xx is mapped onto ErrWorkerUnroutable, which nothing may act on,
	// while a 200 with the error field set passes through unwrapped so
	// cluster.IsWorkerAnswer sees it and a reap guard MAY act on it. A handler
	// that answered `{"error":"invalid request"}` for an unparseable body would
	// hand a malformed request to a reap guard as evidence about a file.
	//
	// Pinned at every verb that decodes a body, because each one writes the
	// exit out for itself. The base's NATS handlers shipped the wrong answer
	// here; adopting the shared door fixed it, and this is what keeps it fixed.
	DescribeTable("reports a request body it cannot read as a rejection, never as a file that is not there",
		func(path string) {
			resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader("{not json"))
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = resp.Body.Close() })
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))

			// The status alone is not the whole rule: a 400 whose body still
			// decodes as a reply with an error field would be read correctly by
			// today's client and wrongly by anything that reads the body first.
			var reply struct {
				Error string `json:"error"`
			}
			Expect(json.NewDecoder(resp.Body).Decode(&reply)).NotTo(Succeed())
		},
		Entry("ensure", workerctl.PathFilesEnsure),
		Entry("stage", workerctl.PathFilesStage),
		Entry("listdir", workerctl.PathFilesListDir),
		// temp decodes no body, so it has no such exit to state.
	)

	It("fails a listing the caller abandoned rather than answering a short one", func() {
		// A caller that gave up must not be answered with the files walked so
		// far. A partial listing is the one shape the frontend cannot tell from
		// a directory that really is that size, and it would read as files the
		// worker does not have. So the walk returns the context error and the
		// verb reports a FAILED listing.
		dir := filepath.Join(modelsDir, "abandoned")
		Expect(os.MkdirAll(dir, 0o750)).To(Succeed())
		for i := range 32 {
			Expect(os.WriteFile(filepath.Join(dir, fmt.Sprintf("f-%02d", i)), []byte("x"), 0o600)).To(Succeed())
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		files, err := listStagedFiles(ctx, dir)
		Expect(err).To(MatchError(context.Canceled))
		Expect(files).To(BeEmpty())
	})

	It("mounts every file verb, so none can be dropped from the set the frontend calls", func() {
		for _, path := range []string{
			workerctl.PathFilesEnsure, workerctl.PathFilesStage,
			workerctl.PathFilesTemp, workerctl.PathFilesListDir,
		} {
			resp := post(path, struct{}{})
			Expect(resp.StatusCode).NotTo(Equal(http.StatusNotFound), "%s is not mounted", path)
		}
	})
})
