package gallery

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/system"
	"gopkg.in/yaml.v3"
)

const (
	realGalleryIndex  = "../../gallery/index.yaml"
	realModelLibrary  = "../../gallery/models/index.yaml"
	modelLibraryGolds = "testdata/model_library"
)

func loadRealGallery(t *testing.T) []GalleryModel {
	t.Helper()
	src, err := os.ReadFile(realGalleryIndex)
	if err != nil {
		t.Fatal(err)
	}
	// The host reads the index into a list of pointers; the shape is the same.
	var entries []GalleryModel
	if err := yaml.Unmarshal(src, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("the real gallery index has no entries")
	}
	return entries
}

// Every entry of the real index must survive the library unchanged. The count
// is logged so a run shows how many real entries it covered.
func TestModelLibraryRoundTripsEveryRealEntry(t *testing.T) {
	entries := loadRealGallery(t)
	failed := 0
	for _, m := range entries {
		if err := VerifyModelRoundTrip(m); err != nil {
			failed++
			t.Errorf("%s: %v", m.Name, err)
		}
	}
	t.Logf("round trip: %d of %d real model entries are equal after gallery -> library -> gallery", len(entries)-failed, len(entries))
}

// An entry whose gallery digest is in the golden file must still produce the
// recorded library entry. An entry changed or added since the golden file was
// rendered has no recorded digest and is covered by the round trip only.
func TestModelLibraryMatchesGoldenDigests(t *testing.T) {
	f, err := os.Open(filepath.Join(modelLibraryGolds, "digests.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	type pair struct{ gallery, library string }
	golden := map[string]pair{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			t.Fatalf("malformed golden line %q", line)
		}
		golden[parts[0]] = pair{parts[1], parts[2]}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(golden) < 2000 {
		t.Fatalf("the golden file lists %d entries; it should list the real index", len(golden))
	}

	checked, unchanged := 0, 0
	for _, m := range loadRealGallery(t) {
		want, ok := golden[m.Name]
		if !ok {
			continue
		}
		g, l, err := ModelLibraryDigests(m)
		if err != nil {
			t.Fatalf("%s: %v", m.Name, err)
		}
		checked++
		if g != want.gallery {
			continue // the entry changed after the golden file was rendered
		}
		unchanged++
		if l != want.library {
			t.Errorf("%s: the library entry changed (digest %s, golden %s); render the golden files again only if the change is intended", m.Name, l, want.library)
		}
	}
	t.Logf("golden digests: %d of %d golden entries are in the index, %d unchanged and compared", checked, len(golden), unchanged)
	// Most entries are stable between renders. If almost none match, the digest
	// of the gallery side has changed meaning and the check proves nothing.
	if unchanged*2 < checked {
		t.Errorf("only %d of %d golden entries kept their gallery digest; render the golden files again", unchanged, checked)
	}
}

// The readable samples are written both ways: the gallery entry as it stands
// in the index and the library entry made from it.
func TestModelLibraryMatchesGoldenSamples(t *testing.T) {
	galleries, err := filepath.Glob(filepath.Join(modelLibraryGolds, "samples", "*.gallery.yaml"))
	if err != nil || len(galleries) == 0 {
		t.Fatalf("no golden samples: %v", err)
	}
	for _, gpath := range galleries {
		name := strings.TrimSuffix(filepath.Base(gpath), ".gallery.yaml")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(gpath)
			if err != nil {
				t.Fatal(err)
			}
			var entries []GalleryModel
			if err := yaml.Unmarshal(src, &entries); err != nil || len(entries) != 1 {
				t.Fatalf("reading the sample: %v (%d entries)", err, len(entries))
			}
			libWant, err := os.ReadFile(strings.TrimSuffix(gpath, ".gallery.yaml") + ".library.yaml")
			if err != nil {
				t.Fatal(err)
			}

			entry, err := ModelLibraryEntryFromGallery(entries[0])
			if err != nil {
				t.Fatal(err)
			}
			got, err := EncodeModelIndex([]ModelLibraryEntry{entry})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(libWant) {
				t.Errorf("the library entry differs from the golden file:\n%s", got)
			}

			res, err := DecodeModelIndex(libWant)
			if err != nil || len(res.Entries) != 1 {
				t.Fatalf("reading the golden library entry: %v", err)
			}
			back, err := res.Entries[0].ToGallery()
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyModelRoundTrip(back); err != nil {
				t.Error(err)
			}
			if d := firstDifference(stripRuntime(entries[0]), back); d != "unknown field" {
				t.Errorf("the golden library entry does not give back the gallery entry: %s", d)
			}
		})
	}
}

// The rendered file in the repository is read by the host, so it must decode,
// and every entry must agree with itself. It may be older than the gallery
// index; the render command brings it up to date.
func TestRenderedModelLibraryIsConsistent(t *testing.T) {
	src, err := os.ReadFile(realModelLibrary)
	if err != nil {
		t.Fatal(err)
	}
	res, err := DecodeModelIndex(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 0 {
		t.Errorf("the rendered library skipped %d entries", len(res.Skipped))
	}
	for _, e := range res.Entries {
		m, err := e.ToGallery()
		if err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
		again, err := ModelLibraryEntryFromGallery(m)
		if err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
		a, _ := EncodeModelIndex([]ModelLibraryEntry{e})
		b, _ := EncodeModelIndex([]ModelLibraryEntry{again})
		if string(a) != string(b) {
			t.Errorf("%s: the rendered entry is not what its own gallery entry renders to; it was edited by hand or is from another shape", e.ID)
		}
	}
	t.Logf("rendered model library: %d entries", len(res.Entries))
}

func libraryEntry(extra string) string {
	return "- kind: model\n  id: m1\n  name: m1\n  version: 0.0.0-test\n  policy:\n    requireSignature: false\n  schemaVersion: 1\n" + extra
}

func TestDecodeModelIndexIsStrict(t *testing.T) {
	cases := map[string]string{
		"unknown header field": libraryEntry("  surprise: 1\n"),
		"unknown body field":   libraryEntry("  model:\n    surprise: 1\n"),
		"duplicate key":        libraryEntry("  tags: [a]\n  tags: [b]\n"),
		"duplicate id":         libraryEntry("") + libraryEntry(""),
		"future schema":        strings.Replace(libraryEntry(""), "schemaVersion: 1", "schemaVersion: 2", 1),
		"no schema":            strings.Replace(libraryEntry(""), "schemaVersion: 1", "schemaVersion: 0", 1),
		"empty id":             strings.Replace(libraryEntry(""), "id: m1", "id: \"\"", 1),
		"malformed kind":       "- kind: 5\n  id: x\n",
		"bad kind name":        "- kind: Model!\n  id: x\n",
		"flow list":            "[{kind: model, id: m1}]\n",
		"two documents":        libraryEntry("") + "---\n" + libraryEntry(""),
		"not a list":           "kind: model\n",
		"entry not a mapping":  "- 3\n",
		"wrong type":           libraryEntry("  model:\n    files: yes\n"),
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeModelIndex([]byte(doc)); err == nil {
				t.Errorf("the reader accepted %q", doc)
			}
		})
	}
	t.Run("too large", func(t *testing.T) {
		if _, err := DecodeModelIndex(make([]byte, MaxModelIndexBytes+1)); err == nil {
			t.Error("the reader accepted an oversized document")
		}
	})
}

func TestDecodeModelIndexSkipsOtherKinds(t *testing.T) {
	doc := "- kind: backend\n  id: b1\n  anything: goes\n" +
		"- id: legacy-addon\n  addonId: legacy-addon\n" +
		"- kind: future-kind\n  id: f1\n" +
		libraryEntry("")
	res, err := DecodeModelIndex([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].ID != "m1" {
		t.Errorf("entries = %+v", res.Entries)
	}
	var kinds []string
	for _, s := range res.Skipped {
		kinds = append(kinds, s.Kind+":"+s.ID)
	}
	want := "backend:b1 addon:legacy-addon future-kind:f1"
	if strings.Join(kinds, " ") != want {
		t.Errorf("skipped = %v, want %s", kinds, want)
	}
}

func TestModelLibraryKeepsAnAddonReaderWorking(t *testing.T) {
	// The add-on reader skips model entries, so one file can later hold both.
	doc, err := EncodeModelIndex([]ModelLibraryEntry{{
		Envelope: catalog.Envelope{Kind: catalog.KindModel, ID: "m1", Name: "m1", Version: "1", SchemaVersion: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := catalog.ReadIndex(doc, catalog.ReadableKinds...)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Releases) != 0 || len(res.Skipped) != 1 || res.Skipped[0].Kind != "model" {
		t.Errorf("the add-on reader made %+v", res)
	}
}

func TestPromptTemplatesAreLiftedAndRestored(t *testing.T) {
	m := GalleryModel{
		Metadata: Metadata{Name: "templated"},
		ConfigFile: map[string]any{
			"backend": "llama",
			"prompt_templates": []any{
				map[string]any{"name": "chat", "content": "{{.Input}}"},
			},
		},
	}
	e, err := ModelLibraryEntryFromGallery(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Model.PromptTemplates) != 1 || e.Model.PromptTemplates[0].Name != "chat" {
		t.Errorf("promptTemplates = %+v", e.Model.PromptTemplates)
	}
	if _, still := e.Model.ConfigFile["prompt_templates"]; still {
		t.Error("prompt_templates was left in configFile")
	}
	if err := VerifyModelRoundTrip(m); err != nil {
		t.Error(err)
	}

	// Only templates are in the config: nothing is left behind and it still
	// round trips.
	only := GalleryModel{
		Metadata:   Metadata{Name: "only"},
		ConfigFile: map[string]any{"prompt_templates": []any{map[string]any{"name": "a", "content": "b"}}},
	}
	if err := VerifyModelRoundTrip(only); err != nil {
		t.Error(err)
	}

	// A template with another key is not reshaped.
	odd := GalleryModel{
		Metadata:   Metadata{Name: "odd"},
		ConfigFile: map[string]any{"prompt_templates": []any{map[string]any{"name": "a", "content": "b", "extra": 1}}},
	}
	oe, _ := ModelLibraryEntryFromGallery(odd)
	if len(oe.Model.PromptTemplates) != 0 {
		t.Error("a template with an extra key was lifted")
	}
	if err := VerifyModelRoundTrip(odd); err != nil {
		t.Error(err)
	}
}

func TestRoundTripKeepsNumberTypesAndEmptyLists(t *testing.T) {
	m := GalleryModel{
		Metadata: Metadata{Name: "numbers", Tags: []string{"1.0", "yes", "null"}},
		Overrides: map[string]any{
			"temperature": 1.0,
			"top_k":       20,
			"min_p":       0.0,
			"big":         1e21,
			"nested":      map[string]any{"list": []any{1.0, 2, "3"}},
			"flag":        true,
			"nothing":     nil,
		},
		Variants: []Variant{},
	}
	if err := VerifyModelRoundTrip(m); err != nil {
		t.Error(err)
	}
	if err := VerifyModelRoundTrip(GalleryModel{}); err == nil {
		t.Error("an entry without a name was accepted")
	}
}

func TestRuntimeFieldsAreNotPartOfTheEntry(t *testing.T) {
	m := GalleryModel{Metadata: Metadata{Name: "runtime", Installed: true, Gallery: config.Gallery{Name: "x", URL: "y"}}}
	e, err := ModelLibraryEntryFromGallery(m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := EncodeModelIndex([]ModelLibraryEntry{e})
	if strings.Contains(string(raw), "installed") || strings.Contains(string(raw), "gallery") && strings.Contains(string(raw), "url: y") {
		t.Errorf("runtime fields reached the library:\n%s", raw)
	}
	if err := VerifyModelRoundTrip(m); err != nil {
		t.Error(err)
	}
}

func TestParseModelIndexSource(t *testing.T) {
	for in, want := range map[string]ModelIndexSource{"": ModelIndexGallery, "gallery": ModelIndexGallery, " Library ": ModelIndexLibrary} {
		got, err := ParseModelIndexSource(in)
		if err != nil || got != want {
			t.Errorf("ParseModelIndexSource(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseModelIndexSource("other"); err == nil {
		t.Error("an unknown source was accepted")
	}
}

// The setting decides which file is read, and only for the official gallery.
func TestModelIndexSourceSelectsTheFile(t *testing.T) {
	library := libraryEntry("  model:\n    files:\n      - filename: m1.gguf\n        sha256: abc\n        uri: https://example.invalid/m1.gguf\n    overrides:\n      backend: llama-cpp\n")
	var libraryHits, galleryHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models/index.yaml":
			libraryHits++
			_, _ = w.Write([]byte(library))
		case "/gallery/index.yaml":
			galleryHits++
			_, _ = w.Write([]byte("- name: from-gallery\n  overrides:\n    backend: llama-cpp\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	systemState, err := system.GetSystemState(system.WithModelPath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { SetModelIndex(ModelIndexGallery, "") })
	names := func(g config.Gallery) []string {
		t.Helper()
		models, err := AvailableGalleryModels([]config.Gallery{g}, systemState)
		if err != nil {
			t.Fatal(err)
		}
		return galleryModelNames(models)
	}
	official := config.Gallery{Name: OfficialModelGalleryName, URL: srv.URL + "/gallery/index.yaml"}

	// Default: the gallery index, and the library file is never requested.
	if got := names(official); len(got) != 1 || got[0] != "from-gallery" {
		t.Errorf("default source gave %v", got)
	}
	if libraryHits != 0 {
		t.Errorf("the library was read %d times with the default source", libraryHits)
	}

	// Library: the official gallery reads the library instead.
	SetModelIndex(ModelIndexLibrary, srv.URL+"/models/index.yaml")
	models, err := AvailableGalleryModels([]config.Gallery{official}, systemState)
	if err != nil {
		t.Fatal(err)
	}
	if got := galleryModelNames(models); len(got) != 1 || got[0] != "m1" {
		t.Errorf("library source gave %v", got)
	}
	if models[0].GetGallery().Name != OfficialModelGalleryName || models[0].GetGallery().URL != official.URL {
		t.Errorf("the entry should carry the configured gallery, got %+v", models[0].GetGallery())
	}
	if len(models[0].AdditionalFiles) != 1 || models[0].Overrides["backend"] != "llama-cpp" {
		t.Errorf("the entry lost its body: %+v", models[0])
	}

	// A gallery the owner added keeps its own index.
	other := config.Gallery{Name: "mine", URL: srv.URL + "/gallery/index.yaml"}
	if got := names(other); len(got) != 1 || got[0] != "from-gallery" {
		t.Errorf("another gallery gave %v", got)
	}

	// An unreachable library is an error, not a silent fall back.
	SetModelIndex(ModelIndexLibrary, srv.URL+"/missing.yaml")
	if _, err := AvailableGalleryModels([]config.Gallery{official}, systemState); err == nil {
		t.Error("a missing library fell back to the gallery")
	}
}
