package pii

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/routing/contract"
	"github.com/mudler/xlog"
)

// Echo context keys this middleware reads from / writes to. The string
// values must match the constants in core/http/middleware/context_keys.go;
// kept in sync by hand because echoing constants across packages would
// drag the http/middleware package into pii's import graph and create
// a cycle (http/middleware will import this one).
const (
	ctxKeyCorrelationID = "routing.correlation_id"
	ctxKeyPIIEventID    = "routing.pii_event_id"
	// Must match the constants in core/http/middleware/request.go.
	// Echoing them across packages would create an import cycle
	// (http/middleware imports this package). Drift is caught by
	// integration tests against the chat route.
	ctxKeyParsedRequest = "LOCALAI_REQUEST"
	ctxKeyModelConfig   = "MODEL_CONFIG"
)

// ModelPIIConfig is the duck-typed view this middleware needs of the
// per-model PII configuration carried on the echo context.
// *config.ModelConfig satisfies it via PIIIsEnabled / PIIDetectors; the
// indirection keeps the pii package from importing core/config.
//
// PIIDetectors lists the token-classification models whose detections
// drive redaction for this (consuming) model. The detection policy lives
// on each named detector model — resolved via NERDetectorResolver — so
// this consuming view carries no per-entity actions of its own.
type ModelPIIConfig interface {
	PIIIsEnabled() bool
	PIIDetectors() []string
}

// NERDetectorResolver resolves a detector model name to a ready-to-use
// NERConfig — the detector plus the policy (min score, entity→action
// map, default action) read from that model's own pii_detection block.
// ok is false when the name can't supply a detector (unknown model, not
// a token_classify model, or load failure); the middleware fails closed
// in that case. Supplied by the application layer, which owns the model
// loader and the core/backend dependency, keeping the pii package free of
// both. A nil resolver (or the option being unset) disables the NER tier.
type NERDetectorResolver func(modelName string) (NERConfig, bool)

// Option configures optional RequestMiddleware behaviour. Threaded as
// variadic options so adding the NER tier doesn't break the existing
// four-argument call sites (routes and tests).
type Option func(*mwOptions)

type mwOptions struct {
	nerResolver    NERDetectorResolver
	policyResolver PolicyResolver
}

// PolicyResolver returns the effective (enabled, detectors) for the model
// carried on the request context, layering instance-wide PII defaults over the
// per-model config. Supplied by the application layer (which owns core/config),
// keeping this package decoupled from it — the middleware passes the raw
// context value through as `any`. When unset, the middleware falls back to the
// duck-typed ModelPIIConfig (explicit per-model config only, no global default).
type PolicyResolver func(modelCfg any) (enabled bool, detectors []string)

// WithPolicyResolver overrides how the middleware decides enablement and the
// detector list, so the instance-wide default detector / default-on usecases
// apply. Without it the middleware reads ModelPIIConfig off the context.
func WithPolicyResolver(r PolicyResolver) Option {
	return func(o *mwOptions) { o.policyResolver = r }
}

// WithNERResolver enables the NER tier. When a request's model lists
// pii.detectors, the middleware resolves each to a NERConfig and runs
// RedactNER (the union of all detectors' hits, merged). Without this
// option, or when a model lists no detectors, redaction is a no-op.
func WithNERResolver(r NERDetectorResolver) Option {
	return func(o *mwOptions) { o.nerResolver = r }
}

// ScannedText is one piece of user text from the request. Index is
// opaque to the middleware — the Adapter implementation uses it to
// put the redacted version back in the right place.
type ScannedText struct {
	Index int
	Text  string
}

// Adapter pulls scannable text out of a parsed request and writes
// redacted text back. Provided as a per-API-shape function rather
// than an interface on the request type so the schema package does
// not have to depend on pii. Each route registration passes the
// adapter that knows its request format.
//
// The middleware calls Scan once per request and Apply once with
// every span the redactor returned. updates are guaranteed to share
// indices the adapter previously returned from Scan; the adapter
// must not assume input order matches scan order.
type Adapter struct {
	Scan  func(parsed any) []ScannedText
	Apply func(parsed any, updates []ScannedText)
}

// RequestMiddleware applies the regex PII tier to incoming chat
// requests. If the parsed request is not a MessageScanner (e.g.,
// non-chat endpoints registered against the same group later), the
// middleware passes through.
//
//   - On match with action=block: the request is rejected with 400 and
//     a PIIEvent is recorded. The matched value is never echoed back
//     to the client.
//   - On match with action=mask: the redacted text replaces the
//     original on the parsed request. PIIEvents are recorded.
//   - On match with action=allow: the original text is left intact; a
//     PIIEvent is still recorded so the detection is auditable.
//
// recorder is the Recorder on which to record events; nil disables
// recording (the redaction still happens). fallbackUser supplies the
// no-auth identity. The middleware writes ctxKeyPIIEventID on the echo
// context so the usage middleware can later cross-reference the event
// with the UsageRecord.
func RequestMiddleware(redactor *Redactor, store EventStore, adapter Adapter, fallbackUser *auth.User, opts ...Option) echo.MiddlewareFunc {
	var o mwOptions
	for _, opt := range opts {
		opt(&o)
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if redactor == nil || adapter.Scan == nil {
				return next(c)
			}

			// Per-model gating: redaction is opt-in per model. The policy
			// resolver (when wired) layers instance-wide defaults over the
			// per-model config; otherwise we read the per-model config
			// directly. A missing config (non-chat routes, or middleware
			// wired before SetModelAndConfig) or a not-enabled result passes
			// through.
			rawCfg := c.Get(ctxKeyModelConfig)
			var enabled bool
			var detectors []string
			if o.policyResolver != nil {
				enabled, detectors = o.policyResolver(rawCfg)
			} else if cfg, ok := rawCfg.(ModelPIIConfig); ok {
				enabled, detectors = cfg.PIIIsEnabled(), cfg.PIIDetectors()
			}
			if !enabled {
				return next(c)
			}

			parsed := c.Get(ctxKeyParsedRequest)
			if parsed == nil {
				return next(c)
			}

			// A PII-enabled model with no detectors (or no resolver wired)
			// has nothing to scan with — pass through.
			if len(detectors) == 0 || o.nerResolver == nil {
				return next(c)
			}

			user := auth.GetUser(c)
			if user == nil {
				user = fallbackUser
			}
			userID := ""
			if user != nil {
				userID = user.ID
			}
			correlationID, _ := c.Get(ctxKeyCorrelationID).(string)

			// Resolve each named detector to its NERConfig (detector +
			// the policy from that model's own pii_detection block). A
			// configured detector that can't be resolved fails closed:
			// serving the request without the semantic check the operator
			// asked for is exactly the leak this tier exists to prevent.
			cfgs := make([]NERConfig, 0, len(detectors))
			for _, name := range detectors {
				nc, ok := o.nerResolver(name)
				if !ok {
					xlog.Error("pii: configured detector model could not be resolved; blocking request (fail-closed)", "detector", name)
					return blockNERUnavailable(c, store, correlationID, userID)
				}
				cfgs = append(cfgs, nc)
			}

			texts := adapter.Scan(parsed)
			updates := make([]ScannedText, 0, len(texts))
			var blocked bool
			var firstEventID string

			for _, st := range texts {
				if st.Text == "" {
					continue
				}
				// Fail closed: a detector outage at request time must NOT
				// silently serve the request. The NER tier was explicitly
				// configured for this model, so the semantic check is part
				// of the contract.
				res, nerErr := RedactNER(c.Request().Context(), st.Text, cfgs)
				if nerErr != nil {
					xlog.Error("pii: NER detector failed; blocking request (fail-closed)", "error", nerErr)
					return blockNERUnavailable(c, store, correlationID, userID)
				}
				if len(res.Spans) == 0 {
					continue
				}

				// Persist one event per detected span. The action recorded
				// is the one that actually fired (carried on the span after
				// the overlap merge), so the events log reflects what
				// happened to the request.
				for _, span := range res.Spans {
					ev := PIIEvent{
						ID:            newEventID(),
						CorrelationID: correlationID,
						UserID:        userID,
						Direction:     DirectionIn,
						PatternID:     span.Pattern,
						ByteOffset:    span.Start,
						Length:        span.End - span.Start,
						HashPrefix:    span.HashPrefix,
						Action:        span.Action,
						Score:         span.Score,
						CreatedAt:     time.Now().UTC(),
					}
					if firstEventID == "" {
						firstEventID = ev.ID
					}
					if store != nil {
						if err := store.Record(context.Background(), ev); err != nil {
							xlog.Error("pii: failed to record event", "error", err, "pattern", span.Pattern)
						}
					}
					// Contract: every span must produce an event.
					contract.Invariant(
						"pii.event_per_span",
						span.Pattern != "" && ev.PatternID != "",
						"correlation", correlationID, "pattern", span.Pattern,
					)
				}

				if res.Blocked {
					blocked = true
				}
				updates = append(updates, ScannedText{Index: st.Index, Text: res.Redacted})
			}

			if blocked {
				return c.JSON(http.StatusBadRequest, map[string]any{
					"error": map[string]string{
						"message": "request blocked by content policy (sensitive data detected)",
						"type":    "pii_blocked",
					},
					"correlation_id": correlationID,
					"pii_event_id":   firstEventID,
				})
			}

			if len(updates) > 0 && adapter.Apply != nil {
				adapter.Apply(parsed, updates)
			}
			if firstEventID != "" {
				c.Set(ctxKeyPIIEventID, firstEventID)
			}
			return next(c)
		}
	}
}

// nerUnavailablePattern is the sentinel PatternID recorded on the
// fail-closed audit event when a model's configured NER tier cannot
// run. It is not a real regex pattern — it marks a request blocked
// because the encoder/NER check was unavailable (model unresolved or
// backend error), so the events log distinguishes it from a content
// block (which carries a real pattern ID).
const nerUnavailablePattern = "__ner_unavailable__"

// blockNERUnavailable records a fail-closed audit event and returns the
// response used when a model has an NER tier configured but it could
// not run. Failing closed is deliberate for a PII filter: if the
// semantic check the operator asked for cannot execute, refusing the
// request is safer than serving it with only the cheap regex tier. The
// 503 (vs the 400 used for a content block) tells clients and operators
// this was a dependency outage, not sensitive data in the request.
func blockNERUnavailable(c echo.Context, store EventStore, correlationID, userID string) error {
	ev := PIIEvent{
		ID:            newEventID(),
		Kind:          KindPII,
		CorrelationID: correlationID,
		UserID:        userID,
		Direction:     DirectionIn,
		PatternID:     nerUnavailablePattern,
		Action:        ActionBlock,
		CreatedAt:     time.Now().UTC(),
	}
	if store != nil {
		if err := store.Record(context.Background(), ev); err != nil {
			xlog.Error("pii: failed to record NER-unavailable event", "error", err)
		}
	}
	c.Set(ctxKeyPIIEventID, ev.ID)
	return c.JSON(http.StatusServiceUnavailable, map[string]any{
		"error": map[string]string{
			"message": "request blocked: PII NER check is configured but unavailable",
			"type":    "pii_ner_unavailable",
		},
		"correlation_id": correlationID,
		"pii_event_id":   ev.ID,
	})
}

// validAction converts a raw YAML action string to the typed Action,
// returning "" for anything that isn't a known action.
func validAction(raw string) Action {
	switch Action(raw) {
	case ActionMask, ActionBlock, ActionAllow:
		return Action(raw)
	default:
		return ""
	}
}

// validActionOr is validAction with a fallback for empty/invalid input.
func validActionOr(raw string, fallback Action) Action {
	if a := validAction(raw); a != "" {
		return a
	}
	return fallback
}

// validActions converts a raw entity-group->action map to typed
// Actions, dropping (and logging) unknown actions so a model YAML typo
// is ignored rather than taking the request down — mirroring how the
// per-pattern overrides are validated above.
func validActions(raw map[string]string) map[string]Action {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]Action, len(raw))
	for group, action := range raw {
		if a := validAction(action); a != "" {
			out[group] = a
		} else {
			xlog.Warn("pii: ignoring unknown NER entity action", "group", group, "action", action)
		}
	}
	return out
}

func newEventID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "pii_" + hex.EncodeToString(b[:])
}
