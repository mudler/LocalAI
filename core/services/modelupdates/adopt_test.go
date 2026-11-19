package modelupdates_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/modelupdates"
)

var _ = Describe("Adopting a model installed by an earlier release", func() {
	var (
		f   *fixture
		ctx context.Context
		u   *modelupdates.Upgrader
	)

	// legacy rewrites the gallery file the way a release without origins
	// wrote it: the gallery config of the install, no version, no origin.
	legacy := func() {
		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		sc.Version, sc.Origin, sc.Managed, sc.ConfigSHA256 = 0, nil, nil, ""
		Expect(gallery.WriteSidecar(f.dir, "tiny", sc)).To(Succeed())
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(f.dir, name))
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
	secondGallery := func() {
		index, err := os.ReadFile(filepath.Join(f.src, "index.yaml"))
		Expect(err).ToNot(HaveOccurred())
		path := filepath.Join(f.src, "mirror.yaml")
		Expect(os.WriteFile(path, index, 0600)).To(Succeed())
		f.galleries = append(f.galleries, config.Gallery{Name: f.galleries[0].Name + "-mirror", URL: "file://" + path})
		gallery.ForgetGalleryIndexes()
	}

	BeforeEach(func() {
		f = newFixture()
		ctx = context.Background()
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v1")))
		f.install("tiny")
		legacy()
		u = &modelupdates.Upgrader{SystemState: f.systemState, ListGallery: f.list}
	})

	It("records the origin and what the gallery writes, and leaves the config on disk alone", func() {
		before := read("tiny.yaml")
		res, err := u.Adopt(ctx, modelupdates.AdoptRequest{Name: "tiny"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Gallery).To(Equal(f.galleries[0].Name))
		Expect(res.Entry).To(Equal("tiny"))
		Expect(res.FilesMatch).To(BeTrue())
		Expect(read("tiny.yaml")).To(Equal(before))

		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		Expect(sc.IsV2()).To(BeTrue())
		Expect(sc.Origin.EntryName).To(Equal("tiny"))
		Expect(sc.Origin.ConfigYAML).ToNot(BeEmpty())
		Expect(sc.ConfigSHA256).To(Equal(gallery.SHA256Hex([]byte(before))))
		Expect(sc.Managed).To(ContainElement(HaveField("Path", "tiny.gguf")))

		svc := f.service(modelupdates.Options{})
		Expect(itemOf(svc.CheckNow(ctx), "tiny").State).To(Equal(modelupdates.StateUpToDate))

		By("refusing a second adoption")
		_, err = u.Adopt(ctx, modelupdates.AdoptRequest{Name: "tiny"})
		Expect(err).To(MatchError(modelupdates.ErrAlreadyAdopted))
	})

	It("shows an edit made before the adoption as an edit", func() {
		patchDisk(f, "tiny", func(m map[string]any) { m["context_size"] = 8192 })
		_, err := u.Adopt(ctx, modelupdates.AdoptRequest{Name: "tiny"})
		Expect(err).ToNot(HaveOccurred())
		e := tinyEntry(f, f.weights("tiny.gguf", "v1"))
		e.Overrides["context_size"] = 4096
		f.publish(e)
		it := itemOf(f.service(modelupdates.Options{}).CheckNow(ctx), "tiny")
		Expect(it.State).To(Equal(modelupdates.StateEdited))
		Expect(it.EditedKeys).To(ContainElement(HaveField("Key", "context_size")))
	})

	It("offers the update of a model whose files are older than the entry", func() {
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		res, err := u.Adopt(ctx, modelupdates.AdoptRequest{Name: "tiny"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.FilesMatch).To(BeFalse())
		it := itemOf(f.service(modelupdates.Options{}).CheckNow(ctx), "tiny")
		Expect(it.State).To(Equal(modelupdates.StateUpdateAvailable))
		Expect(it.ChangedFiles).To(ContainElement(HaveField("Path", "tiny.gguf")))

		By("upgrading it as any other model")
		u.Runtime = newRuntime()
		Expect(u.Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(Succeed())
		Expect(read("tiny.gguf")).To(Equal("v2"))
	})

	It("lists the candidates when more than one gallery has the entry, and takes the gallery named", func() {
		secondGallery()
		_, err := u.Adopt(ctx, modelupdates.AdoptRequest{Name: "tiny"})
		var ambiguous *modelupdates.ErrAmbiguousEntry
		Expect(errors.As(err, &ambiguous)).To(BeTrue())
		Expect(ambiguous.Candidates).To(ConsistOf(
			modelupdates.AdoptCandidate{Gallery: f.galleries[0].Name, Entry: "tiny"},
			modelupdates.AdoptCandidate{Gallery: f.galleries[1].Name, Entry: "tiny"}))

		res, err := u.Adopt(ctx, modelupdates.AdoptRequest{Name: "tiny", Gallery: f.galleries[1].Name})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Gallery).To(Equal(f.galleries[1].Name))
	})

	It("refuses when no gallery lists the entry, with the entries it could take", func() {
		_, err := u.Adopt(ctx, modelupdates.AdoptRequest{Name: "tiny", Gallery: "elsewhere"})
		var none *modelupdates.ErrNoCandidate
		Expect(errors.As(err, &none)).To(BeTrue())
		Expect(none.Candidates).To(ConsistOf(modelupdates.AdoptCandidate{Gallery: f.galleries[0].Name, Entry: "tiny"}))

		_, err = u.Adopt(ctx, modelupdates.AdoptRequest{Name: "tiny", Entry: "missing"})
		Expect(errors.As(err, &none)).To(BeTrue())
		Expect(none.Candidates).To(BeEmpty())
	})

	It("refuses a model with no record of a gallery install, and one that is not installed", func() {
		Expect(os.Remove(gallery.SidecarPath(f.dir, "tiny"))).To(Succeed())
		_, err := u.Adopt(ctx, modelupdates.AdoptRequest{Name: "tiny"})
		Expect(err).To(MatchError(modelupdates.ErrNoInstallRecord))
		_, err = u.Adopt(ctx, modelupdates.AdoptRequest{Name: "none"})
		Expect(err).To(MatchError(modelupdates.ErrNotFound))
	})
})
