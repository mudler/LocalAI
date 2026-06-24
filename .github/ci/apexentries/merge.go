package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	hfShorthandPrefix = "huggingface://"
	hfResolvePrefix   = "https://huggingface.co/"
	hfResolveInfix    = "/resolve/main/"
)

// canonicalURI reduces the two interchangeable spellings of a HuggingFace file
// to one key, so a generated resolve/main URI dedups against the shorthand the
// gallery uses for the majority of its entries.
//
// The repo is exactly the first two path segments; everything after is the file
// path, which may itself contain slashes because sharded quants live in a
// subdirectory. Anything that is not recognisably one of the two forms is
// returned unchanged rather than guessed at, so mirrors and other hosts still
// dedup on their literal string.
func canonicalURI(uri string) string {
	switch {
	case strings.HasPrefix(uri, hfShorthandPrefix):
		rest := strings.TrimPrefix(uri, hfShorthandPrefix)
		owner, after, ok := strings.Cut(rest, "/")
		if !ok {
			return uri
		}
		name, file, ok := strings.Cut(after, "/")
		if !ok || owner == "" || name == "" || file == "" {
			return uri
		}
		return hfShorthandPrefix + owner + "/" + name + "/" + file

	case strings.HasPrefix(uri, hfResolvePrefix):
		rest := strings.TrimPrefix(uri, hfResolvePrefix)
		repo, file, ok := strings.Cut(rest, hfResolveInfix)
		if !ok || file == "" {
			return uri
		}
		// A repo is owner/name and nothing more; a longer prefix means this is
		// some other huggingface.co URL that must not be rewritten.
		owner, name, ok := strings.Cut(repo, "/")
		if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
			return uri
		}
		return hfShorthandPrefix + repo + "/" + file

	default:
		return uri
	}
}

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
				ix.ByURI[canonicalURI(f.URI)] = e.Name
			}
		}
	}
	return ix, nil
}

// Merge splits generated entries into those to add and those already covered.
// reused maps a generated name to the existing entry that stands in for it, so
// a parent can reference what is already there instead of duplicating weights.
// Several APEX repos share one base model, so the same counterpart rungs are
// generated more than once in a batch. The batch has to dedup against itself as
// well as against the gallery, tracked locally because the caller may reuse the
// ExistingIndex it passed in.
func Merge(existing *ExistingIndex, generated []GalleryEntry) (add []GalleryEntry, reused map[string]string) {
	reused = map[string]string{}
	batchNames := map[string]string{}
	batchURIs := map[string]string{}

	// Canonicalized into a local copy rather than in place: an ExistingIndex may
	// be hand-built or reused by the caller, so Merge must not rewrite it.
	existingURIs := make(map[string]string, len(existing.ByURI))
	for uri, owner := range existing.ByURI {
		existingURIs[canonicalURI(uri)] = owner
	}

	for _, e := range generated {
		// Name is checked before URI: a name collision must block the add
		// whatever the weights say, since duplicate names corrupt the index.
		if _, clash := existing.ByName[e.Name]; clash {
			reused[e.Name] = e.Name
			continue
		}
		if claimant, clash := batchNames[e.Name]; clash {
			reused[e.Name] = claimant
			continue
		}
		if len(e.Files) > 0 {
			uri := canonicalURI(e.Files[0].URI)
			if owner, ok := existingURIs[uri]; ok {
				reused[e.Name] = owner
				continue
			}
			if claimant, ok := batchURIs[uri]; ok {
				reused[e.Name] = claimant
				continue
			}
			batchURIs[uri] = e.Name
		}
		batchNames[e.Name] = e.Name
		add = append(add, e)
	}
	return add, reused
}
