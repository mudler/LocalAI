package gallery

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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

// loadRealLibrary reads the library file, the source of the models, the way
// the host reads a library.
func loadRealLibrary(t *testing.T) []ModelLibraryEntry {
	t.Helper()
	src, err := os.ReadFile(realModelLibrary)
	if err != nil {
		t.Fatal(err)
	}
	res, err := DecodeModelIndex(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) == 0 {
		t.Fatal("the real model library has no entries")
	}
	return res.Entries
}

// sameModelEntry compares two library entries apart from the generator-owned
// header fields the gallery has no place for: the version, which is derived
// from the content, and the channel.
func sameModelEntry(t *testing.T, a, b ModelLibraryEntry) bool {
	t.Helper()
	a.Version, b.Version = "", ""
	a.Channel, b.Channel = "", ""
	x, err := EncodeModelIndex([]ModelLibraryEntry{a})
	if err != nil {
		t.Fatal(err)
	}
	y, err := EncodeModelIndex([]ModelLibraryEntry{b})
	if err != nil {
		t.Fatal(err)
	}
	return string(x) == string(y)
}

// Every entry of the real library must survive the gallery copy unchanged:
// library -> gallery -> library is the same entry, and gallery -> library ->
// gallery is the same gallery entry. The count is logged so a run shows how
// many real entries it covered.
func TestModelLibraryRoundTripsEveryRealEntry(t *testing.T) {
	entries := loadRealLibrary(t)
	failed := 0
	for _, e := range entries {
		m, err := e.ToGallery()
		if err != nil {
			failed++
			t.Errorf("%s: %v", e.ID, err)
			continue
		}
		if err := VerifyModelRoundTrip(m); err != nil {
			failed++
			t.Errorf("%s: %v", e.ID, err)
			continue
		}
		back, err := ModelLibraryEntryFromGallery(m)
		if err != nil {
			failed++
			t.Errorf("%s: %v", e.ID, err)
			continue
		}
		if !sameModelEntry(t, e, back) {
			failed++
			t.Errorf("%s: library -> gallery -> library changed the entry", e.ID)
		}
	}
	t.Logf("round trip: %d of %d real library entries are equal after library -> gallery -> library", len(entries)-failed, len(entries))
}

// The gallery generated from the library is read by the gallery reader, and
// the reader sees the same entries: the names, the file uris and the digests.
func TestGeneratedGalleryLoadsWithTheSameEntries(t *testing.T) {
	entries := loadRealLibrary(t)
	models, err := ModelsFromLibrary(entries)
	if err != nil {
		t.Fatal(err)
	}
	flat := make([]GalleryModel, len(models))
	for i, m := range models {
		flat[i] = *m
	}
	body, err := EncodeGalleryIndex(flat)
	if err != nil {
		t.Fatal(err)
	}
	// The host reads the gallery file into a list of pointers.
	var loaded []*GalleryModel
	if err := yaml.Unmarshal(body, &loaded); err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(entries) {
		t.Fatalf("the gallery has %d entries, the library %d", len(loaded), len(entries))
	}
	files := 0
	for i, e := range entries {
		g := loaded[i]
		if g.Name != e.ID {
			t.Fatalf("entry %d is %q in the gallery and %q in the library", i, g.Name, e.ID)
		}
		if len(g.AdditionalFiles) != len(e.Model.Files) {
			t.Errorf("%s: %d files in the gallery, %d in the library", e.ID, len(g.AdditionalFiles), len(e.Model.Files))
			continue
		}
		for j, f := range e.Model.Files {
			files++
			if g.AdditionalFiles[j].URI != f.URI || g.AdditionalFiles[j].SHA256 != f.SHA256 || g.AdditionalFiles[j].Filename != f.Filename {
				t.Errorf("%s: file %d differs: %+v and %+v", e.ID, j, g.AdditionalFiles[j], f)
			}
		}
		if !reflect.DeepEqual(*g, *models[i]) {
			t.Errorf("%s: the gallery reader sees a different entry than the library gives", e.ID)
		}
		gd, _ := galleryEntryDigest(*g)
		wd, _ := galleryEntryDigest(*models[i])
		if gd != wd {
			t.Errorf("%s: gallery digest %s, library digest %s", e.ID, gd[:12], wd[:12])
		}
	}
	t.Logf("generated gallery: %d entries and %d files read back equal to the library", len(loaded), files)
}

// gallery/index.yaml is generated and never edited. The CI check regenerates it
// and fails on a diff; this test does the same in the Go suite, so a hand edit
// is found by go test too.
func TestCheckedInGalleryIsTheGeneratedCopy(t *testing.T) {
	models, err := ModelsFromLibrary(loadRealLibrary(t))
	if err != nil {
		t.Fatal(err)
	}
	flat := make([]GalleryModel, len(models))
	for i, m := range models {
		flat[i] = *m
	}
	want, err := EncodeGalleryIndex(flat)
	if err != nil {
		t.Fatal(err)
	}
	have, err := os.ReadFile(realGalleryIndex)
	if err != nil {
		t.Fatal(err)
	}
	// The generator puts a comment header in front of the body.
	body := string(have)
	for strings.HasPrefix(body, "#") {
		_, rest, _ := strings.Cut(body, "\n")
		body = rest
	}
	if body != string(want) {
		t.Error("gallery/index.yaml is not the copy generated from gallery/models/index.yaml. It is generated; do not edit it by hand. Regenerate: go run ./scripts/models/cmd/modelcatalog --library gallery/models/index.yaml --out gallery/index.yaml")
	}
}

// An entry whose library digest is in the golden file must still produce the
// recorded gallery entry. An entry changed or added since the golden file was
// rendered has no recorded digest and is covered by the round trip only.
func TestModelLibraryMatchesGoldenDigests(t *testing.T) {
	f, err := os.Open(filepath.Join(modelLibraryGolds, "digests.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	type pair struct{ library, gallery string }
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
		t.Fatalf("the golden file lists %d entries; it should list the real library", len(golden))
	}

	checked, unchanged := 0, 0
	for _, e := range loadRealLibrary(t) {
		want, ok := golden[e.ID]
		if !ok {
			continue
		}
		l, g, err := ModelLibraryDigests(e)
		if err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
		checked++
		if l != want.library {
			continue // the entry changed after the golden file was rendered
		}
		unchanged++
		if g != want.gallery {
			t.Errorf("%s: the generated gallery entry changed (digest %s, golden %s); render the golden files again only if the change is intended", e.ID, g, want.gallery)
		}
	}
	t.Logf("golden digests: %d of %d golden entries are in the library, %d unchanged and compared", checked, len(golden), unchanged)
	// Most entries are stable between renders. If almost none match, the digest
	// of the library side has changed meaning and the check proves nothing.
	if unchanged*2 < checked {
		t.Errorf("only %d of %d golden entries kept their library digest; render the golden files again", unchanged, checked)
	}
}

// The readable samples are written both ways: the library entry as it stands
// in the library and the gallery entry generated from it.
func TestModelLibraryMatchesGoldenSamples(t *testing.T) {
	libraries, err := filepath.Glob(filepath.Join(modelLibraryGolds, "samples", "*.library.yaml"))
	if err != nil || len(libraries) == 0 {
		t.Fatalf("no golden samples: %v", err)
	}
	for _, lpath := range libraries {
		name := strings.TrimSuffix(filepath.Base(lpath), ".library.yaml")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(lpath)
			if err != nil {
				t.Fatal(err)
			}
			galWant, err := os.ReadFile(strings.TrimSuffix(lpath, ".library.yaml") + ".gallery.yaml")
			if err != nil {
				t.Fatal(err)
			}
			res, err := DecodeModelIndex(src)
			if err != nil || len(res.Entries) != 1 {
				t.Fatalf("reading the sample: %v (%d entries)", err, len(res.Entries))
			}
			// The library sample is what a library file holds.
			again, err := EncodeModelIndex(res.Entries)
			if err != nil || string(again) != string(src) {
				t.Errorf("the library sample does not encode back to itself: %v", err)
			}
			m, err := res.Entries[0].ToGallery()
			if err != nil {
				t.Fatal(err)
			}
			got, err := EncodeGalleryIndex([]GalleryModel{m})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(galWant) {
				t.Errorf("the gallery entry differs from the golden file:\n%s", got)
			}

			var loaded []GalleryModel
			if err := yaml.Unmarshal(galWant, &loaded); err != nil || len(loaded) != 1 {
				t.Fatalf("the gallery reader on the golden entry: %v", err)
			}
			if d := firstDifference(stripRuntime(m), loaded[0]); d != "unknown field" {
				t.Errorf("the golden gallery entry does not load as the library entry: %s", d)
			}
			if err := VerifyModelRoundTrip(loaded[0]); err != nil {
				t.Error(err)
			}
		})
	}
}

// The library file in the repository is the source and is read by the host,
// so it must decode, and every entry must have the library shape: it converts
// to a gallery entry and comes back as itself. Edited entries keep their own
// version; only the content is compared.
func TestRealModelLibraryHasTheLibraryShape(t *testing.T) {
	src, err := os.ReadFile(realModelLibrary)
	if err != nil {
		t.Fatal(err)
	}
	res, err := DecodeModelIndex(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 0 {
		t.Errorf("the model library skipped %d entries", len(res.Skipped))
	}
	for _, e := range res.Entries {
		if e.Name != e.ID {
			t.Errorf("%s: the name %q differs from the id; the gallery copy cannot carry it", e.ID, e.Name)
		}
		m, err := e.ToGallery()
		if err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
		again, err := ModelLibraryEntryFromGallery(m)
		if err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
		if !sameModelEntry(t, e, again) {
			t.Errorf("%s: the entry changes on the way through the gallery copy; it has a field the gallery cannot carry", e.ID)
		}
	}
	t.Logf("model library: %d entries have the library shape", len(res.Entries))
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

// A gallery marked as a library is read as a library; any other gallery is read as an index.
func TestLibraryGalleryReadsTheLibrary(t *testing.T) {
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

	// Library: a gallery marked as a library is read as one.
	official.Library = true
	official.URL = srv.URL + "/models/index.yaml"
	models, err := AvailableGalleryModels([]config.Gallery{official}, systemState)
	if err != nil {
		t.Fatal(err)
	}
	if got := galleryModelNames(models); len(got) != 1 || got[0] != "m1" {
		t.Errorf("library source gave %v", got)
	}
	if models[0].GetGallery().Name != OfficialModelGalleryName || models[0].GetGallery().URL != official.URL || !models[0].GetGallery().Library {
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
	official.URL = srv.URL + "/missing.yaml"
	if _, err := AvailableGalleryModels([]config.Gallery{official}, systemState); err == nil {
		t.Error("a missing library fell back to the gallery")
	}
}
