package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// The word filter of libparakeet (the "guard"): an opt-in pass over a finished
// decode that drops words which stand alone or sit among low-confidence words,
// as noise tends to give, and words that are only punctuation. It is off unless
// one of these model options is set:
//
//	guard_min_local_conf:0.5     0 to 1; 0 = off
//	guard_local_radius:5         seconds > 0; the library default is 5
//	guard_drop_punct_only:true
//
// The names carry the guard_ prefix because the bare library names
// (min_local_conf, local_radius) say nothing next to vad_*, speaker_* and the
// other options of this backend, and the library's result calls the section
// "guard".

// parseGuardOptions reads the guard_* model options and returns the JSON
// options object of parakeet_capi_transcribe_path_json_with (the keys the
// library names min_local_conf, local_radius and drop_punct_only), or "" when
// none is set. A value that does not parse or is out of range fails the load,
// and so does the filter on a libparakeet.so that cannot run it.
func parseGuardOptions(opts *pb.ModelOptions) (string, error) {
	obj := map[string]any{}
	if raw := optString(opts, "guard_min_local_conf"); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return "", fmt.Errorf("parakeet-cpp: option guard_min_local_conf: %q is not a number", raw)
		}
		if v < 0 || v > 1 {
			return "", fmt.Errorf("parakeet-cpp: option guard_min_local_conf: %v is out of range, want 0 to 1", v)
		}
		obj["min_local_conf"] = v
	}
	if raw := optString(opts, "guard_local_radius"); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return "", fmt.Errorf("parakeet-cpp: option guard_local_radius: %q is not a number", raw)
		}
		if v <= 0 {
			return "", fmt.Errorf("parakeet-cpp: option guard_local_radius: %v is out of range, want seconds > 0", v)
		}
		obj["local_radius"] = v
	}
	if optString(opts, "guard_drop_punct_only") != "" {
		b, err := optBool(opts, "guard_drop_punct_only", false)
		if err != nil {
			return "", err
		}
		obj["drop_punct_only"] = b
	}
	if len(obj) == 0 {
		return "", nil
	}
	if CppTranscribePathJSONWith == nil {
		return "", errors.New("parakeet-cpp: the guard_* options need a libparakeet.so with parakeet_capi_transcribe_path_json_with; rebuild the backend against a newer parakeet.cpp")
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// mergeJSONObjects joins two flat JSON objects ("" is an empty one). The
// segmenter entry point takes the VAD keys and the guard keys in one object.
func mergeJSONObjects(a, b string) (string, error) {
	if a == "" {
		return b, nil
	}
	if b == "" {
		return a, nil
	}
	m := map[string]json.RawMessage{}
	for _, s := range []string{a, b} {
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return "", err
		}
	}
	out, err := json.Marshal(m)
	return string(out), err
}
