package worker

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("llamaCPPRPCCommand", func() {
	var backend, server, lib string

	BeforeEach(func() {
		backend = GinkgoT().TempDir()
		server = filepath.Join(backend, llamaCPPRPCBinaryName)
		lib = filepath.Join(backend, "lib")
	})

	It("starts the server through the bundled loader with lib/ first on LD_LIBRARY_PATH", func() {
		Expect(os.MkdirAll(lib, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(lib, "ld.so"), nil, 0o755)).To(Succeed())

		path, argv, env := llamaCPPRPCCommand("linux", server, []string{"-p", "50052"},
			[]string{"HOME=/root", "LD_LIBRARY_PATH=/usr/local/cuda/lib64"})

		loader := filepath.Join(lib, "ld.so")
		Expect(path).To(Equal(loader))
		Expect(argv).To(Equal([]string{loader, server, "-p", "50052"}))
		Expect(env).To(Equal([]string{"HOME=/root", "LD_LIBRARY_PATH=" + lib + ":/usr/local/cuda/lib64"}))
	})

	It("sets LD_LIBRARY_PATH without a loader when lib/ has none", func() {
		Expect(os.MkdirAll(lib, 0o755)).To(Succeed())

		path, argv, env := llamaCPPRPCCommand("linux", server, nil, []string{"HOME=/root"})

		Expect(path).To(Equal(server))
		Expect(argv).To(Equal([]string{server}))
		Expect(env).To(Equal([]string{"HOME=/root", "LD_LIBRARY_PATH=" + lib}))
	})

	It("starts the server directly when the backend has no lib/", func() {
		env := []string{"HOME=/root"}
		path, argv, got := llamaCPPRPCCommand("linux", server, []string{"-p", "50052"}, env)

		Expect(path).To(Equal(server))
		Expect(argv).To(Equal([]string{server, "-p", "50052"}))
		Expect(got).To(Equal(env))
	})

	It("leaves other systems unchanged", func() {
		Expect(os.MkdirAll(lib, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(lib, "ld.so"), nil, 0o755)).To(Succeed())

		env := []string{"HOME=/root"}
		path, argv, got := llamaCPPRPCCommand("windows", server, nil, env)

		Expect(path).To(Equal(server))
		Expect(argv).To(Equal([]string{server}))
		Expect(got).To(Equal(env))
	})
})
