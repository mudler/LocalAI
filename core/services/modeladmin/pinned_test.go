package modeladmin

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ConfigService.TogglePinned", func() {
	var (
		svc *ConfigService
		dir string
		ctx context.Context
	)

	BeforeEach(func() {
		svc, dir = newTestService()
		ctx = context.Background()
	})

	It("pins a model by writing pinned: true", func() {
		writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp"})

		_, err := svc.TogglePinned(ctx, "qwen", ActionPin, nil)
		Expect(err).ToNot(HaveOccurred())

		got := readMap(filepath.Join(dir, "qwen.yaml"))
		Expect(got).To(HaveKeyWithValue("pinned", true))
	})

	It("unpins by removing the pinned key entirely", func() {
		writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp", "pinned": true})

		_, err := svc.TogglePinned(ctx, "qwen", ActionUnpin, nil)
		Expect(err).ToNot(HaveOccurred())

		got := readMap(filepath.Join(dir, "qwen.yaml"))
		Expect(got).ToNot(HaveKey("pinned"))
	})

	It("rejects unknown actions with ErrBadAction", func() {
		writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp"})
		_, err := svc.TogglePinned(ctx, "qwen", Action("stick"), nil)
		Expect(err).To(MatchError(ErrBadAction))
	})

	It("invokes the syncPinned callback after a successful toggle", func() {
		writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp"})

		called := false
		_, err := svc.TogglePinned(ctx, "qwen", ActionPin, func() { called = true })
		Expect(err).ToNot(HaveOccurred())
		Expect(called).To(BeTrue(), "syncPinned callback should be invoked")
	})
})
