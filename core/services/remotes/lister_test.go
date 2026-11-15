package remotes_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/remotes"
)

const (
	testTimeout = 2 * time.Second
	testMaxBody = 1 << 20
)

func list(kind remotes.Kind, url, key string) ([]remotes.UpstreamModel, string, string) {
	m, step, msg, _ := remotes.ListUpstream(context.Background(), kind, url, key, testTimeout, testMaxBody)
	return m, step, msg
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func ids(models []remotes.UpstreamModel) []string {
	out := []string{}
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

var _ = Describe("LocalAI lister", func() {
	It("reads ids and capabilities, sends the key and maps names to usecases", func() {
		var auth []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth = append(auth, r.Header.Get("Authorization"))
			switch r.URL.Path {
			case "/v1/models":
				writeJSON(w, map[string]any{"data": []map[string]any{{"id": "qwen"}, {"id": "bge"}, {"id": "weird"}, {"id": "qwen"}, {"id": "nocaps"}}})
			case "/v1/models/capabilities":
				writeJSON(w, map[string]any{"data": []map[string]any{
					{"id": "qwen", "capabilities": []string{"chat", "tools", "thinking", "vision"}},
					{"id": "bge", "capabilities": []string{"embeddings"}},
					{"id": "weird", "capabilities": []string{"decisions", "tools"}},
				}})
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()
		models, step, _ := list(remotes.KindLocalAI, srv.URL+"/v1/", "sk-test-key")
		Expect(step).To(BeEmpty())
		Expect(models).To(Equal([]remotes.UpstreamModel{
			{ID: "bge", Usecases: []string{"embeddings"}},
			{ID: "nocaps", Usecases: []string{"chat"}},
			{ID: "qwen", Usecases: []string{"chat", "vision"}},
			{ID: "weird", Usecases: []string{"chat"}},
		}))
		for _, a := range auth {
			Expect(a).To(Equal("Bearer sk-test-key"))
		}
	})

	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusInternalServerError} {
		It(fmt.Sprintf("falls back to chat when capabilities answers %d", status), func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/models" {
					writeJSON(w, map[string]any{"data": []map[string]any{{"id": "a"}, {"id": "b"}}})
					return
				}
				w.WriteHeader(status)
			}))
			defer srv.Close()
			models, step, _ := list(remotes.KindLocalAI, srv.URL, "")
			Expect(step).To(BeEmpty())
			Expect(models).To(Equal([]remotes.UpstreamModel{{ID: "a", Usecases: []string{"chat"}}, {ID: "b", Usecases: []string{"chat"}}}))
		})
	}

	It("keeps a path prefix of a reverse proxy", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/ai/v1/models" {
				writeJSON(w, map[string]any{"data": []map[string]any{{"id": "a"}}})
				return
			}
			http.NotFound(w, r)
		}))
		defer srv.Close()
		models, step, _ := list(remotes.KindLocalAI, srv.URL+"/ai/v1", "")
		Expect(step).To(BeEmpty())
		Expect(ids(models)).To(Equal([]string{"a"}))
	})
})

var _ = Describe("OpenAI lister", func() {
	It("keeps chat-looking ids and sends the bearer key", func() {
		var auth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth = r.Header.Get("Authorization")
			if r.URL.Path != "/v1/models" {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, map[string]any{"data": []map[string]any{
				{"id": "gpt-4o"}, {"id": "text-embedding-3-small"}, {"id": "whisper-1"}, {"id": "dall-e-3"},
				{"id": "gpt-4o-mini-tts"}, {"id": "omni-moderation-latest"}, {"id": "gpt-4o-realtime-preview"}, {"id": "gpt-image-1"}, {"id": "o3"},
			}})
		}))
		defer srv.Close()
		models, step, _ := list(remotes.KindOpenAI, srv.URL+"/v1", "sk-test-key")
		Expect(step).To(BeEmpty())
		Expect(ids(models)).To(Equal([]string{"gpt-4o", "o3"}))
		Expect(models[0].Usecases).To(BeEmpty())
		Expect(auth).To(Equal("Bearer sk-test-key"))
	})
	It("accepts a pasted chat endpoint as the address", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, map[string]any{"data": []map[string]any{{"id": "m"}}})
		}))
		defer srv.Close()
		models, step, _ := list(remotes.KindOpenAI, srv.URL+"/v1/chat/completions", "")
		Expect(step).To(BeEmpty())
		Expect(ids(models)).To(Equal([]string{"m"}))
	})
	It("reports no_models when the filter leaves nothing", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, map[string]any{"data": []map[string]any{{"id": "whisper-1"}}})
		}))
		defer srv.Close()
		_, step, _ := list(remotes.KindOpenAI, srv.URL, "")
		Expect(step).To(Equal(remotes.StepNoModels))
	})
})

var _ = Describe("chat model filter", func() {
	DescribeTable("ids",
		func(id string, want bool) { Expect(remotes.LooksLikeChatModel(id)).To(Equal(want)) },
		Entry("chat", "gpt-4.1", true),
		Entry("org chat", "meta-llama/Llama-3.1-8B-Instruct", true),
		Entry("embedding", "text-embedding-ada-002", false),
		Entry("embed", "nomic-embed-text", false),
		Entry("whisper", "whisper-large", false),
		Entry("transcribe", "gpt-4o-transcribe", false),
		Entry("tts", "tts-1-hd", false),
		Entry("dall-e", "dall-e-2", false),
		Entry("image", "gpt-image-1", false),
		Entry("moderation", "text-moderation-stable", false),
		Entry("audio", "gpt-4o-audio-preview", false),
		Entry("realtime", "gpt-realtime", false),
		Entry("case", "Text-Embedding-3", false),
	)
})

var _ = Describe("Anthropic lister", func() {
	It("sends the headers and walks the pages", func() {
		var pages int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Header.Get("x-api-key")).To(Equal("sk-ant-test"))
			Expect(r.Header.Get("anthropic-version")).To(Equal("2023-06-01"))
			Expect(r.Header.Get("Authorization")).To(BeEmpty())
			Expect(r.URL.Path).To(Equal("/v1/models"))
			Expect(r.URL.Query().Get("limit")).To(Equal("100"))
			atomic.AddInt32(&pages, 1)
			if r.URL.Query().Get("after_id") == "" {
				writeJSON(w, map[string]any{"data": []map[string]any{{"id": "claude-b"}, {"id": "claude-a"}}, "has_more": true, "last_id": "claude-a"})
				return
			}
			Expect(r.URL.Query().Get("after_id")).To(Equal("claude-a"))
			writeJSON(w, map[string]any{"data": []map[string]any{{"id": "claude-c"}}, "has_more": false, "last_id": "claude-c"})
		}))
		defer srv.Close()
		models, step, _ := list(remotes.KindAnthropic, srv.URL, "sk-ant-test")
		Expect(step).To(BeEmpty())
		Expect(ids(models)).To(Equal([]string{"claude-a", "claude-b", "claude-c"}))
		Expect(atomic.LoadInt32(&pages)).To(Equal(int32(2)))
	})
	It("stops after a fixed number of pages", func() {
		var pages int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := atomic.AddInt32(&pages, 1)
			writeJSON(w, map[string]any{"data": []map[string]any{{"id": fmt.Sprintf("m%03d", n)}}, "has_more": true, "last_id": fmt.Sprintf("m%03d", n)})
		}))
		defer srv.Close()
		models, step, _ := list(remotes.KindAnthropic, srv.URL, "k")
		Expect(step).To(BeEmpty())
		Expect(atomic.LoadInt32(&pages)).To(BeNumerically("<=", 20))
		Expect(len(models)).To(Equal(int(atomic.LoadInt32(&pages))))
	})
	It("stops when the cursor does not advance", func() {
		var pages int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&pages, 1)
			writeJSON(w, map[string]any{"data": []map[string]any{{"id": "same"}}, "has_more": true, "last_id": "same"})
		}))
		defer srv.Close()
		_, _, _ = list(remotes.KindAnthropic, srv.URL, "k")
		Expect(atomic.LoadInt32(&pages)).To(BeNumerically("<=", 2))
	})
	It("uses the public address when none is given", func() {
		url, err := remotes.EntryUpstreamURL(remotes.KindAnthropic, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(url).To(Equal("https://api.anthropic.com/v1/messages"))
	})
})

var _ = Describe("lister failures", func() {
	It("maps 401 and 403 to auth, for every kind", func() {
		for _, status := range []int{401, 403} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			for _, k := range []remotes.Kind{remotes.KindLocalAI, remotes.KindOpenAI, remotes.KindAnthropic} {
				_, step, _ := list(k, srv.URL, "sk-test-key")
				Expect(step).To(Equal(remotes.StepAuth), string(k))
			}
			srv.Close()
		}
	})
	It("maps another status to unreachable", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
		defer srv.Close()
		_, step, msg := list(remotes.KindLocalAI, srv.URL, "")
		Expect(step).To(Equal(remotes.StepUnreachable))
		Expect(msg).To(ContainSubstring("502"))
	})
	It("maps a closed port to unreachable", func() {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		_, step, _ := list(remotes.KindLocalAI, url, "")
		Expect(step).To(Equal(remotes.StepUnreachable))
	})
	It("maps a slow server to unreachable", func() {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		defer srv.Close()
		defer close(release)
		start := time.Now()
		_, step, msg, _ := remotes.ListUpstream(context.Background(), remotes.KindLocalAI, srv.URL, "", 150*time.Millisecond, testMaxBody)
		Expect(step).To(Equal(remotes.StepUnreachable))
		Expect(msg).To(ContainSubstring("in time"))
		Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second))
	})
	It("refuses a redirect and never sends the key to the target", func() {
		var leaked atomic.Bool
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			leaked.Store(true)
			writeJSON(w, map[string]any{"data": []map[string]any{{"id": "x"}}})
		}))
		defer target.Close()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/v1/models", http.StatusFound)
		}))
		defer srv.Close()
		_, step, msg := list(remotes.KindLocalAI, srv.URL, "sk-test-key")
		Expect(step).To(Equal(remotes.StepUnreachable))
		Expect(msg).To(ContainSubstring("redirect"))
		Expect(leaked.Load()).To(BeFalse())
	})
	It("refuses an answer above the size cap", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":[{"id":"` + strings.Repeat("a", 4096) + `"}]}`))
		}))
		defer srv.Close()
		_, step, msg, _ := remotes.ListUpstream(context.Background(), remotes.KindLocalAI, srv.URL, "", testTimeout, 1024)
		Expect(step).To(Equal(remotes.StepUnreachable))
		Expect(msg).To(ContainSubstring("too large"))
	})
	It("reports an answer that is not JSON", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) }))
		defer srv.Close()
		_, step, _ := list(remotes.KindOpenAI, srv.URL, "")
		Expect(step).To(Equal(remotes.StepUnreachable))
	})
	It("reaches a private address", func() {
		// httptest listens on loopback, which is a private address.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, map[string]any{"data": []map[string]any{{"id": "a"}}})
		}))
		defer srv.Close()
		models, step, _ := list(remotes.KindOpenAI, srv.URL, "")
		Expect(step).To(BeEmpty())
		Expect(models).To(HaveLen(1))
	})
	It("refuses the cloud metadata address", func() {
		_, step, _ := list(remotes.KindLocalAI, "http://169.254.169.254", "")
		Expect(step).To(Equal(remotes.StepInvalidURL))
	})
	It("never puts the key in a message", func() {
		for _, h := range []http.HandlerFunc{
			func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) },
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(500)
				_, _ = w.Write([]byte(r.Header.Get("Authorization")))
			},
			func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(r.Header.Get("x-api-key"))) },
		} {
			srv := httptest.NewServer(h)
			for _, k := range []remotes.Kind{remotes.KindLocalAI, remotes.KindOpenAI, remotes.KindAnthropic} {
				_, _, msg := list(k, srv.URL, "sk-SECRET-key-value")
				Expect(msg).NotTo(ContainSubstring("SECRET"))
			}
			srv.Close()
		}
	})
})

var _ = Describe("address rules", func() {
	DescribeTable("invalid",
		func(kind remotes.Kind, raw string) {
			_, step, msg := list(kind, raw, "")
			Expect(step).To(Equal(remotes.StepInvalidURL))
			Expect(msg).NotTo(BeEmpty())
		},
		Entry("empty", remotes.KindLocalAI, ""),
		Entry("no scheme", remotes.KindLocalAI, "remote.example.com"),
		Entry("ftp", remotes.KindOpenAI, "ftp://remote.example.com"),
		Entry("credentials", remotes.KindOpenAI, "https://user:pass@remote.example.com/v1"),
		Entry("query", remotes.KindOpenAI, "https://remote.example.com/v1?key=abc"),
	)
	DescribeTable("entry upstream url",
		func(kind remotes.Kind, raw, want string) {
			got, err := remotes.EntryUpstreamURL(kind, raw)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("localai root", remotes.KindLocalAI, "https://remote.example.com/v1/", "https://remote.example.com"),
		Entry("localai prefix", remotes.KindLocalAI, "http://10.0.0.2:8080/ai", "http://10.0.0.2:8080/ai"),
		Entry("openai", remotes.KindOpenAI, "https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"),
		Entry("openai slash", remotes.KindOpenAI, "https://gw.example.com/v1/", "https://gw.example.com/v1/chat/completions"),
		Entry("anthropic gateway", remotes.KindAnthropic, "https://gw.example.com/anthropic/v1", "https://gw.example.com/anthropic/v1/messages"),
	)
})

var _ = Describe("usecase mapping", func() {
	It("keeps valid known_usecases and drops modifiers and reserved ones", func() {
		Expect(remotes.ValidUsecases([]string{"chat", "tools", "thinking", "vision", "decisions", "score", "transcript", "chat", "nonsense", "tts"})).
			To(Equal([]string{"chat", "vision", "transcript", "tts"}))
	})
	It("falls back to chat when nothing is valid", func() {
		Expect(remotes.ValidUsecases([]string{"tools"})).To(Equal([]string{"chat"}))
		Expect(remotes.ValidUsecases(nil)).To(Equal([]string{"chat"}))
	})
})
