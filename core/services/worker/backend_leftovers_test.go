package worker

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A worker killed in the middle of an install or an upgrade leaves the
// staged build next to the backend. Listings hide it, so only a later
// install of the same backend removed it. The worker now sweeps it at start,
// before it registers the backends and before it accepts any install.
var _ = Describe("registerInstalledBackends", func() {
	var dir string
	backend := func(name string) string {
		GinkgoHelper()
		p := filepath.Join(dir, name)
		Expect(os.MkdirAll(p, 0o750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(p, "run.sh"), nil, 0o755)).To(Succeed())
		return p
	}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
	})

	It("removes staged builds and keeps previous builds before it registers the backends", func() {
		piper := backend("piper")
		kokoro := backend("kokoro")
		installTmp := backend("piper.install-tmp")
		upgradeTmp := backend("kokoro.upgrade-tmp")
		backup := backend("kokoro.backup")
		installBackup := backend("piper.install-backup")
		failed := backend("whisper.failed")

		state, err := system.GetSystemState(system.WithBackendPath(dir))
		Expect(err).NotTo(HaveOccurred())
		ml := model.NewModelLoader(state)
		Expect(registerInstalledBackends(state, ml)).To(Succeed())

		Expect(installTmp).NotTo(BeAnExistingFile())
		Expect(upgradeTmp).NotTo(BeAnExistingFile())
		for _, kept := range []string{piper, kokoro, backup, installBackup, failed} {
			Expect(kept).To(BeADirectory())
		}
		Expect(ml.GetExternalBackend("piper")).To(Equal(filepath.Join(piper, "run.sh")))
		Expect(ml.GetExternalBackend("kokoro")).To(Equal(filepath.Join(kokoro, "run.sh")))
		Expect(ml.GetExternalBackend("piper.install-tmp")).To(BeEmpty())
	})
})
