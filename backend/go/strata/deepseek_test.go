package main

import (
	"os"
	"path/filepath"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Strata v0.2.15 removed DeepSeek V4.1, so the backend refuses the family at
// load, before it looks for an engine or a pack.
var _ = Describe("deepseek41 family", func() {
	It("is refused with a clear error", func() {
		dir := GinkgoT().TempDir()
		exe := filepath.Join(dir, "engine")
		Expect(os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755)).To(Succeed())
		_, err := parseLoad(&pb.ModelOptions{ModelFile: dir, Options: []string{"engine_path:" + exe, "family:deepseek41"}})
		Expect(err).To(MatchError(ContainSubstring("removed DeepSeek V4.1")))
	})

	It("names qwen as the only family for other values", func() {
		_, err := parseLoad(&pb.ModelOptions{Options: []string{"family:llama"}})
		Expect(err).To(MatchError(ContainSubstring("must be qwen")))
	})
})
