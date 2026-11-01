package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
)

func open(dir string) *catalog.Sources {
	s, err := Open(dir, true, false)
	Expect(err).ToNot(HaveOccurred())
	return s
}

func byID(s *catalog.Sources, id string) (catalog.Source, bool) {
	for _, e := range s.Entries() {
		if e.ID == id {
			return e, true
		}
	}
	return catalog.Source{}, false
}

// modelLibrary is the gallery entry of the shipped model library, which the
// server reads next to the galleries the flags name.
func modelLibrary() config.Gallery {
	return config.Gallery{Name: catalog.OfficialModelLibrary().Name, URL: catalog.OfficialModelLibraryURL, Library: true}
}

// setModelSource sets the build-time model source for one spec.
func setModelSource(v string) {
	old := ModelSource
	ModelSource = v
	DeferCleanup(func() { ModelSource = old })
}

// modelGalleries runs the startup option with the default flags and returns
// the model galleries the server would read.
func modelGalleries() []config.Gallery {
	o := config.NewApplicationConfig(
		config.WithStringGalleries(config.DefaultGalleriesJSON),
		config.WithBackendGalleries(config.DefaultBackendGalleriesJSON),
	)
	Option(GinkgoT().TempDir(), true, false, true)(o)
	return o.Galleries
}

// setBackendSource sets the build-time backend source for one spec.
func setBackendSource(v string) {
	old := BackendSource
	BackendSource = v
	DeferCleanup(func() { BackendSource = old })
}

// backendLibraryGallery is the gallery entry of the shipped backend library,
// which the server reads in place of the backend gallery.
func backendLibraryGallery() config.Gallery {
	return config.Gallery{Name: catalog.OfficialBackendLibrary().Name, URL: catalog.OfficialBackendLibraryURL, Library: true}
}

// backendGalleries runs the startup option the way the server runs it, with the
// default flags and the backend library on, and returns the backend galleries
// the server would read.
func backendGalleries(backendLibrary bool) []config.Gallery {
	o := config.NewApplicationConfig(
		config.WithStringGalleries(config.DefaultGalleriesJSON),
		config.WithBackendGalleries(config.DefaultBackendGalleriesJSON),
	)
	Option(GinkgoT().TempDir(), true, backendLibrary, true)(o)
	return o.BackendGalleries
}

var _ = Describe("mapping flags and sources", func() {
	It("leaves the defaults flags unchanged", func() {
		store := open(GinkgoT().TempDir())
		models, backends := parse(config.DefaultGalleriesJSON), parse(config.DefaultBackendGalleriesJSON)
		for kind, in := range map[catalog.Kind][]config.Gallery{catalog.KindModel: models, catalog.KindBackend: backends} {
			added, err := MapGalleries(store, kind, in)
			Expect(err).ToNot(HaveOccurred(), "%s", kind)
			Expect(added).To(BeEmpty(), "%s: the shipped gallery was added as a user source", kind)
			// The same JSON loads, mirrors and policy included.
			want := in
			if kind == catalog.KindModel {
				// A master build reads the model library instead of the localai gallery.
				want = []config.Gallery{modelLibrary()}
			}
			Expect(ApplyGalleries(store, kind, in)).To(Equal(want), "%s: galleries changed", kind)
		}
	})

	It("makes each flag entry a user source of its kind", func() {
		store := open(GinkgoT().TempDir())
		models := append(parse(config.DefaultGalleriesJSON), config.Gallery{Name: "Team Models", URL: "https://example.org/models.yaml"})
		backends := append(parse(config.DefaultBackendGalleriesJSON), config.Gallery{Name: "Team Backends", URL: "https://example.org/backends.yaml"})
		_, err := MapGalleries(store, catalog.KindModel, models)
		Expect(err).ToNot(HaveOccurred())
		_, err = MapGalleries(store, catalog.KindBackend, backends)
		Expect(err).ToNot(HaveOccurred())
		m, ok := byID(store, "team-models")
		Expect(ok).To(BeTrue())
		Expect(m.Kind).To(Equal(catalog.KindModel), "model source = %+v", m)
		Expect(m.Default).To(BeFalse(), "model source = %+v", m)
		Expect(m.Enabled).To(BeTrue(), "model source = %+v", m)
		Expect(m.Format).To(Equal(catalog.FormatGallery), "model source = %+v", m)
		Expect(m.URL).To(Equal("https://example.org/models.yaml"))
		Expect(m.Name).To(Equal("Team Models"))
		b, ok := byID(store, "team-backends")
		Expect(ok).To(BeTrue())
		Expect(b.Kind).To(Equal(catalog.KindBackend), "backend source = %+v", b)
		Expect(b.Default).To(BeFalse(), "backend source = %+v", b)
		Expect(b.Enabled).To(BeTrue(), "backend source = %+v", b)
		// The flags still decide the list the server reads.
		Expect(ApplyGalleries(store, catalog.KindModel, models)).To(Equal([]config.Gallery{models[1], modelLibrary()}))
	})

	It("maps additively and never twice", func() {
		dir := GinkgoT().TempDir()
		flag := []config.Gallery{{Name: "mine", URL: "https://example.org/a.yaml"}}
		for run := range 3 {
			store := open(dir)
			added, err := MapGalleries(store, catalog.KindModel, flag)
			Expect(err).ToNot(HaveOccurred())
			Expect(run == 0).To(Equal(len(added) == 1), "start %d added %d sources", run, len(added))
		}
		n := 0
		for _, e := range open(dir).Entries() {
			if e.URL == flag[0].URL {
				n++
			}
		}
		Expect(n).To(Equal(1), "the entry is listed %d times", n)
	})

	// A source the user added with the CLI is not added a second time when a flag
	// names the same index.
	It("does not add an entry that is already a user source", func() {
		store := open(GinkgoT().TempDir())
		_, err := Add(store, catalog.KindModel, "https://example.org/a.yaml", "chosen", "Chosen", "")
		Expect(err).ToNot(HaveOccurred())
		added, err := MapGalleries(store, catalog.KindModel, []config.Gallery{{Name: "other-name", URL: "https://example.org/a.yaml"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(added).To(BeEmpty())
		_, ok := byID(store, "other-name")
		Expect(ok).To(BeFalse(), "a second record was made for the same index")
	})

	It("never lets mapped IDs collide", func() {
		store := open(GinkgoT().TempDir())
		// "localai" is the id of the shipped model gallery; a different index with
		// the same name gets its own id.
		added, err := MapGalleries(store, catalog.KindModel, []config.Gallery{{Name: "localai", URL: "https://example.org/other.yaml"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(added).To(HaveLen(1), "added %+v", added)
		Expect(added[0].ID).To(Equal("localai-2"))
		e, _ := byID(store, "localai")
		Expect(e.Default).To(BeTrue(), "the default was changed: %+v", e)
		Expect(e.URL).ToNot(Equal("https://example.org/other.yaml"), "the default was changed: %+v", e)
	})

	It("drops removed or disabled sources from the gallery list", func() {
		dir := GinkgoT().TempDir()
		store := open(dir)
		models := append(parse(config.DefaultGalleriesJSON), config.Gallery{Name: "mine", URL: "https://example.org/a.yaml"})
		_, err := MapGalleries(store, catalog.KindModel, models)
		Expect(err).ToNot(HaveOccurred())
		Expect(store.RemoveSource(DefaultModelSourceID)).To(Succeed())
		Expect(store.SetEnabled("mine", false)).To(Succeed())
		// The shipped model library is listed until the user removes it.
		got := ApplyGalleries(store, catalog.KindModel, models)
		Expect(got).To(HaveLen(1), "galleries = %+v", got)
		Expect(got[0].Library).To(BeTrue(), "galleries = %+v", got)
		Expect(store.RemoveSource(catalog.OfficialModelLibraryID)).To(Succeed())
		Expect(ApplyGalleries(store, catalog.KindModel, models)).To(BeEmpty())
		// The removal holds at the next start, with the same flags.
		o := config.NewApplicationConfig(config.WithStringGalleries(config.DefaultGalleriesJSON))
		Option(dir, true, false, true)(o)
		Expect(o.Galleries).To(BeEmpty(), "the removed default came back")
		e, _ := byID(open(dir), DefaultModelSourceID)
		Expect(e.IsTombstone()).To(BeTrue(), "record = %+v", e)
	})

	It("lets the sources the user added reach the server", func() {
		store := open(GinkgoT().TempDir())
		_, err := Add(store, catalog.KindModel, "https://example.org/lib.yaml", "", "Model library", "")
		Expect(err).ToNot(HaveOccurred())
		_, err = Add(store, catalog.KindBackend, "https://example.org/bk.yaml", "bk", "", catalog.FormatGallery)
		Expect(err).ToNot(HaveOccurred())
		// A library-format backend source is read by the add-on catalog, not as a gallery.
		_, err = Add(store, catalog.KindBackend, "https://example.org/bklib.yaml", "bklib", "", "")
		Expect(err).ToNot(HaveOccurred())
		models := ApplyGalleries(store, catalog.KindModel, parse(config.DefaultGalleriesJSON))
		Expect(models).To(HaveLen(2), "models = %+v", models)
		Expect(models[0].Library).To(BeTrue(), "models = %+v", models)
		Expect(models[0].URL).To(Equal("https://example.org/lib.yaml"))
		Expect(models[0].Name).To(Equal("Model library"))
		Expect(models[1]).To(Equal(modelLibrary()))
		backends := ApplyGalleries(store, catalog.KindBackend, parse(config.DefaultBackendGalleriesJSON))
		Expect(backends).To(HaveLen(2), "backends = %+v", backends)
		Expect(backends[1].Library).To(BeFalse(), "backends = %+v", backends)
		Expect(backends[1].URL).To(Equal("https://example.org/bk.yaml"))
	})
})

var _ = Describe("the startup option", func() {
	// The startup option, run the way the server runs it, with the default flags.
	It("keeps default deployments as they were", func() {
		dir := GinkgoT().TempDir()
		build := func() *config.ApplicationConfig {
			o := config.NewApplicationConfig(
				config.WithStringGalleries(config.DefaultGalleriesJSON),
				config.WithBackendGalleries(config.DefaultBackendGalleriesJSON),
			)
			Option(dir, true, false, true)(o)
			return o
		}
		want := config.NewApplicationConfig(
			config.WithStringGalleries(config.DefaultGalleriesJSON),
			config.WithBackendGalleries(config.DefaultBackendGalleriesJSON),
		)
		for i := range 2 {
			o := build()
			Expect(o.Galleries).To(Equal([]config.Gallery{modelLibrary()}), "start %d changed the galleries", i)
			Expect(o.BackendGalleries).To(Equal(want.BackendGalleries), "start %d changed the galleries", i)
		}
		// The shipped defaults are listed after the first start.
		var ids []string
		for _, e := range open(dir).Entries() {
			ids = append(ids, e.ID)
		}
		Expect(strings.Join(ids, ",")).To(Equal("official,localai-backends,localai,official-models"), "sources")
	})

	// A data path the process cannot write must not stop the server.
	It("survives an unusable store", func() {
		file := filepath.Join(GinkgoT().TempDir(), "file")
		Expect(os.WriteFile(file, []byte("x"), 0o600)).To(Succeed())
		o := config.NewApplicationConfig(config.WithStringGalleries(config.DefaultGalleriesJSON))
		before := append([]config.Gallery(nil), o.Galleries...)
		Option(filepath.Join(file, "sub"), true, false, true)(o)
		Expect(o.Galleries).To(Equal(before), "galleries changed")
	})

	// A custom --galleries / --backend-galleries JSON still loads as it is, the
	// autoload switches are untouched, and each entry is listed as a source.
	It("loads custom flags as before", func() {
		dir := GinkgoT().TempDir()
		models := `[{"name":"a","url":"https://example.org/a.yaml"},{"name":"b","url":"https://example.org/b.yaml","mirrors":["https://mirror.example.org/b.yaml"]}]`
		backends := `[{"name":"c","url":"https://example.org/c.yaml"}]`
		o := config.NewApplicationConfig(
			config.WithStringGalleries(models),
			config.WithBackendGalleries(backends),
			config.EnableGalleriesAutoload,
			config.EnableBackendGalleriesAutoload,
		)
		Option(dir, true, false, true)(o)
		Expect(o.Galleries).To(Equal(append(parse(models), modelLibrary())), "galleries changed")
		Expect(o.BackendGalleries).To(Equal(parse(backends)), "galleries changed")
		Expect(o.AutoloadGalleries).To(BeTrue(), "an autoload switch changed")
		Expect(o.AutoloadBackendGalleries).To(BeTrue(), "an autoload switch changed")
		store := open(dir)
		for id, kind := range map[string]catalog.Kind{"a": catalog.KindModel, "b": catalog.KindModel, "c": catalog.KindBackend} {
			e, ok := byID(store, id)
			Expect(ok).To(BeTrue(), "source %s", id)
			Expect(e.Kind).To(Equal(kind), "source %s = %+v", id, e)
			Expect(e.Default).To(BeFalse(), "source %s = %+v", id, e)
		}
	})
})

var _ = Describe("the model source of a build", func() {
	// A build from master, and any build that sets no value, reads the model
	// library. The release workflow passes ModelSource=gallery for a tagged build
	// until it cuts over.
	It("defaults a master build to the model library", func() {
		Expect(ModelSource).To(Equal(ModelSourceLibrary), "the unset build value is %q; a master build must read the library", ModelSource)
		Expect(UseModelLibrary()).To(BeTrue())
		got := modelGalleries()
		Expect(got).To(HaveLen(1), "a master build reads %+v, want only the model library", got)
		Expect(got[0].URL).To(Equal(catalog.OfficialModelLibraryURL), "a master build reads %+v, want only the model library", got)
		for _, g := range got {
			Expect(g.Name).ToNot(Equal("localai"), "a master build reads the localai gallery by default: %+v", g)
		}
	})

	It("defaults a tagged build to the gallery until the cutover", func() {
		setModelSource(ModelSourceGallery)
		Expect(UseModelLibrary()).To(BeFalse(), "a tagged build before the cutover must not read the library")
		got := modelGalleries()
		Expect(got).To(HaveLen(1), "a tagged build reads %+v, want only the localai gallery", got)
		Expect(got[0].Name).To(Equal("localai"), "a tagged build reads %+v, want only the localai gallery", got)
		Expect(got[0].Library).To(BeFalse(), "a tagged build reads %+v, want only the localai gallery", got)
		// The cutover: the release workflow passes the library value.
		setModelSource(ModelSourceLibrary)
		got = modelGalleries()
		Expect(got).To(HaveLen(1), "after the cutover a tagged build reads %+v, want only the model library", got)
		Expect(got[0].URL).To(Equal(catalog.OfficialModelLibraryURL), "after the cutover a tagged build reads %+v, want only the model library", got)
	})

	// Only the exact value "gallery" turns the library off, so a mistyped build
	// value never leaves a build without a model source.
	It("reads the library for an unknown model source", func() {
		setModelSource("galery")
		Expect(UseModelLibrary()).To(BeTrue(), "an unknown value must fall back to the library")
	})

	// The other default stays shipped and can be turned on by the user.
	It("ships both model sources with one enabled", func() {
		for _, lib := range []bool{true, false} {
			var enabled []string
			for _, s := range defaults(true, false, lib, true) {
				if s.Kind == catalog.KindModel && s.Enabled {
					enabled = append(enabled, s.ID)
				}
			}
			want := catalog.OfficialModelLibraryID
			if !lib {
				want = DefaultModelSourceID
			}
			Expect(enabled).To(Equal([]string{want}), "library=%v: enabled model sources", lib)
		}
	})
})

var _ = Describe("the backend source of a build", func() {
	// A build from master, and any build that sets no value, reads the backend
	// library. The release workflow passes BackendSource=gallery for a tagged build
	// until it cuts over.
	It("defaults a master build to the backend library", func() {
		Expect(BackendSource).To(Equal(BackendSourceLibrary), "the unset build value is %q; a master build must read the library", BackendSource)
		Expect(UseBackendLibrary()).To(BeTrue())
		got := backendGalleries(true)
		Expect(got).To(HaveLen(1), "a master build reads %+v, want only the backend library", got)
		Expect(got[0]).To(Equal(backendLibraryGallery()), "a master build reads %+v, want only the backend library", got)
		for _, g := range got {
			Expect(g.Name).ToNot(Equal("localai"), "a master build reads the localai backend gallery by default: %+v", g)
		}
	})

	It("defaults a tagged build to the backend gallery until the cutover", func() {
		setBackendSource(BackendSourceGallery)
		Expect(UseBackendLibrary()).To(BeFalse(), "a tagged build before the cutover must not read the library")
		want := parse(config.DefaultBackendGalleriesJSON)
		Expect(backendGalleries(true)).To(Equal(want), "a tagged build must read only the backend gallery")
		// The cutover: the release workflow passes the library value.
		setBackendSource(BackendSourceLibrary)
		got := backendGalleries(true)
		Expect(got).To(HaveLen(1), "after the cutover a tagged build reads %+v, want only the backend library", got)
		Expect(got[0].URL).To(Equal(catalog.OfficialBackendLibraryURL), "after the cutover a tagged build reads %+v, want only the backend library", got)
		Expect(got[0].Library).To(BeTrue(), "after the cutover a tagged build reads %+v, want only the backend library", got)
	})

	// Only the exact value "gallery" turns the library off, so a mistyped build
	// value never leaves a build without a backend source.
	It("reads the library for an unknown backend source", func() {
		for _, v := range []string{"galery", "", "Gallery", "library"} {
			setBackendSource(v)
			Expect(UseBackendLibrary()).To(BeTrue(), "%q must fall back to the library", v)
		}
	})

	// A deployment that turns the backend library off reads the backend gallery on
	// every build, so it never loses its backend source.
	It("reads the gallery on any build when the backend library is off", func() {
		want := parse(config.DefaultBackendGalleriesJSON)
		for _, v := range []string{BackendSourceLibrary, BackendSourceGallery} {
			setBackendSource(v)
			Expect(backendGalleries(false)).To(Equal(want), "BackendSource=%s with the library off", v)
		}
	})

	// The other default stays shipped and can be turned on by the user.
	It("ships both backend sources with one enabled", func() {
		for _, lib := range []bool{true, false} {
			var enabled, shipped []string
			for _, s := range defaults(true, true, true, lib) {
				if s.Kind == catalog.KindBackend {
					shipped = append(shipped, s.ID)
					if s.Enabled {
						enabled = append(enabled, s.ID)
					}
				}
			}
			want := catalog.OfficialBackendLibraryID
			if !lib {
				want = DefaultBackendSourceID
			}
			Expect(shipped).To(HaveLen(2), "library=%v: shipped backend sources, want both", lib)
			Expect(enabled).To(Equal([]string{want}), "library=%v: enabled backend sources", lib)
		}
		// With the backend library not shipped the gallery is the only source.
		for _, s := range defaults(true, false, true, true) {
			Expect(s.ID).ToNot(Equal(catalog.OfficialBackendLibraryID), "the backend library is shipped although it is off: %+v", s)
			if s.ID == DefaultBackendSourceID {
				Expect(s.Enabled).To(BeTrue(), "the backend gallery must be enabled when the library is off")
			}
		}
	})

	// The shipped backend library is a backend source in library format, so the
	// server hands it to the gallery reader as a library, and the user can turn the
	// other default on to read both.
	It("lists the backend library as a backend source", func() {
		store, err := Open(GinkgoT().TempDir(), true, true)
		Expect(err).ToNot(HaveOccurred())
		e, ok := byID(store, catalog.OfficialBackendLibraryID)
		Expect(ok).To(BeTrue())
		Expect(e.Kind).To(Equal(catalog.KindBackend), "backend library source = %+v", e)
		Expect(e.Default).To(BeTrue(), "backend library source = %+v", e)
		Expect(e.Enabled).To(BeTrue(), "backend library source = %+v", e)
		Expect(e.Format).To(BeEmpty(), "backend library source = %+v", e)
		g, ok := byID(store, DefaultBackendSourceID)
		Expect(ok).To(BeTrue(), "the backend gallery is shipped")
		Expect(g.Enabled).To(BeFalse(), "the backend gallery is shipped but off on a master build: %+v", g)
		Expect(g.Format).To(Equal(catalog.FormatGallery), "the backend gallery is shipped but off on a master build: %+v", g)
		Expect(store.SetEnabled(DefaultBackendSourceID, true)).To(Succeed())
		got := ApplyGalleries(store, catalog.KindBackend, parse(config.DefaultBackendGalleriesJSON))
		Expect(got).To(HaveLen(2), "with both on the server reads the gallery and the library: %+v", got)
		Expect(got[0].Name).To(Equal("localai"), "with both on the server reads the gallery and the library: %+v", got)
		Expect(got[1]).To(Equal(backendLibraryGallery()))
	})

	// A backend library the user adds, which is not in gallery format, is read by
	// the add-on catalog only, as before: it installs by digest under the signature
	// policy of its library. The shipped backend library is the exception, because
	// it is the backend source of a build.
	It("leaves a user backend library to the catalog", func() {
		store, err := Open(GinkgoT().TempDir(), true, false)
		Expect(err).ToNot(HaveOccurred())
		_, err = Add(store, catalog.KindBackend, "https://example.org/backends/index.yaml", "", "Team backends", "")
		Expect(err).ToNot(HaveOccurred())
		want := parse(config.DefaultBackendGalleriesJSON)
		Expect(ApplyGalleries(store, catalog.KindBackend, want)).To(Equal(want), "galleries")
		_, got := Reconcile(store, nil, want)
		Expect(got).To(Equal(want), "reconciled galleries")
	})

	// A source turned on or off while the server runs changes the backend
	// galleries it reads: the shipped backend library comes and goes as a library.
	It("adds and drops the backend library on reconcile", func() {
		store, err := Open(GinkgoT().TempDir(), true, true)
		Expect(err).ToNot(HaveOccurred())
		gallerySet := parse(config.DefaultBackendGalleriesJSON)
		// On a master build the library is enabled and the gallery is off.
		Expect(store.SetEnabled(DefaultBackendSourceID, true)).To(Succeed())
		_, got := Reconcile(store, nil, gallerySet)
		Expect(got).To(HaveLen(2), "both on: %+v", got)
		Expect(got[1]).To(Equal(backendLibraryGallery()), "both on: %+v", got)
		Expect(store.SetEnabled(catalog.OfficialBackendLibraryID, false)).To(Succeed())
		_, got = Reconcile(store, nil, got)
		Expect(got).To(Equal(gallerySet), "the library is off, so only the gallery is read")
		Expect(store.SetEnabled(DefaultBackendSourceID, false)).To(Succeed())
		Expect(store.SetEnabled(catalog.OfficialBackendLibraryID, true)).To(Succeed())
		_, got = Reconcile(store, nil, got)
		Expect(got).To(HaveLen(1), "only the library is on: %+v", got)
		Expect(got[0]).To(Equal(backendLibraryGallery()), "only the library is on: %+v", got)
	})
})

var _ = Describe("mirrors and local add-on libraries", func() {
	It("carries mirrors to the gallery and lets a changed source win", func() {
		store := open(GinkgoT().TempDir())
		_, err := AddWithMirrors(store, catalog.KindModel, "https://example.org/team.yaml", "team", "Team", catalog.FormatGallery, []string{"https://m1.example/team.yaml"})
		Expect(err).ToNot(HaveOccurred())
		models, _ := Reconcile(store, nil, nil)
		var got []string
		for _, g := range models {
			if g.URL == "https://example.org/team.yaml" {
				got = g.Mirrors
			}
		}
		Expect(got).To(Equal([]string{"https://m1.example/team.yaml"}), "an appended gallery carries the source's mirrors")
		// A gallery already listed keeps its mirrors while the source lists none.
		Expect(store.SetMirrors("team", nil)).To(Succeed())
		kept, _ := Reconcile(store, []config.Gallery{{Name: "Team", URL: "https://example.org/team.yaml", Mirrors: []string{"https://flag.example/t.yaml"}}}, nil)
		for _, g := range kept {
			if g.URL == "https://example.org/team.yaml" {
				Expect(g.Mirrors).To(Equal([]string{"https://flag.example/t.yaml"}), "a listed gallery keeps its mirrors")
			}
		}
		// A changed list wins over the listed gallery's.
		Expect(store.SetMirrors("team", []string{"https://m2.example/team.yaml"})).To(Succeed())
		won, _ := Reconcile(store, []config.Gallery{{Name: "Team", URL: "https://example.org/team.yaml", Mirrors: []string{"https://flag.example/t.yaml"}}}, nil)
		for _, g := range won {
			if g.URL == "https://example.org/team.yaml" {
				Expect(g.Mirrors).To(Equal([]string{"https://m2.example/team.yaml"}), "the source's changed mirrors win")
			}
		}
	})

	It("keeps the mirrors of a flag gallery when mapping it", func() {
		store := open(GinkgoT().TempDir())
		added, err := MapGalleries(store, catalog.KindModel, []config.Gallery{{Name: "Mine", URL: "https://mine.example/i.yaml", Mirrors: []string{"https://mirror.example/i.yaml"}}})
		Expect(err).ToNot(HaveOccurred())
		Expect(added).To(HaveLen(1))
		e, _ := byID(store, added[0].ID)
		Expect(e.Mirrors).To(Equal([]string{"https://mirror.example/i.yaml"}))
		found := false
		for _, in := range List(store) {
			if in.ID == e.ID {
				found = len(in.Mirrors) == len(e.Mirrors)
				if found {
					Expect(in.Mirrors).To(Equal(e.Mirrors))
				}
			}
		}
		Expect(found).To(BeTrue(), "the info lists the mirrors")
	})

	It("refuses an add-on library on this machine over plain HTTP when added", func() {
		admin := LocalAdmin{Store: func() (*catalog.Sources, error) { return open(GinkgoT().TempDir()), nil }}
		ctx := context.Background()
		for _, url := range []string{"http://127.0.0.1:9000/index.yaml", "http://localhost:9000/index.yaml", "http://[::1]:9000/index.yaml"} {
			_, err := admin.Add(ctx, AddRequest{Kind: "addon", URL: url})
			var bad *BadRequestError
			Expect(errors.As(err, &bad)).To(BeTrue(), "%s: err = %v, want a bad request that asks for https", url, err)
			Expect(bad.Message).To(ContainSubstring("https"), url)
		}
		// A remote library over https, and a gallery on this machine, stay allowed.
		for _, req := range []AddRequest{{Kind: "addon", URL: "https://example.com/index.yaml"}, {Kind: "model", URL: "http://127.0.0.1:9000/index.yaml"}} {
			_, err := admin.Add(ctx, req)
			Expect(err).ToNot(HaveOccurred(), "%+v", req)
		}
	})
})
