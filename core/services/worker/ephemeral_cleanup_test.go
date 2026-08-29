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
})
