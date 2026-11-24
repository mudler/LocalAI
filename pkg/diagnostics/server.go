// SPDX-License-Identifier: MIT
package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	runtimepprof "runtime/pprof"
	"sync"
	"time"
)

type samplingControl interface {
	SetMutexProfileFraction(int) int
	SetBlockProfileRate(int)
}
type runtimeSampling struct{}

func (runtimeSampling) SetMutexProfileFraction(v int) int { return runtime.SetMutexProfileFraction(v) }
func (runtimeSampling) SetBlockProfileRate(v int)         { runtime.SetBlockProfileRate(v) }

// Server owns a private listener and process-global sampling. The run process
// must have only one sampling owner; prior runtime rates are not restored.
type Server struct {
	httpServer   *http.Server
	addr         net.Addr
	errors       chan error
	done         chan struct{}
	sampling     samplingControl
	resetOnce    sync.Once
	shutdownOnce sync.Once
	shutdownErr  error
}

// Start validates and binds before returning. Disabled profiling is inert.
func Start(opts Options) (*Server, error) { return start(opts, net.Listen, runtimeSampling{}) }

func start(opts Options, listen func(string, string) (net.Listener, error), sampling samplingControl) (*Server, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	if !opts.Pprof {
		return &Server{}, nil
	}
	listener, err := listen("tcp", opts.Address)
	if err != nil {
		return nil, fmt.Errorf("bind diagnostics listener: %w", err)
	}
	mux := http.NewServeMux()
	// Index dispatches subtree requests, so deny cmdline explicitly at both paths.
	mux.HandleFunc("/debug/pprof/cmdline", http.NotFound)
	mux.HandleFunc("/debug/pprof/cmdline/", http.NotFound)
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	for _, profile := range runtimepprof.Profiles() {
		mux.Handle("/debug/pprof/"+profile.Name(), pprof.Handler(profile.Name()))
	}
	s := &Server{
		httpServer: &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20},
		addr:       listener.Addr(), errors: make(chan error, 1), done: make(chan struct{}), sampling: sampling,
	}
	sampling.SetMutexProfileFraction(opts.MutexProfileFraction)
	sampling.SetBlockProfileRate(opts.BlockProfileRate)
	go func() {
		defer close(s.done)
		defer close(s.errors)
		if err := s.httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Stop active connections and sampling even if the caller has not yet
			// consumed the buffered fatal error.
			_ = s.httpServer.Close()
			s.resetSampling()
			s.errors <- err
		}
	}()
	return s, nil
}

func (s *Server) resetSampling() {
	s.resetOnce.Do(func() {
		s.sampling.SetMutexProfileFraction(0)
		s.sampling.SetBlockProfileRate(0)
	})
}

// Errors delivers one unexpected serving failure, then closes. Normal shutdown
// closes it without a value; disabled servers return nil.
func (s *Server) Errors() <-chan error { return s.errors }
func (s *Server) Addr() net.Addr       { return s.addr }

// Shutdown drains requests, force-closing on expiry. Repeated calls return the
// first result and wait for the serving goroutine to finish.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	s.shutdownOnce.Do(func() {
		s.shutdownErr = s.httpServer.Shutdown(ctx)
		if s.shutdownErr != nil {
			_ = s.httpServer.Close()
		}
		<-s.done
		s.resetSampling()
	})
	return s.shutdownErr
}
