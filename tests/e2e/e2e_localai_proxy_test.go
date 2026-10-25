package e2e_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// The localai-proxy specs point the backend at this same test server: a
// request to an lp-* model leaves LocalAI through the localai-proxy process,
// comes back in over REST and is answered by the mock model it names, so the
// whole round trip (core -> gRPC -> REST upstream -> gRPC reply) is real.
var _ = Describe("localai-proxy backend", Label("failover"), Ordered, func() {
	BeforeAll(func() {
		if localAIProxyPath == "" {
			Skip("localai-proxy backend binary not built (make build-localai-proxy-backend)")
		}
		registerModelConfigs(
			localAIProxyModel("lp-chat", anthropicBaseURL, "mock-model", "chat"),
			localAIProxyModel("lp-embeddings", anthropicBaseURL, "mock-model", "embeddings"),
			localAIProxyModel("lp-tts", anthropicBaseURL, "mock-model", "tts"),
			localAIProxyModel("lp-transcription", anthropicBaseURL, "mock-model", "transcript"),
			// The upstream serves this target from a model whose load always
			// fails, so every request through it errors with a 5xx.
			localAIProxyModel("lp-broken", anthropicBaseURL, "fail-chat", "chat"),
			map[string]any{
				"name": "chain-lp",
				"failover": map[string]any{
					"targets": []map[string]any{{"model": "lp-broken"}, {"model": "mock-model"}},
				},
			},
		)
		Eventually(func() string { return chainActive("chain-lp") }, 10*time.Second, 200*time.Millisecond).
			Should(Equal("lp-broken"))
	})

	// sameBody posts the same request to the proxied model and to the model
	// the upstream serves it from, and returns both bodies: the proxy answered
	// with the upstream's answer when they match.
	sameBody := func(path string, body func(model string) map[string]any, proxied, upstream string) (string, string) {
		get := func(model string) string {
			resp := postJSONTo(path, body(model))
			defer func() { _ = resp.Body.Close() }()
			b, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK), "%s: %s", model, b)
			return string(b)
		}
		return get(proxied), get(upstream)
	}

	It("answers chat with the upstream model's reply", func() {
		chat := func(m string) map[string]any {
			return map[string]any{"model": m, "messages": []map[string]string{{"role": "user", "content": "hi"}}}
		}
		got, want := sameBody("/chat/completions", chat, "lp-chat", "mock-model")
		Expect(chatContent(got)).ToNot(BeEmpty())
		Expect(chatContent(got)).To(Equal(chatContent(want)))
	})

	It("answers embeddings with the upstream model's vector", func() {
		embed := func(m string) map[string]any { return map[string]any{"model": m, "input": "hello"} }
		got, want := sameBody("/embeddings", embed, "lp-embeddings", "mock-model")
		Expect(embeddingVector(got)).ToNot(BeEmpty())
		Expect(embeddingVector(got)).To(Equal(embeddingVector(want)))
	})

	It("answers TTS with the upstream model's audio", func() {
		speech := func(m string) map[string]any { return map[string]any{"model": m, "input": "hello", "voice": "default"} }
		got, want := sameBody("/audio/speech", speech, "lp-tts", "mock-model")
		Expect(got).To(HavePrefix("RIFF"))
		Expect(got).To(Equal(want))
	})

	It("answers transcription with the upstream model's text", func() {
		transcribe := func(model string) string {
			var body bytes.Buffer
			mw := multipart.NewWriter(&body)
			Expect(mw.WriteField("model", model)).To(Succeed())
			fw, err := mw.CreateFormFile("file", "a.wav")
			Expect(err).ToNot(HaveOccurred())
			_, err = fw.Write(wavFromPCM(make([]byte, 6400), 16000))
			Expect(err).ToNot(HaveOccurred())
			Expect(mw.Close()).To(Succeed())
			resp, err := http.Post(apiURL+"/audio/transcriptions", mw.FormDataContentType(), &body)
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			b, _ := io.ReadAll(resp.Body)
			Expect(resp.StatusCode).To(Equal(http.StatusOK), "%s: %s", model, b)
			var out struct {
				Text string `json:"text"`
			}
			Expect(json.Unmarshal(b, &out)).To(Succeed(), string(b))
			return out.Text
		}
		got := transcribe("lp-transcription")
		Expect(got).To(HavePrefix("transcribed:"))
		Expect(got).To(Equal(transcribe("mock-model")))
	})

	It("fails over from a proxy target whose upstream model fails to the local target", func() {
		resp := postJSONTo("/chat/completions", map[string]any{
			"model":    "chain-lp",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		})
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		Expect(resp.StatusCode).To(Equal(http.StatusOK), string(b))
		Expect(resp.Header.Get("X-LocalAI-Served-Model")).To(Equal("mock-model"))
		Expect(resp.Header.Get("X-LocalAI-Failover")).To(Equal("fallback"))
		Expect(chainActive("chain-lp")).To(Equal("mock-model"))
	})
})

// postJSONTo posts body as JSON to an /v1 path of the test server.
func postJSONTo(path string, body map[string]any) *http.Response {
	b, err := json.Marshal(body)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	resp, err := http.Post(apiURL+path, "application/json", bytes.NewReader(b))
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return resp
}

func chatContent(body string) string {
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	ExpectWithOffset(1, json.Unmarshal([]byte(body), &out)).To(Succeed(), body)
	ExpectWithOffset(1, out.Choices).ToNot(BeEmpty(), body)
	return out.Choices[0].Message.Content
}

func embeddingVector(body string) []float32 {
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	ExpectWithOffset(1, json.Unmarshal([]byte(body), &out)).To(Succeed(), body)
	ExpectWithOffset(1, out.Data).ToNot(BeEmpty(), body)
	return out.Data[0].Embedding
}

// localAIProxyModel is a localai-proxy config serving upstreamModel from the
// LocalAI at baseURL. known_usecases is what failover matches targets on, and
// "chat" also makes the proxy send structured messages upstream.
func localAIProxyModel(name, baseURL, upstreamModel string, usecases ...string) map[string]any {
	return map[string]any{
		"name":           name,
		"backend":        "localai-proxy",
		"known_usecases": usecases,
		"parameters":     map[string]any{"model": name + ".bin"},
		"proxy": map[string]any{
			"upstream_url":   baseURL,
			"upstream_model": upstreamModel,
		},
	}
}

// registerModelConfigs writes model YAMLs after startup and has the loader
// re-read the models directory, for configs that embed runtime URLs. The
// failover manager picks new chains up on its next tick.
func registerModelConfigs(cfgs ...map[string]any) {
	for _, cfg := range cfgs {
		data, err := yaml.Marshal(cfg)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		ExpectWithOffset(1, os.WriteFile(filepath.Join(modelsPath, cfg["name"].(string)+".yaml"), data, 0644)).To(Succeed())
	}
	ExpectWithOffset(1, localAIApp.ModelConfigLoader().LoadModelConfigsFromPath(modelsPath)).To(Succeed())
}

// upstreamGate is a reverse proxy in front of the test server that a spec
// can take down: while down it answers 503, as an upstream LocalAI with no
// healthy backend would, so a localai-proxy target behind it starts failing
// without restarting its backend process.
type upstreamGate struct {
	srv  *httptest.Server
	down atomic.Bool
}

func newUpstreamGate(target string) *upstreamGate {
	u, err := url.Parse(target)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	g := &upstreamGate{}
	rp := httputil.NewSingleHostReverseProxy(u)
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.down.Load() {
			http.Error(w, `{"error":{"message":"no healthy backend"}}`, http.StatusServiceUnavailable)
			return
		}
		rp.ServeHTTP(w, r)
	}))
	return g
}

func (g *upstreamGate) URL() string       { return g.srv.URL }
func (g *upstreamGate) Close()            { g.srv.Close() }
func (g *upstreamGate) SetDown(down bool) { g.down.Store(down) }
