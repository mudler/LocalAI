// SPDX-License-Identifier: MIT

// Command modelcatalog renders the model library from the model gallery. The
// gallery, gallery/index.yaml, stays the source of truth: this command reads
// it with the same structs the host reads it with and never edits it.
//
// Every entry is converted to a library entry and back before anything is
// written, and the command fails when any entry does not come back equal. It
// can also write the golden files the tests in core/gallery compare against.
//
//	go run ./scripts/models/cmd/modelcatalog \
//	  --index gallery/index.yaml --out gallery/models/index.yaml
//
//	go run ./scripts/models/cmd/modelcatalog \
//	  --index gallery/index.yaml --golden core/gallery/testdata/model_library
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

const header = `# Model library: the models of the model gallery in the library entry shape. The
# gallery, gallery/index.yaml, stays the source of truth and the default. This
# file is rendered with scripts/models/cmd/modelcatalog; render it again after
# the gallery changes and never edit it by hand. The host lists it as a default
# model source.
`

// load reads the gallery index the way the host does.
func load(src []byte) ([]gallery.GalleryModel, error) {
	var entries []gallery.GalleryModel
	if err := yaml.Unmarshal(src, &entries); err != nil {
		return nil, fmt.Errorf("reading the model gallery: %w", err)
	}
	return entries, nil
}

// render converts every entry, checks that each one comes back equal and
// returns the library entries in gallery order.
func render(entries []gallery.GalleryModel) ([]gallery.ModelLibraryEntry, error) {
	out := make([]gallery.ModelLibraryEntry, 0, len(entries))
	var failures []string
	for _, m := range entries {
		if err := gallery.VerifyModelRoundTrip(m); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", m.Name, err))
			continue
		}
		e, err := gallery.ModelLibraryEntryFromGallery(m)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", m.Name, err))
			continue
		}
		out = append(out, e)
	}
	if len(failures) > 0 {
		return nil, fmt.Errorf("%d of %d entries do not round trip:\n%s", len(failures), len(entries), strings.Join(failures, "\n"))
	}
	return out, nil
}

// droppedKeys lists the keys of the raw gallery that the gallery struct does
// not read, with a count. The host drops them when it loads the index, so the
// library cannot carry them; they are reported so a new field is noticed.
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

// expandAliases copies a node and replaces every alias with a copy of the node
// it points at. The index uses anchors; a sample is one entry on its own, so it
// cannot refer to an anchor in another entry.
func expandAliases(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode {
		return expandAliases(n.Alias)
	}
	c := *n
	c.Anchor = ""
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, child := range n.Content {
		c.Content[i] = expandAliases(child)
	}
	return &c
}

// writeGolden writes the digests of every entry and the readable samples.
func writeGolden(dir string, src []byte, entries []gallery.GalleryModel) error {
	// The gallery side of a sample is the entry as written in the index, so a
	// float such as 1.0 keeps its form.
	var raw []yaml.Node
	if err := yaml.Unmarshal(src, &raw); err != nil {
		return err
	}
	if len(raw) != len(entries) {
		return fmt.Errorf("the index has %d entries and %d raw nodes", len(entries), len(raw))
	}
	samples := filepath.Join(dir, "samples")
	if err := os.RemoveAll(samples); err != nil {
		return err
	}
	if err := os.MkdirAll(samples, 0o755); err != nil {
		return err
	}
	var lines []string
	for _, m := range entries {
		g, l, err := gallery.ModelLibraryDigests(m)
		if err != nil {
			return fmt.Errorf("%s: %w", m.Name, err)
		}
		lines = append(lines, m.Name+"\t"+g+"\t"+l)
	}
	sort.Strings(lines)
	digests := "# name, digest of the gallery entry, digest of the library entry\n# Written by scripts/models/cmd/modelcatalog --golden; do not edit by hand.\n" + strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "digests.tsv"), []byte(digests), 0o644); err != nil {
		return err
	}
	for _, c := range sampleClasses {
		var picked *gallery.GalleryModel
		var pickedRaw *yaml.Node
		for i := range entries {
			if c.has(entries[i]) {
				picked, pickedRaw = &entries[i], &raw[i]
				break
			}
		}
		if picked == nil {
			return fmt.Errorf("no gallery entry for the sample class %q", c.name)
		}
		g, err := yaml.Marshal([]*yaml.Node{expandAliases(pickedRaw)})
		if err != nil {
			return err
		}
		e, err := gallery.ModelLibraryEntryFromGallery(*picked)
		if err != nil {
			return err
		}
		l, err := gallery.EncodeModelIndex([]gallery.ModelLibraryEntry{e})
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(samples, c.name+".gallery.yaml"), g, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(samples, c.name+".library.yaml"), l, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func run(args []string) error {
	fs := flag.NewFlagSet("modelcatalog", flag.ContinueOnError)
	index := fs.String("index", "gallery/index.yaml", "the model gallery")
	out := fs.String("out", "", "model library file to write")
	golden := fs.String("golden", "", "directory to write the golden files to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" && *golden == "" {
		return errors.New("usage: --out file and/or --golden dir [--index file]")
	}
	src, err := os.ReadFile(*index)
	if err != nil {
		return err
	}
	entries, err := load(src)
	if err != nil {
		return err
	}
	library, err := render(entries)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "modelcatalog: %d of %d gallery entries round trip\n", len(library), len(entries))
	if dropped, err := droppedKeys(src); err == nil && len(dropped) > 0 {
		keys := make([]string, 0, len(dropped))
		for k := range dropped {
			keys = append(keys, fmt.Sprintf("%s (%d)", k, dropped[k]))
		}
		sort.Strings(keys)
		fmt.Fprintf(os.Stderr, "modelcatalog: keys the gallery struct does not read, so the host drops them too: %s\n", strings.Join(keys, ", "))
	}
	if *out != "" {
		body, err := gallery.EncodeModelIndex(library)
		if err != nil {
			return err
		}
		// The result must be a valid model index for this host.
		if _, err := gallery.DecodeModelIndex(body); err != nil {
			return fmt.Errorf("the rendered index is not valid: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(*out, append([]byte(header), body...), 0o644); err != nil {
			return err
		}
	}
	if *golden != "" {
		if err := writeGolden(*golden, src, entries); err != nil {
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
