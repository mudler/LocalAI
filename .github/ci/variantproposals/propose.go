package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Signal names the grouping heuristic that linked two entries.
type Signal string

const (
	// SignalName is "same name once quantization markers are stripped".
	SignalName Signal = "name-modulo-quant"
	// SignalConfigSuffix is the ":" convention, foo:q8_0 as a build of foo.
	SignalConfigSuffix Signal = "config-suffix"
	// SignalWeightFile is "same primary weight filename once quantization
	// markers are stripped", auxiliary files excluded.
	SignalWeightFile Signal = "weight-filename"
)

// Evidence is what a reviewer needs in order to agree or disagree without
// opening HuggingFace: what the two entries share, and what differs.
type Evidence struct {
	Signals     []Signal
	SharedStem  string
	SharedFile  string
	SharedRepo  string
	QuantTokens []string
}

// Proposal is one variant target offered to one parent.
type Proposal struct {
	Variant  string
	Evidence Evidence
}

// Family is a complete proposal: one parent gaining one or more variants.
type Family struct {
	Parent    string
	Proposals []Proposal
}

// Refusal is a family the heuristics found but the rules would not let through.
// Refusals are reported rather than dropped: a candidate the job keeps refusing
// is either a rule worth revisiting or a gallery bug worth fixing.
type Refusal struct {
	Members []string
	Reason  string
}

// Result is one run of the proposer.
type Result struct {
	Families     []Family
	Refusals     []Refusal
	Suppressed   []Suppression
	AliasSkipped []Suppression
}

// HasProposals reports whether the run found anything to open a pull request
// about. A job that opens an empty pull request every night is a job people
// filter out of their inbox.
func (r *Result) HasProposals() bool {
	return len(r.Families) > 0
}

// sizeToken matches a parameter-count marker: 8b, 1.7b, a3b for an active
// expert count, e2b for the Gemma effective sizes, 8x7b for a mixture.
//
// This is a structural rule rather than a ledger entry because it is about the
// shape of the token, not about any one model. Different parameter sizes were
// mis-grouped by an earlier sweep and the failure is systematic.
var sizeToken = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]+)?[bm]|[ae][0-9]+(?:\.[0-9]+)?b|[0-9]+x[0-9]+(?:\.[0-9]+)?b)$`)

func differsByParameterSize(a, b string) (string, bool) {
	for seg := range differingSegments(a, b) {
		if sizeToken.MatchString(seg) {
			return seg, true
		}
	}
	return "", false
}

// genericFileStem lists weight filenames too generic to be evidence of
// anything. Two entries both shipping "model.safetensors" share a convention,
// not a set of weights.
var genericFileStem = map[string]struct{}{
	"model": {}, "weights": {}, "pytorch_model": {}, "diffusion_pytorch_model": {},
	"consolidated": {}, "ggml-model": {}, "model-00001-of-00002": {},
}

// minFileStemLength keeps short, collision-prone filename stems from linking
// unrelated entries.
const minFileStemLength = 6

type pair struct {
	a, b     int
	evidence Evidence
}

// Propose runs the grouping heuristics over a gallery index and returns what it
// would offer a human, what it refused, and what the ledger silenced.
//
// Nothing here touches the network or git, and the index is not modified.
func Propose(ix *Index, ledger *Ledger) *Result {
	if ledger == nil {
		ledger = &Ledger{}
	}
	result := &Result{}

	byName, dupes := ix.ByName()

	// Existing relationships. A target already claimed must not be claimed
	// again, and two entries already in one family need no proposal.
	claimedBy := map[string]string{}
	familyOf := map[string]string{}
	for _, e := range ix.Entries {
		if !e.HasVariants() {
			continue
		}
		familyOf[strings.ToLower(e.Name)] = strings.ToLower(e.Name)
		for _, v := range e.Variants {
			target := strings.ToLower(v.Model)
			if _, taken := claimedBy[target]; !taken {
				claimedBy[target] = strings.ToLower(e.Name)
			}
			familyOf[target] = strings.ToLower(e.Name)
		}
	}

	candidates := map[[2]int]*Evidence{}

	addPair := func(i, j int, sig Signal, apply func(*Evidence)) {
		if i == j {
			return
		}
		if i > j {
			i, j = j, i
		}
		key := [2]int{i, j}
		ev, ok := candidates[key]
		if !ok {
			ev = &Evidence{}
			candidates[key] = ev
		}
		for _, s := range ev.Signals {
			if s == sig {
				apply(ev)
				return
			}
		}
		ev.Signals = append(ev.Signals, sig)
		apply(ev)
	}

	// Signal 1 and 2: entries sharing a name stem.
	byStem := map[string][]int{}
	for _, e := range ix.Entries {
		if e.Name == "" {
			continue
		}
		byStem[NameStem(e.Name)] = append(byStem[NameStem(e.Name)], e.Index)
	}
	for stem, members := range byStem {
		if len(members) < 2 {
			continue
		}
		for i := 0; i < len(members); i++ {
			for j := i + 1; j < len(members); j++ {
				a, b := ix.Entries[members[i]], ix.Entries[members[j]]
				sig := SignalName
				if HasConfigSuffix(a.Name) || HasConfigSuffix(b.Name) {
					sig = SignalConfigSuffix
				}
				// The bare parent carries no marker in its name, so the
				// evidence would read "differs by q8_0" and say nothing about
				// what the parent is. The weight filenames fill that in.
				fa, _ := a.PrimaryWeightFile()
				fb, _ := b.PrimaryWeightFile()
				addPair(members[i], members[j], sig, func(ev *Evidence) {
					ev.SharedStem = stem
					ev.QuantTokens = quantDifference(a.Name, b.Name, fa, fb)
				})
			}
		}
	}

	// Signal 3: entries whose own weight file is the same file at a different
	// quantization. Auxiliary files never take part.
	byFile := map[string][]int{}
	for _, e := range ix.Entries {
		primary, ok := e.PrimaryWeightFile()
		if !ok {
			continue
		}
		stem := FileStem(primary)
		if len(stem) < minFileStemLength {
			continue
		}
		if _, generic := genericFileStem[stem]; generic {
			continue
		}
		byFile[stem] = append(byFile[stem], e.Index)
	}
	for stem, members := range byFile {
		if len(members) < 2 {
			continue
		}
		for i := 0; i < len(members); i++ {
			for j := i + 1; j < len(members); j++ {
				a, b := ix.Entries[members[i]], ix.Entries[members[j]]
				// The filename alone is not evidence. Publishers reuse the
				// upstream filename for finetunes and for models that merely
				// embed the base weights: bert-embeddings, an ultravox audio
				// model and a roleplay finetune all ship a file called
				// llama-3.2-1b-instruct-q4_k_m.gguf. Requiring the same
				// upstream repository turns the signal back into what it
				// claims to be, one repo publishing one file at two
				// quantizations. Two repos holding the same weights is a fact
				// no filename proves, so it stays a human call.
				repo := a.SourceRepo()
				if repo == "" || repo != b.SourceRepo() {
					continue
				}
				fa, _ := a.PrimaryWeightFile()
				fb, _ := b.PrimaryWeightFile()
				addPair(members[i], members[j], SignalWeightFile, func(ev *Evidence) {
					ev.SharedFile = stem
					ev.SharedRepo = repo
					if len(ev.QuantTokens) == 0 {
						ev.QuantTokens = quantDifference(fa, fb)
					}
				})
			}
		}
	}

	// Filter candidates. Everything dropped here is dropped for a reason a
	// reviewer can read back off the ledger or the rules.
	var kept []pair
	for key, ev := range candidates {
		a, b := ix.Entries[key[0]], ix.Entries[key[1]]
		la, lb := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if la == lb {
			continue
		}
		if dupes[la] > 0 || dupes[lb] > 0 {
			result.Refusals = append(result.Refusals, Refusal{
				Members: []string{a.Name, b.Name},
				Reason:  "one of these names appears more than once in the gallery, so a variant reference to it is ambiguous",
			})
			continue
		}
		if fa, fb := familyOf[la], familyOf[lb]; fa != "" && fa == fb {
			continue
		}
		if seg, differs := differsByParameterSize(la, lb); differs {
			result.Suppressed = append(result.Suppressed, Suppression{
				A: a.Name, B: b.Name, Reason: fmt.Sprintf("different parameter sizes (segment %q)", seg),
			})
			continue
		}
		if s, ok := ledger.Suppresses(a.Name, b.Name); ok {
			result.Suppressed = append(result.Suppressed, s)
			continue
		}
		if SameInstallPayload(a, b) {
			result.AliasSkipped = append(result.AliasSkipped, Suppression{
				A: a.Name, B: b.Name,
				Reason: "identical install payload; these are aliases of one build, not alternative builds",
			})
			continue
		}
		kept = append(kept, pair{a: key[0], b: key[1], evidence: *ev})
	}

	sort.Slice(kept, func(i, j int) bool {
		if kept[i].a != kept[j].a {
			return kept[i].a < kept[j].a
		}
		return kept[i].b < kept[j].b
	})

	// Components. A pair from either signal joins the same family, so a chain
	// of alternative builds discovered by different signals stays one family
	// rather than two overlapping ones that would double claim a target.
	parent := map[int]int{}
	var find func(int) int
	find = func(x int) int {
		if p, ok := parent[x]; ok && p != x {
			parent[x] = find(p)
			return parent[x]
		}
		if _, ok := parent[x]; !ok {
			parent[x] = x
		}
		return parent[x]
	}
	union := func(x, y int) {
		rx, ry := find(x), find(y)
		if rx != ry {
			parent[ry] = rx
		}
	}
	evidenceFor := map[[2]int]Evidence{}
	for _, p := range kept {
		union(p.a, p.b)
		evidenceFor[[2]int{p.a, p.b}] = p.evidence
	}

	components := map[int][]int{}
	for _, p := range kept {
		for _, m := range []int{p.a, p.b} {
			root := find(m)
			if !contains(components[root], m) {
				components[root] = append(components[root], m)
			}
		}
	}

	roots := make([]int, 0, len(components))
	for r := range components {
		roots = append(roots, r)
	}
	sort.Ints(roots)

	proposedTargets := map[string]string{}
	for _, root := range roots {
		members := components[root]
		sort.Ints(members)
		family, refusal := buildFamily(ix, members, evidenceFor, claimedBy, proposedTargets, byName)
		if refusal != nil {
			result.Refusals = append(result.Refusals, *refusal)
			continue
		}
		if family == nil {
			continue
		}
		for _, p := range family.Proposals {
			proposedTargets[strings.ToLower(p.Variant)] = family.Parent
		}
		result.Families = append(result.Families, *family)
	}

	sort.Slice(result.Families, func(i, j int) bool { return result.Families[i].Parent < result.Families[j].Parent })
	result.Suppressed = SortedSuppressions(result.Suppressed)
	result.AliasSkipped = SortedSuppressions(result.AliasSkipped)
	result.Refusals = dedupeRefusals(result.Refusals)
	return result
}

// dedupeRefusals collapses the same refusal reached from both orderings of a
// pair, and sorts what is left. A reviewer reading the same complaint twice
// learns to skim the section.
func dedupeRefusals(in []Refusal) []Refusal {
	seen := map[string]struct{}{}
	var out []Refusal
	for _, r := range in {
		members := append([]string(nil), r.Members...)
		sort.Strings(members)
		key := strings.Join(members, "\x00") + "\x00" + r.Reason
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := strings.Join(out[i].Members, ","), strings.Join(out[j].Members, ","); a != b {
			return a < b
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// buildFamily turns a connected component into a proposal, or refuses it.
func buildFamily(ix *Index, members []int, evidenceFor map[[2]int]Evidence, claimedBy map[string]string, proposedTargets map[string]string, byName map[string]*GalleryEntry) (*Family, *Refusal) {
	names := make([]string, 0, len(members))
	for _, m := range members {
		names = append(names, ix.Entries[m].Name)
	}

	parentIdx, err := selectParent(ix, members)
	if err != nil {
		return nil, &Refusal{Members: names, Reason: err.Error()}
	}
	parentEntry := ix.Entries[parentIdx]
	parentName := strings.ToLower(parentEntry.Name)

	// A parent that is itself somebody's variant would create a chain, which
	// the gallery's own resolution refuses to install.
	if owner, claimed := claimedBy[parentName]; claimed {
		return nil, &Refusal{Members: names, Reason: fmt.Sprintf("the natural parent %q is already a variant of %q; proposing it as a parent would nest variants", parentEntry.Name, owner)}
	}
	if owner, claimed := proposedTargets[parentName]; claimed {
		return nil, &Refusal{Members: names, Reason: fmt.Sprintf("the natural parent %q is already proposed as a variant of %q; proposing it as a parent would nest variants", parentEntry.Name, owner)}
	}

	// Adding a variants key to an anchor is inherited by every entry that
	// merges it, silently grouping models nobody proposed. Handling that means
	// editing each merging child too, which is a larger change than this job
	// should make unsupervised, so it refuses and hands the reviewer the list.
	if parentEntry.AnchorName != "" {
		children := ix.MergeChildren(parentEntry.AnchorName)
		if len(children) > 0 {
			childNames := make([]string, 0, len(children))
			for _, c := range children {
				childNames = append(childNames, c.Name)
			}
			return nil, &Refusal{
				Members: names,
				Reason: fmt.Sprintf("the parent %q defines YAML anchor &%s, and a variants key added there is inherited by the %d entries that merge it (%s). Grouping this family by hand also means adding an explicit `variants: []` to each of those entries",
					parentEntry.Name, parentEntry.AnchorName, len(children), strings.Join(childNames, ", ")),
			}
		}
	}

	existing := map[string]struct{}{}
	for _, v := range parentEntry.Variants {
		existing[strings.ToLower(v.Model)] = struct{}{}
	}

	family := &Family{Parent: parentEntry.Name}
	for _, m := range members {
		if m == parentIdx {
			continue
		}
		target := ix.Entries[m]
		lower := strings.ToLower(target.Name)
		if _, already := existing[lower]; already {
			continue
		}
		if target.HasVariants() {
			return nil, &Refusal{Members: names, Reason: fmt.Sprintf("%q already offers variants of its own, so it cannot itself be a variant target", target.Name)}
		}
		if !target.Installable() {
			return nil, &Refusal{Members: names, Reason: fmt.Sprintf("%q has no url, config_file, overrides or files, so it is not independently installable", target.Name)}
		}
		if owner, claimed := claimedBy[lower]; claimed && owner != parentName {
			return nil, &Refusal{Members: names, Reason: fmt.Sprintf("%q is already a variant of %q; a target claimed by two parents is not something the gallery resolves predictably", target.Name, owner)}
		}
		if owner, claimed := proposedTargets[lower]; claimed && owner != parentEntry.Name {
			return nil, &Refusal{Members: names, Reason: fmt.Sprintf("%q is already proposed as a variant of %q in this same run", target.Name, owner)}
		}
		family.Proposals = append(family.Proposals, Proposal{
			Variant:  target.Name,
			Evidence: lookupEvidence(evidenceFor, parentIdx, m),
		})
	}

	if len(family.Proposals) == 0 {
		return nil, nil
	}
	sort.Slice(family.Proposals, func(i, j int) bool { return family.Proposals[i].Variant < family.Proposals[j].Variant })
	return family, nil
}

func lookupEvidence(evidenceFor map[[2]int]Evidence, a, b int) Evidence {
	if a > b {
		a, b = b, a
	}
	if ev, ok := evidenceFor[[2]int{a, b}]; ok {
		return ev
	}
	// The two entries reached the same family through a third one. Say so
	// rather than inventing evidence that was never observed for this pair.
	return Evidence{Signals: []Signal{SignalName}}
}

// selectParent picks the entry the others should hang off.
//
// The bare name wins when there is one: it is the name a user types and the one
// documentation links to. Otherwise the smallest build wins, judged by the
// quantization token in the entry's own weight filename, so the default install
// is the one most hosts can actually run.
func selectParent(ix *Index, members []int) (int, error) {
	// The family's own stem: the one the most members reduce to, shortest name
	// breaking a tie. An entry named exactly that is the bare entry.
	stemCount := map[string]int{}
	for _, m := range members {
		stemCount[NameStem(ix.Entries[m].Name)]++
	}
	// Only a stem two or more members reduce to is the family's own stem. A
	// stem reached by exactly one member is just that member's name, and
	// treating it as the family stem would crown whichever name happens to be
	// shortest rather than whichever build is the base one.
	familyStem := ""
	for stem, n := range stemCount {
		if n < 2 {
			continue
		}
		if familyStem == "" || n > stemCount[familyStem] ||
			(n == stemCount[familyStem] && len(stem) < len(familyStem)) ||
			(n == stemCount[familyStem] && len(stem) == len(familyStem) && stem < familyStem) {
			familyStem = stem
		}
	}

	var bare []int
	for _, m := range members {
		e := ix.Entries[m]
		if HasConfigSuffix(e.Name) {
			continue
		}
		if strings.ToLower(e.Name) == familyStem {
			bare = append(bare, m)
		}
	}
	if len(bare) == 1 {
		return bare[0], nil
	}
	if len(bare) > 1 {
		names := make([]string, 0, len(bare))
		for _, m := range bare {
			names = append(names, ix.Entries[m].Name)
		}
		return 0, fmt.Errorf("more than one entry is named exactly %q (%s), so which one is the base build is a judgement this job will not make", familyStem, strings.Join(names, ", "))
	}

	// No shared stem to be named after. An entry whose name every other member
	// extends is still recognisably the base one, and this is the only handle
	// left for families whose weights carry no readable quantization token at
	// all, such as the ONNX builds.
	if prefix, ok := uniquePrefixMember(ix, members); ok {
		return prefix, nil
	}

	best := -1
	bestWidth := 1 << 20
	for _, m := range members {
		e := ix.Entries[m]
		width := unknownWidth
		if primary, ok := e.PrimaryWeightFile(); ok {
			width = BuildWidth(primary)
		}
		// Members are visited in gallery order, so a strict comparison leaves
		// the earliest entry holding a tie and the choice is deterministic.
		if width < bestWidth {
			best, bestWidth = m, width
		}
	}
	if best < 0 {
		return 0, fmt.Errorf("no member could be identified as the smallest build")
	}
	if bestWidth == unknownWidth {
		names := make([]string, 0, len(members))
		for _, m := range members {
			names = append(names, ix.Entries[m].Name)
		}
		return 0, fmt.Errorf("no member declares a weight file whose quantization can be read (%s), so the smallest build cannot be identified", strings.Join(names, ", "))
	}
	return best, nil
}

// uniquePrefixMember reports the single member whose name every other member's
// name starts with, if there is exactly one.
func uniquePrefixMember(ix *Index, members []int) (int, bool) {
	found := -1
	for _, m := range members {
		name := strings.ToLower(ix.Entries[m].Name)
		isPrefix := true
		for _, other := range members {
			if other == m {
				continue
			}
			if !strings.HasPrefix(strings.ToLower(ix.Entries[other].Name), name) {
				isPrefix = false
				break
			}
		}
		if !isPrefix {
			continue
		}
		if found >= 0 {
			return 0, false
		}
		found = m
	}
	return found, found >= 0
}

// quantDifference lists the quantization tokens that tell two names apart. It
// is the compact form of the evidence: "these differ only by q4_k_m vs q8_0".
func quantDifference(names ...string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, name := range names {
		// Filenames arrive here too, so the extension goes first and "/" counts
		// as a separator. "_" deliberately does not: it holds "q4_k_m" together.
		trimmed := weightExtension.ReplaceAllString(name, "")
		for _, seg := range strings.FieldsFunc(strings.ToLower(trimmed), func(r rune) bool { return r == '-' || r == '/' }) {
			if !IsQuantToken(seg) {
				continue
			}
			if _, ok := seen[seg]; ok {
				continue
			}
			seen[seg] = struct{}{}
			out = append(out, seg)
		}
	}
	sort.Strings(out)
	return out
}
