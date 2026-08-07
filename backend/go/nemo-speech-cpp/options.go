package main

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mudler/xlog"
)

// loadOptions holds the parsed model-level options. Path fields are resolved
// against ModelOptions.ModelPath at parse time so every consumer sees an
// absolute path.
type loadOptions struct {
	// ASR
	vadModel     string
	pncModel     string
	diarModel    string
	itnDir       string
	languageCode string

	// TTS
	codecModel   string
	tokenizerDir string
	tnDir        string

	// NMT
	sourceLanguage string
	targetLanguage string

	// gpu is the device index passed to the runtime's backend config.
	// -1 selects CPU, matching the C API's own sentinel.
	gpu int32
}

// splitOption splits on the FIRST colon so values may themselves contain one.
func splitOption(o string) (key, value string, ok bool) {
	i := strings.Index(o, ":")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(o[:i]), strings.TrimSpace(o[i+1:]), true
}

// resolve makes a relative asset path absolute against the models directory.
// Empty stays empty so callers can test for "unset".
func resolve(base, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}

// parseOptions reads the backend "key:value" option slice. Unknown keys are
// ignored rather than rejected, so a config written for a newer backend still
// loads on an older one.
func parseOptions(opts []string, modelPath string) loadOptions {
	o := loadOptions{gpu: -1}
	for _, oo := range opts {
		key, value, ok := splitOption(oo)
		if !ok {
			continue
		}
		switch key {
		case "vad_model":
			o.vadModel = resolve(modelPath, value)
		case "pnc_model":
			o.pncModel = resolve(modelPath, value)
		case "diar_model":
			o.diarModel = resolve(modelPath, value)
		case "itn_dir":
			o.itnDir = resolve(modelPath, value)
		case "language_code":
			o.languageCode = value
		case "codec_model":
			o.codecModel = resolve(modelPath, value)
		case "tokenizer_dir":
			o.tokenizerDir = resolve(modelPath, value)
		case "tn_dir":
			o.tnDir = resolve(modelPath, value)
		case "source_language":
			o.sourceLanguage = value
		case "target_language":
			o.targetLanguage = value
		case "gpu":
			// An unknown key is ignored for forward compatibility, but a known key
			// with an unparseable value is a typo, and this one fails expensively:
			// the model still loads and still produces correct output, just on CPU
			// and far slower, with nothing anywhere to say why.
			n, err := strconv.ParseInt(value, 10, 32)
			if err != nil {
				xlog.Warn("nemo-speech-cpp: ignoring unparseable option value, falling back to CPU",
					"key", key, "value", value)
				continue
			}
			o.gpu = int32(n)
		}
	}
	return o
}
