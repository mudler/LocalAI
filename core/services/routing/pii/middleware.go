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
	ctxKeyCorrelationID    = "routing.correlation_id"
	ctxKeyPIIEventID       = "routing.pii_event_id"
	// Must match the constants in core/http/middleware/request.go.
	// Echoing them across packages would create an import cycle
	// (http/middleware imports this package). Drift is caught by
	// integration tests against the chat route.
	ctxKeyParsedRequest    = "LOCALAI_REQUEST"
	ctxKeyModelConfig      = "MODEL_CONFIG"
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
func RequestMiddleware(redactor *Redactor, store EventStore, adapter Adapter, fallbackUser *auth.User) echo.MiddlewareFunc {
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

			texts := adapter.Scan(parsed)
			updates := make([]ScannedText, 0, len(texts))
			var blocked bool
			var firstEventID string

			for _, st := range texts {
				if st.Text == "" {
					continue
				}
				res := redactor.RedactWithOverrides(st.Text, overrides)
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
