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

	It("reports a request body it cannot read as a rejection, never as a file that is not there", func() {
		resp, err := http.Post(srv.URL+workerctl.PathFilesEnsure, "application/json", strings.NewReader("{not json"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = resp.Body.Close() })
		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
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
