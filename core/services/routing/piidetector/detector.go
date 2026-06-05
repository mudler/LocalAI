// Package piidetector adapts the core/backend token-classification
// wrapper to the PII redactor's pii.NERDetector seam. It lives outside
// the pii package so pii stays free of core/backend imports (the
// redactor is unit-tested with stub detectors). The dependency runs one
// way: piidetector -> {core/backend, pii}.
package piidetector

import (
	"context"
	"unicode/utf8"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	model "github.com/mudler/LocalAI/pkg/model"
)

// New builds a pii.NERDetector backed by the token-classification model
// in modelConfig. Phase 0: the Python `transformers` backend loaded with
// Type=TokenClassification; Phase 2: the GGML privacy-filter backend —
// both speak the same gRPC TokenClassify contract, so this adapter is
// unchanged across the swap. The model is resolved lazily on first
// Detect, so building a detector for a not-yet-loaded model is cheap and
// never blocks startup.
func New(loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) pii.NERDetector {
	return &nerDetector{
		classifier: backend.NewTokenClassifier(loader, modelConfig, appConfig, backend.TokenClassifyOptions{}),
		modelName:  modelConfig.Name,
	}
}

type nerDetector struct {
	classifier backend.TokenClassifier
	modelName  string
}

// Detect runs the model and maps its spans onto pii.NEREntity. Offsets
// pass through as BYTE offsets per the TokenClassify proto contract.
// Spans whose offsets fall outside the text or land off a UTF-8 rune
// boundary are dropped: a bad offset must never reach the redactor,
// which splices text[Start:End] and would otherwise corrupt output or
// panic. The redactor applies NERConfig.MinScore and the entity->action
// map itself, so we deliberately return every (validated) span here.
//
// CONTRACT NOTE: the proto defines start/end as UTF-8 byte offsets. The
// Python transformers backend converts HuggingFace's codepoint offsets to
// bytes before responding (see TokenClassify in backend.py), and the GGML
// privacy-filter backend will emit bytes natively. The boundary check
// below is defense-in-depth against a backend that regresses to codepoint
// offsets: it downgrades the bug from "corrupted redaction / panic" to
// "dropped span + warning" rather than trusting the wire blindly.
func (d *nerDetector) Detect(ctx context.Context, text string) ([]pii.NEREntity, error) {
	ents, err := d.classifier.TokenClassify(ctx, text)
	if err != nil {
		return nil, err
	}

	n := len(text)
	out := make([]pii.NEREntity, 0, len(ents))
	for _, e := range ents {
		if e.Group == "" || e.Start < 0 || e.Start >= e.End || e.End > n {
			xlog.Warn("pii NER: dropping span with invalid byte range",
				"model", d.modelName, "group", e.Group, "start", e.Start, "end", e.End, "len", n)
			continue
		}
		// text[e.Start] is safe (Start < End <= n => Start < n). End is
		// exclusive: when End < n, text[End] is the first byte past the
		// span and must itself start a rune. Off-boundary offsets are the
		// signature of codepoint-vs-byte offset confusion.
		if !utf8.RuneStart(text[e.Start]) || (e.End < n && !utf8.RuneStart(text[e.End])) {
			xlog.Warn("pii NER: dropping span off UTF-8 boundary (offset units mismatch?)",
				"model", d.modelName, "group", e.Group, "start", e.Start, "end", e.End)
			continue
		}
		out = append(out, pii.NEREntity{
			Group: e.Group,
			Start: e.Start,
			End:   e.End,
			Score: e.Score,
			Text:  e.Text,
		})
	}
	return out, nil
}
