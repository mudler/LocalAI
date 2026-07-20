package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is the subset of a gallery file entry the proposer reads.
type File struct {
	Filename string `yaml:"filename"`
	URI      string `yaml:"uri"`
	SHA256   string `yaml:"sha256"`
}

// VariantRef mirrors the gallery's variant reference.
type VariantRef struct {
	Model string `yaml:"model"`
}

// GalleryEntry is one gallery entry, carrying both the semantics the heuristics need
// and the text range the editor needs.
//
// The two views are kept together deliberately. The editor must not round-trip
// the index through a YAML marshaller: the gallery is 40,000 lines and a
// reflowed diff cannot be reviewed, which defeats the entire point of a job
// whose output is a human decision.
type GalleryEntry struct {
	Name       string         `yaml:"name"`
	URL        string         `yaml:"url"`
	ConfigFile map[string]any `yaml:"config_file"`
	Overrides  map[string]any `yaml:"overrides"`
	Files      []File         `yaml:"files"`
	Variants   []VariantRef   `yaml:"variants"`

	// Index is the entry's position in gallery order.
	Index int `yaml:"-"`
	// StartLine and EndLine bound the entry's lines, zero based and half open.
	StartLine int `yaml:"-"`
	EndLine   int `yaml:"-"`
	// AnchorName is set when the entry defines a YAML anchor. Adding a variants
	// key to such an entry is inherited by everything that merges it, which is
	// why proposals involving anchors get special treatment.
	AnchorName string `yaml:"-"`
	// MergesFrom is the anchor this entry pulls in with "!!merge <<:".
	MergesFrom string `yaml:"-"`
}

// Index is a parsed gallery index: entries plus the exact lines they came from.
type Index struct {
	Lines   []string
	Entries []*GalleryEntry
}

var (
	entryStart  = regexp.MustCompile(`^-(?: |$)`)
	anchorStart = regexp.MustCompile(`^- &(\S+)`)
	mergeStart  = regexp.MustCompile(`^- !!merge <<: \*(\S+)`)
)

// LoadIndex reads and parses a gallery index file.
func LoadIndex(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseIndex(string(data))
}

// ParseIndex builds an Index from the raw text of a gallery index.
//
// The YAML decode and the textual scan are cross checked against each other: if
// they disagree on how many entries there are, every line number the editor
// would use is suspect, so the run fails rather than editing the wrong entry.
func ParseIndex(text string) (*Index, error) {
	var entries []*GalleryEntry
	if err := yaml.Unmarshal([]byte(text), &entries); err != nil {
		return nil, fmt.Errorf("decoding gallery index: %w", err)
	}

	lines := strings.Split(text, "\n")
	var starts []int
	for i, line := range lines {
		if entryStart.MatchString(line) {
			starts = append(starts, i)
		}
	}
	if len(starts) != len(entries) {
		return nil, fmt.Errorf("gallery index has %d decoded entries but %d top level list items; refusing to edit by line number", len(entries), len(starts))
	}

	for i, e := range entries {
		if e == nil {
			return nil, fmt.Errorf("gallery index list item %d is empty; refusing to edit by line number", i)
		}
		e.Index = i
		e.StartLine = starts[i]
		if i+1 < len(starts) {
			e.EndLine = starts[i+1]
		} else {
			e.EndLine = len(lines)
		}
		if m := anchorStart.FindStringSubmatch(lines[e.StartLine]); m != nil {
			e.AnchorName = m[1]
		}
		if m := mergeStart.FindStringSubmatch(lines[e.StartLine]); m != nil {
			e.MergesFrom = m[1]
		}
	}

	return &Index{Lines: lines, Entries: entries}, nil
}

// MergeChildren lists the entries that pull in the given anchor.
func (ix *Index) MergeChildren(anchor string) []*GalleryEntry {
	var out []*GalleryEntry
	for _, e := range ix.Entries {
		if e.MergesFrom == anchor {
			out = append(out, e)
		}
	}
	return out
}

// ByName indexes entries by lowercased name. A name appearing twice keeps the
// first occurrence, matching the gallery's own first-match-wins resolution, and
// the duplicates are returned so the caller can refuse to touch them: a
// proposal naming an ambiguous entry cannot be reviewed.
func (ix *Index) ByName() (map[string]*GalleryEntry, map[string]int) {
	byName := make(map[string]*GalleryEntry, len(ix.Entries))
	counts := make(map[string]int, len(ix.Entries))
	for _, e := range ix.Entries {
		key := strings.ToLower(e.Name)
		counts[key]++
		if _, seen := byName[key]; !seen {
			byName[key] = e
		}
	}
	dupes := map[string]int{}
	for name, n := range counts {
		if n > 1 {
			dupes[name] = n
		}
	}
	return byName, dupes
}

// Installable reports whether installing this entry would put anything on disk.
// A variant target that installs nothing is a dead end for the selector, so it
// is never proposed as one.
func (e *GalleryEntry) Installable() bool {
	return e.URL != "" || len(e.ConfigFile) > 0 || len(e.Overrides) > 0 || len(e.Files) > 0
}

// HasVariants reports whether the entry already offers builds of its own. Such
// an entry cannot be a variant target: nesting is what the gallery's own
// resolution refuses.
func (e *GalleryEntry) HasVariants() bool {
	return len(e.Variants) > 0
}

// auxiliaryFile matches the shared side files that several unrelated models
// legitimately hand out the same copy of. Grouping on one of these is how an
// earlier sweep linked four wan-2.1 entries to each other and Z-Image-Turbo to
// qwen3-4b: they shared a text encoder, not weights.
var auxiliaryFile = regexp.MustCompile(`(?i)(mmproj|vae|clip|t5|umt5|text_?encoder|tokenizer|\bae\b|^ae\.|scheduler|config)`)

// IsAuxiliaryFile reports whether a filename is a side file rather than the
// model's own weights.
func IsAuxiliaryFile(filename string) bool {
	base := filename
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	return auxiliaryFile.MatchString(base)
}

// PrimaryWeightFile returns the filename of the entry's own weights, and
// whether one could be identified unambiguously.
//
// The declared overrides.parameters.model wins because that is the file the
// backend is actually pointed at. Falling back to the file list only works when
// exactly one non-auxiliary file is present; anything else is ambiguous, and
// guessing is precisely the failure mode this heuristic has already had.
func (e *GalleryEntry) PrimaryWeightFile() (string, bool) {
	if params, ok := e.Overrides["parameters"].(map[string]any); ok {
		if model, ok := params["model"].(string); ok && model != "" && !IsAuxiliaryFile(model) {
			return model, true
		}
	}
	var candidates []string
	for _, f := range e.Files {
		if f.Filename == "" || IsAuxiliaryFile(f.Filename) {
			continue
		}
		candidates = append(candidates, f.Filename)
	}
	if len(candidates) == 1 {
		return candidates[0], true
	}
	return "", false
}

// SourceRepo returns the upstream repository the entry's files come from, as a
// coarse "host + owner + repo" key.
func (e *GalleryEntry) SourceRepo() string {
	for _, f := range e.Files {
		if f.URI == "" {
			continue
		}
		return repoKey(f.URI)
	}
	return ""
}

func repoKey(uri string) string {
	u := strings.ToLower(uri)
	u = strings.TrimPrefix(u, "huggingface://")
	u = strings.TrimPrefix(u, "https://huggingface.co/")
	u = strings.TrimPrefix(u, "http://huggingface.co/")
	parts := strings.Split(u, "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return u
}

// SameInstallPayload reports whether two entries install byte for byte the same
// thing.
//
// Entries like this are aliases, not variants. whisper-1 exists so a client
// speaking the OpenAI API can send that name and get whisper-base; folding it
// under whisper-base as a variant would hide the very name clients send.
func SameInstallPayload(a, b *GalleryEntry) bool {
	if a.URL != b.URL {
		return false
	}
	if !sameYAML(a.Overrides, b.Overrides) || !sameYAML(a.ConfigFile, b.ConfigFile) {
		return false
	}
	return sameChecksums(a.Files, b.Files)
}

func sameChecksums(a, b []File) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	ha := make([]string, 0, len(a))
	hb := make([]string, 0, len(b))
	for _, f := range a {
		if f.SHA256 == "" {
			return false
		}
		ha = append(ha, f.SHA256)
	}
	for _, f := range b {
		if f.SHA256 == "" {
			return false
		}
		hb = append(hb, f.SHA256)
	}
	sort.Strings(ha)
	sort.Strings(hb)
	for i := range ha {
		if ha[i] != hb[i] {
			return false
		}
	}
	return true
}

func sameYAML(a, b any) bool {
	ba, err := yaml.Marshal(a)
	if err != nil {
		return false
	}
	bb, err := yaml.Marshal(b)
	if err != nil {
		return false
	}
	return string(ba) == string(bb)
}
