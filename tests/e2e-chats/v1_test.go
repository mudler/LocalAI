package e2echats_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The OpenAI-compatible endpoint must not change (spec section 2, first
// anti-goal): streaming, tools, reasoning and extra fields work for a plain
// client, and a plain call leaves no chat behind.
var _ = Describe("POST /v1/chat/completions beside the chats", Ordered, func() {
	var (
		in *instance
		c  *client
	)
	BeforeAll(func() {
		in = newInstance()
		in.Start()
		c = &client{base: in.URL()}
	})
	AfterAll(func() { in.Cleanup() })
	BeforeEach(func() { llm.Reset() })

	chatCount := func() int {
		var out struct {
			Conversations []chatInfo `json:"conversations"`
		}
		Expect(c.get("/api/chats?limit=200", &out)).To(Equal(http.StatusOK))
		return len(out.Conversations)
	}

	It("answers a plain request and sends the model the messages as they came", func() {
		llm.Handle(func(fakeReq) fakeStep { return fakeStep{Text: "v1 answer"} })
		before := chatCount()
		code, raw := c.do(http.MethodPost, "/v1/chat/completions", map[string]any{"model": "fake", "messages": []map[string]string{
			{"role": "system", "content": "V1-SYSTEM"}, {"role": "user", "content": "V1-USER"}}})
		Expect(code).To(Equal(http.StatusOK), string(raw))
		var out struct {
			Object  string `json:"object"`
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Message      struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Usage struct {
				Total int `json:"total_tokens"`
			} `json:"usage"`
		}
		Expect(json.Unmarshal(raw, &out)).To(Succeed())
		Expect(out.Object).To(Equal("chat.completion"))
		Expect(out.Choices).To(HaveLen(1))
		Expect(out.Choices[0].Message.Content).To(Equal("v1 answer"))
		Expect(out.Choices[0].Message.Role).To(Equal("assistant"))
		Expect(out.Choices[0].FinishReason).To(Equal("stop"))
		Expect(out.Usage.Total).To(BeNumerically(">", 0))
		Expect(chatCount()).To(Equal(before), "a plain call stores no chat")

		var got fakeReq
		for _, r := range llm.Requests() {
			if strings.Contains(r.Prompt(), "V1-USER") {
				got = r
			}
		}
		// The cloud-proxy backend joins the messages into one prompt; what matters here is
		// that both of the client's messages arrive and nothing of the chats loop is added.
		Expect(got.Prompt()).To(And(ContainSubstring("V1-SYSTEM"), ContainSubstring("V1-USER")))
		Expect(got.Prompt()).ToNot(ContainSubstring("You are nib"))
		Expect(got.Tools).To(BeEmpty(), "no chat tool leaks into a plain request")
	})

	It("streams chunks that end with [DONE]", func() {
		llm.Handle(func(fakeReq) fakeStep { return fakeStep{Text: "streamed v1 answer"} })
		req := c.req(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fake","stream":true,"messages":[{"role":"user","content":"V1-STREAM"}]}`), "application/json")
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/event-stream"))
		sc := bufio.NewScanner(resp.Body)
		var text strings.Builder
		var done bool
		var chunks int
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")
			if payload == "[DONE]" {
				done = true
				break
			}
			var ch struct {
				Object  string `json:"object"`
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			Expect(json.Unmarshal([]byte(payload), &ch)).To(Succeed(), payload)
			Expect(ch.Object).To(Equal("chat.completion.chunk"))
			chunks++
			for _, x := range ch.Choices {
				text.WriteString(x.Delta.Content)
			}
		}
		Expect(done).To(BeTrue())
		Expect(chunks).To(BeNumerically(">", 1))
		Expect(text.String()).To(Equal("streamed v1 answer"))
	})

	It("returns the model's tool calls to the client instead of running them", func() {
		llm.Handle(func(fakeReq) fakeStep {
			return fakeStep{Calls: []fakeCall{{Name: "get_weather", Args: `{"city":"Rome"}`}}}
		})
		code, raw := c.do(http.MethodPost, "/v1/chat/completions", map[string]any{"model": "fake",
			"messages": []map[string]string{{"role": "user", "content": "V1-TOOLS weather?"}},
			"tools": []map[string]any{{"type": "function", "function": map[string]any{"name": "get_weather", "description": "weather",
				"parameters": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]string{"type": "string"}}}}}}})
		Expect(code).To(Equal(http.StatusOK), string(raw))
		var out struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Message      struct {
					ToolCalls []struct {
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		Expect(json.Unmarshal(raw, &out)).To(Succeed())
		Expect(out.Choices[0].FinishReason).To(Equal("tool_calls"))
		Expect(out.Choices[0].Message.ToolCalls).To(HaveLen(1))
		Expect(out.Choices[0].Message.ToolCalls[0].Function.Name).To(Equal("get_weather"))
		Expect(out.Choices[0].Message.ToolCalls[0].Function.Arguments).To(ContainSubstring("Rome"))
		var got fakeReq
		for _, r := range llm.Requests() {
			if strings.Contains(r.Prompt(), "V1-TOOLS") {
				got = r
			}
		}
		Expect(got.Tools).To(Equal([]string{"get_weather"}), "the client's tools only")
	})

	It("keeps splitting reasoning from the answer and accepts metadata", func() {
		llm.Handle(func(fakeReq) fakeStep { return fakeStep{Reasoning: "thinking it over", Text: "the answer"} })
		code, raw := c.do(http.MethodPost, "/v1/chat/completions", map[string]any{"model": "fake",
			"messages": []map[string]string{{"role": "user", "content": "V1-THINK"}}, "metadata": map[string]string{"trace": "e2e"}})
		Expect(code).To(Equal(http.StatusOK), string(raw))
		var out struct {
			Choices []struct {
				Message struct {
					Content   string `json:"content"`
					Reasoning string `json:"reasoning"`
				} `json:"message"`
			} `json:"choices"`
		}
		Expect(json.Unmarshal(raw, &out)).To(Succeed())
		Expect(out.Choices[0].Message.Content).To(Equal("the answer"))
		Expect(out.Choices[0].Message.Reasoning).To(Equal("thinking it over"))
	})

	It("reports a failing model as an HTTP error to the plain client", func() {
		llm.Handle(func(fakeReq) fakeStep { return fakeStep{Status: http.StatusBadGateway} })
		code, raw := c.do(http.MethodPost, "/v1/chat/completions", map[string]any{"model": "fake", "messages": []map[string]string{{"role": "user", "content": "V1-ERR"}}})
		Expect(code).To(BeNumerically(">=", 500), string(raw))
		code, raw = c.do(http.MethodPost, "/v1/chat/completions", map[string]any{"model": "no-such-model", "messages": []map[string]string{{"role": "user", "content": "x"}}})
		Expect(code).To(BeNumerically(">=", 400), string(raw))
	})
})
