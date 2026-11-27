package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// The library rejects an unknown key in its JSON options, so this backend
// keeps its own allow-lists and builds the JSON from typed values. A
// misspelled option then fails with the name of the option in LocalAI's
// vocabulary, not as a JSON error from the library.

// Option names of the model config (options: [key:value, ...]).
const (
	optBackend     = "backend"
	optThreads     = "threads"
	optDecodeBatch = "decode_batch"
	optOverlap     = "overlap"
	optFfmpeg      = "ffmpeg"
	optNfe         = "nfe"
	optCfgScale    = "cfg_scale"
	optSeed        = "seed"
	optAudioMode   = "audio_mode"
	optMaxSeconds  = "max_seconds"
)

// Request options. They are valid in the model config, as the defaults of
// every request, and in the params map of a request.
var requestOptionNames = []string{optNfe, optCfgScale, optSeed, optAudioMode, optMaxSeconds}

// Context options. They are valid in the model config only.
var contextOptionNames = []string{optBackend, optThreads, optDecodeBatch, optOverlap, optFfmpeg}

// minNfe is the smallest value the library accepts for nfe.
const minNfe = 3

// requestOptions holds the request options that are set. A nil field is not
// set, and the library or the context default applies.
type requestOptions struct {
	nfe        *int
	cfgScale   *float64
	seed       *int
	audioMode  *string
	maxSeconds *int
}

// contextOptions holds the options of avatarforcing_load.
type contextOptions struct {
	// backend is the compute device. Empty means automatic: the backend
	// tries the GPU and falls back to the CPU.
	backend     string
	threads     int
	decodeBatch *int
	overlap     *bool
	ffmpeg      string
	request     requestOptions
}

// parseModelOptions reads the options: list of the model config. threads is
// the Threads field of the model config, used when no threads option is set.
func parseModelOptions(opts []string, threads int32) (contextOptions, error) {
	co := contextOptions{threads: int(threads)}
	for _, o := range opts {
		k, v, ok := strings.Cut(o, ":")
		if !ok {
			return co, fmt.Errorf("avatarforcing-cpp: option %q is not in key:value form", o)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case optBackend:
			b, err := parseBackend(v)
			if err != nil {
				return co, err
			}
			co.backend = b
		case optThreads:
			n, err := parseInt(k, v, 0)
			if err != nil {
				return co, err
			}
			co.threads = n
		case optDecodeBatch:
			n, err := parseInt(k, v, 0)
			if err != nil {
				return co, err
			}
			co.decodeBatch = &n
		case optOverlap:
			b, err := strconv.ParseBool(v)
			if err != nil {
				return co, fmt.Errorf("avatarforcing-cpp: option overlap must be true or false, got %q", v)
			}
			co.overlap = &b
		case optFfmpeg:
			if v == "" {
				return co, fmt.Errorf("avatarforcing-cpp: option ffmpeg is empty")
			}
			co.ffmpeg = v
		default:
			if err := co.request.set(k, v); err != nil {
				return co, err
			}
		}
	}
	return co, nil
}

// parseBackend checks the backend option. "auto" is the same as no option.
func parseBackend(v string) (string, error) {
	b := strings.ToLower(v)
	switch {
	case b == "auto":
		return "", nil
	case b == "cpu", b == "gpu", b == "cuda":
		return b, nil
	case strings.HasPrefix(b, "cuda:"):
		if _, err := strconv.Atoi(strings.TrimPrefix(b, "cuda:")); err != nil {
			return "", fmt.Errorf("avatarforcing-cpp: option backend %q: the device index is not a number", v)
		}
		return b, nil
	}
	return "", fmt.Errorf("avatarforcing-cpp: option backend must be auto, cpu, gpu, cuda or cuda:N, got %q "+
		"(only the CPU and CUDA backends are supported)", v)
}

// set parses one request option. It returns an error for an unknown key.
func (r *requestOptions) set(k, v string) error {
	switch k {
	case optNfe:
		n, err := parseInt(k, v, minNfe)
		if err != nil {
			return err
		}
		r.nfe = &n
	case optCfgScale:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("avatarforcing-cpp: option cfg_scale must be a number, got %q", v)
		}
		r.cfgScale = &f
	case optSeed:
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("avatarforcing-cpp: option seed must be an integer, got %q", v)
		}
		r.seed = &n
	case optAudioMode:
		if v != "normalized" && v != "raw" {
			return fmt.Errorf("avatarforcing-cpp: option audio_mode must be normalized or raw, got %q", v)
		}
		r.audioMode = &v
	case optMaxSeconds:
		n, err := parseInt(k, v, 0)
		if err != nil {
			return err
		}
		r.maxSeconds = &n
	default:
		return fmt.Errorf("avatarforcing-cpp: unknown option %q (accepted: %s)", k,
			strings.Join(append(append([]string{}, contextOptionNames...), requestOptionNames...), ", "))
	}
	return nil
}

// parseInt parses an integer option that must not be below min.
func parseInt(k, v string, min int) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("avatarforcing-cpp: option %s must be an integer, got %q", k, v)
	}
	if n < min {
		return 0, fmt.Errorf("avatarforcing-cpp: option %s must be %d or more, got %d", k, min, n)
	}
	return n, nil
}

// fields adds the set request options to m.
func (r requestOptions) fields(m map[string]any) {
	if r.nfe != nil {
		m[optNfe] = *r.nfe
	}
	if r.cfgScale != nil {
		m[optCfgScale] = *r.cfgScale
	}
	if r.seed != nil {
		m[optSeed] = *r.seed
	}
	if r.audioMode != nil {
		m[optAudioMode] = *r.audioMode
	}
	if r.maxSeconds != nil {
		m[optMaxSeconds] = *r.maxSeconds
	}
}

// loadJSON gives the options JSON of avatarforcing_load for a device. The
// request options of the model config go in too: the library uses them as
// the defaults of every request.
func (c contextOptions) loadJSON(device string) string {
	m := map[string]any{}
	if device != "" {
		m[optBackend] = device
	}
	if c.threads > 0 {
		m[optThreads] = c.threads
	}
	if c.decodeBatch != nil {
		m[optDecodeBatch] = *c.decodeBatch
	}
	if c.overlap != nil {
		m[optOverlap] = *c.overlap
	}
	if c.ffmpeg != "" {
		m[optFfmpeg] = c.ffmpeg
	}
	c.request.fields(m)
	return mustJSON(m)
}

// requestFromProto maps a GenerateVideoRequest to request options.
//
// step is the number of denoising steps per block. The library counts
// sampler time points (nfe), one more than the steps, so nfe = step + 1.
// cfg_scale and seed are used when they are not zero, because zero is the
// value of a field the client did not send. params can set every request
// option and wins over the proto fields.
func requestFromProto(req *pb.GenerateVideoRequest) (requestOptions, error) {
	var r requestOptions
	if s := req.GetStep(); s > 0 {
		if int(s)+1 < minNfe {
			return r, fmt.Errorf("avatarforcing-cpp: step must be %d or more, got %d", minNfe-1, s)
		}
		n := int(s) + 1
		r.nfe = &n
	}
	if c := req.GetCfgScale(); c != 0 {
		f := float64(c)
		r.cfgScale = &f
	}
	if s := req.GetSeed(); s != 0 {
		n := int(s)
		r.seed = &n
	}

	params := req.GetParams()
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		known := false
		for _, n := range requestOptionNames {
			if k == n {
				known = true
				break
			}
		}
		if !known {
			return r, fmt.Errorf("avatarforcing-cpp: unknown params key %q (accepted: %s)",
				k, strings.Join(requestOptionNames, ", "))
		}
		if err := r.set(k, strings.TrimSpace(params[k])); err != nil {
			return r, err
		}
	}
	return r, nil
}

// json gives the request JSON for the generate functions.
func (r requestOptions) json() string {
	m := map[string]any{}
	r.fields(m)
	if len(m) == 0 {
		return ""
	}
	return mustJSON(m)
}

func mustJSON(m map[string]any) string {
	b, err := json.Marshal(m)
	if err != nil {
		// A map of strings, numbers and booleans always marshals.
		panic(err)
	}
	return string(b)
}
