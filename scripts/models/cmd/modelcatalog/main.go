// Command modelcatalog writes the model gallery from the model library. The
// library, gallery/models/index.yaml, is the source of truth and is where
// models are edited. The gallery, gallery/index.yaml, is a generated copy that
// stays only so releases before the cutover keep working; it is never edited
// by hand.
//
// Every library entry is converted to a gallery entry and back before
// anything is written, and the command fails when any entry does not come
// back equal. Fields the gallery cannot carry are dropped, and every run lists
// them. It can also write the golden files the tests in core/gallery compare
// against.
//
//	go run ./scripts/models/cmd/modelcatalog \
//	  --library gallery/models/index.yaml --out gallery/index.yaml
//
//	go run ./scripts/models/cmd/modelcatalog \
//	  --library gallery/models/index.yaml --golden core/gallery/testdata/model_library
package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/gallery"
	"gopkg.in/yaml.v3"
)

const header = `# Model gallery: a GENERATED copy of the model library, gallery/models/index.yaml.
# The library is the source of truth; edit it, not this file. This file is
# written by scripts/models/cmd/modelcatalog and stays only so releases before
# the cutover keep working. CI fails when it differs from the generated copy:
#   go run ./scripts/models/cmd/modelcatalog --library gallery/models/index.yaml --out gallery/index.yaml
`

// droppedGalleryKeys are the keys that the old hand-edited gallery carried
// and the library does not. The gallery struct does not read them, so the host
// ignored them on load and dropping them changes nothing it reads. They are
// listed on every run so a change in this set is noticed.
var droppedGalleryKeys = []string{"last_checked", "parameters", "Base Model", "Context Length", "License", "Training Data"}

// load reads the model library: strictly, as the host reads it.
func load(src []byte) ([]gallery.ModelLibraryEntry, error) {
	res, err := gallery.DecodeModelIndex(src)
	if err != nil {
		return nil, fmt.Errorf("reading the model library: %w", err)
	}
	if len(res.Entries) == 0 {
		return nil, fmt.Errorf("the model library has no model entries (%d entries of another kind skipped); is this the library and not a gallery?", len(res.Skipped))
	}
	if len(res.Skipped) > 0 {
		fmt.Fprintf(os.Stderr, "modelcatalog: %d library entries of another kind are not in the gallery copy\n", len(res.Skipped))
	}
	return res.Entries, nil
}

// render converts every library entry to its gallery entry, checks that the
// gallery entry goes back to the library and forward again unchanged, and
// returns the gallery entries in library order.
func render(entries []gallery.ModelLibraryEntry) ([]gallery.GalleryModel, error) {
	out := make([]gallery.GalleryModel, 0, len(entries))
	var failures []string
	for _, e := range entries {
		m, err := e.ToGallery()
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", e.ID, err))
			continue
		}
		if err := gallery.VerifyModelRoundTrip(m); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", e.ID, err))
			continue
		}
		out = append(out, m)
	}
	if len(failures) > 0 {
		return nil, fmt.Errorf("%d of %d entries do not convert to the gallery and back:\n%s", len(failures), len(entries), strings.Join(failures, "\n"))
	}
	return out, nil
}

// droppedKeys counts the keys of a raw gallery file that the gallery struct
// does not read. Run on the gallery copy that is about to be replaced, it
// shows what the replacement drops.
func droppedKeys(src []byte) (map[string]int, error) {
	var raw []map[string]any
	if err := yaml.Unmarshal(src, &raw); err != nil {
		return nil, err
	}
	known := yamlKeys(reflect.TypeOf(gallery.GalleryModel{}))
	dropped := map[string]int{}
	for _, e := range raw {
		for k := range e {
			if !known[k] {
				dropped[k]++
			}
		}
	}
	return dropped, nil
}

func yamlKeys(t reflect.Type) map[string]bool {
	keys := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("yaml")
		name, opts, _ := strings.Cut(tag, ",")
		if strings.Contains(opts, "inline") {
			for k := range yamlKeys(f.Type) {
				keys[k] = true
			}
			continue
		}
		if name != "" && name != "-" {
			keys[name] = true
		}
	}
	return keys
}

// reportDropped writes what the gallery copy drops: the fixed set of gallery
// keys, how often the copy being replaced still had each, and the library
// header fields that have no place in a gallery entry.
func reportDropped(w func(format string, args ...any), library []gallery.ModelLibraryEntry, previous []byte) {
	w("modelcatalog: the gallery copy drops these gallery keys; the host does not read them: %s\n", strings.Join(droppedGalleryKeys, ", "))
	if len(previous) > 0 {
		if dropped, err := droppedKeys(previous); err == nil {
			keys := make([]string, 0, len(dropped))
			for k, n := range dropped {
				keys = append(keys, fmt.Sprintf("%s (%d)", k, n))
			}
			sort.Strings(keys)
			if len(keys) == 0 {
				keys = []string{"none"}
			}
			w("modelcatalog: in the gallery file being replaced: %s\n", strings.Join(keys, ", "))
			// A key outside the fixed set means the host may now read something
			// the library cannot carry, or a new unread key appeared.
			for k := range dropped {
				if !contains(droppedGalleryKeys, k) {
					w("modelcatalog: WARNING: %q is not in the known dropped set\n", k)
				}
			}
		}
	}
	lost := map[string]int{}
	for _, e := range library {
		for _, f := range gallery.GalleryHeaderLoss(e) {
			lost[f]++
		}
	}
	parts := []string{"version, channel and schemaVersion"}
	for _, f := range []string{"name", "policy"} {
		if lost[f] > 0 {
			parts = append(parts, fmt.Sprintf("%s (%d entries set it)", f, lost[f]))
		}
	}
	w("modelcatalog: library header fields with no place in a gallery entry: %s\n", strings.Join(parts, ", "))
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// sampleClasses picks readable golden entries: the first gallery entry that
// has each feature.
var sampleClasses = []struct {
	name string
	has  func(gallery.GalleryModel) bool
}{
	{"plain", func(m gallery.GalleryModel) bool {
		return m.URL != "" && len(m.AdditionalFiles) > 0 && len(m.Variants) == 0 && m.Size == "" && m.Backend == "" && len(m.Overrides) > 0
	}},
	{"variants", func(m gallery.GalleryModel) bool { return len(m.Variants) > 0 }},
	{"config-file", func(m gallery.GalleryModel) bool { return len(m.ConfigFile) > 0 }},
	{"size", func(m gallery.GalleryModel) bool { return m.Size != "" }},
	{"backend", func(m gallery.GalleryModel) bool { return m.Backend != "" }},
	{"no-url", func(m gallery.GalleryModel) bool { return m.URL == "" && len(m.ConfigFile) == 0 }},
	{"no-files", func(m gallery.GalleryModel) bool { return m.URL != "" && len(m.AdditionalFiles) == 0 }},
	{"multiline-description", func(m gallery.GalleryModel) bool {
		return strings.Contains(m.Description, "\n") && len(m.Variants) == 0
	}},
	{"empty-variants", func(m gallery.GalleryModel) bool { return m.Variants != nil && len(m.Variants) == 0 }},
	{"float-override", func(m gallery.GalleryModel) bool { return hasFloat(m.Overrides) }},
}

func hasFloat(v any) bool {
	switch t := v.(type) {
	case float64:
		// A whole number written as a float, such as 1.0, is the case the
		// encoder has to keep apart from an integer.
		return t == math.Trunc(t)
	case map[string]any:
		for _, x := range t {
			if hasFloat(x) {
				return true
			}
		}
	case []any:
		for _, x := range t {
			if hasFloat(x) {
				return true
			}
		}
	}
	return false
}

// writeGolden writes the digests of every library entry and the readable
// samples: the library entry as it stands in the library, and the gallery
// entry generated from it.
func writeGolden(dir string, library []gallery.ModelLibraryEntry, models []gallery.GalleryModel) error {
	if len(library) != len(models) {
		return fmt.Errorf("the library has %d entries and %d gallery entries", len(library), len(models))
	}
	samples := filepath.Join(dir, "samples")
	if err := os.RemoveAll(samples); err != nil {
		return err
	}
	if err := os.MkdirAll(samples, 0o750); err != nil {
		return err
	}
	var lines []string
	for _, e := range library {
		l, g, err := gallery.ModelLibraryDigests(e)
		if err != nil {
			return fmt.Errorf("%s: %w", e.ID, err)
		}
		lines = append(lines, e.ID+"\t"+l+"\t"+g)
	}
	sort.Strings(lines)
	digests := "# id, digest of the library entry, digest of the gallery entry generated from it\n# Written by scripts/models/cmd/modelcatalog --golden; do not edit by hand.\n" + strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "digests.tsv"), []byte(digests), 0o600); err != nil {
		return err
	}
	for _, c := range sampleClasses {
		picked := -1
		for i := range models {
			if c.has(models[i]) {
				picked = i
				break
			}
		}
		if picked < 0 {
			return fmt.Errorf("no library entry for the sample class %q", c.name)
		}
		l, err := gallery.EncodeModelIndex([]gallery.ModelLibraryEntry{library[picked]})
		if err != nil {
			return err
		}
		g, err := gallery.EncodeGalleryIndex([]gallery.GalleryModel{models[picked]})
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(samples, c.name+".library.yaml"), l, 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(samples, c.name+".gallery.yaml"), g, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func run(args []string) error {
	fs := flag.NewFlagSet("modelcatalog", flag.ContinueOnError)
	libraryPath := fs.String("library", "gallery/models/index.yaml", "the model library, the source")
	out := fs.String("out", "", "generated gallery file to write")
	golden := fs.String("golden", "", "directory to write the golden files to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" && *golden == "" {
		return errors.New("usage: --out file and/or --golden dir [--library file]")
	}
	src, err := os.ReadFile(*libraryPath)
	if err != nil {
		return err
	}
	library, err := load(src)
	if err != nil {
		return err
	}
	models, err := render(library)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "modelcatalog: %d of %d library entries convert to the gallery and back\n", len(models), len(library))
	var previous []byte
	if *out != "" {
		previous, _ = os.ReadFile(*out)
	}
	reportDropped(func(f string, a ...any) { fmt.Fprintf(os.Stderr, f, a...) }, library, previous)
	if *out != "" {
		body, err := gallery.EncodeGalleryIndex(models)
		if err != nil {
			return err
		}
		// The result must load the way the host loads a gallery, and give the
		// same entries.
		var loaded []gallery.GalleryModel
		if err := yaml.Unmarshal(body, &loaded); err != nil {
			return fmt.Errorf("the generated gallery does not load: %w", err)
		}
		if len(loaded) != len(models) {
			return fmt.Errorf("the generated gallery loads %d entries, the library has %d", len(loaded), len(models))
		}
		for i := range loaded {
			if !reflect.DeepEqual(loaded[i], models[i]) {
				return fmt.Errorf("the generated gallery entry %q loads differently from the library entry", models[i].Name)
			}
		}
		if err := os.MkdirAll(filepath.Dir(*out), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(*out, append([]byte(header), body...), 0o600); err != nil {
			return err
		}
	}
	if *golden != "" {
		if err := writeGolden(*golden, library, models); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "modelcatalog:", err)
		os.Exit(1)
	}
}
