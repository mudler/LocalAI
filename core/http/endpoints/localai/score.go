package localai

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
)

// ScoreRequest is the wire format for POST /api/score. Mirrors the
// gRPC ScoreRequest one-to-one — the endpoint exists primarily to
// smoke-test the new Score primitive end-to-end without writing a
// custom gRPC client. Production routing will call backend.ModelScore
// directly via the router-side adapter.
type ScoreRequest struct {
	Model                string   `json:"model"`
	Prompt               string   `json:"prompt"`
	Candidates           []string `json:"candidates"`
	IncludeTokenLogprobs bool     `json:"include_token_logprobs,omitempty"`
	LengthNormalize      bool     `json:"length_normalize,omitempty"`
}

type ScoreResponseCandidate struct {
	LogProb                 float64        `json:"log_prob"`
	LengthNormalizedLogProb float64        `json:"length_normalized_log_prob,omitempty"`
	NumTokens               int            `json:"num_tokens"`
	Tokens                  []ScoreTokenLP `json:"tokens,omitempty"`
}

type ScoreTokenLP struct {
	Token   string  `json:"token"`
	LogProb float64 `json:"log_prob"`
}

type ScoreResponse struct {
	Model      string                   `json:"model"`
	Candidates []ScoreResponseCandidate `json:"candidates"`
}

// ScoreEndpoint exposes the Score gRPC primitive over HTTP. Admin-only —
// scoring loads a model and runs inference, same risk surface as
// /v1/chat/completions.
func ScoreEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req ScoreRequest
		if err := c.Bind(&req); err != nil {
			return echo.NewHTTPError(400, "invalid request body: "+err.Error())
		}
		if req.Model == "" {
			return echo.NewHTTPError(400, "model is required")
		}
		if len(req.Candidates) == 0 {
			return echo.NewHTTPError(400, "candidates must be non-empty")
		}

		modelConfig, err := cl.LoadModelConfigFileByNameDefaultOptions(req.Model, appConfig)
		if err != nil || modelConfig == nil {
			return echo.NewHTTPError(404, "model not found: "+req.Model)
		}

		fn, err := backend.ModelScore(req.Prompt, req.Candidates, backend.ScoreOptions{
			IncludeTokenLogprobs: req.IncludeTokenLogprobs,
			LengthNormalize:      req.LengthNormalize,
		}, ml, *modelConfig, appConfig)
		if err != nil {
			return echo.NewHTTPError(500, "failed to bind scorer: "+err.Error())
		}
		results, err := fn(c.Request().Context())
		if err != nil {
			return echo.NewHTTPError(500, "score call failed: "+err.Error())
		}

		out := ScoreResponse{Model: req.Model, Candidates: make([]ScoreResponseCandidate, len(results))}
		for i, r := range results {
			out.Candidates[i] = ScoreResponseCandidate{
				LogProb:                 r.LogProb,
				LengthNormalizedLogProb: r.LengthNormalizedLogProb,
				NumTokens:               r.NumTokens,
			}
			if req.IncludeTokenLogprobs && len(r.Tokens) > 0 {
				toks := make([]ScoreTokenLP, len(r.Tokens))
				for j, t := range r.Tokens {
					toks[j] = ScoreTokenLP{Token: t.Token, LogProb: t.LogProb}
				}
				out.Candidates[i].Tokens = toks
			}
		}
		return c.JSON(200, out)
	}
}
