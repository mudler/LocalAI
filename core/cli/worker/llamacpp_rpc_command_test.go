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

var _ = Describe("llamaCPPRPCStart", func() {
	var backend, runFile, server, lib string

	BeforeEach(func() {
		backend = GinkgoT().TempDir()
		runFile = filepath.Join(backend, "run.sh")
		server = filepath.Join(backend, llamaCPPRPCBinaryName)
		lib = filepath.Join(backend, "lib")
		Expect(os.MkdirAll(lib, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(lib, "ld.so"), nil, 0o755)).To(Succeed())
	})

	It("starts run.sh with the binary override when run.sh supports it", func() {
		Expect(os.WriteFile(runFile, []byte("BINARY=${LLAMACPP_BINARY:-x}\n"), 0o755)).To(Succeed())

		path, argv, env := llamaCPPRPCStart("linux", runFile, []string{"-p", "50052"},
			[]string{"HOME=/root", "LLAMACPP_BINARY=other", "LD_LIBRARY_PATH=/opt/lib"})

		Expect(path).To(Equal(runFile))
		Expect(argv).To(Equal([]string{runFile, "-p", "50052"}))
		Expect(env).To(Equal([]string{"HOME=/root", "LD_LIBRARY_PATH=/opt/lib", "LLAMACPP_BINARY=" + llamaCPPRPCBinaryName}))
	})

	It("falls back to the direct start when run.sh has no override", func() {
		Expect(os.WriteFile(runFile, []byte("exec ./llama-cpp-fallback \"$@\"\n"), 0o755)).To(Succeed())
		args := []string{"-p", "50052"}
		env := []string{"HOME=/root"}

		path, argv, got := llamaCPPRPCStart("linux", runFile, args, env)
		wantPath, wantArgv, wantEnv := llamaCPPRPCCommand("linux", server, args, env)

		Expect(path).To(Equal(wantPath))
		Expect(argv).To(Equal(wantArgv))
		Expect(got).To(Equal(wantEnv))
		Expect(path).To(Equal(filepath.Join(lib, "ld.so")))
	})

	It("falls back when the run file is missing", func() {
		path, _, _ := llamaCPPRPCStart("linux", runFile, nil, nil)

		Expect(path).To(Equal(filepath.Join(lib, "ld.so")))
	})

	It("starts the binary directly on other systems even with a new run.sh", func() {
		Expect(os.WriteFile(runFile, []byte("${LLAMACPP_BINARY:-x}\n"), 0o755)).To(Succeed())
		env := []string{"HOME=/root"}

		for _, goos := range []string{"darwin", "windows"} {
			path, argv, got := llamaCPPRPCStart(goos, runFile, []string{"-p", "1"}, env)

			Expect(path).To(Equal(executableName(goos, server)))
			Expect(argv).To(Equal([]string{path, "-p", "1"}))
			Expect(got).To(Equal(env))
		}
	})
})
