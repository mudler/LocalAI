package modeladmin

import (
	"context"
	"errors"
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

		_, err := svc.ToggleState(ctx, "qwen", ActionDisable)
		Expect(err).ToNot(HaveOccurred())

		got := readMap(filepath.Join(dir, "qwen.yaml"))
		Expect(got).To(HaveKeyWithValue("disabled", true))
	})

	It("applies disable through the revision lifecycle", func() {
		lifecycle := &fakeRevisionLifecycle{pending: 3}
		svc.Lifecycle = lifecycle
		writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp"})

		result, err := svc.ToggleState(ctx, "qwen", ActionDisable)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.ConfigRevision).ToNot(BeEmpty())
		Expect(result.PendingCleanup).To(Equal(3))
		Expect(lifecycle.calls).To(ConsistOf(revisionLifecycleCall{
			oldName: "qwen", newName: "qwen", revision: result.ConfigRevision, disabled: true,
		}))
	})

	It("restores disk and loader when state publication fails", func() {
		svc.Lifecycle = &fakeRevisionLifecycle{err: errors.New("registry unavailable")}
		writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp"})

		_, err := svc.ToggleState(ctx, "qwen", ActionDisable)
		Expect(err).To(MatchError(ContainSubstring("registry unavailable")))
		Expect(readMap(filepath.Join(dir, "qwen.yaml"))).NotTo(HaveKey("disabled"))
		loaded, ok := svc.Loader.GetModelConfig("qwen")
		Expect(ok).To(BeTrue())
		Expect(loaded.IsDisabled()).To(BeFalse())
	})

	It("enables a model by removing the disabled key entirely", func() {
		writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp", "disabled": true})

		_, err := svc.ToggleState(ctx, "qwen", ActionEnable)
		Expect(err).ToNot(HaveOccurred())

		got := readMap(filepath.Join(dir, "qwen.yaml"))
		Expect(got).ToNot(HaveKey("disabled"))
	})

	It("rejects unknown actions with ErrBadAction", func() {
		writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp"})
		_, err := svc.ToggleState(ctx, "qwen", Action("noop"))
		Expect(err).To(MatchError(ErrBadAction))
	})

	It("returns ErrNotFound for an unknown model", func() {
		_, err := svc.ToggleState(ctx, "ghost", ActionDisable)
		Expect(err).To(MatchError(ErrNotFound))
	})
})
