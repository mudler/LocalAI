package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/pii"
)

// PIIDecideEndpoint exposes the PII redactor as a decision oracle:
// scan the supplied text and return findings + the strongest action
// the configured pattern set would take, without rewriting the
// caller's request or recording an audit event.
//
// External routers (e.g. the localai-org/platform router) call this
// before dispatching to learn whether to mask the prompt in place,
// block the request, or pass it through. LocalAI's in-band PII
// middleware is the alternative path for direct-to-LocalAI clients —
// same Redactor, different framing.
//
// Takes the *pii.Redactor directly rather than the whole
// *application.Application so the handler stays unit-testable with a
// freshly-constructed redactor (mirrors the pattern in
// router_decide.go). The route-registration site is responsible for
// stubbing this endpoint when --disable-pii is set so callers get a
// 503 signalling "admin opted out" rather than a misleading allow.
//
// @Summary  Scan text for PII and return findings + suggested action (decision oracle)
// @Tags     pii
// @Accept   json
// @Produce  json
// @Param    request body schema.PIIDecideRequest true "decide params"
// @Success  200 {object} schema.PIIDecideResponse
// @Failure  400 {object} map[string]string
// @Router   /api/pii/decide [post]
func PIIDecideEndpoint(redactor *pii.Redactor) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req schema.PIIDecideRequest
		if err := c.Bind(&req); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid request body: "+err.Error())
		}
		if req.Text == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "text is required")
		}

		res := redactor.Redact(req.Text)
		findings := make([]schema.PIIFinding, len(res.Spans))
		for i, s := range res.Spans {
			findings[i] = schema.PIIFinding{
				Start:      s.Start,
				End:        s.End,
				Pattern:    s.Pattern,
				HashPrefix: s.HashPrefix,
			}
		}
		return c.JSON(http.StatusOK, schema.PIIDecideResponse{
			Findings:        findings,
			SuggestedAction: suggestedAction(res),
			RedactedPreview: res.Redacted,
		})
	}
}

// suggestedAction collapses the Redactor's Result flags onto a single
// wire-format action using the in-band ordering (block > mask >
// allow). "allow" covers both "nothing matched" and "matched but every
// span resolved to the allow action" — in both cases the caller may
// dispatch unchanged, with the Findings list reporting what was seen.
func suggestedAction(res pii.Result) string {
	switch {
	case res.Blocked:
		return string(pii.ActionBlock)
	case res.Masked:
		return string(pii.ActionMask)
	default:
		return string(pii.ActionAllow)
	}
}
