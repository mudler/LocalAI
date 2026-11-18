package model

import (
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// recordingProcessTree counts the tree operations, so the tests can check the
// calls on a platform whose real tracker does nothing.
type recordingProcessTree struct {
	assigned   atomic.Int32
	terminated atomic.Int32
}

func (t *recordingProcessTree) assign(int) error { t.assigned.Add(1); return nil }
func (t *recordingProcessTree) terminate()       { t.terminated.Add(1) }

var _ = Describe("TerminateProcessTree", func() {
	It("terminates the tree of a process started through StartProcess", func() {
		tree := &recordingProcessTree{}
		previous := newProcessTree
		newProcessTree = func() processTree { return tree }
		DeferCleanup(func() { newProcessTree = previous })

		tmpDir := GinkgoT().TempDir()
		GinkgoT().Setenv(backendTempDirEnv, filepath.Join(tmpDir, "backend-runtime"))
		backendPath := filepath.Join(tmpDir, "sleeping-backend")
		Expect(os.WriteFile(backendPath, []byte("#!/bin/sh\nsleep 30\n"), 0o700)).To(Succeed())

		loader := NewModelLoader(&system.SystemState{Model: system.Model{ModelsPath: tmpDir}})
		proc, err := loader.StartProcess(backendPath, "tree-model", "127.0.0.1:65535", nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_ = proc.Stop()
			loader.CleanupProcessRuntime(proc)
		})
		Expect(tree.assigned.Load()).To(BeEquivalentTo(1))

		loader.TerminateProcessTree(proc)
		Expect(tree.terminated.Load()).To(BeEquivalentTo(1))
	})

	It("does nothing for a process it did not start", func() {
		loader := NewModelLoader(&system.SystemState{})
		Expect(func() { loader.TerminateProcessTree(nil) }).ToNot(Panic())
	})
})
