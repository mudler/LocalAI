package e2e_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("Failover chains", Label("failover"), func() {
	postJSON := func(path string, body map[string]any) *http.Response {
		b, err := json.Marshal(body)
		Expect(err).ToNot(HaveOccurred())
		resp, err := http.Post(apiURL+path, "application/json", bytes.NewReader(b))
		Expect(err).ToNot(HaveOccurred())
		return resp
	}
	expectServedByMock := func(resp *http.Response) {
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		Expect(resp.StatusCode).To(BeNumerically("<", 300), "%s\nheaders: %v", body, resp.Header)
		Expect(resp.Header.Get("X-LocalAI-Served-Model")).To(Equal("mock-model"))
		Expect(resp.Header.Get("X-LocalAI-Failover")).To(Equal("fallback"))
	}

	// The entry name is the chain suffix: chain-<name> is written by the suite.
	DescribeTable("retries every endpoint family on the next target",
		func(path string, body func(model string) map[string]any) {
			expectServedByMock(postJSON(path, body("chain-"+CurrentSpecReport().LeafNodeText)))
		},
		Entry("chat", "/chat/completions", func(m string) map[string]any {
			return map[string]any{"model": m, "messages": []map[string]string{{"role": "user", "content": "hi"}}}
		}),
		Entry("completion", "/completions", func(m string) map[string]any {
			return map[string]any{"model": m, "prompt": "hi"}
		}),
		Entry("embeddings", "/embeddings", func(m string) map[string]any {
			return map[string]any{"model": m, "input": "hi"}
		}),
		Entry("tts", "/audio/speech", func(m string) map[string]any {
			return map[string]any{"model": m, "input": "hi", "voice": "default"}
		}),
		Entry("image", "/images/generations", func(m string) map[string]any {
			return map[string]any{"model": m, "prompt": "a cat", "size": "256x256"}
		}),
		Entry("rerank", "/rerank", func(m string) map[string]any {
			return map[string]any{"model": m, "query": "q", "documents": []string{"a", "b"}}
		}),
		Entry("vad", "/vad", func(m string) map[string]any {
			return map[string]any{"model": m, "audio": []float32{0, 0, 0, 0}}
		}),
	)

	It("retries transcription with the multipart body", func() {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		Expect(mw.WriteField("model", "chain-transcription")).To(Succeed())
		fw, err := mw.CreateFormFile("file", "a.wav")
		Expect(err).ToNot(HaveOccurred())
		// 200 ms of 16 kHz mono silence.
		_, err = fw.Write(wavFromPCM(make([]byte, 6400), 16000))
		Expect(err).ToNot(HaveOccurred())
		Expect(mw.Close()).To(Succeed())
		resp, err := http.Post(apiURL+"/audio/transcriptions", mw.FormDataContentType(), &body)
		Expect(err).ToNot(HaveOccurred())
		expectServedByMock(resp)
	})

	Describe("remote targets", Ordered, func() {
		var up1, up2 *fakeOpenAIUpstreamServer

		chatReply := func([]byte) (int, string, string) {
			return 200, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`, "application/json"
		}

		BeforeAll(func() {
			if cloudProxyPath == "" {
				Skip("cloud-proxy backend binary not built (make build-cloud-proxy-backend)")
			}
			up1, up2 = newFakeOpenAIUpstream(), newFakeOpenAIUpstream()
			DeferCleanup(up1.Close)
			DeferCleanup(up2.Close)
			up1.SetModels("up-1")
			up2.SetModels("up-2")
			registerFailoverRemoteModels(up1.URL(), up2.URL())
		})

		It("fails over when the primary upstream errors and fails back when it recovers", func() {
			up1.SetScript(func([]byte) (int, string, string) {
				return 503, `{"error":"no healthy nodes"}`, "application/json"
			})
			up2.SetScript(chatReply)

			resp := postJSON("/chat/completions", map[string]any{"model": "chain-remote", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			Expect(resp.StatusCode).To(Equal(200), string(body))
			Expect(resp.Header.Get("X-LocalAI-Served-Model")).To(Equal("up-2"))
			Expect(resp.Header.Get("X-LocalAI-Failover")).To(Equal("fallback"))
			Expect(chainActive("chain-remote")).To(Equal("up-2"))
			// The targets set no upstream_model: each upstream must get its
			// target's name (what the liveness probe checks), not the chain
			// name the client sent. up-2 is healthy, so only this request
			// posted to it.
			Expect(upstreamBodyModel(up2)).To(Equal("up-2"))

			up1.SetScript(chatReply)
			Eventually(func() string { return chainActive("chain-remote") }, 30*time.Second, 500*time.Millisecond).
				Should(Equal("up-1"))

			resp2 := postJSON("/chat/completions", map[string]any{"model": "chain-remote", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
			defer func() { _ = resp2.Body.Close() }()
			Expect(resp2.StatusCode).To(Equal(200))
			Expect(resp2.Header.Get("X-LocalAI-Served-Model")).To(Equal("up-1"))
			Expect(resp2.Header.Get("X-LocalAI-Failover")).To(BeEmpty())
			Expect(upstreamBodyModel(up1)).To(Equal("up-1"))
		})
	})
})

// upstreamBodyModel returns the "model" field of the last request body the
// fake upstream recorded.
func upstreamBodyModel(up *fakeOpenAIUpstreamServer) string {
	_, _, _, body := up.recorder.snapshot()
	var req struct {
		Model string `json:"model"`
	}
	Expect(json.Unmarshal(body, &req)).To(Succeed(), string(body))
	return req.Model
}

// chainActive returns the active target of a chain as the REST status reports
// it, or "" when the status cannot be read.
func chainActive(chain string) string {
	r, err := http.Get(anthropicBaseURL + "/api/failover/" + chain)
	if err != nil {
		return ""
	}
	defer func() { _ = r.Body.Close() }()
	var st struct {
		Active string `json:"active"`
	}
	_ = json.NewDecoder(r.Body).Decode(&st)
	return st.Active
}

// registerFailoverRemoteModels registers two cloud-proxy passthrough models
// (up-1, up-2) and a chain over them. The upstream URLs exist only at runtime,
// so the YAMLs are written after startup and the loader re-reads the models
// directory; the failover manager picks the chain up on its next tick.
func registerFailoverRemoteModels(url1, url2 string) {
	proxyModel := func(name, upstream string) map[string]any {
		return map[string]any{
			"name":       name,
			"backend":    "cloud-proxy",
			"parameters": map[string]any{"model": name + ".bin"},
			"proxy": map[string]any{
				"mode":         "passthrough",
				"provider":     "openai",
				"upstream_url": upstream + "/v1/chat/completions",
				"api_key_env":  "CLOUD_PROXY_E2E_OPENAI_KEY",
			},
		}
	}
	chain := map[string]any{
		"name": "chain-remote",
		"failover": map[string]any{
			"targets":  []map[string]any{{"model": "up-1"}, {"model": "up-2"}},
			"probe":    map[string]any{"interval": "1s"},
			"recovery": map[string]any{"probes": 2, "min_dwell": "2s"},
		},
	}
	for _, cfg := range []map[string]any{proxyModel("up-1", url1), proxyModel("up-2", url2), chain} {
		data, err := yaml.Marshal(cfg)
		Expect(err).ToNot(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(modelsPath, cfg["name"].(string)+".yaml"), data, 0644)).To(Succeed())
	}
	Expect(localAIApp.ModelConfigLoader().LoadModelConfigsFromPath(modelsPath)).To(Succeed())
	Eventually(func() string { return chainActive("chain-remote") }, 10*time.Second, 200*time.Millisecond).
		Should(Equal("up-1"))
}
