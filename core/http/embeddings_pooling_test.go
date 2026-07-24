package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/xlog"
)

// embedTestModel is served by the mock backend. embd_normalize:-1 disables
// Go-side normalization so the pooled goldens below stay hand-computable.
const embedTestModel = "embed-pooling-model"

var _ = Describe("Embeddings chat messages[] and Go-side pooling", func() {
	var app *echo.Echo
	var localApp *application.Application
	var localModelDir string
	var c context.Context
	var cancel context.CancelFunc

	const baseURL = "http://127.0.0.1:9092"

	postEmbeddings := func(body string) (int, map[string]any) {
		resp, err := http.Post(baseURL+"/v1/embeddings", "application/json", bytes.NewBufferString(body))
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		payload, err := io.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())
		decoded := map[string]any{}
		// Some error shapes are plain text; tolerate that and return nil map.
		_ = json.Unmarshal(payload, &decoded)
		if decoded == nil {
			decoded = map[string]any{}
		}
		decoded["__raw"] = string(payload)
		return resp.StatusCode, decoded
	}

	embeddingOf := func(response map[string]any) []float64 {
		data, ok := response["data"].([]any)
		Expect(ok).To(BeTrue(), "response has no data array: %v", response["__raw"])
		Expect(data).To(HaveLen(1))
		item := data[0].(map[string]any)
		raw := item["embedding"].([]any)
		out := make([]float64, len(raw))
		for i, v := range raw {
			out[i] = v.(float64)
		}
		return out
	}

	BeforeEach(func() {
		if mockBackendPath == "" {
			Skip("mock-backend binary not built; run 'make build-mock-backend'")
		}

		var err error
		c, cancel = context.WithCancel(context.Background())

		localModelDir, err = os.MkdirTemp("", "embeddings-pooling-models-")
		Expect(err).ToNot(HaveOccurred())

		mockModelYAML := "name: " + embedTestModel + "\n" +
			"backend: mock-backend\n" +
			"embeddings: true\n" +
			"options:\n" +
			"- embd_normalize:-1\n" +
			"parameters:\n" +
			"  model: mock-model.bin\n"
		Expect(os.WriteFile(filepath.Join(localModelDir, embedTestModel+".yaml"), []byte(mockModelYAML), 0644)).To(Succeed())

		systemState, err := system.GetSystemState(
			system.WithBackendPath(backendDir),
			system.WithModelPath(localModelDir),
		)
		Expect(err).ToNot(HaveOccurred())

		localApp, err = application.New(
			config.WithDebug(true),
			config.WithContext(c),
			config.WithSystemState(systemState),
		)
		Expect(err).ToNot(HaveOccurred())
		localApp.ModelLoader().SetExternalBackend("mock-backend", mockBackendPath)

		app, err = API(localApp)
		Expect(err).ToNot(HaveOccurred())

		go func() {
			if err := app.Start("127.0.0.1:9092"); err != nil && err != http.ErrServerClosed {
				xlog.Error("server error", "error", err)
			}
		}()

		Eventually(func() error {
			resp, err := http.Get(baseURL + "/healthz")
			if err != nil {
				return err
			}
			_ = resp.Body.Close()
			return nil
		}, "2m").ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		if localApp != nil {
			_ = localApp.Shutdown()
			localApp = nil
		}
		cancel()
		if app != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			Expect(app.Shutdown(ctx)).To(Succeed())
			app = nil
		}
		if localModelDir != "" {
			_ = os.RemoveAll(localModelDir)
		}
	})

	It("embeds a messages[] conversation with decayed_mean pooling to the exact golden", func() {
		// The rendered fallback conversation is "user: per-token: alpha beta gamma",
		// so the mock returns 3 per-token vectors of dim 8 with
		// vec[i][j] = (i+1)/(j+2). decayed_mean with half-life 1 weighs them
		// [0.25 0.5 1]/1.75; embd_normalize:-1 skips normalization, so the
		// exact pooled value is hand-computable.
		status, response := postEmbeddings(`{
			"model": "` + embedTestModel + `",
			"messages": [{"role": "user", "content": "per-token: alpha beta gamma"}],
			"pooling": "decayed_mean",
			"pooling_half_life_tokens": 1
		}`)
		Expect(status).To(Equal(200), "body: %v", response["__raw"])
		got := embeddingOf(response)
		Expect(got).To(HaveLen(8))
		for j := 0; j < 8; j++ {
			want := (0.25*1 + 0.5*2 + 1.0*3) / 1.75 / float64(j+2)
			Expect(got[j]).To(BeNumerically("~", want, 1e-6), "component %d", j)
		}
	})

	It("passes plain input through unchanged when no pooling is requested", func() {
		status, response := postEmbeddings(`{"model": "` + embedTestModel + `", "input": "hello"}`)
		Expect(status).To(Equal(200), "body: %v", response["__raw"])
		got := embeddingOf(response)
		// The mock's default vector: index%100/100, 768 wide, passed through
		// untouched (no Go-side pooling, no normalization).
		Expect(got).To(HaveLen(768))
		Expect(got[0]).To(BeNumerically("~", 0.0, 1e-6))
		Expect(got[1]).To(BeNumerically("~", 0.01, 1e-6))
		Expect(got[99]).To(BeNumerically("~", 0.99, 1e-6))
		Expect(got[100]).To(BeNumerically("~", 0.0, 1e-6))
	})

	It("embeds messages[] without pooling via the backend's own vector", func() {
		status, response := postEmbeddings(`{
			"model": "` + embedTestModel + `",
			"messages": [{"role": "user", "content": "hello"}]
		}`)
		Expect(status).To(Equal(200), "body: %v", response["__raw"])
		Expect(embeddingOf(response)).To(HaveLen(768))
	})

	It("fails closed when pooling is requested but the backend reports no shape", func() {
		// "no-shape:" makes the mock omit tokens/dim, simulating a backend
		// built before EmbeddingResult carried shape fields.
		status, response := postEmbeddings(`{
			"model": "` + embedTestModel + `",
			"input": "no-shape: hello",
			"pooling": "mean"
		}`)
		Expect(status).ToNot(Equal(200))
		Expect(response["__raw"]).To(ContainSubstring("no shape"))
	})

	It("rejects input combined with messages", func() {
		status, response := postEmbeddings(`{
			"model": "` + embedTestModel + `",
			"input": "hello",
			"messages": [{"role": "user", "content": "hello"}]
		}`)
		Expect(status).To(Equal(400), "body: %v", response["__raw"])
	})

	It("rejects an unknown pooling scheme", func() {
		status, response := postEmbeddings(`{
			"model": "` + embedTestModel + `",
			"input": "hello",
			"pooling": "sideways"
		}`)
		Expect(status).To(Equal(400), "body: %v", response["__raw"])
		Expect(response["__raw"]).To(ContainSubstring("pooling"))
	})
})
