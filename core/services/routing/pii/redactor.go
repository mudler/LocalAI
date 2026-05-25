package pii

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
)

// rawHit is one detection — regex-side or NER-side — before
// overlap-merging. Lifted to file scope so the regex and NER
// collectors can both produce them and feed the same merge/emit step.
type rawHit struct {
	patternID string
	action    Action
	start     int
	end       int
}

// Redactor scans text against a configured pattern set and applies the
// per-pattern action. The pattern set itself is mutable at runtime via
// SetAction (the /api/pii/patterns/:id admin endpoint mutates it
// in-place); reads are guarded by a mutex so concurrent requests stay
// race-free.
type Redactor struct {
	mu       sync.RWMutex
	patterns []Pattern
	maxLen   int
}

// NewRedactor constructs a redactor from a list of compiled patterns
// (use Compile() to compile config-loaded patterns first). nil
// patterns is valid and produces a no-op redactor — convenient for the
// "PII disabled" deployment.
func NewRedactor(patterns []Pattern) *Redactor {
	return &Redactor{
		patterns: patterns,
		maxLen:   MaxPatternLength(patterns),
	}
}

// MaxPatternLength is exposed so the streaming wrapper can size its
// tail buffer to match.
func (r *Redactor) MaxPatternLength() int { return r.maxLen }

// Patterns returns a copy of the configured pattern set so callers can
// iterate without holding the redactor lock. The compiled regexes are
// shared — they are immutable once built.
func (r *Redactor) Patterns() []Pattern {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Clone(r.patterns)
}

// SetAction overrides the action for a single pattern. Used by the
// /api/pii/patterns/:id admin endpoint and the set_pii_pattern_action
// MCP tool — transient until process restart unless persisted via
// --pii-config.
//
// Publishes a new slice so concurrent Redact callers iterating an
// older snapshot don't race on the per-element Action string (Go
// strings are not atomic two-word values).
func (r *Redactor) SetAction(id string, action Action) error {
	if action != ActionMask && action != ActionBlock && action != ActionRouteLocal {
		return fmt.Errorf("unknown action %q (must be mask, block, or route_local)", action)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.patterns {
		if r.patterns[i].ID == id {
			next := slices.Clone(r.patterns)
			next[i].Action = action
			r.patterns = next
			return nil
		}
	}
	return fmt.Errorf("unknown pattern id %q", id)
}

// SetDisabled toggles a pattern's enabled state in the live redactor.
// Same COW publish as SetAction.
func (r *Redactor) SetDisabled(id string, disabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.patterns {
		if r.patterns[i].ID == id {
			next := slices.Clone(r.patterns)
			next[i].Disabled = disabled
			r.patterns = next
			return nil
		}
	}
	return fmt.Errorf("unknown pattern id %q", id)
}

// Redact is a thin wrapper for callers that don't need per-request
// action overrides. It applies each pattern's compiled-in default
// action.
func (r *Redactor) Redact(text string) Result {
	return r.RedactWithOverrides(text, nil)
}

// RedactWithOverrides scans text and returns the result. The override
// map is keyed by pattern id; when present, the value replaces the
// pattern's compiled-in action for this call only — the redactor's
// stored action is unchanged. Pattern ids missing from the map use
// their stored action.
//
// For every match it records a Span (with HashPrefix, never the value)
// and applies the resolved Action:
//   - block: sets Result.Blocked, leaves text intact (caller decides
//     whether to surface the redacted form).
//   - mask: replaces the span with maskFor(pattern.ID).
//   - route_local: sets Result.LocalOnly, leaves text intact.
//
// Spans are returned in the original input's coordinate system so the
// PIIEvent record can be written without re-running the scan.
func (r *Redactor) RedactWithOverrides(text string, overrides map[string]Action) Result {
	return r.redact(context.Background(), text, overrides, NERConfig{})
}

// RedactWithNER is the encoder-tier variant: runs both the regex tier
// (with per-pattern overrides) and the NER tier, merges hits, and
// emits one redacted output. A nil NERConfig.Detector skips the NER
// pass — callers can hand the same path the same NERConfig{} whether
// or not the model has NER configured.
//
// Errors from the NER detector are returned alongside a best-effort
// regex-only Result so the caller can decide whether to fail open
// (return the regex Result, log the error) or fail closed (refuse the
// request). The regex tier never errors.
func (r *Redactor) RedactWithNER(ctx context.Context, text string, overrides map[string]Action, nerCfg NERConfig) (Result, error) {
	if nerCfg.Detector == nil {
		return r.redact(ctx, text, overrides, nerCfg), nil
	}
	hits, err := r.collectRegexHits(text, overrides)
	if err != nil {
		return Result{Redacted: text}, err
	}
	nerHits, nerErr := collectNERHits(ctx, text, nerCfg)
	if nerErr != nil {
		// Return the regex-only result so a NER-backend outage doesn't
		// strip the cheap protection. Caller decides fail-open vs
		// fail-closed via the returned error.
		return mergeAndEmit(text, hits), nerErr
	}
	return mergeAndEmit(text, append(hits, nerHits...)), nil
}

// redact is the internal regex-only entry point. RedactWithOverrides
// is the public wrapper; RedactWithNER routes through here only when
// the NER detector is nil (so the call site doesn't need a separate
// "regex-only" code path).
func (r *Redactor) redact(_ context.Context, text string, overrides map[string]Action, _ NERConfig) Result {
	hits, _ := r.collectRegexHits(text, overrides)
	return mergeAndEmit(text, hits)
}

// collectRegexHits walks the configured pattern set against text and
// returns each verified match as a rawHit. The redactor lock is held
// only long enough to snapshot the pattern slice — regex evaluation
// runs lock-free against the snapshot, so SetAction/SetDisabled don't
// stall a long-running Redact.
func (r *Redactor) collectRegexHits(text string, overrides map[string]Action) ([]rawHit, error) {
	r.mu.RLock()
	patterns := r.patterns
	r.mu.RUnlock()

	if len(patterns) == 0 || text == "" {
		return nil, nil
	}
	var hits []rawHit
	for _, p := range patterns {
		if p.regex == nil {
			// Pattern declared but Compile() not called. Skip rather
			// than panic; the caller already saw an error from Compile.
			continue
		}
		if p.Disabled {
			continue
		}
		action := p.Action
		if override, ok := overrides[p.ID]; ok {
			action = override
		}
		idxs := p.regex.FindAllStringIndex(text, -1)
		for _, idx := range idxs {
			candidate := text[idx[0]:idx[1]]
			if VerifyMatch(p.ID, candidate) == "" {
				continue
			}
			hits = append(hits, rawHit{
				patternID: p.ID,
				action:    action,
				start:     idx[0],
				end:       idx[1],
			})
		}
	}
	return hits, nil
}

// collectNERHits invokes the configured NERDetector and converts each
// returned entity into a rawHit using the NERConfig's action map.
// Entities below MinScore or with no resolved action are dropped — the
// detector doesn't know which entity groups the admin cares about, so
// the redactor filters here.
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
			// Defensive: the backend should return byte offsets into
			// the original text, but a misconfigured model could
			// produce garbage. Skip rather than panic on slice OOB.
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

// mergeAndEmit handles the overlap-merge + masked-output step that
// regex-only and combined regex+NER redactions both perform. Sorts by
// start (stable on equal starts by descending action strength), drops
// overlapping hits in favour of the stronger action, and walks the
// text once to emit replacement spans.
func mergeAndEmit(text string, hits []rawHit) Result {
	if len(hits) == 0 {
		return Result{Redacted: text}
	}
	// Sort and deduplicate overlapping hits — when two patterns claim
	// the same span (e.g., a credit-card-shaped value also scans as
	// digits, or NER tags a span the regex also caught), keep the one
	// with the strongest action. Order: block > route_local > mask.
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
		}
		res.Spans = append(res.Spans, span)

		out.WriteString(text[cursor:h.start])
		switch h.action {
		case ActionBlock:
			res.Blocked = true
			out.WriteString(matched)
		case ActionRouteLocal:
			res.LocalOnly = true
			out.WriteString(matched)
		default:
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
// pattern id back to the model, which is sometimes useful (e.g., the
// model can say "I see you redacted an email"). Admins who want a
// less informative replacement can build one in front of this.
func maskFor(patternID string) string {
	return "[REDACTED:" + patternID + "]"
}

// hashPrefix returns the first 8 chars of sha256(value). Two calls
// with the same input produce the same prefix so an admin auditing
// the PIIEvent log can spot a recurring leak ("the same SSN appears
// 200 times this hour") without ever recovering the value.
func hashPrefix(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}

func actionRank(a Action) int {
	switch a {
	case ActionBlock:
		return 3
	case ActionRouteLocal:
		return 2
	case ActionMask:
		return 1
	}
	return 0
}
