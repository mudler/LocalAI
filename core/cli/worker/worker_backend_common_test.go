package worker

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("findBackendPath with backend installation disabled", func() {
	var state *system.SystemState

	BeforeEach(func() {
		root := GinkgoT().TempDir()
		var err error
		state, err = system.GetSystemState(
			system.WithBackendPath(filepath.Join(root, "backends")),
			system.WithBackendSystemPath(filepath.Join(root, "system")),
			system.WithDisableBackendInstall(true),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.MkdirAll(state.Backend.BackendsPath, 0o750)).To(Succeed())
		Expect(os.MkdirAll(state.Backend.BackendsSystemPath, 0o750)).To(Succeed())
	})

	It("uses a backend that is already present", func() {
		dir := filepath.Join(state.Backend.BackendsSystemPath, "vllm")
		Expect(os.MkdirAll(dir, 0o750)).To(Succeed())
		meta, _ := json.Marshal(&gallery.BackendMetadata{Name: "vllm"})
		Expect(os.WriteFile(filepath.Join(dir, "metadata.json"), meta, 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\n"), 0o755)).To(Succeed())

		path, err := findBackendPath("vllm", "[]", state, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(path).To(Equal(dir))
	})

	It("refuses to install a missing backend", func() {
		_, err := findBackendPath("vllm", "[]", state, false)
		Expect(errors.Is(err, gallery.ErrBackendInstallDisabled)).To(BeTrue(), err.Error())
	})
})
