package main

import (
	"fmt"
	"strings"
)

// RenderBody writes the pull request body.
//
// The body is the product of this job, not the diff. Grouping is a judgement
// call that has gone wrong in both directions before, so a reviewer has to be
// able to accept or reject each family from the body alone, without opening
// HuggingFace to work out whether two entries hold the same weights.
func RenderBody(r *Result, ledgerPath string) string {
	var b strings.Builder

	b.WriteString("## Proposed gallery variant groupings\n\n")
	b.WriteString("This is a proposal, not a decision. The gallery agent adds one build per model and never joins an existing family, so entries that are alternative builds of the same weights drift apart as the gallery grows. This job re-applies the grouping heuristics from the manual sweeps and asks a human to confirm.\n\n")
	b.WriteString("Each family below lists the parent, the variants, and the evidence that they are the same weights. **Reject anything whose evidence you do not believe.**\n\n")
	b.WriteString(fmt.Sprintf("To decline a family permanently, add one line to `%s` in this pull request and close it:\n\n", ledgerPath))
	b.WriteString("```yaml\npairs:\n  - {parent: some-model, variant: some-model-thing, reason: \"different finetune\"}\n```\n\n")

	b.WriteString(fmt.Sprintf("### Proposed families (%d)\n\n", len(r.Families)))
	if len(r.Families) == 0 {
		b.WriteString("None.\n\n")
	}
	for _, f := range r.Families {
		b.WriteString(fmt.Sprintf("#### `%s`\n\n", f.Parent))
		b.WriteString("| variant | signals | evidence |\n|---|---|---|\n")
		for _, p := range f.Proposals {
			b.WriteString(fmt.Sprintf("| `%s` | %s | %s |\n", p.Variant, joinSignals(p.Evidence.Signals), describeEvidence(p.Evidence)))
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf("### Declined by the ledger (%d)\n\n", len(r.Suppressed)))
	if len(r.Suppressed) == 0 {
		b.WriteString("Nothing the heuristics found was already on the ledger.\n\n")
	} else {
		b.WriteString("Candidates the heuristics found and the ledger has already settled. They are listed so the ledger's effect stays visible rather than silently shrinking the job's output.\n\n")
		for _, s := range r.Suppressed {
			b.WriteString(fmt.Sprintf("- `%s` + `%s`: %s\n", s.A, s.B, s.Reason))
		}
		b.WriteString("\n")
	}

	if len(r.AliasSkipped) > 0 {
		b.WriteString(fmt.Sprintf("### Aliases, not variants (%d)\n\n", len(r.AliasSkipped)))
		b.WriteString("These entries install byte for byte the same payload. An alias exists so clients can send a particular name; folding it under another entry would hide that name.\n\n")
		for _, s := range r.AliasSkipped {
			b.WriteString(fmt.Sprintf("- `%s` + `%s`: %s\n", s.A, s.B, s.Reason))
		}
		b.WriteString("\n")
	}

	if len(r.Refusals) > 0 {
		b.WriteString(fmt.Sprintf("### Found but refused (%d)\n\n", len(r.Refusals)))
		b.WriteString("Candidates the heuristics found but the authoring rules would not let this job write. They need a human edit or a rule change.\n\n")
		for _, ref := range r.Refusals {
			b.WriteString(fmt.Sprintf("- %s: %s\n", codeList(ref.Members), ref.Reason))
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n\nOpened by `.github/ci/variantproposals`. Heuristics and the rejection ledger live there and in the ledger file; a wrong proposal is a bug in one of the two.\n")
	return b.String()
}

func joinSignals(signals []Signal) string {
	if len(signals) == 0 {
		return "inferred through another member of the family"
	}
	out := make([]string, 0, len(signals))
	for _, s := range signals {
		out = append(out, "`"+string(s)+"`")
	}
	return strings.Join(out, ", ")
}

func describeEvidence(e Evidence) string {
	var parts []string
	if e.SharedStem != "" {
		parts = append(parts, fmt.Sprintf("same name once quantization markers are stripped: `%s`", e.SharedStem))
	}
	if e.SharedFile != "" {
		parts = append(parts, fmt.Sprintf("same primary weight filename once quantization markers are stripped: `%s`", e.SharedFile))
	}
	if e.SharedRepo != "" {
		parts = append(parts, fmt.Sprintf("same upstream repo `%s`", e.SharedRepo))
	}
	if len(e.QuantTokens) > 0 {
		parts = append(parts, "differing quantization tokens: `"+strings.Join(e.QuantTokens, "`, `")+"`")
	}
	if len(parts) == 0 {
		return "reached this family through another member"
	}
	return strings.Join(parts, "; ")
}

func codeList(names []string) string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, "`"+n+"`")
	}
	return strings.Join(out, " + ")
}

// RenderSummary is the terminal-facing digest of a run, so the workflow log
// says what happened without anyone opening the pull request.
func RenderSummary(r *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "families proposed: %d\n", len(r.Families))
	for _, f := range r.Families {
		names := make([]string, 0, len(f.Proposals))
		for _, p := range f.Proposals {
			names = append(names, p.Variant)
		}
		fmt.Fprintf(&b, "  %s <- %s\n", f.Parent, strings.Join(names, ", "))
	}
	fmt.Fprintf(&b, "declined by ledger: %d\n", len(r.Suppressed))
	for _, s := range r.Suppressed {
		fmt.Fprintf(&b, "  %s\n", s)
	}
	fmt.Fprintf(&b, "aliases skipped: %d\n", len(r.AliasSkipped))
	for _, s := range r.AliasSkipped {
		fmt.Fprintf(&b, "  %s\n", s)
	}
	fmt.Fprintf(&b, "refused: %d\n", len(r.Refusals))
	for _, ref := range r.Refusals {
		fmt.Fprintf(&b, "  %s: %s\n", strings.Join(ref.Members, " + "), ref.Reason)
	}
	return b.String()
}
