package model

import (
	"os"
	"path/filepath"
	"runtime"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("gRPC backend startup errors", func() {
	It("reports a backend process exit and its stderr instead of only a readiness timeout", func() {
		if runtime.GOOS == "windows" {
			Skip("the fixture backend is a shell script, which Windows cannot execute")
		}
		tmpDir := GinkgoT().TempDir()
		backendPath := filepath.Join(tmpDir, "failing-backend")
		Expect(os.WriteFile(backendPath, []byte("#!/bin/sh\necho 'dyld: Library not loaded: libprotobuf.33.dylib' >&2\nexit 42\n"), 0o700)).To(Succeed())

		loader := NewModelLoader(&system.SystemState{Model: system.Model{ModelsPath: tmpDir}})
		options := NewOptions(
			WithGRPCAttempts(2),
			WithGRPCAttemptsDelay(1),
			WithLoadGRPCLoadModelOpts(&pb.ModelOptions{}),
		)

		loaded, err := loader.spawnGRPCModel("failing", backendPath, options, "test-model", "test-model", "test.gguf")

		Expect(loaded).To(BeNil())
		Expect(err).To(MatchError(And(
			ContainSubstring("backend process exited with code 42"),
			ContainSubstring("dyld: Library not loaded: libprotobuf.33.dylib"),
		)))
	})
})
