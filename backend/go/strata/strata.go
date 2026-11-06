package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

const backendName = "strata"

// Default end-of-turn ids of the Qwen-family models Strata serves
// (<|endoftext|>, <|im_end|>). The engine reports these as ordinary tokens
// followed by a "stop" finish, so the backend drops them from the output.
// Override with the eos_ids option for other families.
var defaultEOSIDs = []int{248044, 248046}

// Strata is the LocalAI backend wrapping the Strata engine process.
type Strata struct {
	base.SingleThread

	mu     sync.RWMutex
	eng    *engine
	tok    Tokenizer
	eos    map[int]bool
	ctxLen int

	// Seams for tests.
	startEngine  func(engineConfig) (*engine, error)
	newTokenizer func(python, strataRoot, pack string, timeout time.Duration) (Tokenizer, error)
}

func NewStrata() *Strata {
	return &Strata{
		startEngine: startEngine,
		newTokenizer: func(python, root, pack string, timeout time.Duration) (Tokenizer, error) {
			return newHelperTokenizer(python, root, pack, timeout)
		},
	}
}

// loadConfig is the parsed form of ModelOptions.
type loadConfig struct {
	enginePath   string
	pack         string
	strataRoot   string
	python       string
	extraArgs    []string
	readyTimeout time.Duration
	stopTimeout  time.Duration
	eos          []int
	ctxSize      int
}

// parseLoad reads the model options and checks the files exist. Load must
// refuse models it cannot serve: with no explicit backend, LocalAI probes
// every backend with the model and binds to the first Load that succeeds.
func parseLoad(opts *pb.ModelOptions) (loadConfig, error) {
	c := loadConfig{eos: defaultEOSIDs, ctxSize: int(opts.GetContextSize())}
	kv := map[string]string{}
	for _, o := range opts.GetOptions() {
		k, v, _ := strings.Cut(o, ":")
		kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	c.enginePath = kv["engine_path"]
	if c.enginePath == "" {
		c.enginePath = os.Getenv("STRATA_ENGINE_PATH")
	}
	if c.enginePath == "" {
		return c, errors.New("strata: no engine executable configured; set the engine_path option (or STRATA_ENGINE_PATH)")
	}
	if st, err := os.Stat(c.enginePath); err != nil || st.IsDir() {
		return c, fmt.Errorf("strata: engine_path %q is not an executable file", c.enginePath)
	}

	c.pack = opts.GetModelFile()
	if c.pack == "" {
		c.pack = filepath.Join(opts.GetModelPath(), opts.GetModel())
	}
	if _, err := os.Stat(filepath.Join(c.pack, "tokenizer", "vocab.json")); err != nil {
		return c, fmt.Errorf("strata: %q is not a Strata pack directory (tokenizer/vocab.json missing)", c.pack)
	}

	c.strataRoot = kv["strata_root"]
	if c.strataRoot == "" {
		c.strataRoot = os.Getenv("STRATA_ROOT")
	}
	c.python = kv["python"]
	if v := kv["engine_args"]; v != "" {
		c.extraArgs = strings.Fields(v)
	}
	c.readyTimeout = 15 * time.Minute
	if v := kv["ready_timeout"]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return c, fmt.Errorf("strata: bad ready_timeout %q", v)
		}
		c.readyTimeout = d
	}
	c.stopTimeout = 30 * time.Second
	if v := kv["stop_timeout"]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return c, fmt.Errorf("strata: bad stop_timeout %q", v)
		}
		c.stopTimeout = d
	}
	if v := kv["eos_ids"]; v != "" {
		c.eos = nil
		for _, f := range strings.Split(v, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(f))
			if err != nil {
				return c, fmt.Errorf("strata: bad eos_ids entry %q", f)
			}
			c.eos = append(c.eos, n)
		}
	}
	return c, nil
}

func (s *Strata) Load(opts *pb.ModelOptions) error {
	c, err := parseLoad(opts)
	if err != nil {
		return err
	}
	if c.strataRoot == "" {
		return errors.New("strata: strata_root option (a Strata source checkout, used for its tokenizer and chat template) is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.freeLocked()

	tok, err := s.newTokenizer(c.python, c.strataRoot, c.pack, c.readyTimeout)
	if err != nil {
		return fmt.Errorf("strata: %w", err)
	}
	args := []string{"--pack", c.pack}
	if c.ctxSize > 0 {
		args = append(args, "--max-context", strconv.Itoa(c.ctxSize))
	}
	args = append(args, c.extraArgs...)
	eng, err := s.startEngine(engineConfig{
		Path:         c.enginePath,
		Args:         args,
		ReadyTimeout: c.readyTimeout,
		StopTimeout:  c.stopTimeout,
	})
	if err != nil {
		_ = tok.Close()
		return fmt.Errorf("strata: %w", err)
	}
	s.eng, s.tok, s.ctxLen = eng, tok, eng.ctxLen
	s.eos = map[int]bool{}
	for _, id := range c.eos {
		s.eos[id] = true
	}
	xlog.Info("strata: engine ready", "context", eng.ctxLen, "stop_supported", eng.canStop)
	return nil
}

func (s *Strata) freeLocked() {
	if s.eng != nil {
		s.eng.close()
		s.eng = nil
	}
	if s.tok != nil {
		_ = s.tok.Close()
		s.tok = nil
	}
}

func (s *Strata) Free() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.freeLocked()
	return nil
}

func (s *Strata) Status() (pb.StatusResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch {
	case s.eng == nil:
		return pb.StatusResponse{State: pb.StatusResponse_UNINITIALIZED}, nil
	case !s.eng.alive():
		return pb.StatusResponse{State: pb.StatusResponse_ERROR}, nil
	case s.eng.busy.Get():
		return pb.StatusResponse{State: pb.StatusResponse_BUSY}, nil
	}
	return pb.StatusResponse{State: pb.StatusResponse_READY}, nil
}

func (s *Strata) Embeddings(*pb.PredictOptions) ([]float32, error) {
	return nil, errors.New("strata: embeddings are not supported: the engine serves text generation only")
}

func (s *Strata) TokenizeString(opts *pb.PredictOptions) (pb.TokenizationResponse, error) {
	_, tok, _, _, err := s.state()
	if err != nil {
		return pb.TokenizationResponse{}, err
	}
	ids, err := tok.Encode(opts.GetPrompt(), true)
	if err != nil {
		return pb.TokenizationResponse{}, err
	}
	out := make([]int32, len(ids))
	for i, v := range ids {
		out[i] = int32(v)
	}
	return pb.TokenizationResponse{Length: int32(len(out)), Tokens: out}, nil
}

func (s *Strata) Detokenize(req *pb.DetokenizeRequest) (pb.DetokenizeResponse, error) {
	_, tok, _, _, err := s.state()
	if err != nil {
		return pb.DetokenizeResponse{}, err
	}
	var j utf8Joiner
	var sb strings.Builder
	for _, id := range req.GetTokens() {
		b, ok := tok.TokenBytes(int(id))
		if !ok {
			return pb.DetokenizeResponse{}, fmt.Errorf("strata: token id %d is outside the vocabulary", id)
		}
		sb.WriteString(j.write(b))
	}
	sb.WriteString(j.flush())
	return pb.DetokenizeResponse{Content: sb.String()}, nil
}

func (s *Strata) state() (*engine, Tokenizer, map[int]bool, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.eng == nil || s.tok == nil {
		return nil, nil, nil, 0, grpcerrors.ModelNotLoaded(backendName)
	}
	return s.eng, s.tok, s.eos, s.ctxLen, nil
}

// buildPrompt renders chat messages with the model's template when the
// request asks for it, and otherwise uses the prompt LocalAI already built.
func buildPrompt(tok Tokenizer, opts *pb.PredictOptions) (string, error) {
	if opts.GetUseTokenizerTemplate() && len(opts.GetMessages()) > 0 {
		msgs := make([]ChatMessage, 0, len(opts.GetMessages()))
		for _, m := range opts.GetMessages() {
			cm := ChatMessage{
				Role: m.GetRole(), Content: m.GetContent(), Name: m.GetName(),
				ToolCallID: m.GetToolCallId(), ReasoningContent: m.GetReasoningContent(),
			}
			if tc := m.GetToolCalls(); tc != "" {
				cm.ToolCalls = jsonRaw(tc)
			}
			msgs = append(msgs, cm)
		}
		kwargs := map[string]any{}
		if v, ok := opts.GetMetadata()["enable_thinking"]; ok {
			if b, err := strconv.ParseBool(v); err == nil {
				kwargs["enable_thinking"] = b
			}
		}
		return tok.ApplyChatTemplate(msgs, opts.GetTools(), kwargs)
	}
	return opts.GetPrompt(), nil
}

// samplingKeys maps the sampling fields the engine's GEN line accepts.
// Fields the engine has no key for (mirostat, typical-p, tail-free, grammar,
// logit bias in LocalAI's format) are ignored; a grammar is refused earlier.
func samplingKeys(opts *pb.PredictOptions) []string {
	var k []string
	f := func(name string, v float32) { k = append(k, name+"="+strconv.FormatFloat(float64(v), 'g', -1, 32)) }
	if t := opts.GetTemperature(); t > 0 {
		f("temperature", t)
		if p := opts.GetTopP(); p > 0 && p < 1 {
			f("top_p", p)
		}
		if tk := opts.GetTopK(); tk > 0 {
			if tk > 64 { // the sampled path requires top_k in 1..64
				tk = 64
			}
			k = append(k, "top_k="+strconv.Itoa(int(tk)))
		}
		if mp := opts.GetMinP(); mp > 0 {
			f("min_p", mp)
		}
		if sd := opts.GetSeed(); sd > 0 {
			k = append(k, "seed="+strconv.Itoa(int(sd)))
		}
	}
	pen := false
	if p := opts.GetPenalty(); p > 0 && p != 1 {
		f("penalty_repeat", p)
		pen = true
	}
	if p := opts.GetFrequencyPenalty(); p != 0 {
		f("penalty_freq", p)
		pen = true
	}
	if p := opts.GetPresencePenalty(); p != 0 {
		f("penalty_present", p)
		pen = true
	}
	if pen {
		n := int(opts.GetRepeat())
		if n <= 0 {
			n = 64
		}
		k = append(k, "penalty_last_n="+strconv.Itoa(n))
	}
	return k
}

type runResult struct {
	completionTokens int
	promptTokens     int
	finish           string
}

var errStopString = errors.New("stop string reached")

// run is the shared generation path. emit receives text chunks in order.
func (s *Strata) run(ctx context.Context, opts *pb.PredictOptions, emit func(string) error) (runResult, error) {
	var res runResult
	eng, tok, eos, ctxLen, err := s.state()
	if err != nil {
		return res, err
	}
	if opts.GetGrammar() != "" {
		return res, errors.New("strata: grammar-constrained generation is not supported")
	}
	if len(opts.GetImages()) > 0 || len(opts.GetVideos()) > 0 || len(opts.GetAudios()) > 0 {
		return res, errors.New("strata: image, video and audio input are not supported (the engine's vision path, GENI, is not wired)")
	}

	prompt, err := buildPrompt(tok, opts)
	if err != nil {
		return res, fmt.Errorf("strata: chat template: %w", err)
	}
	ids, err := tok.Encode(prompt, true)
	if err != nil {
		return res, fmt.Errorf("strata: tokenize: %w", err)
	}
	if len(ids) == 0 {
		return res, errors.New("strata: empty prompt")
	}
	res.promptTokens = len(ids)

	room := ctxLen - len(ids)
	if room < 1 {
		return res, fmt.Errorf("strata: prompt (%d tokens) does not fit the context (%d)", len(ids), ctxLen)
	}
	maxNew := int(opts.GetTokens())
	if maxNew <= 0 || maxNew > room {
		maxNew = room
	}

	var (
		j       utf8Joiner
		matcher = newStopMatcher(opts.GetStopPrompts())
	)
	send := func(text string) error {
		if text == "" {
			return nil
		}
		return emit(text)
	}
	onToken := func(id int) error {
		if eos[id] {
			return nil
		}
		b, ok := tok.TokenBytes(id)
		if !ok {
			return fmt.Errorf("engine produced token id %d outside the vocabulary", id)
		}
		res.completionTokens++
		if err := send(matcher.push(j.write(b))); err != nil {
			return err
		}
		if matcher.hit {
			return errStopString
		}
		return nil
	}

	finish, err := eng.generate(ctx, genRequest{MaxNew: maxNew, Keys: samplingKeys(opts), IDs: ids}, onToken)
	res.finish = finish
	if errors.Is(err, errStopString) {
		res.finish = "stop"
		return res, nil
	}
	if err != nil {
		return res, err
	}
	if err := send(matcher.push(j.flush())); err != nil {
		return res, err
	}
	if err := send(matcher.flush()); err != nil {
		return res, err
	}
	return res, nil
}

func (s *Strata) PredictRichContext(ctx context.Context, opts *pb.PredictOptions) (*pb.Reply, error) {
	var sb strings.Builder
	res, err := s.run(ctx, opts, func(t string) error { sb.WriteString(t); return nil })
	if err != nil {
		return nil, err
	}
	return &pb.Reply{Message: []byte(sb.String()), Tokens: int32(res.completionTokens), PromptTokens: int32(res.promptTokens)}, nil
}

// PredictStreamRichContext sends replies into the channel and does not close it.
func (s *Strata) PredictStreamRichContext(ctx context.Context, opts *pb.PredictOptions, out chan<- *pb.Reply) error {
	res, err := s.run(ctx, opts, func(t string) error {
		select {
		case out <- &pb.Reply{Message: []byte(t)}:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		return err
	}
	select {
	case out <- &pb.Reply{Tokens: int32(res.completionTokens), PromptTokens: int32(res.promptTokens)}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// Legacy entry points, for callers that do not use the rich interface.
func (s *Strata) Predict(opts *pb.PredictOptions) (string, error) {
	r, err := s.PredictRichContext(context.Background(), opts)
	if err != nil {
		return "", err
	}
	return string(r.Message), nil
}

func (s *Strata) PredictStream(opts *pb.PredictOptions, results chan string) error {
	defer close(results)
	_, err := s.run(context.Background(), opts, func(t string) error { results <- t; return nil })
	return err
}
