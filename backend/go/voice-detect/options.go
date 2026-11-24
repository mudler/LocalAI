package main

import (
	"strconv"
	"strings"
)

// defaultVerifyThreshold is the cosine-distance cutoff used when a request does
// not set one. Matches the Python speaker-recognition backend's default so the
// two implementations agree on verdicts out of the box.
const defaultVerifyThreshold float32 = 0.25

// loadOptions holds the parsed model-level options for voice-detect.
type loadOptions struct {
	verifyThreshold float32
	modelName       string
}

func splitOption(o string) (key, value string, ok bool) {
	i := strings.Index(o, ":")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(o[:i]), strings.TrimSpace(o[i+1:]), true
}

// parseOptions reads the backend "key:value" option slice. Unknown keys are
// ignored. Defaults: verify_threshold 0.25, model_name derived from the file.
func parseOptions(opts []string) loadOptions {
	o := loadOptions{verifyThreshold: defaultVerifyThreshold}
	for _, oo := range opts {
		key, value, ok := splitOption(oo)
		if !ok {
			continue
		}
		switch key {
		case "verify_threshold", "threshold":
			if f, err := strconv.ParseFloat(value, 32); err == nil && f > 0 {
				o.verifyThreshold = float32(f)
			}
		case "model_name":
			o.modelName = value
		}
	}
	return o
}
