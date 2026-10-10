// SPDX-License-Identifier: MIT
package cli

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/alecthomas/kong"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeDiagnosticsServer struct {
	failures chan error
	stopped  chan struct{}
}

func (s *fakeDiagnosticsServer) Errors() <-chan error { return s.failures }
func (s *fakeDiagnosticsServer) Shutdown(ctx context.Context) error {
	close(s.stopped)
	return ctx.Err()
}

var _ = Describe("Diagnostics CLI", Serial, func() {
	parse := func(args ...string) (*RunCMD, error) {
		command := struct {
			cliContext.Context `embed:""`
			Run                RunCMD `cmd:"" default:"withargs"`
		}{}
		r := &command.Run
		// Match cmd/local-ai/main.go, including gallery substitutions. Parsing
		// must fail for the supplied value, not an unrelated missing variable.
		p, err := kong.New(&command, kong.Vars{
			"basepath":             kong.ExpandPath("."),
			"generatedcontentpath": DefaultGeneratedContentPath(),
			"uploadpath":           DefaultUploadPath(),
			"galleries":            config.DefaultGalleriesJSON,
			"backends":             config.DefaultBackendGalleriesJSON,
		})
		if err != nil {
			return r, err
		}
		_, err = p.Parse(args)
		return r, err
	}
	BeforeEach(func() {
		for _, key := range []string{"LOCALAI_PPROF", "LOCALAI_PPROF_ADDRESS", "LOCALAI_PPROF_MUTEX_PROFILE_FRACTION", "LOCALAI_PPROF_BLOCK_PROFILE_RATE", "LOCALAI_REQUEST_PHASE_TIMING"} {
			value, present := os.LookupEnv(key)
			DeferCleanup(func() {
				if present {
					Expect(os.Setenv(key, value)).To(Succeed())
				} else {
					Expect(os.Unsetenv(key)).To(Succeed())
				}
			})
			Expect(os.Unsetenv(key)).To(Succeed())
		}
	})
	It("defaults to inactive independently of debug", func() {
		for _, args := range [][]string{nil, {"--debug"}, {"--log-level=debug"}} {
			r, err := parse(args...)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.diagnosticsOptions()).To(Equal(diagnostics.DefaultOptions()))
		}
	})
	It("maps all five flags", func() {
		r, err := parse("--pprof", "--pprof-address=[::1]:6061", "--pprof-mutex-profile-fraction=7", "--pprof-block-profile-rate=123", "--request-phase-timing")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.diagnosticsOptions()).To(Equal(diagnostics.Options{Pprof: true, Address: "[::1]:6061", MutexProfileFraction: 7, BlockProfileRate: 123, RequestPhaseTiming: true}))
	})
	It("maps all environment settings and explicit false", func() {
		GinkgoT().Setenv("LOCALAI_PPROF", "true")
		GinkgoT().Setenv("LOCALAI_PPROF_ADDRESS", "127.0.0.2:6062")
		GinkgoT().Setenv("LOCALAI_PPROF_MUTEX_PROFILE_FRACTION", "3")
		GinkgoT().Setenv("LOCALAI_PPROF_BLOCK_PROFILE_RATE", "42")
		GinkgoT().Setenv("LOCALAI_REQUEST_PHASE_TIMING", "true")
		r, err := parse()
		Expect(err).NotTo(HaveOccurred())
		Expect(r.diagnosticsOptions()).To(Equal(diagnostics.Options{Pprof: true, Address: "127.0.0.2:6062", MutexProfileFraction: 3, BlockProfileRate: 42, RequestPhaseTiming: true}))
		GinkgoT().Setenv("LOCALAI_PPROF", "false")
		GinkgoT().Setenv("LOCALAI_REQUEST_PHASE_TIMING", "false")
		r, err = parse()
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Pprof).To(BeFalse())
		Expect(r.RequestPhaseTiming).To(BeFalse())
	})
	It("rejects malformed and overflowing rates", func() {
		for _, arg := range []string{"--pprof-block-profile-rate=no", "--pprof-mutex-profile-fraction=999999999999999999999999"} {
			_, err := parse(arg)
			Expect(err).To(HaveOccurred())
		}
		GinkgoT().Setenv("LOCALAI_PPROF", "not-bool")
		_, err := parse()
		Expect(err).To(HaveOccurred())
	})
	It("validates disabled addresses and conflicting rates", func() {
		for _, arg := range []string{"--pprof-address=0.0.0.0:6060", "--pprof-block-profile-rate=-1", "--pprof-mutex-profile-fraction=1"} {
			r, err := parse(arg)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.diagnosticsOptions().Validate()).To(HaveOccurred())
		}
	})
	It("stores one optional startup recorder", func() {
		Expect(config.NewApplicationConfig().DiagnosticsRecorder).To(BeNil())
		recorder := diagnostics.NewRecorder(nil)
		Expect(config.NewApplicationConfig(config.WithDiagnosticsRecorder(recorder)).DiagnosticsRecorder).To(BeIdenticalTo(recorder))
	})
})

var _ = Describe("Diagnostics lifecycle", func() {
	It("rejects a busy profiler before initialization", func() {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer l.Close()
		opts := diagnostics.DefaultOptions()
		opts.Pprof = true
		opts.Address = l.Addr().String()
		err = runWithDiagnostics(context.Background(), opts, diagnosticsRunHooks{Init: func(context.Context) error { Fail("initialized before bind"); return nil }}, nil)
		Expect(err).To(HaveOccurred())
	})
	It("cleans up failed initialization once using a fresh bounded context", func() {
		cause := errors.New("init failed")
		var stop func()
		var calls int
		err := runWithDiagnostics(context.Background(), diagnostics.DefaultOptions(), diagnosticsRunHooks{
			Init: func(context.Context) error { return cause },
			Stop: func(ctx context.Context) error {
				calls++
				Expect(ctx.Err()).NotTo(HaveOccurred())
				deadline, ok := ctx.Deadline()
				Expect(ok).To(BeTrue())
				Expect(time.Until(deadline)).To(BeNumerically("<=", 5*time.Second))
				return nil
			},
		}, func(fn func()) { stop = fn })
		Expect(err).To(MatchError(cause))
		stop()
		Expect(calls).To(Equal(1))
	})
	It("retains API errors and joins serving before return", func() {
		cause := errors.New("api bind failed")
		var stopped atomic.Int32
		err := runWithDiagnostics(context.Background(), diagnostics.DefaultOptions(), diagnosticsRunHooks{Init: func(context.Context) error { return nil }, Serve: func() error { return cause }, Stop: func(context.Context) error { stopped.Add(1); return nil }}, nil)
		Expect(err).To(MatchError(cause))
		Expect(stopped.Load()).To(Equal(int32(1)))
	})
	It("runs actual cleanup in the signal callback and tolerates closed error channels", func() {
		s := &fakeDiagnosticsServer{make(chan error), make(chan struct{})}
		close(s.failures)
		registered := make(chan func(), 1)
		serving := make(chan struct{})
		stopped := make(chan struct{})
		result := make(chan error, 1)
		go func() {
			result <- runWithDiagnosticsStart(context.Background(), diagnostics.DefaultOptions(), diagnosticsRunHooks{Init: func(context.Context) error { return nil }, Serve: func() error { close(serving); <-stopped; return http.ErrServerClosed }, Stop: func(context.Context) error { close(stopped); return nil }}, func(fn func()) { registered <- fn }, func(diagnostics.Options) (diagnosticsServer, error) { return s, nil })
		}()
		stop := <-registered
		<-serving
		stop()
		Expect(stopped).To(BeClosed())
		Expect(s.stopped).To(BeClosed())
		Eventually(result).Should(Receive(BeNil()))
		stop()
	})
	for _, early := range []bool{true, false} {
		early := early
		It("retains profiler failure and cancels owned context (early="+map[bool]string{true: "true", false: "false"}[early]+")", func() {
			cause := errors.New("profiler accept failed")
			s := &fakeDiagnosticsServer{make(chan error, 1), make(chan struct{})}
			var ctx context.Context
			var stops atomic.Int32
			hooks := diagnosticsRunHooks{Init: func(c context.Context) error {
				ctx = c
				if early {
					s.failures <- cause
					close(s.failures)
					<-c.Done()
					return c.Err()
				}
				return nil
			}, Serve: func() error { s.failures <- cause; close(s.failures); <-ctx.Done(); return http.ErrServerClosed }, Stop: func(context.Context) error { stops.Add(1); return nil }}
			err := runWithDiagnosticsStart(context.Background(), diagnostics.DefaultOptions(), hooks, nil, func(diagnostics.Options) (diagnosticsServer, error) { return s, nil })
			Expect(err).To(MatchError(cause))
			Expect(stops.Load()).To(Equal(int32(1)))
			Expect(s.stopped).To(BeClosed())
		})
	}
	It("cancels initialization on parent cancellation", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := runWithDiagnostics(ctx, diagnostics.DefaultOptions(), diagnosticsRunHooks{Init: func(c context.Context) error { cancel(); <-c.Done(); return c.Err() }, Stop: func(context.Context) error { return nil }}, nil)
		Expect(err).To(MatchError(context.Canceled))
	})
	It("allows timing without opening the busy profiling port", func() {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer l.Close()
		opts := diagnostics.DefaultOptions()
		opts.Address = l.Addr().String()
		opts.RequestPhaseTiming = true
		Expect(runWithDiagnostics(context.Background(), opts, diagnosticsRunHooks{Init: func(context.Context) error { return nil }, Serve: func() error { return http.ErrServerClosed }, Stop: func(context.Context) error { return nil }}, nil)).To(Succeed())
	})
})

var _ = Describe("Diagnostics preload and shutdown boundaries", func() {
	It("validates preload without binding or stopping preloaded backends", func() {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer listener.Close()
		r := &RunCMD{PreloadBackendOnly: true, Pprof: true, PprofAddress: listener.Addr().String(), PprofMutexProfileFraction: 1}
		calls := 0
		hooks := diagnosticsRunHooks{
			Init:  func(ctx context.Context) error { calls++; Expect(ctx.Err()).NotTo(HaveOccurred()); return nil },
			Serve: func() error { Fail("preload served API"); return nil },
			Stop:  func(context.Context) error { Fail("preload stopped backends"); return nil },
		}
		Expect(r.runConfiguredDiagnostics(context.Background(), hooks, func(func()) { Fail("preload registered server cleanup") })).To(Succeed())
		Expect(calls).To(Equal(1))
		r.PprofAddress = "0.0.0.0:6060"
		Expect(r.runConfiguredDiagnostics(context.Background(), hooks, nil)).NotTo(Succeed())
		Expect(calls).To(Equal(1))
	})
	It("retains preload initialization errors", func() {
		cause := errors.New("preload failed")
		r := &RunCMD{PreloadBackendOnly: true, PprofAddress: diagnostics.DefaultOptions().Address}
		Expect(r.runConfiguredDiagnostics(context.Background(), diagnosticsRunHooks{
			Init: func(context.Context) error { return cause },
		}, nil)).To(MatchError(cause))
	})
	It("validates before startup side effects even in preload mode", func() {
		r := &RunCMD{PreloadBackendOnly: true, PprofAddress: "0.0.0.0:6060"}
		// A nil CLI context would panic at credentials loading if validation moved later.
		Expect(r.Run(nil)).To(MatchError(r.diagnosticsOptions().Validate()))
	})
	It("waits for initialization before Stop and joins Serve with nil profiler errors", func() {
		s := &fakeDiagnosticsServer{nil, make(chan struct{})}
		entered := make(chan struct{})
		release := make(chan struct{})
		stopped := make(chan struct{})
		registered := make(chan func(), 1)
		result := make(chan error, 1)
		var stops atomic.Int32
		go func() {
			result <- runWithDiagnosticsStart(context.Background(), diagnostics.DefaultOptions(), diagnosticsRunHooks{
				Init:  func(ctx context.Context) error { close(entered); <-ctx.Done(); <-release; return ctx.Err() },
				Serve: func() error { return errors.New("must not serve canceled initialization") },
				Stop:  func(context.Context) error { stops.Add(1); close(stopped); return nil },
			}, func(fn func()) { registered <- fn }, func(diagnostics.Options) (diagnosticsServer, error) { return s, nil })
		}()
		stop := <-registered
		<-entered
		callbackDone := make(chan struct{})
		go func() { stop(); close(callbackDone) }()
		Eventually(s.stopped).Should(BeClosed())
		Expect(stops.Load()).To(BeZero())
		close(release)
		Eventually(callbackDone).Should(BeClosed())
		Eventually(result).Should(Receive())
		Expect(stopped).To(BeClosed())
		stop()
		Expect(stops.Load()).To(Equal(int32(1)))
	})
	It("does not return until serving unwinds after Stop", func() {
		s := &fakeDiagnosticsServer{nil, make(chan struct{})}
		serving := make(chan struct{})
		stopped := make(chan struct{})
		release := make(chan struct{})
		registered := make(chan func(), 1)
		result := make(chan error, 1)
		go func() {
			result <- runWithDiagnosticsStart(context.Background(), diagnostics.DefaultOptions(), diagnosticsRunHooks{
				Init:  func(context.Context) error { return nil },
				Serve: func() error { close(serving); <-release; return http.ErrServerClosed },
				Stop:  func(context.Context) error { close(stopped); return nil },
			}, func(fn func()) { registered <- fn }, func(diagnostics.Options) (diagnosticsServer, error) { return s, nil })
		}()
		stop := <-registered
		<-serving
		callbackDone := make(chan struct{})
		go func() { stop(); close(callbackDone) }()
		Eventually(stopped).Should(BeClosed())
		Expect(result).NotTo(Receive())
		Expect(callbackDone).NotTo(BeClosed())
		close(release)
		Eventually(callbackDone).Should(BeClosed())
		Eventually(result).Should(Receive(BeNil()))
	})
	It("bounds signal cleanup while initialization ignores cancellation", func() {
		s := &fakeDiagnosticsServer{nil, make(chan struct{})}
		entered := make(chan struct{})
		release := make(chan struct{})
		stopped := make(chan struct{})
		registered := make(chan func(), 1)
		result := make(chan error, 1)
		go func() {
			result <- runWithDiagnosticsStart(context.Background(), diagnostics.DefaultOptions(), diagnosticsRunHooks{
				Init: func(context.Context) error { close(entered); <-release; return nil },
				Stop: func(context.Context) error { close(stopped); return nil },
			}, func(fn func()) { registered <- fn }, func(diagnostics.Options) (diagnosticsServer, error) { return s, nil })
		}()
		stop := <-registered
		<-entered
		callbackDone := make(chan struct{})
		go func() { stop(); close(callbackDone) }()
		Eventually(s.stopped).Should(BeClosed())
		// Exercise the real five-second budget, not a shortened test-only timeout.
		Eventually(callbackDone, 7*time.Second).Should(BeClosed())
		Expect(stopped).NotTo(BeClosed())
		close(release)
		Eventually(result).Should(Receive(MatchError(context.DeadlineExceeded)))
		Eventually(stopped).Should(BeClosed())
	})
})
