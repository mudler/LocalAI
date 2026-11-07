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

// Model families. The Qwen engine and the DeepSeek V4.1 engine speak the same
// line protocol but take different flags, a different tokenizer and a
// different pack, so the model config names the family.
const (
	familyQwen       = "qwen"
	familyDeepSeek41 = "deepseek41"
)

// defaultDeepSeekEOS is the end-of-sentence id of the DeepSeek V4.1 tokenizer,
// which is also the engine's own --eos default.
var defaultDeepSeekEOS = []int{1}

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
	// family is familyQwen or familyDeepSeek41.
	family string
	// experts and dspark are the DeepSeek engine's --experts and --dspark
	// values; empty experts leaves the engine's own choice.
	experts     string
	dspark      string
	mmapExperts string

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
	if c.family == familyDeepSeek41 {
		return c.deepSeekArgs()
	}
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

// deepSeekArgs is the command line of the DeepSeek V4.1 engine before --serve.
// The engine reads the pack and, through the pack, the GGUF files; the Qwen
// file flags do not exist on it. Pictures are off: this backend does not send
// them, and the engine would keep its vision tower in memory for nothing.
func (c loadConfig) deepSeekArgs() []string {
	args := []string{"--pack", c.pack}
	for _, f := range c.files {
		args = append(args, f.flag, f.path)
	}
	if c.ctxSize > 0 {
		args = append(args, "--max-context", strconv.Itoa(c.ctxSize))
	}
	if c.mainGPU != "" {
		args = append(args, "--gpus", c.mainGPU)
	}
	args = append(args, "--vision", "off", "--dspark", c.dspark)
	if c.experts != "" {
		args = append(args, "--experts", c.experts)
	}
	if c.mmapExperts != "" {
		args = append(args, "--mmap-experts", c.mmapExperts)
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
	if c.family == familyDeepSeek41 {
		if tool != "" && tool != packToolDSV41 {
			return "", nil, fmt.Errorf("strata: pack_tool %q is for the Qwen engine; a deepseek41 model uses %s", tool, packToolDSV41)
		}
		tool = packToolDSV41
	}
	switch tool {
	case "":
		tool = packToolIQ
	case packToolIQ, packToolTokenizer, packToolDSV41:
		if tool == packToolDSV41 && c.family != familyDeepSeek41 {
			return "", nil, fmt.Errorf("strata: pack_tool %s needs the option family:%s", packToolDSV41, familyDeepSeek41)
		}
	default:
		return "", nil, fmt.Errorf("strata: pack_tool %q must be %s, %s or %s", tool, packToolIQ, packToolTokenizer, packToolDSV41)
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
	c.family = familyQwen
	switch f := kv["family"]; f {
	case "", familyQwen:
	case familyDeepSeek41:
		c.family = f
		if err := parseDeepSeek(&c, kv); err != nil {
			return c, err
		}
	default:
		return c, fmt.Errorf("strata: family %q must be %s or %s", f, familyQwen, familyDeepSeek41)
	}
	envName := "STRATA_ENGINE_PATH"
	if c.family == familyDeepSeek41 {
		// The DeepSeek engine is a separate program. STRATA_ENGINE_PATH names
		// the Qwen engine and must not be picked up for it.
		envName = "STRATA_DSV41_ENGINE_PATH"
	}
	c.enginePath = kv["engine_path"]
	if c.enginePath == "" {
		c.enginePath = os.Getenv(envName)
	}
	if c.enginePath == "" {
		return c, fmt.Errorf("strata: no engine executable configured; set the engine_path option (or %s)", envName)
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
			if c.family == familyDeepSeek41 && fo.option != "expert_profile" {
				return c, fmt.Errorf("strata: the %s option is for the Qwen engine; a deepseek41 pack holds the file list", fo.option)
			}
			p, err := resolve(v, dirs...)
			if err != nil {
				return c, fmt.Errorf("strata: %s option: %w", fo.option, err)
			}
			c.files = append(c.files, engineFile{fo.flag, p})
		}
	}
	if c.family == familyDeepSeek41 {
		if kv["ple_io"] != "" {
			return c, errors.New("strata: the ple_io option is for the Qwen engine")
		}
	} else {
		if v := kv["ple_io"]; v != "" {
			c.extraArgs = append([]string{"--ple-io", v}, c.extraArgs...)
		}
		if kv["mmap_experts"] == "true" {
			c.extraArgs = append([]string{"--mmap-experts"}, c.extraArgs...)
		}
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

	if c.family == familyDeepSeek41 {
		// The engine has its own picture path (raw RGB in the GENI file). This
		// backend does not implement it, so a config that asks for the Qwen
		// vision tower is wrong, not just unused.
		if kv["mmproj"] != "" || opts.GetMMProj() != "" || kv["vision_exe"] != "" {
			return c, errors.New("strata: images are not supported with family:deepseek41; remove mmproj and vision_exe")
		}
		return c, nil
	}
	if err := parseVision(&c, kv, opts, dirs); err != nil {
		return c, err
	}
	return c, nil
}

// parseDeepSeek reads the options only the DeepSeek V4.1 engine has.
func parseDeepSeek(c *loadConfig, kv map[string]string) error {
	c.dspark = "auto"
	if v := kv["dspark"]; v != "" {
		switch v {
		case "auto", "on", "off", "none":
			c.dspark = v
		default:
			return fmt.Errorf("strata: dspark %q must be auto, on, off or none", v)
		}
	}
	if v := kv["experts"]; v != "" {
		if n, err := strconv.Atoi(v); v != "auto" && (err != nil || (n != 5 && n != 6)) {
			return fmt.Errorf("strata: experts %q must be auto, 5 or 6", v)
		}
		c.experts = v
	}
	switch v := kv["mmap_experts"]; v {
	case "":
	case "true", "on":
		c.mmapExperts = "on"
	case "false", "off":
		c.mmapExperts = "off"
	case "auto":
		c.mmapExperts = "auto"
	default:
		return fmt.Errorf("strata: mmap_experts %q must be true, false or auto", v)
	}
	return nil
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
