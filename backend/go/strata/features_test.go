package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// idsOf turns text into the comma-separated byte ids the stub engine replays.
func idsOf(s string) string {
	var p []string
	for _, b := range []byte(s) {
		p = append(p, strconv.Itoa(int(b)))
	}
	return "STRATA_STUB_TOKENS=" + strings.Join(p, ",")
}

func chat(content string) *pb.PredictOptions {
	return &pb.PredictOptions{
		UseTokenizerTemplate: true,
		Messages:             []*pb.Message{{Role: "user", Content: content}},
		Tokens:               500,
	}
}

// --- vision ---

func runVisionStub() {
	// strata-vision: READY, then ENC <image> <out> -> OK 3 3 1 ms, writing one record.
	sc := bufio.NewScanner(os.Stdin)
	_, _ = os.Stdout.WriteString("READY 2\n")
	for sc.Scan() {
		l := sc.Text()
		if l == "QUIT" {
			return
		}
		f := strings.Fields(l)
		if len(f) != 3 || f[0] != "ENC" {
			_, _ = os.Stdout.WriteString("ERR bad line\n")
			continue
		}
		if _, err := os.Stat(f[1]); err != nil {
			_, _ = os.Stdout.WriteString("ERR cannot read image\n")
			continue
		}
		var buf bytes.Buffer
		for _, v := range []int32{0x31455653, 3, 3, 1, 2} {
			_ = binary.Write(&buf, binary.LittleEndian, v)
		}
		for range 6 {
			_ = binary.Write(&buf, binary.LittleEndian, float32(0.5))
		}
		_ = os.WriteFile(f[2], buf.Bytes(), 0o600)
		_, _ = os.Stdout.WriteString("OK 3 3 1 1.5\n")
	}
}

func testPNG() string {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		Fail(err.Error())
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func visionOptions() []string {
	exe, _ := os.Executable()
	GinkgoT().Setenv("STRATA_STUB_VISION", "1")
	return []string{"native:model-00001.gguf", "mmproj:mmproj.gguf", "vision_exe:" + exe}
}

var _ = Describe("features", func() {
	It("reasoning and tool call deltas", func() {
		text := `I should look it up.</think>Checking.<call:get_weather:{"city":"Rome"}>`
		s, _ := loaded("ok", idsOf(text))
		r, err := s.PredictRichContext(context.Background(), chat("weather?"))
		Expect(err).ToNot(HaveOccurred())
		if string(r.Message) != text {
			Fail(fmt.Sprintf("raw text %q", r.Message))
		}
		if len(r.ChatDeltas) != 1 {
			Fail(fmt.Sprintf("want one aggregated delta, got %d", len(r.ChatDeltas)))
		}
		d := r.ChatDeltas[0]
		if d.ReasoningContent != "I should look it up." || d.Content != "Checking." {
			Fail(fmt.Sprintf("reasoning %q content %q", d.ReasoningContent, d.Content))
		}
		if len(d.ToolCalls) != 1 || d.ToolCalls[0].Name != "get_weather" || d.ToolCalls[0].Arguments != `{"city":"Rome"}` || d.ToolCalls[0].Id == "" {
			Fail(fmt.Sprintf("tool calls %+v", d.ToolCalls))
		}
	})

	It("stream deltas arrive incrementally", func() {
		text := `thinking about it now</think>The answer is 42.<call:f:{"a":1}>`
		s, _ := loaded("ok", idsOf(text))
		out := make(chan *pb.Reply, 256)
		if err := s.PredictStreamRichContext(context.Background(), chat("q"), out); err != nil {
			Fail(err.Error())
		}
		close(out)
		var (
			agg      deltaAggregate
			raw      strings.Builder
			withData int
			last     *pb.Reply
		)
		for r := range out {
			raw.Write(r.Message)
			for _, d := range r.ChatDeltas {
				agg.add(d)
				withData++
			}
			last = r
		}
		if raw.String() != text {
			Fail(fmt.Sprintf("raw %q", raw.String()))
		}
		if withData < 5 {
			Fail(fmt.Sprintf("deltas were not incremental: %d chunks", withData))
		}
		d := agg.delta()
		if d.ReasoningContent != "thinking about it now" || d.Content != "The answer is 42." ||
			len(d.ToolCalls) != 1 || d.ToolCalls[0].Name != "f" || d.ToolCalls[0].Arguments != `{"a":1}` {
			Fail(fmt.Sprintf("aggregate %+v", d))
		}
		if last.Tokens != int32(len(text)) || last.PromptTokens == 0 {
			Fail(fmt.Sprintf("final usage reply: %+v", last))
		}
	})

	It("thinking off and metadata", func() {
		s, ft := loaded("ok", idsOf("plain answer here"))
		opts := chat("hi")
		opts.Metadata = map[string]string{"enable_thinking": "false", "reasoning_effort": "low"}
		r, err := s.PredictRichContext(context.Background(), opts)
		Expect(err).ToNot(HaveOccurred())
		if ft.lastReq.ChatTemplateKwargs["enable_thinking"] != false || ft.lastReq.ReasoningEffort != "low" {
			Fail(fmt.Sprintf("request %+v", ft.lastReq))
		}
		if r.ChatDeltas[0].ReasoningContent != "" || r.ChatDeltas[0].Content != "plain answer here" {
			Fail(fmt.Sprintf("delta %+v", r.ChatDeltas[0]))
		}
	})

	It("tool choice required forces the call", func() {
		// The prompt ends with "<call:"; the model goes on with the name and arguments.
		s, ft := loaded("ok", idsOf(`f:{"x":"y"}>`))
		opts := chat("call it")
		opts.Tools = `[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}]`
		opts.ToolChoice = `required`
		r, err := s.PredictRichContext(context.Background(), opts)
		Expect(err).ToNot(HaveOccurred())
		if ft.lastReq.ToolChoice != "required" {
			Fail(fmt.Sprintf("tool_choice not passed: %+v", ft.lastReq.ToolChoice))
		}
		d := r.ChatDeltas[0]
		if len(d.ToolCalls) != 1 || d.ToolCalls[0].Name != "f" || d.Content != "" || d.ReasoningContent != "" {
			Fail(fmt.Sprintf("delta %+v", d))
		}
		if r.PromptTokens < int32(len("user:call it;<call:")) {
			Fail(fmt.Sprintf("the forced opening is part of the prompt: %d", r.PromptTokens))
		}
	})

	It("logprobs", func() {
		log := filepath.Join(GinkgoT().TempDir(), "gen.log")
		s, _ := loaded("ok", idsOf("AB"), "STRATA_STUB_LOG="+log)
		opts := chat("x")
		opts.Logprobs, opts.TopLogprobs = 1, 2
		r, err := s.PredictRichContext(context.Background(), opts)
		Expect(err).ToNot(HaveOccurred())
		b, _ := os.ReadFile(log)
		if !strings.Contains(string(b), " logprobs=2 ") {
			Fail(fmt.Sprintf("request line %q", b))
		}
		var lp struct {
			Content []struct {
				ID          int32
				Token       string
				Bytes       []int
				Logprob     float64
				TopLogprobs []struct {
					ID      int32
					Token   string
					Logprob float64
				} `json:"top_logprobs"`
			}
		}
		if err := json.Unmarshal(r.Logprobs, &lp); err != nil {
			Fail(fmt.Sprintf("%v: %s", err, r.Logprobs))
		}
		if len(lp.Content) != 2 || lp.Content[0].Token != "A" || lp.Content[0].Logprob != -0.5 || lp.Content[1].Token != "B" {
			Fail(fmt.Sprintf("logprobs %s", r.Logprobs))
		}
		top := lp.Content[0].TopLogprobs
		if len(top) != 2 || top[0].Token != "A" || top[1].ID != 66 || top[1].Logprob != -2.25 {
			Fail(fmt.Sprintf("top logprobs %s", r.Logprobs))
		}
		// Without the option the engine is not asked and no logprobs come back.
		r, err = s.PredictRichContext(context.Background(), chat("x"))
		if err != nil || len(r.Logprobs) != 0 {
			Fail(fmt.Sprintf("logprobs without request: %s %v", r.Logprobs, err))
		}
	})

	It("logprobs in stream", func() {
		s, _ := loaded("ok", idsOf("hi"))
		opts := chat("x")
		opts.Logprobs = 1
		out := make(chan *pb.Reply, 64)
		if err := s.PredictStreamRichContext(context.Background(), opts, out); err != nil {
			Fail(err.Error())
		}
		close(out)
		var last *pb.Reply
		for r := range out {
			last = r
		}
		if !bytes.Contains(last.Logprobs, []byte(`"token":"h"`)) {
			Fail(fmt.Sprintf("the final reply must carry the logprobs: %s", last.Logprobs))
		}
	})

	It("timings and finish", func() {
		s, _ := loaded("ok", idsOf("ok"))
		r, err := s.PredictRichContext(context.Background(), chat("x"))
		Expect(err).ToNot(HaveOccurred())
		if r.TimingPromptProcessing != 1 || r.TimingTokenGeneration != 1 {
			Fail(fmt.Sprintf("timings %v %v", r.TimingPromptProcessing, r.TimingTokenGeneration))
		}
	})

	It("status reports memory", func() {
		if runtime.GOOS != "linux" {
			Skip("memory is read from /proc")
		}
		s, _ := loaded("ok")
		st, err := s.Status()
		if err != nil || st.State != pb.StatusResponse_READY || st.Memory == nil || st.Memory.Total == 0 || st.Memory.Breakdown["engine"] == 0 {
			Fail(fmt.Sprintf("status %v %v %v", st.State, st.Memory, err))
		}
	})

	It("tokenize string uses the template", func() {
		s, _ := loaded("ok")
		tr, err := s.TokenizeString(chat("abc"))
		if err != nil || int(tr.Length) != len("user:abc;") {
			Fail(fmt.Sprintf("%d %v", tr.Length, err))
		}
	})

	It("images reach the engine as g e n i", func() {
		log := filepath.Join(GinkgoT().TempDir(), "gen.log")
		s, ft := loadedWith("ok", visionOptions(), idsOf("a cat"), "STRATA_STUB_LOG="+log)
		if s.cfg.vision == nil {
			Fail("vision should be configured")
		}
		opts := chat(defaultMediaMarker + "what is this?")
		opts.Images = []string{testPNG()}
		r, err := s.PredictRichContext(context.Background(), opts)
		Expect(err).ToNot(HaveOccurred())
		if string(r.Message) != "a cat" {
			Fail(fmt.Sprintf("got %q", r.Message))
		}
		parts, _ := ft.lastReq.Messages[0]["content"].([]map[string]any)
		if len(parts) != 2 || parts[0]["type"] != "image_url" {
			Fail(fmt.Sprintf("message parts %+v", parts))
		}
		b, _ := os.ReadFile(log)
		line := strings.TrimSpace(string(b))
		f := strings.Fields(line)
		if f[0] != "GENI" || !strings.HasSuffix(f[2], ".sve") {
			Fail(fmt.Sprintf("request line %q", line))
		}
		ids := strings.Split(f[3], ",")
		pads := 0
		for _, id := range ids {
			if id == strconv.Itoa(fakePad) {
				pads++
			}
		}
		if pads != 3 {
			Fail(fmt.Sprintf("the pad must be widened to the 3 image rows, got %d in %q", pads, f[3]))
		}
		if r.PromptTokens != int32(len(ids)) {
			Fail(fmt.Sprintf("prompt tokens %d, ids %d", r.PromptTokens, len(ids)))
		}
		// The same image again is served from the encoder's cache; text-only requests still use GEN.
		if _, err := s.PredictRichContext(context.Background(), opts); err != nil {
			Fail(err.Error())
		}
		_ = os.Remove(log)
		if _, err := s.PredictRichContext(context.Background(), chat("plain")); err != nil {
			Fail(err.Error())
		}
		b, _ = os.ReadFile(log)
		if !strings.HasPrefix(string(b), "GEN ") {
			Fail(fmt.Sprintf("text request line %q", b))
		}
		st, _ := s.Status()
		if runtime.GOOS == "linux" && st.Memory.Breakdown["vision"] == 0 {
			Fail(fmt.Sprintf("the encoder should show in the memory breakdown: %+v", st.Memory))
		}
	})

	It("images in raw prompt mode", func() {
		log := filepath.Join(GinkgoT().TempDir(), "gen.log")
		s, _ := loadedWith("ok", visionOptions(), idsOf("ok"), "STRATA_STUB_LOG="+log)
		_, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{
			Prompt: "look: " + defaultMediaMarker + " done", Images: []string{testPNG()}, Tokens: 5,
		})
		Expect(err).ToNot(HaveOccurred())
		b, _ := os.ReadFile(log)
		if !strings.HasPrefix(string(b), "GENI 5 ") {
			Fail(fmt.Sprintf("request line %q", b))
		}
	})

	It("image errors", func() {
		s, _ := loadedWith("ok", visionOptions())
		opts := chat("no marker here")
		opts.Images = []string{testPNG()}
		if _, err := s.PredictRichContext(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "image markers") {
			Fail(fmt.Sprintf("marker mismatch: %v", err))
		}
		opts = chat(defaultMediaMarker + "x")
		opts.Images = []string{"!!not base64!!"}
		if _, err := s.PredictRichContext(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "base64") {
			Fail(fmt.Sprintf("bad image: %v", err))
		}
		// The engine refuses GENI when it was started without --vision.
		s2, _ := loadedWith("ok", visionOptions(), "STRATA_STUB_NOVISION=1")
		opts = chat(defaultMediaMarker + "x")
		opts.Images = []string{testPNG()}
		if _, err := s2.PredictRichContext(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "--vision") {
			Fail(fmt.Sprintf("engine refusal: %v", err))
		}
	})

	// --- options ---
	It("file options and vision args", func() {
		dir := GinkgoT().TempDir()
		pack := filepath.Join(dir, "m", "pack")
		for _, f := range []string{filepath.Join(pack, "tokenizer", "vocab.json"), filepath.Join(dir, "m", "a-00001-of-00002.gguf"),
			filepath.Join(dir, "m", "table.gguf"), filepath.Join(dir, "m", "mm.gguf"), filepath.Join(dir, "m", "strata-vision")} {
			_ = os.MkdirAll(filepath.Dir(f), 0o755)
			if err := os.WriteFile(f, []byte("x"), 0o755); err != nil {
				Fail(err.Error())
			}
		}
		exe, _ := os.Executable()
		c, err := parseLoad(&pb.ModelOptions{
			ModelFile: filepath.Join(dir, "m"), ContextSize: 4096, MainGPU: "1", Threads: 6,
			Options: []string{"engine_path:" + exe, "native:a-00001-of-00002.gguf", "ple_gguf:table.gguf", "ple_io:ram",
				"mmproj:mm.gguf", "vision_exe:strata-vision", "env:FOO=bar", "engine_args:--spec 4"},
		})
		Expect(err).ToNot(HaveOccurred())
		m := filepath.Join(dir, "m")
		want := "--pack " + pack + " --native " + m + "/a-00001-of-00002.gguf --ple-gguf " + m + "/table.gguf --max-context 4096 --gpu 1 --vision --ple-io ram --spec 4"
		if got := strings.Join(c.engineArgs(), " "); got != want {
			Fail(fmt.Sprintf("got  %s\nwant %s", got, want))
		}
		if len(c.env) != 1 || c.env[0] != "FOO=bar" || c.vision == nil || c.vision.Threads != 6 || c.vision.TextModel != m+"/a-00001-of-00002.gguf" {
			Fail(fmt.Sprintf("env %v vision %+v", c.env, c.vision))
		}
		_, err = parseLoad(&pb.ModelOptions{ModelFile: m, Options: []string{"engine_path:" + exe, "native:missing.gguf"}})
		if err == nil || !strings.Contains(err.Error(), "native option") {
			Fail(fmt.Sprintf("missing file: %v", err))
		}
		// Images need the text model's GGUF for the encoder.
		_, err = parseLoad(&pb.ModelOptions{ModelFile: m, Options: []string{"engine_path:" + exe, "mmproj:mm.gguf", "vision_exe:strata-vision"}})
		if err == nil || !strings.Contains(err.Error(), "native") {
			Fail(fmt.Sprintf("vision without native: %v", err))
		}
	})

})
