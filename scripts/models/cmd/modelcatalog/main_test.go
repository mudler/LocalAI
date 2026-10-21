// SPDX-License-Identifier: MIT
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/gallery"
)

const miniGallery = `- name: a
  url: github:example/virtual.yaml@master
  last_checked: 2026-10-01
  overrides:
    temperature: 1.0
  variants:
    - model: b
- name: b
  variants: []
  files:
    - filename: b.gguf
      sha256: abc
      uri: https://example.invalid/b.gguf
`

func TestRenderIsDeterministicAndReportsDroppedKeys(t *testing.T) {
	entries, err := load([]byte(miniGallery))
	if err != nil {
		t.Fatal(err)
	}
	first, err := render(entries)
	if err != nil {
		t.Fatal(err)
	}
	second, err := render(entries)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := gallery.EncodeModelIndex(first)
	b, _ := gallery.EncodeModelIndex(second)
	if string(a) != string(b) || len(first) != 2 {
		t.Errorf("render is not deterministic or lost entries: %d entries", len(first))
	}
	if !strings.Contains(string(a), "temperature: 1.0") {
		t.Errorf("the float lost its form:\n%s", a)
	}
	dropped, err := droppedKeys([]byte(miniGallery))
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 1 || dropped["last_checked"] != 1 {
		t.Errorf("dropped = %v", dropped)
	}
}

func TestRunWritesAValidLibraryAndRefusesNoOutput(t *testing.T) {
	dir := t.TempDir()
	index := filepath.Join(dir, "index.yaml")
	if err := os.WriteFile(index, []byte(miniGallery), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "models", "index.yaml")
	if err := run([]string{"--index", index, "--out", out}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	res, err := gallery.DecodeModelIndex(raw)
	if err != nil || len(res.Entries) != 2 {
		t.Fatalf("the output is not a valid library: %v", err)
	}
	if err := run([]string{"--index", index}); err == nil {
		t.Error("run without an output succeeded")
	}
	if err := run([]string{"--index", index, "--out", out + ".x", "--golden", filepath.Join(dir, "g")}); err == nil {
		t.Error("a golden run on an index without every sample class succeeded")
	}
}
