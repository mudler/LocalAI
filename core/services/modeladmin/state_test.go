package modeladmin

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// readMap reads the YAML file at path as a map[string]any. Used by both
// state and pinned specs to assert on the on-disk shape.
func readMap(path string) map[string]any {
	raw, err := os.ReadFile(path)
	Expect(err).ToNot(HaveOccurred())
	var m map[string]any
	Expect(yaml.Unmarshal(raw, &m)).To(Succeed())
	return m
}

var _ = Describe("ConfigService.ToggleState", func() {
	var (
		svc *ConfigService
		dir string
		ctx context.Context
	)

	BeforeEach(func() {
		svc, dir = newTestService()
		ctx = context.Background()
	})

	It("disables a model by writing disabled: true", func() {
		writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp"})

		_, err := svc.ToggleState(ctx, "qwen", ActionDisable, nil)
		Expect(err).ToNot(HaveOccurred())

		got := readMap(filepath.Join(dir, "qwen.yaml"))
		Expect(got).To(HaveKeyWithValue("disabled", true))
	})

	It("enables a model by removing the disabled key entirely", func() {
		writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp", "disabled": true})

		_, err := svc.ToggleState(ctx, "qwen", ActionEnable, nil)
		Expect(err).ToNot(HaveOccurred())

		got := readMap(filepath.Join(dir, "qwen.yaml"))
		Expect(got).ToNot(HaveKey("disabled"))
	})

	It("rejects unknown actions with ErrBadAction", func() {
		writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp"})
		_, err := svc.ToggleState(ctx, "qwen", Action("noop"), nil)
		Expect(err).To(MatchError(ErrBadAction))
	})

	It("returns ErrNotFound for an unknown model", func() {
		_, err := svc.ToggleState(ctx, "ghost", ActionDisable, nil)
		Expect(err).To(MatchError(ErrNotFound))
	})
})
