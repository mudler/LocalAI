package worker

import (
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("worker platform checks", func() {
	It("refuses the backends without a Windows build on Windows", func() {
		for _, command := range []string{"mlx-distributed", "p2p-mlx", "vllm", "ds4-distributed"} {
			err := requireBackendBuild("windows", command, "x")
			Expect(err).To(HaveOccurred(), command)
			Expect(err.Error()).To(ContainSubstring("not available on Windows"))
		}
	})

	It("allows the llama.cpp workers on Windows", func() {
		Expect(requireBackendBuild("windows", "llama-cpp-rpc", "llama-cpp")).To(Succeed())
		Expect(requireBackendBuild("windows", "p2p-llama-cpp-rpc", "llama-cpp")).To(Succeed())
	})

	It("allows every worker on Linux and macOS", func() {
		for _, goos := range []string{"linux", "darwin"} {
			Expect(requireBackendBuild(goos, "vllm", "vllm")).To(Succeed())
		}
	})

	It("adds the .exe suffix on Windows only", func() {
		Expect(executableName("windows", "llama-cpp-rpc-server")).To(Equal("llama-cpp-rpc-server.exe"))
		Expect(executableName("windows", "server.EXE")).To(Equal("server.EXE"))
		Expect(executableName("linux", "llama-cpp-rpc-server")).To(Equal("llama-cpp-rpc-server"))
	})

	Describe("llamaCPPRPCServerPath", func() {
		It("finds the RPC server next to the run file", func() {
			dir := GinkgoT().TempDir()
			server := filepath.Join(dir, hostExecutableName(llamaCPPRPCBinaryName))
			Expect(os.WriteFile(server, nil, 0o700)).To(Succeed())
			path, err := llamaCPPRPCServerPath(filepath.Join(dir, "run.sh"))
			Expect(err).ToNot(HaveOccurred())
			Expect(path).To(Equal(server))
		})

		It("says so when the backend has no RPC server", func() {
			dir := GinkgoT().TempDir()
			_, err := llamaCPPRPCServerPath(filepath.Join(dir, "run.sh"))
			Expect(err).To(MatchError(ContainSubstring("has no " + hostExecutableName(llamaCPPRPCBinaryName))))
		})

		It("fails for a backend that is not installed", func() {
			_, err := llamaCPPRPCServerPath("")
			Expect(err).To(MatchError(ContainSubstring("not found")))
		})
	})

	It("uses the host OS", func() {
		Expect(hostExecutableName("a")).To(Equal(executableName(runtime.GOOS, "a")))
	})
})
