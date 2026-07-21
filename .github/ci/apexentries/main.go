// Command apexentries generates gallery entries for the mudler APEX GGUF
// repositories: one parent entry per model carrying a variants list, plus one
// child entry per imatrix tier, per unsloth quant rung, and per speculative
// build.
//
// Builds are discovered by inspecting the filenames a repo actually publishes.
// Repo names do not reliably predict them: mudler/gemma-4-26B-A4B-it-APEX-GGUF
// ships gemma-4-26B-A4B-APEX-*.gguf, and three other repos drop a suffix or a
// vendor prefix in the same way.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// entryTemplate carries no backend and no parameters of its own, which is
	// why RenderChild states everything inline.
	entryTemplate = "virtual.yaml"
	unslothOwner  = "unsloth"
	authorListURL = "https://huggingface.co/api/models?author=mudler&limit=300"
)

// rungRank orders the quality ladder from best to smallest. The HuggingFace API
// returns siblings alphabetically and DiscoverAPEXTiers preserves that order, so
// an unsorted variants list reads I-Balanced, I-Compact, I-Mini, I-Nano,
// I-Quality. Selection ignores authored order, so this is purely so the file a
// human reviews scans in a meaningful sequence.
var rungRank = map[string]int{
	"I-Quality": 0, "I-Balanced": 1, "I-Compact": 2, "I-Mini": 3, "I-Nano": 4,
	"Quality": 5, "Balanced": 6, "Compact": 7, "Mini": 8, "Nano": 9,
}

// baseTags are the tags every generated entry carries. dflash and mtp are never
// among them: RenderChild adds those if and only if the entry configures the
// matching spec_type.
var baseTags = []string{"llm", "gguf", "cpu", "gpu"}

// childBuild pairs a rendered entry with its position on the quality ladder, so
// the parent's variants list can be sorted without re-parsing entry names.
type childBuild struct {
	entry GalleryEntry
	rank  int
}

// family is one APEX repo's full generated output.
type family struct {
	repo     string
	parent   GalleryEntry
	children []childBuild
}

func main() {
	verify := flag.String("verify", "", "verify a gallery index and exit")
	index := flag.String("index", "gallery/index.yaml", "gallery index to dedup against")
	only := flag.String("only", "", "comma-separated repo names to restrict generation to")
	out := flag.String("out", "", "write the entries to add to this file")
	apply := flag.Bool("apply", false, "append the entries to add to -index")
	flag.Parse()

	if *verify != "" {
		problems := Verify(*verify)
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, p)
		}
		if len(problems) > 0 {
			fmt.Fprintf(os.Stderr, "%d problem(s)\n", len(problems))
			os.Exit(1)
		}
		fmt.Println("index is sound")
		return
	}

	if err := generate(*index, *only, *out, *apply); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func generate(indexPath, only, outPath string, apply bool) error {
	if outPath == "" && !apply {
		return fmt.Errorf("nothing to do: pass -out <file> or -apply")
	}

	client := newHTTPClient()

	repos, err := listAPEXRepos(client)
	if err != nil {
		return err
	}
	if only != "" {
		repos = restrict(repos, only)
	}
	if len(repos) == 0 {
		return fmt.Errorf("no APEX repos selected")
	}
	fmt.Printf("repos selected: %d\n", len(repos))

	var families []family
	var failed []string

	for _, repo := range repos {
		f, err := buildFamily(client, repo)
		if err != nil {
			// A missing sha256 is fatal for the family rather than skippable: an
			// entry without one ships an unverifiable download. Report which repo
			// and keep going, so one bad repo does not hide the state of the rest.
			fmt.Fprintf(os.Stderr, "FAILED %s: %v\n", repo, err)
			failed = append(failed, repo)
			continue
		}
		families = append(families, *f)
	}

	existing, err := LoadExisting(indexPath)
	if err != nil {
		return err
	}
	fmt.Printf("existing index: %d names, %d weight URIs\n", len(existing.ByName), len(existing.ByURI))

	// Children first, then parents, in one Merge call so the batch dedups against
	// itself across both kinds.
	var generated []GalleryEntry
	for _, f := range families {
		for _, c := range f.children {
			generated = append(generated, c.entry)
		}
	}
	parentStart := len(generated)
	for _, f := range families {
		generated = append(generated, f.parent)
	}

	add, reused := Merge(existing, generated)
	reportReuse(existing, generated, reused)

	// Variant references are resolved from `reused`, never used to decide what to
	// emit: on a within-batch name collision Merge records reused[name] = name
	// while the first entry of that name is still in `add`, so treating presence
	// in `reused` as "dropped" would silently emit nothing for it.
	added := map[string]bool{}
	for _, e := range add {
		added[e.Name] = true
	}
	for i := range add {
		for j, v := range add[i].Variants {
			add[i].Variants[j].Model = resolveVariant(v.Model, reused, added)
		}
	}

	reportDroppedParents(generated[parentStart:], reused, added)

	fmt.Printf("\nentries generated: %d\nentries to add:    %d\nentries reused:    %d\n",
		len(generated), len(add), len(reused))

	if err := writeEntries(add, outPath, apply, indexPath); err != nil {
		return err
	}

	if len(failed) > 0 {
		return fmt.Errorf("%d repo(s) failed: %s", len(failed), strings.Join(failed, ", "))
	}
	return nil
}

// resolveVariant maps a generated child name onto whatever entry actually stands
// for it after the merge. `added` is consulted first because a within-batch name
// collision puts a name in BOTH add and reused, and the entry that was emitted
// is the one the parent must reference.
func resolveVariant(name string, reused map[string]string, added map[string]bool) string {
	if added[name] {
		return name
	}
	if target, ok := reused[name]; ok {
		return target
	}
	return name
}

// buildFamily discovers everything one APEX repo and its unsloth counterpart
// publish, and renders it.
func buildFamily(client *http.Client, repo string) (*family, error) {
	files, err := FetchRepoFiles(client, repo)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no gguf files")
	}

	imatrix, plain := DiscoverAPEXTiers(files)
	mmproj, hasMMProj := DiscoverMMProj(files)

	reportUnclassified(repo, files, imatrix, plain)

	// The imatrix ladder is preferred, but two of the 45 repos publish no
	// imatrix tiers at all and must still contribute their plain ladder.
	ladder := imatrix
	ladderKind := "imatrix"
	if len(ladder) == 0 {
		ladder = plain
		ladderKind = "plain"
	}
	if len(ladder) == 0 {
		return nil, fmt.Errorf("no tiers discovered")
	}

	sortTiers(ladder)

	var mm *GGUFFile
	if hasMMProj {
		mm = &mmproj
	}

	repoBase := strings.TrimSuffix(path.Base(repo), "-GGUF")
	f := &family{repo: repo}

	for _, t := range ladder {
		f.children = append(f.children, childBuild{
			rank: rungRank[t.Label],
			entry: RenderChild(ChildInput{
				Name:     slug(repoBase) + "-" + slug(t.Label),
				Repo:     repo,
				Template: entryTemplate,
				Weights:  []GGUFFile{t.File},
				MMProj:   mm,
				BaseTags: baseTags,
			}),
		})
	}

	stem := FileStem(ladder[0])
	fmt.Printf("%s: %d %s tier(s) [%s], stem %s, mmproj %v\n",
		repo, len(ladder), ladderKind, tierLabels(ladder), stem, hasMMProj)

	counterpart, cpFiles, err := resolveCounterpart(client, repoBase, stem)
	if err != nil {
		return nil, err
	}
	if counterpart != "" {
		builds := DiscoverUnslothQuants(cpFiles)

		// Called here rather than inside Verify: a quant dropped at discovery
		// leaves no trace at all in the finished gallery file, so the only place
		// the shortfall is still visible is the moment of discovery.
		for _, p := range UnaccountedQuants(cpFiles, builds) {
			fmt.Fprintf(os.Stderr, "UNACCOUNTED QUANT %s: %s\n", counterpart, p)
		}

		cpMMProj, hasCPMMProj := DiscoverMMProj(cpFiles)
		var cpMM *GGUFFile
		if hasCPMMProj {
			cpMM = &cpMMProj
		}
		cpBase := strings.TrimSuffix(path.Base(counterpart), "-GGUF")

		for i, b := range builds {
			f.children = append(f.children, childBuild{
				rank: 100 + i,
				entry: RenderChild(ChildInput{
					Name:     slug(cpBase) + "-" + slug(b.Quant),
					Repo:     counterpart,
					Template: entryTemplate,
					Weights:  b.Files,
					MMProj:   cpMM,
					BaseTags: baseTags,
				}),
			})
		}
		fmt.Printf("%s: counterpart %s, %d quant build(s) %s\n", repo, counterpart, len(builds), quantLabels(builds))
	} else {
		fmt.Printf("%s: no unsloth counterpart\n", repo)
	}

	f.parent = renderParent(repo, repoBase, f.children, hasMMProj)
	return f, nil
}

// renderParent builds the family root. It carries no overrides, so it must carry
// no dflash/mtp tag either: the verifier skips entries that declare no backend,
// and a feature tag there would escape the tagging check entirely.
func renderParent(repo, repoBase string, children []childBuild, hasMMProj bool) GalleryEntry {
	sorted := append([]childBuild{}, children...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].rank < sorted[j].rank })

	tags := append([]string{}, baseTags...)
	if hasMMProj {
		tags = append(tags, "vision")
	}

	e := GalleryEntry{
		Name:        slug(repoBase),
		URL:         fmt.Sprintf("github:mudler/LocalAI/gallery/%s@master", entryTemplate),
		Description: fmt.Sprintf("%s. Quality ladder and quantization rungs published by %s and its unsloth counterpart; LocalAI picks the build that fits the hardware.", repoBase, repo),
		Tags:        tags,
	}
	for _, c := range sorted {
		e.Variants = append(e.Variants, VariantRef{Model: c.entry.Name})
	}
	return e
}

// resolveCounterpart probes the unsloth candidates in order and returns the
// first that publishes files.
//
// CounterpartCandidates is handed a BARE repo name: its cleaner does not strip
// an owner prefix, so passing "mudler/Foo-APEX-GGUF" would yield "mudler/Foo"
// and compose into the nonsense probe "unsloth/mudler/Foo".
func resolveCounterpart(client *http.Client, repoBase, stem string) (string, []GGUFFile, error) {
	for _, cand := range CounterpartCandidates(repoBase, stem) {
		repo := unslothOwner + "/" + cand + "-GGUF"
		files, err := FetchRepoFiles(client, repo)
		if err != nil {
			return "", nil, fmt.Errorf("probing %s: %w", repo, err)
		}
		if len(files) > 0 {
			return repo, files, nil
		}
	}
	return "", nil, nil
}

// reportUnclassified prints the files discovery turned into nothing.
//
// It is a set difference on COUNTS, not a re-match of filenames: re-matching
// would duplicate the tier regex from discover.go and the two copies would
// drift. The likeliest trigger is a typo or case change from a publishing script
// rather than a genuine sixth tier, and because generation falls back to the
// plain ladder when the imatrix one is empty, a repo whose imatrix files all
// fail to match silently downgrades the whole family instead of erroring. The
// downstream HTTP check cannot catch that: it validates URLs that were emitted,
// and an undiscovered tier emits none.
func reportUnclassified(repo string, files []GGUFFile, imatrix, plain []Tier) {
	mmprojCount := 0
	for _, f := range files {
		if strings.HasPrefix(f.Name, "mmproj") {
			mmprojCount++
		}
	}

	classified := len(imatrix) + len(plain) + mmprojCount
	if classified < len(files) {
		fmt.Fprintf(os.Stderr, "UNCLASSIFIED %s: %d of %d .gguf files classified, %d unaccounted for\n",
			repo, classified, len(files), len(files)-classified)
	}
}

// reportReuse splits Merge's single reused map into the two cases it conflates.
//
// A URI match means the gallery already ships exactly these weights, and
// pointing the parent at the existing entry is correct. A NAME match with a
// different URI means an unrelated entry happens to own the name, and
// referencing it would point the parent at different weights than were
// generated, substituting a build without saying so. Only the first is safe to
// wave through.
func reportReuse(existing *ExistingIndex, generated []GalleryEntry, reused map[string]string) {
	byName := map[string]GalleryEntry{}
	for _, e := range generated {
		if _, seen := byName[e.Name]; !seen {
			byName[e.Name] = e
		}
	}

	var nameCollisions, uriMatches []string
	for name, target := range reused {
		gen := byName[name]
		uri := ""
		if len(gen.Files) > 0 {
			uri = gen.Files[0].URI
		}

		switch {
		case hasName(existing, name):
			nameCollisions = append(nameCollisions,
				fmt.Sprintf("  %s -> gallery entry of the same name (generated uri: %s)", name, orNone(uri)))
		case target == name:
			nameCollisions = append(nameCollisions,
				fmt.Sprintf("  %s -> earlier entry of the same name in this batch (generated uri: %s)", name, orNone(uri)))
		default:
			uriMatches = append(uriMatches, fmt.Sprintf("  %s -> %s (same weights: %s)", name, target, orNone(uri)))
		}
	}
	sort.Strings(nameCollisions)
	sort.Strings(uriMatches)

	fmt.Printf("\nNAME COLLISIONS (%d) - inspect each by hand, the target may hold different weights\n", len(nameCollisions))
	for _, l := range nameCollisions {
		fmt.Println(l)
	}
	fmt.Printf("\nURI MATCHES (%d) - the gallery or this batch already ships these exact weights\n", len(uriMatches))
	for _, l := range uriMatches {
		fmt.Println(l)
	}
}

// reportDroppedParents prints the variants list of every parent that lost its
// name to an existing entry. Without this the family's grouping would vanish
// from the run with nothing said, and a name collision on a parent is exactly
// the case a human has to resolve by hand.
func reportDroppedParents(parents []GalleryEntry, reused map[string]string, added map[string]bool) {
	var dropped []GalleryEntry
	for _, p := range parents {
		if !added[p.Name] {
			dropped = append(dropped, p)
		}
	}
	if len(dropped) == 0 {
		return
	}

	fmt.Printf("\nPARENTS NOT EMITTED (%d) - the gallery already owns the name; the intended variants list follows\n", len(dropped))
	for _, p := range dropped {
		fmt.Printf("  %s:\n", p.Name)
		for _, v := range p.Variants {
			fmt.Printf("    - model: %s\n", resolveVariant(v.Model, reused, added))
		}
	}
}

func hasName(ix *ExistingIndex, name string) bool {
	_, ok := ix.ByName[name]
	return ok
}

func orNone(s string) string {
	if s == "" {
		return "(no files)"
	}
	return s
}

// writeEntries emits the additions. -apply appends rather than rewriting: the
// index is 40,000 lines and a YAML round trip would reflow the whole file into
// an unreviewable diff.
func writeEntries(add []GalleryEntry, outPath string, apply bool, indexPath string) error {
	if len(add) == 0 {
		fmt.Println("\nnothing to write")
		return nil
	}

	blob, err := yaml.Marshal(add)
	if err != nil {
		return err
	}

	if outPath != "" {
		if err := os.WriteFile(outPath, blob, 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %d entries to %s\n", len(add), outPath)
	}

	if apply {
		f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Write(blob); err != nil {
			return err
		}
		fmt.Printf("appended %d entries to %s\n", len(add), indexPath)
	}
	return nil
}

// listAPEXRepos returns the mudler repos whose name marks them as APEX builds.
func listAPEXRepos(client *http.Client) ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, authorListURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "localai-apexentries/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing models: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var models []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &models); err != nil {
		return nil, fmt.Errorf("decoding model list: %w", err)
	}

	var out []string
	for _, m := range models {
		if strings.Contains(m.ID, "APEX") {
			out = append(out, m.ID)
		}
	}
	sort.Strings(out)
	return out, nil
}

func restrict(repos []string, only string) []string {
	want := map[string]bool{}
	for _, r := range strings.Split(only, ",") {
		if r = strings.TrimSpace(r); r != "" {
			want[r] = true
		}
	}

	var out []string
	for _, r := range repos {
		if want[r] {
			out = append(out, r)
			delete(want, r)
		}
	}
	// A name in -only that matched nothing is a typo, not an empty result.
	for r := range want {
		fmt.Fprintf(os.Stderr, "WARNING: -only names %s, which is not an APEX repo of this author\n", r)
	}
	return out
}

func sortTiers(tiers []Tier) {
	sort.SliceStable(tiers, func(i, j int) bool { return rungRank[tiers[i].Label] < rungRank[tiers[j].Label] })
}

func tierLabels(tiers []Tier) string {
	var out []string
	for _, t := range tiers {
		out = append(out, t.Label)
	}
	return strings.Join(out, ",")
}

func quantLabels(builds []QuantBuild) string {
	var out []string
	for _, b := range builds {
		l := b.Quant
		if b.Sharded {
			l += fmt.Sprintf("(%d shards)", len(b.Files))
		}
		out = append(out, l)
	}
	return strings.Join(out, ",")
}

// slug turns a repo, tier or quant label into a gallery entry name component.
func slug(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "_", "-")
}
