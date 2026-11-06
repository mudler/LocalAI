package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

const (
	imagePadText    = "<|image_pad|>"
	visionStartText = "<|vision_start|>"
	visionEndText   = "<|vision_end|>"
	// defaultMediaMarker is the placeholder LocalAI writes into message text
	// for each image.
	defaultMediaMarker = "<__media__>"
)

// Strata is the LocalAI backend wrapping the Strata engine process.
type Strata struct {
	base.SingleThread

	mu     sync.RWMutex
	eng    *engine
	tok    Tokenizer
	vision *imageEncoder
	cfg    loadConfig
	eos    map[int]bool
	ctxLen int

	padID, startID int
	padLiteral     []int

	// Seams for tests.
	startEngine  func(engineConfig) (*engine, error)
	buildPack    func(packBuild) error
	newTokenizer func(python, strataRoot, pack, family string, timeout time.Duration) (Tokenizer, error)
}

func NewStrata() *Strata {
	return &Strata{
		startEngine: startEngine,
		buildPack:   runPackBuild,
		newTokenizer: func(python, root, pack, family string, timeout time.Duration) (Tokenizer, error) {
			return newHelperTokenizer(python, root, pack, family, timeout)
		},
	}
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

	if c.build != nil {
		b := *c.build
		b.script = filepath.Join(c.strataRoot, packScript(b.tool))
		if err := s.buildPack(b); err != nil {
			return fmt.Errorf("strata: cannot build the pack from %s: %w", filepath.Base(b.gguf), err)
		}
	}

	tok, err := s.newTokenizer(c.python, c.strataRoot, c.pack, c.family, c.readyTimeout)
	if err != nil {
		return fmt.Errorf("strata: %w", err)
	}
	eng, err := s.startEngine(engineConfig{
		Path:         c.enginePath,
		Args:         c.engineArgs(),
		Env:          c.env,
		ReadyTimeout: c.readyTimeout,
		StopTimeout:  c.stopTimeout,
	})
	if err != nil {
		_ = tok.Close()
		return fmt.Errorf("strata: %w", err)
	}
	s.eng, s.tok, s.ctxLen, s.cfg = eng, tok, eng.ctxLen, c
	s.eos = map[int]bool{}
	eosIDs := c.eos
	switch {
	case eosIDs != nil:
	case c.family == familyDeepSeek41:
		// The pack's tokenizer names the end id; the engine itself ends on --eos, which defaults to 1.
		eosIDs = defaultDeepSeekEOS
		if r, ok := tok.(interface{ EOSID() (int, bool) }); ok {
			if id, found := r.EOSID(); found {
				eosIDs = []int{id}
			}
		}
	default:
		eosIDs = defaultEOS(tok)
	}
	for _, id := range eosIDs {
		s.eos[id] = true
	}
	s.padID, s.startID, s.padLiteral = -1, -1, nil
	if c.vision != nil {
		s.vision = newImageEncoder(*c.vision)
		if ids, err := tok.Encode(imagePadText, true); err == nil && len(ids) == 1 {
			s.padID = ids[0]
		}
		if ids, err := tok.Encode(visionStartText, true); err == nil && len(ids) == 1 {
			s.startID = ids[0]
		}
		s.padLiteral, _ = tok.Encode(imagePadText, false)
	}
	xlog.Info("strata: engine ready", "context", eng.ctxLen, "stop_supported", eng.canStop, "vision", c.vision != nil)
	return nil
}

// defaultEOS finds the end-of-turn tokens of the Qwen family by name, and
// falls back to their known ids.
func defaultEOS(tok Tokenizer) []int {
	var out []int
	for _, t := range []string{"<|endoftext|>", "<|im_end|>"} {
		if ids, err := tok.Encode(t, true); err == nil && len(ids) == 1 {
			out = append(out, ids[0])
		}
	}
	if len(out) == 0 {
		return defaultEOSIDs
	}
	return out
}

func (s *Strata) familyName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.family
}

func (s *Strata) freeLocked() {
	if s.eng != nil {
		s.eng.close()
		s.eng = nil
	}
	if s.vision != nil {
		s.vision.Close()
		s.vision = nil
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
	}
	// Resident memory of the child processes. On a machine with unified
	// memory this includes what the GPU uses; with a discrete GPU it leaves
	// VRAM out, which the engine does not report.
	mem := &pb.MemoryUsageData{Breakdown: map[string]uint64{}}
	if pid := s.eng.pid(); pid > 0 {
		r := processRSS(pid)
		mem.Breakdown["engine"] = r
		mem.Total += r
	}
	if s.vision != nil {
		if pid := s.vision.Pid(); pid > 0 {
			r := processRSS(pid)
			mem.Breakdown["vision"] = r
			mem.Total += r
		}
	}
	st := pb.StatusResponse_READY
	if s.eng.busy.Get() {
		st = pb.StatusResponse_BUSY
	}
	return pb.StatusResponse{State: st, Memory: mem}, nil
}

func (s *Strata) Embeddings(*pb.PredictOptions) ([]float32, error) {
	return nil, errors.New("strata: embeddings are not supported: the engine serves text generation only")
}

func (s *Strata) state() (*engine, Tokenizer, *imageEncoder, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.eng == nil || s.tok == nil {
		return nil, nil, nil, grpcerrors.ModelNotLoaded(backendName)
	}
	return s.eng, s.tok, s.vision, nil
}

func (s *Strata) TokenizeString(opts *pb.PredictOptions) (pb.TokenizationResponse, error) {
	_, tok, _, err := s.state()
	if err != nil {
		return pb.TokenizationResponse{}, err
	}
	p, err := s.preparePrompt(tok, opts)
	if err != nil {
		return pb.TokenizationResponse{}, err
	}
	out := make([]int32, len(p.IDs))
	for i, v := range p.IDs {
		out[i] = int32(v) // #nosec G115 -- token ids and counts are far below the int32 range
	}
	return pb.TokenizationResponse{Length: int32(len(out)), Tokens: out}, nil // #nosec G115 -- token ids and counts are far below the int32 range
}

func (s *Strata) Detokenize(req *pb.DetokenizeRequest) (pb.DetokenizeResponse, error) {
	_, tok, _, err := s.state()
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

// preparedPrompt is a request turned into ids, before image rows are added.
type preparedPrompt struct {
	IDs      []int
	Images   []string // decoded sources in prompt order
	Thinking bool
	Force    string
	Tools    any
}

// preparePrompt renders the request. With UseTokenizerTemplate the model's own
// chat template does it; otherwise LocalAI's prompt is encoded as it is.
func (s *Strata) preparePrompt(tok Tokenizer, opts *pb.PredictOptions) (preparedPrompt, error) {
	var p preparedPrompt
	marker := s.cfg.mediaMarker
	if marker == "" {
		marker = defaultMediaMarker
	}
	tools, err := parseJSONArg(opts.GetTools())
	if err != nil {
		return p, fmt.Errorf("tools is not valid JSON: %w", err)
	}
	if opts.GetUseTokenizerTemplate() && len(opts.GetMessages()) > 0 {
		choice := parseToolChoice(opts.GetToolChoice())
		req, err := buildPromptRequest(opts, marker, tools, choice)
		if err != nil {
			return p, err
		}
		if raw := strings.TrimSpace(opts.GetTools()); raw != "" && s.cfg.family == familyDeepSeek41 {
			// The V4.1 template writes each tool's schema with tojson, and its bytes
			// are the model's prompt. A Go map would sort the keys; the request's own
			// JSON keeps the order the client wrote, as DeepSeek's reference encoder does.
			req.Tools = json.RawMessage(raw)
		}
		r, err := tok.Prompt(req)
		if err != nil {
			return p, fmt.Errorf("chat template: %w", err)
		}
		p.IDs, p.Thinking, p.Force, p.Tools = r.IDs, r.Thinking, r.Force, r.Tools
		for _, src := range r.Images {
			n, ok := strings.CutPrefix(src, imagePlaceholder)
			idx, err := strconv.Atoi(n)
			if !ok || err != nil || idx < 0 || idx >= len(opts.GetImages()) {
				return p, fmt.Errorf("the prompt refers to an image (%q) the request does not have", src)
			}
			p.Images = append(p.Images, opts.GetImages()[idx])
		}
		return p, nil
	}
	text := opts.GetPrompt()
	if n := len(opts.GetImages()); n > 0 {
		if got := strings.Count(text, marker); got != n {
			return p, fmt.Errorf("the prompt has %d image markers (%s) for %d images", got, marker, n)
		}
		text = strings.ReplaceAll(text, marker, visionStartText+imagePadText+visionEndText)
		p.Images = opts.GetImages()
	}
	ids, err := tok.Encode(text, true)
	if err != nil {
		return p, fmt.Errorf("tokenize: %w", err)
	}
	p.IDs = ids
	p.Tools = flattenTools(tools)
	return p, nil
}

// expandImages encodes the images and widens each image pad token to one per
// image row. It returns the embeddings file for GENI and a release function.
func (s *Strata) expandImages(vis *imageEncoder, p *preparedPrompt) (string, func(), error) {
	if len(p.Images) == 0 {
		return "", func() {}, nil
	}
	if vis == nil || s.padID < 0 || s.startID < 0 {
		return "", nil, errors.New("strata: this model has no image support configured (set the mmproj and vision_exe options)")
	}
	file, counts, release, err := vis.EncodeAll(p.Images)
	if err != nil {
		return "", nil, fmt.Errorf("strata: %w", err)
	}
	out := make([]int, 0, len(p.IDs)+sum(counts))
	k := 0
	for j, t := range p.IDs {
		switch {
		case t == s.padID && j > 0 && p.IDs[j-1] == s.startID:
			if k >= len(counts) {
				release()
				return "", nil, errors.New("strata: the prompt and its images do not match")
			}
			for range counts[k] {
				out = append(out, s.padID)
			}
			k++
		case t == s.padID:
			out = append(out, s.padLiteral...)
		default:
			out = append(out, t)
		}
	}
	if k != len(counts) {
		release()
		return "", nil, errors.New("strata: the prompt and its images do not match")
	}
	p.IDs = out
	return file, release, nil
}

func sum(v []int) int {
	t := 0
	for _, x := range v {
		t += x
	}
	return t
}

// samplingKeys maps the sampling fields the engine's GEN line accepts.
// Fields the engine has no key for (mirostat, typical-p, tail-free, grammar)
// are ignored; a grammar is refused earlier.
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

// logprobsK is the number of alternatives to ask the engine for, or -1.
func logprobsK(opts *pb.PredictOptions) int {
	if opts.GetLogprobs() <= 0 && opts.GetTopLogprobs() <= 0 {
		return -1
	}
	return min(int(opts.GetTopLogprobs()), maxLogprobs)
}

type runResult struct {
	completionTokens int
	promptTokens     int
	finish           string
	timing           genResult
	logprobs         []byte
}

var errStopString = errors.New("stop string reached")

// run is the shared generation path. emit receives, in order, the raw text of
// each token (cut at a stop string) and the parsed delta for it (either may be empty).
func (s *Strata) run(ctx context.Context, opts *pb.PredictOptions, emit func(raw string, d *pb.ChatDelta) error) (runResult, error) {
	var res runResult
	eng, tok, vis, err := s.state()
	if err != nil {
		return res, err
	}
	s.mu.RLock()
	eos, ctxLen := s.eos, s.ctxLen
	s.mu.RUnlock()
	if opts.GetGrammar() != "" {
		return res, errors.New("strata: grammar-constrained generation is not supported")
	}
	if len(opts.GetVideos()) > 0 || len(opts.GetAudios()) > 0 {
		return res, errors.New("strata: video and audio input are not supported")
	}
	if len(opts.GetImages()) > 0 && s.familyName() == familyDeepSeek41 {
		return res, errors.New("strata: images are not supported with the deepseek41 family")
	}

	p, err := s.preparePrompt(tok, opts)
	if err != nil {
		return res, fmt.Errorf("strata: %w", err)
	}
	if len(p.IDs) == 0 {
		return res, errors.New("strata: empty prompt")
	}
	embFile, release, err := s.expandImages(vis, &p)
	if err != nil {
		return res, err
	}
	defer release()
	ids := p.IDs
	res.promptTokens = len(ids)

	room := ctxLen - len(ids) - 8
	if room < 1 {
		return res, fmt.Errorf("strata: request (%d tokens) exceeds the available context size (%d tokens)", len(ids), ctxLen)
	}
	maxNew := int(opts.GetTokens())
	if maxNew <= 0 || maxNew > room {
		maxNew = room
	}

	parser, opening, err := tok.NewParser(p.Thinking, p.Tools, p.Force)
	if err != nil {
		return res, fmt.Errorf("strata: output parser: %w", err)
	}
	defer parser.Close()

	var (
		j       utf8Joiner
		matcher = newStopMatcher(opts.GetStopPrompts())
		conv    = newDeltaBuilder()
		lps     logprobCollector
		k       = logprobsK(opts)
		topN    = int(opts.GetTopLogprobs())
	)
	send := func(raw string, evs []parseEvent) error {
		d := conv.add(evs)
		if raw == "" && d.empty() {
			return nil
		}
		return emit(raw, d.proto())
	}
	if err := send("", opening); err != nil {
		return res, err
	}
	onToken := func(t genToken) error {
		if eos[t.ID] {
			return nil
		}
		b, ok := tok.TokenBytes(t.ID)
		if !ok {
			return fmt.Errorf("engine produced token id %d outside the vocabulary", t.ID)
		}
		res.completionTokens++
		if k >= 0 && t.HasLP {
			lps.add(tok, t, topN)
		}
		// Stop strings are matched on the raw text, before the parser sees it.
		text := matcher.push(j.write(b))
		var evs []parseEvent
		if text != "" {
			evs, err = parser.Feed(text)
			if err != nil {
				return fmt.Errorf("output parser: %w", err)
			}
		}
		if err := send(text, evs); err != nil {
			return err
		}
		if matcher.hit {
			return errStopString
		}
		return nil
	}

	greq := genRequest{MaxNew: maxNew, Keys: samplingKeys(opts), IDs: ids, Embeddings: embFile}
	if k >= 0 {
		greq.Logprobs = &k
	}
	dr, err := eng.generate(ctx, greq, onToken)
	res.timing, res.finish = dr, dr.Finish
	stoppedByString := errors.Is(err, errStopString)
	if stoppedByString {
		res.finish, err = "stop", nil
	}
	if err != nil {
		return res, err
	}
	tail := ""
	if !stoppedByString {
		tail = matcher.push(j.flush())
		if !matcher.hit {
			tail += matcher.flush()
		} else {
			res.finish = "stop"
		}
	}
	var evs []parseEvent
	if tail != "" {
		if evs, err = parser.Feed(tail); err != nil {
			return res, fmt.Errorf("strata: output parser: %w", err)
		}
	}
	reason := res.finish
	if reason == "" {
		reason = "stop"
	}
	fin, err := parser.Finish(reason)
	if err != nil {
		return res, fmt.Errorf("strata: output parser: %w", err)
	}
	if err := send(tail, append(evs, fin...)); err != nil {
		return res, err
	}
	res.logprobs = lps.json()
	xlog.Debug("strata: request done", "finish", res.finish, "prompt", res.promptTokens, "completion", res.completionTokens,
		"resumed", dr.Resumed, "prompt_ms", dr.PromptMS, "decode_ms", dr.DecodeMS)
	return res, nil
}

func (r runResult) usage() *pb.Reply {
	return &pb.Reply{
		Tokens:                 int32(r.completionTokens), // #nosec G115 -- token ids and counts are far below the int32 range
		PromptTokens:           int32(r.promptTokens),     // #nosec G115 -- token ids and counts are far below the int32 range
		TimingPromptProcessing: r.timing.PromptMS,
		TimingTokenGeneration:  r.timing.DecodeMS,
		Logprobs:               r.logprobs,
	}
}

func (s *Strata) PredictRichContext(ctx context.Context, opts *pb.PredictOptions) (*pb.Reply, error) {
	var (
		sb  strings.Builder
		agg deltaAggregate
	)
	res, err := s.run(ctx, opts, func(raw string, d *pb.ChatDelta) error {
		sb.WriteString(raw)
		agg.add(d)
		return nil
	})
	if err != nil {
		return nil, err
	}
	r := res.usage()
	r.Message = []byte(sb.String())
	if d := agg.delta(); d != nil {
		r.ChatDeltas = []*pb.ChatDelta{d}
	}
	return r, nil
}

// PredictStreamRichContext sends replies into the channel and does not close it.
func (s *Strata) PredictStreamRichContext(ctx context.Context, opts *pb.PredictOptions, out chan<- *pb.Reply) error {
	put := func(r *pb.Reply) error {
		select {
		case out <- r:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	res, err := s.run(ctx, opts, func(raw string, d *pb.ChatDelta) error {
		r := &pb.Reply{Message: []byte(raw)}
		if d != nil {
			r.ChatDeltas = []*pb.ChatDelta{d}
		}
		return put(r)
	})
	if err != nil {
		return err
	}
	return put(res.usage())
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
	_, err := s.run(context.Background(), opts, func(raw string, _ *pb.ChatDelta) error {
		if raw != "" {
			results <- raw
		}
		return nil
	})
	return err
}
