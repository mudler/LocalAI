package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Fake vocabulary: ids 0..255 are bytes; these three are the vision markers.
const (
	fakeStart = 1000
	fakePad   = 1001
	fakeEnd   = 1002
)

// fakeTokenizer maps each byte to the id of the same value and the vision
// markers to ids 1000..1002. Prompt renders "role:content;" for each message.
type fakeTokenizer struct {
	lastReq    promptRequest
	newParsers int
}

func (f *fakeTokenizer) Encode(text string, parseSpecial bool) ([]int, error) {
	var ids []int
	for len(text) > 0 {
		matched := false
		if parseSpecial {
			for id, lit := range map[int]string{fakeStart: visionStartText, fakePad: imagePadText, fakeEnd: visionEndText} {
				if strings.HasPrefix(text, lit) {
					ids = append(ids, id)
					text = text[len(lit):]
					matched = true
					break
				}
			}
		}
		if !matched {
			ids = append(ids, int(text[0]))
			text = text[1:]
		}
	}
	return ids, nil
}

func (f *fakeTokenizer) TokenBytes(id int) ([]byte, bool) {
	switch {
	case id >= 0 && id <= 255:
		return []byte{byte(id)}, true
	case id == fakeStart:
		return []byte(visionStartText), true
	case id == fakePad:
		return []byte(imagePadText), true
	case id == fakeEnd:
		return []byte(visionEndText), true
	}
	return nil, false
}

func (f *fakeTokenizer) Prompt(req promptRequest) (promptResult, error) {
	f.lastReq = req
	var sb strings.Builder
	var images []string
	for _, m := range req.Messages {
		fmt.Fprintf(&sb, "%v:", m["role"])
		switch c := m["content"].(type) {
		case string:
			sb.WriteString(c)
		case []map[string]any:
			for _, p := range c {
				if p["type"] == "text" {
					sb.WriteString(p["text"].(string))
				} else {
					sb.WriteString(visionStartText + imagePadText + visionEndText)
					images = append(images, p["image_url"].(map[string]any)["url"].(string))
				}
			}
		}
		sb.WriteString(";")
	}
	res := promptResult{Images: images, Thinking: true, Tools: flattenTools(req.Tools)}
	if v, ok := req.ChatTemplateKwargs["enable_thinking"].(bool); ok {
		res.Thinking = v
	}
	if req.ToolChoice == "required" {
		res.Thinking, res.Force = false, "<call:"
	}
	text := sb.String() + res.Force
	res.IDs, _ = f.Encode(text, true)
	return res, nil
}

func (f *fakeTokenizer) NewParser(thinking bool, tools any, opening string) (outputParser, []parseEvent, error) {
	f.newParsers++
	p := &fakeParser{thinking: thinking}
	evs, _ := p.Feed(opening)
	return p, evs, nil
}

func (f *fakeTokenizer) Close() error { return nil }

// fakeParser is a tiny stand-in for Strata's output parser: "</think>" ends
// the reasoning, "<call:NAME:ARGS>" is a tool call.
type fakeParser struct {
	thinking bool
	buf      string
	inCall   bool
	n        int
	id       string
}

func (p *fakeParser) Close() {}

func (p *fakeParser) Feed(text string) ([]parseEvent, error) {
	p.buf += text
	var out []parseEvent
	for {
		switch {
		case p.inCall:
			i := strings.Index(p.buf, ">")
			if i < 0 {
				return out, nil
			}
			body := p.buf[:i]
			p.buf = p.buf[i+1:]
			p.inCall = false
			name, args, _ := strings.Cut(body, ":")
			p.n++
			p.id = fmt.Sprintf("call_%d", p.n)
			call := &parseCall{ID: p.id, Name: name}
			out = append(out, parseEvent{Kind: "tool_start", Call: call})
			for _, ch := range []string{args[:len(args)/2], args[len(args)/2:]} {
				out = append(out, parseEvent{Kind: "tool_args", Text: ch, Call: call})
			}
			out = append(out, parseEvent{Kind: "tool_call", Call: &parseCall{ID: p.id, Name: name, Arguments: []byte(args)}})
		case p.thinking:
			if i := strings.Index(p.buf, "</think>"); i >= 0 {
				if i > 0 {
					out = append(out, parseEvent{Kind: "reasoning", Text: p.buf[:i]})
				}
				p.buf, p.thinking = p.buf[i+len("</think>"):], false
				continue
			}
			if n := len(p.buf) - 8; n > 0 {
				out = append(out, parseEvent{Kind: "reasoning", Text: p.buf[:n]})
				p.buf = p.buf[n:]
			}
			return out, nil
		default:
			if i := strings.Index(p.buf, "<call:"); i >= 0 {
				if i > 0 {
					out = append(out, parseEvent{Kind: "content", Text: p.buf[:i]})
				}
				p.buf, p.inCall = p.buf[i+len("<call:"):], true
				continue
			}
			if n := len(p.buf) - 5; n > 0 {
				out = append(out, parseEvent{Kind: "content", Text: p.buf[:n]})
				p.buf = p.buf[n:]
			}
			return out, nil
		}
	}
}

func (p *fakeParser) Finish(string) ([]parseEvent, error) {
	if p.buf == "" {
		return nil, nil
	}
	kind := "content"
	if p.thinking {
		kind = "reasoning"
	}
	text := p.buf
	p.buf = ""
	return []parseEvent{{Kind: kind, Text: text}}, nil
}

// loaded returns a Strata whose engine is the stub, loaded through Load.
func loaded(mode string, env ...string) (*Strata, *fakeTokenizer) {
	return loadedWith(mode, nil, env...)
}

func loadedWith(mode string, extra []string, env ...string) (*Strata, *fakeTokenizer) {
	dir := GinkgoT().TempDir()
	pack := filepath.Join(dir, "pack")
	if err := os.MkdirAll(filepath.Join(pack, "tokenizer"), 0o755); err != nil {
		Fail(err.Error())
	}
	if err := os.WriteFile(filepath.Join(pack, "tokenizer", "vocab.json"), []byte("{}"), 0o644); err != nil {
		Fail(err.Error())
	}
	for _, f := range []string{"model-00001.gguf", "mmproj.gguf"} {
		if err := os.WriteFile(filepath.Join(pack, f), []byte("x"), 0o644); err != nil {
			Fail(err.Error())
		}
	}
	exe, _ := os.Executable()
	ft := &fakeTokenizer{}
	s := NewStrata()
	s.newTokenizer = func(string, string, string, string, time.Duration) (Tokenizer, error) { return ft, nil }
	s.startEngine = func(c engineConfig) (*engine, error) {
		c.Env = append(c.Env, "STRATA_STUB_MODE="+mode)
		c.Env = append(c.Env, env...)
		return startEngine(c)
	}
	err := s.Load(&pb.ModelOptions{
		ModelFile:   pack,
		ContextSize: 2048,
		Options:     append([]string{"engine_path:" + exe, "strata_root:/unused", "ready_timeout:5s"}, extra...),
	})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = s.Free() })
	return s, ft
}

var _ = Describe("backend", func() {
	It("load refuses without engine", func() {
		s := NewStrata()
		err := s.Load(&pb.ModelOptions{Model: "some-gguf-model.gguf"})
		if err == nil || !strings.Contains(err.Error(), "engine_path") {
			Fail(fmt.Sprintf("Load must refuse a model with no engine configured, got %v", err))
		}
	})

	It("load refuses non pack", func() {
		exe, _ := os.Executable()
		s := NewStrata()
		err := s.Load(&pb.ModelOptions{ModelFile: GinkgoT().TempDir(), Options: []string{"engine_path:" + exe, "strata_root:/x"}})
		if err == nil || !strings.Contains(err.Error(), "pack") {
			Fail(fmt.Sprintf("want a pack error, got %v", err))
		}
	})

	It("load passes engine args", func() {
		s, _ := loaded("ok")
		var seen engineConfig
		orig := s.startEngine
		s.startEngine = func(c engineConfig) (*engine, error) { seen = c; return orig(c) }
		exe, _ := os.Executable()
		pack := filepath.Join(GinkgoT().TempDir(), "p")
		_ = os.MkdirAll(filepath.Join(pack, "tokenizer"), 0o755)
		_ = os.WriteFile(filepath.Join(pack, "tokenizer", "vocab.json"), []byte("{}"), 0o644)
		if err := s.Load(&pb.ModelOptions{ModelFile: pack, ContextSize: 1000,
			Options: []string{"engine_path:" + exe, "strata_root:/x", "engine_args:--vram-reserve-mib 900"}}); err != nil {
			Fail(err.Error())
		}
		got := strings.Join(seen.Args, " ")
		if got != "--pack "+pack+" --max-context 1000 --vram-reserve-mib 900" {
			Fail(fmt.Sprintf("engine args: %q", got))
		}
	})

	It("predict and e o s", func() {
		// "Hi" then the end-of-turn id, which must not reach the client.
		s, ft := loaded("ok", "STRATA_STUB_TOKENS=72,105,248046")
		r, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{
			UseTokenizerTemplate: true,
			Messages:             []*pb.Message{{Role: "user", Content: "hello"}},
			Tokens:               20,
		})
		Expect(err).ToNot(HaveOccurred())
		if string(r.Message) != "Hi" {
			Fail(fmt.Sprintf("got %q", r.Message))
		}
		if r.Tokens != 2 || r.PromptTokens != int32(len("user:hello;")) {
			Fail(fmt.Sprintf("usage: %d completion, %d prompt", r.Tokens, r.PromptTokens))
		}
		if len(ft.lastReq.Messages) != 1 || ft.lastReq.Messages[0]["role"] != "user" {
			Fail(fmt.Sprintf("template got %+v", ft.lastReq.Messages))
		}
		// The legacy entry point works too.
		if out, err := s.Predict(&pb.PredictOptions{Prompt: "x", Tokens: 20}); err != nil || out != "Hi" {
			Fail(fmt.Sprintf("Predict: %q %v", out, err))
		}
	})

	It("stream splits multibyte characters", func() {
		// U+20AC (euro sign) is e2 82 ac: three tokens, one character.
		s, _ := loaded("ok", "STRATA_STUB_TOKENS=226,130,172,33")
		out := make(chan string, 16)
		if err := s.PredictStream(&pb.PredictOptions{Prompt: "x", Tokens: 20}, out); err != nil {
			Fail(err.Error())
		}
		var chunks []string
		for c := range out {
			chunks = append(chunks, c)
		}
		if strings.Join(chunks, "") != "€!" {
			Fail(fmt.Sprintf("chunks %q", chunks))
		}
		for _, c := range chunks {
			if strings.ContainsRune(c, '�') {
				Fail(fmt.Sprintf("a chunk split a character: %q", chunks))
			}
		}
	})

	It("stop string", func() {
		// "abSTOPcd": text after the stop string is cut, and the engine is told to STOP.
		s, _ := loaded("ok", "STRATA_STUB_DELAY_MS=5", "STRATA_STUB_TOKENS=97,98,83,84,79,80,99,100")
		r, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{
			Prompt: "x", Tokens: 1000, StopPrompts: []string{"STOP"},
		})
		Expect(err).ToNot(HaveOccurred())
		if string(r.Message) != "ab" {
			Fail(fmt.Sprintf("got %q", r.Message))
		}
	})

	It("stream cancel by context", func() {
		s, _ := loaded("ok", "STRATA_STUB_DELAY_MS=10")
		ctx, cancel := context.WithCancel(context.Background())
		out := make(chan *pb.Reply, 1024)
		n := 0
		go func() {
			for range out {
				if n++; n == 3 {
					cancel()
				}
			}
		}()
		err := s.PredictStreamRichContext(ctx, &pb.PredictOptions{Prompt: "x", Tokens: 100000}, out)
		close(out)
		if !errors.Is(err, context.Canceled) {
			Fail(fmt.Sprintf("want context.Canceled, got %v", err))
		}
		// The backend is usable afterwards.
		if _, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{Prompt: "x", Tokens: 2}); err != nil {
			Fail(fmt.Sprintf("request after cancel: %v", err))
		}
	})

	It("engine crash surfaces and status", func() {
		s, _ := loaded("crash")
		st, _ := s.Status()
		if st.State != pb.StatusResponse_READY {
			Fail(fmt.Sprintf("state %v, want READY", st.State))
		}
		_, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{Prompt: "x", Tokens: 10})
		if !errors.Is(err, errEngineDied) {
			Fail(fmt.Sprintf("want errEngineDied, got %v", err))
		}
		st, _ = s.Status()
		if st.State != pb.StatusResponse_ERROR {
			Fail(fmt.Sprintf("state %v, want ERROR", st.State))
		}
	})

	It("engine e r r line", func() {
		s, _ := loaded("err")
		_, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{Prompt: "x", Tokens: 10})
		if err == nil || !strings.Contains(err.Error(), "strata engine:") {
			Fail(fmt.Sprintf("got %v", err))
		}
	})

	It("sampling keys and limits", func() {
		log := filepath.Join(GinkgoT().TempDir(), "gen.log")
		s, _ := loaded("ok", "STRATA_STUB_LOG="+log, "STRATA_STUB_TOKENS=65")
		_, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{
			Prompt: "ab", Tokens: 7, Temperature: 0.7, TopP: 0.9, TopK: 100, MinP: 0.05, Seed: 42,
			Penalty: 1.1, FrequencyPenalty: 0.2,
		})
		Expect(err).ToNot(HaveOccurred())
		b, _ := os.ReadFile(log)
		want := "GEN 7 temperature=0.7 top_p=0.9 top_k=64 min_p=0.05 seed=42 penalty_repeat=1.1 penalty_freq=0.2 penalty_last_n=64 97,98"
		if strings.TrimSpace(string(b)) != want {
			Fail(fmt.Sprintf("got  %q\nwant %q", strings.TrimSpace(string(b)), want))
		}
		// Greedy request: no sampling keys at all. Tokens=0 fills the context.
		_ = os.Remove(log)
		if _, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{Prompt: "ab"}); err != nil {
			Fail(err.Error())
		}
		b, _ = os.ReadFile(log)
		if strings.TrimSpace(string(b)) != "GEN 4086 97,98" {
			Fail(fmt.Sprintf("greedy line %q", b))
		}
	})

	It("unsupported and not loaded", func() {
		s := NewStrata()
		if _, err := s.Embeddings(&pb.PredictOptions{}); err == nil || !strings.Contains(err.Error(), "not supported") {
			Fail(fmt.Sprintf("Embeddings: %v", err))
		}
		if _, err := s.Predict(&pb.PredictOptions{Prompt: "x"}); err == nil {
			Fail("Predict before Load must fail")
		}
		s2, _ := loaded("ok")
		if _, err := s2.Predict(&pb.PredictOptions{Prompt: "x", Grammar: "root ::= x"}); err == nil || !strings.Contains(err.Error(), "grammar") {
			Fail(fmt.Sprintf("grammar: %v", err))
		}
		if _, err := s2.Predict(&pb.PredictOptions{Prompt: "<__media__>x", Images: []string{"abc"}}); err == nil || !strings.Contains(err.Error(), "image support") {
			Fail(fmt.Sprintf("images: %v", err))
		}
		if _, err := s2.Predict(&pb.PredictOptions{Prompt: "x", Videos: []string{"abc"}}); err == nil || !strings.Contains(err.Error(), "not supported") {
			Fail(fmt.Sprintf("videos: %v", err))
		}
		if _, err := s2.Predict(&pb.PredictOptions{Prompt: strings.Repeat("a", 5000)}); err == nil || !strings.Contains(err.Error(), "exceeds the available context size") {
			Fail(fmt.Sprintf("long prompt: %v", err))
		}
	})

	It("free ends engine", func() {
		s, _ := loaded("ok")
		eng := s.eng
		if err := s.Free(); err != nil {
			Fail(err.Error())
		}
		if eng.alive() {
			Fail("Free must end the engine")
		}
		if st, _ := s.Status(); st.State != pb.StatusResponse_UNINITIALIZED {
			Fail(fmt.Sprintf("state %v", st.State))
		}
	})

	It("detokenize and tokenize", func() {
		s, _ := loaded("ok")
		r, err := s.Detokenize(&pb.DetokenizeRequest{Tokens: []int32{226, 130, 172, 65}})
		if err != nil || r.Content != "€A" {
			Fail(fmt.Sprintf("%q %v", r.Content, err))
		}
		tr, err := s.TokenizeString(&pb.PredictOptions{Prompt: "ab"})
		if err != nil || tr.Length != 2 {
			Fail(fmt.Sprintf("%v %v", tr.Length, err))
		}
	})

	It("u t f8 joiner and stop matcher", func() {
		var j utf8Joiner
		if got := j.write([]byte{0xe2, 0x82}); got != "" {
			Fail(fmt.Sprintf("incomplete sequence emitted %q", got))
		}
		if got := j.write([]byte{0xac, 'x'}); got != "€x" {
			Fail(fmt.Sprintf("got %q", got))
		}
		j.write([]byte{0xe2})
		if got := j.flush(); got != string([]byte{0xe2}) {
			Fail(fmt.Sprintf("flush %q", got))
		}

		m := newStopMatcher([]string{"</s>"})
		var out string
		for _, p := range []string{"he", "llo<", "/", "s>tail"} {
			out += m.push(p)
		}
		if out != "hello" || !m.hit {
			Fail(fmt.Sprintf("out %q hit %v", out, m.hit))
		}
		m = newStopMatcher([]string{"zz"})
		out = m.push("abc") + m.push("z") + m.flush()
		if out != "abcz" {
			Fail(fmt.Sprintf("a stop-string prefix at the end must be flushed, got %q", out))
		}
	})

})
