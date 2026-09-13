// SPDX-License-Identifier: MIT
package benchmark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBenchmark(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Benchmark") }

var _ = Describe("Benchmark command", func() {
	var cmd Command
	var output bytes.Buffer
	BeforeEach(func() {
		cmd = Command{Models: []string{"a"}, Endpoint: "http://127.0.0.1:8080", Prompt: "hello", MaxTokens: 128, Runs: 2, Warmup: 1, Timeout: time.Second, JSON: true}
		output.Reset()
	})
	It("parses required models and defaults", func() {
		var c Command
		parser, err := kong.New(&c)
		Expect(err).NotTo(HaveOccurred())
		_, err = parser.Parse(nil)
		Expect(err).To(HaveOccurred())
		_, err = parser.Parse([]string{"a", "b"})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Models).To(Equal([]string{"a", "b"}))
		Expect(c.Endpoint).To(Equal("http://127.0.0.1:8080"))
		Expect(c.Runs).To(Equal(3))
		Expect(c.Warmup).To(Equal(1))
		Expect(c.MaxTokens).To(Equal(128))
		Expect(c.Timeout).To(Equal(5 * time.Minute))
		Expect(c.Prompt).NotTo(BeEmpty())
	})
	It("reads API key environment variables in priority order", func() {
		for _, key := range []string{"LOCALAI_API_KEY", "API_KEY"} {
			value, present := os.LookupEnv(key)
			DeferCleanup(func() {
				if present {
					Expect(os.Setenv(key, value)).To(Succeed())
				} else {
					Expect(os.Unsetenv(key)).To(Succeed())
				}
			})
		}
		Expect(os.Unsetenv("LOCALAI_API_KEY")).To(Succeed())
		Expect(os.Setenv("API_KEY", "fallback")).To(Succeed())
		var c Command
		parser, err := kong.New(&c)
		Expect(err).NotTo(HaveOccurred())
		_, err = parser.Parse([]string{"a"})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.APIKey).To(Equal("fallback"))
		Expect(os.Setenv("LOCALAI_API_KEY", "preferred")).To(Succeed())
		_, err = parser.Parse([]string{"a"})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.APIKey).To(Equal("preferred"))
	})
	DescribeTable("normalizes endpoints", func(input, expected string) {
		actual, err := completionURL(input)
		Expect(err).NotTo(HaveOccurred())
		Expect(actual).To(Equal(expected))
	},
		Entry("root", "http://localhost:8080", "http://localhost:8080/v1/chat/completions"), Entry("slash", "http://localhost:8080/", "http://localhost:8080/v1/chat/completions"), Entry("v1", "https://example.org/v1/", "https://example.org/v1/chat/completions"), Entry("proxy", "https://example.org/proxy/", "https://example.org/proxy/v1/chat/completions"), Entry("proxy v1", "https://example.org/proxy/v1", "https://example.org/proxy/v1/chat/completions"))
	It("posts authenticated requests sequentially and excludes each model's warmup", func() {
		var models []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.Method).To(Equal("POST"))
			Expect(r.URL.Path).To(Equal("/proxy/v1/chat/completions"))
			Expect(r.Header.Get("Authorization")).To(Equal("Bearer secret"))
			Expect(r.Header.Get("Content-Type")).To(Equal("application/json"))
			var body map[string]any
			Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
			Expect(body["temperature"]).To(Equal(float64(0)))
			Expect(body["stream"]).To(BeFalse())
			Expect(body["max_tokens"]).To(Equal(float64(128)))
			Expect(body["messages"]).To(Equal([]any{map[string]any{"role": "user", "content": "hello"}}))
			models = append(models, body["model"].(string))
			_, _ = fmt.Fprintf(w, `{"choices":[{}],"usage":{"prompt_tokens":5,"completion_tokens":%d}}`, len(models))
		}))
		defer server.Close()
		cmd.Endpoint = server.URL + "/proxy"
		cmd.APIKey = "secret"
		cmd.Models = []string{"a", "b"}
		Expect(cmd.run(context.Background(), &output)).To(Succeed())
		Expect(models).To(Equal([]string{"a", "a", "a", "b", "b", "b"}))
		Expect(output.String()).NotTo(ContainSubstring("secret"))
		var result report
		Expect(json.Unmarshal(output.Bytes(), &result)).To(Succeed())
		Expect(result.Results).To(HaveLen(2))
		Expect(result.Settings.Runs).To(Equal(2))
		Expect(result.Settings.Warmup).To(Equal(1))
		Expect(result.Settings.Prompt).To(Equal("hello"))
		Expect(result.Settings.Temperature).To(BeZero())
		Expect(result.Settings.Stream).To(BeFalse())
		first := result.Results[0]
		Expect(first.Samples).To(HaveLen(2))
		Expect(*first.Samples[0].CompletionTokens).To(Equal(2))
		Expect(*first.Samples[1].CompletionTokens).To(Equal(3))
		Expect(*first.Samples[0].PromptTokens).To(Equal(5))
		Expect(first.MinSeconds).To(BeNumerically(">", 0))
		Expect(first.MeanSeconds).To(BeNumerically(">=", first.MinSeconds))
		Expect(first.MaxSeconds).To(BeNumerically(">=", first.MeanSeconds))
		Expect(*first.CompletionTokensPerSecond).To(BeNumerically("~", 5/(first.Samples[0].LatencySeconds+first.Samples[1].LatencySeconds), 0.001))
	})
	DescribeTable("preserves missing and zero usage", func(usage string, available bool) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, `{"choices":[{}]`+usage+`}`) }))
		defer server.Close()
		cmd.Endpoint = server.URL
		cmd.Warmup = 0
		Expect(cmd.run(context.Background(), &output)).To(Succeed())
		var result report
		Expect(json.Unmarshal(output.Bytes(), &result)).To(Succeed())
		if available {
			Expect(*result.Results[0].CompletionTokensPerSecond).To(BeZero())
		} else {
			Expect(result.Results[0].CompletionTokensPerSecond).To(BeNil())
		}
		cmd.JSON = false
		output.Reset()
		Expect(cmd.run(context.Background(), &output)).To(Succeed())
		if !available {
			Expect(output.String()).To(ContainSubstring("N/A"))
		}
	}, Entry("absent", "", false), Entry("empty", `,"usage":{}`, false), Entry("partial", `,"usage":{"prompt_tokens":0}`, false), Entry("zero", `,"usage":{"prompt_tokens":0,"completion_tokens":0}`, true))
	It("retains API error details while redacting the key", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, `{"error":{"message":"model unavailable: secret"}}`)
		}))
		defer server.Close()
		cmd.Endpoint = server.URL
		cmd.APIKey = "secret"
		cmd.Warmup = 0
		err := cmd.run(context.Background(), &output)
		Expect(err).To(MatchError(ContainSubstring(`model "a" run 1: server returned an API error: model unavailable: [redacted]`)))
		Expect(output.Len()).To(BeZero())
	})
	It("marks throughput unavailable when one measured request omits usage", func() {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			if requests == 1 {
				_, _ = fmt.Fprint(w, `{"choices":[{}],"usage":{"completion_tokens":2}}`)
			} else {
				_, _ = fmt.Fprint(w, `{"choices":[{}]}`)
			}
		}))
		defer server.Close()
		cmd.Endpoint = server.URL
		cmd.Warmup = 0
		Expect(cmd.run(context.Background(), &output)).To(Succeed())
		var result report
		Expect(json.Unmarshal(output.Bytes(), &result)).To(Succeed())
		Expect(result.Results[0].CompletionTokensPerSecond).To(BeNil())
		Expect(result.Results[0].Samples[1].CompletionTokens).To(BeNil())
	})
	DescribeTable("fails without result output", func(status int, body string) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); _, _ = fmt.Fprint(w, body) }))
		defer server.Close()
		cmd.Endpoint = server.URL
		cmd.APIKey = "secret"
		err := cmd.run(context.Background(), &output)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`model "a" warmup 1`))
		Expect(err.Error()).NotTo(ContainSubstring("secret"))
		Expect(output.Len()).To(BeZero())
	}, Entry("HTTP", 500, `secret`), Entry("API", 200, `{"error":{"message":"secret"}}`), Entry("JSON", 200, `invalid`), Entry("empty choices", 200, `{"choices":[]}`), Entry("trailing JSON", 200, `{"choices":[{}]} {}`), Entry("negative tokens", 200, `{"choices":[{}],"usage":{"completion_tokens":-1}}`))
	It("refuses redirects", func() {
		reached := false
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
		defer target.Close()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
		}))
		defer server.Close()
		cmd.Endpoint = server.URL
		Expect(cmd.run(context.Background(), &output)).NotTo(Succeed())
		Expect(reached).To(BeFalse())
		Expect(output.Len()).To(BeZero())
	})
	It("honors cancellation", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := cmd.run(ctx, &output)
		Expect(err).To(MatchError(ContainSubstring("context canceled")))
		Expect(output.Len()).To(BeZero())
	})
	It("times out requests", func() {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
		defer server.Close()
		defer close(release)
		cmd.Endpoint = server.URL
		cmd.Timeout = 20 * time.Millisecond
		Expect(cmd.run(context.Background(), &output)).NotTo(Succeed())
		Expect(output.Len()).To(BeZero())
	})
	It("times out while reading a response body", func() {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, `{"choices":[`)
			w.(http.Flusher).Flush()
			<-release
		}))
		defer server.Close()
		defer close(release)
		cmd.Endpoint = server.URL
		cmd.Timeout = 20 * time.Millisecond
		Expect(cmd.run(context.Background(), &output)).To(MatchError(ContainSubstring("request timed out")))
		Expect(output.Len()).To(BeZero())
	})
	It("cancels an active request", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cancel() }))
		defer server.Close()
		cmd.Endpoint = server.URL
		Expect(cmd.run(ctx, &output)).To(MatchError(ContainSubstring("context canceled")))
		Expect(output.Len()).To(BeZero())
	})
	DescribeTable("rejects invalid inputs before requests", func(change func(*Command)) {
		reached := false
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
		defer server.Close()
		cmd.Endpoint = server.URL
		change(&cmd)
		Expect(cmd.run(context.Background(), &output)).NotTo(Succeed())
		Expect(reached).To(BeFalse())
		Expect(output.Len()).To(BeZero())
	}, Entry("runs", func(c *Command) { c.Runs = 0 }), Entry("warmup", func(c *Command) { c.Warmup = -1 }), Entry("tokens", func(c *Command) { c.MaxTokens = 0 }), Entry("timeout", func(c *Command) { c.Timeout = 0 }), Entry("prompt", func(c *Command) { c.Prompt = " " }), Entry("models", func(c *Command) { c.Models = nil }), Entry("blank model", func(c *Command) { c.Models = []string{"a", " "} }), Entry("scheme", func(c *Command) { c.Endpoint = "file:///tmp" }), Entry("host", func(c *Command) { c.Endpoint = "http:///v1" }), Entry("userinfo", func(c *Command) { c.Endpoint = "http://secret@localhost" }), Entry("query", func(c *Command) { c.Endpoint += "?secret" }), Entry("fragment", func(c *Command) { c.Endpoint += "#secret" }))
})
