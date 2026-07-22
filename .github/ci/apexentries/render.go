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
	Name string
	Repo string
	// DraftRepo is the repo publishing the drafter, when it is not the repo
	// publishing the weights. Speculative pairings routinely cross repos, so
	// the drafter cannot be assumed to sit next to the weights. Empty means
	// same-repo, which is how the *-APEX-MTP-GGUF repos ship.
	DraftRepo string
	Template  string
	Weights   []GGUFFile
	MMProj    *GGUFFile
	SpecType  string
	DraftFile *GGUFFile
	BaseTags  []string
}

// specTuning is the acceptance-window tuning each spec type ships with, copied
// from the hand-written entries that already run these two mechanisms rather
// than invented here. The two differ because the drafters differ: self-drafted
// MTP heads produce a short, high-confidence proposal (15+ hand-written entries
// use 6 with a 0.75 floor), while a separate DFlash drafter is cheap enough to
// run far ahead unconditionally (the five hand-written dflash entries use 15 and
// set no floor).
var specTuning = map[string][]string{
	"draft-mtp":    {"spec_n_max:6", "spec_p_min:0.75"},
	"draft-dflash": {"spec_n_max:15"},
}

func hfURI(repo, file string) string {
	return fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repo, file)
}

// localPath is where a downloaded file lands.
//
// The hand-written entries namespace by the repo's BARE name
// (llama-cpp/models/<repo>/<file>), which is not unique. LiquidAI/LFM2.5-8B-A1B-GGUF
// and unsloth/LFM2.5-8B-A1B-GGUF share a basename, so both claim
// llama-cpp/models/LFM2.5-8B-A1B-GGUF/, and installing the second after the first
// either overwrites weights whose recorded sha256 belongs to the other file or is
// skipped as already present. Two owners publishing the same model name is the
// normal case for quantizers, not an edge case, so the owner has to be in the path.
//
// The owner becomes its own path segment rather than being folded into the
// directory name: owner/repo is unique on HuggingFace and "/" cannot occur inside
// either half, so this is the only form that is collision-proof by construction.
// It still reads as the hand-written convention with the owner restored, and the
// extra depth is already present in the index for sharded builds.
func localPath(kind, repo, file string) string {
	// path.Dir yields "." for a repo named without an owner, which path.Join
	// drops, so such a caller keeps the historical two-segment layout.
	return path.Join("llama-cpp", kind, path.Dir(repo), path.Base(repo), file)
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

	// gallery/virtual.yaml carries no backend, so nothing else would name an
	// engine for these entries. Matching the hand-written entries on
	// known_usecases too: LocalAI would fall back to the backend defaults, but
	// generated entries should not read differently from their neighbours.
	e.Overrides["backend"] = "llama-cpp"
	e.Overrides["known_usecases"] = []string{"chat"}

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
		// An explicit known_usecases SUPPRESSES the backend-default fallback in
		// core/gallery/models_types.go, so a multimodal entry left at chat-only
		// never matches FilterGalleryModelsByUsecase(FLAG_VISION) or
		// FilterGalleryModelsByMultimodal and vanishes from the UI's vision and
		// multimodal filters. 19 of the 45 APEX repos ship an mmproj.
		e.Overrides["known_usecases"] = []string{"chat", "vision"}
		e.Overrides["mmproj"] = localPath("mmproj", in.Repo, in.MMProj.Name)
		e.Files = append(e.Files, EntryFile{
			Filename: localPath("mmproj", in.Repo, in.MMProj.Name),
			SHA256:   in.MMProj.SHA256,
			URI:      hfURI(in.Repo, in.MMProj.Name),
		})
	}

	// A spec type is configured independently of a drafter FILE. Weights that
	// carry their own MTP heads need no second download, and requiring one left
	// the *-APEX-MTP-GGUF builds shipping the larger heads-bearing weights with
	// the heads switched off: a strictly bigger download at the same speed,
	// ranked identically to the plain rung at the same tier.
	if in.SpecType != "" {
		options = append(options, "spec_type:"+in.SpecType)
		options = append(options, specTuning[in.SpecType]...)
		// The tag is derived from the spec type this entry sets and from nothing
		// else. Variant ranking reads tags only, so a tag taken from a repo or
		// entry NAME would promote a build that is no faster whenever the name
		// and the configuration disagree.
		e.Tags = append(e.Tags, strings.TrimPrefix(in.SpecType, "draft-"))
	}

	if in.SpecType != "" && in.DraftFile != nil {
		// Fall back to the weights repo so pairings that publish the drafter
		// alongside the weights keep working without restating the repo.
		draftRepo := in.DraftRepo
		if draftRepo == "" {
			draftRepo = in.Repo
		}
		draftPath := localPath("models", draftRepo, in.DraftFile.Name)

		e.Overrides["draft_model"] = draftPath
		e.Overrides["flash_attention"] = "on"
		e.Files = append(e.Files, EntryFile{
			Filename: draftPath,
			SHA256:   in.DraftFile.SHA256,
			URI:      hfURI(draftRepo, in.DraftFile.Name),
		})
	}

	e.Overrides["options"] = options
	return e
}
