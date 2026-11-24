package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// Model families. Qwen runs on the Strata engine ("strata"). Hy4 is Tencent
// Hy4-Flash, which runs on its own engine program ("strata-hy4"); see hy4.go.
// Inkling is Thinking Machines Inkling 2 Mini, which runs on a third engine
// program ("strata-inkling"); see inkling.go.
// Strata v0.2.15 removed DeepSeek V4.1, so family:deepseek41 is refused at load.
const (
	familyQwen       = "qwen"
	familyHy4        = "hy4"
	familyInkling    = "inkling"
	familyDeepSeek41 = "deepseek41"
)

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
	// family is familyQwen, familyHy4 or familyInkling.
	family string

	// hy4 is set for the Hy4 family: the files of a .strata model and the
	// options of the strata-hy4 engine. Nil for every other family.
	hy4 *hy4Config

	// inkling is set for the Inkling family: the files of an Inkling 2 Mini
	// model and the options of the strata-inkling engine. Nil for every other
	// family.
	inkling *inklingConfig

	// nativePack is set when the pack is a native (IQ) pack, the kind the iq
	// pack tool makes. The engine refuses it without --native, --spec and
	// --prefill, so the backend supplies them.
	nativePack bool

	// noPackFlag leaves --pack off the engine command line: the engine path
	// of this model reads the GGUF as it is and takes only the tokenizer.
	noPackFlag bool
}

// Defaults for a native (IQ) pack. The engine needs a speculative window of at
// least 2 tokens and a prefill chunk; these are the values Strata's own setup
// writes for a CUDA or HIP card: a window of 4, and a chunk it picks itself.
// engine_args can set either flag to override them.
const (
	defaultNativeSpec    = "4"
	defaultNativePrefill = "auto"
)

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
	if c.hy4 != nil {
		return c.hy4EngineArgs()
	}
	if c.inkling != nil {
		return c.inklingEngineArgs()
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
	if c.nativePack {
		// Only what the user did not set: the engine takes the last value of a
		// flag, but a missing flag is an error, so add nothing that is there.
		if !hasEngineFlag(c.extraArgs, "--spec") {
			args = append(args, "--spec", defaultNativeSpec)
		}
		if !hasEngineFlag(c.extraArgs, "--prefill") {
			args = append(args, "--prefill", defaultNativePrefill)
		}
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
	c.family = familyQwen
	switch f := kv["family"]; f {
	case "", familyQwen:
	case familyHy4:
		c.family = familyHy4
	case familyInkling:
		c.family = familyInkling
	case familyDeepSeek41:
		return c, errors.New("strata: family:deepseek41 is not supported: Strata v0.2.15 removed DeepSeek V4.1")
	default:
		return c, fmt.Errorf("strata: family %q must be %s, %s or %s", f, familyQwen, familyHy4, familyInkling)
	}
	envName := "STRATA_ENGINE_PATH"
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

	if c.family == familyHy4 {
		return parseHy4(c, kv, opts)
	}
	if c.family == familyInkling {
		return parseInkling(c, kv, opts)
	}

	pack, dirs, err := planPack(&c, opts, kv)
	if err != nil {
		return c, err
	}
	c.pack = pack
	dirs = append([]string{pack}, dirs...)

	parseRuntime(&c, kv)
	for _, fo := range fileOptions {
		if v := kv[fo.option]; v != "" {
			p, err := resolve(v, dirs...)
			if err != nil {
				return c, fmt.Errorf("strata: %s option: %w", fo.option, err)
			}
			c.files = append(c.files, engineFile{fo.flag, p})
		}
	}
	if err := parseNativePack(&c, kv, dirs); err != nil {
		return c, err
	}
	if v := kv["ple_io"]; v != "" {
		c.extraArgs = append([]string{"--ple-io", v}, c.extraArgs...)
	}
	if kv["mmap_experts"] == "true" {
		c.extraArgs = append([]string{"--mmap-experts"}, c.extraArgs...)
	}

	if err := parseTimeouts(&c, kv); err != nil {
		return c, err
	}

	if err := parseVision(&c, kv, opts, dirs); err != nil {
		return c, err
	}
	return c, nil
}

// parseRuntime reads the options every family shares: where the Strata Python
// files are, the Python to run, the image marker and the extra engine arguments.
func parseRuntime(c *loadConfig, kv map[string]string) {
	c.strataRoot = kv["strata_root"]
	if c.strataRoot == "" {
		c.strataRoot = os.Getenv("STRATA_ROOT")
	}
	c.python = kv["python"]
	c.mediaMarker = kv["media_marker"]
	if v := kv["engine_args"]; v != "" {
		c.extraArgs = strings.Fields(v)
	}
}

// parseTimeouts reads the two timeouts and the end-of-turn ids.
func parseTimeouts(c *loadConfig, kv map[string]string) error {
	c.readyTimeout = 15 * time.Minute
	if v := kv["ready_timeout"]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("strata: bad ready_timeout %q", v)
		}
		c.readyTimeout = d
	}
	c.stopTimeout = 30 * time.Second
	if v := kv["stop_timeout"]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("strata: bad stop_timeout %q", v)
		}
		c.stopTimeout = d
	}
	if v := kv["eos_ids"]; v != "" {
		c.eos = []int{}
		for _, f := range strings.Split(v, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(f))
			if err != nil {
				return fmt.Errorf("strata: bad eos_ids entry %q", f)
			}
			c.eos = append(c.eos, n)
		}
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

// hasEngineFlag reports whether args sets flag, as "--flag value" or "--flag=value".
func hasEngineFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

var shardName = regexp.MustCompile(`^(.*)-(\d+)-of-(\d+)\.gguf$`)

// firstShard returns the first file of the split GGUF set that path belongs
// to, or path itself when it is not a split file or is already the first. The
// engine finds the other shards from the first one.
func firstShard(path string) string {
	m := shardName.FindStringSubmatch(path)
	if m == nil {
		return path
	}
	if n, err := strconv.Atoi(m[2]); err != nil || n <= 1 {
		return path
	}
	first := fmt.Sprintf("%s-%0*d-of-%s.gguf", m[1], len(m[2]), 1, m[3])
	if fileExists(first) {
		return first
	}
	return path
}

// findFirstShard looks for the one first shard of a split model in dir, for a
// pack that has no native or pack_gguf option. The vision projector is not a
// model shard.
func findFirstShard(dir string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "*-00001-of-*.gguf"))
	var found []string
	for _, m := range matches {
		if !strings.HasPrefix(strings.ToLower(filepath.Base(m)), "mmproj") {
			found = append(found, m)
		}
	}
	if len(found) == 1 {
		return found[0]
	}
	return ""
}

// parseNativePack handles a native (IQ) pack: a pack the iq pack tool made or
// will make. The engine reads its experts from the GGUF files and needs
// --native with the first shard. The shard comes from the native option (a
// later shard is replaced by the first), else from pack_gguf, else from the
// one split model in the model directory.
func parseNativePack(c *loadConfig, kv map[string]string, dirs []string) error {
	if c.noPackFlag || c.pack == "" {
		return nil
	}
	if c.build == nil && !packComplete(c.pack, packToolIQ) {
		return nil
	}
	c.nativePack = true
	for i := range c.files {
		if c.files[i].flag == "--native" {
			c.files[i].path = firstShard(c.files[i].path)
			return nil
		}
	}
	shard := ""
	if v := kv["pack_gguf"]; v != "" {
		if p, err := resolve(v, dirs...); err == nil {
			shard = firstShard(p)
		}
	}
	if shard == "" && len(dirs) > 1 {
		shard = findFirstShard(dirs[1])
	}
	if shard == "" {
		return errors.New("strata: this is a native (IQ) pack and the engine needs the model's first GGUF file; set the native option")
	}
	c.files = append([]engineFile{{"--native", shard}}, c.files...)
	return nil
}
