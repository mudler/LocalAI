package localai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/promptcache"
	"github.com/mudler/LocalAI/core/services/promptcachesvc"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Prompt cache endpoints", func() {
	var (
		tmp    string
		root   string
		app    *application.Application
		e      *echo.Echo
		cancel context.CancelFunc
	)

	// entry writes one cache file of the model file and the namespace.
	entry := func(modelFile, ns, hash string) string {
		dir := filepath.Join(root, "llama-cpp", "1111111111111111-"+promptcache.ModelID(modelFile))
		Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
		p := filepath.Join(dir, "cccccccccccccccc-"+promptcache.NSID(promptcache.Salt(ns))+"-"+strings.Repeat(hash, 32)+".cache")
		Expect(os.WriteFile(p, []byte("kv-state"), 0o600)).To(Succeed())
		return p
	}

	BeforeEach(func() {
		var err error
		tmp, err = os.MkdirTemp("", "prompt-cache-test-*")
		Expect(err).ToNot(HaveOccurred())
		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		models := filepath.Join(tmp, "models")
		Expect(os.MkdirAll(models, 0o750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(models, "qwen.yaml"),
			[]byte("name: qwen\nbackend: llama-cpp\nparameters:\n  model: qwen.gguf\n"), 0o600)).To(Succeed())
		st, err := system.GetSystemState(system.WithModelPath(models), system.WithBackendPath(filepath.Join(tmp, "backends")))
		Expect(err).ToNot(HaveOccurred())
		app, err = application.New(config.WithContext(ctx), config.WithSystemState(st), config.WithDataPath(filepath.Join(tmp, "data")))
		Expect(err).ToNot(HaveOccurred())
		root = app.ApplicationConfig().PromptCacheRoot()
		Expect(app.PromptCacheNode().Root()).To(Equal(root))

		e = echo.New()
		e.GET("/api/prompt-cache", PromptCacheStatusEndpoint(app))
		e.POST("/api/prompt-cache/clear", PromptCacheClearEndpoint(app))
		e.DELETE("/api/prompt-cache/mine", PromptCacheClearMineEndpoint(app))
	})

	AfterEach(func() {
		cancel()
		// Background services of the app (plugins) may still write under the
		// data path for a moment after the cancel.
		Eventually(func() error { return os.RemoveAll(tmp) }, 10*time.Second, 100*time.Millisecond).Should(Succeed())
	})

	do := func(method, path, body string) *httptest.ResponseRecorder {
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
			r.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, r)
		return rec
	}

	It("reports the files of the node, by model name", func() {
		entry("qwen.gguf", "user:a", "1")
		entry("gone.gguf", "user:b", "2")
		rec := do(http.MethodGet, "/api/prompt-cache", "")
		Expect(rec.Code).To(Equal(http.StatusOK))
		var resp promptcachesvc.StatusReport
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Enabled).To(BeTrue())
		Expect(resp.Nodes).To(HaveLen(1))
		Expect(resp.Nodes[0].Files).To(Equal(2))
		Expect(resp.Nodes[0].Namespaces).To(Equal(2))
		names := []string{}
		for _, m := range resp.Nodes[0].Models {
			names = append(names, m.Model)
		}
		Expect(names).To(ConsistOf("qwen", ""))
	})

	It("clears one model, refuses an unknown one, and clears everything", func() {
		q := entry("qwen.gguf", "user:a", "1")
		o := entry("other.gguf", "user:a", "2")

		Expect(do(http.MethodPost, "/api/prompt-cache/clear", `{"model":"missing"}`).Code).To(Equal(http.StatusNotFound))

		rec := do(http.MethodPost, "/api/prompt-cache/clear", `{"model":"qwen"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"files":1`))
		Expect(q).ToNot(BeAnExistingFile())
		Expect(o).To(BeAnExistingFile())

		rec = do(http.MethodPost, "/api/prompt-cache/clear", "")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(o).ToNot(BeAnExistingFile())
	})

	It("clears the cached prompts of the caller only", func() {
		// Without authentication the caller is the local namespace.
		mine := entry("qwen.gguf", promptcache.Local, "1")
		other := entry("qwen.gguf", "user:a", "2")
		shared := entry("qwen.gguf", promptcache.Shared, "3")
		rec := do(http.MethodDelete, "/api/prompt-cache/mine", "")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(mine).ToNot(BeAnExistingFile())
		Expect(other).To(BeAnExistingFile())
		Expect(shared).To(BeAnExistingFile())
	})

	It("gives backends the root, and removes it when the setting is off", func() {
		env := app.PromptCacheNode().BackendEnv()
		Expect(env).To(HaveKeyWithValue(config.EnvPromptCacheRoot, root))
		app.ApplicationConfig().PromptCache.DiskDisabled = true
		Expect(app.PromptCacheNode().BackendEnv()).To(HaveKeyWithValue(config.EnvPromptCacheRoot, ""))
	})
})
