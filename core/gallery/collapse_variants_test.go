package gallery_test

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/gallery"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// VariantReferencedIDs answers the only question the deduplicated listing asks:
// which entries does another entry already offer, and so need no row of their
// own. Everything else is shown.
var _ = Describe("VariantReferencedIDs", func() {
	entry := func(gal, name string, variants ...string) *GalleryModel {
		m := &GalleryModel{}
		m.Name = name
		m.Gallery = config.Gallery{Name: gal}
		for _, v := range variants {
			m.Variants = append(m.Variants, Variant{Model: v})
		}
		return m
	}

	It("reports the builds a parent references and nothing else", func() {
		parent := entry("test", "parent", "build-a", "build-b")
		models := []*GalleryModel{
			parent,
			entry("test", "build-a"),
			entry("test", "build-b"),
			entry("test", "standalone"),
		}

		Expect(VariantReferencedIDs(models)).To(HaveLen(2))
		Expect(VariantReferencedIDs(models)).To(HaveKey("test@build-a"))
		Expect(VariantReferencedIDs(models)).To(HaveKey("test@build-b"))
		// The parent is installable in its own right, and a standalone entry
		// is nobody's variant.
		Expect(VariantReferencedIDs(models)).NotTo(HaveKey("test@parent"))
		Expect(VariantReferencedIDs(models)).NotTo(HaveKey("test@standalone"))
	})

	It("never hides an entry that declares variants of its own", func() {
		// A parent referencing another parent is an authoring error that
		// variant resolution refuses to install. The listing still has to stay
		// coherent: if the chain hid the middle entry, the build it offers
		// would be unreachable, since nothing visible would mention it.
		top := entry("test", "top", "middle")
		middle := entry("test", "middle", "leaf")
		models := []*GalleryModel{top, middle, entry("test", "leaf")}

		referenced := VariantReferencedIDs(models)
		Expect(referenced).NotTo(HaveKey("test@middle"),
			"hiding a parent would strand the builds only it offers")
		Expect(referenced).To(HaveKey("test@leaf"))
	})

	It("ignores an entry that references itself", func() {
		// Self-reference would otherwise erase the entry from the gallery.
		models := []*GalleryModel{entry("test", "loop", "loop", "build")}
		models = append(models, entry("test", "build"))

		Expect(VariantReferencedIDs(models)).NotTo(HaveKey("test@loop"))
		Expect(VariantReferencedIDs(models)).To(HaveKey("test@build"))
	})

	It("ignores a reference that names no existing entry", func() {
		// A dangling reference hides nothing; reporting it is the linter's job.
		models := []*GalleryModel{entry("test", "parent", "does-not-exist")}
		Expect(VariantReferencedIDs(models)).To(BeEmpty())
	})

	It("resolves references the way install-time lookup does", func() {
		// Matching has to agree with FindGalleryElement, otherwise a reference
		// that installs fine would fail to hide its target. Case-insensitive,
		// and an "@" addresses gallery and name together.
		models := []*GalleryModel{
			entry("test", "parent", "BUILD-A", "other@build-b"),
			entry("test", "build-a"),
			entry("other", "build-b"),
			entry("test", "build-b"),
		}

		referenced := VariantReferencedIDs(models)
		Expect(referenced).To(HaveKey("test@build-a"))
		Expect(referenced).To(HaveKey("other@build-b"))
		// The qualified reference names the other gallery's entry, so the
		// same-named local entry keeps its row.
		Expect(referenced).NotTo(HaveKey("test@build-b"))
	})

	It("returns an empty set for a gallery declaring no variants at all", func() {
		models := []*GalleryModel{entry("test", "a"), entry("test", "b")}
		Expect(VariantReferencedIDs(models)).To(BeEmpty())
	})
})
