package model

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("backend process exit diagnostics", func() {
	It("includes the exit code and final stderr line for an unexpected exit", func() {
		tmpDir := GinkgoT().TempDir()
		backendTempRoot := filepath.Join(tmpDir, "backend-runtime")
		GinkgoT().Setenv(backendTempDirEnv, backendTempRoot)
		backendPath := filepath.Join(tmpDir, "failing-backend")
		Expect(os.WriteFile(backendPath, []byte("#!/bin/sh\nprintf '%s' \"$TMPDIR\" > \"$0.tmpdir\"\necho 'first diagnostic' >&2\necho 'fatal metal pipeline error' >&2\nexit 42\n"), 0o700)).To(Succeed())

		captured := &bytes.Buffer{}
		handler := slog.NewTextHandler(captured, &slog.HandlerOptions{Level: slog.LevelWarn})
		xlog.SetLogger(xlog.NewLoggerWithHandler(handler, xlog.LogLevelWarn))
		DeferCleanup(func() {
			xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text"))
		})

		loader := NewModelLoader(&system.SystemState{Model: system.Model{ModelsPath: tmpDir}})
		process, err := loader.startProcess(backendPath, "test-model", "127.0.0.1:65535", nil)
		Expect(err).ToNot(HaveOccurred())
		Eventually(process.Done()).Should(BeClosed())
		backendTemp, err := os.ReadFile(backendPath + ".tmpdir")
		Expect(err).ToNot(HaveOccurred())
		Expect(string(backendTemp)).To(Equal(filepath.Join(process.StateDir(), "tmp")))
		Eventually(string(backendTemp)).ShouldNot(BeADirectory())
		Eventually(captured.String).Should(And(
			ContainSubstring("Backend process exited unexpectedly"),
			ContainSubstring("exitCode=42"),
			ContainSubstring(`stderr="fatal metal pipeline error"`),
		))
		loader.cleanupProcessRuntime(process)
		Eventually(process.StateDir()).ShouldNot(BeADirectory())
	})
})
