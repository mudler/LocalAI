package templates

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// --- isTemplateFile ---

func TestIsTemplateFile(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"tmpl suffix", "chatml.tmpl", true},
		{"tmpl uppercase", "CHATML.TMPL", true},
		{"tmpl mixed case", "ChatML.Tmpl", true},
		{"no suffix", "chatml", false},
		{"wrong suffix", "chatml.txt", false},
		{"model binary", "llama-2-7b.gguf", false},
		{"yaml config", "model.yaml", false},
		{"empty string", "", false},
		{"just suffix", ".tmpl", true},
		{"dotfile", ".secret.tmpl", true}, // isTemplateFile only checks suffix
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTemplateFile(tt.input); got != tt.want {
				t.Errorf("isTemplateFile(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- NewTemplateLoader ---

func TestNewTemplateLoader(t *testing.T) {
	loader := NewTemplateLoader("/tmp")
	if loader == nil {
		t.Fatal("NewTemplateLoader returned nil")
	}
}

// --- helpers ---

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// --- ListTemplates ---

func TestListTemplatesEmptyDir(t *testing.T) {
	dir := t.TempDir()
	loader := NewTemplateLoader(dir)
	names, err := loader.ListTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Errorf("expected 0 templates, got %d: %v", len(names), names)
	}
}

func TestListTemplatesSingle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "chatml.tmpl", "template content")
	loader := NewTemplateLoader(dir)
	names, err := loader.ListTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 {
		t.Fatalf("expected 1 template, got %d: %v", len(names), names)
	}
	if names[0] != "chatml" {
		t.Errorf("expected bare name 'chatml', got %q", names[0])
	}
}

func TestListTemplatesMultiple(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "chatml.tmpl", "a")
	writeFile(t, dir, "llama3.tmpl", "b")
	writeFile(t, dir, "mistral.tmpl", "c")
	loader := NewTemplateLoader(dir)
	names, err := loader.ListTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 3 {
		t.Fatalf("expected 3 templates, got %d: %v", len(names), names)
	}
	sort.Strings(names)
	expected := []string{"chatml", "llama3", "mistral"}
	for i, n := range names {
		if n != expected[i] {
			t.Errorf("names[%d] = %q, want %q", i, n, expected[i])
		}
	}
}

func TestListTemplatesSkipsHiddenFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "chatml.tmpl", "visible")
	writeFile(t, dir, ".secret.tmpl", "hidden")
	writeFile(t, dir, ".DS_Store", "garbage")
	loader := NewTemplateLoader(dir)
	names, err := loader.ListTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "chatml" {
		t.Errorf("expected only 'chatml', got %v", names)
	}
}

func TestListTemplatesSkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "chatml.tmpl", "a")
	if err := os.MkdirAll(filepath.Join(dir, "subdir.tmpl"), 0755); err != nil {
		t.Fatal(err)
	}
	loader := NewTemplateLoader(dir)
	names, err := loader.ListTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "chatml" {
		t.Errorf("expected only 'chatml', got %v", names)
	}
}

func TestListTemplatesSkipsNonTmplFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "chatml.tmpl", "a")
	writeFile(t, dir, "model.gguf", "binary")
	writeFile(t, dir, "config.yaml", "yaml")
	writeFile(t, dir, "README.md", "docs")
	loader := NewTemplateLoader(dir)
	names, err := loader.ListTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "chatml" {
		t.Errorf("expected only 'chatml', got %v", names)
	}
}

func TestListTemplatesNonExistentDir(t *testing.T) {
	loader := NewTemplateLoader("/tmp/nonexistent-template-dir-12345")
	_, err := loader.ListTemplates()
	if err == nil {
		t.Fatal("expected error for non-existent directory")
	}
}

// --- ListTemplates caching ---

func TestListTemplatesCachesResults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "chatml.tmpl", "a")
	loader := NewTemplateLoader(dir)

	// First call populates cache
	names1, err := loader.ListTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(names1) != 1 {
		t.Fatalf("first call: expected 1, got %d", len(names1))
	}

	// Add a new file AFTER first call
	writeFile(t, dir, "llama3.tmpl", "b")

	// Second call should return cached result (only chatml)
	names2, err := loader.ListTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(names2) != 1 {
		t.Errorf("expected cached result with 1 template, got %d: %v", len(names2), names2)
	}
}

func TestListTemplatesAfterInvalidate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "chatml.tmpl", "a")
	loader := NewTemplateLoader(dir)

	// Populate cache
	_, _ = loader.ListTemplates()

	// Add new file
	writeFile(t, dir, "llama3.tmpl", "b")

	// Invalidate and re-list
	loader.Invalidate()
	names, err := loader.ListTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Errorf("after invalidate expected 2 templates, got %d: %v", len(names), names)
	}
}

func TestListTemplatesConcurrent(t *testing.T) {
	dir := t.TempDir()
	// Use single-letter filenames properly
	for i := 0; i < 10; i++ {
		writeFile(t, dir, string(rune('a'+i))+".tmpl", "x")
	}
	loader := NewTemplateLoader(dir)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := loader.ListTemplates()
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	names, err := loader.ListTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 10 {
		t.Errorf("expected 10 templates, got %d", len(names))
	}
}

// --- Resolve ---

func TestResolveExisting(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "chatml.tmpl", "content")
	loader := NewTemplateLoader(dir)

	path, ok := loader.Resolve("chatml")
	if !ok {
		t.Fatal("Resolve('chatml') should succeed")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("expected absolute path, got %q", path)
	}
	if filepath.Base(path) != "chatml.tmpl" {
		t.Errorf("expected base 'chatml.tmpl', got %q", filepath.Base(path))
	}
}

func TestResolveWithTmplSuffix(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "chatml.tmpl", "content")
	loader := NewTemplateLoader(dir)

	path, ok := loader.Resolve("chatml.tmpl")
	if !ok {
		t.Fatal("Resolve('chatml.tmpl') should succeed")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("expected absolute path, got %q", path)
	}
}

func TestResolveNonExistent(t *testing.T) {
	dir := t.TempDir()
	loader := NewTemplateLoader(dir)

	_, ok := loader.Resolve("nonexistent")
	if ok {
		t.Fatal("Resolve('nonexistent') should fail")
	}
}

func TestResolveEmptyDir(t *testing.T) {
	dir := t.TempDir()
	loader := NewTemplateLoader(dir)

	_, ok := loader.Resolve("chatml")
	if ok {
		t.Fatal("Resolve should fail for empty dir")
	}
}

func TestResolvePathTraversal(t *testing.T) {
	dir := t.TempDir()
	// Create a file outside the templates dir
	malicious := filepath.Join(dir, "..", "etc", "passwd")
	loader := NewTemplateLoader(dir)

	_, ok := loader.Resolve(malicious)
	if ok {
		t.Fatal("Resolve with path traversal should fail")
	}
}

// --- Invalidate ---

func TestInvalidateClearsCache(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "chatml.tmpl", "a")
	loader := NewTemplateLoader(dir)

	// Populate cache
	_, _ = loader.ListTemplates()

	loader.Invalidate()

	// known should be nil, next ListTemplates should scan
	names, err := loader.ListTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 {
		t.Errorf("expected 1 template after invalidate, got %d", len(names))
	}
}

func TestInvalidateConcurrent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "chatml.tmpl", "a")
	loader := NewTemplateLoader(dir)
	_, _ = loader.ListTemplates()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			loader.Invalidate()
		}()
	}
	wg.Wait()

	// Should still work after concurrent invalidate
	names, err := loader.ListTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 {
		t.Errorf("expected 1 template, got %d", len(names))
	}
}

// --- edge cases ---

func TestResolveOnlyExactMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "chatml.tmpl", "a")
	writeFile(t, dir, "chatml-v2.tmpl", "b")
	loader := NewTemplateLoader(dir)

	path, ok := loader.Resolve("chatml")
	if !ok {
		t.Fatal("Resolve('chatml') should find exact match")
	}
	if filepath.Base(path) != "chatml.tmpl" {
		t.Errorf("expected 'chatml.tmpl', got %q", filepath.Base(path))
	}
}

func TestResolveTmplFileWithExtraDots(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.bin.tmpl", "a")
	loader := NewTemplateLoader(dir)

	// Resolve with .tmpl suffix should still work
	path, ok := loader.Resolve("file.bin.tmpl")
	if !ok {
		t.Fatal("Resolve('file.bin.tmpl') should succeed")
	}
	if filepath.Base(path) != "file.bin.tmpl" {
		t.Errorf("expected 'file.bin.tmpl', got %q", filepath.Base(path))
	}
}
