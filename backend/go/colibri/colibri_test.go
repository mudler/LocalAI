//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// stubOptions loads a fake model directory with the test binary as coli.
func stubOptions(mode string, env map[string]string) *pb.ModelOptions {
	dir := GinkgoT().TempDir()
	Expect(os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o600)).To(Succeed())
	self, err := os.Executable()
	Expect(err).ToNot(HaveOccurred())
	e := map[string]string{"COLIBRI_STUB_MODE": mode}
	for k, v := range env {
		e[k] = v
	}
	return &pb.ModelOptions{
		ModelFile: dir,
		Options:   []string{"coli_path:" + self, "python:none", "start_timeout:20", "stop_timeout:5"},
		EnvVars:   e,
	}
}

func chatOpts(msgs ...*pb.Message) *pb.PredictOptions {
	return &pb.PredictOptions{UseTokenizerTemplate: true, Messages: msgs, Temperature: 0.7, TopP: 0.9, Tokens: 64}
}

func user(s string) *pb.Message { return &pb.Message{Role: "user", Content: s} }

const weatherTool = `[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]`

func pidGone(pid int) bool { return syscall.Kill(pid, 0) != nil }

var _ = Describe("parseLoad", func() {
	var self string
	BeforeEach(func() {
		var err error
		self, err = os.Executable()
		Expect(err).ToNot(HaveOccurred())
	})

	It("takes a colibri model directory and maps the options to flags", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o600)).To(Succeed())
		c, err := parseLoad(&pb.ModelOptions{
			ModelFile: dir, ContextSize: 4096, Threads: 6,
			Options: []string{"coli_path:" + self, "python:none", "parallel:2", "ram:24", "gpu:none", "think:false", "policy:balanced"},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(c.modelDir).To(Equal(dir))
		Expect(c.gguf).To(BeEmpty())
		Expect(c.serveArgs()).To(Equal([]string{"serve", "--model", dir, "--model-id", "colibri",
			"--ctx", "4096", "--gpu", "none", "--kv-slots", "2", "--policy", "balanced", "--ram", "24", "--no-think"}))
		Expect(c.env).To(ContainElement("OMP_NUM_THREADS=6"))
	})

	It("lets OMP_NUM_THREADS from the model config win over threads", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o600)).To(Succeed())
		c, err := parseLoad(&pb.ModelOptions{ModelFile: dir, Threads: 6, EnvVars: map[string]string{"OMP_NUM_THREADS": "2"},
			Options: []string{"coli_path:" + self, "python:none"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(c.env).To(Equal([]string{"OMP_NUM_THREADS=2"}))
	})

	It("plans the import of a GGUF into a sibling directory", func() {
		dir := GinkgoT().TempDir()
		gguf := filepath.Join(dir, "Big-Model-Q2_K-00001-of-00003.gguf")
		Expect(os.WriteFile(gguf, []byte("GGUF"), 0o600)).To(Succeed())
		c, err := parseLoad(&pb.ModelOptions{ModelFile: gguf, Options: []string{"coli_path:" + self, "python:none"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(c.gguf).To(Equal(gguf))
		Expect(c.modelDir).To(Equal(filepath.Join(dir, "Big-Model-Q2_K.colibri")))

		// Once imported, the directory is used as it is.
		Expect(os.MkdirAll(c.modelDir, 0o750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(c.modelDir, "config.json"), []byte(`{}`), 0o600)).To(Succeed())
		c, err = parseLoad(&pb.ModelOptions{ModelFile: gguf, Options: []string{"coli_path:" + self, "python:none"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(c.gguf).To(BeEmpty())
	})

	It("refuses what it cannot serve", func() {
		dir := GinkgoT().TempDir()
		_, err := parseLoad(&pb.ModelOptions{ModelFile: dir, Options: []string{"coli_path:" + self, "python:none"}})
		Expect(err).To(MatchError(ContainSubstring("not a colibri model directory")))

		f := filepath.Join(dir, "model.safetensors")
		Expect(os.WriteFile(f, []byte("x"), 0o600)).To(Succeed())
		_, err = parseLoad(&pb.ModelOptions{ModelFile: f, Options: []string{"coli_path:" + self, "python:none"}})
		Expect(err).To(MatchError(ContainSubstring("neither a .gguf file")))

		Expect(os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o600)).To(Succeed())
		_, err = parseLoad(&pb.ModelOptions{ModelFile: dir, Options: []string{"coli_path:" + self, "python:none", "ram:lots"}})
		Expect(err).To(MatchError(ContainSubstring("ram must be a whole number")))
		_, err = parseLoad(&pb.ModelOptions{ModelFile: dir, Options: []string{"coli_path:" + self, "python:none", "parallel:2", "kv_slots:3"}})
		Expect(err).To(MatchError(ContainSubstring("both set --kv-slots")))
	})
})

var _ = Describe("request translation", func() {
	It("sends the structured chat, tools and reasoning settings to the chat endpoint", func() {
		o := chatOpts(
			&pb.Message{Role: "system", Content: "Be brief."},
			user("Weather in Paris?"),
			&pb.Message{Role: "assistant", ToolCalls: `[{"id":"c0","type":"function","function":{"name":"get_weather","arguments":"{}"}}]`, ReasoningContent: "r"},
			&pb.Message{Role: "tool", ToolCallId: "c0", Content: "sunny"},
		)
		o.Tools = weatherTool
		o.ToolChoice = "auto"
		o.StopPrompts = []string{"<|im_end|>"}
		o.Metadata = map[string]string{"enable_thinking": "true", "reasoning_effort": "high"}
		o.TopK, o.PresencePenalty, o.Seed = 20, 1.5, 7 // LocalAI defaults colibri has no knob for
		b, err := buildRequest(o, "m", true)
		Expect(err).ToNot(HaveOccurred())
		var got map[string]any
		Expect(json.Unmarshal(b, &got)).To(Succeed())
		Expect(got).To(HaveKeyWithValue("stream", true))
		Expect(got).To(HaveKeyWithValue("tool_choice", "auto"))
		Expect(got).To(HaveKeyWithValue("enable_thinking", true))
		Expect(got).To(HaveKeyWithValue("reasoning_effort", "high"))
		Expect(got).To(HaveKeyWithValue("max_tokens", BeNumerically("==", 64)))
		Expect(got).To(HaveKey("tools"))
		Expect(got).ToNot(HaveKey("prompt"))
		Expect(got).ToNot(HaveKey("top_k"))
		Expect(got).ToNot(HaveKey("presence_penalty"))
		Expect(got).ToNot(HaveKey("seed"))
		msgs := got["messages"].([]any)
		Expect(msgs).To(HaveLen(4))
		Expect(msgs[2]).To(HaveKey("tool_calls"))
		Expect(msgs[2]).To(HaveKeyWithValue("reasoning_content", "r"))
		Expect(msgs[3]).To(HaveKeyWithValue("tool_call_id", "c0"))
	})

	It("sends a plain prompt to the completions endpoint", func() {
		b, err := buildRequest(&pb.PredictOptions{Prompt: "The capital of France is", Logprobs: 3}, "m", false)
		Expect(err).ToNot(HaveOccurred())
		var got map[string]any
		Expect(json.Unmarshal(b, &got)).To(Succeed())
		Expect(got).To(HaveKeyWithValue("prompt", "The capital of France is"))
		Expect(got).To(HaveKeyWithValue("logprobs", BeNumerically("==", 3)))
		Expect(got).ToNot(HaveKey("messages"))
	})

	It("refuses grammars, media and tools without structured messages", func() {
		Expect(checkRequest(&pb.PredictOptions{Prompt: "x", Grammar: "root ::= \"a\""})).To(MatchError(ContainSubstring("grammar.disable")))
		Expect(checkRequest(&pb.PredictOptions{Prompt: "x", Images: []string{"aGk="}})).To(MatchError(ContainSubstring("image")))
		Expect(checkRequest(&pb.PredictOptions{Prompt: "x", Tools: weatherTool})).To(MatchError(ContainSubstring("use_tokenizer_template")))
	})
})

var _ = Describe("backend against a stub coli serve", func() {
	var (
		b   *Colibri
		log string
	)
	BeforeEach(func() {
		b = NewColibri()
		log = filepath.Join(GinkgoT().TempDir(), "requests.log")
	})
	AfterEach(func() { _ = b.Free() })

	It("answers a chat with content and reasoning, and maps usage", func() {
		Expect(b.Load(stubOptions("ok", map[string]string{"COLIBRI_STUB_LOG": log}))).To(Succeed())
		st, _ := b.Status()
		Expect(st.State).To(Equal(pb.StatusResponse_READY))

		o := chatOpts(user("hi"))
		o.Metadata = map[string]string{"reasoning_effort": "low"}
		r, err := b.PredictRichContext(context.Background(), o)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(r.Message)).To(Equal("Hello there."))
		Expect(r.ChatDeltas).To(HaveLen(1))
		Expect(r.ChatDeltas[0].Content).To(Equal("Hello there."))
		Expect(r.ChatDeltas[0].ReasoningContent).To(Equal("The user greets me."))
		Expect(r.PromptTokens).To(BeEquivalentTo(12))
		Expect(r.Tokens).To(BeEquivalentTo(5))
	})

	It("returns tool calls as ChatDeltas, streamed and not", func() {
		Expect(b.Load(stubOptions("ok", nil))).To(Succeed())
		o := chatOpts(user("Weather in Paris?"))
		o.Tools = weatherTool

		r, err := b.PredictRichContext(context.Background(), o)
		Expect(err).ToNot(HaveOccurred())
		Expect(r.Message).To(BeEmpty())
		Expect(r.ChatDeltas[0].ToolCalls).To(HaveLen(1))
		tc := r.ChatDeltas[0].ToolCalls[0]
		Expect(tc.Id).To(Equal("call_1"))
		Expect(tc.Name).To(Equal("get_weather"))
		Expect(tc.Arguments).To(Equal(`{"city":"Paris"}`))

		out := make(chan *pb.Reply, 32)
		Expect(b.PredictStreamRichContext(context.Background(), o, out)).To(Succeed())
		close(out)
		var name, args string
		var last *pb.Reply
		for rep := range out {
			Expect(rep.Message).To(BeEmpty(), "tool-call markup must not stream as content")
			for _, d := range rep.ChatDeltas {
				for _, c := range d.ToolCalls {
					Expect(c.Index).To(BeEquivalentTo(0))
					name += c.Name
					args += c.Arguments
				}
			}
			last = rep
		}
		Expect(name).To(Equal("get_weather"))
		Expect(args).To(Equal(`{"city":"Paris"}`))
		Expect(last.Tokens).To(BeEquivalentTo(3))
	})

	It("streams content and reasoning deltas and plain completions", func() {
		Expect(b.Load(stubOptions("ok", nil))).To(Succeed())
		o := chatOpts(user("hi"))
		o.Metadata = map[string]string{"reasoning_effort": "low"}
		out := make(chan *pb.Reply, 32)
		Expect(b.PredictStreamRichContext(context.Background(), o, out)).To(Succeed())
		close(out)
		var content, reasoning string
		for rep := range out {
			for _, d := range rep.ChatDeltas {
				content += d.Content
				reasoning += d.ReasoningContent
			}
		}
		Expect(content).To(Equal("Hello there."))
		Expect(reasoning).To(Equal("Thinking."))

		results := make(chan string, 8)
		Expect(b.PredictStream(&pb.PredictOptions{Prompt: "The capital of France is"}, results)).To(Succeed())
		var text string
		for s := range results {
			text += s
		}
		Expect(text).To(Equal(" Paris."))
	})

	It("passes the gateway's named refusal on", func() {
		Expect(b.Load(stubOptions("ok", nil))).To(Succeed())
		o := chatOpts(user("hi"))
		o.LogitBias = `{"42":5}`
		_, err := b.PredictRichContext(context.Background(), o)
		Expect(err).To(MatchError(And(ContainSubstring("400"), ContainSubstring("logit_bias"))))
	})

	It("stops generating when the request is cancelled", func() {
		Expect(b.Load(stubOptions("slow", map[string]string{"COLIBRI_STUB_LOG": log}))).To(Succeed())
		ctx, cancel := context.WithCancel(context.Background())
		out := make(chan *pb.Reply, 64)
		errc := make(chan error, 1)
		go func() { errc <- b.PredictStreamRichContext(ctx, chatOpts(user("go on forever")), out) }()
		Eventually(out, 5*time.Second).Should(Receive())
		cancel()
		Eventually(errc, 5*time.Second).Should(Receive(MatchError(context.Canceled)))
		Eventually(func() string { s, _ := os.ReadFile(log); return string(s) }, 5*time.Second).Should(ContainSubstring("disconnected"))
		Expect(b.Busy()).To(BeFalse())
	})

	It("reports a failed start with the exit status and coli's last output", func() {
		err := b.Load(stubOptions("startfail", nil))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("exit status 3"))
		Expect(err.Error()).To(ContainSubstring("needs 99 GB of RAM"))
		st, _ := b.Status()
		Expect(st.State).To(Equal(pb.StatusResponse_UNINITIALIZED))
	})

	It("restarts coli serve once after a crash", func() {
		mark := filepath.Join(GinkgoT().TempDir(), "crashed")
		Expect(b.Load(stubOptions("crash", map[string]string{"COLIBRI_STUB_MARK": mark}))).To(Succeed())
		firstPid := b.sc.pid

		out := make(chan *pb.Reply, 32)
		err := b.PredictStreamRichContext(context.Background(), chatOpts(user("hi")), out)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Segmentation fault"))
		st, _ := b.Status()
		Expect(st.State).To(Equal(pb.StatusResponse_ERROR), "a dead coli serve is not reported healthy")

		r, err := b.PredictRichContext(context.Background(), chatOpts(user("hi again")))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(r.Message)).To(Equal("Hello there."))
		Expect(b.sc.pid).ToNot(Equal(firstPid))
		st, _ = b.Status()
		Expect(st.State).To(Equal(pb.StatusResponse_READY))
	})

	It("stays unhealthy when the restart fails too", func() {
		mark := filepath.Join(GinkgoT().TempDir(), "crashed")
		Expect(b.Load(stubOptions("crash", map[string]string{"COLIBRI_STUB_MARK": mark, "COLIBRI_STUB_NORESTART": "1"}))).To(Succeed())
		_, err := b.PredictRichContext(context.Background(), chatOpts(user("hi")))
		Expect(err).To(MatchError(ContainSubstring("Segmentation fault")))

		_, err = b.PredictRichContext(context.Background(), chatOpts(user("again")))
		Expect(err).To(MatchError(And(ContainSubstring("could not be started again"), ContainSubstring("engine binary is gone"))))
		st, _ := b.Status()
		Expect(st.State).To(Equal(pb.StatusResponse_ERROR))
		_, err = b.PredictRichContext(context.Background(), chatOpts(user("and again")))
		Expect(err).To(MatchError(ContainSubstring("could not be started again")))
	})

	It("imports a GGUF once and stops the process group on Free", func() {
		dir := GinkgoT().TempDir()
		gguf := filepath.Join(dir, "tiny-Q8_0.gguf")
		Expect(os.WriteFile(gguf, []byte("GGUF"), 0o600)).To(Succeed())
		o := stubOptions("ok", map[string]string{"COLIBRI_STUB_LOG": log})
		o.ModelFile = gguf
		Expect(b.Load(o)).To(Succeed())
		Expect(filepath.Join(dir, "tiny-Q8_0.colibri", "config.json")).To(BeARegularFile())
		Expect(b.Load(o)).To(Succeed()) // reload: no second import
		s, _ := os.ReadFile(log)
		Expect(strings.Count(string(s), "import ")).To(Equal(1))

		pid := b.sc.pid
		Expect(b.Free()).To(Succeed())
		Eventually(func() bool { return pidGone(pid) }, 5*time.Second).Should(BeTrue())
		st, _ := b.Status()
		Expect(st.State).To(Equal(pb.StatusResponse_UNINITIALIZED))
		_, err := b.PredictRichContext(context.Background(), chatOpts(user("hi")))
		Expect(err).To(HaveOccurred())
	})

	It("does not implement embeddings or tokenization", func() {
		_, err := b.Embeddings(&pb.PredictOptions{})
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		_, err = b.TokenizeString(&pb.PredictOptions{})
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})
})
