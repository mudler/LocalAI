// SPDX-License-Identifier: MIT
package cli

import (
	"net"
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/diagnostics"
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
