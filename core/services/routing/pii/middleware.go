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
// per-model PII configuration carried on the echo context. *config.ModelConfig
// satisfies it via PIIIsEnabled / PIIPatternOverrides; the indirection
// keeps the pii package from importing core/config.
//
// Consumers of the override map: the action returned from PIIPatternOverrides
// is the raw YAML string (e.g. "block"). Validation against the canonical
// ActionMask/Block/Allow constants happens here, so a typo in a model
// YAML logs and is ignored rather than panicking.
type ModelPIIConfig interface {
	PIIIsEnabled() bool
	PIIPatternOverrides() map[string]string
}

// ModelNERConfig is the optional encoder/NER view of a model's PII
// config. It is kept separate from ModelPIIConfig so existing
// implementers (and regex-only test stubs) don't have to grow methods:
// the middleware type-asserts for it and runs the NER tier only when
// the model both satisfies this interface and names a Model. As with
// ModelPIIConfig, *config.ModelConfig satisfies it without dragging
// core/config into this package's import graph.
type ModelNERConfig interface {
	PIINERModel() string
	PIINERMinScore() float32
	PIINERDefaultAction() string
	PIINEREntityActions() map[string]string
}

// NERDetectorResolver returns the NER detector for a model name, or nil
// if that model can't supply one (unknown / not loadable). Supplied by
// the application layer, which owns the model loader and the
// core/backend dependency — this keeps the pii package free of both. A
// nil resolver (or the option being unset) disables the NER tier.
type NERDetectorResolver func(modelName string) NERDetector

// Option configures optional RequestMiddleware behaviour. Threaded as
// variadic options so adding the NER tier doesn't break the existing
// four-argument call sites (routes and tests).
type Option func(*mwOptions)

type mwOptions struct {
	nerResolver NERDetectorResolver
}

// WithNERResolver enables the encoder/NER tier. When a request's model
// carries a pii.ner.model, the middleware resolves a detector for it
// and runs RedactWithNER (regex + NER, merged); without this option, or
// when no NER model is configured, redaction stays regex-only.
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
			if redactor == nil || len(redactor.Patterns()) == 0 || adapter.Scan == nil {
				return next(c)
			}

			// Per-model gating: redaction is opt-in per model. If the
			// resolved config disables PII for this model (the default
			// for non-proxy backends), pass through immediately. We do
			// this before parsing the request so a disabled model
			// doesn't pay the regex scan cost.
			if cfg, ok := c.Get(ctxKeyModelConfig).(ModelPIIConfig); ok {
				if !cfg.PIIIsEnabled() {
					return next(c)
				}
			} else {
				// No ModelPIIConfig on context → fail-closed: skip
				// redaction. This protects routes that wire the
				// middleware before SetModelAndConfig runs (or non-chat
				// routes that don't carry a model). The middleware was
				// previously fail-open, applying the global redactor
				// unconditionally; the new contract is per-model
				// opt-in, and a missing model is treated as disabled.
				return next(c)
			}

			parsed := c.Get(ctxKeyParsedRequest)
			if parsed == nil {
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

			// Resolve per-model action overrides once per request. The
			// raw map is YAML strings; convert to the typed Action set
			// and silently drop unknown values rather than failing the
			// request — model YAML typos shouldn't take chat down.
			var overrides map[string]Action
			if cfg, ok := c.Get(ctxKeyModelConfig).(ModelPIIConfig); ok {
				if raw := cfg.PIIPatternOverrides(); len(raw) > 0 {
					overrides = make(map[string]Action, len(raw))
					for id, action := range raw {
						switch Action(action) {
						case ActionMask, ActionBlock, ActionAllow:
							overrides[id] = Action(action)
						default:
							xlog.Warn("pii: ignoring unknown action in per-model override",
								"pattern", id, "action", action)
						}
					}
				}
			}

			// Resolve the encoder/NER tier once per request. Only when
			// the option supplied a resolver, the model satisfies
			// ModelNERConfig, names a Model, and that model resolves to
			// a detector. Otherwise nerCfg.Detector stays nil and the
			// redaction path below is regex-only.
			var nerCfg NERConfig
			if o.nerResolver != nil {
				if nc, ok := c.Get(ctxKeyModelConfig).(ModelNERConfig); ok {
					if nerModel := nc.PIINERModel(); nerModel != "" {
						det := o.nerResolver(nerModel)
						if det == nil {
							// Fail closed: a configured NER model that
							// cannot be resolved (not installed, wrong
							// name, or load failure) must block, not
							// silently downgrade to regex-only — same
							// reasoning as the detector-error path below.
							xlog.Error("pii: configured NER model could not be resolved; blocking request (fail-closed)", "ner_model", nerModel)
							return blockNERUnavailable(c, store, correlationID, userID)
						}
						nerCfg = NERConfig{
							Detector: det,
							MinScore: nc.PIINERMinScore(),
							// §9.7: a detected entity is masked unless an
							// admin downgrades it — safe-by-default for a
							// PII filter. Empty/invalid config => mask.
							DefaultAction: validActionOr(nc.PIINERDefaultAction(), ActionMask),
							EntityActions: validActions(nc.PIINEREntityActions()),
						}
					}
				}
			}

			texts := adapter.Scan(parsed)
			updates := make([]ScannedText, 0, len(texts))
			var blocked bool
			var firstEventID string

			for _, st := range texts {
				if st.Text == "" {
					continue
				}
				var res Result
				if nerCfg.Detector != nil {
					// Fail closed: a NER-backend outage at request time
					// must NOT silently downgrade to regex-only. The NER
					// tier was explicitly configured for this model, so
					// the semantic check it provides is part of the
					// contract — serving the request with only the cheap
					// regex tier would leak exactly the PII NER was added
					// to catch. RedactWithNER still returns a best-effort
					// regex Result alongside the error (the redactor stays
					// fail-open and leaves the policy to us); we discard it
					// and block.
					r2, nerErr := redactor.RedactWithNER(c.Request().Context(), st.Text, overrides, nerCfg)
					if nerErr != nil {
						xlog.Error("pii: NER detector failed; blocking request (fail-closed)", "error", nerErr)
						return blockNERUnavailable(c, store, correlationID, userID)
					}
					res = r2
				} else {
					res = redactor.RedactWithOverrides(st.Text, overrides)
				}
				if len(res.Spans) == 0 {
					continue
				}

				// Persist one event per span so admins can see exactly
				// which patterns fired in which positions. The action
				// recorded is the resolved one (after override), so the
				// events log reflects what actually happened to the
				// request, not the global default.
				for _, span := range res.Spans {
					action := actionForSpan(redactor.Patterns(), span.Pattern, overrides)
					ev := PIIEvent{
						ID:            newEventID(),
						CorrelationID: correlationID,
						UserID:        userID,
						Direction:     DirectionIn,
						PatternID:     span.Pattern,
						ByteOffset:    span.Start,
						Length:        span.End - span.Start,
						HashPrefix:    span.HashPrefix,
						Action:        action,
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

func actionForPattern(patterns []Pattern, id string) Action {
	for _, p := range patterns {
		if p.ID == id {
			return p.Action
		}
	}
	return ActionMask
}

// actionForSpan returns the resolved action for a span, preferring a
// per-request override over the pattern's stored action. Used so the
// PIIEvent log reflects the action that actually fired (e.g., a model
// upgraded email from mask to block — the event row says "block").
func actionForSpan(patterns []Pattern, id string, overrides map[string]Action) Action {
	if action, ok := overrides[id]; ok {
		return action
	}
	return actionForPattern(patterns, id)
}

func newEventID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "pii_" + hex.EncodeToString(b[:])
}
