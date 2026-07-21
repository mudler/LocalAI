// Command apexentries generates gallery entries for the mudler APEX GGUF
// repositories: one entry per imatrix tier, per unsloth quant rung and per
// speculative build, all gathered under the BASE model's entry.
//
// The base model entry is the hub. Somebody looking for qwen3.6-35b-a3b must
// find every build of those weights under that one name, so when the gallery
// already ships the base entry this command splices a variants block into it
// rather than emitting a competing *-apex parent beside it. Only a family whose
// base model the gallery does not ship at all gets a new hub entry, and that one
// is still named for the base model.
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

	"github.com/mudler/LocalAI/.github/ci/galleryedit"
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
	repo      string
	repoBase  string
	stem      string
	hasMMProj bool
	children  []childBuild

	// skippedRepos are counterpart candidates HuggingFace would not describe.
	// Carried on the family rather than printed and forgotten so the run can
	// summarize them next to everything else a reviewer has to eyeball.
	skippedRepos []string
	census       fileCensus
	unaccounted  int
}

// fileCensus splits the files discovery emitted nothing for into the ones a
// reviewer must chase and the ones that are deliberately out of scope.
//
// Full-precision sources are the second kind: they are the unquantized weights
// the ladder is derived FROM, not a rung of it. Folding them into the
// unclassified total would leave a permanent benign baseline, and a permanent
// baseline is exactly what hides the one file that ever genuinely matters.
type fileCensus struct {
	unclassified  int
	fullPrecision int
}

// add accumulates one repo's census into a running total.
func (c *fileCensus) add(o fileCensus) {
	c.unclassified += o.unclassified
	c.fullPrecision += o.fullPrecision
}

// sortedChildren returns the family's builds in ladder order, best first.
func (f *family) sortedChildren() []childBuild {
	sorted := append([]childBuild{}, f.children...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].rank < sorted[j].rank })
	return sorted
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
	ixText, err := LoadIndexText(indexPath)
	if err != nil {
		return err
	}
	fmt.Printf("existing index: %d names, %d weight URIs, %d lines\n",
		len(existing.ByName), len(existing.ByURI), len(ixText.Lines))

	// Only the builds go through Merge. A hub is deliberately kept out of it: a
	// new hub carries the family's top rung as its own payload, so Merge's URI
	// dedup would fold the hub into that rung and the family would lose the very
	// entry point this command exists to create. Hub names are checked against
	// the index directly, by ResolveHub.
	var generated []GalleryEntry
	for _, f := range families {
		for _, c := range f.children {
			generated = append(generated, c.entry)
		}
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

	inserts, newHubs, err := planHubs(families, ixText, reused, added)
	if err != nil {
		return err
	}
	reportHubs(ixText, inserts, newHubs)

	skipped, census, fullPrecisionRepos, unaccounted := reportSkipped(families)

	add = append(add, newHubs...)

	fmt.Printf("\nentries generated: %d\nentries to add:    %d\nentries reused:    %d\nhubs spliced:      %d\nhubs created:      %d\nrepos skipped:     %d\nexcluded (full precision): %d files across %d repos\nunclassified:      %d\nunaccounted:       %d\n",
		len(generated), len(add), len(reused), len(inserts), len(newHubs), len(skipped),
		census.fullPrecision, fullPrecisionRepos, census.unclassified, unaccounted)

	lines, err := galleryedit.Apply(ixText.Lines, inserts)
	if err != nil {
		return err
	}

	if err := writeEntries(add, lines, outPath, apply, indexPath); err != nil {
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

	census := reportUnclassified(repo, files, imatrix, plain)

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
	f := &family{repo: repo, repoBase: repoBase, hasMMProj: hasMMProj, census: census}

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
	f.stem = stem
	fmt.Printf("%s: %d %s tier(s) [%s], stem %s, mmproj %v\n",
		repo, len(ladder), ladderKind, tierLabels(ladder), stem, hasMMProj)

	counterpart, cpFiles, skipped, err := resolveCounterpart(client, repoBase, stem)
	f.skippedRepos = skipped
	if err != nil {
		return nil, err
	}
	if counterpart != "" {
		builds := DiscoverUnslothQuants(cpFiles)

		// Called here rather than inside Verify: a quant dropped at discovery
		// leaves no trace at all in the finished gallery file, so the only place
		// the shortfall is still visible is the moment of discovery.
		unaccounted := UnaccountedQuants(cpFiles, builds)
		f.unaccounted = len(unaccounted)
		for _, p := range unaccounted {
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

	return f, nil
}

// planHubs decides, per family, whether the family's builds are spliced into a
// base model entry the gallery already ships or gathered under a new hub.
//
// Splicing is strongly preferred and is the measured majority-adjacent case. The
// existing entry keeps its description, icon, tags, overrides and files
// untouched; only variant lines are added to it.
func planHubs(families []family, ix *IndexText, reused map[string]string, added map[string]bool) ([]galleryedit.Insert, []GalleryEntry, error) {
	// Several APEX repos can resolve to one base model, so both paths accumulate
	// by hub name rather than assuming one family per hub.
	wantByHub := map[string][]string{}
	var spliceOrder []string

	var newHubs []GalleryEntry
	hubAt := map[string]int{}

	for i := range families {
		f := &families[i]

		hubName, exists := ResolveHub(ix, f.repoBase, f.stem)
		want := hubVariants(f, ix, reused, added)

		if exists {
			if _, seen := wantByHub[hubName]; !seen {
				spliceOrder = append(spliceOrder, hubName)
			}
			wantByHub[hubName] = append(wantByHub[hubName], want...)
			continue
		}

		if at, dup := hubAt[hubName]; dup {
			for _, v := range filterVariants(hubName, newHubs[at].Variants, want) {
				newHubs[at].Variants = append(newHubs[at].Variants, VariantRef{Model: v})
			}
			continue
		}

		builds := f.sortedChildren()
		if len(builds) == 0 {
			return nil, nil, fmt.Errorf("%s: no builds to hang a hub on", f.repo)
		}
		hubAt[hubName] = len(newHubs)
		newHubs = append(newHubs, renderHub(hubName, f, builds[0], filterVariants(hubName, nil, want)))
	}

	var inserts []galleryedit.Insert
	for _, name := range spliceOrder {
		e := ix.Find(name)
		items := filterVariants(name, e.Variants, wantByHub[name])
		if len(items) == 0 {
			continue
		}
		inserts = append(inserts, galleryedit.Insert{Entry: e.Pos, Variants: items})
	}
	return inserts, newHubs, nil
}

// hubVariants is a family's full build list, in ladder order, named as the hub
// must reference them after the merge.
func hubVariants(f *family, ix *IndexText, reused map[string]string, added map[string]bool) []string {
	var out []string

	// A hand-written *-apex entry is an ordinary build of these weights. It is
	// never deleted, never renamed and never treated as a hub; it is simply
	// referenced like any other rung.
	if apex := slug(f.repoBase); ix.Find(apex) != nil {
		out = append(out, apex)
	}
	for _, c := range f.sortedChildren() {
		out = append(out, resolveVariant(c.entry.Name, reused, added))
	}
	return out
}

// renderHub builds the hub for a family whose base model the gallery does not
// ship at all. It is named for the BASE model, never for the APEX repo.
//
// It carries one of the discovered builds as its own payload so it is a complete
// installable entry rather than a bare index pointing at other entries. That
// payload is what supplies overrides.backend, which matters beyond installation:
// the verifier can only judge the tagging rule for a backend it can read, so a
// hub carrying feature tags and no backend would escape the check in silence.
//
// The payload's own tags are kept rather than rebuilt from baseTags, so a hub
// whose payload configures a spec_type stays tagged for it and consistent with
// the overrides copied alongside.
func renderHub(name string, f *family, payload childBuild, variants []string) GalleryEntry {
	e := payload.entry
	e.Name = name
	e.Description = fmt.Sprintf(
		"%s. Quality ladder and quantization rungs published by %s and its unsloth counterpart; LocalAI picks the build that fits the hardware.",
		HubLabel(f.repoBase, f.stem), f.repo)

	e.Tags = append([]string{}, payload.entry.Tags...)
	if f.hasMMProj && !hasTag(e.Tags, "vision") {
		e.Tags = append(e.Tags, "vision")
	}

	e.Variants = nil
	for _, v := range variants {
		e.Variants = append(e.Variants, VariantRef{Model: v})
	}
	return e
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// resolveCounterpart probes the unsloth candidates in order and returns the
// first that publishes files.
//
// CounterpartCandidates is handed a BARE repo name: its cleaner does not strip
// an owner prefix, so passing "mudler/Foo-APEX-GGUF" would yield "mudler/Foo"
// and compose into the nonsense probe "unsloth/mudler/Foo".
//
// It also returns the candidates HuggingFace refused to describe. Those are
// indistinguishable from absent without credentials, so they are skipped, but
// they are named rather than dropped: one of them could be a real gated repo
// whose quants belong in the gallery.
func resolveCounterpart(client *http.Client, repoBase, stem string) (string, []GGUFFile, []string, error) {
	var unavailable []string
	for _, cand := range CounterpartCandidates(repoBase, stem) {
		repo := unslothOwner + "/" + cand + "-GGUF"
		files, unreadable, err := FetchOptionalRepoFiles(client, repo)
		if err != nil {
			return "", nil, unavailable, fmt.Errorf("probing %s: %w", repo, err)
		}
		if unreadable {
			unavailable = append(unavailable, repo)
			continue
		}
		if len(files) > 0 {
			return repo, files, unavailable, nil
		}
	}
	return "", nil, unavailable, nil
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
// It returns the census so the run can total it.
func reportUnclassified(repo string, files []GGUFFile, imatrix, plain []Tier) fileCensus {
	mmprojCount, fullPrecision := 0, 0
	for _, f := range files {
		// The mmproj test comes first because projectors are themselves often
		// published at f16 (mmproj-F16.gguf), and counting such a file in both
		// buckets would understate the unclassified remainder.
		if strings.HasPrefix(f.Name, "mmproj") {
			mmprojCount++
			continue
		}
		if IsFullPrecision(f.Name) {
			fullPrecision++
		}
	}

	classified := len(imatrix) + len(plain) + mmprojCount + fullPrecision
	if classified >= len(files) {
		return fileCensus{fullPrecision: fullPrecision}
	}
	fmt.Fprintf(os.Stderr, "UNCLASSIFIED %s: %d of %d .gguf files classified, %d unaccounted for\n",
		repo, classified, len(files), len(files)-classified)
	return fileCensus{unclassified: len(files) - classified, fullPrecision: fullPrecision}
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

// reportHubs prints exactly what will be written where. The splices are the part
// a human has to read: they modify entries the gallery already ships, so the
// review needs the target, the line, and every added reference spelled out.
func reportHubs(ix *IndexText, inserts []galleryedit.Insert, newHubs []GalleryEntry) {
	fmt.Printf("\nHUBS SPLICED (%d) - variants added to the EXISTING base model entry, nothing else touched\n", len(inserts))
	for _, in := range inserts {
		e := ix.Find(in.Entry.Name)
		fmt.Printf("  %s (line %d, %d variant(s) already declared):\n", in.Entry.Name, in.Entry.StartLine+1, len(e.Variants))
		for _, v := range in.Variants {
			fmt.Printf("    + - model: %s\n", galleryedit.QuoteName(v))
		}
	}

	fmt.Printf("\nHUBS CREATED (%d) - the gallery ships no base model entry, so one is emitted for it\n", len(newHubs))
	for _, h := range newHubs {
		fmt.Printf("  %s:\n", h.Name)
		for _, v := range h.Variants {
			fmt.Printf("    - model: %s\n", v.Model)
		}
	}
}

// reportSkipped names the counterpart repos HuggingFace would not describe, and
// totals the other two silent-shortfall counters alongside them.
//
// A skipped repo is not the same as a clean 404. HuggingFace answers 401 for a
// nonexistent repo to an unauthenticated client, so the overwhelmingly likely
// reading is "there is no such counterpart", which is the normal case for the
// community merges. But a private or gated repo answers 401 too, and that one
// WOULD have quants worth shipping. Printing the list is what keeps that
// possibility auditable instead of silently discarded.
func reportSkipped(families []family) ([]string, fileCensus, int, int) {
	var skipped []string
	var census fileCensus
	fullPrecisionRepos, unaccounted := 0, 0
	for _, f := range families {
		skipped = append(skipped, f.skippedRepos...)
		census.add(f.census)
		if f.census.fullPrecision > 0 {
			fullPrecisionRepos++
		}
		unaccounted += f.unaccounted
	}
	sort.Strings(skipped)

	fmt.Printf("\nREPOS SKIPPED AS UNAVAILABLE (%d) - HuggingFace answered 401/403, which is indistinguishable from absent without a token; check none of these is a real gated repo\n", len(skipped))
	for _, r := range skipped {
		fmt.Printf("  %s\n", r)
	}
	return skipped, census, fullPrecisionRepos, unaccounted
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

// writeEntries emits the additions.
//
// -apply does two things in one pass: it writes back the spliced lines, which
// differ from the original only by the variant lines galleryedit inserted, and
// then appends the new entries. New entries are APPENDED rather than merged into
// the structure, for the same reason the splice is textual: a YAML round trip
// over 40,000 lines would reflow the whole file into an unreviewable diff.
func writeEntries(add []GalleryEntry, lines []string, outPath string, apply bool, indexPath string) error {
	if apply {
		if err := os.WriteFile(indexPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return err
		}
		fmt.Printf("spliced %s\n", indexPath)
	}

	if len(add) == 0 {
		fmt.Println("nothing to append")
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
