// SPDX-License-Identifier: MIT
package middleware

import (
	"context"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/pkg/diagnostics"
)

func diagnosticOutcome(ctx context.Context, failed bool) diagnostics.Outcome {
	if ctx.Err() != nil {
		return diagnostics.OutcomeCanceled
	}
	if failed {
		return diagnostics.OutcomeError
	}
	return diagnostics.OutcomeOK
}

// Extraction timings are individual middleware/retry segments, not a single
// socket-to-inference duration. Finish before next; its latency and errors do
// not belong to extraction. The deferred call covers early extraction exits.
func noopExtractionEnd(error) {}

func (re *RequestExtractor) beginExtraction(c echo.Context) func(error) {
	ctx := re.getRequestContext(c)
	if !diagnostics.Enabled(ctx) {
		return noopExtractionEnd
	}
	end := diagnostics.Begin(ctx, diagnostics.PhaseExtraction)
	finished := false
	return func(err error) {
		if finished {
			return
		}
		finished = true
		end(diagnosticOutcome(ctx, err != nil || c.Response().Status >= 400), 0)
	}
}
