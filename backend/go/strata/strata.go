package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
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

	// lifeMu serializes Load and the restart of a dead engine. They can run
	// for minutes (a pack build takes up to two hours), so they must not hold
	// mu: Status, Free and the request path only ever need mu, briefly.
	lifeMu sync.Mutex

	mu     sync.RWMutex
	freed  uint64 // counts Free calls, so a Load that Free overtook can tell
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

	// Hy4 seams: the tokenizer from the model file, and the engine's --info.
	prepareHy4Tokenizer func(hy4TokenizerJob) error
	probeHy4Info        func(enginePath, native string, env []string) (map[string]string, error)

	// Inkling seams: the tokenizer from the model files, and the engine's --info.
	prepareInklingTokenizer func(inklingTokenizerJob) error
	probeInklingInfo        func(enginePath string, args, env []string) (map[string]string, error)
}

func NewStrata() *Strata {
	return &Strata{
		startEngine: startEngine,
		buildPack:   runPackBuild,

		prepareHy4Tokenizer: prepareHy4Tokenizer,
		probeHy4Info:        probeHy4Info,

		prepareInklingTokenizer: prepareInklingTokenizer,
		probeInklingInfo:        probeInklingInfo,
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

	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()

	// The previous model goes first, so its memory is free for the new one.
	// From here until the swap below the backend reports no model, and the
	// slow steps (pack build, tokenizer, engine start) run without mu.
	s.mu.Lock()
	s.freeLocked()
	freedAt := s.freed
	s.mu.Unlock()

	if c.build != nil {
		b := *c.build
		b.script = filepath.Join(c.strataRoot, packScript(b.tool))
		if err := s.buildPack(b); err != nil {
			return fmt.Errorf("strata: cannot build the pack from %s: %w", filepath.Base(b.gguf), err)
		}
	}

	if c.hy4 != nil {
		if err := s.prepareHy4(&c); err != nil {
			return err
		}
	}

	if c.inkling != nil {
		if err := s.prepareInkling(&c); err != nil {
			return err
		}
	}

	tok, err := s.newTokenizer(c.python, c.strataRoot, c.pack, c.family, c.readyTimeout)
	if err != nil {
		return fmt.Errorf("strata: %w", err)
	}
	eng, err := s.startEngine(c.engineConfig())
	if err != nil {
		_ = tok.Close()
		return fmt.Errorf("strata: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.freed != freedAt {
		eng.close()
		_ = tok.Close()
		return errors.New("strata: the model was freed while it was loading")
	}
	s.eng, s.tok, s.ctxLen, s.cfg = eng, tok, eng.ctxLen, c
	s.eos = map[int]bool{}
	eosIDs := c.eos
	switch {
	case eosIDs != nil:
	default:
		eosIDs = defaultEOS(tok, c.family)
	}
	for _, id := range eosIDs {
		s.eos[id] = true
	}
	s.padID, s.startID, s.padLiteral = -1, -1, nil
	// Hy4 and Inkling: the engine reads pictures itself, so there is no encoder to start.
	if c.hy4 == nil && c.inkling == nil && c.vision != nil {
		s.vision = newImageEncoder(*c.vision)
		if ids, err := tok.Encode(imagePadText, true); err == nil && len(ids) == 1 {
			s.padID = ids[0]
		}
		if ids, err := tok.Encode(visionStartText, true); err == nil && len(ids) == 1 {
			s.startID = ids[0]
		}
		s.padLiteral, _ = tok.Encode(imagePadText, false)
	}
	xlog.Info("strata: engine ready", "context", eng.ctxLen, "stop_supported", eng.canStop, "vision", c.vision != nil || (c.hy4 != nil && c.hy4.useVision) || (c.inkling != nil && c.inkling.useVision))
	return nil
}

func (c loadConfig) engineConfig() engineConfig {
	return engineConfig{
		Path:         c.enginePath,
		Args:         c.engineArgs(),
		Env:          c.env,
		ReadyTimeout: c.readyTimeout,
		StopTimeout:  c.stopTimeout,
	}
}

// restartAttempts bounds how often one request tries to start the engine again.
const restartAttempts = 2

// restartEngine replaces an engine that has died with a new one started from
// the saved load configuration. The model stays loaded in every other way (the
// tokenizer, the image encoder, the end tokens), so only the process is new;
// its conversation cache starts empty.
//
// It is single-flight: restarts and Load share lifeMu, so a second request
// that finds the same dead engine waits for the first and then uses its result.
// A failed restart leaves the dead engine in place; the next request tries
// again.
func (s *Strata) restartEngine(ctx context.Context, dead *engine) (*engine, error) {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()

	s.mu.RLock()
	cur, cfg := s.eng, s.cfg
	s.mu.RUnlock()
	if cur == nil {
		return nil, grpcerrors.ModelNotLoaded(backendName)
	}
	if cur != dead && cur.alive() {
		return cur, nil // another request, or a Load, already replaced it
	}
	xlog.Warn("strata: the engine is not running, starting it again", "pid", cur.pid())
	cur.close() // reaps it if it is still being waited for

	var (
		eng *engine
		err error
	)
	for attempt := 1; attempt <= restartAttempts; attempt++ {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		if eng, err = s.startEngine(cfg.engineConfig()); err == nil {
			break
		}
		xlog.Warn("strata: the engine did not restart", "attempt", attempt, "error", err)
	}
	if err != nil {
		return nil, fmt.Errorf("strata: the engine exited and could not be started again: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eng != cur { // Free ran meanwhile
		eng.close()
		return nil, grpcerrors.ModelNotLoaded(backendName)
	}
	s.eng = eng
	xlog.Info("strata: engine restarted", "context", eng.ctxLen)
	return eng, nil
}

// defaultEOS finds the end-of-turn tokens of the model family by name, and
// falls back to their known ids.
func defaultEOS(tok Tokenizer, family string) []int {
	names, fallback := []string{"<|endoftext|>", "<|im_end|>"}, defaultEOSIDs
	switch family {
	case familyHy4:
		names, fallback = hy4EOSTexts, defaultHy4EOSIDs
	case familyInkling:
		names, fallback = inklingEOSTexts, defaultInklingEOSIDs
	}
	var out []int
	for _, t := range names {
		if ids, err := tok.Encode(t, true); err == nil && len(ids) == 1 {
			out = append(out, ids[0])
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

// prepareHy4 makes the tokenizer directory from the model file and asks the
// engine what it can do. It sets the pack the tokenizer helper reads.
func (s *Strata) prepareHy4(c *loadConfig) error {
	h := c.hy4
	if c.strataRoot == "" {
		return errors.New("strata: strata_root option (a Strata source checkout, used for its tokenizer and chat template) is required")
	}
	pack := hy4PackDir(h.native)
	if err := s.prepareHy4Tokenizer(hy4TokenizerJob{native: h.native, pack: pack, python: c.python, strataRoot: c.strataRoot}); err != nil {
		return fmt.Errorf("strata: cannot make the tokenizer from %s: %w", filepath.Base(h.native), err)
	}
	c.pack = pack
	var info map[string]string
	if h.wantDrafter || h.vision != "" {
		var err error
		if info, err = s.probeHy4Info(c.enginePath, h.native, c.env); err != nil {
			xlog.Warn("strata: cannot ask the engine what it supports", "error", err)
		}
	}
	for _, n := range h.applyInfo(info) {
		xlog.Info("strata: " + n)
	}
	return nil
}

// prepareInkling makes the tokenizer directory from the model files and asks
// the engine what it can do. It sets the pack the tokenizer helper reads.
func (s *Strata) prepareInkling(c *loadConfig) error {
	h := c.inkling
	if c.strataRoot == "" {
		return errors.New("strata: strata_root option (a Strata source checkout, used for its tokenizer and chat template) is required")
	}
	pack := inklingPackDir(h.native)
	job := inklingTokenizerJob{native: h.native, tokenizerJSON: h.tokenizerJSON, chatTemplate: h.chatTemplate, pack: pack, python: c.python, strataRoot: c.strataRoot}
	if err := s.prepareInklingTokenizer(job); err != nil {
		return fmt.Errorf("strata: cannot make the tokenizer for %s: %w", filepath.Base(h.native), err)
	}
	c.pack = pack
	var info map[string]string
	if h.wantDrafter || h.vision != "" {
		var err error
		if info, err = s.probeInklingInfo(c.enginePath, h.infoArgs(), c.env); err != nil {
			xlog.Warn("strata: cannot ask the engine what it supports", "error", err)
		}
	}
	for _, n := range h.applyInfo(info) {
		xlog.Info("strata: " + n)
	}
	return nil
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
	s.freed++
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
		switch {
		case s.cfg.hy4 != nil:
			text = strings.ReplaceAll(text, marker, hy4ImageStartText+hy4ImagePadText+hy4ImageEndText)
		case s.cfg.inkling != nil:
			text = strings.ReplaceAll(text, marker, inklingImageText)
		default:
			text = strings.ReplaceAll(text, marker, visionStartText+imagePadText+visionEndText)
		}
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

// logitBiasKey turns the request's logit_bias (a JSON object of token id to
// bias, as LocalAI sends it) into the engine's key, "logit_bias=id:bias,id:bias"
// with the ids in ascending order. It returns "" when there is no bias. The
// engine takes biases from -100 to 100, where -100 bans the token, and refuses
// a repeated id.
func logitBiasKey(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	var m map[string]float64
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return "", fmt.Errorf("strata: logit_bias is not a JSON object of token id to bias: %w", err)
	}
	biases := make(map[int]float64, len(m))
	for k, v := range m {
		id, err := strconv.Atoi(k)
		if err != nil || id < 0 {
			return "", fmt.Errorf("strata: logit_bias: %q is not a token id", k)
		}
		if math.IsNaN(v) || v < -100 || v > 100 {
			return "", fmt.Errorf("strata: logit_bias: the bias of token %d must be between -100 and 100", id)
		}
		if _, dup := biases[id]; dup {
			return "", fmt.Errorf("strata: logit_bias: token %d is given twice", id)
		}
		biases[id] = v
	}
	if len(biases) == 0 {
		return "", nil
	}
	ids := make([]int, 0, len(biases))
	for id := range biases {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id) + ":" + strconv.FormatFloat(biases[id], 'g', -1, 64)
	}
	return "logit_bias=" + strings.Join(parts, ","), nil
}

// logprobsK is the number of alternatives to ask the engine for, or -1.
func logprobsK(opts *pb.PredictOptions) int {
	if opts.GetLogprobs() <= 0 && opts.GetTopLogprobs() <= 0 {
		return -1
	}
	return min(int(opts.GetTopLogprobs()), maxLogprobs)
}

// noBiasEngine names the engine of this model when it has no logit_bias key
// (the engine reads the key only on the pack path), or returns "".
func (s *Strata) noBiasEngine() string {
	switch {
	case s.cfg.hy4 != nil:
		return "the Hy4-Flash engine"
	case s.cfg.inkling != nil:
		return "the Inkling engine"
	case s.cfg.noPackFlag:
		return "the Qwen engine of a model that runs without a pack"
	}
	return ""
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
	if !eng.alive() {
		if eng, err = s.restartEngine(ctx, eng); err != nil {
			return res, err
		}
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

	// Refuse what the engine would ignore: a request that is accepted and then
	// answered without its logprobs or its bias is worse than an error.
	biasKey, err := logitBiasKey(opts.GetLogitBias())
	if err != nil {
		return res, err
	}
	if biasKey != "" {
		if name := s.noBiasEngine(); name != "" {
			return res, fmt.Errorf("strata: logit_bias is not supported by %s", name)
		}
	}
	if s.cfg.noPackFlag && logprobsK(opts) >= 0 {
		return res, errors.New("strata: logprobs are not supported by the Qwen engine of a model that runs without a pack")
	}

	p, err := s.preparePrompt(tok, opts)
	if err != nil {
		return res, fmt.Errorf("strata: %w", err)
	}
	if len(p.IDs) == 0 {
		return res, errors.New("strata: empty prompt")
	}
	var (
		embFile   string
		imageKeys []string
		release   = func() {}
	)
	if s.cfg.hy4 != nil {
		if logprobsK(opts) >= 0 {
			return res, errors.New("strata: logprobs are not supported by the Hy4-Flash engine")
		}
		if len(p.Images) > 0 {
			if !s.cfg.hy4.useVision {
				return res, errors.New("strata: this model cannot read images: the vision file is missing or the engine does not support it")
			}
			if imageKeys, release, err = hy4ImageKeys(p.Images); err != nil {
				return res, fmt.Errorf("strata: %w", err)
			}
		}
	} else if s.cfg.inkling != nil {
		if logprobsK(opts) >= 0 {
			return res, errors.New("strata: logprobs are not supported by the Inkling engine")
		}
		if len(p.Images) > 0 {
			if !s.cfg.inkling.useVision {
				return res, errors.New("strata: this model cannot read images: the vision file is missing or the engine does not support it")
			}
			if imageKeys, release, err = directImageKeys("strata-inkling-images-", p.Images); err != nil {
				return res, fmt.Errorf("strata: %w", err)
			}
		}
	} else if embFile, release, err = s.expandImages(vis, &p); err != nil {
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

	keys := append(samplingKeys(opts), imageKeys...)
	if biasKey != "" {
		keys = append(keys, biasKey)
	}
	greq := genRequest{MaxNew: maxNew, Keys: keys, IDs: ids, Embeddings: embFile}
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
	// A chat reply streams the parsed content as Message, as the llama.cpp
	// backend does. With the raw text, LocalAI's stream worker falls back to it
	// for any chunk whose ChatDelta has no content (a tool call, held text) and
	// streams tool-call markup and reasoning as content.
	parsed := opts.GetUseTokenizerTemplate() && len(opts.GetMessages()) > 0
	res, err := s.run(ctx, opts, func(raw string, d *pb.ChatDelta) error {
		msg := raw
		if parsed && d != nil {
			msg = d.Content
		}
		r := &pb.Reply{Message: []byte(msg)}
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

// PredictRich and PredictStreamRich make Strata a grpc.AIModelRich as well.
// The gRPC server's PredictStream only looks for AIModelRichContext inside its
// AIModelRich check (pkg/grpc/server.go), so without these a streamed request
// takes the legacy text-only PredictStream below and loses the ChatDeltas
// (tool calls, reasoning) the output parser produced.
func (s *Strata) PredictRich(opts *pb.PredictOptions) (*pb.Reply, error) {
	return s.PredictRichContext(context.Background(), opts)
}

func (s *Strata) PredictStreamRich(opts *pb.PredictOptions, out chan<- *pb.Reply) error {
	return s.PredictStreamRichContext(context.Background(), opts, out)
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
