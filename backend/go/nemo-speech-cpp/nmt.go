package main

import (
	"regexp"
	"runtime"
	"strings"
	"unsafe"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// pairDirective matches a leading "[src->tgt] " override.
//
// Each side is an unbounded run of two-letter segments, not one or two of them.
// Either side may also be omitted, which keeps the model-level default for it:
// resolve_tag accepts a READY pair tag in one field with the other empty
// (src/nmt/langpairs.cc:167-172), so "[->en-de]" names a pair for one request.
//
// The two rules together are what force the unbounded run. A regional code on
// its own is only two segments (pt-br, zh-cn, es-us) and would parse under a
// stricter pattern; it is the SINGLE-FIELD form of a regional pair that runs to
// three (en-zh-cn, en-zh-tw, en-es-us, en-pt-br, pt-br-en, zh-tw-en). And the
// failure is not a mis-split: a pattern too short to cover the tag does not
// match the directive at all, so the whole bracket survives into the text and
// is handed to the model as something to translate.
//
// The codes are not normalised or validated here. normalize_language_code
// lowercases and folds BCP-47 down to a supported base, and is_supported has the
// authoritative table; duplicating either would be a second source of truth that
// drifts on the next pin bump.
var pairDirective = regexp.MustCompile(`^\[\s*([a-zA-Z]{2}(?:-[a-zA-Z]{2})*)?\s*->\s*([a-zA-Z]{2}(?:-[a-zA-Z]{2})*)?\s*\]\s*`)

// translator is the NMT half of the C API, narrowed to what Predict uses.
//
// It is an interface for the same reason synthesizer and diarStream are: no
// Riva-Translate GGUF is small enough to keep in the tree, so the layer above
// the ABI (pair resolution, validation, the text array, the single-chunk stream)
// would otherwise have no test at all. A fake here scripts what the C API
// returns; it does not pretend to translate anything.
type translator interface {
	// translate returns one translation per input text, in order.
	translate(texts []string, source, target string) ([]string, error)
}

// cTranslator is the real translator, over one nemo_speech_nmt_translator.
type cTranslator struct {
	handle uintptr
}

// nmtTexts builds the `const char* const* texts` argument and returns it with
// the release the caller MUST defer.
//
// Two levels need pinning, not one. cstr pins each string's bytes, but the array
// carrying their addresses is a separate Go allocation holding uintptrs: the
// collector neither traces through it nor is obliged to leave it where it is,
// and C dereferences it for the whole call. Pinning only the strings would leave
// the array itself free to move out from under the runtime.
//
// An empty element is refused rather than passed on. cstr maps "" to NULL and
// src/nmt/c_api.cpp maps a NULL element back to "" (str_or_empty), so a blank
// text would come back as a confident translation of nothing rather than an
// error.
func nmtTexts(texts []string) ([]uintptr, func(), error) {
	pin := new(runtime.Pinner)
	// The pin is released first so that the array stops being pinned before the
	// strings it points at do.
	frees := []func(){pin.Unpin}
	release := func() {
		for _, f := range frees {
			f()
		}
	}

	if len(texts) == 0 {
		return nil, release, status.Error(codes.InvalidArgument,
			"nemo-speech-cpp: nothing to translate")
	}

	ptrs := make([]uintptr, len(texts))
	for i, t := range texts {
		if t == "" {
			return nil, release, status.Error(codes.InvalidArgument,
				"nemo-speech-cpp: nothing to translate")
		}
		p, free := cstr(t)
		frees = append(frees, free)
		ptrs[i] = p
	}
	pin.Pin(&ptrs[0])
	return ptrs, release, nil
}

func (t *cTranslator) translate(texts []string, source, target string) ([]string, error) {
	ptrs, release, err := nmtTexts(texts)
	if err != nil {
		release()
		return nil, err
	}
	defer release()

	// source and target cross as Go strings: purego NUL-terminates and copies
	// them itself for the duration of the call, and c_api.cpp deep-copies both
	// into std::string before doing anything with them.
	var result uintptr
	if st := NMTTranslate(t.handle, &ptrs[0], uint64(len(ptrs)), source, target, &result); st != 0 {
		// An unsupported language pair arrives here as INVALID_ARGUMENT
		// (src/nmt/translator.cpp throws std::invalid_argument, which
		// src/nmt/c_api.cpp's guard maps to it), which statusErrorf turns into
		// the caller-facing code rather than Internal.
		return nil, statusErrorf(st, "nemo-speech-cpp: translate: %s", NMTLastError())
	}
	defer NMTResultDestroy(result)

	count := NMTResultCount(result)
	out := make([]string, 0, count)
	for i := uint64(0); i < count; i++ {
		out = append(out, NMTResultText(result, i))
	}
	return out, nil
}

// nmtTranslatorConfig builds the create-time config.
//
// Extracted from loadNMT so its four adjacent pointer fields can be asserted
// against distinct sentinels. Backend, Model, Generation and Pool are all
// uintptr and all sit next to each other, so transposing two of them changes
// neither the struct's size nor any field's offset: the layout assertions in
// abi_test.go are blind to it, and what it produces at runtime is the backend
// config being read as the model config.
//
// Generation and Pool stay NULL, which nmt.h documents as "library defaults":
// max_new_tokens (256) and contexts (1) are create-time settings this backend
// has no option to fill them from, and PredictOptions carries no per-request
// equivalent that a create-time config could honour anyway.
//
// backend and model are pinned addresses, not Go pointers, and the caller owns
// the pins.
func nmtTranslatorConfig(backend, model uintptr) cNMTTranslatorConfig {
	return cNMTTranslatorConfig{
		Size:    unsafe.Sizeof(cNMTTranslatorConfig{}),
		Backend: backend,
		Model:   model,
	}
}

// loadNMT creates the Riva-Translate translator.
//
// This must not take engineMu: Load is its only caller and already holds it.
func (n *NemoSpeech) loadNMT(modelFile string) error {
	// nemo_speech_nmt_create deep-copies the path into a std::string
	// (src/nmt/c_api.cpp to_config, via str_or_empty) and retains no pointer
	// afterwards, so pinning for the duration of the create call is both
	// necessary and sufficient.
	var pinner runtime.Pinner
	defer pinner.Unpin()

	pathP, freePath := cstr(modelFile)
	defer freePath()

	// NCtx is left at 0, which to_config reads as "keep the default" (it applies
	// the field only when > 0) and which the runtime resolves to 1024 tokens.
	// That is sized for the sentence-length input Riva-Translate is built for,
	// and raising it costs one n_ctx-sized KV cache per pooled context, so it
	// wants a deliberate option rather than a guess made here.
	model := cNMTModelConfig{Size: unsafe.Sizeof(cNMTModelConfig{}), Path: pathP}
	// BackendConfig.gpu defaults to 0 in C++ (device 0), not to CPU, and
	// to_config assigns it unconditionally, so the option's own -1 default is
	// what keeps an unconfigured model on the CPU.
	backend := cNMTBackendConfig{Size: unsafe.Sizeof(cNMTBackendConfig{}), GPU: n.opts.gpu}

	cfg := nmtTranslatorConfig(pinPtr(&pinner, &backend), pinPtr(&pinner, &model))

	xlog.Info("nemo-speech-cpp: creating translator",
		"gpu", n.opts.gpu,
		"source_language", n.opts.sourceLanguage,
		"target_language", n.opts.targetLanguage)

	// #nosec G103 -- cfg is a local POD struct borrowed for this call only. Its
	// Backend and Model members are pinPtr addresses held by the pinner unpinned
	// on return, Model.Path is the cstr allocation freed by the defer above, and
	// nemo_speech_nmt_create deep-copies everything it reads.
	if st := NMTCreate(unsafe.Pointer(&cfg), &n.nmt); st != 0 {
		return statusErrorf(st, "nemo-speech-cpp: nmt create: %s", NMTLastError())
	}
	return nil
}

// languagePair resolves the languages for one request and returns the text to
// translate.
//
// nemo_speech_nmt_translate takes explicit source and target languages and has
// no free-form generation entry point at all, so there is no prompt in the LLM
// sense to carry an instruction. The pair therefore comes from the model
// options, and a leading "[src->tgt]" directive is the only per-request control
// Predict can offer.
func (n *NemoSpeech) languagePair(prompt string) (source, target, text string) {
	source, target = n.opts.sourceLanguage, n.opts.targetLanguage

	m := pairDirective.FindStringSubmatch(prompt)
	if m == nil {
		return source, target, strings.TrimSpace(prompt)
	}
	// An omitted side keeps the model-level default rather than blanking it.
	if m[1] != "" {
		source = m[1]
	}
	if m[2] != "" {
		target = m[2]
	}
	// The directive must not survive into the text: the runtime wraps it in a
	// chat template (src/nmt/langpairs.cc build_prompt), so anything left here is
	// translated along with the sentence.
	return source, target, strings.TrimSpace(prompt[len(m[0]):])
}

// unsupportedPredictFields names the PredictOptions fields a caller may have set
// that this C API has no way to honour, so they are logged rather than silently
// dropped.
//
// The list is deliberately narrow. Everything nemo_speech_nmt_translate accepts
// is in its five arguments: a translator, the texts, and two language codes.
// Everything else in PredictOptions is therefore unsupported, and naming all of
// it would log on every single request, because LocalAI fills the sampling
// defaults in from the model config whether or not the user asked for them.
//
// So the sampling and decoding knobs (temperature, top_p, top_k, min_p, seed,
// tokens, repeat/frequency/presence penalties, mirostat, tfz, typical_p,
// stop_prompts, prompt caching, rope scaling, n_draft, logit_bias) are ignored
// silently: there is no field for any of them on either side of the ABI.
// max_new_tokens and n_ctx exist but are CREATE-time settings on the translator,
// not per-request ones, so PredictOptions.Tokens has nowhere to go either.
//
// What is named here is the structural asks: requests that only make sense
// against a general language model, where honouring them partially would be
// worse than saying nothing at all.
func unsupportedPredictFields(opts *pb.PredictOptions) []string {
	var out []string
	if opts.GetGrammar() != "" {
		out = append(out, "grammar")
	}
	if opts.GetTools() != "" {
		out = append(out, "tools")
	}
	if len(opts.GetImages()) > 0 {
		out = append(out, "images")
	}
	if len(opts.GetVideos()) > 0 {
		out = append(out, "videos")
	}
	if len(opts.GetAudios()) > 0 {
		out = append(out, "audios")
	}
	if opts.GetNegativePrompt() != "" {
		out = append(out, "negative_prompt")
	}
	if opts.GetLogprobs() > 0 {
		out = append(out, "logprobs")
	}
	return out
}

// translateText runs one translation and returns it.
//
// The two rejections happen before anything crosses the ABI. An empty text would
// otherwise reach the runtime as a NULL element (see nmtTexts), and a missing
// target would come back as "unsupported language pair:  -> ", which names
// neither the option the operator has to set nor the request that failed.
func translateText(t translator, source, target, text string) (string, error) {
	if text == "" {
		return "", status.Error(codes.InvalidArgument,
			"nemo-speech-cpp: PredictOptions.prompt is required, it is the text to translate")
	}
	if target == "" {
		return "", status.Error(codes.InvalidArgument,
			"nemo-speech-cpp: no target language: set the target_language model option, "+
				"or prefix the prompt with a [src->tgt] directive")
	}

	out, err := t.translate([]string{text}, source, target)
	if err != nil {
		return "", err
	}
	// One text in, one translation out. A call that returned OK with none is a
	// runtime bug, and the empty string it would hand back reaches the user as a
	// successful but blank completion with nothing anywhere to say why.
	if len(out) == 0 {
		return "", status.Error(codes.Internal, "nemo-speech-cpp: translation produced no result")
	}
	return out[0], nil
}

// streamTranslation runs one translation and puts the whole of it on out as a
// single chunk.
//
// That is a limit of the C API and not a shortcut taken here.
// nemo_speech_nmt_translate has no token callback and no incremental result: it
// returns once the decode has finished, with the completed text. There is
// nothing finer to stream, and splitting the finished string into fake chunks
// would imitate progress that never happened.
//
// out is not closed here. PredictStream owns it, and closing it in one of two
// places depending on how far the request got is how a stream ends up
// half-closed.
func streamTranslation(t translator, source, target, text string, out chan<- string) error {
	translated, err := translateText(t, source, target, text)
	if err != nil {
		return err
	}
	out <- translated
	return nil
}

// resolveRequest is the shared front half of both RPCs: it names what it is
// dropping and works out the pair and the text.
func (n *NemoSpeech) resolveRequest(opts *pb.PredictOptions) (source, target, text string) {
	// Logged rather than rejected, for the reason the diarization path logs its
	// own dropped fields: a caller that asked for something extra still wants the
	// translation it can have, and a request naming a field this backend drops
	// should say so where an operator can find it.
	if dropped := unsupportedPredictFields(opts); len(dropped) > 0 {
		xlog.Warn("nemo-speech-cpp: ignoring request fields this model has no equivalent for",
			"fields", dropped)
	}
	return n.languagePair(opts.GetPrompt())
}

// Predict translates PredictOptions.Prompt.
//
// The whole body runs inside withEngine, so the family check and the C calls
// that trust the handle happen under a single acquisition of engineMu. See the
// handoff notes at the bottom of nemospeech.go: Free runs without the backend
// lock, so anything that checks the family and then releases the lock before
// calling C can have the handle destroyed underneath it.
func (n *NemoSpeech) Predict(opts *pb.PredictOptions) (string, error) {
	var out string
	if err := n.withEngine(familyNMT, func() error {
		source, target, text := n.resolveRequest(opts)
		s, err := translateText(&cTranslator{handle: n.nmt}, source, target, text)
		out = s
		return err
	}); err != nil {
		return "", err
	}
	return out, nil
}

// PredictStream translates PredictOptions.Prompt and emits the result on
// results.
//
// results is closed on EVERY path, including the family rejection and a
// validation failure, and the close is deferred outside withEngine so that a
// rejected family still closes it. This is the LEGACY streaming contract, which
// is the opposite of PredictStreamRich's: pkg/grpc/server.go:529 calls this and
// then blocks on a drain goroutine that only finishes when the channel closes,
// so a channel left open does not fail the request, it hangs the RPC and, with
// the backend lock still held, every request queued behind it. The rich variant
// is the one whose channel the host closes; this one is not.
func (n *NemoSpeech) PredictStream(opts *pb.PredictOptions, results chan string) error {
	defer close(results)

	return n.withEngine(familyNMT, func() error {
		source, target, text := n.resolveRequest(opts)
		return streamTranslation(&cTranslator{handle: n.nmt}, source, target, text, results)
	})
}
