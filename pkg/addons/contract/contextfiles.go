// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Limits of the context files an add-on declares.
const (
	// MaxContextFiles is the most files one add-on declares: one for each
	// scope.
	MaxContextFiles = 3
	// MaxContextFileBytes is the most bytes of one index the prompt carries.
	// It is the cap of the host; a smaller Cap lowers it.
	MaxContextFileBytes = 2 << 10
	// ContextFilesRoot is the folder of the user's home that every declared
	// file lies in. An add-on cannot name any other part of the home.
	ContextFilesRoot = "Memory/"
	// maxContextPath bounds the path of a declared file.
	maxContextPath = 200
)

// ContextFile names one small index file that the host loads into the prompt
// of a chat, under a fixed header that asks the model to read the notes the
// index points to. The add-on declares where its files are; the host reads
// them as the user, caps them and builds the prompt. The add-on is not asked
// at each turn and holds no authority over the files: it names a path under
// ContextFilesRoot and nothing else.
//
// A user file applies to every chat of the person. An agent file holds the
// placeholder {agent} once and applies to the chats of one saved agent. A
// channel file holds {channel} once and applies to the chats that one
// connector conversation started. The host fills a placeholder from the chat,
// never from the model.
type ContextFile struct {
	Scope ChangeScope `json:"scope"`
	// Path is relative to the user's home, under ContextFilesRoot, and ends
	// in .md.
	Path string `json:"path"`
	// Cap is the most bytes of the index the prompt carries. Zero means
	// MaxContextFileBytes.
	Cap int `json:"cap,omitempty"`
}

var contextPlaceholder = regexp.MustCompile(`\{[^{}]*\}`)

// placeholderOf is the one placeholder a scope's path holds, or "".
func placeholderOf(s ChangeScope) string {
	switch s {
	case ScopeAgent:
		return "{agent}"
	case ScopeChannel:
		return "{channel}"
	}
	return ""
}

func (f ContextFile) validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("context file %q: %s", f.Path, fmt.Sprintf(format, a...))
	}
	if !ValidChangeScope(string(f.Scope)) {
		return bad("the scope must be user, agent or channel")
	}
	if f.Cap < 0 || f.Cap > MaxContextFileBytes {
		return bad("the cap must be 0 to %d bytes", MaxContextFileBytes)
	}
	p := f.Path
	if len(p) > maxContextPath || !strings.HasPrefix(p, ContextFilesRoot) || !strings.HasSuffix(p, ".md") {
		return bad("the path must lie under %s, end in .md and have at most %d bytes", ContextFilesRoot, maxContextPath)
	}
	found := contextPlaceholder.FindAllString(p, -1)
	if want := placeholderOf(f.Scope); (want == "" && len(found) > 0) || (want != "" && (len(found) != 1 || found[0] != want)) {
		return bad("a %s path holds %s once and no other placeholder", f.Scope, placeholderOrNone(want))
	}
	rest := contextPlaceholder.ReplaceAllString(p, "")
	if strings.ContainsAny(rest, `{}\`) || strings.IndexFunc(rest, unicode.IsControl) >= 0 {
		return bad("the path holds a character that is not allowed")
	}
	for seg := range strings.SplitSeq(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return bad("the path holds an empty or parent segment")
		}
	}
	return nil
}

func placeholderOrNone(p string) string {
	if p == "" {
		return "no placeholder"
	}
	return p
}

// validateContextFiles checks the declaration against the manifest.
func validateContextFiles(m Manifest) error {
	files := m.Capabilities.ContextFiles
	if len(files) == 0 {
		return nil
	}
	if len(files) > MaxContextFiles {
		return fmt.Errorf("too many context files: at most %d", MaxContextFiles)
	}
	seen := map[ChangeScope]bool{}
	for _, f := range files {
		if err := f.validate(); err != nil {
			return err
		}
		if seen[f.Scope] {
			return fmt.Errorf("context files: a second file for the %s scope", f.Scope)
		}
		seen[f.Scope] = true
	}
	return nil
}
