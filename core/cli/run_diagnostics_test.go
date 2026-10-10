// SPDX-License-Identifier: MIT
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

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

var _ = Describe("Diagnostics inline startup", Serial, func() {
	fixture := func() *RunCMD {
		dir := GinkgoT().TempDir()
		return &RunCMD{
			PprofAddress: diagnostics.DefaultOptions().Address,
			ModelsPath:   filepath.Join(dir, "models"), BackendsPath: filepath.Join(dir, "backends"),
			BackendsSystemPath: filepath.Join(dir, "system"), DataPath: filepath.Join(dir, "data"),
			Galleries: "[]", BackendGalleries: "[]",
			// Invalid JSON fails application configuration without starting services.
			PreloadModels: "not-json",
		}
	}
	It("validates before startup side effects even in preload mode", func() {
		r := &RunCMD{PreloadBackendOnly: true, PprofAddress: "0.0.0.0:6060"}
		Expect(r.Run(nil)).To(MatchError(r.diagnosticsOptions().Validate()))
	})
	It("reports a busy profiler before application initialization", func() {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer l.Close()
		r := fixture()
		r.Pprof, r.PprofAddress = true, l.Addr().String()
		Expect(r.Run(&cliContext.Context{CredentialsFile: "unused"})).To(MatchError(ContainSubstring("bind diagnostics listener")))
	})
	It("does not bind profiling for preload or independent timing", func() {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer l.Close()
		for _, preload := range []bool{true, false} {
			r := fixture()
			r.PreloadBackendOnly, r.Pprof = preload, preload
			r.RequestPhaseTiming, r.PprofAddress = true, l.Addr().String()
			err := r.Run(&cliContext.Context{CredentialsFile: "unused"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("bind diagnostics listener"))
		}
	})
	It("releases profiling after real application configuration failure", func() {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		address := l.Addr().String()
		Expect(l.Close()).To(Succeed())
		r := fixture()
		r.Pprof, r.PprofAddress = true, address
		Expect(r.Run(&cliContext.Context{CredentialsFile: "unused"})).To(MatchError(ContainSubstring("LocalAI failed to start")))
		l, err = net.Listen("tcp", address)
		Expect(err).NotTo(HaveOccurred())
		Expect(l.Close()).To(Succeed())
	})
})

// Fail the real private listener only once Run has constructed the public API.
// The subprocess isolates the logger, signal registrations and socket operation.
type diagnosticsListenerFailure struct {
	slog.Handler
	port       int
	failed     chan error
	canceled   chan struct{}
	cancelOnce sync.Once
}

func (h *diagnosticsListenerFailure) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == "Context canceled, shutting down" {
		h.cancelOnce.Do(func() { close(h.canceled) })
	}
	if record.Message == "LocalAI is started and running" {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			h.failed <- err
			return nil
		}
		for _, entry := range entries {
			fd, err := strconv.Atoi(entry.Name())
			if err != nil {
				continue
			}
			address, err := syscall.Getsockname(fd)
			if err != nil {
				continue
			}
			if address, ok := address.(*syscall.SockaddrInet4); ok && address.Port == h.port {
				if err := syscall.Shutdown(fd, syscall.SHUT_RDWR); err != nil {
					h.failed <- err
					return nil
				}
				// Application's cancellation log acknowledges the real run context,
				// without sleeps or a production lifecycle injection point.
				select {
				case <-h.canceled:
					h.failed <- nil
				case <-time.After(5 * time.Second):
					h.failed <- fmt.Errorf("run context was not canceled")
				}
				return nil
			}
		}
		h.failed <- fmt.Errorf("private listener not found")
	}
	return nil
}

var _ = Describe("Diagnostics concurrent public bind failure", Serial, func() {
	It("retains the occupied API port error alongside cancellation", func() {
		if runtime.GOOS != "linux" {
			Skip("socket failure fixture uses /proc/self/fd")
		}
		if os.Getenv("LOCALAI_DIAGNOSTICS_BIND_CHILD") != "1" {
			executable, err := os.Executable()
			Expect(err).NotTo(HaveOccurred())
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=TestCLI", "-ginkgo.focus=Diagnostics concurrent public bind failure", "-ginkgo.fail-on-empty")
			cmd.Env = append(os.Environ(), "LOCALAI_DIAGNOSTICS_BIND_CHILD=1")
			output, err := cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(output))
			return
		}
		occupied, err := net.Listen("tcp4", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer occupied.Close()
		private, err := net.Listen("tcp4", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		address := private.Addr().String()
		port := private.Addr().(*net.TCPAddr).Port
		Expect(private.Close()).To(Succeed())
		failure := &diagnosticsListenerFailure{Handler: slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}), port: port, failed: make(chan error, 1), canceled: make(chan struct{})}
		xlog.SetLogger(xlog.NewLoggerWithHandler(failure, xlog.LogLevelDebug))
		dir := GinkgoT().TempDir()
		r := &RunCMD{
			Pprof: true, PprofAddress: address, Address: occupied.Addr().String(),
			ModelsPath: filepath.Join(dir, "models"), BackendsPath: filepath.Join(dir, "backends"),
			BackendsSystemPath: filepath.Join(dir, "system"), DataPath: filepath.Join(dir, "data"),
			GeneratedContentPath: filepath.Join(dir, "generated"), UploadPath: filepath.Join(dir, "uploads"),
			Galleries: "[]", BackendGalleries: "[]", DisableAgents: true, DisableLocalAIAssistant: true,
			DisableMCP: true, DisableMetricsEndpoint: true, DisableWebUI: true,
		}
		err = r.Run(&cliContext.Context{CredentialsFile: "unused"})
		Expect(failure.failed).To(Receive(BeNil()))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("accept"))
		Expect(err.Error()).To(ContainSubstring("address already in use"))
	})
})
