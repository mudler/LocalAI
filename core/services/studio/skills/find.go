package skills

import (
	"slices"
	"strings"
)

// maxSuggestions is the most names that Suggest returns.
const maxSuggestions = 3

// Find returns the skill of a name among the skills that Usable keeps. The
// name must match exactly; Suggest finds a near one.
func Find(list []Skill, name string) (Skill, bool) {
	name = strings.TrimSpace(name)
	for _, s := range Usable(list) {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// Names returns the names of the skills that Usable keeps, in its order.
func Names(list []Skill) []string {
	u := Usable(list)
	out := make([]string, len(u))
	for i, s := range u {
		out[i] = s.Name
	}
	return out
}

// Suggest returns the names of usable skills that are near a name that Find
// did not match, best first, at most three: the same name in other case, the
// name with a "<prefix>-" or "<layer>:" part added or taken away (xlsx and
// office-xlsx, addon:xlsx), then a name one or two edits away (one for a name
// of four characters or fewer). It returns nil when none is near.
func Suggest(list []Skill, name string) []string {
	asked := strings.ToLower(strings.TrimSpace(name))
	if asked == "" {
		return nil
	}
	if _, plain, ok := strings.Cut(asked, ":"); ok {
		asked = plain
	}
	maxEdits := 2
	if len(asked) <= 4 {
		maxEdits = 1
	}
	type hit struct {
		name string
		rank int
	}
	var hits []hit
	for _, n := range Names(list) {
		ln := strings.ToLower(n)
		switch {
		case ln == asked:
			hits = append(hits, hit{n, 0})
		case afterDash(ln) == asked || afterDash(asked) == ln:
			hits = append(hits, hit{n, 1})
		default:
			if d := editDistance(ln, asked, maxEdits); d <= maxEdits {
				hits = append(hits, hit{n, 1 + d})
			}
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return a.rank - b.rank })
	var out []string
	for _, h := range hits {
		if len(out) == maxSuggestions {
			break
		}
		out = append(out, h.name)
	}
	return out
}

// afterDash returns what follows the first "-" of s, or "" when s has none.
func afterDash(s string) string {
	_, rest, ok := strings.Cut(s, "-")
	if !ok {
		return ""
	}
	return rest
}

// editDistance returns the Levenshtein distance of a and b, or max+1 as soon
// as it is known to be larger than max.
func editDistance(a, b string, max int) int {
	if d := len(a) - len(b); d > max || -d > max {
		return max + 1
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		best := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			best = min(best, cur[j])
		}
		if best > max {
			return max + 1
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// Body returns the text of a SKILL.md after its frontmatter: the instructions
// the model follows. A file without frontmatter is returned whole.
func Body(data []byte) string {
	s := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.SplitAfter(s, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t\n") != "---" {
		return strings.TrimSpace(s)
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t\n") == "---" {
			return strings.TrimSpace(strings.Join(lines[i+1:], ""))
		}
	}
	return strings.TrimSpace(s)
}
