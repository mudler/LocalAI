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

		for _, f := range e.Files {
			if f.SHA256 == "" {
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
func checkWeightCount(e verifyEntry) []string {
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
func checkFeatureTag(e verifyEntry, feature string) []string {
	tagged := false
	for _, t := range e.Tags {
		if t == feature {
			tagged = true
			break
		}
	}

	configured := false
	for _, o := range e.Overrides.Options {
		if strings.TrimSpace(o) == "spec_type:draft-"+feature {
			configured = true
			break
		}
	}

	switch {
	case tagged && !configured:
		return []string{fmt.Sprintf("%s: tagged %s but sets no spec_type:draft-%s", e.Name, feature, feature)}
	case configured && !tagged:
		return []string{fmt.Sprintf("%s: sets spec_type:draft-%s but is not tagged %s", e.Name, feature, feature)}
	}
	return nil
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
