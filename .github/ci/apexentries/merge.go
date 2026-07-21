package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ExistingIndex is the lookup built from the current gallery: entry names, and
// which entry claims each weight URI.
type ExistingIndex struct {
	ByName map[string]int
	ByURI  map[string]string
}

// LoadExisting reads the gallery index for dedup purposes only. It is
// deliberately not used to rewrite the file: the index is 40,000 lines, and a
// YAML round trip would reflow the whole thing into an unreviewable diff.
func LoadExisting(path string) (*ExistingIndex, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var entries []struct {
		Name  string `yaml:"name"`
		Files []struct {
			URI string `yaml:"uri"`
		} `yaml:"files"`
	}
	if err := yaml.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	ix := &ExistingIndex{ByName: map[string]int{}, ByURI: map[string]string{}}
	for i, e := range entries {
		ix.ByName[e.Name] = i
		for _, f := range e.Files {
			if f.URI != "" {
				ix.ByURI[f.URI] = e.Name
			}
		}
	}
	return ix, nil
}

// Merge splits generated entries into those to add and those already covered.
// reused maps a generated name to the existing entry that stands in for it, so
// a parent can reference what is already there instead of duplicating weights.
func Merge(existing *ExistingIndex, generated []GalleryEntry) (add []GalleryEntry, reused map[string]string) {
	reused = map[string]string{}

	for _, e := range generated {
		if _, clash := existing.ByName[e.Name]; clash {
			reused[e.Name] = e.Name
			continue
		}
		if len(e.Files) > 0 {
			if owner, ok := existing.ByURI[e.Files[0].URI]; ok {
				reused[e.Name] = owner
				continue
			}
		}
		add = append(add, e)
	}
	return add, reused
}
