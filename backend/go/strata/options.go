package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// Default end-of-turn ids of the Qwen-family models Strata serves
// (<|endoftext|>, <|im_end|>), used when the tokenizer cannot name them. The
// engine reports these as ordinary tokens followed by a "stop" finish, so the
// backend drops them from the output. Override with the eos_ids option.
var defaultEOSIDs = []int{248044, 248046}

// loadConfig is the parsed form of ModelOptions.
type loadConfig struct {
	enginePath   string
	pack         string
	strataRoot   string
	python       string
	extraArgs    []string
	env          []string
	readyTimeout time.Duration
	stopTimeout  time.Duration
	eos          []int // nil: ask the tokenizer
	ctxSize      int
	mainGPU      string
	mediaMarker  string

	// Files the engine reads next to the pack, as engine flag -> path.
	files []engineFile
	// vision is nil when the model has no image support configured.
	vision *visionConfig

	// build is set when the pack does not exist yet and must be made from the
	// GGUF files before the engine starts (the pack_gguf option).
	build *packBuild
	// noPackFlag leaves --pack off the engine command line: the engine path
	// of this model reads the GGUF as it is and takes only the tokenizer.
	noPackFlag bool
}

type engineFile struct{ flag, path string }

// fileOptions maps a model option to the engine flag that takes a file.
var fileOptions = []struct{ option, flag string }{
	{"native", "--native"},
	{"native_dense_gguf", "--native-dense-gguf"},
	{"ple_gguf", "--ple-gguf"},
	{"embd_gguf", "--embd-gguf"},
	{"expert_profile", "--expert-profile"},
	{"mtp", "--mtp"},
}

// engineArgs is the command line before --serve. User arguments come last so
// they can override what the options chose.
func (c loadConfig) engineArgs() []string {
	var args []string
	if !c.noPackFlag {
		args = append(args, "--pack", c.pack)
	}
	for _, f := range c.files {
		args = append(args, f.flag, f.path)
	}
	if c.ctxSize > 0 {
		args = append(args, "--max-context", strconv.Itoa(c.ctxSize))
	}
	if c.mainGPU != "" {
		args = append(args, "--gpu", c.mainGPU)
	}
	if c.vision != nil {
		args = append(args, "--vision")
	}
	return append(args, c.extraArgs...)
}

// resolve finds a file named by an option: absolute, or relative to the pack
// directory, the models directory, or the pack's parent.
func resolve(p string, dirs ...string) (string, error) {
	if filepath.IsAbs(p) {
		if _, err := os.Stat(p); err != nil { // #nosec G703 -- existence check of a path from the model config; nothing is read or written
			return "", err
		}
		return p, nil
	}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		c := filepath.Join(d, p)
		if _, err := os.Stat(c); err == nil { // #nosec G703 -- existence check under the pack and models directories; nothing is read or written
			return c, nil
		}
	}
	return "", fmt.Errorf("%q not found in %s", p, strings.Join(dirs, ", "))
}

func findPack(opts *pb.ModelOptions, kv map[string]string) (pack string, dirs []string, err error) {
	dir := opts.GetModelFile()
	if dir == "" {
		dir = filepath.Join(opts.GetModelPath(), opts.GetModel())
	}
	dirs = []string{dir, opts.GetModelPath(), filepath.Dir(dir)}
	cands := []string{dir}
	if v := kv["pack"]; v != "" {
		p, rerr := resolve(v, dirs...)
		if rerr != nil {
			return "", nil, fmt.Errorf("strata: pack option: %w", rerr)
		}
		cands = []string{p}
	} else {
		cands = append(cands, filepath.Join(dir, "pack"))
	}
	for _, c := range cands {
		if _, serr := os.Stat(filepath.Join(c, "tokenizer", "vocab.json")); serr == nil {
			return c, dirs, nil
		}
	}
	return "", nil, fmt.Errorf("strata: %q is not a Strata pack directory (tokenizer/vocab.json missing)", dir)
}

// planPack finds the pack, or plans its build when the model config names the
// GGUF files to build it from (pack_gguf) and no finished pack exists. The
// build itself runs in Load. build_pack:false turns the build off.
func planPack(c *loadConfig, opts *pb.ModelOptions, kv map[string]string) (string, []string, error) {
	src := kv["pack_gguf"]
	if src == "" || kv["build_pack"] == "false" {
		return findPack(opts, kv)
	}
	if kv["pack"] != "" {
		return "", nil, errors.New("strata: pack_gguf builds the pack in the model directory; do not set the pack option with it")
	}
	tool := kv["pack_tool"]
	switch tool {
	case "":
		tool = packToolIQ
	case packToolIQ, packToolTokenizer:
	default:
		return "", nil, fmt.Errorf("strata: pack_tool %q must be %s or %s", tool, packToolIQ, packToolTokenizer)
	}
	c.noPackFlag = tool == packToolTokenizer

	dir := opts.GetModelFile()
	if dir == "" {
		dir = filepath.Join(opts.GetModelPath(), opts.GetModel())
	}
	dirs := []string{dir, opts.GetModelPath(), filepath.Dir(dir)}
	dest := filepath.Join(dir, "pack")
	if packComplete(dest, tool) {
		return dest, dirs, nil
	}
	gguf, err := resolve(src, dirs...)
	if err != nil {
		return "", nil, fmt.Errorf("strata: pack_gguf option: %w", err)
	}
	b := &packBuild{tool: tool, gguf: gguf, dest: dest, python: kv["python"], args: strings.Fields(kv["pack_args"]), timeout: defaultPackTimeout}
	if b.python == "" {
		b.python = "python3"
	}
	if v := kv["pack_timeout"]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return "", nil, fmt.Errorf("strata: bad pack_timeout %q", v)
		}
		b.timeout = d
	}
	c.build = b
	return dest, dirs, nil
}

// parseLoad reads the model options and checks the files exist. Load must
// refuse models it cannot serve: with no explicit backend, LocalAI probes
// every backend with the model and binds to the first Load that succeeds.
func parseLoad(opts *pb.ModelOptions) (loadConfig, error) {
	c := loadConfig{ctxSize: int(opts.GetContextSize()), mainGPU: strings.TrimSpace(opts.GetMainGPU())}
	kv := map[string]string{}
	for _, o := range opts.GetOptions() {
		k, v, _ := strings.Cut(o, ":")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "env" {
			if !strings.Contains(v, "=") {
				return c, fmt.Errorf("strata: env option %q must be KEY=VALUE", v)
			}
			c.env = append(c.env, v)
			continue
		}
		kv[k] = v
	}
	for k, v := range opts.GetEnvVars() {
		c.env = append(c.env, k+"="+v)
	}
	c.enginePath = kv["engine_path"]
	if c.enginePath == "" {
		c.enginePath = os.Getenv("STRATA_ENGINE_PATH")
	}
	if c.enginePath == "" {
		return c, errors.New("strata: no engine executable configured; set the engine_path option (or STRATA_ENGINE_PATH)")
	}
	enginePath, err := cleanExecutable(c.enginePath)
	if err != nil {
		return c, fmt.Errorf("strata: engine_path %q is not an absolute path to an executable file", c.enginePath)
	}
	c.enginePath = enginePath

	pack, dirs, err := planPack(&c, opts, kv)
	if err != nil {
		return c, err
	}
	c.pack = pack
	dirs = append([]string{pack}, dirs...)

	c.strataRoot = kv["strata_root"]
	if c.strataRoot == "" {
		c.strataRoot = os.Getenv("STRATA_ROOT")
	}
	c.python = kv["python"]
	c.mediaMarker = kv["media_marker"]
	if v := kv["engine_args"]; v != "" {
		c.extraArgs = strings.Fields(v)
	}
	for _, fo := range fileOptions {
		if v := kv[fo.option]; v != "" {
			p, err := resolve(v, dirs...)
			if err != nil {
				return c, fmt.Errorf("strata: %s option: %w", fo.option, err)
			}
			c.files = append(c.files, engineFile{fo.flag, p})
		}
	}
	if v := kv["ple_io"]; v != "" {
		c.extraArgs = append([]string{"--ple-io", v}, c.extraArgs...)
	}
	if kv["mmap_experts"] == "true" {
		c.extraArgs = append([]string{"--mmap-experts"}, c.extraArgs...)
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
		c.eos = []int{}
		for _, f := range strings.Split(v, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(f))
			if err != nil {
				return c, fmt.Errorf("strata: bad eos_ids entry %q", f)
			}
			c.eos = append(c.eos, n)
		}
	}

	if err := parseVision(&c, kv, opts, dirs); err != nil {
		return c, err
	}
	return c, nil
}

// parseVision enables images when the model has an mmproj file and the
// strata-vision program is known.
func parseVision(c *loadConfig, kv map[string]string, opts *pb.ModelOptions, dirs []string) error {
	mm := kv["mmproj"]
	if mm == "" {
		mm = opts.GetMMProj()
	}
	exe := kv["vision_exe"]
	if exe == "" {
		exe = os.Getenv("STRATA_VISION_PATH")
	}
	if exe == "" {
		// The engine package ships the encoder next to the engine.
		if cand := filepath.Join(filepath.Dir(c.enginePath), "strata-vision"); fileExists(cand) {
			exe = cand
		}
	}
	if mm == "" || kv["vision"] == "off" {
		return nil
	}
	if exe == "" {
		if kv["vision"] == "on" {
			return errors.New("strata: vision is on but no strata-vision program is configured (vision_exe option, or STRATA_VISION_PATH)")
		}
		return nil
	}
	mmPath, err := resolve(mm, dirs...)
	if err != nil {
		return fmt.Errorf("strata: mmproj: %w", err)
	}
	if !filepath.IsAbs(exe) {
		if exe, err = resolve(exe, dirs...); err != nil {
			return fmt.Errorf("strata: vision_exe: %w", err)
		}
	}
	// The encoder reads the text model's vocabulary only: any shard that has it.
	text := ""
	for _, f := range c.files {
		if f.flag == "--native" || f.flag == "--native-dense-gguf" {
			text = f.path
		}
	}
	if text == "" {
		return errors.New("strata: images need the native option (the text model's GGUF, first shard)")
	}
	v := &visionConfig{Exe: exe, MMProj: mmPath, TextModel: text, GPU: kv["vision_gpu"] == "true", Timeout: 2 * time.Minute}
	if s := kv["vision_threads"]; s != "" {
		if v.Threads, err = strconv.Atoi(s); err != nil {
			return fmt.Errorf("strata: bad vision_threads %q", s)
		}
	} else if t := int(opts.GetThreads()); t > 0 {
		v.Threads = t
	}
	if s := kv["vision_max_tokens"]; s != "" {
		if v.MaxTokens, err = strconv.Atoi(s); err != nil {
			return fmt.Errorf("strata: bad vision_max_tokens %q", s)
		}
	}
	c.vision = v
	return nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
