package cli

import (
	"path/filepath"
	"runtime"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/system"
)

// The default folders of every command come from KongVars. On Linux and macOS
// they must stay exactly what earlier releases used: the current folder and
// /var/lib/local-ai/backends.
var _ = Describe("default folders of the commands", func() {
	type commands struct {
		Run      RunCMD      `cmd:"" default:"withargs"`
		Backends BackendsCMD `cmd:""`
		Models   ModelsCMD   `cmd:""`
		Worker   WorkerCMD   `cmd:""`
	}

	parse := func(args ...string) *commands {
		var c commands
		k, err := kong.New(&c, KongVars(system.ResolveDefaultDirs()))
		Expect(err).NotTo(HaveOccurred())
		_, err = k.Parse(args)
		Expect(err).NotTo(HaveOccurred())
		return &c
	}

	BeforeEach(func() {
		if runtime.GOOS == "windows" {
			Skip("Windows uses the standard folders; see pkg/system")
		}
		GinkgoT().Setenv(system.BasePathEnv, "")
	})

	It("keeps the folders of earlier releases on Linux and macOS", func() {
		wd := kong.ExpandPath(".")
		c := parse("run")
		Expect(c.Run.ModelsPath).To(Equal(filepath.Join(wd, "models")))
		Expect(c.Run.BackendsPath).To(Equal(filepath.Join(wd, "backends")))
		Expect(c.Run.BackendsSystemPath).To(Equal("/var/lib/local-ai/backends"))
		Expect(c.Run.DownloadStagingPath).To(Equal(filepath.Join(wd, "downloading")))
		Expect(c.Run.DataPath).To(Equal(filepath.Join(wd, "data")))
		Expect(c.Run.LocalaiConfigDir).To(Equal(filepath.Join(wd, "configuration")))
		Expect(c.Run.GeneratedContentPath).To(Equal(DefaultGeneratedContentPath()))
		Expect(c.Run.UploadPath).To(Equal(DefaultUploadPath()))

		b := parse("backends", "list")
		Expect(b.Backends.List.BackendsPath).To(Equal(filepath.Join(wd, "backends")))
		Expect(b.Backends.List.BackendsSystemPath).To(Equal("/var/lib/local-ai/backends"))

		m := parse("models", "list")
		Expect(m.Models.List.ModelsPath).To(Equal(filepath.Join(wd, "models")))
		Expect(m.Models.List.BackendsPath).To(Equal(filepath.Join(wd, "backends")))
	})

	It("moves every base folder with LOCALAI_BASE_PATH", func() {
		base := GinkgoT().TempDir()
		GinkgoT().Setenv(system.BasePathEnv, base)
		c := parse("run")
		Expect(c.Run.ModelsPath).To(Equal(filepath.Join(base, "models")))
		Expect(c.Run.BackendsPath).To(Equal(filepath.Join(base, "backends")))
		Expect(c.Run.DownloadStagingPath).To(Equal(filepath.Join(base, "downloading")))
		Expect(c.Run.DataPath).To(Equal(filepath.Join(base, "data")))
		Expect(c.Run.LocalaiConfigDir).To(Equal(filepath.Join(base, "configuration")))
		// Not part of the base folder.
		Expect(c.Run.BackendsSystemPath).To(Equal("/var/lib/local-ai/backends"))
		Expect(c.Run.GeneratedContentPath).To(Equal(DefaultGeneratedContentPath()))
	})

	It("lets a flag or a variable of one folder win over LOCALAI_BASE_PATH", func() {
		base := GinkgoT().TempDir()
		own := GinkgoT().TempDir()
		GinkgoT().Setenv(system.BasePathEnv, base)
		GinkgoT().Setenv("LOCALAI_BACKENDS_PATH", filepath.Join(own, "b"))
		c := parse("run", "--models-path", filepath.Join(own, "m"))
		Expect(c.Run.ModelsPath).To(Equal(filepath.Join(own, "m")))
		Expect(c.Run.BackendsPath).To(Equal(filepath.Join(own, "b")))
		Expect(c.Run.DataPath).To(Equal(filepath.Join(base, "data")))
	})
})

var _ = Describe("longPaths", func() {
	long := filepath.Join("C:", string(make([]byte, system.LongPathWarnLength)))
	paths := []namedPath{{"--models-path", long}, {"--data-path", `C:\LocalAI\data`}, {"--backends-path", ""}}

	It("names the long folders on Windows", func() {
		Expect(longPaths("windows", paths)).To(Equal([]namedPath{{"--models-path", long}}))
	})

	It("names none on Linux", func() {
		Expect(longPaths("linux", paths)).To(BeEmpty())
	})
})
