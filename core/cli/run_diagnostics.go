// SPDX-License-Identifier: MIT
package cli

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/diagnostics"
)

func (r *RunCMD) diagnosticsOptions() diagnostics.Options {
	return diagnostics.Options{Pprof: r.Pprof, Address: r.PprofAddress, MutexProfileFraction: r.PprofMutexProfileFraction, BlockProfileRate: r.PprofBlockProfileRate, RequestPhaseTiming: r.RequestPhaseTiming}
}

type diagnosticsRunHooks struct {
	Init  func(context.Context) error
	Serve func() error
	Stop  func(context.Context) error
}

// The private starter seam permits deterministic early/late Accept failures
// without adding listener injection to the public diagnostics API.
type diagnosticsServer interface {
	Errors() <-chan error
	Shutdown(context.Context) error
}

func runWithDiagnostics(ctx context.Context, opts diagnostics.Options, hooks diagnosticsRunHooks, registerStop func(func())) error {
	return runWithDiagnosticsStart(ctx, opts, hooks, registerStop, func(o diagnostics.Options) (diagnosticsServer, error) { return diagnostics.Start(o) })
}

// Preload-only starts backends without taking ownership of a serving lifecycle.
func (r *RunCMD) runConfiguredDiagnostics(ctx context.Context, hooks diagnosticsRunHooks, registerStop func(func())) error {
	opts := r.diagnosticsOptions()
	if err := opts.Validate(); err != nil {
		return err
	}
	if r.PreloadBackendOnly {
		return hooks.Init(ctx)
	}
	return runWithDiagnostics(ctx, opts, hooks, registerStop)
}

var errDiagnosticsStopped = errors.New("run stopped")

func runWithDiagnosticsStart(parent context.Context, opts diagnostics.Options, hooks diagnosticsRunHooks, registerStop func(func()), start func(diagnostics.Options) (diagnosticsServer, error)) (result error) {
	if err := opts.Validate(); err != nil {
		return err
	}
	server, err := start(opts)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(errDiagnosticsStopped)
	monitorDone := make(chan struct{})
	monitorStop := make(chan struct{})
	var profilerErr error
	go func() {
		defer close(monitorDone)
		select {
		case err, ok := <-server.Errors():
			if ok && err != nil {
				profilerErr = err
				cancel(err)
			}
		case <-monitorStop:
			// A buffered failure must not disappear when startup fails concurrently.
			select {
			case err, ok := <-server.Errors():
				if ok && err != nil {
					profilerErr = err
					cancel(err)
				}
			default:
			}
		}
	}()
	initDone := make(chan struct{})
	serveDone := make(chan struct{})
	var cleanupOnce sync.Once
	cleanupDone := make(chan struct{})
	var cleanupErr error
	cleanup := func() {
		cleanupOnce.Do(func() {
			cancel(errDiagnosticsStopped)
			go func() {
				defer close(cleanupDone)
				shutdownCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
				defer done()
				// Profiler shutdown must run even if application initialization or Stop
				// ignores cancellation. Both share the same total shutdown budget.
				profilerDone := make(chan error, 1)
				go func() { profilerDone <- server.Shutdown(shutdownCtx) }()
				appDone := make(chan error, 1)
				go func() {
					<-initDone
					var err error
					if hooks.Stop != nil {
						err = hooks.Stop(shutdownCtx)
					}
					<-serveDone
					appDone <- err
				}()
				for appDone != nil || profilerDone != nil {
					select {
					case err := <-appDone:
						cleanupErr = errors.Join(cleanupErr, err)
						appDone = nil
					case err := <-profilerDone:
						cleanupErr = errors.Join(cleanupErr, err)
						profilerDone = nil
					case <-shutdownCtx.Done():
						cleanupErr = errors.Join(cleanupErr, shutdownCtx.Err())
						return
					}
				}
			}()
		})
		<-cleanupDone
	}
	defer func() {
		cleanup()
		close(monitorStop)
		<-monitorDone
		if profilerErr != nil {
			result = profilerErr
		}
		if result == nil {
			result = cleanupErr
		}
	}()
	if registerStop != nil {
		registerStop(cleanup)
	}
	err = hooks.Init(ctx)
	close(initDone)
	if err != nil || ctx.Err() != nil {
		close(serveDone)
		if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, errDiagnosticsStopped) {
			return cause
		}
		return err
	}
	served := make(chan error, 1)
	go func() { defer close(serveDone); served <- hooks.Serve() }()
	select {
	case err = <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		cause := context.Cause(ctx)
		if errors.Is(cause, errDiagnosticsStopped) {
			return nil
		}
		return cause
	}
}
