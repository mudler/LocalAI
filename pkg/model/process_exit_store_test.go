package model

import (
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("backend process exit store cleanup", func() {
	It("removes the model from the store when its backend exits unexpectedly", func() {
		tmpDir := GinkgoT().TempDir()
		GinkgoT().Setenv(backendTempDirEnv, filepath.Join(tmpDir, "backend-runtime"))
		backendPath := filepath.Join(tmpDir, "crashing-backend")
		script := "#!/bin/sh\nsleep 1\necho 'simulated crash' >&2\nexit 1\n"
		Expect(os.WriteFile(backendPath, []byte(script), 0o700)).To(Succeed())

		loader := NewModelLoader(&system.SystemState{Model: system.Model{ModelsPath: tmpDir}})
		process, err := loader.startProcess(backendPath, "test-model", "127.0.0.1:65535", nil)
		Expect(err).ToNot(HaveOccurred())

		// Simulate the loader publishing the model: the store entry points at
		// the just-started process.
		loader.store.Set("test-model", NewModel("test-model", "127.0.0.1:65535", process))

		Eventually(process.Done()).Should(BeClosed())
		Eventually(func() bool {
			_, ok := loader.store.Get("test-model")
			return !ok
		}, 5*time.Second).Should(BeTrue())
	})

	It("keeps a replacement model when the store no longer points at the exiting process", func() {
		tmpDir := GinkgoT().TempDir()
		GinkgoT().Setenv(backendTempDirEnv, filepath.Join(tmpDir, "backend-runtime"))
		backendPath := filepath.Join(tmpDir, "crashing-backend")
		script := "#!/bin/sh\nsleep 1\necho 'simulated crash' >&2\nexit 1\n"
		Expect(os.WriteFile(backendPath, []byte(script), 0o700)).To(Succeed())

		loader := NewModelLoader(&system.SystemState{Model: system.Model{ModelsPath: tmpDir}})
		process, err := loader.startProcess(backendPath, "test-model", "127.0.0.1:65535", nil)
		Expect(err).ToNot(HaveOccurred())

		// A replacement backend for the same id: a different process object
		// (here a remote/external model with no local process).
		loader.store.Set("test-model", NewModel("test-model", "127.0.0.1:65536", nil))

		Eventually(process.Done()).Should(BeClosed())
		Consistently(func() bool {
			_, ok := loader.store.Get("test-model")
			return ok
		}, 2*time.Second).Should(BeTrue())
	})
})
