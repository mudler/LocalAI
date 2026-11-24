package main

// Thinking Machines Inkling 2 Mini. Strata runs it on its own engine program,
// strata-inkling, which is neither the program that runs the Qwen models nor
// strata-hy4. Facts this file relies on (all from the Strata sources at the
// pinned commit and the engine's own usage text):
//
//   - strata-inkling speaks the same `--serve` line protocol as the other
//     engines (GEN, T, DONE, STOP, QUIT) and refuses a flag it does not know,
//     so the Qwen engine's flags are never sent to it.
//   - The model is one file: a Strata build (`Inkling-2-Mini-SQ-10.strata`,
//     `-SQ-15` or `-SQ-20`) or an Unsloth GGUF (`Inkling-2-Mini-UD-Q4_K_XL.gguf`
//     and the other UD quants). There is no pack directory. `--native <file>
//     --max-context <n>` is the start line. A picture reader is a separate file
//     (`Inkling-2-Mini-Vision.strata`, `--vision`), and a GGUF model has a
//     separate DSpark drafter file (`dspark-Inkling-2-Mini-Q8_0.gguf`,
//     `--dspark`); a Strata build carries its drafter inside.
//   - `strata-inkling --info <file> [--vision <file>] [--dspark <file>]`
//     prints one line (arch=inkling2_loop ... drafter=dspark images=1 ...)
//     with what this engine can do. The drafter and the vision file are asked
//     for only when it says so.
//   - The tokenizer (o200k byte-level BPE) and the chat template come from the
//     Section 0 manifest of a Strata build, or from the tokenizer.json and
//     chat_template.jinja files Strata publishes beside the builds. A GGUF has
//     only the second source. Strata's own inkling_tokenizer.py turns either
//     into the tokenizer directory the helper reads.
//   - Pictures are files the engine reads itself: one `image=<path>` GEN key
//     per picture, in prompt order, and one image placeholder in the prompt per
//     picture. There is no separate encoder program and no GENI request.
//   - The model has no thinking mode. Its chat template ends at the last
//     message, so the prompt gets an explicit answer opener (Strata's
//     inkling_dialect.finish_prompt). Its output is a stream of channel tokens
//     (<|content_text|>, <|content_invoke_tool_json|>, ...) that Strata's
//     InklingOutputParser turns into content and tool calls.
//   - The engine does not return logprobs and takes no logit bias.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/xlog"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

const (
	inklingVisionFile    = "Inkling-2-Mini-Vision.strata"
	inklingDrafterFile   = "dspark-Inkling-2-Mini-Q8_0.gguf"
	inklingTokenizerFile = "tokenizer.json"
	inklingTemplateFile  = "chat_template.jinja"

	// inklingDefaultContext is the context Strata starts Inkling 2 Mini with
	// when the model config names none. The model reads up to 1M tokens; the
	// KV cache grows with the context, so the default stays small.
	inklingDefaultContext = 32768

	// inklingImageText is what the chat template writes for each picture: the
	// image channel token and the image pad token. The engine expands the pad.
	inklingImageText = "<|content_image|><|unused_200054|>"

	inklingInfoTimeout = time.Minute
)

// inklingEOSTexts are the tokens that end an Inkling 2 Mini reply: the end of
// sampling token (the config's eos) and the end of text token.
var inklingEOSTexts = []string{"<|content_model_end_sampling|>", "<|endoftext|>"}

// defaultInklingEOSIDs are those two tokens' ids, for a tokenizer that cannot
// name them.
var defaultInklingEOSIDs = []int{200006, 199999}

// inklingConfig is what the Inkling family needs beyond the shared load options.
type inklingConfig struct {
	native  string // the model file: a .strata build or a .gguf
	vision  string // the vision file, or empty
	drafter string // the DSpark drafter GGUF (GGUF models only), or empty

	tokenizerJSON string // tokenizer.json beside the model, or empty
	chatTemplate  string // chat_template.jinja beside the model, or empty

	wantDrafter bool   // drafter option: on unless "off"
	vramExperts string // --vram-experts-gb, or empty
	ramExperts  string // --ram-experts-gb, or empty

	// Set from the engine's own answer to --info, after parsing.
	useDrafter bool
	useVision  bool
}

func (h *inklingConfig) isGGUF() bool {
	return strings.EqualFold(filepath.Ext(h.native), ".gguf")
}

// parseInkling reads the options of an Inkling 2 Mini model. It finds no pack:
// the model is the files named below, looked up in the model directory.
func parseInkling(c loadConfig, kv map[string]string, opts *pb.ModelOptions) (loadConfig, error) {
	dir := opts.GetModelFile()
	if dir == "" {
		dir = filepath.Join(opts.GetModelPath(), opts.GetModel())
	}
	dirs := []string{dir, opts.GetModelPath(), filepath.Dir(dir)}

	parseRuntime(&c, kv)
	if err := parseTimeouts(&c, kv); err != nil {
		return c, err
	}
	if c.ctxSize <= 0 {
		c.ctxSize = inklingDefaultContext
	}

	h := &inklingConfig{}
	if kv["native"] == "" {
		return c, errors.New("strata: family:inkling needs the native option: the model file, for example native:Inkling-2-Mini-SQ-10.strata")
	}
	native, err := resolve(kv["native"], dirs...)
	if err != nil {
		return c, fmt.Errorf("strata: native option: %w", err)
	}
	h.native = native
	// The other files sit next to the model file, or in the model directory.
	near := append([]string{filepath.Dir(native)}, dirs...)

	switch v := kv["vision"]; v {
	case "off":
	case "", "on":
		p, rerr := resolve(inklingVisionFile, near...)
		switch {
		case rerr == nil:
			h.vision = p
		case v == "on":
			return c, fmt.Errorf("strata: vision is on but the vision file is missing: %w", rerr)
		}
	default:
		p, rerr := resolve(v, near...)
		if rerr != nil {
			return c, fmt.Errorf("strata: vision option: %w", rerr)
		}
		h.vision = p
	}

	switch v := kv["drafter"]; v {
	case "", "auto", "dspark":
		h.wantDrafter = true
	case "off":
	default:
		return c, fmt.Errorf("strata: drafter %q must be dspark, auto or off", v)
	}
	// A GGUF model has no drafter inside: the drafter is a file. Without one a
	// GGUF model starts without speculative decoding.
	if h.wantDrafter && h.isGGUF() {
		name := kv["dspark"]
		if name == "" {
			name = inklingDrafterFile
		}
		p, rerr := resolve(name, near...)
		switch {
		case rerr == nil:
			h.drafter = p
		case kv["dspark"] != "":
			return c, fmt.Errorf("strata: dspark option: %w", rerr)
		default:
			h.wantDrafter = false
			xlog.Info("strata: no DSpark drafter file next to the GGUF model, so it starts without speculative decoding", "file", inklingDrafterFile)
		}
	}

	for _, o := range []struct {
		name string
		dst  *string
	}{{"tokenizer_json", &h.tokenizerJSON}, {"chat_template", &h.chatTemplate}} {
		name := kv[o.name]
		explicit := name != ""
		if !explicit {
			name = map[string]string{"tokenizer_json": inklingTokenizerFile, "chat_template": inklingTemplateFile}[o.name]
		}
		p, rerr := resolve(name, near...)
		switch {
		case rerr == nil:
			*o.dst = p
		case explicit:
			return c, fmt.Errorf("strata: %s option: %w", o.name, rerr)
		}
	}

	for _, o := range []struct {
		name string
		dst  *string
	}{{"vram_experts_gb", &h.vramExperts}, {"ram_experts_gb", &h.ramExperts}} {
		v := kv[o.name]
		if v == "" {
			continue
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 {
			return c, fmt.Errorf("strata: bad %s %q", o.name, v)
		}
		*o.dst = strconv.FormatFloat(f, 'f', -1, 64)
	}

	// The launcher picks the engine package by this variable; a user who runs
	// strata-inkling directly through engine_path is not affected by it.
	hasFamily := false
	for _, e := range c.env {
		hasFamily = hasFamily || strings.HasPrefix(e, engineFamilyEnv+"=")
	}
	if !hasFamily {
		c.env = append(c.env, engineFamilyEnv+"="+familyInkling)
	}

	c.inkling = h
	return c, nil
}

// inklingEngineArgs is the command line before --serve. Engine arguments given
// by the user come last so they can override what the options chose.
func (c loadConfig) inklingEngineArgs() []string {
	h := c.inkling
	args := []string{"--native", h.native, "--max-context", strconv.Itoa(c.ctxSize)}
	if h.useVision && h.vision != "" {
		args = append(args, "--vision", h.vision)
	}
	if h.drafter != "" {
		args = append(args, "--dspark", h.drafter)
	}
	if h.vramExperts != "" {
		args = append(args, "--vram-experts-gb", h.vramExperts)
	}
	if h.ramExperts != "" {
		args = append(args, "--ram-experts-gb", h.ramExperts)
	}
	if c.mainGPU != "" {
		args = append(args, "--gpu", c.mainGPU)
	}
	if !hasFlag(c.extraArgs, "--draft") {
		switch {
		case h.useDrafter:
			args = append(args, "--draft", "dspark")
		case !h.wantDrafter:
			args = append(args, "--draft", "off")
		}
	}
	return append(args, c.extraArgs...)
}

// infoArgs is the argument list of `--info`: the model file, and the vision
// and drafter files the engine is asked about.
func (h *inklingConfig) infoArgs() []string {
	args := []string{"--info", h.native}
	if h.vision != "" {
		args = append(args, "--vision", h.vision)
	}
	if h.drafter != "" {
		args = append(args, "--dspark", h.drafter)
	}
	return args
}

// applyInfo decides, from what the engine says it can do, whether to ask for
// the drafter and the vision file. A nil info (the engine could not be asked)
// means neither. It returns the reasons something was left out.
func (h *inklingConfig) applyInfo(info map[string]string) (notes []string) {
	h.useDrafter = h.wantDrafter && info["drafter"] == "dspark"
	h.useVision = h.vision != "" && info["images"] == "1"
	if h.wantDrafter && !h.useDrafter {
		notes = append(notes, "this engine has no drafter (DSpark) for this model, so it starts without speculative decoding")
	}
	if h.vision != "" && !h.useVision {
		notes = append(notes, "this engine does not read images, so it starts without the vision file")
	}
	return notes
}

// probeInklingInfo runs `<engine> --info <model> [--vision v] [--dspark d]`.
// The launcher passes the arguments on, so enginePath may be the launcher.
func probeInklingInfo(enginePath string, args []string, env []string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), inklingInfoTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, enginePath, args...) // #nosec G204 -- absolute regular file from the engine_path option, arguments built here, no shell
	cmd.Env = append(os.Environ(), env...)
	setChildAttrs(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s --info: %w", filepath.Base(enginePath), err)
	}
	info := parseHy4Info(string(out))
	if info == nil {
		return nil, fmt.Errorf("%s --info printed no arch= line", filepath.Base(enginePath))
	}
	return info, nil
}

// --- tokenizer ---------------------------------------------------------------

// inklingManifest keeps the parts of a Strata build's manifest the tokenizer
// tool reads. The tokenizer itself may be missing here: Strata then reads the
// tokenizer.json published beside the build. ok is false when the manifest has
// no chat template, so the template file must be used.
func inklingManifest(section0 []byte) (manifest []byte, hasTokenizer, ok bool, err error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(section0, &all); err != nil {
		return nil, false, false, fmt.Errorf("the manifest is not JSON: %w", err)
	}
	keep := map[string]json.RawMessage{}
	for _, k := range []string{"config", "tokenizer_json", "chat_template"} {
		if v, found := all[k]; found {
			keep[k] = v
		}
	}
	tpl, found := keep["chat_template"]
	var text string
	if found && json.Unmarshal(tpl, &text) == nil && text != "" {
		ok = true
	}
	_, hasTokenizer = keep["tokenizer_json"]
	if hasTokenizer && string(keep["tokenizer_json"]) == "null" {
		hasTokenizer = false
		delete(keep, "tokenizer_json")
	}
	manifest, err = json.Marshal(keep)
	return manifest, hasTokenizer, ok, err
}

// inklingTokenizerScript turns the model's tokenizer sources into the
// tokenizer directory with Strata's own tool. Arguments: the Strata source
// directory, the output directory, then either "manifest" (the trimmed
// manifest on stdin; an optional tokenizer.json path fills a missing
// tokenizer) or "files" (tokenizer.json and chat_template.jinja paths).
const inklingTokenizerScript = `import json, sys
sys.path.insert(0, sys.argv[1] + "/tools")
import inkling_tokenizer
out = sys.argv[2] + "/tokenizer"
if sys.argv[3] == "manifest":
    cfg = inkling_tokenizer.convert_manifest(json.load(sys.stdin), out, tokenizer_json_path=(sys.argv[4] or None))
else:
    cfg = inkling_tokenizer.convert_files(sys.argv[4], sys.argv[5], out)
print("tokenizer: vocab %d, merges %d" % (cfg["vocab_size"], cfg["n_merges"]))
`

// inklingTokenizerJob asks for the tokenizer directory of one model file.
type inklingTokenizerJob struct {
	native        string
	tokenizerJSON string // may be empty
	chatTemplate  string // may be empty
	pack          string // the directory that gets a tokenizer/ subdirectory
	python        string
	strataRoot    string
	timeout       time.Duration
}

// inklingPackDir is where the tokenizer of a model file is kept: next to it,
// named after it.
func inklingPackDir(native string) string {
	return hy4PackDir(native)
}

// inklingStamp says which files a tokenizer directory was made from: their
// names and sizes. A replaced file makes the directory stale.
func inklingStamp(j inklingTokenizerJob) (string, error) {
	var b strings.Builder
	for _, p := range []string{j.native, j.tokenizerJSON, j.chatTemplate} {
		if p == "" {
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s %d\n", filepath.Base(p), st.Size())
	}
	return b.String(), nil
}

// inklingScriptArgs decides how the tokenizer is made and returns the script
// arguments after the output directory, and the manifest for stdin (nil for
// the files mode). A Strata build's own manifest comes first, as in Strata's
// own setup; the files published beside the builds fill what it lacks, and
// are the only source for a GGUF.
func inklingScriptArgs(j inklingTokenizerJob) (args []string, stdin []byte, err error) {
	files := j.tokenizerJSON != "" && j.chatTemplate != ""
	if !strings.EqualFold(filepath.Ext(j.native), ".gguf") {
		section0, rerr := readStrataSection0(j.native)
		if rerr != nil {
			return nil, nil, rerr
		}
		manifest, hasTokenizer, ok, merr := inklingManifest(section0)
		if merr != nil {
			return nil, nil, merr
		}
		if ok && (hasTokenizer || j.tokenizerJSON != "") {
			path := ""
			if !hasTokenizer {
				path = j.tokenizerJSON
			}
			return []string{"manifest", path}, manifest, nil
		}
	}
	if files {
		return []string{"files", j.tokenizerJSON, j.chatTemplate}, nil, nil
	}
	return nil, nil, fmt.Errorf("no tokenizer: put %s and %s next to the model file (Strata publishes them beside its builds), or use a Strata build that carries them", inklingTokenizerFile, inklingTemplateFile)
}

var inklingTokenizerFiles = hy4TokenizerFiles

func inklingTokenizerComplete(pack, stamp string) bool {
	for _, f := range inklingTokenizerFiles {
		if !fileExists(filepath.Join(pack, "tokenizer", f)) {
			return false
		}
	}
	b, err := os.ReadFile(filepath.Join(pack, "source.txt")) // #nosec G304 -- a file this backend wrote next to the model
	return err == nil && string(b) == stamp
}

// prepareInklingTokenizer makes the tokenizer directory when it is not there
// yet. It builds in a staging directory that is renamed when it is done, so a
// stop part-way leaves nothing that looks finished.
func prepareInklingTokenizer(j inklingTokenizerJob) error {
	stamp, err := inklingStamp(j)
	if err != nil {
		return err
	}
	if inklingTokenizerComplete(j.pack, stamp) {
		return nil
	}
	script := filepath.Join(j.strataRoot, "tools", "inkling_tokenizer.py")
	if !fileExists(script) {
		return fmt.Errorf("the tokenizer tool %s is missing: this backend image carries an older Strata source tree, or strata_root points at a tree without it", script)
	}
	scriptArgs, stdin, err := inklingScriptArgs(j)
	if err != nil {
		return err
	}
	python, err := resolveInterpreter(j.python)
	if err != nil {
		return fmt.Errorf("python: %w", err)
	}

	stage := j.pack + ".partial"
	if err := os.RemoveAll(stage); err != nil { // an earlier stop; only this backend makes this name
		return err
	}
	if err := os.MkdirAll(stage, 0o750); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(stage)
		}
	}()

	timeout := j.timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	argv := append([]string{"-I", "-c", inklingTokenizerScript, j.strataRoot, stage}, scriptArgs...)
	cmd := exec.CommandContext(ctx, python, argv...) // #nosec G204 -- interpreter from the python option, fixed script, paths as argv
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
	var tail bytes.Buffer
	cmd.Stdout, cmd.Stderr = &tail, &tail
	xlog.Info("strata: making the Inkling 2 Mini tokenizer (first load only)", "model", filepath.Base(j.native), "from", scriptArgs[0])
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the tokenizer tool failed: %w\n%s", err, lastLines(tail.String(), 20))
	}
	if err := os.WriteFile(filepath.Join(stage, "source.txt"), []byte(stamp), 0o600); err != nil {
		return err
	}
	if !inklingTokenizerComplete(stage, stamp) {
		return fmt.Errorf("the tokenizer tool ended without a finished tokenizer in %s\n%s", stage, lastLines(tail.String(), 20))
	}
	if err := os.RemoveAll(j.pack); err != nil { // a stale one
		return err
	}
	if err := os.Rename(stage, j.pack); err != nil {
		return err
	}
	ok = true
	return nil
}
