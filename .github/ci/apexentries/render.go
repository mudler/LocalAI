package main

import (
	"fmt"
	"path"
	"strings"
)

// EntryFile is one downloadable file of a gallery entry.
type EntryFile struct {
	Filename string `yaml:"filename"`
	SHA256   string `yaml:"sha256"`
	URI      string `yaml:"uri"`
}

// GalleryEntry is the subset of a gallery entry this generator writes.
//
// Named GalleryEntry rather than Entry because the test files dot-import
// Ginkgo, whose table DSL exports an Entry that a package-level Entry would
// collide with. The yaml tags are what the gallery index sees, so the Go
// identifier is free to differ.
type GalleryEntry struct {
	Name        string         `yaml:"name"`
	URL         string         `yaml:"url"`
	Description string         `yaml:"description,omitempty"`
	Tags        []string       `yaml:"tags,omitempty"`
	Overrides   map[string]any `yaml:"overrides,omitempty"`
	Files       []EntryFile    `yaml:"files,omitempty"`
	Variants    []VariantRef   `yaml:"variants,omitempty"`
}

// VariantRef mirrors the gallery's variant reference: a name and nothing else.
type VariantRef struct {
	Model string `yaml:"model"`
}

// ChildInput is everything needed to render one non-parent entry.
type ChildInput struct {
	Name      string
	Repo      string
	Template  string
	Weights   []GGUFFile
	MMProj    *GGUFFile
	SpecType  string
	DraftFile *GGUFFile
	BaseTags  []string
}

func hfURI(repo, file string) string {
	return fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repo, file)
}

// localPath is where a downloaded file lands, namespaced by repo so two
// entries drawing on different repos never collide on a bare filename.
func localPath(kind, repo, file string) string {
	return path.Join("llama-cpp", kind, path.Base(repo), file)
}

// RenderChild builds one child entry.
//
// The dflash/mtp tag is added if and only if this entry sets a spec_type,
// because variant ranking reads tags and nothing else, and a tag that does not
// match what the entry configures either promotes a build that is no faster or
// hides one that is.
func RenderChild(in ChildInput) GalleryEntry {
	e := GalleryEntry{
		Name:      in.Name,
		URL:       fmt.Sprintf("github:mudler/LocalAI/gallery/%s@master", in.Template),
		Tags:      append([]string{}, in.BaseTags...),
		Overrides: map[string]any{},
	}

	options := []string{"use_jinja:true"}

	for _, w := range in.Weights {
		e.Files = append(e.Files, EntryFile{
			Filename: localPath("models", in.Repo, w.Name),
			SHA256:   w.SHA256,
			URI:      hfURI(in.Repo, w.Name),
		})
	}
	e.Overrides["parameters"] = map[string]any{
		"model": localPath("models", in.Repo, in.Weights[0].Name),
	}

	if in.MMProj != nil {
		e.Overrides["mmproj"] = localPath("mmproj", in.Repo, in.MMProj.Name)
		e.Files = append(e.Files, EntryFile{
			Filename: localPath("mmproj", in.Repo, in.MMProj.Name),
			SHA256:   in.MMProj.SHA256,
			URI:      hfURI(in.Repo, in.MMProj.Name),
		})
	}

	if in.SpecType != "" && in.DraftFile != nil {
		options = append(options, "spec_type:"+in.SpecType)
		e.Overrides["draft_model"] = localPath("models", in.Repo, in.DraftFile.Name)
		e.Overrides["flash_attention"] = "on"
		e.Files = append(e.Files, EntryFile{
			Filename: localPath("models", in.Repo, in.DraftFile.Name),
			SHA256:   in.DraftFile.SHA256,
			URI:      hfURI(in.Repo, in.DraftFile.Name),
		})
		e.Tags = append(e.Tags, strings.TrimPrefix(in.SpecType, "draft-"))
	}

	e.Overrides["options"] = options
	return e
}
