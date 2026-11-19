package modeladmin

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

// Every writer of an existing model config must record the new hash in the
// version 2 gallery file, or the update check would read an edit made through
// LocalAI as a change made outside it.
var _ = Describe("Writers of a model config", func() {
	var (
		svc *ConfigService
		dir string
		ctx context.Context
	)

	BeforeEach(func() {
		svc, dir = newTestService()
		ctx = context.Background()
		writeModelYAML(svc, dir, "m", map[string]any{"backend": "llama-cpp", "context_size": 1024})
		Expect(gallery.WriteSidecar(dir, "m", &gallery.ModelConfig{
			Name: "m", Version: gallery.SidecarVersion, ConfigSHA256: "stale",
			Origin: &gallery.Origin{EntryName: "m"},
		})).To(Succeed())
	})

	diskHash := func(name string) string {
		data, err := os.ReadFile(filepath.Join(dir, name+".yaml"))
		Expect(err).ToNot(HaveOccurred())
		return gallery.SHA256Hex(data)
	}
	sidecarHash := func(name string) string {
		sc, err := gallery.ReadSidecar(dir, name)
		Expect(err).ToNot(HaveOccurred())
		return sc.ConfigSHA256
	}

	It("records the hash after EditYAML", func() {
		_, err := svc.EditYAML(ctx, "m", []byte("name: m\nbackend: llama-cpp\ncontext_size: 2048\n"))
		Expect(err).ToNot(HaveOccurred())
		Expect(sidecarHash("m")).To(Equal(diskHash("m")))
	})

	It("moves the gallery file and records the hash after a rename", func() {
		_, err := svc.EditYAML(ctx, "m", []byte("name: n\nbackend: llama-cpp\n"))
		Expect(err).ToNot(HaveOccurred())
		Expect(gallery.SidecarPath(dir, "m")).ToNot(BeAnExistingFile())
		Expect(sidecarHash("n")).To(Equal(diskHash("n")))
	})

	It("records the hash after PatchConfig", func() {
		_, err := svc.PatchConfig(ctx, "m", map[string]any{"context_size": 8192})
		Expect(err).ToNot(HaveOccurred())
		Expect(sidecarHash("m")).To(Equal(diskHash("m")))
	})

	It("records the hash after a disable and an enable", func() {
		_, err := svc.ToggleState(ctx, "m", ActionDisable)
		Expect(err).ToNot(HaveOccurred())
		Expect(sidecarHash("m")).To(Equal(diskHash("m")))
		_, err = svc.ToggleState(ctx, "m", ActionEnable)
		Expect(err).ToNot(HaveOccurred())
		Expect(sidecarHash("m")).To(Equal(diskHash("m")))
	})

	It("records the hash after a pin", func() {
		_, err := svc.TogglePinned(ctx, "m", ActionPin, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(sidecarHash("m")).To(Equal(diskHash("m")))
	})

	It("never rewrites a gallery file of an older release", func() {
		old := []byte("name: o\nentry_name: o\n")
		writeModelYAML(svc, dir, "o", map[string]any{"backend": "llama-cpp"})
		Expect(os.WriteFile(gallery.SidecarPath(dir, "o"), old, 0600)).To(Succeed())
		_, err := svc.PatchConfig(ctx, "o", map[string]any{"context_size": 4096})
		Expect(err).ToNot(HaveOccurred())
		Expect(os.ReadFile(gallery.SidecarPath(dir, "o"))).To(Equal(old))
	})

	It("restores the gallery file with the config when an edit is rolled back", func() {
		before, err := os.ReadFile(gallery.SidecarPath(dir, "m"))
		Expect(err).ToNot(HaveOccurred())
		svc.Lifecycle = &fakeRevisionLifecycle{err: os.ErrPermission}
		_, err = svc.PatchConfig(ctx, "m", map[string]any{"context_size": 8192})
		Expect(err).To(HaveOccurred())
		Expect(os.ReadFile(gallery.SidecarPath(dir, "m"))).To(Equal(before))
	})

	It("is the only writer of an existing model config in this package", func() {
		// The allowed callers of writeFileAtomic: its definition, the restore of
		// a rolled back edit (which puts back the bytes it saved, gallery file
		// included), the helper, and CreateConfig, which writes a new model that
		// has no gallery file.
		allowed := map[string]bool{"atomic.go": true, "mutation.go": true, "model_yaml.go": true, "create_delete.go": true}
		entries, err := os.ReadDir(".")
		Expect(err).ToNot(HaveOccurred())
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || allowed[name] {
				continue
			}
			src, err := os.ReadFile(name)
			Expect(err).ToNot(HaveOccurred())
			for _, banned := range []string{"writeFileAtomic(", "os.WriteFile(", "os.Create("} {
				Expect(string(src)).ToNot(ContainSubstring(banned), "%s writes a file with %s; use writeModelConfig", name, banned)
			}
		}
	})
})
