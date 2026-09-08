package worker

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Worker ephemeral staging cleanup", func() {
	var stagingDir string

	// mkEphemeral creates one staged request directory holding a file, and
	// backdates both so the sweeper sees it as `age` old.
	mkEphemeral := func(id string, age time.Duration) string {
		dir := filepath.Join(stagingDir, "ephemeral", "inputs", id)
		Expect(os.MkdirAll(dir, 0o750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "payload.bin"), []byte("x"), 0o600)).To(Succeed())
		stamp := time.Now().Add(-age)
		Expect(os.Chtimes(filepath.Join(dir, "payload.bin"), stamp, stamp)).To(Succeed())
		Expect(os.Chtimes(dir, stamp, stamp)).To(Succeed())
		return dir
	}

	BeforeEach(func() { stagingDir = GinkgoT().TempDir() })

	It("removes staged request directories older than the TTL", func() {
		old := mkEphemeral("aaaa1111", 48*time.Hour)
		CleanEphemeralStaging(stagingDir, time.Hour)
		Expect(old).ToNot(BeAnExistingFile())
	})

	It("keeps directories a running request may still be reading", func() {
		fresh := mkEphemeral("bbbb2222", 5*time.Minute)
		CleanEphemeralStaging(stagingDir, time.Hour)
		Expect(fresh).To(BeAnExistingFile())
	})

	It("leaves staged models and everything outside ephemeral alone", func() {
		modelDir := filepath.Join(stagingDir, "models", "some-model")
		Expect(os.MkdirAll(modelDir, 0o750)).To(Succeed())
		weights := filepath.Join(modelDir, "weights.gguf")
		Expect(os.WriteFile(weights, []byte("w"), 0o600)).To(Succeed())
		stamp := time.Now().Add(-90 * 24 * time.Hour)
		Expect(os.Chtimes(weights, stamp, stamp)).To(Succeed())
		Expect(os.Chtimes(modelDir, stamp, stamp)).To(Succeed())

		CleanEphemeralStaging(stagingDir, time.Hour)

		Expect(weights).To(BeAnExistingFile(), "a staged model is not ephemeral scratch")
	})

	It("does nothing when no ephemeral directory exists", func() {
		Expect(func() { CleanEphemeralStaging(stagingDir, time.Hour) }).ToNot(Panic())
	})

	It("sweeps both transport roots by newest descendant and skips active requests", func() {
		cacheDir := GinkgoT().TempDir()
		httpRoot := filepath.Join(stagingDir, "ephemeral")
		s3Root := filepath.Join(cacheDir, "ephemeral")
		guard, err := NewEphemeralCapacityGuard([]string{httpRoot, s3Root}, 8, 0)
		Expect(err).NotTo(HaveOccurred())

		staleRequest := filepath.Join(httpRoot, "audio", "stale")
		activeRequest := filepath.Join(s3Root, "audio", "active")
		freshChildRequest := filepath.Join(s3Root, "audio", "fresh-child")
		for _, requestDir := range []string{staleRequest, activeRequest, freshChildRequest} {
			Expect(os.MkdirAll(requestDir, 0o750)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(requestDir, "input.bin"), []byte("data"), 0o600)).To(Succeed())
		}
		old := time.Now().Add(-2 * time.Hour)
		fresh := time.Now().Add(-5 * time.Minute)
		for _, requestDir := range []string{staleRequest, activeRequest, freshChildRequest} {
			Expect(os.Chtimes(requestDir, old, old)).To(Succeed())
		}
		Expect(os.Chtimes(filepath.Join(staleRequest, "input.bin"), old, old)).To(Succeed())
		Expect(os.Chtimes(filepath.Join(activeRequest, "input.bin"), old, old)).To(Succeed())
		Expect(os.Chtimes(filepath.Join(freshChildRequest, "input.bin"), fresh, fresh)).To(Succeed())
		Expect(guard.Account(filepath.Join(staleRequest, "input.bin"), 4)).To(Succeed())
		Expect(guard.Reserve(filepath.Join(activeRequest, "input.bin"), 4)).To(Succeed())

		CleanEphemeralRoots([]string{httpRoot, s3Root}, time.Hour, guard)

		Expect(staleRequest).NotTo(BeADirectory())
		Expect(activeRequest).To(BeADirectory())
		Expect(freshChildRequest).To(BeADirectory())
		Expect(guard.Reserve(filepath.Join(httpRoot, "audio", "replacement", "input.bin"), 4)).To(Succeed())
	})

	It("keeps committed request inputs until exact release ends ownership", func() {
		root := filepath.Join(stagingDir, "ephemeral")
		requestDir := filepath.Join(root, "audio", "owned")
		path := filepath.Join(requestDir, "input.bin")
		Expect(os.MkdirAll(requestDir, 0o750)).To(Succeed())
		guard, err := NewEphemeralCapacityGuard([]string{root}, 8, 0)
		Expect(err).NotTo(HaveOccurred())

		Expect(guard.Reserve(path, 4)).To(Succeed())
		Expect(os.WriteFile(path, []byte("data"), 0o600)).To(Succeed())
		Expect(guard.Commit(path)).To(Succeed())
		old := time.Now().Add(-2 * time.Hour)
		Expect(os.Chtimes(path, old, old)).To(Succeed())
		Expect(os.Chtimes(requestDir, old, old)).To(Succeed())

		CleanEphemeralRoots([]string{root}, time.Hour, guard)
		Expect(requestDir).To(BeADirectory())

		Expect(guard.Release(path)).To(Succeed())
		CleanEphemeralRoots([]string{root}, time.Hour, guard)
		Expect(requestDir).NotTo(BeADirectory())
	})
})
