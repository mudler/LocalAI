package library

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
)

func open(t *testing.T, dir string) *catalog.Sources {
	t.Helper()
	s, err := Open(dir, true, false)
	if err != nil {
		t.Fatal(err)
	}
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

func TestDefaultsFlagsAreUnchanged(t *testing.T) {
	store := open(t, t.TempDir())
	models, backends := parse(config.DefaultGalleriesJSON), parse(config.DefaultBackendGalleriesJSON)
	for kind, in := range map[catalog.Kind][]config.Gallery{catalog.KindModel: models, catalog.KindBackend: backends} {
		added, err := MapGalleries(store, kind, in)
		if err != nil || len(added) != 0 {
			t.Errorf("%s: the shipped gallery was added as a user source: %v, %v", kind, added, err)
		}
		// The same JSON loads, mirrors and policy included.
		if got := ApplyGalleries(store, kind, in); !reflect.DeepEqual(got, in) {
			t.Errorf("%s: galleries changed:\n got %+v\nwant %+v", kind, got, in)
		}
	}
}

func TestEachFlagEntryBecomesAUserSourceOfItsKind(t *testing.T) {
	store := open(t, t.TempDir())
	models := append(parse(config.DefaultGalleriesJSON), config.Gallery{Name: "Team Models", URL: "https://example.org/models.yaml"})
	backends := append(parse(config.DefaultBackendGalleriesJSON), config.Gallery{Name: "Team Backends", URL: "https://example.org/backends.yaml"})
	if _, err := MapGalleries(store, catalog.KindModel, models); err != nil {
		t.Fatal(err)
	}
	if _, err := MapGalleries(store, catalog.KindBackend, backends); err != nil {
		t.Fatal(err)
	}
	m, ok := byID(store, "team-models")
	if !ok || m.Kind != catalog.KindModel || m.Default || !m.Enabled || m.Format != catalog.FormatGallery || m.URL != "https://example.org/models.yaml" || m.Name != "Team Models" {
		t.Errorf("model source = %+v, %v", m, ok)
	}
	b, ok := byID(store, "team-backends")
	if !ok || b.Kind != catalog.KindBackend || b.Default || !b.Enabled {
		t.Errorf("backend source = %+v, %v", b, ok)
	}
	// The flags still decide the list the server reads.
	if got := ApplyGalleries(store, catalog.KindModel, models); !reflect.DeepEqual(got, models) {
		t.Errorf("model galleries = %+v", got)
	}
}

func TestMappingIsAdditiveAndNeverTwice(t *testing.T) {
	dir := t.TempDir()
	flag := []config.Gallery{{Name: "mine", URL: "https://example.org/a.yaml"}}
	for run := 0; run < 3; run++ {
		store := open(t, dir)
		added, err := MapGalleries(store, catalog.KindModel, flag)
		if err != nil {
			t.Fatal(err)
		}
		if (run == 0) != (len(added) == 1) {
			t.Fatalf("start %d added %d sources", run, len(added))
		}
	}
	n := 0
	for _, e := range open(t, dir).Entries() {
		if e.URL == flag[0].URL {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the entry is listed %d times", n)
	}
}

// A source the user added with the CLI is not added a second time when a flag
// names the same index.
func TestEntryAlreadyAUserSourceIsNotAddedAgain(t *testing.T) {
	store := open(t, t.TempDir())
	if _, err := Add(store, catalog.KindModel, "https://example.org/a.yaml", "chosen", "Chosen", ""); err != nil {
		t.Fatal(err)
	}
	added, err := MapGalleries(store, catalog.KindModel, []config.Gallery{{Name: "other-name", URL: "https://example.org/a.yaml"}})
	if err != nil || len(added) != 0 {
		t.Fatalf("added %v, %v", added, err)
	}
	if _, ok := byID(store, "other-name"); ok {
		t.Error("a second record was made for the same index")
	}
}

func TestMappedIDsNeverCollide(t *testing.T) {
	store := open(t, t.TempDir())
	// "localai" is the id of the shipped model gallery; a different index with
	// the same name gets its own id.
	added, err := MapGalleries(store, catalog.KindModel, []config.Gallery{{Name: "localai", URL: "https://example.org/other.yaml"}})
	if err != nil || len(added) != 1 || added[0].ID != "localai-2" {
		t.Fatalf("added %+v, %v", added, err)
	}
	if e, _ := byID(store, "localai"); !e.Default || e.URL == "https://example.org/other.yaml" {
		t.Errorf("the default was changed: %+v", e)
	}
}

func TestRemovedOrDisabledSourcesLeaveTheGalleryList(t *testing.T) {
	dir := t.TempDir()
	store := open(t, dir)
	models := append(parse(config.DefaultGalleriesJSON), config.Gallery{Name: "mine", URL: "https://example.org/a.yaml"})
	if _, err := MapGalleries(store, catalog.KindModel, models); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveSource(DefaultModelSourceID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnabled("mine", false); err != nil {
		t.Fatal(err)
	}
	if got := ApplyGalleries(store, catalog.KindModel, models); len(got) != 0 {
		t.Errorf("galleries = %+v", got)
	}
	// The removal holds at the next start, with the same flags.
	o := config.NewApplicationConfig(config.WithStringGalleries(config.DefaultGalleriesJSON))
	Option(dir, true, false)(o)
	if len(o.Galleries) != 0 {
		t.Errorf("the removed default came back: %+v", o.Galleries)
	}
	e, _ := byID(open(t, dir), DefaultModelSourceID)
	if !e.IsTombstone() {
		t.Errorf("record = %+v", e)
	}
}

func TestSourcesTheUserAddedReachTheServer(t *testing.T) {
	store := open(t, t.TempDir())
	if _, err := Add(store, catalog.KindModel, "https://example.org/lib.yaml", "", "Model library", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(store, catalog.KindBackend, "https://example.org/bk.yaml", "bk", "", catalog.FormatGallery); err != nil {
		t.Fatal(err)
	}
	// A library-format backend source is read by the add-on catalog, not as a gallery.
	if _, err := Add(store, catalog.KindBackend, "https://example.org/bklib.yaml", "bklib", "", ""); err != nil {
		t.Fatal(err)
	}
	models := ApplyGalleries(store, catalog.KindModel, parse(config.DefaultGalleriesJSON))
	if len(models) != 2 || !models[1].Library || models[1].URL != "https://example.org/lib.yaml" || models[1].Name != "Model library" {
		t.Errorf("models = %+v", models)
	}
	backends := ApplyGalleries(store, catalog.KindBackend, parse(config.DefaultBackendGalleriesJSON))
	if len(backends) != 2 || backends[1].Library || backends[1].URL != "https://example.org/bk.yaml" {
		t.Errorf("backends = %+v", backends)
	}
}

// The startup option, run the way the server runs it, with the default flags.
func TestOptionKeepsDefaultDeploymentsAsTheyWere(t *testing.T) {
	dir := t.TempDir()
	build := func() *config.ApplicationConfig {
		o := config.NewApplicationConfig(
			config.WithStringGalleries(config.DefaultGalleriesJSON),
			config.WithBackendGalleries(config.DefaultBackendGalleriesJSON),
		)
		Option(dir, true, false)(o)
		return o
	}
	want := config.NewApplicationConfig(
		config.WithStringGalleries(config.DefaultGalleriesJSON),
		config.WithBackendGalleries(config.DefaultBackendGalleriesJSON),
	)
	for i := 0; i < 2; i++ {
		o := build()
		if !reflect.DeepEqual(o.Galleries, want.Galleries) || !reflect.DeepEqual(o.BackendGalleries, want.BackendGalleries) {
			t.Fatalf("start %d changed the galleries:\n%+v\n%+v", i, o.Galleries, o.BackendGalleries)
		}
	}
	// The shipped defaults are listed after the first start.
	var ids []string
	for _, e := range open(t, dir).Entries() {
		ids = append(ids, e.ID)
	}
	if got := strings.Join(ids, ","); got != "official,localai-backends,localai" {
		t.Errorf("sources = %s", got)
	}
}

// A data path the process cannot write must not stop the server.
func TestOptionSurvivesAnUnusableStore(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	o := config.NewApplicationConfig(config.WithStringGalleries(config.DefaultGalleriesJSON))
	before := append([]config.Gallery(nil), o.Galleries...)
	Option(filepath.Join(file, "sub"), true, false)(o)
	if !reflect.DeepEqual(o.Galleries, before) {
		t.Errorf("galleries changed: %+v", o.Galleries)
	}
}

// A custom --galleries / --backend-galleries JSON still loads as it is, the
// autoload switches are untouched, and each entry is listed as a source.
func TestCustomFlagsLoadAsBefore(t *testing.T) {
	dir := t.TempDir()
	models := `[{"name":"a","url":"https://example.org/a.yaml"},{"name":"b","url":"https://example.org/b.yaml","mirrors":["https://mirror.example.org/b.yaml"]}]`
	backends := `[{"name":"c","url":"https://example.org/c.yaml"}]`
	o := config.NewApplicationConfig(
		config.WithStringGalleries(models),
		config.WithBackendGalleries(backends),
		config.EnableGalleriesAutoload,
		config.EnableBackendGalleriesAutoload,
	)
	Option(dir, true, false)(o)
	if !reflect.DeepEqual(o.Galleries, parse(models)) || !reflect.DeepEqual(o.BackendGalleries, parse(backends)) {
		t.Errorf("galleries changed: %+v %+v", o.Galleries, o.BackendGalleries)
	}
	if !o.AutoloadGalleries || !o.AutoloadBackendGalleries {
		t.Error("an autoload switch changed")
	}
	store := open(t, dir)
	for id, kind := range map[string]catalog.Kind{"a": catalog.KindModel, "b": catalog.KindModel, "c": catalog.KindBackend} {
		if e, ok := byID(store, id); !ok || e.Kind != kind || e.Default {
			t.Errorf("source %s = %+v, %v", id, e, ok)
		}
	}
}
