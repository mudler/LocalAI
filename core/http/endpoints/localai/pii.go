package localai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/pii"
)

// ErrNoDetectors is returned by RunPIIScan when neither an explicit detector
// list nor a model's effective PII policy resolve to anything to scan with —
// including a model that has PII disabled, or one that is enabled but names
// no detectors while no instance-wide default is set. The handler maps it to
// 400: the truthful answer is "the middleware would scan nothing", and
// surfacing that loudly beats implying a clean scan happened.
var ErrNoDetectors = errors.New("no PII detectors specified")

// ErrUnknownDetector is returned when a named detector model cannot be
// resolved. Wrapped (errors.Is) so the handler can map it to 400 — a bad
// detector name is a client error, distinct from a detector that resolved but
// failed at scan time (mapped to 502, fail-closed).
var ErrUnknownDetector = errors.New("unknown PII detector")

// RunPIIScan resolves the requested detectors and runs the shared NER/pattern
// redaction pipeline over text. It is the engine behind both /api/pii/analyze
// and /api/pii/redact, kept free of echo so the resolution + scan logic is
// unit-testable with a fake resolver.
//
// Detector selection mirrors the inline chat middleware (middleware.go):
// explicit names take precedence; otherwise the consuming model's effective
// policy is resolved through policy (Application.ResolvePIIPolicy — the
// model's own pii.detectors, else the instance-wide PIIDefaultDetectors, and
// nothing when the model has PII disabled), so the model path answers "what
// would the middleware do with this text?" with the same inputs the
// middleware uses. A nil policy falls back to the model's raw pii.detectors
// (unit tests). Unknown names fail closed (ErrUnknownDetector) rather than
// silently scanning with fewer detectors than asked for.
func RunPIIScan(ctx context.Context, resolver pii.NERDetectorResolver, cl *config.ModelConfigLoader, policy pii.PolicyResolver, names []string, model, text string) (pii.Result, error) {
	if len(names) == 0 && model != "" && cl != nil {
		if cfg, ok := cl.GetModelConfig(model); ok {
			if policy != nil {
				if enabled, detectors := policy(&cfg); enabled {
					names = detectors
				}
			} else {
				names = cfg.PIIDetectors()
			}
		}
	}
	if len(names) == 0 {
		return pii.Result{}, ErrNoDetectors
	}

	cfgs := make([]pii.NERConfig, 0, len(names))
	for _, name := range names {
		nc, ok := resolver(name)
		if !ok {
			return pii.Result{}, fmt.Errorf("%w: %q", ErrUnknownDetector, name)
		}
		cfgs = append(cfgs, nc)
	}
	return pii.RedactNER(ctx, text, cfgs)
}

// piiEntities maps redaction spans to API entities. Each span's Pattern is the
// synthetic "<source>:<GROUP>" id (e.g. "ner:EMAIL"); it is split back into
// the entity type and its source tier. hash_prefix is included only when
// revealHash is set (admin + reveal) — the raw matched value is never exposed.
func piiEntities(spans []pii.Span, revealHash bool) []schema.PIIEntity {
	out := make([]schema.PIIEntity, 0, len(spans))
	for _, s := range spans {
		source, group := splitPatternID(s.Pattern)
		e := schema.PIIEntity{
			EntityType: group,
			Source:     source,
			Start:      s.Start,
			End:        s.End,
			Score:      s.Score,
			Action:     string(s.Action),
		}
		if revealHash {
			e.HashPrefix = s.HashPrefix
		}
		out = append(out, e)
	}
	return out
}

// splitPatternID splits "ner:EMAIL" into ("ner", "EMAIL"). A value with no
// colon is returned as (group, "") inverted to ("", value) so the group is
// never lost.
func splitPatternID(patternID string) (source, group string) {
	if i := strings.IndexByte(patternID, ':'); i >= 0 {
		return patternID[:i], patternID[i+1:]
	}
	return "", patternID
}

// recordPIIEvents persists one audit event per span, tagged with the calling
// API as its Origin so /api/pii/events can be filtered to this surface. Mirrors
// the per-span recording the chat middleware does. Best-effort: a store error
// is logged by the store layer, not surfaced to the caller.
func recordPIIEvents(store pii.EventStore, spans []pii.Span, origin pii.Origin, correlationID, userID string) {
	if store == nil {
		return
	}
	for _, s := range spans {
		_ = store.Record(context.Background(), pii.PIIEvent{
			ID:            pii.NewEventID(),
			Kind:          pii.KindPII,
			Origin:        origin,
			CorrelationID: correlationID,
			UserID:        userID,
			Direction:     pii.DirectionIn,
			PatternID:     s.Pattern,
			ByteOffset:    s.Start,
			Length:        s.End - s.Start,
			HashPrefix:    s.HashPrefix,
			Action:        s.Action,
			Score:         s.Score,
			CreatedAt:     time.Now().UTC(),
		})
	}
}

// piiScanError maps a RunPIIScan error to an HTTP response. Selection/naming
// errors are client errors (400); a detector that resolved but failed at scan
// time is a fail-closed dependency error (502) — the text is never returned
// unredacted.
func piiScanError(c echo.Context, err error) error {
	if errors.Is(err, ErrNoDetectors) || errors.Is(err, ErrUnknownDetector) {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "invalid_request"},
		})
	}
	return c.JSON(http.StatusBadGateway, map[string]any{
		"error": map[string]string{"message": err.Error(), "type": "pii_detector_error"},
	})
}

// piiViewer resolves the request's user (the authenticated user, or the
// synthetic local admin in single-user mode) so the handlers can attribute
// events and gate the admin-only hash reveal.
func piiViewer(c echo.Context, app *application.Application) *auth.User {
	if u := auth.GetUser(c); u != nil {
		return u
	}
	return app.FallbackUser()
}

// PIIAnalyzeEndpoint scans text and returns the detected PII entities without
// mutating it. Always 200 (detection, not enforcement); Blocked reports
// whether the redact endpoint would reject the same text.
// @Summary Detect PII entities in a string (no mutation).
// @Description Runs the configured PII detectors (NER and/or pattern tiers) over the supplied text and returns the matched entity spans with the policy action that would fire. Detection only — the text is not modified and no block is enforced. Select detectors explicitly via `detectors`, or pass a consuming `model` to use its effective policy: the model's own `pii.detectors`, else the instance-wide `pii_default_detectors`. A model with PII disabled, or enabled with nothing to scan with, is a 400. The raw matched value is never returned; admins may set `reveal:true` for the audit hash prefix.
// @Tags pii
// @Param request body schema.PIIAnalyzeRequest true "text + detector selection"
// @Success 200 {object} schema.PIIAnalyzeResponse "Detected entities"
// @Router /api/pii/analyze [post]
func PIIAnalyzeEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req schema.PIIAnalyzeRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": map[string]string{"message": "invalid request body", "type": "invalid_request"},
			})
		}
		viewer := piiViewer(c, app)
		if viewer == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}

		correlationID := pii.NewEventID()
		res, err := RunPIIScan(c.Request().Context(), app.PIINERResolver(), app.ModelConfigLoader(), app.PIIPolicyResolver(), req.Detectors, req.Model, req.Text)
		if err != nil {
			return piiScanError(c, err)
		}

		recordPIIEvents(app.PIIEvents(), res.Spans, pii.OriginAnalyzeAPI, correlationID, viewer.ID)
		revealHash := req.Reveal && viewer.Role == auth.RoleAdmin
		return c.JSON(http.StatusOK, schema.PIIAnalyzeResponse{
			Entities:      piiEntities(res.Spans, revealHash),
			Blocked:       res.Blocked,
			CorrelationID: correlationID,
		})
	}
}

// PIIRedactEndpoint scans text and applies the configured mask/block/allow
// policy. Returns the redacted text (200), or 400 with type "pii_blocked" and
// the offending entities when a block action fires — never a redacted body in
// that case. Mirrors the inline middleware's block contract.
// @Summary Redact PII in a string by applying the configured policy.
// @Description Runs the configured PII detectors over the text and applies each detector model's policy: masked spans are replaced with `[REDACTED:<id>]`, allow spans pass through, and a single block action causes a 400 (type `pii_blocked`) carrying the offending entities — the text is never returned in that case. Select detectors via `detectors`, or a consuming `model`'s effective policy (its own `pii.detectors`, else the instance-wide `pii_default_detectors`; PII must be enabled on the model). Records audit events (origin `pii_redact`) visible at /api/pii/events.
// @Tags pii
// @Param request body schema.PIIAnalyzeRequest true "text + detector selection"
// @Success 200 {object} schema.PIIRedactResponse "Redacted text + entities"
// @Router /api/pii/redact [post]
func PIIRedactEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req schema.PIIAnalyzeRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": map[string]string{"message": "invalid request body", "type": "invalid_request"},
			})
		}
		viewer := piiViewer(c, app)
		if viewer == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}

		correlationID := pii.NewEventID()
		res, err := RunPIIScan(c.Request().Context(), app.PIINERResolver(), app.ModelConfigLoader(), app.PIIPolicyResolver(), req.Detectors, req.Model, req.Text)
		if err != nil {
			return piiScanError(c, err)
		}

		recordPIIEvents(app.PIIEvents(), res.Spans, pii.OriginRedactAPI, correlationID, viewer.ID)
		revealHash := req.Reveal && viewer.Role == auth.RoleAdmin
		entities := piiEntities(res.Spans, revealHash)

		if res.Blocked {
			// Fail closed: a block action returns no redacted text, only the
			// reason and the offending entities — identical to the middleware.
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error":          map[string]string{"message": "text blocked by content policy (sensitive data detected)", "type": "pii_blocked"},
				"entities":       entities,
				"correlation_id": correlationID,
			})
		}
		return c.JSON(http.StatusOK, schema.PIIRedactResponse{
			RedactedText:  res.Redacted,
			Entities:      entities,
			Blocked:       false,
			Masked:        res.Masked,
			CorrelationID: correlationID,
		})
	}
}
