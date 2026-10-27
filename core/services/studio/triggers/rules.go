package triggers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// RulesFile is the name of the file that holds the rules of a folder.
const RulesFile = "AGENTS.md"

// MaxRulesBytes is the most text that Rules gives, not counting the note that
// tells about a cut.
const MaxRulesBytes = 32 << 10

// rulesCutNote derives its size from MaxRulesBytes.
var rulesCutNote = fmt.Sprintf("(The folder rules were cut to fit %d KiB. The rules of the outer folders were left out first.)\n\n", MaxRulesBytes>>10)

// Rules reads the AGENTS.md file of each folder from the top of the path down
// to folder, outermost first, as the actor. A file that the actor cannot read,
// that does not exist, or that is a folder is left out. Each part starts with
// a line that names its folder. When the parts together are longer than
// MaxRulesBytes, the outer parts are left out first, and the text of a single
// part that is too long is cut at its end; a note at the start says so.
//
// The text is untrusted: anyone who can write a folder on the path controls
// it, so a host that sends it to an agent must treat it as untrusted input.
func Rules(ctx context.Context, store workspace.Store, actor workspace.Actor, folder string) (string, error) {
	return RulesAs(ctx, store, actor, folder, nil)
}

// RulesAs is Rules with a name for each folder in the heading of its part. A
// host that keeps users' folders below an internal path gives the path the user
// knows. A nil name uses the workspace path.
func RulesAs(ctx context.Context, store workspace.Store, actor workspace.Actor, folder string, name func(dir string) string) (string, error) {
	if folder == "" || folder[0] != '/' || path.Clean(folder) != folder {
		return "", fmt.Errorf("triggers: %q is not a clean workspace path", folder)
	}
	dirs := []string{"/"}
	if folder != "/" {
		cur := ""
		for _, seg := range strings.Split(strings.Trim(folder, "/"), "/") {
			cur += "/" + seg
			dirs = append(dirs, cur)
		}
	}
	type part struct{ head, text string }
	var parts []part
	for _, dir := range dirs {
		text, ok, err := readRules(ctx, store, actor, path.Join(dir, RulesFile))
		if err != nil {
			return "", err
		}
		text = strings.TrimSpace(text)
		if !ok || text == "" { // an empty file would leave a bare heading
			continue
		}
		shown := dir
		if name != nil {
			shown = name(dir)
		}
		parts = append(parts, part{head: "# Folder rules: " + shown + "\n\n", text: text + "\n\n"})
	}
	size := func(p part) int { return len(p.head) + len(p.text) }
	total, cut := 0, false
	for _, p := range parts {
		total += size(p)
	}
	for len(parts) > 1 && total > MaxRulesBytes {
		total -= size(parts[0])
		parts, cut = parts[1:], true
	}
	if len(parts) == 1 && total > MaxRulesBytes {
		room := MaxRulesBytes - len(parts[0].head)
		parts[0].text = cutUTF8(parts[0].text, room)
		cut = true
	}
	var b strings.Builder
	if cut {
		b.WriteString(rulesCutNote)
	}
	for _, p := range parts {
		b.WriteString(p.head)
		b.WriteString(p.text)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// cutUTF8 keeps at most n bytes of s, without a broken character.
func cutUTF8(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// readRules reads one rules file. ok is false when the actor cannot read it.
func readRules(ctx context.Context, store workspace.Store, actor workspace.Actor, p string) (string, bool, error) {
	r, node, err := store.Open(ctx, actor, p)
	if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrInvalid) { // no access, no file, or a folder
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("triggers: read %s: %w", p, err)
	}
	defer r.Close()
	if node.Kind != workspace.KindFile {
		return "", false, nil
	}
	// One byte more than the limit is enough to know that the file is too long.
	b, err := io.ReadAll(io.LimitReader(r, MaxRulesBytes+1))
	if err != nil {
		return "", false, fmt.Errorf("triggers: read %s: %w", p, err)
	}
	return strings.ToValidUTF8(string(b), "�"), true, nil
}
