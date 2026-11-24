// SPDX-License-Identifier: MIT
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/gallery"
	"gopkg.in/yaml.v3"
)

const miniLibrary = `- kind: model
  id: a
  name: a
  version: 0.0.0-test
  channel: gallery
  policy:
    requireSignature: false
  schemaVersion: 1
  model:
    url: github:example/virtual.yaml@master
    overrides:
      temperature: 1.0
    variants:
      - model: b
- kind: model
  id: b
  name: b
  version: 0.0.0-test
  policy:
    requireSignature: false
  schemaVersion: 1
  model:
    files:
      - filename: b.gguf
        sha256: abc
        uri: https://example.invalid/b.gguf
    variants: []
`

// The old gallery file, with the keys the library cannot carry.
const oldGallery = `- name: a
  last_checked: 2026-10-01
  parameters:
    x: 1
- name: b
  License: x
  last_checked: 2026-10-01
`

func TestRenderIsDeterministicAndKeepsTheEntries(t *testing.T) {
	library, err := load([]byte(miniLibrary))
	if err != nil {
		t.Fatal(err)
	}
	first, err := render(library)
	if err != nil {
		t.Fatal(err)
	}
	second, err := render(library)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := gallery.EncodeGalleryIndex(first)
	b, _ := gallery.EncodeGalleryIndex(second)
	if string(a) != string(b) || len(first) != 2 {
		t.Fatalf("render is not deterministic or lost entries: %d entries", len(first))
	}
	if !strings.Contains(string(a), "temperature: 1.0") {
		t.Errorf("the float lost its form:\n%s", a)
	}
	if !strings.Contains(string(a), "variants: []") {
		t.Errorf("the empty variants list was not kept:\n%s", a)
	}
	var loaded []gallery.GalleryModel
	if err := yaml.Unmarshal(a, &loaded); err != nil || len(loaded) != 2 || loaded[0].Name != "a" || loaded[1].Variants == nil {
		t.Errorf("the gallery reader did not load the output: %v %+v", err, loaded)
	}
}

func TestLoadRefusesALibraryThatIsNotOne(t *testing.T) {
	if _, err := load([]byte("- name: a\n  url: x\n")); err == nil {
		t.Error("a gallery shaped file was accepted as the library")
	}
}

func TestDroppedKeysAreListedOnEveryRun(t *testing.T) {
	dropped, err := droppedKeys([]byte(oldGallery))
	if err != nil {
		t.Fatal(err)
	}
	if dropped["last_checked"] != 2 || dropped["parameters"] != 1 || dropped["License"] != 1 || len(dropped) != 3 {
		t.Errorf("dropped = %v", dropped)
	}
	var lines []string
	w := func(f string, a ...any) { lines = append(lines, strings.TrimSpace(strings.NewReplacer("%s", "S").Replace(f))) }
	reportDropped(w, nil, nil)
	if len(lines) < 2 {
		t.Fatalf("a run with no previous file listed %d lines", len(lines))
	}
	var all []string
	w2 := func(f string, a ...any) {
		all = append(all, f)
		for _, x := range a {
			if s, ok := x.(string); ok {
				all = append(all, s)
			}
		}
	}
	reportDropped(w2, nil, []byte(oldGallery))
	joined := strings.Join(all, "\n")
	for _, k := range droppedGalleryKeys {
		if !strings.Contains(joined, k) {
			t.Errorf("the report does not name %q:\n%s", k, joined)
		}
	}
	if !strings.Contains(joined, "last_checked (2)") {
		t.Errorf("the report has no count for the file being replaced:\n%s", joined)
	}
}

func TestRunWritesAGalleryTheHostLoads(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "models", "index.yaml")
	if err := os.MkdirAll(filepath.Dir(lib), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lib, []byte(miniLibrary), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "index.yaml")
	if err := os.WriteFile(out, []byte(oldGallery), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--library", lib, "--out", out}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "# Model gallery: a GENERATED copy") {
		t.Errorf("the generated file has no header:\n%s", raw)
	}
	for _, k := range droppedGalleryKeys {
		if strings.Contains(string(raw), k+":") {
			t.Errorf("the generated gallery still has %q", k)
		}
	}
	var loaded []gallery.GalleryModel
	if err := yaml.Unmarshal(raw, &loaded); err != nil || len(loaded) != 2 {
		t.Fatalf("the output does not load: %v", err)
	}
	// A second run writes the same bytes.
	if err := run([]string{"--library", lib, "--out", out}); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(out)
	if string(again) != string(raw) {
		t.Error("two runs wrote different files")
	}
	if err := run([]string{"--library", lib}); err == nil {
		t.Error("run without an output succeeded")
	}
	if err := run([]string{"--library", lib, "--out", out + ".x", "--golden", filepath.Join(dir, "g")}); err == nil {
		t.Error("a golden run on a library without every sample class succeeded")
	}
}
