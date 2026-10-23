package worker

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Worker with backend installation disabled", func() {
	var sup *backendSupervisor
	var state *system.SystemState

	BeforeEach(func() {
		root := GinkgoT().TempDir()
		var err error
		state, err = system.GetSystemState(
			system.WithBackendPath(filepath.Join(root, "backends")),
			system.WithBackendSystemPath(filepath.Join(root, "system-backends")),
			system.WithModelPath(filepath.Join(root, "models")),
			system.WithDisableBackendInstall(true),
		)
		Expect(err).NotTo(HaveOccurred())
		for _, dir := range []string{state.Backend.BackendsPath, state.Backend.BackendsSystemPath, state.Model.ModelsPath} {
			Expect(os.MkdirAll(dir, 0o750)).To(Succeed())
		}
		sup = &backendSupervisor{
			cfg:         &Config{DisableBackendInstall: true},
			ml:          model.NewModelLoader(state),
			systemState: state,
			processes:   map[string]*backendProcess{},
		}
	})

	It("refuses a frontend install request for a backend it does not have", func() {
		_, err := sup.installBackend(workerctl.BackendInstallRequest{Backend: "vllm", ModelID: "m"}, false, nil)
		Expect(errors.Is(err, gallery.ErrBackendInstallDisabled)).To(BeTrue(), err.Error())
		_, statErr := os.Stat(filepath.Join(state.Backend.BackendsPath, "vllm"))
		Expect(os.IsNotExist(statErr)).To(BeTrue())
	})

	It("refuses an upgrade request", func() {
		_, err := sup.upgradeBackend(workerctl.BackendUpgradeRequest{Backend: "vllm"}, nil)
		Expect(errors.Is(err, gallery.ErrBackendInstallDisabled)).To(BeTrue(), err.Error())
	})
})
