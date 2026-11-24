package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeBackend lays out a backend directory the way package.sh does: run.sh and
// the launcher at the top, a stand-in for the Go backend that prints what it
// was given, and one folder per engine package under engines/. A package
// listed in withVision also holds the strata-vision program.
func fakeBackend(packages []string, withVision ...string) string {
	root := GinkgoT().TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(body), 0o755)).To(Succeed())
	}
	for _, f := range []string{"run.sh", "strata-engine"} {
		body, err := os.ReadFile(f)
		Expect(err).ToNot(HaveOccurred())
		write(f, string(body))
	}
	write("strata", "#!/bin/sh\necho \"vision=${STRATA_VISION_PATH:-}\"\n")
	for _, name := range packages {
		write(filepath.Join("engines", name, "strata"), "#!/bin/sh\n")
		write(filepath.Join("engines-hy4", name, "strata-hy4"), "#!/bin/sh\n")
	}
	for _, name := range withVision {
		write(filepath.Join("engines", name, "strata-vision"), "#!/bin/sh\n")
	}
	return root
}

// shell runs one script of the fake backend with a clean Strata environment.
func shell(root, script string, env []string, args ...string) (string, error) {
	cmd := exec.Command(filepath.Join(root, script), args...)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + root}, env...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

var _ = Describe("launcher scripts", func() {
	BeforeEach(func() {
		if runtime.GOOS != "linux" {
			Skip("the launcher scripts run on Linux")
		}
	})

	Describe("run.sh", func() {
		It("points the backend at the strata-vision of the package the launcher picks", func() {
			root := fakeBackend([]string{"cuda12", "sycl"}, "cuda12", "sycl")
			out, err := shell(root, "run.sh", []string{"STRATA_ENGINE_VARIANT=sycl"})
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(Equal("vision=" + filepath.Join(root, "engines", "sycl", "strata-vision")))
		})

		It("gives the backend image support with the launcher as engine path", func() {
			root := fakeBackend([]string{"cuda12"}, "cuda12")
			out, err := shell(root, "run.sh", nil)
			Expect(err).ToNot(HaveOccurred())
			vision := strings.TrimPrefix(out, "vision=")
			Expect(vision).To(Equal(filepath.Join(root, "engines", "cuda12", "strata-vision")))

			// The backend gets image support from this, with the launcher as engine_path.
			dir := nativeModel(true)
			GinkgoT().Setenv("STRATA_VISION_PATH", vision)
			c, err := parseLoad(&pb.ModelOptions{ModelFile: dir, ContextSize: 2048, Options: []string{
				"engine_path:" + filepath.Join(root, "strata-engine"), "native:m-00001-of-00002.gguf", "mmproj:mmproj-m-BF16.gguf"}})
			Expect(err).ToNot(HaveOccurred())
			Expect(c.vision).ToNot(BeNil())
			Expect(c.vision.Exe).To(Equal(vision))
		})

		It("keeps a reader the owner set", func() {
			root := fakeBackend([]string{"cuda12"}, "cuda12")
			out, err := shell(root, "run.sh", []string{"STRATA_VISION_PATH=/opt/own/strata-vision"})
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(Equal("vision=/opt/own/strata-vision"))
		})

		It("leaves the reader alone for another engine", func() {
			root := fakeBackend([]string{"cuda12"}, "cuda12")
			out, err := shell(root, "run.sh", []string{"STRATA_ENGINE_PATH=/opt/own/strata"})
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(Equal("vision="))
		})

		It("sets no reader when the package has none, and the backend refuses images only when asked", func() {
			root := fakeBackend([]string{"cuda12"})
			out, err := shell(root, "run.sh", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(Equal("vision="))

			dir := nativeModel(true)
			opts := []string{"engine_path:" + filepath.Join(root, "strata-engine"), "native:m-00001-of-00002.gguf", "mmproj:mmproj-m-BF16.gguf"}
			c, err := parseLoad(&pb.ModelOptions{ModelFile: dir, ContextSize: 2048, Options: opts})
			Expect(err).ToNot(HaveOccurred())
			Expect(c.vision).To(BeNil())
			_, err = parseLoad(&pb.ModelOptions{ModelFile: dir, ContextSize: 2048, Options: append(opts, "vision:on")})
			Expect(err).To(MatchError(ContainSubstring("no strata-vision program")))
		})
	})

	Describe("strata-engine --print-engine-dir", func() {
		It("prints the folder of the chosen package", func() {
			root := fakeBackend([]string{"cuda12"})
			out, err := shell(root, "strata-engine", nil, "--print-engine-dir")
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(Equal(filepath.Join(root, "engines", "cuda12")))
		})

		It("follows the engine family", func() {
			root := fakeBackend([]string{"cuda12"})
			out, err := shell(root, "strata-engine", []string{"STRATA_ENGINE_FAMILY=hy4"}, "--print-engine-dir")
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(Equal(filepath.Join(root, "engines-hy4", "cuda12")))
		})
	})
})
