// Package remotemodel joins what a remote search or repository listing says
// with what this server knows: which models are already installed, and which
// backend an import would pick. It adds no network access of its own.
package remotemodel

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"github.com/mudler/LocalAI/pkg/modelsearch"
)

// RepoOfURI reads the repository an installed file or model uri points at:
// huggingface://owner/repo[/file], hf://, https://huggingface.co/owner/repo/...
// or pirateface://owner/repo[/file]. It reports false for anything else.
func RepoOfURI(uri string) (string, bool) {
	ref, err := modelsearch.ParseRepoURI(uri)
	if err != nil {
		return "", false
	}
	// A bare "owner/repo" or a "word/word" file path is not a repository
	// reference in an installed config: only schemes and the Hugging Face
	// host count.
	lower := strings.ToLower(strings.TrimSpace(uri))
	for _, p := range []string{"huggingface://", "hf://", "pirateface://", "https://huggingface.co/", "http://huggingface.co/", "huggingface.co/"} {
		if strings.HasPrefix(lower, p) {
			return strings.ToLower(ref.ID), true
		}
	}
	return "", false
}

// InstalledIndex answers whether a repository is installed.
type InstalledIndex struct {
	byRepo map[string]string
}

// NewInstalledIndex indexes installed model configs by the repositories they
// were installed from: their download file uris, the model uri, managed
// artifact sources, and the "Imported from <uri>" description an import
// writes. A repository seen in several configs maps to the first by name.
func NewInstalledIndex(cfgs []config.ModelConfig) *InstalledIndex {
	x := &InstalledIndex{byRepo: map[string]string{}}
	add := func(name, uri string) {
		if repo, ok := RepoOfURI(uri); ok {
			if cur, seen := x.byRepo[repo]; !seen || name < cur {
				x.byRepo[repo] = name
			}
		}
	}
	for _, c := range cfgs {
		for _, f := range c.DownloadFiles {
			add(c.Name, string(f.URI))
		}
		add(c.Name, c.Model)
		for _, a := range c.Artifacts {
			if a.Source.Type == "huggingface" && a.Source.Repo != "" {
				add(c.Name, "huggingface://"+a.Source.Repo)
			}
		}
		if rest, ok := strings.CutPrefix(c.Description, "Imported from "); ok {
			add(c.Name, strings.Fields(rest + " ")[0])
		}
	}
	return x
}

// Lookup returns the installed model name for a repository id.
func (x *InstalledIndex) Lookup(repoID string) (string, bool) {
	if x == nil {
		return "", false
	}
	name, ok := x.byRepo[strings.ToLower(repoID)]
	return name, ok
}

// EnrichSearch adds the installed state, a backend derived from the result's
// tags, and the variants hint to each result of a search response. The
// response is modified in place; its results must not be shared with a cache
// (Searcher returns copies).
func EnrichSearch(resp *modelsearch.Response, idx *InstalledIndex) {
	for i := range resp.Results {
		r := &resp.Results[i]
		if name, ok := idx.Lookup(r.ID); ok {
			r.Installed, r.InstalledName = true, name
		}
		r.Backend = DeriveBackend(r)
		r.HasVariants = r.Provider == modelsearch.PirateFaceName || hasTag(r.Tags, "gguf")
	}
}

func hasTag(tags []string, t string) bool {
	for _, x := range tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}

// DeriveBackend guesses the backend an import of a search result would pick.
// A search row carries tags, not files, so a placeholder file for each weight
// format the tags announce is given to the real importer registry, which then
// applies its own rules. The answer is a hint: the file listing of the
// repository (RepoFiles) gives the exact backend. Empty when no importer
// would match.
func DeriveBackend(r *modelsearch.Result) string {
	d := &hfapi.ModelDetails{ModelID: r.ID, Author: strings.SplitN(r.ID, "/", 2)[0], PipelineTag: r.PipelineTag}
	for _, t := range r.Tags {
		switch strings.ToLower(t) {
		case "gguf":
			d.Files = append(d.Files, hfapi.ModelFile{Path: "model-q4_k_m.gguf"})
		case "safetensors":
			d.Files = append(d.Files, hfapi.ModelFile{Path: "model.safetensors"})
		case "diffusers", "transformers", "sentence-transformers":
			d.LibraryName = strings.ToLower(t)
		}
	}
	if len(d.Files) == 0 && d.LibraryName == "" && d.PipelineTag == "" {
		return ""
	}
	p, err := importers.PreviewImport(r.URI, d, nil)
	if err != nil {
		return ""
	}
	return p.Backend
}

// PreviewFiles fills the installed state and the import dry run of a
// repository listing, on a copy: the listing the Inspector returns is shared
// and cached. quantization is the value for preferences.quantizations; empty
// uses the importer default.
func PreviewFiles(rf *modelsearch.RepoFiles, idx *InstalledIndex, quantization string) *modelsearch.RepoFiles {
	out := *rf
	if name, ok := idx.Lookup(rf.ID); ok {
		out.Installed, out.InstalledName = true, name
	}
	details := &hfapi.ModelDetails{
		ModelID: rf.ID, Author: strings.SplitN(rf.ID, "/", 2)[0],
		PipelineTag: rf.PipelineTag, LibraryName: rf.LibraryName,
	}
	for _, f := range rf.Files {
		details.Files = append(details.Files, hfapi.ModelFile{Path: f.Path, Size: f.Size, SHA256: f.SHA256, URL: rf.URI + "/" + f.Path})
	}
	// The importers key off weight files; a repository listing here has only
	// those, plus the tags the model card carries.
	var prefs json.RawMessage
	if quantization != "" {
		prefs, _ = json.Marshal(map[string]string{"quantizations": quantization})
	}
	p, err := importers.PreviewImport(rf.URI, details, prefs)
	if err != nil {
		out.ImportError = err.Error()
		return &out
	}
	out.Backend = p.Backend
	out.ImportFiles = p.Files
	return &out
}

var (
	sharedOnce sync.Once
	shared     *modelsearch.Inspector
)

// Shared returns the process-wide Inspector. Its quantization rule is the
// importer's own, so a Variant.Preference selects what the import will pick.
func Shared() *modelsearch.Inspector {
	sharedOnce.Do(func() {
		shared = modelsearch.NewInspector(modelsearch.InspectorOptions{QuantMatches: importers.QuantMatches})
	})
	return shared
}
