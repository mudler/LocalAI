package model

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Backend process runtime directory", func() {
	It("keeps active runtimes while sweeping abandoned ones", func() {
		root := GinkgoT().TempDir()
		GinkgoT().Setenv(backendTempDirEnv, root)
		ownedRoot := backendRuntimeRoot()
		unrelated := filepath.Join(root, backendRuntimeDirPrefix+"unrelated")
		Expect(os.MkdirAll(unrelated, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(unrelated, "keep"), []byte("unrelated"), 0o600)).To(Succeed())

		active, err := newBackendProcessRuntime()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(active.cleanup)
		Expect(os.WriteFile(filepath.Join(active.tempDir, "active.img"), []byte("active"), 0o600)).To(Succeed())

		abandoned := filepath.Join(ownedRoot, backendRuntimeDirPrefix+"abandoned")
		Expect(os.MkdirAll(abandoned, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(abandoned, backendRuntimeMarker), []byte(backendRuntimeMagic), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(abandoned, "orphan.img"), []byte("orphan"), 0o600)).To(Succeed())
		foreign := filepath.Join(ownedRoot, backendRuntimeDirPrefix+"foreign")
		Expect(os.MkdirAll(foreign, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(foreign, "keep"), []byte("foreign"), 0o600)).To(Succeed())

		other, err := newBackendProcessRuntime()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(other.cleanup)

		Expect(active.dir).To(BeADirectory())
		Expect(abandoned).ToNot(BeAnExistingFile())
		Expect(foreign).To(BeADirectory())
		Expect(unrelated).To(BeADirectory())
	})

	It("uses one private directory for process state and backend scratch", func() {
		root := GinkgoT().TempDir()
		GinkgoT().Setenv(backendTempDirEnv, root)

		runtime, err := newBackendProcessRuntime()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(runtime.cleanup)

		Expect(filepath.Dir(runtime.dir)).To(Equal(backendRuntimeRoot()))
		Expect(runtime.tempDir).To(Equal(filepath.Join(runtime.dir, "tmp")))
		info, err := os.Stat(runtime.tempDir)
		Expect(err).ToNot(HaveOccurred())
		Expect(info.IsDir()).To(BeTrue())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o700)))
	})

	It("overrides inherited temp variables for the backend only", func() {
		env := backendTempEnvironment([]string{
			"PATH=/bin",
			"TMPDIR=/old/tmpdir",
			"TMP=/old/tmp",
			"TEMP=/old/temp",
		}, "/owned/scratch")

		Expect(env).To(ConsistOf(
			"PATH=/bin",
			"TMPDIR=/owned/scratch",
			"TMP=/owned/scratch",
			"TEMP=/owned/scratch",
		))
		for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
			count := 0
			for _, entry := range env {
				if strings.HasPrefix(entry, key+"=") {
					count++
				}
			}
			Expect(count).To(Equal(1), key)
		}
	})

	It("removes the runtime when its owner exits", func() {
		GinkgoT().Setenv(backendTempDirEnv, GinkgoT().TempDir())

		runtime, err := newBackendProcessRuntime()
		Expect(err).ToNot(HaveOccurred())
		dir := runtime.dir
		runtime.cleanup()

		Expect(dir).ToNot(BeAnExistingFile())
	})

	It("reports which configured root cannot be used", func() {
		parent := GinkgoT().TempDir()
		file := filepath.Join(parent, "not-a-directory")
		Expect(os.WriteFile(file, []byte("x"), 0o600)).To(Succeed())
		base := filepath.Join(file, "backend-runtime")
		GinkgoT().Setenv(backendTempDirEnv, base)
		root := backendRuntimeRoot()

		runtime, err := newBackendProcessRuntime()
		Expect(err).To(HaveOccurred())
		Expect(runtime).To(BeNil())
		Expect(err.Error()).To(ContainSubstring(root))
	})
})
