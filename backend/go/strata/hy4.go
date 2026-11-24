package main

// Tencent Hy4-Flash. Strata runs it on its own engine program, strata-hy4,
// which is not the program that runs the Qwen models. Facts this file relies on
// (all from the Strata sources at the pinned commit):
//
//   - strata-hy4 speaks the same `--serve` line protocol as the other engine
//     (GEN, T, DONE, STOP, QUIT), with a short list of flags and a stricter
//     parser: it refuses a flag or a GEN key it does not know, so the Qwen
//     engine's flags are never sent to it.
//   - The model is one `.strata` file (SQ-64, SQ-96 or SQ-128) plus the Engram
//     file every build shares, plus an optional vision file. There is no pack
//     directory. `--native <build> --engram <file> --max-context <n>
//     [--vision <file>] [--draft dfly2]` is the whole start line, and a card
//     with little memory may also pass `--vram-experts-gb` and
//     `--ram-experts-gb`.
//   - `strata-hy4 --info <build>` prints one line (arch=hy4flash drafter=dfly2
//     images=1 ...) with what this engine can do. The drafter (DFly2
//     speculative decoding) and the vision file are asked for only when it
//     says so; an engine that cannot do one starts without it.
//   - The tokenizer and the chat template are inside the build file, in
//     Section 0: a zstd-compressed JSON manifest at the end of the file. The
//     backend reads it and has Strata's own hy4_tokenizer.py turn it into the
//     tokenizer directory the helper reads.
//   - Pictures are files the engine reads itself: one `image=<path>` GEN key
//     per picture, in prompt order, and one image placeholder in the prompt per
//     picture. The engine expands the placeholder. There is no separate
//     encoder program and no GENI request.
//   - The model writes reasoning and tool calls as tagged tokens
//     (`<think:opensource>`, `<tool_calls:opensource>`, ...). Strata's own
//     serve/hy4_dialect.py translates them for the output parser; the helper
//     uses the same parser class the Strata server uses for this template.
//   - The engine does not return logprobs and takes no logit bias.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/mudler/xlog"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

const (
	// engineFamilyEnv makes the strata-engine launcher pick the Hy4 engine
	// package (engines-hy4/) and its program instead of the Qwen one.
	engineFamilyEnv = "STRATA_ENGINE_FAMILY"

	hy4EngramFile = "Hy4-Flash-Engram-Q8.strata"
	hy4VisionFile = "Hy4-Flash-Vision.strata"

	// hy4DefaultContext is the context Strata starts Hy4-Flash with when the
	// model config names none.
	hy4DefaultContext = 131072

	// The image placeholder the chat template writes for each picture.
	hy4ImageStartText = "<｜hy_image▁start:opensource｜>"
	hy4ImagePadText   = "<｜hy_image▁pad:opensource｜>"
	hy4ImageEndText   = "<｜hy_image▁end:opensource｜>"

	hy4InfoTimeout = time.Minute
	// maxSection0Bytes bounds the manifest read from a model file.
	maxSection0Bytes = 512 << 20
)

// hy4EOSTexts are the tokens that end a Hy4-Flash reply besides the tokenizer's
// own end token: <hy_end> (the config's eos) and <hy_EOT>.
var hy4EOSTexts = []string{"<｜hy_end:opensource｜>", "<｜hy_EOT｜>"}

// defaultHy4EOSIDs are those two tokens' ids, for a tokenizer that cannot name them.
var defaultHy4EOSIDs = []int{120025, 120008}

// hy4Config is what the Hy4 family needs beyond the shared load options.
type hy4Config struct {
	native string // the build file, Hy4-Flash-SQ-*.strata
	engram string // the shared Engram file
	vision string // the vision file, or empty

	wantDrafter bool   // drafter option: on unless "off"
	vramExperts string // --vram-experts-gb, or empty
	ramExperts  string // --ram-experts-gb, or empty

	// Set from the engine's own answer to --info, after parsing.
	useDrafter bool
	useVision  bool
}

// parseHy4 reads the options of a Hy4-Flash model. It finds no pack: the model
// is the files named below, looked up in the model directory.
func parseHy4(c loadConfig, kv map[string]string, opts *pb.ModelOptions) (loadConfig, error) {
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
		c.ctxSize = hy4DefaultContext
	}

	h := &hy4Config{}
	if v := kv["native"]; v == "" {
		return c, errors.New("strata: family:hy4 needs the native option: the model file, for example native:Hy4-Flash-SQ-64.strata")
	}
	native, err := resolve(kv["native"], dirs...)
	if err != nil {
		return c, fmt.Errorf("strata: native option: %w", err)
	}
	h.native = native
	// The other files sit next to the build file, or in the model directory.
	near := append([]string{filepath.Dir(native)}, dirs...)

	engram := kv["engram"]
	if engram == "" {
		engram = hy4EngramFile
	}
	if h.engram, err = resolve(engram, near...); err != nil {
		return c, fmt.Errorf("strata: Engram file (engram option, default %s): %w", hy4EngramFile, err)
	}

	switch v := kv["vision"]; v {
	case "off":
	case "", "on":
		name := hy4VisionFile
		p, rerr := resolve(name, near...)
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
	case "", "auto", "dfly2":
		h.wantDrafter = true
	case "off":
	default:
		return c, fmt.Errorf("strata: drafter %q must be dfly2, auto or off", v)
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
	// strata-hy4 directly through engine_path is not affected by it.
	hasFamily := false
	for _, e := range c.env {
		hasFamily = hasFamily || strings.HasPrefix(e, engineFamilyEnv+"=")
	}
	if !hasFamily {
		c.env = append(c.env, engineFamilyEnv+"="+familyHy4)
	}

	c.hy4 = h
	return c, nil
}

// hy4EngineArgs is the command line before --serve. Engine arguments given by
// the user come last so they can override what the options chose.
func (c loadConfig) hy4EngineArgs() []string {
	h := c.hy4
	args := []string{"--native", h.native, "--engram", h.engram, "--max-context", strconv.Itoa(c.ctxSize)}
	if h.useVision && h.vision != "" {
		args = append(args, "--vision", h.vision)
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
	if h.useDrafter && !hasFlag(c.extraArgs, "--draft") {
		args = append(args, "--draft", "dfly2")
	}
	return append(args, c.extraArgs...)
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// parseHy4Info reads the line `strata-hy4 --info` prints: key=value words that
// start with arch=. It returns nil when there is no such line.
func parseHy4Info(out string) map[string]string {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "arch=") {
			continue
		}
		info := map[string]string{}
		for _, w := range strings.Fields(line) {
			if k, v, ok := strings.Cut(w, "="); ok {
				info[k] = v
			}
		}
		return info
	}
	return nil
}

// applyInfo decides, from what the engine says it can do, whether to ask for
// the drafter and the vision file. A nil info (the engine could not be asked)
// means neither. It returns the reasons something was left out.
func (h *hy4Config) applyInfo(info map[string]string) (notes []string) {
	h.useDrafter = h.wantDrafter && info["drafter"] == "dfly2"
	h.useVision = h.vision != "" && info["images"] == "1"
	if h.wantDrafter && !h.useDrafter {
		notes = append(notes, "this engine has no drafter (DFly2), so it starts without speculative decoding")
	}
	if h.vision != "" && !h.useVision {
		notes = append(notes, "this engine does not read images, so it starts without the vision file")
	}
	return notes
}

// probeHy4Info runs `<engine> --info <build file>`. The launcher passes the
// arguments on, so enginePath may be the launcher.
func probeHy4Info(enginePath, native string, env []string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), hy4InfoTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, enginePath, "--info", native) // #nosec G204 -- absolute regular file from the engine_path option, no shell
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

// --- tokenizer from the model file -------------------------------------------

// strataMagic starts every .strata file.
var strataMagic = []byte("STRATA\x00\x01")

// readStrataSection0 returns the decompressed manifest (Section 0) of a
// .strata file. The header holds, at byte 16, the offset and the length of the
// compressed manifest as two little-endian 64-bit numbers.
func readStrataSection0(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- a model file the user configured
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var head [32]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return nil, fmt.Errorf("%s: header: %w", filepath.Base(path), err)
	}
	if !bytes.Equal(head[:8], strataMagic) {
		return nil, fmt.Errorf("%s is not a .strata file (no STRATA magic)", filepath.Base(path))
	}
	off, length := binary.LittleEndian.Uint64(head[16:24]), binary.LittleEndian.Uint64(head[24:32])
	size := uint64(st.Size()) // #nosec G115 -- a file size is not negative
	if length == 0 || length > maxSection0Bytes || off > size || length > size-off {
		return nil, fmt.Errorf("%s: the manifest (offset %d, length %d) is outside the file (%d bytes): the file is cut short", filepath.Base(path), off, length, size)
	}
	raw := make([]byte, length)
	if _, err := f.ReadAt(raw, int64(off)); err != nil { // #nosec G115 -- bounded above
		return nil, fmt.Errorf("%s: manifest: %w", filepath.Base(path), err)
	}
	dec, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(maxSection0Bytes*2))
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	out, err := dec.DecodeAll(raw, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: manifest: %w", filepath.Base(path), err)
	}
	return out, nil
}

// tokenizerManifest keeps the parts of the manifest the tokenizer tool reads.
// Everything else (tensor tables) is dropped before it goes to Python.
func tokenizerManifest(section0 []byte) ([]byte, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(section0, &all); err != nil {
		return nil, fmt.Errorf("the manifest is not JSON: %w", err)
	}
	keep := map[string]json.RawMessage{}
	for _, k := range []string{"config", "tokenizer_json", "chat_template"} {
		v, ok := all[k]
		if !ok {
			return nil, fmt.Errorf("the manifest has no %q", k)
		}
		keep[k] = v
	}
	return json.Marshal(keep)
}

// hy4TokenizerScript turns the trimmed manifest on stdin into the tokenizer
// directory with Strata's own tool.
const hy4TokenizerScript = `import json, sys
sys.path.insert(0, sys.argv[1] + "/tools")
import hy4_tokenizer
cfg = hy4_tokenizer.convert_manifest(json.load(sys.stdin), sys.argv[2] + "/tokenizer")
print("tokenizer: vocab %d, merges %d" % (cfg["vocab_size"], cfg["n_merges"]))
`

// hy4TokenizerJob asks for the tokenizer directory of one model file.
type hy4TokenizerJob struct {
	native     string
	pack       string // the directory that gets a tokenizer/ subdirectory
	python     string
	strataRoot string
	timeout    time.Duration
}

// hy4PackDir is where the tokenizer of a build file is kept: next to it, named
// after it, so builds that share a directory do not share a tokenizer.
func hy4PackDir(native string) string {
	return filepath.Join(filepath.Dir(native), strings.TrimSuffix(filepath.Base(native), filepath.Ext(native))+".tokenizer")
}

// hy4Stamp says which file a tokenizer directory was made from: its name and
// size. A replaced model file makes the directory stale.
func hy4Stamp(native string) (string, error) {
	st, err := os.Stat(native)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %d\n", filepath.Base(native), st.Size()), nil
}

var hy4TokenizerFiles = []string{"vocab.json", "merges.txt", "token_type.json", "tokenizer.json", "chat_template.jinja"}

func hy4TokenizerComplete(pack, stamp string) bool {
	for _, f := range hy4TokenizerFiles {
		if !fileExists(filepath.Join(pack, "tokenizer", f)) {
			return false
		}
	}
	b, err := os.ReadFile(filepath.Join(pack, "source.txt")) // #nosec G304 -- a file this backend wrote next to the model
	return err == nil && string(b) == stamp
}

// prepareHy4Tokenizer makes the tokenizer directory from the model file when
// it is not there yet. It builds in a staging directory that is renamed when
// it is done, so a stop part-way leaves nothing that looks finished.
func prepareHy4Tokenizer(j hy4TokenizerJob) error {
	stamp, err := hy4Stamp(j.native)
	if err != nil {
		return err
	}
	if hy4TokenizerComplete(j.pack, stamp) {
		return nil
	}
	script := filepath.Join(j.strataRoot, "tools", "hy4_tokenizer.py")
	if !fileExists(script) {
		return fmt.Errorf("the tokenizer tool %s is missing: this backend image carries an older Strata source tree, or strata_root points at a tree without it", script)
	}
	section0, err := readStrataSection0(j.native)
	if err != nil {
		return err
	}
	manifest, err := tokenizerManifest(section0)
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
	cmd := exec.CommandContext(ctx, python, "-I", "-c", hy4TokenizerScript, j.strataRoot, stage) // #nosec G204 -- interpreter from the python option, fixed script, paths as argv
	cmd.Stdin = bytes.NewReader(manifest)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
	var tail bytes.Buffer
	cmd.Stdout, cmd.Stderr = &tail, &tail
	xlog.Info("strata: making the Hy4-Flash tokenizer from the model file (first load only)", "model", filepath.Base(j.native))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the tokenizer tool failed: %w\n%s", err, lastLines(tail.String(), 20))
	}
	if err := os.WriteFile(filepath.Join(stage, "source.txt"), []byte(stamp), 0o600); err != nil {
		return err
	}
	if !hy4TokenizerComplete(stage, stamp) {
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

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// --- pictures ------------------------------------------------------------------

// encodeHy4Path writes a path as the protocol's image= value, which ends at the
// next space: a space is %20 and a percent sign is %25.
func encodeHy4Path(p string) string {
	return strings.ReplaceAll(strings.ReplaceAll(p, "%", "%25"), " ", "%20")
}

// hy4ImageKeys writes each picture to a file the engine can read and returns
// the GEN keys that name them, in prompt order. release removes the files; the
// engine has read them by the end of the request.
func hy4ImageKeys(sources []string) (keys []string, release func(), err error) {
	return directImageKeys("strata-hy4-images-", sources)
}

// directImageKeys is hy4ImageKeys for any engine that reads picture files
// itself; dirPrefix names the temporary directory.
func directImageKeys(dirPrefix string, sources []string) (keys []string, release func(), err error) {
	if len(sources) == 0 {
		return nil, func() {}, nil
	}
	dir, err := os.MkdirTemp("", dirPrefix)
	if err != nil {
		return nil, nil, err
	}
	release = func() { _ = os.RemoveAll(dir) }
	for i, src := range sources {
		data, err := loadImage(src)
		if err == nil {
			data, err = normalizeImage(data)
		}
		if err != nil {
			release()
			return nil, nil, fmt.Errorf("image %d: %w", i+1, err)
		}
		sum := sha256.Sum256(data)
		p := filepath.Join(dir, fmt.Sprintf("%s-%d%s", hex.EncodeToString(sum[:12]), i, imageExt(data)))
		if err := os.WriteFile(p, data, 0o600); err != nil {
			release()
			return nil, nil, err
		}
		keys = append(keys, "image="+encodeHy4Path(p))
	}
	return keys, release, nil
}

func imageExt(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return ".png"
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return ".jpg"
	case bytes.HasPrefix(data, []byte("BM")):
		return ".bmp"
	case bytes.HasPrefix(data, []byte("GIF")):
		return ".gif"
	}
	return ".img"
}
