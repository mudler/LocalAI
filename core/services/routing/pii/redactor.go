package pii

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/mudler/xlog"
)

// rawHit is one detection before overlap-merging. Lifted to file scope so
// the NER collector and the merge/emit step can share it.
type rawHit struct {
	patternID string
	action    Action
	start     int
	end       int
	score     float32
}

// Redactor is a stateless handle for the PII subsystem. The regex tier
// was removed: detection is driven entirely by per-model NER detectors
// (see RedactNER), whose policy lives on each detector model's
// pii_detection config. The type is retained (zero-field) as the
// on/off sentinel the application wiring and middleware gate on, so a
// nil *Redactor still means "PII subsystem unavailable".
type Redactor struct{}

// RedactNER runs every configured NER detector over text, unions their
// detections, and emits one redacted output. Each NERConfig carries its
// own detector and policy (min score, entity→action map, default
// action), so a consuming model that references several detector models
// gets each model's policy applied to its own hits before the overlap
// merge (block > mask > allow) resolves any span two detectors both
// claim.
//
// Any detector error is returned alongside a best-effort Result built
// from the detectors that did succeed, so the caller can fail closed
// (refuse the request) while still seeing what the healthy detectors
// found. Configs with a nil Detector are skipped.
//
// Package-level (no Redactor state): both the in-band request middleware
// and the MITM request path call it with their own resolved []NERConfig.
func RedactNER(ctx context.Context, text string, cfgs []NERConfig) (Result, error) {
	if text == "" || len(cfgs) == 0 {
		return Result{Redacted: text}, nil
	}
	var hits []rawHit
	var firstErr error
	for _, cfg := range cfgs {
		if cfg.Detector == nil {
			continue
		}
		h, err := collectNERHits(ctx, text, cfg)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		hits = append(hits, h...)
	}
	return mergeAndEmit(text, hits), firstErr
}

// segmentSeparator joins per-message texts into the single document
// RedactNERSegments scans. Two newlines read as a paragraph break to the
// NER encoder — neutral, in-distribution context — and never carry PII
// themselves, so a detected span landing on the separator can only be the
// fringe of an entity that started in a real segment.
const segmentSeparator = "\n\n"

// RedactNERSegments scans texts as ONE concatenated document and maps the
// detections back to one Result per input text. Scanning the segments
// together is what gives the NER tier conversational context: whether
// "jdoe_42" is a USERNAME or "4421" is a PIN is decided by the question
// asked in the *previous* message, and a bidirectional encoder only sees
// that context if both messages are in the same forward pass. (Measured on
// privacy-filter-multilingual: "4421" alone detects nothing; preceded by
// "What are the last four digits of your card?" it detects PIN at 0.726.)
//
// Span offsets in each Result are local to its text, so callers rewrite
// fields in place exactly as with per-text RedactNER. A hit that crosses a
// segment boundary is split and each fragment keeps the hit's action —
// conservative, and only possible for an entity the model stretched across
// the separator. Error semantics mirror RedactNER: best-effort results
// plus the first detector error, so callers can fail closed.
func RedactNERSegments(ctx context.Context, texts []string, cfgs []NERConfig) ([]Result, error) {
	results := make([]Result, len(texts))
	if len(texts) == 0 || len(cfgs) == 0 {
		for i := range results {
			results[i] = Result{Redacted: texts[i]}
		}
		return results, nil
	}

	var joined strings.Builder
	starts := make([]int, len(texts))
	ends := make([]int, len(texts))
	for i, t := range texts {
		if i > 0 {
			joined.WriteString(segmentSeparator)
		}
		starts[i] = joined.Len()
		joined.WriteString(t)
		ends[i] = joined.Len()
	}
	doc := joined.String()

	var hits []rawHit
	var firstErr error
	for _, cfg := range cfgs {
		if cfg.Detector == nil {
			continue
		}
		h, err := collectNERHits(ctx, doc, cfg)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		hits = append(hits, h...)
	}

	perSegment := make([][]rawHit, len(texts))
	for _, h := range hits {
		for i := range texts {
			s := max(h.start, starts[i])
			e := min(h.end, ends[i])
			if s >= e {
				continue
			}
			local := h
			local.start = s - starts[i]
			local.end = e - starts[i]
			perSegment[i] = append(perSegment[i], local)
		}
	}
	for i := range texts {
		results[i] = mergeAndEmit(texts[i], perSegment[i])
	}
	return results, firstErr
}

// collectNERHits invokes the configured NERDetector and converts each
// returned entity into a rawHit using the NERConfig's action map.
// Entities below MinScore or with no resolved action are dropped — the
// detector doesn't know which entity groups the admin cares about, so
// the policy filters here.
func collectNERHits(ctx context.Context, text string, cfg NERConfig) ([]rawHit, error) {
	if cfg.Detector == nil || text == "" {
		return nil, nil
	}
	entities, err := cfg.Detector.Detect(ctx, text)
	if err != nil {
		return nil, err
	}
	var hits []rawHit
	for _, e := range entities {
		// One DEBUG line per raw detection with the model's confidence, the
		// byte range, the matched substring, and the policy decision. This is
		// the lowest-level view of why a request was masked/blocked — e.g. a
		// phone number scored as SSN — and answers "what was in that range and
		// how sure was the model" without re-running the detector. DEBUG-gated
		// because the matched value is sensitive.
		if e.Score < cfg.MinScore {
			xlog.Debug("pii/ner: detection dropped (below min score)",
				"group", e.Group, "score", e.Score, "min_score", cfg.MinScore,
				"start", e.Start, "end", e.End, "text", e.Text)
			continue
		}
		action, ok := cfg.ResolveAction(e.Group)
		if !ok {
			xlog.Debug("pii/ner: detection ignored (no action for group)",
				"group", e.Group, "score", e.Score,
				"start", e.Start, "end", e.End, "text", e.Text)
			continue
		}
		if e.Start < 0 || e.End <= e.Start || e.End > len(text) {
			// Defensive: the backend should return byte offsets into the
			// original text, but a misconfigured model could produce
			// garbage. Skip rather than panic on slice OOB.
			xlog.Warn("pii/ner: detection has out-of-range offsets; skipping",
				"group", e.Group, "start", e.Start, "end", e.End, "text_len", len(text))
			continue
		}
		xlog.Debug("pii/ner: detection accepted",
			"group", e.Group, "score", e.Score, "action", action,
			"start", e.Start, "end", e.End, "text", e.Text)
		hits = append(hits, rawHit{
			patternID: cfg.patternID(e.Group),
			action:    action,
			start:     e.Start,
			end:       e.End,
			score:     e.Score,
		})
	}
	return hits, nil
}

// mergeAndEmit handles the overlap-merge + masked-output step. Sorts by
// start (stable on equal starts by descending action strength), drops
// overlapping hits in favour of the stronger action, and walks the text
// once to emit replacement spans.
func mergeAndEmit(text string, hits []rawHit) Result {
	if len(hits) == 0 {
		return Result{Redacted: text}
	}
	// Sort and deduplicate overlapping hits — when two detectors claim
	// the same span, keep the one with the strongest action. Order:
	// block > mask > allow.
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].start != hits[j].start {
			return hits[i].start < hits[j].start
		}
		return actionRank(hits[i].action) > actionRank(hits[j].action)
	})
	merged := hits[:0]
	for _, h := range hits {
		if len(merged) > 0 {
			last := &merged[len(merged)-1]
			if h.start < last.end {
				if actionRank(h.action) > actionRank(last.action) {
					last.action = h.action
					last.patternID = h.patternID
					last.score = h.score
				}
				if h.end > last.end {
					last.end = h.end
				}
				continue
			}
		}
		merged = append(merged, h)
	}

	res := Result{}
	var out strings.Builder
	out.Grow(len(text))
	cursor := 0
	for _, h := range merged {
		matched := text[h.start:h.end]
		span := Span{
			Start:      h.start,
			End:        h.end,
			Pattern:    h.patternID,
			HashPrefix: hashPrefix(matched),
			Action:     h.action,
			Score:      h.score,
		}
		res.Spans = append(res.Spans, span)

		out.WriteString(text[cursor:h.start])
		switch h.action {
		case ActionBlock:
			res.Blocked = true
			out.WriteString(matched)
		case ActionAllow:
			// Detect-and-log only: leave the matched text in place.
			out.WriteString(matched)
		default:
			res.Masked = true
			out.WriteString(maskFor(h.patternID))
		}
		cursor = h.end
	}
	out.WriteString(text[cursor:])
	res.Redacted = out.String()
	return res
}

// maskFor returns the placeholder that replaces a matched span. The
// shape "[REDACTED:<id>]" is intentionally stable — it surfaces the
// detector group back to the model (e.g. "I see you redacted an email").
func maskFor(patternID string) string {
	return "[REDACTED:" + patternID + "]"
}

// hashPrefix returns the first 8 chars of sha256(value). Two calls with
// the same input produce the same prefix so an admin auditing the
// PIIEvent log can spot a recurring leak without ever recovering the
// value.
func hashPrefix(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}

func actionRank(a Action) int {
	switch a {
	case ActionBlock:
		return 3
	case ActionMask:
		return 2
	case ActionAllow:
		return 1
	}
	return 0
}
