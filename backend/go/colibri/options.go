package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

const (
	defaultStartTimeout = 10 * time.Minute
	defaultStopTimeout  = 15 * time.Second
	defaultModelID      = "colibri"
)

// loadConfig is everything needed to start `coli serve` again after a crash.
type loadConfig struct {
	python   string // interpreter that runs coli; empty runs coli directly
	coliPath string // the coli program of the colibri release

	modelDir string // the colibri model directory that is served
	gguf     string // a GGUF to import into modelDir first; empty when none

	modelID string
	args    []string // extra `coli serve` flags
	env     []string // extra environment, KEY=VALUE

	startTimeout time.Duration
	stopTimeout  time.Duration
}

// serveArgs is the command line of `coli serve`, without host, port and key.
func (c loadConfig) serveArgs() []string {
	a := []string{"serve", "--model", c.modelDir, "--model-id", c.modelID}
	return append(a, c.args...)
}

// command returns the program and the arguments that run coli with args.
func (c loadConfig) command(args ...string) (string, []string) {
	if c.python == "" {
		return c.coliPath, args
	}
	return c.python, append([]string{c.coliPath}, args...)
}

// optionFlags maps a model option to the `coli serve` flag it sets. All take a
// whole number except the ones listed in stringFlags.
var optionFlags = map[string]string{
	"kv_slots":      "--kv-slots",
	"parallel":      "--kv-slots",
	"n_parallel":    "--kv-slots",
	"ram":           "--ram",
	"cap":           "--cap",
	"vram":          "--vram",
	"repin":         "--repin",
	"max_queue":     "--max-queue",
	"queue_timeout": "--queue-timeout",
	"ngen":          "--ngen",
	"gpu":           "--gpu",
	"policy":        "--policy",
	"effort":        "--effort",
}

var stringFlags = map[string]bool{"gpu": true, "policy": true, "effort": true}

var allowedStrings = map[string][]string{
	"policy": {"quality", "balanced", "experimental-fast"},
	"effort": {"minimal", "low", "medium", "high", "xhigh"},
}

var splitGGUF = regexp.MustCompile(`-00001-of-\d{5}\.gguf$`)

// parseLoad turns the LoadModel request into a start configuration. It
// refuses a model it cannot serve, so a model config without an explicit
// backend is not captured by colibri when LocalAI probes the backends.
func parseLoad(opts *pb.ModelOptions) (loadConfig, error) {
	c := loadConfig{
		modelID:      defaultModelID,
		startTimeout: defaultStartTimeout,
		stopTimeout:  defaultStopTimeout,
	}

	kv := map[string]string{}
	for _, o := range opts.GetOptions() {
		k, v, ok := strings.Cut(o, ":")
		if !ok {
			k, v = o, "true"
		}
		kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}

	if err := resolveProgram(&c, kv); err != nil {
		return c, err
	}
	if err := resolveModel(&c, opts, kv); err != nil {
		return c, err
	}

	if n := opts.GetContextSize(); n > 0 {
		c.args = append(c.args, "--ctx", strconv.Itoa(int(n)))
	}

	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := map[string]string{}
	var think *bool
	for _, k := range keys {
		v := kv[k]
		switch k {
		case "coli_path", "python", "import_dir":
			continue // read above
		case "model_id":
			if v == "" {
				return c, errors.New("colibri: model_id must not be empty")
			}
			c.modelID = v
			continue
		case "think":
			b, err := strconv.ParseBool(v)
			if err != nil {
				return c, fmt.Errorf("colibri: think must be true or false, got %q", v)
			}
			think = &b
			continue
		case "start_timeout", "stop_timeout":
			d, err := parseSeconds(v)
			if err != nil {
				return c, fmt.Errorf("colibri: %s: %w", k, err)
			}
			if k == "start_timeout" {
				c.startTimeout = d
			} else {
				c.stopTimeout = d
			}
			continue
		}
		flag, ok := optionFlags[k]
		if !ok {
			xlog.Warn("colibri: ignoring unknown model option", "option", k)
			continue
		}
		if prev, dup := seen[flag]; dup {
			return c, fmt.Errorf("colibri: options %s and %s both set %s", prev, k, flag)
		}
		seen[flag] = k
		if stringFlags[k] {
			if allowed, ok := allowedStrings[k]; ok && !contains(allowed, v) {
				return c, fmt.Errorf("colibri: %s must be one of %s, got %q", k, strings.Join(allowed, ", "), v)
			}
			if v == "" {
				return c, fmt.Errorf("colibri: %s must not be empty", k)
			}
		} else if n, err := strconv.Atoi(v); err != nil || n < 0 {
			return c, fmt.Errorf("colibri: %s must be a whole number >= 0, got %q", k, v)
		}
		c.args = append(c.args, flag, v)
	}
	if think != nil {
		if *think {
			c.args = append(c.args, "--think")
		} else {
			c.args = append(c.args, "--no-think")
		}
	}

	// Threads: colibri sizes its own OpenMP team unless OMP_NUM_THREADS is
	// set. LocalAI always sends a thread count, so it wins unless the model
	// config sets OMP_NUM_THREADS itself.
	envKeys := make([]string, 0, len(opts.GetEnvVars()))
	for k := range opts.GetEnvVars() {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		c.env = append(c.env, k+"="+opts.GetEnvVars()[k])
	}
	if t := opts.GetThreads(); t > 0 {
		if _, set := opts.GetEnvVars()["OMP_NUM_THREADS"]; !set {
			c.env = append(c.env, "OMP_NUM_THREADS="+strconv.Itoa(int(t)))
		}
	}
	return c, nil
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func parseSeconds(v string) (time.Duration, error) {
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("want seconds (or a Go duration) above zero, got %q", v)
	}
	return time.Duration(n) * time.Second, nil
}

// resolveProgram finds coli and the Python that runs it. The backend image
// sets COLIBRI_COLI and COLIBRI_PYTHON in run.sh; the model options
// coli_path and python override them. python:none runs coli directly.
func resolveProgram(c *loadConfig, kv map[string]string) error {
	coli := kv["coli_path"]
	if coli == "" {
		coli = os.Getenv("COLIBRI_COLI")
	}
	if coli == "" {
		p, err := exec.LookPath("coli")
		if err != nil {
			return errors.New("colibri: cannot find the coli program: set COLIBRI_COLI or the coli_path model option")
		}
		coli = p
	}
	if _, err := os.Stat(coli); err != nil {
		return fmt.Errorf("colibri: coli program %s: %w", coli, err)
	}
	c.coliPath = coli

	py, set := kv["python"]
	if !set {
		py = os.Getenv("COLIBRI_PYTHON")
	}
	switch py {
	case "none":
		c.python = ""
		return nil
	case "":
		py = "python3"
	}
	p, err := exec.LookPath(py)
	if err != nil {
		return fmt.Errorf("colibri: coli serve needs Python 3, and %q was not found: %w", py, err)
	}
	c.python = p
	return nil
}

// resolveModel accepts a colibri model directory (it has a config.json) or a
// GGUF file. A GGUF is imported once with `coli import` into a sibling
// directory named after it (<stem>.colibri), the same place coli import
// itself chooses; the import_dir option names another one.
func resolveModel(c *loadConfig, opts *pb.ModelOptions, kv map[string]string) error {
	p := opts.GetModelFile()
	if p == "" {
		p = opts.GetModel()
		if p != "" && !filepath.IsAbs(p) && opts.GetModelPath() != "" {
			p = filepath.Join(opts.GetModelPath(), p)
		}
	}
	if p == "" {
		return errors.New("colibri: no model given")
	}
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("colibri: model %s: %w", p, err)
	}
	if st.IsDir() {
		if !fileExists(filepath.Join(p, "config.json")) {
			return fmt.Errorf("colibri: %s is not a colibri model directory (no config.json); give a .gguf file or a directory made by `coli import`", p)
		}
		c.modelDir = p
		return nil
	}
	if !strings.HasSuffix(strings.ToLower(p), ".gguf") {
		return fmt.Errorf("colibri: %s is neither a .gguf file nor a colibri model directory", p)
	}
	out := kv["import_dir"]
	if out == "" {
		stem := splitGGUF.ReplaceAllString(filepath.Base(p), "")
		stem = strings.TrimSuffix(stem, filepath.Ext(stem))
		out = filepath.Join(filepath.Dir(p), stem+".colibri")
	} else if !filepath.IsAbs(out) && opts.GetModelPath() != "" {
		out = filepath.Join(opts.GetModelPath(), out)
	}
	c.modelDir = out
	if !fileExists(filepath.Join(out, "config.json")) {
		c.gguf = p
	}
	return nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
