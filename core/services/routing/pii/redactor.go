package pii

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// rawHit is one detection before overlap-merging. Lifted to file scope so
// the NER collector and the merge/emit step can share it.
type rawHit struct {
	patternID string
	action    Action
	start     int
	end       int
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
		if e.Score < cfg.MinScore {
			continue
		}
		action, ok := cfg.ResolveAction(e.Group)
		if !ok {
			continue
		}
		if e.Start < 0 || e.End <= e.Start || e.End > len(text) {
			// Defensive: the backend should return byte offsets into the
			// original text, but a misconfigured model could produce
			// garbage. Skip rather than panic on slice OOB.
			continue
		}
		hits = append(hits, rawHit{
			patternID: nerPatternID(e.Group),
			action:    action,
			start:     e.Start,
			end:       e.End,
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
