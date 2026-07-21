package main

import (
	"regexp"
	"sort"
	"strings"
)

// WantedQuants is the fixed unsloth subset this generator emits. It is a
// deliberate subset: unsloth publishes north of 20 quants per repo, and the
// selector needs useful fitness points rather than every rung.
var WantedQuants = []string{"UD-Q4_K_M", "UD-Q5_K_M", "UD-Q6_K", "Q8_0"}

var shardRE = regexp.MustCompile(`-(\d{5})-of-(\d{5})\.gguf$`)

// QuantBuild is one unsloth quantization, which may be a single file or an
// ordered set of shards.
type QuantBuild struct {
	Quant   string
	Files   []GGUFFile
	Sharded bool
}

// CounterpartCandidates returns the unsloth repo base names worth probing, most
// likely first. Both derivations are needed: the repo name finds
// unsloth/gemma-4-26B-A4B-it-GGUF, while the file stem is what matches for
// repos whose stem is the canonical model name.
func CounterpartCandidates(repoName, fileStem string) []string {
	clean := func(s string) string {
		s = strings.TrimSuffix(s, "-GGUF")
		s = regexp.MustCompile(`-(MTP|TQ)$`).ReplaceAllString(s, "")
		s = strings.TrimSuffix(s, "-APEX")
		return regexp.MustCompile(`-(MTP|TQ)$`).ReplaceAllString(s, "")
	}

	out := []string{clean(repoName)}
	if stem := clean(fileStem); stem != out[0] {
		out = append(out, stem)
	}
	return out
}

// DiscoverUnslothQuants returns the wanted quants a repo publishes, handling
// both the flat single-file layout and the sharded layout where a quant lives
// in its own subdirectory.
func DiscoverUnslothQuants(files []GGUFFile) []QuantBuild {
	var out []QuantBuild

	for _, q := range WantedQuants {
		var flat []GGUFFile
		var shards []GGUFFile

		for _, f := range files {
			switch {
			case !strings.Contains(f.Name, "/") && strings.HasSuffix(f.Name, "-"+q+".gguf"):
				flat = append(flat, f)
			case strings.HasPrefix(f.Name, q+"/") && shardRE.MatchString(f.Name):
				shards = append(shards, f)
			}
		}

		switch {
		case len(flat) > 0:
			out = append(out, QuantBuild{Quant: q, Files: flat})
		case len(shards) > 0:
			sort.Slice(shards, func(i, j int) bool { return shards[i].Name < shards[j].Name })
			out = append(out, QuantBuild{Quant: q, Files: shards, Sharded: true})
		}
	}
	return out
}
