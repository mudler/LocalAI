package piidetector

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/LocalAI/core/services/routing/piipattern"
	"github.com/mudler/LocalAI/core/trace"
)

// NewPattern builds a pii.NERDetector that matches secrets with the restricted
// regex tier (built-ins + operator-defined patterns) instead of a neural model.
// It runs entirely in-process — no backend, GGUF, or VRAM — and the patterns
// compile once here, so an invalid pattern is reported now (the resolver fails
// closed) rather than per request. Matches are reported under their group with
// a deterministic Score of 1.0.
func NewPattern(modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (pii.NERDetector, error) {
	custom := make([]piipattern.Pattern, 0, len(modelConfig.PIIDetection.Patterns))
	for _, p := range modelConfig.PIIDetection.Patterns {
		custom = append(custom, piipattern.Pattern{Group: p.Name, Pattern: p.Match, MinLen: p.MinLen})
	}
	m, err := piipattern.NewMatcher(modelConfig.PIIDetection.Builtins, custom)
	if err != nil {
		return nil, err
	}
	return &patternDetector{matcher: m, modelName: modelConfig.Name, appConfig: appConfig}, nil
}

type patternDetector struct {
	matcher   *piipattern.Matcher
	modelName string
	appConfig *config.ApplicationConfig
}

// Detect runs the compiled patterns and maps each match onto a pii.NEREntity.
// When tracing is enabled it records a pattern_pii BackendTrace so the matches
// (group, byte range, text) show in the Traces UI alongside NER detections.
func (d *patternDetector) Detect(_ context.Context, text string) ([]pii.NEREntity, error) {
	tracing := d.appConfig != nil && d.appConfig.EnableTracing
	var start time.Time
	if tracing {
		trace.InitBackendTracingIfEnabled(d.appConfig.TracingMaxItems, d.appConfig.TracingMaxBodyBytes)
		start = time.Now()
	}

	matches := d.matcher.Find(text)
	out := make([]pii.NEREntity, 0, len(matches))
	var traceEnts []backend.TokenEntity
	for _, mt := range matches {
		out = append(out, pii.NEREntity{Group: mt.Group, Start: mt.Start, End: mt.End, Score: 1.0, Text: mt.Text})
		if tracing {
			traceEnts = append(traceEnts, backend.TokenEntity{Group: mt.Group, Start: mt.Start, End: mt.End, Score: 1.0, Text: mt.Text})
		}
	}

	if tracing {
		trace.RecordBackendTrace(patternPIITrace(d.modelName, text, traceEnts, start))
	}
	return out, nil
}

// patternPIITrace assembles the Traces-UI row for one pattern-detector run.
// Split out so the Data assembly is unit-testable without a request.
func patternPIITrace(modelName, text string, entities []backend.TokenEntity, start time.Time) trace.BackendTrace {
	return trace.BackendTrace{
		Timestamp: start,
		Duration:  time.Since(start),
		Type:      trace.BackendTracePatternPII,
		ModelName: modelName,
		Backend:   "pattern",
		Summary:   trace.TruncateString(text, 200),
		Data: map[string]any{
			"input_chars": len(text),
			"matches":     len(entities),
			"entities":    entities,
		},
	}
}
