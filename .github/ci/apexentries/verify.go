package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type verifyEntry struct {
	Name      string       `yaml:"name"`
	Tags      []string     `yaml:"tags"`
	Variants  []VariantRef `yaml:"variants"`
	Overrides struct {
		// Backend scopes the checks that only hold for one engine. An entry that
		// declares none takes its configuration from the referenced url: template,
		// which this verifier never reads, so it cannot be judged either way.
		Backend string   `yaml:"backend"`
		Options []string `yaml:"options"`
		// MMProj and DraftModel name the files that are not weights. They are
		// the only signal for it: a drafter lands in the same models/ prefix as
		// the weights, so the path alone cannot tell them apart.
		MMProj     string `yaml:"mmproj"`
		DraftModel string `yaml:"draft_model"`
	} `yaml:"overrides"`
	Files []struct {
		Filename string `yaml:"filename"`
		SHA256   string `yaml:"sha256"`
		URI      string `yaml:"uri"`
	} `yaml:"files"`
}

// Verify checks the invariants the variants schema and the tagging rule
// require. It returns every problem rather than the first, so one run tells the
// author everything that needs fixing.
func Verify(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("reading %s: %v", path, err)}
	}

	var entries []verifyEntry
	if err := yaml.Unmarshal(raw, &entries); err != nil {
		return []string{fmt.Sprintf("parsing %s: %v", path, err)}
	}

	var problems []string

	byName := map[string]verifyEntry{}
	for _, e := range entries {
		if _, seen := byName[e.Name]; seen {
			problems = append(problems, fmt.Sprintf("duplicate entry name: %s", e.Name))
			continue
		}
		byName[e.Name] = e
	}

	for _, e := range entries {
		for _, v := range e.Variants {
			target, ok := byName[v.Model]
			if !ok {
				problems = append(problems, fmt.Sprintf("%s: variant %q does not exist", e.Name, v.Model))
				continue
			}
			if len(target.Variants) > 0 {
				problems = append(problems, fmt.Sprintf("%s: variant %q declares variants of its own", e.Name, v.Model))
			}
		}

		// sha256 is required on .gguf files only. Every non-GGUF asset in the
		// index today belongs to a hand-curated entry this generator does not
		// produce, and gating on them would make the verifier unusable as a gate
		// over work that is outside its scope. Their missing checksums are a real
		// gallery-hygiene issue, but one for the curators of those entries, not
		// something this tool can act on.
		for _, f := range e.Files {
			if strings.HasSuffix(f.Filename, ".gguf") && f.SHA256 == "" {
				problems = append(problems, fmt.Sprintf("%s: file %s has no sha256", e.Name, f.Filename))
			}
		}

		problems = append(problems, checkWeightCount(e)...)
		problems = append(problems, checkFeatureTag(e, "dflash")...)
		problems = append(problems, checkFeatureTag(e, "mtp")...)
	}

	return problems
}

// checkWeightCount catches an entry carrying two whole models. The flat-match
// branch in DiscoverUnslothQuants appends every match, so a quant label that is
// a suffix of another one (Q8_0 of UD-Q8_0) collects both files into one build
// while the rendered model: points at only the first. The result downloads
// twice the bytes and serves whichever file sorted first, silently.
//
// Shards are exempt because a sharded build is legitimately many files.
//
// The collision is a property of llama-cpp quant discovery, so the check is
// scoped to that backend. Multi-component TTS, ASR and diffusion engines ship an
// encoder, a decoder and a vocoder as one model, and there the second GGUF is
// the design rather than a bug.
func checkWeightCount(e verifyEntry) []string {
	if e.Overrides.Backend != "llama-cpp" {
		return nil
	}

	var weights []string
	for _, f := range e.Files {
		switch {
		case !strings.HasSuffix(f.Filename, ".gguf"):
		case shardRE.MatchString(f.Filename):
		case f.Filename == e.Overrides.MMProj:
		case f.Filename == e.Overrides.DraftModel:
		default:
			weights = append(weights, f.Filename)
		}
	}

	if len(weights) > 1 {
		return []string{fmt.Sprintf("%s: more than one weight file: %s", e.Name, strings.Join(weights, ", "))}
	}
	return nil
}

// checkFeatureTag enforces the rule in both directions. A tag without the
// configuration promotes a build that is no faster; configuration without the
// tag leaves a genuinely faster build ranked as plain.
//
// It only speaks about backends whose declaration it can actually read, because
// a rule applied where the evidence is invisible reports noise rather than bugs.
func checkFeatureTag(e verifyEntry, feature string) []string {
	decl, configured, judgeable := featureDeclaration(e, feature)
	if !judgeable {
		return nil
	}

	tagged := false
	for _, t := range e.Tags {
		if t == feature {
			tagged = true
			break
		}
	}

	switch {
	case tagged && !configured:
		return []string{fmt.Sprintf("%s: tagged %s but sets no %s", e.Name, feature, decl)}
	case configured && !tagged:
		return []string{fmt.Sprintf("%s: sets %s but is not tagged %s", e.Name, decl, feature)}
	}
	return nil
}

// featureDeclaration implements the per-backend table in
// .agents/adding-gallery-models.md. It returns the declaration the backend uses
// to configure the feature, whether the entry carries it, and whether this
// verifier is in a position to answer at all.
func featureDeclaration(e verifyEntry, feature string) (decl string, configured, judgeable bool) {
	switch e.Overrides.Backend {
	case "llama-cpp":
		decl = "spec_type:draft-" + feature
		for _, o := range e.Overrides.Options {
			if strings.TrimSpace(o) == decl {
				return decl, true, true
			}
		}
		return decl, false, true

	case "ds4":
		// ds4 carries the MTP heads in the weights and turns them on with
		// mtp_path / mtp_draft. It has no dflash counterpart, so dflash is not a
		// question that can be asked of a ds4 entry.
		if feature != "mtp" {
			return "", false, false
		}
		decl = "mtp_path:"
		for _, o := range e.Overrides.Options {
			o = strings.TrimSpace(o)
			if strings.HasPrefix(o, "mtp_path:") || strings.HasPrefix(o, "mtp_draft:") {
				return decl, true, true
			}
		}
		return decl, false, true

	default:
		// sglang configures the feature with speculative_algorithm: in the
		// referenced gallery/*.yaml, and an entry that declares no backend takes
		// its whole configuration from its url: template. Verify reads one index
		// file and follows neither, so it must not judge these in either
		// direction.
		return "", false, false
	}
}

// UnaccountedQuants reports a wanted quant the repo demonstrably publishes but
// that discovery produced no build for. The layout that triggers it today is
// root-level shards, which match neither branch of DiscoverUnslothQuants; no
// counterpart ships that way yet, but a batch generator must not drop a build
// with nothing said about it.
func UnaccountedQuants(files []GGUFFile, builds []QuantBuild) []string {
	built := map[string]bool{}
	for _, b := range builds {
		built[b.Quant] = true
	}

	var problems []string
	for _, q := range WantedQuants {
		if built[q] {
			continue
		}
		for _, f := range files {
			if strings.Contains(f.Name, q) {
				problems = append(problems, fmt.Sprintf("quant %s is published upstream (%s) but produced no build", q, f.Name))
				break
			}
		}
	}
	return problems
}
