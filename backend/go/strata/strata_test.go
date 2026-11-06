package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// fakeTokenizer maps each byte to the id of the same value.
type fakeTokenizer struct{ lastMsgs []ChatMessage }

func (f *fakeTokenizer) Encode(text string, _ bool) ([]int, error) {
	ids := make([]int, 0, len(text))
	for _, b := range []byte(text) {
		ids = append(ids, int(b))
	}
	return ids, nil
}

func (f *fakeTokenizer) TokenBytes(id int) ([]byte, bool) {
	if id < 0 || id > 255 {
		return nil, false
	}
	return []byte{byte(id)}, true
}

func (f *fakeTokenizer) ApplyChatTemplate(msgs []ChatMessage, _ string, _ map[string]any) (string, error) {
	f.lastMsgs = msgs
	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(m.Role + ":" + m.Content + ";")
	}
	return sb.String(), nil
}

func (f *fakeTokenizer) Close() error { return nil }

// loaded returns a Strata whose engine is the stub, loaded through Load.
func loaded(t *testing.T, mode string, env ...string) (*Strata, *fakeTokenizer) {
	t.Helper()
	dir := t.TempDir()
	pack := filepath.Join(dir, "pack")
	if err := os.MkdirAll(filepath.Join(pack, "tokenizer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "tokenizer", "vocab.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	ft := &fakeTokenizer{}
	s := NewStrata()
	s.newTokenizer = func(string, string, string, time.Duration) (Tokenizer, error) { return ft, nil }
	s.startEngine = func(c engineConfig) (*engine, error) {
		c.Env = append(c.Env, "STRATA_STUB_MODE="+mode)
		c.Env = append(c.Env, env...)
		return startEngine(c)
	}
	err := s.Load(&pb.ModelOptions{
		ModelFile:   pack,
		ContextSize: 2048,
		Options:     []string{"engine_path:" + exe, "strata_root:/unused", "ready_timeout:5s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Free() })
	return s, ft
}

func TestLoadRefusesWithoutEngine(t *testing.T) {
	s := NewStrata()
	err := s.Load(&pb.ModelOptions{Model: "some-gguf-model.gguf"})
	if err == nil || !strings.Contains(err.Error(), "engine_path") {
		t.Fatalf("Load must refuse a model with no engine configured, got %v", err)
	}
}

func TestLoadRefusesNonPack(t *testing.T) {
	exe, _ := os.Executable()
	s := NewStrata()
	err := s.Load(&pb.ModelOptions{ModelFile: t.TempDir(), Options: []string{"engine_path:" + exe, "strata_root:/x"}})
	if err == nil || !strings.Contains(err.Error(), "pack") {
		t.Fatalf("want a pack error, got %v", err)
	}
}

func TestLoadPassesEngineArgs(t *testing.T) {
	s, _ := loaded(t, "ok")
	var seen engineConfig
	orig := s.startEngine
	s.startEngine = func(c engineConfig) (*engine, error) { seen = c; return orig(c) }
	exe, _ := os.Executable()
	pack := filepath.Join(t.TempDir(), "p")
	_ = os.MkdirAll(filepath.Join(pack, "tokenizer"), 0o755)
	_ = os.WriteFile(filepath.Join(pack, "tokenizer", "vocab.json"), []byte("{}"), 0o644)
	if err := s.Load(&pb.ModelOptions{ModelFile: pack, ContextSize: 1000,
		Options: []string{"engine_path:" + exe, "strata_root:/x", "engine_args:--vram-reserve-mib 900"}}); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(seen.Args, " ")
	if got != "--pack "+pack+" --max-context 1000 --vram-reserve-mib 900" {
		t.Fatalf("engine args: %q", got)
	}
}

func TestPredictAndEOS(t *testing.T) {
	// "Hi" then the end-of-turn id, which must not reach the client.
	s, ft := loaded(t, "ok", "STRATA_STUB_TOKENS=72,105,248046")
	r, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{
		UseTokenizerTemplate: true,
		Messages:             []*pb.Message{{Role: "user", Content: "hello"}},
		Tokens:               20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Message) != "Hi" {
		t.Fatalf("got %q", r.Message)
	}
	if r.Tokens != 2 || r.PromptTokens != int32(len("user:hello;")) {
		t.Fatalf("usage: %d completion, %d prompt", r.Tokens, r.PromptTokens)
	}
	if len(ft.lastMsgs) != 1 || ft.lastMsgs[0].Role != "user" {
		t.Fatalf("template got %+v", ft.lastMsgs)
	}
	// The legacy entry point works too.
	if out, err := s.Predict(&pb.PredictOptions{Prompt: "x", Tokens: 20}); err != nil || out != "Hi" {
		t.Fatalf("Predict: %q %v", out, err)
	}
}

func TestStreamSplitsMultibyteCharacters(t *testing.T) {
	// U+20AC (euro sign) is e2 82 ac: three tokens, one character.
	s, _ := loaded(t, "ok", "STRATA_STUB_TOKENS=226,130,172,33")
	out := make(chan string, 16)
	if err := s.PredictStream(&pb.PredictOptions{Prompt: "x", Tokens: 20}, out); err != nil {
		t.Fatal(err)
	}
	var chunks []string
	for c := range out {
		chunks = append(chunks, c)
	}
	if strings.Join(chunks, "") != "€!" {
		t.Fatalf("chunks %q", chunks)
	}
	for _, c := range chunks {
		if strings.ContainsRune(c, '�') {
			t.Fatalf("a chunk split a character: %q", chunks)
		}
	}
}

func TestStopString(t *testing.T) {
	// "abSTOPcd": text after the stop string is cut, and the engine is told to STOP.
	s, _ := loaded(t, "ok", "STRATA_STUB_DELAY_MS=5", "STRATA_STUB_TOKENS=97,98,83,84,79,80,99,100")
	r, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{
		Prompt: "x", Tokens: 1000, StopPrompts: []string{"STOP"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Message) != "ab" {
		t.Fatalf("got %q", r.Message)
	}
}

func TestStreamCancelByContext(t *testing.T) {
	s, _ := loaded(t, "ok", "STRATA_STUB_DELAY_MS=10")
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
		t.Fatalf("want context.Canceled, got %v", err)
	}
	// The backend is usable afterwards.
	if _, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{Prompt: "x", Tokens: 2}); err != nil {
		t.Fatalf("request after cancel: %v", err)
	}
}

func TestEngineCrashSurfacesAndStatus(t *testing.T) {
	s, _ := loaded(t, "crash")
	st, _ := s.Status()
	if st.State != pb.StatusResponse_READY {
		t.Fatalf("state %v, want READY", st.State)
	}
	_, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{Prompt: "x", Tokens: 10})
	if !errors.Is(err, errEngineDied) {
		t.Fatalf("want errEngineDied, got %v", err)
	}
	st, _ = s.Status()
	if st.State != pb.StatusResponse_ERROR {
		t.Fatalf("state %v, want ERROR", st.State)
	}
}

func TestEngineERRLine(t *testing.T) {
	s, _ := loaded(t, "err")
	_, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{Prompt: "x", Tokens: 10})
	if err == nil || !strings.Contains(err.Error(), "strata engine:") {
		t.Fatalf("got %v", err)
	}
}

func TestSamplingKeysAndLimits(t *testing.T) {
	log := filepath.Join(t.TempDir(), "gen.log")
	s, _ := loaded(t, "ok", "STRATA_STUB_LOG="+log, "STRATA_STUB_TOKENS=65")
	_, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{
		Prompt: "ab", Tokens: 7, Temperature: 0.7, TopP: 0.9, TopK: 100, MinP: 0.05, Seed: 42,
		Penalty: 1.1, FrequencyPenalty: 0.2,
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	want := "GEN 7 temperature=0.7 top_p=0.9 top_k=64 min_p=0.05 seed=42 penalty_repeat=1.1 penalty_freq=0.2 penalty_last_n=64 97,98"
	if strings.TrimSpace(string(b)) != want {
		t.Fatalf("got  %q\nwant %q", strings.TrimSpace(string(b)), want)
	}
	// Greedy request: no sampling keys at all. Tokens=0 fills the context.
	_ = os.Remove(log)
	if _, err := s.PredictRichContext(context.Background(), &pb.PredictOptions{Prompt: "ab"}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(log)
	if strings.TrimSpace(string(b)) != "GEN 4094 97,98" {
		t.Fatalf("greedy line %q", b)
	}
}

func TestUnsupportedAndNotLoaded(t *testing.T) {
	s := NewStrata()
	if _, err := s.Embeddings(&pb.PredictOptions{}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("Embeddings: %v", err)
	}
	if _, err := s.Predict(&pb.PredictOptions{Prompt: "x"}); err == nil {
		t.Fatal("Predict before Load must fail")
	}
	s2, _ := loaded(t, "ok")
	if _, err := s2.Predict(&pb.PredictOptions{Prompt: "x", Grammar: "root ::= x"}); err == nil || !strings.Contains(err.Error(), "grammar") {
		t.Fatalf("grammar: %v", err)
	}
	if _, err := s2.Predict(&pb.PredictOptions{Prompt: "x", Images: []string{"abc"}}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("images: %v", err)
	}
	if _, err := s2.Predict(&pb.PredictOptions{Prompt: strings.Repeat("a", 5000)}); err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("long prompt: %v", err)
	}
}

func TestFreeEndsEngine(t *testing.T) {
	s, _ := loaded(t, "ok")
	eng := s.eng
	if err := s.Free(); err != nil {
		t.Fatal(err)
	}
	if eng.alive() {
		t.Fatal("Free must end the engine")
	}
	if st, _ := s.Status(); st.State != pb.StatusResponse_UNINITIALIZED {
		t.Fatalf("state %v", st.State)
	}
}

func TestDetokenizeAndTokenize(t *testing.T) {
	s, _ := loaded(t, "ok")
	r, err := s.Detokenize(&pb.DetokenizeRequest{Tokens: []int32{226, 130, 172, 65}})
	if err != nil || r.Content != "€A" {
		t.Fatalf("%q %v", r.Content, err)
	}
	tr, err := s.TokenizeString(&pb.PredictOptions{Prompt: "ab"})
	if err != nil || tr.Length != 2 {
		t.Fatalf("%v %v", tr.Length, err)
	}
}

func TestUTF8JoinerAndStopMatcher(t *testing.T) {
	var j utf8Joiner
	if got := j.write([]byte{0xe2, 0x82}); got != "" {
		t.Fatalf("incomplete sequence emitted %q", got)
	}
	if got := j.write([]byte{0xac, 'x'}); got != "€x" {
		t.Fatalf("got %q", got)
	}
	j.write([]byte{0xe2})
	if got := j.flush(); got != string([]byte{0xe2}) {
		t.Fatalf("flush %q", got)
	}

	m := newStopMatcher([]string{"</s>"})
	var out string
	for _, p := range []string{"he", "llo<", "/", "s>tail"} {
		out += m.push(p)
	}
	if out != "hello" || !m.hit {
		t.Fatalf("out %q hit %v", out, m.hit)
	}
	m = newStopMatcher([]string{"zz"})
	out = m.push("abc") + m.push("z") + m.flush()
	if out != "abcz" {
		t.Fatalf("a stop-string prefix at the end must be flushed, got %q", out)
	}
}
