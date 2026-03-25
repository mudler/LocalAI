// gen_inference_defaults fetches unsloth's inference_defaults.json,
// validates its structure, remaps field names to LocalAI conventions,
// and writes the result to core/config/inference_defaults.json.
//
// Run via: go generate ./core/config/
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

const (
	unslothURL = "https://raw.githubusercontent.com/unslothai/unsloth/main/studio/backend/assets/configs/inference_defaults.json"
	outputFile = "inference_defaults.json"
)

// unslothDefaults mirrors the upstream JSON structure
type unslothDefaults struct {
	Comment  string                        `json:"_comment"`
	Families map[string]map[string]float64 `json:"families"`
	Patterns []string                      `json:"patterns"`
}

// localAIDefaults is our output structure
type localAIDefaults struct {
	Comment  string                        `json:"_comment"`
	Families map[string]map[string]float64 `json:"families"`
	Patterns []string                      `json:"patterns"`
}

// requiredFields are the fields every family entry must have
var requiredFields = []string{"temperature", "top_p", "top_k"}

// fieldRemap maps unsloth field names to LocalAI field names
var fieldRemap = map[string]string{
	"repetition_penalty": "repeat_penalty",
}

// allowedFields are the only fields we keep (after remapping)
var allowedFields = map[string]bool{
	"temperature":      true,
	"top_p":            true,
	"top_k":            true,
	"min_p":            true,
	"repeat_penalty":   true,
	"presence_penalty": true,
}

func main() {
	fmt.Fprintf(os.Stderr, "Fetching %s ...\n", unslothURL)

	resp, err := http.Get(unslothURL)
	if err != nil {
		fatal("fetch failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fatal("fetch returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fatal("read body: %v", err)
	}

	var upstream unslothDefaults
	if err := json.Unmarshal(body, &upstream); err != nil {
		fatal("parse upstream JSON: %v", err)
	}

	// Validate structure
	if len(upstream.Families) == 0 {
		fatal("upstream has no families")
	}
	if len(upstream.Patterns) == 0 {
		fatal("upstream has no patterns")
	}

	// Validate every pattern references a family
	for _, p := range upstream.Patterns {
		if _, ok := upstream.Families[p]; !ok {
			fatal("pattern %q has no corresponding family entry", p)
		}
	}

	// Validate every family has required fields and remap field names
	output := localAIDefaults{
		Comment:  "Auto-generated from unsloth inference_defaults.json. DO NOT EDIT. Run go generate ./core/config/ to update.",
		Families: make(map[string]map[string]float64, len(upstream.Families)),
		Patterns: upstream.Patterns,
	}

	// Sort family names for deterministic output
	familyNames := make([]string, 0, len(upstream.Families))
	for name := range upstream.Families {
		familyNames = append(familyNames, name)
	}
	sort.Strings(familyNames)

	for _, name := range familyNames {
		params := upstream.Families[name]

		// Check required fields
		for _, req := range requiredFields {
			found := false
			for k := range params {
				mapped := k
				if m, ok := fieldRemap[k]; ok {
					mapped = m
				}
				if mapped == req || k == req {
					found = true
					break
				}
			}
			if !found {
				fatal("family %q missing required field %q", name, req)
			}
		}

		// Remap and filter fields
		remapped := make(map[string]float64)
		for k, v := range params {
			if newName, ok := fieldRemap[k]; ok {
				k = newName
			}
			if allowedFields[k] {
				remapped[k] = v
			}
		}
		output.Families[name] = remapped
	}

	// Validate patterns are ordered longest-match-first within same prefix groups
	validatePatternOrder(output.Patterns)

	// Marshal with ordered keys for readability
	data, err := marshalOrdered(output)
	if err != nil {
		fatal("marshal output: %v", err)
	}

	if err := os.WriteFile(outputFile, data, 0644); err != nil {
		fatal("write %s: %v", outputFile, err)
	}

	fmt.Fprintf(os.Stderr, "Written %s (%d families, %d patterns)\n",
		outputFile, len(output.Families), len(output.Patterns))
}

// validatePatternOrder warns if a shorter pattern appears before a longer one
// that it's a prefix of (e.g., "qwen3" before "qwen3.5")
func validatePatternOrder(patterns []string) {
	for i, p := range patterns {
		for j := i + 1; j < len(patterns); j++ {
			if strings.HasPrefix(patterns[j], p) {
				fmt.Fprintf(os.Stderr, "WARNING: pattern %q at index %d is a prefix of %q at index %d — longer match should come first\n",
					p, i, patterns[j], j)
			}
		}
	}
}

// marshalOrdered produces JSON with families in pattern order for readability
func marshalOrdered(d localAIDefaults) ([]byte, error) {
	var sb strings.Builder
	sb.WriteString("{\n")
	sb.WriteString(fmt.Sprintf("  %q: %q,\n", "_comment", d.Comment))
	sb.WriteString("  \"families\": {\n")

	// Write families in pattern order, then any remaining not in patterns
	written := make(map[string]bool)
	allFamilies := make([]string, 0, len(d.Families))
	for _, p := range d.Patterns {
		if _, ok := d.Families[p]; ok && !written[p] {
			allFamilies = append(allFamilies, p)
			written[p] = true
		}
	}
	for name := range d.Families {
		if !written[name] {
			allFamilies = append(allFamilies, name)
		}
	}

	for i, name := range allFamilies {
		params := d.Families[name]
		paramJSON, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		comma := ","
		if i == len(allFamilies)-1 {
			comma = ""
		}
		sb.WriteString(fmt.Sprintf("    %q: %s%s\n", name, paramJSON, comma))
	}

	sb.WriteString("  },\n")

	// Patterns array
	patternsJSON, err := json.Marshal(d.Patterns)
	if err != nil {
		return nil, err
	}
	sb.WriteString(fmt.Sprintf("  \"patterns\": %s\n", patternsJSON))
	sb.WriteString("}\n")

	return []byte(sb.String()), nil
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen_inference_defaults: "+format+"\n", args...)
	os.Exit(1)
}
