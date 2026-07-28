package piipattern

import "sort"

// Builtin is a named, ready-made secret pattern. Group is the uppercase entity
// label a match is reported under (so it keys into a detector model's
// pii_detection.entity_actions, exactly like an NER group). Every Builtin
// pattern is written in the restricted subset and is verified at test time to
// pass ValidatePattern and compile.
type Builtin struct {
	Name        string
	Group       string
	Pattern     string
	Description string
}

// builtins is the curated catalogue. Patterns intentionally anchor on each
// provider's fixed prefix and require a long high-entropy tail, so they fire on
// real credentials and not on ordinary prose. Names are stable identifiers
// referenced from a model config's pii_detection.builtins list.
var builtins = []Builtin{
	{"anthropic_api_key", "ANTHROPIC_KEY", `sk-ant-[A-Za-z0-9_-]{20,}`, "Anthropic API key (sk-ant-…)"},
	{"openai_api_key", "OPENAI_KEY", `sk-(?:proj-)?[A-Za-z0-9_-]{20,}`, "OpenAI API key (sk-… / sk-proj-…)"},
	{"github_token", "GITHUB_TOKEN", `(?:ghp|gho|ghs|ghr|ghu)_[A-Za-z0-9]{36,}`, "GitHub access token (ghp_/gho_/ghs_/ghr_/ghu_)"},
	{"github_pat", "GITHUB_TOKEN", `github_pat_[A-Za-z0-9_]{20,}`, "GitHub fine-grained personal access token"},
	{"aws_access_key", "AWS_ACCESS_KEY", `AKIA[0-9A-Z]{16}`, "AWS access key ID (AKIA…)"},
	{"google_api_key", "GOOGLE_API_KEY", `AIza[0-9A-Za-z_-]{35}`, "Google API key (AIza…)"},
	{"slack_token", "SLACK_TOKEN", `xox[baprs]-[0-9A-Za-z-]{10,}`, "Slack token (xoxb-/xoxa-/xoxp-/xoxr-/xoxs-)"},
	{"stripe_key", "STRIPE_KEY", `(?:sk|rk)_live_[0-9A-Za-z]{16,}`, "Stripe live secret/restricted key"},
	{"jwt", "JWT", `eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`, "JSON Web Token (eyJ….eyJ….…)"},
	{"private_key_block", "PRIVATE_KEY", `-----BEGIN [A-Z ]*PRIVATE KEY-----`, "PEM private-key header"},
}

// BuiltinCatalogue returns the built-in patterns sorted by name. Used by the
// config-metadata registry to populate the editor's builtins checklist.
func BuiltinCatalogue() []Builtin {
	out := make([]Builtin, len(builtins))
	copy(out, builtins)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// BuiltinNames returns the built-in pattern names, sorted.
func BuiltinNames() []string {
	out := make([]string, 0, len(builtins))
	for _, b := range builtins {
		out = append(out, b.Name)
	}
	sort.Strings(out)
	return out
}

// LookupBuiltin finds a built-in by name.
func LookupBuiltin(name string) (Builtin, bool) {
	for _, b := range builtins {
		if b.Name == name {
			return b, true
		}
	}
	return Builtin{}, false
}
