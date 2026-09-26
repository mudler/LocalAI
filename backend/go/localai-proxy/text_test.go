package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

func codeOf(err error) codes.Code {
	st, ok := status.FromError(err)
	if !ok {
		return codes.Unknown
	}
	return st.Code()
}

var _ = Describe("localai-proxy", func() {
	var up *fakeUpstream

	BeforeEach(func() {
		up = newFakeUpstream()
		DeferCleanup(up.Close)
	})

	Describe("Load", func() {
		It("refuses a model without proxy options", func() {
			err := NewLocalAIProxy().Load(&pb.ModelOptions{Model: "m"})
			Expect(err).To(MatchError(ContainSubstring("proxy")))
		})

		It("refuses a missing or invalid upstream_url", func() {
			p := NewLocalAIProxy()
			Expect(p.Load(&pb.ModelOptions{Proxy: &pb.ProxyOptions{}})).NotTo(Succeed())
			Expect(p.Load(&pb.ModelOptions{Proxy: &pb.ProxyOptions{UpstreamUrl: "not a url"}})).NotTo(Succeed())
			Expect(p.Load(&pb.ModelOptions{Proxy: &pb.ProxyOptions{UpstreamUrl: "ftp://host"}})).NotTo(Succeed())
		})

		It("refuses an api_key_env that is unset", func() {
			err := NewLocalAIProxy().Load(&pb.ModelOptions{Proxy: &pb.ProxyOptions{
				UpstreamUrl: up.URL, ApiKeyEnv: "LOCALAI_PROXY_TEST_UNSET_KEY",
			}})
			Expect(err).To(MatchError(ContainSubstring("LOCALAI_PROXY_TEST_UNSET_KEY")))
		})

		It("parses realtime_pipeline, strips the trailing slash and keeps the timeout", func() {
			p := loadProxy(up, func(o *pb.ModelOptions) {
				o.Options = []string{"other:1", "realtime_pipeline:my-pipe"}
				o.Proxy.RequestTimeoutSeconds = 7
			})
			cfg := p.cfg.Load()
			Expect(cfg.realtimePipeline).To(Equal("my-pipe"))
			Expect(cfg.base).To(Equal(up.URL))
			Expect(cfg.timeout).To(Equal(7 * time.Second))
		})

		It("falls back to the model name when upstream_model is unset", func() {
			p := loadProxy(up, func(o *pb.ModelOptions) { o.Proxy.UpstreamModel = "" })
			Expect(p.model("")).To(Equal("local-name"))
		})
	})

	Describe("PredictRich", func() {
		It("sends messages to /v1/chat/completions with the upstream model and key", func() {
			GinkgoT().Setenv("LOCALAI_PROXY_TEST_KEY", "sk-test")
			p := loadProxy(up, func(o *pb.ModelOptions) { o.Proxy.ApiKeyEnv = "LOCALAI_PROXY_TEST_KEY" })
			up.replyJSON("/v1/chat/completions", map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "hello back"}}},
				"usage":   map[string]any{"prompt_tokens": 3, "completion_tokens": 2},
			})

			reply, err := p.PredictRich(&pb.PredictOptions{
				Messages:    []*pb.Message{{Role: "user", Content: "hello"}},
				Tokens:      32,
				Temperature: 0.5,
				TopK:        40,
				StopPrompts: []string{"</s>"},
				Seed:        9,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(reply.GetMessage())).To(Equal("hello back"))
			Expect(reply.GetPromptTokens()).To(Equal(int32(3)))
			Expect(reply.GetTokens()).To(Equal(int32(2)))

			req := up.last()
			Expect(req.Method).To(Equal(http.MethodPost))
			Expect(req.Path).To(Equal("/v1/chat/completions"))
			Expect(req.Auth).To(Equal("Bearer sk-test"))
			Expect(req.JSON).To(HaveKeyWithValue("model", "remote-model"))
			Expect(req.JSON).To(HaveKeyWithValue("max_tokens", BeNumerically("==", 32)))
			Expect(req.JSON).To(HaveKeyWithValue("temperature", BeNumerically("==", 0.5)))
			Expect(req.JSON).To(HaveKeyWithValue("top_k", BeNumerically("==", 40)))
			Expect(req.JSON).To(HaveKeyWithValue("seed", BeNumerically("==", 9)))
			Expect(req.JSON).To(HaveKeyWithValue("stop", ConsistOf("</s>")))
			Expect(req.JSON).NotTo(HaveKey("stream"))
			Expect(req.JSON["messages"]).To(ConsistOf(HaveKeyWithValue("content", "hello")))
		})

		It("returns upstream tool calls as chat deltas", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/chat/completions", map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id": "call_1", "type": "function",
						"function": map[string]any{"name": "get_weather", "arguments": `{"city":"Rome"}`},
					}},
				}}},
			})

			reply, err := p.PredictRich(&pb.PredictOptions{
				Messages: []*pb.Message{{Role: "user", Content: "weather?"}},
				Tools:    `[{"type":"function","function":{"name":"get_weather"}}]`,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(reply.GetChatDeltas()).To(HaveLen(1))
			tc := reply.GetChatDeltas()[0].GetToolCalls()
			Expect(tc).To(HaveLen(1))
			Expect(tc[0].GetName()).To(Equal("get_weather"))
			Expect(tc[0].GetArguments()).To(Equal(`{"city":"Rome"}`))
			Expect(up.last().JSON).To(HaveKey("tools"))
		})

		It("sends a bare prompt to /v1/completions", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/completions", map[string]any{
				"choices": []any{map[string]any{"text": "completed"}},
			})

			reply, err := p.PredictRich(&pb.PredictOptions{Prompt: "once upon"})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(reply.GetMessage())).To(Equal("completed"))
			req := up.last()
			Expect(req.Path).To(Equal("/v1/completions"))
			Expect(req.JSON).To(HaveKeyWithValue("prompt", "once upon"))
			Expect(req.JSON).To(HaveKeyWithValue("model", "remote-model"))
		})

		It("maps a 5xx upstream to Unavailable with the body in the message", func() {
			p := loadProxy(up, nil)
			up.script("/v1/chat/completions", scriptedResponse{Status: http.StatusServiceUnavailable, Body: "backend is down"})

			_, err := p.PredictRich(&pb.PredictOptions{Messages: []*pb.Message{{Role: "user", Content: "x"}}})
			Expect(codeOf(err)).To(Equal(codes.Unavailable))
			Expect(err.Error()).To(ContainSubstring("backend is down"))
		})

		It("maps a 4xx upstream to InvalidArgument", func() {
			p := loadProxy(up, nil)
			up.script("/v1/chat/completions", scriptedResponse{Status: http.StatusBadRequest, Body: "bad request"})

			_, err := p.PredictRich(&pb.PredictOptions{Messages: []*pb.Message{{Role: "user", Content: "x"}}})
			Expect(codeOf(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("bad request"))
		})

		It("truncates a long upstream error body", func() {
			p := loadProxy(up, nil)
			long := make([]byte, 2000)
			for i := range long {
				long[i] = 'a'
			}
			up.script("/v1/completions", scriptedResponse{Status: http.StatusInternalServerError, Body: string(long)})

			_, err := p.PredictRich(&pb.PredictOptions{Prompt: "x"})
			Expect(codeOf(err)).To(Equal(codes.Unavailable))
			Expect(len(status.Convert(err).Message())).To(BeNumerically("<", 700))
		})

		It("maps an unreachable upstream to Unavailable", func() {
			p := loadProxy(up, nil)
			up.Close()

			_, err := p.PredictRich(&pb.PredictOptions{Prompt: "x"})
			Expect(codeOf(err)).To(Equal(codes.Unavailable))
		})

		It("reports an unloaded proxy as FailedPrecondition", func() {
			_, err := NewLocalAIProxy().PredictRich(&pb.PredictOptions{Prompt: "x"})
			Expect(codeOf(err)).To(Equal(codes.FailedPrecondition))
		})
	})

	Describe("PredictStreamRich", func() {
		It("streams SSE deltas in order and leaves the channel open", func() {
			p := loadProxy(up, nil)
			up.script("/v1/chat/completions", scriptedResponse{SSE: []string{
				sseJSON(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"role": "assistant"}}}}),
				sseJSON(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Hel"}}}}),
				sseJSON(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "lo"}}}}),
				"[DONE]",
			}})

			results := make(chan *pb.Reply, 10)
			err := p.PredictStreamRich(&pb.PredictOptions{Messages: []*pb.Message{{Role: "user", Content: "hi"}}}, results)
			Expect(err).NotTo(HaveOccurred())

			var got []string
			for len(results) > 0 {
				got = append(got, string((<-results).GetMessage()))
			}
			Expect(got).To(Equal([]string{"Hel", "lo"}))
			// The gRPC server closes the channel; closing it here must not panic.
			close(results)
			Expect(up.last().JSON).To(HaveKeyWithValue("stream", true))
		})

		It("streams /v1/completions text for a bare prompt", func() {
			p := loadProxy(up, nil)
			up.script("/v1/completions", scriptedResponse{SSE: []string{
				sseJSON(map[string]any{"choices": []any{map[string]any{"text": "a"}}}),
				sseJSON(map[string]any{"choices": []any{map[string]any{"text": "b"}}}),
				"[DONE]",
			}})

			results := make(chan *pb.Reply, 10)
			Expect(p.PredictStreamRich(&pb.PredictOptions{Prompt: "go"}, results)).To(Succeed())
			Expect(results).To(HaveLen(2))
			Expect(string((<-results).GetMessage())).To(Equal("a"))
			Expect(string((<-results).GetMessage())).To(Equal("b"))
		})

		It("maps a failing upstream to a gRPC code", func() {
			p := loadProxy(up, nil)
			up.script("/v1/completions", scriptedResponse{Status: http.StatusBadGateway, Body: "gateway"})

			err := p.PredictStreamRich(&pb.PredictOptions{Prompt: "go"}, make(chan *pb.Reply, 1))
			Expect(codeOf(err)).To(Equal(codes.Unavailable))
		})
	})

	Describe("legacy Predict and PredictStream", func() {
		It("wrap the rich variants", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/completions", map[string]any{"choices": []any{map[string]any{"text": "plain"}}})
			out, err := p.Predict(&pb.PredictOptions{Prompt: "x"})
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal("plain"))

			up.script("/v1/completions", scriptedResponse{SSE: []string{
				sseJSON(map[string]any{"choices": []any{map[string]any{"text": "s1"}}}),
				"[DONE]",
			}})
			results := make(chan string, 10)
			Expect(p.PredictStream(&pb.PredictOptions{Prompt: "x"}, results)).To(Succeed())
			var got []string
			for s := range results { // PredictStream closes the channel
				got = append(got, s)
			}
			Expect(got).To(Equal([]string{"s1"}))
		})
	})

	Describe("Embeddings", func() {
		It("posts the input to /v1/embeddings and returns the first vector", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/embeddings", map[string]any{
				"data": []any{map[string]any{"embedding": []float32{0.1, 0.2, 0.3}}},
			})

			vec, err := p.Embeddings(&pb.PredictOptions{Embeddings: "embed me"})
			Expect(err).NotTo(HaveOccurred())
			Expect(vec).To(Equal([]float32{0.1, 0.2, 0.3}))
			req := up.last()
			Expect(req.Path).To(Equal("/v1/embeddings"))
			Expect(req.JSON).To(HaveKeyWithValue("input", "embed me"))
			Expect(req.JSON).To(HaveKeyWithValue("model", "remote-model"))
		})

		It("fails when the upstream returns no vector", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/embeddings", map[string]any{"data": []any{}})
			_, err := p.Embeddings(&pb.PredictOptions{Embeddings: "x"})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Rerank", func() {
		It("posts to /v1/rerank and maps the results", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/rerank", map[string]any{
				"model": "remote-model",
				"usage": map[string]any{"total_tokens": 12, "prompt_tokens": 10},
				"results": []any{
					map[string]any{"index": 1, "document": map[string]any{"text": "b"}, "relevance_score": 0.9},
					map[string]any{"index": 0, "document": map[string]any{"text": "a"}, "relevance_score": 0.1},
				},
			})

			res, err := p.Rerank(context.Background(), &pb.RerankRequest{Query: "q", Documents: []string{"a", "b"}, TopN: 2})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.GetUsage().GetTotalTokens()).To(Equal(int32(12)))
			Expect(res.GetUsage().GetPromptTokens()).To(Equal(int32(10)))
			Expect(res.GetResults()).To(HaveLen(2))
			Expect(res.GetResults()[0].GetIndex()).To(Equal(int32(1)))
			Expect(res.GetResults()[0].GetText()).To(Equal("b"))
			Expect(res.GetResults()[0].GetRelevanceScore()).To(BeNumerically("~", 0.9, 1e-6))

			req := up.last()
			Expect(req.Path).To(Equal("/v1/rerank"))
			Expect(req.JSON).To(HaveKeyWithValue("query", "q"))
			Expect(req.JSON).To(HaveKeyWithValue("top_n", BeNumerically("==", 2)))
			Expect(req.JSON).To(HaveKeyWithValue("documents", ConsistOf("a", "b")))
			Expect(req.JSON).To(HaveKeyWithValue("model", "remote-model"))
		})
	})

	Describe("TokenizeString and Detokenize", func() {
		It("posts the prompt to /v1/tokenize", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/tokenize", map[string]any{"tokens": []int32{5, 6, 7}})

			res, err := p.TokenizeString(&pb.PredictOptions{Prompt: "abc"})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.GetTokens()).To(Equal([]int32{5, 6, 7}))
			Expect(res.GetLength()).To(Equal(int32(3)))
			req := up.last()
			Expect(req.Path).To(Equal("/v1/tokenize"))
			Expect(req.JSON).To(HaveKeyWithValue("content", "abc"))
			Expect(req.JSON).To(HaveKeyWithValue("model", "remote-model"))
		})

		It("posts tokens to /v1/detokenize", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/detokenize", map[string]any{"content": "abc"})

			res, err := p.Detokenize(&pb.DetokenizeRequest{Tokens: []int32{5, 6}})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.GetContent()).To(Equal("abc"))
			Expect(up.last().JSON).To(HaveKeyWithValue("tokens", ConsistOf(BeNumerically("==", 5), BeNumerically("==", 6))))
		})
	})

	Describe("Score", func() {
		It("posts to /api/score and maps the candidates", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/api/score", map[string]any{
				"model": "remote-model",
				"candidates": []any{map[string]any{
					"log_prob": -1.5, "length_normalized_log_prob": -0.75, "num_tokens": 2,
					"tokens": []any{map[string]any{"token": "yes", "log_prob": -1.5}},
				}},
			})

			res, err := p.Score(context.Background(), &pb.ScoreRequest{
				Prompt: "p", Candidates: []string{"yes"}, IncludeTokenLogprobs: true, LengthNormalize: true,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.GetCandidates()).To(HaveLen(1))
			c := res.GetCandidates()[0]
			Expect(c.GetLogProb()).To(Equal(-1.5))
			Expect(c.GetLengthNormalizedLogProb()).To(Equal(-0.75))
			Expect(c.GetNumTokens()).To(Equal(int32(2)))
			Expect(c.GetTokens()).To(HaveLen(1))
			Expect(c.GetTokens()[0].GetToken()).To(Equal("yes"))

			req := up.last()
			Expect(req.Path).To(Equal("/api/score"))
			Expect(req.JSON).To(HaveKeyWithValue("prompt", "p"))
			Expect(req.JSON).To(HaveKeyWithValue("include_token_logprobs", true))
			Expect(req.JSON).To(HaveKeyWithValue("length_normalize", true))
			Expect(req.JSON).To(HaveKeyWithValue("model", "remote-model"))
		})

		It("refuses decision-pipeline requests it cannot forward", func() {
			p := loadProxy(up, nil)
			_, err := p.Score(context.Background(), &pb.ScoreRequest{Prompt: "{}", QuestionType: "systemone"})
			Expect(codeOf(err)).To(Equal(codes.Unimplemented))
			Expect(up.recorded()).To(BeEmpty())
		})
	})

	Describe("helpers", func() {
		It("postMultipart sends fields and the file with auth", func() {
			GinkgoT().Setenv("LOCALAI_PROXY_TEST_KEY", "sk-mp")
			p := loadProxy(up, func(o *pb.ModelOptions) { o.Proxy.ApiKeyEnv = "LOCALAI_PROXY_TEST_KEY" })
			up.replyJSON("/v1/audio/transcriptions", map[string]any{"text": "ok"})

			path := filepath.Join(GinkgoT().TempDir(), "a.wav")
			Expect(os.WriteFile(path, []byte("RIFFDATA"), 0o600)).To(Succeed())

			var out struct {
				Text string `json:"text"`
			}
			err := p.postMultipart(context.Background(), "/v1/audio/transcriptions",
				map[string]string{"model": "remote-model", "language": "it"}, "file", path, &out)
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Text).To(Equal("ok"))
			req := up.last()
			Expect(req.Auth).To(Equal("Bearer sk-mp"))
			Expect(req.Fields).To(Equal(map[string]string{"model": "remote-model", "language": "it"}))
			Expect(req.Files).To(HaveKeyWithValue("file", "RIFFDATA"))
		})

		It("postMultipart reports a missing local file without calling upstream", func() {
			p := loadProxy(up, nil)
			err := p.postMultipart(context.Background(), "/v1/audio/transcriptions", nil, "file", "/nonexistent/a.wav", nil)
			Expect(err).To(HaveOccurred())
			Expect(up.recorded()).To(BeEmpty())
		})

		It("applies request_timeout_seconds to non-streaming calls", func() {
			slow := make(chan struct{})
			hang := newFakeUpstreamWithHandler(func(w http.ResponseWriter, r *http.Request) { <-slow })
			// Cleanups run last-in first-out: release the handler before
			// Close, which waits for in-flight requests.
			DeferCleanup(hang.Close)
			DeferCleanup(func() { close(slow) })

			p := NewLocalAIProxy()
			Expect(p.Load(&pb.ModelOptions{Proxy: &pb.ProxyOptions{
				UpstreamUrl: hang.URL, UpstreamModel: "m", RequestTimeoutSeconds: 1,
			}})).To(Succeed())
			_, err := p.Embeddings(&pb.PredictOptions{Embeddings: "x"})
			Expect(codeOf(err)).To(Equal(codes.DeadlineExceeded))
		})

		It("postStream returns the open response for a 2xx", func() {
			p := loadProxy(up, nil)
			up.script("/tts", scriptedResponse{Status: http.StatusOK, ContentType: "audio/wav", Body: "WAVBYTES"})
			resp, err := p.postStream(context.Background(), "/tts", map[string]any{"input": "hi"})
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.Header.Get("Content-Type")).To(Equal("audio/wav"))
		})
	})

	Describe("through the gRPC server", func() {
		It("dispatches Rerank and keeps the Unimplemented code end to end", func() {
			p := loadProxy(up, nil)
			up.replyJSON("/v1/rerank", map[string]any{"results": []any{
				map[string]any{"index": 0, "document": map[string]any{"text": "a"}, "relevance_score": 0.5},
			}})
			addr := "test://localai-proxy-grpc"
			grpc.Provide(addr, p)
			client := grpc.NewClient(addr, true, nil, false)

			res, err := client.Rerank(context.Background(), &pb.RerankRequest{Query: "q", Documents: []string{"a"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.GetResults()).To(HaveLen(1))

			_, err = client.AudioEncode(context.Background(), &pb.AudioEncodeRequest{})
			Expect(codeOf(err)).To(Equal(codes.Unimplemented))
			Expect(status.Convert(err).Message()).To(Equal("localai-proxy: AudioEncode has no upstream counterpart"))
		})
	})

	Describe("methods with no upstream counterpart", func() {
		It("return Unimplemented with the exact message", func() {
			p := loadProxy(up, nil)
			_, err := p.AudioEncode(&pb.AudioEncodeRequest{})
			Expect(codeOf(err)).To(Equal(codes.Unimplemented))
			Expect(status.Convert(err).Message()).To(Equal("localai-proxy: AudioEncode has no upstream counterpart"))

			_, err = p.TokenClassify(context.Background(), &pb.TokenClassifyRequest{})
			Expect(codeOf(err)).To(Equal(codes.Unimplemented))
			_, err = p.ModelMetadata(&pb.ModelOptions{})
			Expect(codeOf(err)).To(Equal(codes.Unimplemented))
			_, err = p.StartFineTune(&pb.FineTuneRequest{})
			Expect(codeOf(err)).To(Equal(codes.Unimplemented))
		})

		It("close the output channel of streaming stubs so the server does not hang", func() {
			p := loadProxy(up, nil)
			updates := make(chan *pb.FineTuneProgressUpdate)
			Expect(codeOf(p.FineTuneProgress(&pb.FineTuneProgressRequest{}, updates))).To(Equal(codes.Unimplemented))
			Eventually(updates).Should(BeClosed())

			out := make(chan *pb.AudioToAudioResponse)
			Expect(codeOf(p.AudioToAudioStream(make(chan *pb.AudioToAudioRequest), out))).To(Equal(codes.Unimplemented))
			Eventually(out).Should(BeClosed())
		})
	})
})
