package main

// LocalAI gRPC backend for dllm.cpp (DiffusionGemma block-diffusion models).
//
// Wiring overview:
//   - Load opens the GGUF via dllm_capi_load and starts the per-model worker
//     goroutine that serializes every C call (see submit).
//   - PredictRich / PredictStreamRich implement grpc.AIModelRich: when the
//     request carries raw messages (use_tokenizer_template), the backend owns
//     templating (RenderGemma4) and output parsing (Gemma4Parser) and replies
//     with ChatDeltas, like the llama.cpp autoparser and the ds4 backend.
//   - The legacy Predict / PredictStream methods delegate to the rich pair
//     (cloud-proxy precedent); the gRPC server prefers the rich path anyway.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

// generator is the seam between the backend wiring and the dllm.cpp C-ABI:
// the real implementation (capiGenerator) wraps the cGenerate/cTokenizeJSON
// family, while tests substitute a fake to exercise prompt construction,
// parsing and serialization without libdllm.so.
type generator interface {
	generate(prompt, optsJSON string) (string, error)
	// generateStream invokes onBlock once per committed diffusion block, on
	// the thread running the C call, before returning.
	generateStream(prompt, optsJSON string, onBlock func(text string)) error
	tokenizeJSON(text string) (string, error)
	// cancel is the ONE entry point safe to call concurrently with an
	// in-flight generate on the same ctx (dllm_capi.h: it only flips an
	// atomic; everything else must be externally serialized per ctx).
	cancel()
	free()
}

// capiGenerator is the production generator over one dllm_ctx handle.
type capiGenerator struct {
	h uintptr
}

func (g *capiGenerator) generate(prompt, optsJSON string) (string, error) {
	return cGenerate(g.h, prompt, optsJSON)
}

func (g *capiGenerator) generateStream(prompt, optsJSON string, onBlock func(text string)) error {
	// on_step (per-denoise-step canvas preview, dllm.cpp's --visual) is
	// passed as nil for now: a future progress hook for the React UI can
	// plumb it through without touching the C binding.
	return cGenerateStream(g.h, prompt, optsJSON, onBlock, nil)
}

func (g *capiGenerator) tokenizeJSON(text string) (string, error) {
	return cTokenizeJSON(g.h, text)
}

func (g *capiGenerator) cancel() {
	cCancel(g.h)
}

func (g *capiGenerator) free() {
	cFree(g.h)
}

// Dllm is the gRPC backend instance: one per loaded model (LocalAI starts
// one backend process per model).
type Dllm struct {
	base.Base

	gen generator
	// genOpts holds the model-level generation overrides parsed from
	// ModelOptions.Options at Load (eb_*, blocks, kv_cache). The C-ABI takes
	// them per-generate, not per-load, so they are merged into every
	// request's opts JSON (requestOptsJSON).
	genOpts map[string]any

	// jobs is the per-model worker queue. dllm_capi.h requires every entry
	// point EXCEPT dllm_capi_cancel to be externally serialized per ctx (one
	// ctx = one concurrent generate/tokenize; last_error is unsafe to read
	// while a call is in flight). A single goroutine owning all C calls makes
	// that contract structural instead of relying on lock discipline.
	jobs     chan func()
	workerWG sync.WaitGroup

	// genMu guards gen against Free racing in-flight requests: requests hold
	// the read lock for their full duration (they stay concurrent with each
	// other - the worker still serializes the C calls), Free takes the write
	// lock so it can only run when no request is in flight.
	genMu sync.RWMutex
}

func (d *Dllm) startWorker() {
	d.jobs = make(chan func())
	d.workerWG.Add(1)
	go func() {
		defer d.workerWG.Done()
		for job := range d.jobs {
			job()
		}
	}()
}

// submit runs job on the worker goroutine and waits for it to finish.
// Concurrent gRPC requests therefore queue up and execute one at a time
// against the single dllm_ctx.
func (d *Dllm) submit(job func()) {
	done := make(chan struct{})
	d.jobs <- func() {
		defer close(done)
		job()
	}
	<-done
}

// Load opens the GGUF and prepares the worker. Load-time engine parameters
// travel as the flat params JSON of dllm_capi_load; generation overrides
// from Options are stored for per-request opts JSON instead (the C-ABI has
// no per-load sampler state).
func (d *Dllm) Load(opts *pb.ModelOptions) error {
	if d.gen != nil {
		return errors.New("dllm: model already loaded")
	}

	params := map[string]any{
		"n_gpu_layers": opts.GetNGPULayers(),
	}
	if opts.GetThreads() > 0 {
		params["n_threads"] = opts.GetThreads()
	}
	if opts.GetContextSize() > 0 {
		params["ctx_len"] = opts.GetContextSize()
	}
	paramsJSON, err := buildOptsJSON(params)
	if err != nil {
		return err
	}

	d.genOpts = parseModelGenOpts(opts.GetOptions())

	h := cLoad(opts.GetModelFile(), paramsJSON)
	if h == 0 {
		// No ctx exists on load failure, so last_error(NULL) only carries the
		// static NULL-ctx message; the real reason is on the backend's stderr.
		return fmt.Errorf("dllm: load %q failed: %s (see backend log for details)",
			opts.GetModelFile(), lastErrorOr(0, "unknown error"))
	}
	d.gen = &capiGenerator{h: h}
	d.startWorker()
	xlog.Info("dllm: model loaded", "model", opts.GetModelFile(), "params", paramsJSON, "gen_opts", d.genOpts)
	return nil
}

// Free releases the dllm ctx and stops the worker. Safe when never loaded.
//
// The write lock is essential: the gRPC server (pkg/grpc/server.go, see the
// model-unload path around line 764) calls Free with no locking of its own,
// and base.Base provides none either. Without it a request racing Free would
// panic sending on the closed jobs channel - or worse, generate on a freed C
// ctx. Holding genMu until gen is nil also turns post-Free requests into a
// clean "model not loaded" error instead of a crash.
func (d *Dllm) Free() error {
	d.genMu.Lock()
	defer d.genMu.Unlock()
	if d.gen == nil {
		return nil
	}
	d.submit(d.gen.free)
	close(d.jobs)
	d.workerWG.Wait()
	d.gen = nil
	return nil
}

// Cancel requests cancellation of the in-flight generate. It deliberately
// bypasses the worker queue: dllm_capi_cancel is the one call the C-ABI
// allows from any goroutine mid-generate (it only flips an atomic).
//
// LIMITATION: nothing invokes this on client disconnect today. The gRPC
// server (pkg/grpc/server.go) does not hand the request/stream context to
// Predict/PredictStreamRich, so a dropped HTTP client cannot reach the
// backend until that plumbing exists; the method is here so future server
// wiring (or an admin RPC) has something to call. Note dllm_capi.h's
// cancel-reset race: each generate resets the flag on entry, so a caller
// racing a new generate should re-issue Cancel.
func (d *Dllm) Cancel() {
	if d.gen != nil {
		d.gen.cancel()
	}
}

// dllmGenOptKeys are the ModelOptions.Options keys this backend forwards to
// the engine. Options is a shared free-form bag (other layers put their own
// entries there), so unknown keys are skipped with a warning, not an error.
var dllmGenOptKeys = map[string]bool{
	"blocks":   true,
	"kv_cache": true, // "auto"|"on"|"off"; honored by the engine from P3
}

// parseModelGenOpts parses "key:value" Options entries into the flat scalar
// map merged into every generate's opts JSON. eb_* (Entropy-Bound sampler
// knobs) and the keys in dllmGenOptKeys are recognized; values are typed by
// first successful parse (int, then float, else string) to match the C
// scanner's number/string scalars.
func parseModelGenOpts(options []string) map[string]any {
	out := map[string]any{}
	for _, o := range options {
		key, val, found := strings.Cut(o, ":")
		if !found {
			xlog.Warn("dllm: ignoring malformed option (want key:value)", "option", o)
			continue
		}
		if !strings.HasPrefix(key, "eb_") && !dllmGenOptKeys[key] {
			xlog.Debug("dllm: ignoring unrecognized option", "key", key)
			continue
		}
		out[key] = parseScalarOpt(val)
	}
	return out
}

func parseScalarOpt(v string) any {
	if iv, err := strconv.ParseInt(v, 10, 64); err == nil {
		return iv
	}
	if fv, err := strconv.ParseFloat(v, 64); err == nil {
		return fv
	}
	return v
}

// metadataEnableThinking reads the enable_thinking gate. Unlike ds4 (default
// ON, matching ds4-server), dllm defaults OFF: DiffusionGemma's chat
// template guards every thinking branch with `enable_thinking is defined and
// enable_thinking`, i.e. thinking is opt-in for this model family, and the
// no-thinking render pre-closes an empty thought channel that the OFF
// default must produce.
func metadataEnableThinking(opts *pb.PredictOptions) bool {
	v := opts.GetMetadata()["enable_thinking"]
	return v == "true" || v == "1"
}

// buildPrompt resolves the prompt for a request. With use_tokenizer_template
// and raw messages the backend owns templating (RenderGemma4) and the output
// is in the known gemma4 format, so parse=true. Without it the caller
// templated the prompt themselves (LocalAI's Go templates + PEG fallback, or
// a bare completion): the prompt passes through verbatim and the output is
// NOT gemma4-parsed - it is emitted as plain content and the Go side's
// extraction applies, as for any non-autoparsing backend.
func buildPrompt(opts *pb.PredictOptions) (prompt string, parse bool, err error) {
	if opts.GetUseTokenizerTemplate() && len(opts.GetMessages()) > 0 {
		prompt, err = RenderGemma4(opts.GetMessages(), opts.GetTools(), metadataEnableThinking(opts), true)
		return prompt, true, err
	}
	return opts.GetPrompt(), false, nil
}

// requestOptsJSON merges the model-level overrides with the request's
// sampling fields into the flat opts JSON for one generate call.
func (d *Dllm) requestOptsJSON(opts *pb.PredictOptions) (string, error) {
	m := make(map[string]any, len(d.genOpts)+2)
	for k, v := range d.genOpts {
		m[k] = v
	}
	if n := opts.GetTokens(); n > 0 {
		// The engine rounds n_predict UP to a whole number of diffusion
		// blocks (the canvas is denoised block-wise), so the completion may
		// run slightly past the requested budget. Tokens==0 omits the key so
		// the engine's GGUF-metadata default applies (the C-ABI documents
		// per-key defaults; no hardcoded 256 like ds4's grpc-server).
		m["n_predict"] = n
	}
	if s := opts.GetSeed(); s > 0 {
		// The engine seeds mt19937 with explicit non-negative seeds. Seed<=0
		// is omitted: proto3 cannot distinguish 0 from unset, and negative
		// values conventionally mean "random" across LocalAI backends.
		m["seed"] = s
	}
	return buildOptsJSON(m)
}

// prepareRequest is the shared prologue of the rich methods: resolve the
// prompt (and whether the output gets gemma4-parsed) and build the per-call
// opts JSON.
func (d *Dllm) prepareRequest(opts *pb.PredictOptions) (prompt string, parse bool, optsJSON string, err error) {
	prompt, parse, err = buildPrompt(opts)
	if err != nil {
		return "", false, "", err
	}
	optsJSON, err = d.requestOptsJSON(opts)
	if err != nil {
		return "", false, "", err
	}
	return prompt, parse, optsJSON, nil
}

// sanitizeUTF8 makes s safe for a proto3 string field. Block-boundary
// detokenization and byte-fallback tokens can produce invalid UTF-8, and
// grpc-go refuses to marshal it ("string field contains invalid UTF-8"), so
// every string destined for a Reply/ChatDelta must pass through here (or
// through splitValidUTF8, which calls it). Lone malformed bytes are genuinely
// undecodable: replace with U+FFFD rather than crash the stream.
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}

// utf8SeqLen returns the declared sequence length of a UTF-8 leading byte
// (1 for bytes that can never lead a multi-byte sequence, so they are never
// held back and fall through to sanitizeUTF8's replacement).
func utf8SeqLen(b byte) int {
	switch {
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	case b&0xF8 == 0xF0:
		return 4
	default:
		return 1
	}
}

// splitValidUTF8 prepends the previous block's carry to the new block and
// splits the result into text safe to emit now and a trailing INCOMPLETE
// UTF-8 sequence (at most utf8.UTFMax-1 bytes) to carry into the next block:
// the per-block detokenize can split a multi-byte character across block
// boundaries (llama.cpp's grpc-server holds back the same way). Only a
// suffix that can still become a valid rune is withheld; bytes that are
// already undecodable are replaced immediately so the carry stays bounded.
func splitValidUTF8(carry, block string) (emit, newCarry string) {
	s := carry + block
	cut := len(s)
	for i := len(s) - 1; i >= 0 && len(s)-i < utf8.UTFMax; i-- {
		b := s[i]
		if b < utf8.RuneSelf {
			break // ASCII: everything before the tail scan is complete
		}
		if !utf8.RuneStart(b) {
			continue // continuation byte: keep looking for its leading byte
		}
		// Leading byte: hold the sequence back iff it declares more bytes
		// than the stream has produced so far (it may complete next block).
		if utf8SeqLen(b) > len(s)-i {
			cut = i
		}
		break
	}
	return sanitizeUTF8(s[:cut]), s[cut:]
}

// PredictRich is the non-streaming inference path (grpc.AIModelRich).
// Returns one Reply whose Message is the aggregated assistant content and
// whose ChatDeltas carry the parsed content/reasoning/tool-call events.
func (d *Dllm) PredictRich(opts *pb.PredictOptions) (*pb.Reply, error) {
	d.genMu.RLock()
	defer d.genMu.RUnlock()
	if d.gen == nil {
		return nil, grpcerrors.ModelNotLoaded("dllm")
	}
	prompt, parse, optsJSON, err := d.prepareRequest(opts)
	if err != nil {
		return nil, err
	}

	var out string
	var genErr error
	d.submit(func() {
		out, genErr = d.gen.generate(prompt, optsJSON)
	})
	if genErr != nil {
		return nil, genErr
	}
	// Byte-fallback tokens can detokenize to invalid UTF-8; proto3 strings
	// must be valid or grpc-go fails the whole reply at marshal time.
	out = sanitizeUTF8(out)

	if !parse {
		// Raw-prompt mode: plain content, no gemma4 parsing (see buildPrompt).
		return &pb.Reply{Message: []byte(out), ChatDeltas: []*pb.ChatDelta{{Content: out}}}, nil
	}

	// The prompt renders with add_generation_prompt; both thinking modes
	// leave the model starting in content state (see the Gemma4Parser header
	// comment), hence NewGemma4Parser(false).
	parser := NewGemma4Parser(false)
	if reply := replyFromDeltas(append(parser.Feed(out), parser.Close()...)); reply != nil {
		return reply, nil
	}
	// Everything was markers (or out was empty): an empty but non-nil Reply.
	return &pb.Reply{}, nil
}

// PredictStreamRich is the streaming counterpart (grpc.AIModelRich): one
// Reply per committed diffusion block that produced deltas. Per the
// interface contract the channel is only sent into here - the gRPC server
// closes it after this returns (opposite to legacy PredictStream).
func (d *Dllm) PredictStreamRich(opts *pb.PredictOptions, results chan<- *pb.Reply) error {
	d.genMu.RLock()
	defer d.genMu.RUnlock()
	if d.gen == nil {
		return grpcerrors.ModelNotLoaded("dllm")
	}
	prompt, parse, optsJSON, err := d.prepareRequest(opts)
	if err != nil {
		return err
	}

	var parser *Gemma4Parser
	if parse {
		parser = NewGemma4Parser(false)
	}
	// emit runs inside onBlock, i.e. on the thread driving the C generate.
	// Sending on results can block on a slow consumer, but the server-side
	// pump (pkg/grpc/server.go PredictStream) drains continuously and drops
	// undeliverable sends, so this backpressure is brief and bounded - and
	// pausing the diffusion loop under it is the desired behavior anyway.
	emit := func(text string) {
		if !parse {
			if text != "" {
				results <- &pb.Reply{Message: []byte(text), ChatDeltas: []*pb.ChatDelta{{Content: text}}}
			}
			return
		}
		deltas := parser.Feed(text)
		if reply := replyFromDeltas(deltas); reply != nil {
			results <- reply
		}
	}
	// onBlock guards emit (and through it the parser) against invalid UTF-8:
	// a multi-byte character split across block boundaries is held back until
	// it completes (see splitValidUTF8), so proto3 marshaling never fails.
	var carry string
	onBlock := func(block string) {
		var text string
		text, carry = splitValidUTF8(carry, block)
		emit(text)
	}

	var genErr error
	d.submit(func() {
		genErr = d.gen.generateStream(prompt, optsJSON, onBlock)
	})
	if genErr != nil {
		return genErr
	}
	if carry != "" {
		// The stream ended mid-sequence: the held-back bytes can no longer
		// complete, so flush them through the U+FFFD last resort.
		emit(sanitizeUTF8(carry))
	}
	if parse {
		if reply := replyFromDeltas(parser.Close()); reply != nil {
			results <- reply
		}
	}
	return nil
}

// replyFromDeltas wraps one batch of parsed deltas into a streaming Reply,
// or nil when the batch is empty (markers consumed, nothing emitted yet).
// Message mirrors the batch's content text so legacy chan-string consumers
// see exactly the displayed tokens.
func replyFromDeltas(deltas []*pb.ChatDelta) *pb.Reply {
	if len(deltas) == 0 {
		return nil
	}
	var content strings.Builder
	for _, delta := range deltas {
		content.WriteString(delta.GetContent())
	}
	return &pb.Reply{Message: []byte(content.String()), ChatDeltas: deltas}
}

// Predict is the legacy (string, error) signature; the gRPC server prefers
// PredictRich, this exists for non-rich callers (cloud-proxy precedent).
func (d *Dllm) Predict(opts *pb.PredictOptions) (string, error) {
	reply, err := d.PredictRich(opts)
	if err != nil {
		return "", err
	}
	return string(reply.GetMessage()), nil
}

// PredictStream is the legacy chan-string path: rich replies reduced to
// their content text. Note the inverted channel ownership - the LEGACY
// contract requires the impl to close the channel.
func (d *Dllm) PredictStream(opts *pb.PredictOptions, results chan string) error {
	defer close(results)
	richCh := make(chan *pb.Reply)
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.PredictStreamRich(opts, richCh)
		close(richCh)
	}()
	for reply := range richCh {
		if msg := reply.GetMessage(); len(msg) > 0 {
			results <- string(msg)
		}
	}
	return <-errCh
}

// TokenizeString tokenizes opts.Prompt via dllm_capi_tokenize_json (the C
// side prepends bos per the vocab) and decodes the returned id array.
func (d *Dllm) TokenizeString(opts *pb.PredictOptions) (pb.TokenizationResponse, error) {
	d.genMu.RLock()
	defer d.genMu.RUnlock()
	if d.gen == nil {
		return pb.TokenizationResponse{}, grpcerrors.ModelNotLoaded("dllm")
	}
	var out string
	var tokErr error
	d.submit(func() {
		out, tokErr = d.gen.tokenizeJSON(opts.GetPrompt())
	})
	if tokErr != nil {
		return pb.TokenizationResponse{}, tokErr
	}
	var tokens []int32
	if err := json.Unmarshal([]byte(out), &tokens); err != nil {
		return pb.TokenizationResponse{}, fmt.Errorf("dllm: decode tokenize result %q: %w", out, err)
	}
	return pb.TokenizationResponse{Length: int32(len(tokens)), Tokens: tokens}, nil
}
