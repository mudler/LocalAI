package gallery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/model/backendtest"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// passStartCheck makes the start check of an upgrade pass for the current
// spec. Specs of the swap and the metadata use it: their builds are scripts
// that print a line and exit, not backends.
func passStartCheck() {
	newUpgradeStartCheck = func(*model.ModelLoader, string) StartCheck {
		return func(context.Context, string) error { return nil }
	}
	DeferCleanup(func() { newUpgradeStartCheck = LoaderStartCheck })
}

var _ = Describe("Rollback of a backend upgrade whose new build fails", func() {
	var (
		dir         string
		pidDir      string
		backends    string
		systemState *system.SystemState
		galleries   []config.Gallery
		ml          *model.ModelLoader
	)

	const name = "my-backend"

	// install puts build v1 (a backend that serves) in place with its
	// metadata, as a previous install would.
	install := func() {
		Expect(backendtest.WriteBuild(filepath.Join(backends, name), backendtest.Serve, "v1", pidDir)).To(Succeed())
		data, err := json.Marshal(BackendMetadata{Name: name, Version: "1.0.0", InstalledAt: time.Now().Format(time.RFC3339)})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(backends, name, "metadata.json"), data, 0o600)).To(Succeed())
		Expect(RegisterBackends(systemState, ml)).To(Succeed())
	}
	// publish makes the gallery offer build v2 in mode.
	publish := func(mode backendtest.Mode) {
		src := filepath.Join(dir, "v2-source")
		Expect(backendtest.WriteBuild(src, mode, "v2", pidDir)).To(Succeed())
		data, err := yaml.Marshal([]GalleryBackend{{Metadata: Metadata{Name: name}, URI: src, Version: "2.0.0"}})
		Expect(err).NotTo(HaveOccurred())
		galleryFile := filepath.Join(backends, "gallery.yaml")
		Expect(os.WriteFile(galleryFile, data, 0o600)).To(Succeed())
		galleries = []config.Gallery{{Name: "test", URL: "file://" + galleryFile}}
	}
	upgrade := func(ctx context.Context) error {
		return UpgradeBackend(ctx, systemState, ml, galleries, name, nil, false)
	}
	installedVersion := func() string {
		b, err := os.ReadFile(filepath.Join(backends, name, "version"))
		Expect(err).NotTo(HaveOccurred())
		return string(b)
	}
	installedMetadata := func() string {
		meta, err := readBackendMetadata(filepath.Join(backends, name))
		Expect(err).NotTo(HaveOccurred())
		return meta.Version
	}
	expectNoLeftovers := func() {
		GinkgoHelper()
		for _, suffix := range []string{".backup", ".failed", ".upgrade-tmp"} {
			Expect(filepath.Join(backends, name+suffix)).NotTo(BeAnExistingFile(), suffix)
		}
	}
	expectNoProcess := func() {
		GinkgoHelper()
		Eventually(func() []int { return backendtest.LivePIDs(pidDir) }, "10s", "100ms").Should(BeEmpty(), "the start check left a process running")
	}
	expectRegistered := func() {
		GinkgoHelper()
		Expect(ml.GetExternalBackend(name)).To(Equal(filepath.Join(backends, name, "run.sh")))
	}

	BeforeEach(func() {
		if runtime.GOOS == "windows" {
			Skip("the fixture backends are shell scripts, which Windows cannot execute")
		}
		dir = GinkgoT().TempDir()
		pidDir = filepath.Join(dir, "pids")
		backends = filepath.Join(dir, "backends")
		Expect(os.MkdirAll(pidDir, 0o750)).To(Succeed())
		Expect(os.MkdirAll(backends, 0o750)).To(Succeed())
		var err error
		systemState, err = system.GetSystemState(system.WithBackendPath(backends))
		Expect(err).NotTo(HaveOccurred())
		ml = model.NewModelLoader(systemState)
		startCheckTimeout = 5 * time.Second
		DeferCleanup(func() {
			startCheckTimeout = model.DefaultStartCheckTimeout
			renameBackend = os.Rename
			writeUpgradeMetadata = writeBackendMetadata
			registerBackends = RegisterBackends
			newUpgradeStartCheck = LoaderStartCheck
		})
		install()
	})

	It("keeps a new build that starts and removes the previous one", func() {
		publish(backendtest.Serve)
		Expect(upgrade(context.Background())).To(Succeed())
		Expect(installedVersion()).To(Equal("v2"))
		Expect(installedMetadata()).To(Equal("2.0.0"))
		expectRegistered()
		expectNoLeftovers()
		expectNoProcess()
		Expect(backendtest.LivePIDs(pidDir)).To(BeEmpty())
		entries, err := os.ReadDir(pidDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1), "the new build was started once, by the check")
	})

	It("keeps the previous build until the check passes", func() {
		publish(backendtest.Serve)
		var sawBackup bool
		newUpgradeStartCheck = func(*model.ModelLoader, string) StartCheck {
			return func(_ context.Context, runFile string) error {
				Expect(runFile).To(Equal(filepath.Join(backends, name, "run.sh")), "the check runs the final path")
				_, err := os.Stat(filepath.Join(backends, name+".backup", "version"))
				sawBackup = err == nil
				return nil
			}
		}
		Expect(upgrade(context.Background())).To(Succeed())
		Expect(sawBackup).To(BeTrue())
		expectNoLeftovers()
	})

	It("restores the previous build when the new one exits, with the end of its output", func() {
		publish(backendtest.Exit)
		err := upgrade(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(UpgradeCodeOf(err)).To(Equal(UpgradeFailureStart))
		Expect(errors.Is(err, ErrUpgradeStart)).To(BeTrue())
		Expect(UpgradeRolledBack(err)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring(backendtest.ExitMessage))
		Expect(err.Error()).To(ContainSubstring("exit code 3"))
		// The code says that the new build did not start, and every reader
		// words the code; the text is the bare cause, so no reader says it twice.
		Expect(err.Error()).NotTo(ContainSubstring("did not start"))
		var startErr *model.StartCheckError
		Expect(errors.As(err, &startErr)).To(BeTrue())
		Expect(len(startErr.Output)).To(BeNumerically("<=", 512))

		Expect(installedVersion()).To(Equal("v1"))
		Expect(installedMetadata()).To(Equal("1.0.0"))
		expectRegistered()
		expectNoLeftovers()
		expectNoProcess()
	})

	It("restores the previous build when the new one never answers", func() {
		publish(backendtest.Hang)
		startCheckTimeout = time.Second
		err := upgrade(context.Background())
		Expect(UpgradeCodeOf(err)).To(Equal(UpgradeFailureStart))
		Expect(UpgradeRolledBack(err)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("did not answer a health check within 1s"))
		Expect(installedVersion()).To(Equal("v1"))
		expectNoLeftovers()
		expectNoProcess()
	})

	It("stops the check and restores the previous build when the context ends", func() {
		publish(backendtest.Hang)
		startCheckTimeout = time.Minute
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer GinkgoRecover()
			Eventually(func() []int { return backendtest.LivePIDs(pidDir) }, "10s", "20ms").ShouldNot(BeEmpty())
			cancel()
		}()
		started := time.Now()
		err := upgrade(ctx)
		Expect(errors.Is(err, context.Canceled)).To(BeTrue(), "a stop keeps its own error: %v", err)
		Expect(time.Since(started)).To(BeNumerically("<", 30*time.Second))
		Expect(installedVersion()).To(Equal("v1"))
		Expect(installedMetadata()).To(Equal("1.0.0"))
		expectNoLeftovers()
		expectNoProcess()
	})

	It("rolls back when the metadata of the new build cannot be written", func() {
		publish(backendtest.Serve)
		writeUpgradeMetadata = func(string, *BackendMetadata) error { return errors.New("disk full") }
		err := upgrade(context.Background())
		Expect(UpgradeCodeOf(err)).To(Equal(UpgradeFailureSwap))
		Expect(UpgradeRolledBack(err)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("disk full"))
		Expect(installedVersion()).To(Equal("v1"))
		Expect(installedMetadata()).To(Equal("1.0.0"))
		expectRegistered()
		expectNoLeftovers()
	})

	It("rolls back when the backends cannot be registered after the swap", func() {
		publish(backendtest.Serve)
		calls := 0
		registerBackends = func(s *system.SystemState, l *model.ModelLoader) error {
			calls++
			if calls == 1 {
				return errors.New("listing failed")
			}
			return RegisterBackends(s, l)
		}
		err := upgrade(context.Background())
		Expect(UpgradeCodeOf(err)).To(Equal(UpgradeFailureSwap))
		Expect(UpgradeRolledBack(err)).To(BeTrue())
		Expect(calls).To(Equal(2), "the restored build is registered again")
		Expect(installedVersion()).To(Equal("v1"))
		expectRegistered()
		expectNoLeftovers()
		expectNoProcess()
	})

	It("says so, and keeps both builds, when the previous build cannot be restored", func() {
		publish(backendtest.Exit)
		renameBackend = func(from, to string) error {
			if filepath.Ext(from) == ".backup" {
				return errors.New("rename refused")
			}
			return os.Rename(from, to)
		}
		err := upgrade(context.Background())
		Expect(UpgradeCodeOf(err)).To(Equal(UpgradeFailureStart))
		Expect(UpgradeRolledBack(err)).To(BeFalse())
		Expect(err.Error()).To(ContainSubstring("could not be restored"))
		Expect(filepath.Join(backends, name+".backup", "version")).To(BeAnExistingFile(), "the previous build is kept")
		Expect(installedVersion()).To(Equal("v2"), "the failed build stays in place for diagnosis")
		expectNoProcess()
	})

	It("does not list the directories of a swap as backends", func() {
		for _, suffix := range []string{".backup", ".upgrade-tmp", ".install-tmp", ".install-backup", ".failed"} {
			side := filepath.Join(backends, name+suffix)
			Expect(os.MkdirAll(side, 0o750)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(side, "metadata.json"), []byte("not json"), 0o600)).To(Succeed())
		}
		listed, err := ListSystemBackends(systemState)
		Expect(err).NotTo(HaveOccurred(), "an unreadable metadata file of a swap directory does not fail the listing")
		Expect(listed.Exists(name)).To(BeTrue())
		for key := range listed {
			Expect(isSwapDir(key)).To(BeFalse(), key)
		}
	})

	Describe("a forced install with a start check", func() {
		forced := func(mode backendtest.Mode) error {
			src := filepath.Join(dir, "forced-source")
			Expect(backendtest.WriteBuild(src, mode, "v2", pidDir)).To(Succeed())
			return InstallBackend(context.Background(), systemState, ml, &GalleryBackend{Metadata: Metadata{Name: name}, URI: src}, nil, false,
				WithStartCheck(LoaderStartCheck(ml, name)))
		}

		It("puts the replaced build back when the new one does not start", func() {
			err := forced(backendtest.Exit)
			Expect(UpgradeCodeOf(err)).To(Equal(UpgradeFailureStart))
			Expect(UpgradeRolledBack(err)).To(BeTrue())
			Expect(err.Error()).NotTo(ContainSubstring("did not start"), "the code says it; the text is the cause")
			Expect(installedVersion()).To(Equal("v1"))
			Expect(installedMetadata()).To(Equal("1.0.0"))
			expectRegistered()
			Expect(filepath.Join(backends, name+".install-backup")).NotTo(BeAnExistingFile())
			Expect(filepath.Join(backends, name+".failed")).NotTo(BeAnExistingFile())
			expectNoProcess()
		})

		It("replaces the build when the new one starts", func() {
			Expect(forced(backendtest.Serve)).To(Succeed())
			Expect(installedVersion()).To(Equal("v2"))
			Expect(filepath.Join(backends, name+".install-backup")).NotTo(BeAnExistingFile())
			expectNoProcess()
		})

		It("runs no check on a first install", func() {
			Expect(os.RemoveAll(filepath.Join(backends, name))).To(Succeed())
			Expect(forced(backendtest.Exit)).To(Succeed(), "nothing was replaced, so nothing is checked")
			entries, err := os.ReadDir(pidDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(BeEmpty())
		})
	})
})
