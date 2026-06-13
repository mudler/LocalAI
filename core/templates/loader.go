package templates

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/pkg/utils"
)

// templateFileSuffixes are file extensions that identify a template file on disk.
var templateFileSuffixes = []string{".tmpl"}

// isTemplateFile reports whether a filename looks like a prompt template,
// optionally suffixed by one of templateFileSuffixes. We deliberately do NOT
// treat embedded model artifacts (e.g. gguf/bin/yaml) as templates — that is
// the job of pkg/model.ModelLoader. Splitting this decision into a dedicated
// loader keeps package responsibilities disjoint, which was the motivation for
// the "Split ModelLoader and TemplateLoader" TODO in pkg/model/loader.go.
func isTemplateFile(name string) bool {
	lower := strings.ToLower(name)
	for _, s := range templateFileSuffixes {
		if strings.HasSuffix(lower, s) {
			return true
		}
	}
	return false
}

// TemplateLoader owns on-disk discovery of prompt template files. It is a
// peer to pkg/model.ModelLoader, but restricted to .tmpl files — which is why
// it lives in core/templates and never touches model binaries.
type TemplateLoader struct {
	mu            sync.RWMutex
	templatesPath string
	// cache of basename -> absolute path discovered on the last ListTemplates
	// call. A nil map means "no scan has happened yet"; callers typically
	// only read it through ListTemplates.
	known map[string]string
}

// NewTemplateLoader returns a loader rooted at templatesPath. The path is
// permitted to be the same directory as the model path; TemplateLoader uses
// suffix-based filtering to only pick up template files within it.
func NewTemplateLoader(templatesPath string) *TemplateLoader {
	return &TemplateLoader{
		templatesPath: templatesPath,
		known:         nil,
	}
}

// ListTemplates returns the basenames of all template files currently
// available under the loader's root directory. Hidden files (leading dot)
// are skipped. The result is cached in-memory; call Invalidate to force a
// re-scan (e.g. after a user uploads a new .tmpl via the model editor).
func (tl *TemplateLoader) ListTemplates() ([]string, error) {
	tl.mu.RLock()
	if tl.known != nil {
		names := make([]string, 0, len(tl.known))
		for n := range tl.known {
			names = append(names, n)
		}
		tl.mu.RUnlock()
		return names, nil
	}
	tl.mu.RUnlock()

	return tl.scanAndCache()
}

// Resolve returns the absolute path for a template basename (with or without
// the .tmpl suffix) if and only if it exists on disk and lives inside
// templatesPath. The second result is false when no such file is present.
func (tl *TemplateLoader) Resolve(name string) (string, bool) {
	// Normalize: drop .tmpl suffix if present so callers can pass either
	// "chatml" or "chatml.tmpl".
	base := strings.TrimSuffix(name, ".tmpl")
	candidate := filepath.Join(tl.templatesPath, base+".tmpl")

	if err := utils.VerifyPath(filepath.Base(candidate), tl.templatesPath); err != nil {
		return "", false
	}
	if _, err := os.Stat(candidate); err != nil {
		return "", false
	}
	return candidate, true
}

// Invalidate clears the internal cache, forcing the next ListTemplates call
// to read the filesystem. Safe to call from a hooks handler after model
// edits that may have added/removed template files.
func (tl *TemplateLoader) Invalidate() {
	tl.mu.Lock()
	tl.known = nil
	tl.mu.Unlock()
}

func (tl *TemplateLoader) scanAndCache() ([]string, error) {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	// Double-check after acquiring the write lock — another goroutine may
	// have populated the cache while we were waiting.
	if tl.known != nil {
		names := make([]string, 0, len(tl.known))
		for n := range tl.known {
			names = append(names, n)
		}
		return names, nil
	}

	entries, err := os.ReadDir(tl.templatesPath)
	if err != nil {
		return nil, fmt.Errorf("reading templates dir %q: %w", tl.templatesPath, err)
	}

	names := make([]string, 0)
	known := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Skip dotfiles; a model editor may drop e.g. ".DS_Store" or swap
		// files that should never surface as templates.
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !isTemplateFile(name) {
			continue
		}
		abs, err := filepath.Abs(filepath.Join(tl.templatesPath, name))
		if err != nil {
			continue
		}
		// Use the "bare" name (without .tmpl) as the lookup key, matching
		// the "chatml", "llama3" convention used in model YAMLs.
		bare := strings.TrimSuffix(name, filepath.Ext(name))
		known[bare] = abs
		names = append(names, bare)
	}
	tl.known = known
	return names, nil
}
