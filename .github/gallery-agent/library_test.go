package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/gallery"
)

const libraryFixture = `# header comment
- kind: model
  id: existing-model
  name: existing-model
  version: 0.0.0-gallery.000000000000
  description: |
    An existing model.
  urls:
    - https://huggingface.co/acme/existing-GGUF
  channel: gallery
  schemaVersion: 1
  model:
    url: github:mudler/LocalAI/gallery/virtual.yaml@master
`

// agentEntry has the shape generateYAMLEntry writes.
const agentEntry = `- name: "new-model"
  url: "github:mudler/LocalAI/gallery/virtual.yaml@master"
  urls:
    - https://huggingface.co/acme/new-model-GGUF
  description: |
    A new model.
  license: "apache-2.0"
  tags:
    - llm
    - gguf
  overrides:
    parameters:
      model: new-model-Q4_K_M.gguf
  files:
    - filename: new-model-Q4_K_M.gguf
      sha256: abc
      uri: huggingface://acme/new-model-GGUF/new-model-Q4_K_M.gguf
`

func writeFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "index.yaml")
	if err := os.WriteFile(p, []byte(libraryFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAppendLibraryEntriesWritesTheLibraryShape(t *testing.T) {
	p := writeFixture(t)
	added, skipped, err := appendLibraryEntries(p, []string{agentEntry})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0] != "new-model" || len(skipped) != 0 {
		t.Fatalf("added %v skipped %v", added, skipped)
	}

	got, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(got), libraryFixture) {
		t.Fatal("existing content was changed; entries must only be appended")
	}
	res, err := gallery.DecodeModelIndex(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(res.Entries))
	}
	e := res.Entries[1]
	if e.ID != "new-model" || e.Kind != "model" {
		t.Fatalf("unexpected entry %+v", e.Envelope)
	}
	if e.Model.URL == "" || len(e.Model.Files) != 1 || e.Model.Overrides == nil {
		t.Fatalf("model body not filled: %+v", e.Model)
	}
	if len(e.URLs) != 1 || e.URLs[0] != "https://huggingface.co/acme/new-model-GGUF" {
		t.Fatalf("urls: %v", e.URLs)
	}
}

func TestAppendLibraryEntriesSkipsAnExistingID(t *testing.T) {
	p := writeFixture(t)
	dup := strings.Replace(agentEntry, `"new-model"`, `"existing-model"`, 1)
	added, skipped, err := appendLibraryEntries(p, []string{dup})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 || len(skipped) != 1 {
		t.Fatalf("added %v skipped %v", added, skipped)
	}
	got, _ := os.ReadFile(p)
	if string(got) != libraryFixture {
		t.Fatal("file changed although nothing was added")
	}
}

func TestDuplicateCheckReadsTheLibrary(t *testing.T) {
	p := writeFixture(t)
	if _, _, err := appendLibraryEntries(p, []string{agentEntry}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GALLERY_INDEX_PATH", p)
	old := galleryIndexPath
	galleryIndexPath = p
	defer func() { galleryIndexPath = old }()

	set, err := loadGalleryURLSet()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"acme/existing-GGUF", "acme/new-model-GGUF"} {
		if !modelAlreadyInGallery(set, id) {
			t.Fatalf("%s should count as already in the library", id)
		}
	}
}
