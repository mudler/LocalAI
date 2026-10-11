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

// DispatchMiddleware belongs before all other application Pre middleware. It
// never reads a client identity or request body. All HTTP admission producers
// finish before next returns, including streaming handler middleware unwinding.
// Asynchronous producers that outlive a handler must flush their own completed
// observations; FlushDispatch drains, but does not close, the bounded collection.
func DispatchMiddleware(recorder *diagnostics.Recorder) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			ctx := attachDiagnostics(c, recorder)
			diagnostics.Dispatch(ctx).MarkOnce(diagnostics.PhaseHTTPEntry, diagnosticOutcome(ctx, false))
			defer diagnostics.FlushDispatch(ctx)
			return next(c)
		}
	}
}

func attachDiagnostics(c echo.Context, recorder *diagnostics.Recorder) context.Context {
	ctx := c.Request().Context()
	observed := recorder.Request(ctx)
	if observed != ctx {
		c.SetRequest(c.Request().WithContext(observed))
		c.Response().Header().Set(diagnostics.HeaderDiagnosticID, diagnostics.RequestID(observed))
	}
	return observed
}

// DispatchAuth measures only authentication, not the downstream handler. The
// deferred end covers rejection; Start's exactly-once end keeps downstream
// failures from rewriting the outcome captured at the successful handoff.
func DispatchAuth(auth echo.MiddlewareFunc) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) (err error) {
			ctx := c.Request().Context()
			_, end := diagnostics.Dispatch(ctx).Start(diagnostics.PhaseAuth)
			defer func() { end(diagnosticOutcome(ctx, err != nil || c.Response().Status >= 400), 0) }()
			return auth(func(c echo.Context) error {
				end(diagnosticOutcome(ctx, false), 0)
				return next(c)
			})(c)
		}
	}
}
