package gallery_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
)

var _ = Describe("EntryDigest", func() {
	base := func() *gallery.GalleryModel {
		return &gallery.GalleryModel{
			Metadata: gallery.Metadata{
				Name: "tiny", URL: "github:example/gallery/tiny.yaml@master",
				Description: "a tiny model", Icon: "https://example.com/i.png", License: "mit",
				Tags: []string{"llm"}, Size: "1GB", URLs: []string{"https://example.com"},
				AdditionalFiles: []gallery.File{{Filename: "tiny.gguf", SHA256: "AB12", URI: "https://example.com/tiny.gguf"}},
			},
			Overrides: map[string]any{"parameters": map[string]any{"model": "tiny.gguf"}, "context_size": 2048},
		}
	}

	// Golden values: a change here means every installed model shows an update
	// after an upgrade of LocalAI. Change them only with a reason.
	It("is stable across releases", func() {
		m := base()
		Expect(gallery.EntryDigest(m, m, "")).To(Equal("sha256:f2e8d8d2db4591d8d0b96ca577a37bfd9a927a353315cc53720a81a1fdae6421"))
		Expect(gallery.EntryDigest(m, m, "deadbeef")).To(Equal("sha256:e71ff242a044cb01afdecde71b1a1756b08b748ee2333950af85264741aa7b8e"))
		Expect(gallery.EntryDigest(m, nil, "deadbeef")).To(Equal(gallery.EntryDigest(m, m, "deadbeef")))
	})

	It("ignores the presentation fields", func() {
		want := gallery.EntryDigest(base(), nil, "x")
		m := base()
		m.Description, m.Icon, m.License, m.Size = "other", "", "apache", "2GB"
		m.Tags, m.URLs = []string{"chat"}, nil
		m.Name = "renamed"
		Expect(gallery.EntryDigest(m, nil, "x")).To(Equal(want))
	})

	DescribeTable("changes with what the install writes",
		func(change func(*gallery.GalleryModel)) {
			m := base()
			change(m)
			Expect(gallery.EntryDigest(m, nil, "x")).ToNot(Equal(gallery.EntryDigest(base(), nil, "x")))
		},
		Entry("a file checksum (the checksum bot)", func(m *gallery.GalleryModel) { m.AdditionalFiles[0].SHA256 = "CD34" }),
		Entry("a file uri", func(m *gallery.GalleryModel) { m.AdditionalFiles[0].URI = "https://example.com/v2.gguf" }),
		Entry("an override", func(m *gallery.GalleryModel) { m.Overrides["context_size"] = 4096 }),
		Entry("a config_file", func(m *gallery.GalleryModel) { m.ConfigFile = map[string]any{"backend": "llama-cpp"} }),
		Entry("the base config url", func(m *gallery.GalleryModel) { m.URL = "github:example/gallery/tiny.yaml@v2" }),
		Entry("a new variant", func(m *gallery.GalleryModel) { m.Variants = []gallery.Variant{{Model: "tiny-mlx"}} }),
	)

	It("changes with the base config bytes", func() {
		Expect(gallery.EntryDigest(base(), nil, "a")).ToNot(Equal(gallery.EntryDigest(base(), nil, "b")))
	})

	It("does not depend on the order of the variants or the case of a checksum", func() {
		a, b := base(), base()
		a.Variants = []gallery.Variant{{Model: "x"}, {Model: "y"}}
		b.Variants = []gallery.Variant{{Model: "y"}, {Model: "x"}}
		b.AdditionalFiles[0].SHA256 = "ab12"
		Expect(gallery.EntryDigest(a, nil, "")).To(Equal(gallery.EntryDigest(b, nil, "")))
	})

	It("includes the payload of the resolved variant", func() {
		parent := base()
		parent.Variants = []gallery.Variant{{Model: "tiny-q8"}}
		v1 := &gallery.GalleryModel{Metadata: gallery.Metadata{AdditionalFiles: []gallery.File{{Filename: "q8.gguf", SHA256: "1"}}}}
		v2 := &gallery.GalleryModel{Metadata: gallery.Metadata{AdditionalFiles: []gallery.File{{Filename: "q8.gguf", SHA256: "2"}}}}
		Expect(gallery.EntryDigest(parent, v1, "")).ToNot(Equal(gallery.EntryDigest(parent, v2, "")))
		Expect(gallery.EntryDigest(parent, v1, "")).ToNot(Equal(gallery.EntryDigest(parent, parent, "")))
	})
})

var _ = Describe("The digest an install records", func() {
	var (
		dir, srcDir string
		systemState *system.SystemState
		galleries   []config.Gallery
	)
	revision := 0

	writeGallery := func(entries ...gallery.GalleryModel) {
		out, err := yaml.Marshal(entries)
		Expect(err).ToNot(HaveOccurred())
		revision++
		name := fmt.Sprintf("digest-%d", revision)
		path := filepath.Join(srcDir, name+".yaml")
		Expect(os.WriteFile(path, out, 0600)).To(Succeed())
		galleries = []config.Gallery{{Name: name, URL: "file://" + path}}
	}

	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "digest")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(dir)).To(Succeed()) })
		srcDir = filepath.Join(dir, "src")
		Expect(os.MkdirAll(srcDir, 0750)).To(Succeed())
		systemState, err = system.GetSystemState(system.WithModelPath(dir))
		Expect(err).ToNot(HaveOccurred())
	})

	It("matches the digest of the gallery entry until its base config changes", func() {
		baseConfig := filepath.Join(srcDir, "tiny-base.yaml")
		Expect(os.WriteFile(baseConfig, []byte("name: tiny\nconfig_file: |\n  backend: llama-cpp\nprompt_templates:\n- name: tiny-chat\n  content: \"{{.Input}}\"\n"), 0600)).To(Succeed())
		writeGallery(gallery.GalleryModel{Metadata: gallery.Metadata{Name: "tiny", URL: "file://" + baseConfig}})
		Expect(gallery.InstallModelFromGallery(context.TODO(), galleries, nil, systemState, nil, galleries[0].Name+"@tiny",
			gallery.GalleryModel{}, func(string, string, string, float64) {}, false, false, false)).To(Succeed())

		sc, err := gallery.ReadSidecar(dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		Expect(sc.Origin.EntryDigest).To(HavePrefix("sha256:"))
		Expect(sc.Origin.BaseConfigSHA256).ToNot(BeEmpty())
		Expect(sc.Managed).To(ContainElement(HaveField("Path", "tiny-chat.tmpl")))

		current := func() string {
			models, err := gallery.AvailableGalleryModels(galleries, systemState)
			Expect(err).ToNot(HaveOccurred())
			parent, resolved, _ := gallery.InstalledEntry(models, sc.Origin.Gallery, sc.Origin.EntryName, sc.ResolvedVariant)
			Expect(parent).ToNot(BeNil())
			digest, _, err := gallery.CurrentEntryDigest(context.TODO(), gallery.NewBaseConfigFetcher(), dir, parent, resolved)
			Expect(err).ToNot(HaveOccurred())
			return digest
		}
		Expect(current()).To(Equal(sc.Origin.EntryDigest))

		// A template change in a base config at @master: same url, new bytes.
		Expect(os.WriteFile(baseConfig, []byte("name: tiny\nconfig_file: |\n  backend: llama-cpp\nprompt_templates:\n- name: tiny-chat\n  content: \"Q: {{.Input}}\"\n"), 0600)).To(Succeed())
		Expect(current()).ToNot(Equal(sc.Origin.EntryDigest))
	})

	It("reports an entry the gallery no longer lists", func() {
		writeGallery(gallery.GalleryModel{Metadata: gallery.Metadata{Name: "other"}, Overrides: map[string]any{"backend": "x"}})
		models, err := gallery.AvailableGalleryModels(galleries, systemState)
		Expect(err).ToNot(HaveOccurred())
		parent, _, _ := gallery.InstalledEntry(models, galleries[0].Name, "gone", "")
		Expect(parent).To(BeNil())
	})
})
