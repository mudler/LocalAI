package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing/iotest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/failover"
	"github.com/mudler/LocalAI/core/services/routing/admission"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

var _ = Describe("failover chains in the request pipeline", func() {
	var (
		app      *echo.Echo
		fm       *failover.Manager
		re       *RequestExtractor
		limiter  *admission.Limiter
		mu       sync.Mutex
		calls    []string
		behavior map[string]func(c echo.Context) error
	)

	served := func(c echo.Context) error {
		cfg := c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		return c.JSON(http.StatusOK, map[string]string{"served": cfg.Name})
	}

	handler := func(c echo.Context) error {
		cfg := c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		mu.Lock()
		calls = append(calls, cfg.Name)
		b := behavior[cfg.Name]
		mu.Unlock()
		if b == nil {
			return served(c)
		}
		return b(c)
	}

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}
	chat := func(model string) *httptest.ResponseRecorder {
		return post("/v1/chat/completions", `{"model":"`+model+`","messages":[{"role":"user","content":"hi"}]}`)
	}

	BeforeEach(func() {
		calls = nil
		behavior = map[string]func(c echo.Context) error{}
		dir := GinkgoT().TempDir()
		write := func(name, body string) {
			Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o600)).To(Succeed())
		}
		write("a", "name: a\nbackend: fake-a\n")
		write("b", "name: b\nbackend: fake-b\n")
		write("plain", "name: plain\nbackend: fake-p\n")
		write("chain", "name: chain\nfailover:\n  targets:\n    - model: a\n    - model: b\n")
		write("capped", "name: capped\nbackend: fake-c\nlimits:\n  max_concurrent: 1\n")
		write("off", "name: off\nbackend: fake-o\ndisabled: true\n")
		write("chain-capped", "name: chain-capped\nfailover:\n  targets:\n    - model: capped\n    - model: b\n")
		write("chain-off", "name: chain-off\nfailover:\n  targets:\n    - model: off\n    - model: b\n")
		write("remote", "name: remote\nbackend: cloud-proxy\nproxy:\n  mode: passthrough\n  upstream_url: http://127.0.0.1:1/v1/chat/completions\n")
		write("remote-mapped", "name: remote-mapped\nbackend: cloud-proxy\nproxy:\n  mode: translate\n  provider: openai\n  upstream_url: http://127.0.0.1:1/v1/chat/completions\n  upstream_model: big-llm\n")
		write("chain-remote", "name: chain-remote\nfailover:\n  targets:\n    - model: remote\n    - model: remote-mapped\n")

		ss := &system.SystemState{Model: system.Model{ModelsPath: dir}}
		appConfig := config.NewApplicationConfig()
		appConfig.SystemState = ss
		mcl := config.NewModelConfigLoader(dir)
		Expect(mcl.LoadModelConfigsFromPath(dir)).To(Succeed())
		re = NewRequestExtractor(mcl, model.NewModelLoader(ss), appConfig)
		fm = failover.New(mcl)
		// The scheduler's first tick syncs in the application; HasChains
		// answers from the last sync.
		fm.Sync()
		re.SetFailoverManager(fm)

		app = echo.New()
		// echo's default handler hides internal error messages; the specs
		// below check which target's error reached the client.
		app.HTTPErrorHandler = func(err error, c echo.Context) {
			code := http.StatusInternalServerError
			var he *echo.HTTPError
			if errors.As(err, &he) {
				code = he.Code
			}
			_ = c.JSON(code, map[string]string{"error": err.Error()})
		}
		app.POST("/v1/chat/completions", handler,
			re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }))
		app.POST("/v1/audio/transcriptions", handler,
			re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }))
		limiter = admission.New()
		app.POST("/v1/chat/admitted", handler,
			re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
			AdmissionControl(limiter, nil))
		// The real transcription route resolves a default model first, which
		// parses the multipart form before SetModelAndConfig runs.
		app.POST("/v1/audio/transcriptions-default", handler,
			re.BuildFilteredFirstAvailableDefaultModel(config.NoFilterFn),
			re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }))
	})

	It("serves from the next target when the first fails before responding", func() {
		behavior["a"] = func(echo.Context) error { return errors.New("dial tcp: connection refused") }
		rec := chat("chain")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"served":"b"`))
		Expect(rec.Header().Get(HeaderServedModel)).To(Equal("b"))
		Expect(rec.Header().Get(HeaderFailover)).To(Equal("fallback"))
		Expect(calls).To(Equal([]string{"a", "b"}))
		st, _ := fm.ChainStatus("chain")
		Expect(st.Active).To(Equal("b"))
	})

	It("sends a remote target its own upstream model, not the chain name", func() {
		upstream := map[string]string{}
		record := func(c echo.Context) error {
			cfg := c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
			mu.Lock()
			upstream[cfg.Name] = cfg.Proxy.UpstreamModel
			mu.Unlock()
			return nil
		}
		behavior["remote"] = func(c echo.Context) error {
			_ = record(c)
			return errors.New("dial tcp: connection refused")
		}
		behavior["remote-mapped"] = func(c echo.Context) error { _ = record(c); return served(c) }
		Expect(chat("chain-remote").Code).To(Equal(http.StatusOK))
		// The probe checks the same name, so a request cannot fail on a model
		// the liveness probe just found.
		Expect(upstream).To(Equal(map[string]string{"remote": "remote", "remote-mapped": "big-llm"}))
		for _, name := range []string{"remote", "remote-mapped"} {
			cfg, ok := re.modelConfigLoader.GetModelConfig(name)
			Expect(ok).To(BeTrue())
			Expect(upstream[name]).To(Equal(failover.UpstreamModel(cfg)))
		}
		stored, _ := re.modelConfigLoader.GetModelConfig("remote")
		Expect(stored.Proxy.UpstreamModel).To(BeEmpty(), "the shared config must not change")
	})

	It("serves the primary without the failover header", func() {
		rec := chat("chain")
		Expect(rec.Header().Get(HeaderServedModel)).To(Equal("a"))
		Expect(rec.Header().Get(HeaderFailover)).To(BeEmpty())
	})

	It("drops a buffered 5xx response and retries", func() {
		behavior["a"] = func(c echo.Context) error {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "no healthy nodes"})
		}
		rec := chat("chain")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).ToNot(ContainSubstring("no healthy nodes"))
	})

	It("does not retry after streaming started, and trips the target", func() {
		behavior["a"] = func(c echo.Context) error {
			c.Response().Header().Set("Content-Type", "text/event-stream")
			_, _ = c.Response().Write([]byte("data: x\n\n"))
			c.Response().Flush()
			return errors.New("connection reset by peer")
		}
		rec := chat("chain")
		Expect(rec.Body.String()).To(HavePrefix("data: x"))
		Expect(calls).To(Equal([]string{"a"}))
		st, _ := fm.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(failover.StateDown))
	})

	It("does not retry or trip on 4xx", func() {
		behavior["a"] = func(echo.Context) error { return echo.NewHTTPError(http.StatusBadRequest, "bad") }
		rec := chat("chain")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(calls).To(Equal([]string{"a"}))
		st, _ := fm.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(failover.StateHealthy))
	})

	It("sends a handler's own 429 as is, without retrying or tripping", func() {
		behavior["a"] = func(c echo.Context) error {
			return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "slow down"})
		}
		rec := chat("chain")
		Expect(rec.Code).To(Equal(http.StatusTooManyRequests))
		Expect(rec.Body.String()).To(ContainSubstring("slow down"))
		Expect(calls).To(Equal([]string{"a"}))
		st, _ := fm.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(failover.StateHealthy))
	})

	It("does not retry when the client cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		behavior["a"] = func(echo.Context) error { cancel(); return context.Canceled }
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"chain","messages":[{"role":"user","content":"hi"}]}`)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		app.ServeHTTP(httptest.NewRecorder(), req)
		Expect(calls).To(Equal([]string{"a"}))
		st, _ := fm.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(failover.StateHealthy))
	})

	It("gives each attempt a fresh request", func() {
		behavior["a"] = func(c echo.Context) error {
			in := c.Get(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
			in.Messages = nil
			return errors.New("dial tcp: connection refused")
		}
		behavior["b"] = func(c echo.Context) error {
			in := c.Get(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
			Expect(in.Messages).To(HaveLen(1))
			return served(c)
		}
		Expect(chat("chain").Code).To(Equal(http.StatusOK))
	})

	It("degraded: tries every target in priority order and returns the last error", func() {
		behavior["a"] = func(echo.Context) error { return errors.New("dial tcp: a down") }
		behavior["b"] = func(echo.Context) error { return errors.New("dial tcp: b down") }
		chat("chain") // trips both
		calls = nil
		rec := chat("chain")
		Expect(calls).To(Equal([]string{"a", "b"}))
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		Expect(rec.Body.String()).To(ContainSubstring("b down"))
		Expect(rec.Header().Get(HeaderFailover)).To(Equal("degraded"))
	})

	DescribeTable("replays a multipart body for the next target", func(path string) {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("model", "chain")
		fw, _ := mw.CreateFormFile("file", "a.wav")
		_, _ = fw.Write(bytes.Repeat([]byte{7}, 4096))
		Expect(mw.Close()).To(Succeed())
		size := func(c echo.Context) int64 {
			fh, err := c.FormFile("file")
			Expect(err).ToNot(HaveOccurred())
			f, _ := fh.Open()
			n, _ := io.Copy(io.Discard, f)
			return n
		}
		behavior["a"] = func(c echo.Context) error { size(c); return errors.New("dial tcp: refused") }
		behavior["b"] = func(c echo.Context) error {
			Expect(size(c)).To(Equal(int64(4096)))
			return served(c)
		}
		req := httptest.NewRequest(http.MethodPost, path, &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(calls).To(Equal([]string{"a", "b"}))
	},
		Entry("read by the handler", "/v1/audio/transcriptions"),
		Entry("parsed before SetModelAndConfig", "/v1/audio/transcriptions-default"),
	)

	It("spills an admission rejection to the next target without tripping it", func() {
		release, ok := limiter.Acquire("capped", 1)
		Expect(ok).To(BeTrue())
		defer release()
		rec := post("/v1/chat/admitted", `{"model":"chain-capped","messages":[{"role":"user","content":"hi"}]}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring(`"served":"b"`))
		Expect(rec.Header().Get("Retry-After")).To(BeEmpty())
		st, _ := fm.ChainStatus("chain-capped")
		Expect(st.Targets[0].State).To(Equal(failover.StateHealthy))
		Expect(st.Active).To(Equal("capped"))
	})

	It("spills a capability gap (gRPC Unimplemented) to the next target without tripping it", func() {
		behavior["a"] = func(echo.Context) error {
			return grpcstatus.Error(codes.Unimplemented, "localai-proxy: Rerank has no upstream counterpart")
		}
		rec := chat("chain")
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring(`"served":"b"`))
		Expect(calls).To(Equal([]string{"a", "b"}))
		st, _ := fm.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(failover.StateHealthy))
	})

	It("spills a written 501 to the next target without tripping it", func() {
		behavior["a"] = func(c echo.Context) error {
			return c.JSON(http.StatusNotImplemented, map[string]string{"error": "not supported"})
		}
		rec := chat("chain")
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring(`"served":"b"`))
		Expect(calls).To(Equal([]string{"a", "b"}))
		st, _ := fm.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(failover.StateHealthy))
	})

	It("fails a rate-limited target (gRPC ResourceExhausted) over to the next target and trips it", func() {
		behavior["a"] = func(echo.Context) error {
			return grpcstatus.Error(codes.ResourceExhausted, "localai-proxy: upstream /v1/chat/completions returned 429: slow down")
		}
		rec := chat("chain")
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring(`"served":"b"`))
		Expect(calls).To(Equal([]string{"a", "b"}))
		st, _ := fm.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(failover.StateDown))
	})

	It("skips a disabled target without tripping it", func() {
		rec := chat("chain-off")
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring(`"served":"b"`))
		Expect(calls).To(Equal([]string{"b"}))
		st, _ := fm.ChainStatus("chain-off")
		Expect(st.Targets[0].State).To(Equal(failover.StateHealthy))
	})

	It("counts a 4xx response as neither success nor failure", func() {
		behavior["a"] = func(echo.Context) error { return errors.New("dial tcp: a down") }
		behavior["b"] = func(echo.Context) error { return errors.New("dial tcp: b down") }
		chat("chain") // trips both
		behavior["a"] = func(c echo.Context) error {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "bad"})
		}
		rec := chat("chain")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		st, _ := fm.ChainStatus("chain")
		// a is a cold local target: a success would have recovered it.
		Expect(st.Targets[0].State).To(Equal(failover.StateDown))
	})

	It("stops recording the body once the model is known not to be a chain", func() {
		behavior["plain"] = func(c echo.Context) error {
			rb, ok := c.Request().Body.(*replayBody)
			Expect(ok).To(BeTrue())
			Expect(rb.replayable()).To(BeFalse())
			Expect(rb.buf.Cap()).To(BeZero())
			return served(c)
		}
		Expect(chat("plain").Code).To(Equal(http.StatusOK))
	})

	It("does not record the body without a failover manager", func() {
		re.SetFailoverManager(nil)
		behavior["plain"] = func(c echo.Context) error {
			_, ok := c.Request().Body.(*replayBody)
			Expect(ok).To(BeFalse())
			return served(c)
		}
		Expect(chat("plain").Code).To(Equal(http.StatusOK))
	})

	It("releases the recorded bytes on overflow", func() {
		// One byte per read, so some bytes are recorded before the limit is hit.
		rb := &replayBody{src: io.NopCloser(iotest.OneByteReader(bytes.NewReader(make([]byte, 64)))), limit: 16}
		_, _ = io.ReadAll(rb)
		Expect(rb.replayable()).To(BeFalse())
		Expect(rb.buf.Cap()).To(BeZero())
	})

	It("leaves plain models untouched", func() {
		behavior["plain"] = func(c echo.Context) error {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "boom"})
		}
		rec := chat("plain")
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		Expect(rec.Body.String()).To(ContainSubstring("boom"))
		Expect(rec.Header().Get(HeaderServedModel)).To(BeEmpty())
	})
})
