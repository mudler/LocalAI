package studio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/wellknown"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The kinds that need more than a file name: a skill, a page and the dashboard.
// Each one is a proposal like a memory note. The model never writes the file;
// the owner confirms, and the host writes as the owner with a change set.

// maxPageFiles bounds the files of one page proposal.
const maxPageFiles = 32

const (
	maxPageMeta   = 16 << 10
	maxPageTitle  = 120
	maxPageDesc   = 500
	pageFolderTop = pages.Dir
)

var pageIconRE = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// skillRel is the home-relative SKILL.md of a skill in the personal layer.
func skillRel(name string) string { return skills.SkillsDir + "/" + name + "/" + skills.SkillFile }

// reservedSkillName reports a name an agent's own skill may not take: a core
// skill or a document slot (the slots are data, pkg/addons/contract/slots.json).
// An agent skill is nearer than every layer but the folder one, so a skill
// of such a name would silently replace the provider's.
func reservedSkillName(name string) bool {
	return contract.ProtectedSkill(name) || contract.ReplaceableSkill(name)
}

var skillKind = proposals.Kind{
	Name:      contract.ChangeSkill,
	Grant:     contract.CapSkillsWrite,
	Sensitive: true,
	Path: func(a proposals.Aim) (string, error) {
		if !skills.ValidName(a.Target) {
			return "", fmt.Errorf("%w: a skill target is its name: lowercase letters, digits and dashes, at most 64", proposals.ErrBadTarget)
		}
		switch a.Scope {
		case contract.ScopeUser:
			return skillRel(a.Target), nil
		case contract.ScopeAgent:
			if !proposals.IDSafe(a.Agent) {
				return "", fmt.Errorf("%w: an agent skill needs a chat that runs a saved agent", proposals.ErrBadTarget)
			}
			return files.AgentSkillsDir(a.Agent) + "/" + a.Target + "/" + skills.SkillFile, nil
		}
		return "", fmt.Errorf("%w: a skill is for the user (personal) or for this agent, not for a channel", proposals.ErrBadTarget)
	},
	Check: func(in proposals.CheckInput) error {
		name, _, err := skills.Parse([]byte(in.Content))
		switch {
		case err != nil:
			return fmt.Errorf("the text is not a valid SKILL.md (%v): it starts with --- lines that hold name and description", err)
		case name != in.Aim.Target:
			return fmt.Errorf("the name in the frontmatter is %q but the target is %q", name, in.Aim.Target)
		case in.Aim.Scope == contract.ScopeAgent && reservedSkillName(name):
			return fmt.Errorf("%q is a built-in skill or a document slot that an add-on serves: an agent skill cannot take its name", name)
		case in.Exists && skills.Locked([]byte(in.Current)):
			return fmt.Errorf("the skill %q is locked by its owner: ask them to unlock it", in.Aim.Target)
		}
		// A skill of the agent layer shadows the personal one of the same name,
		// so a locked personal skill is locked against it too.
		if in.Aim.Scope == contract.ScopeAgent && in.Read != nil {
			cur, ok, err := in.Read(skillRel(in.Aim.Target))
			if err != nil {
				return fmt.Errorf("cannot read the personal skill %q: %v", in.Aim.Target, err)
			}
			if ok && skills.Locked([]byte(cur)) {
				return fmt.Errorf("the personal skill %q is locked by its owner: an agent skill of that name would replace it", in.Aim.Target)
			}
		}
		return nil
	},
}

var dashboardKind = proposals.Kind{
	Name:      contract.ChangeDashboard,
	Grant:     contract.CapDashboardWrite,
	Sensitive: true,
	Path: func(a proposals.Aim) (string, error) {
		if a.Scope != contract.ScopeUser {
			return "", fmt.Errorf("%w: the dashboard is the user's: use scope user", proposals.ErrBadTarget)
		}
		return strings.TrimPrefix(wellknown.File(wellknown.Home), "/"), nil
	},
	Check: func(in proposals.CheckInput) error {
		if _, bad := wellknown.Validate(wellknown.Home, []byte(in.Content)); len(bad) > 0 {
			return fmt.Errorf("home.json does not match its schema: %s", strings.Join(bad, "; "))
		}
		return nil
	},
}

var pageKind = proposals.Kind{
	Name:      contract.ChangePage,
	Grant:     contract.CapPagesWrite,
	Sensitive: true,
	MaxBytes:  contract.MaxPageChangeBytes,
	Path: func(a proposals.Aim) (string, error) {
		switch {
		case a.Scope != contract.ScopeUser:
			return "", fmt.Errorf("%w: a page is a folder of the user's home: use scope user", proposals.ErrBadTarget)
		case !pages.ValidName(a.Target):
			return "", fmt.Errorf("%w: a page target is its name: lowercase letters, digits and dashes, at most 64", proposals.ErrBadTarget)
		}
		return pageFolderTop + "/" + a.Target, nil
	},
	Check: func(in proposals.CheckInput) error {
		_, err := pageFiles(in.Content)
		return err
	},
	Files: pageFiles,
}

// pageFiles reads the content of a page proposal, {"files": {path: text}}, and
// checks every file against the rules of the Pages feature: the paths stay
// inside the page folder, index.html is there, each file is text and fits, and
// page.json is the small JSON object the Pages screen reads.
func pageFiles(content string) ([]proposals.FileWrite, error) {
	var doc struct {
		Files map[string]string `json:"files"`
	}
	dec := json.NewDecoder(strings.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil || len(doc.Files) == 0 {
		return nil, errors.New(`the content of a page is JSON like {"files": {"index.html": "<html>...", "page.json": "{...}"}}`)
	}
	if dec.More() {
		return nil, errors.New("the content of a page is one JSON object")
	}
	if len(doc.Files) > maxPageFiles {
		return nil, fmt.Errorf("a page has at most %d files", maxPageFiles)
	}
	out := make([]proposals.FileWrite, 0, len(doc.Files))
	seen := map[string]string{}
	for rel, text := range doc.Files {
		if _, err := pages.CleanPath(rel); err != nil {
			return nil, fmt.Errorf("%q is not a file inside the page folder", clipText(rel, 60))
		}
		key := strings.ToLower(rel)
		if other, dup := seen[key]; dup {
			return nil, fmt.Errorf("%q and %q are the same file", other, rel)
		}
		seen[key] = rel
		switch {
		case len(text) > pages.MaxFileSize:
			return nil, fmt.Errorf("%s is larger than %d bytes", rel, pages.MaxFileSize)
		case !utf8.ValidString(text) || strings.ContainsRune(text, 0):
			return nil, fmt.Errorf("%s must be text", rel)
		}
		out = append(out, proposals.FileWrite{Path: rel, Content: text})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	// A file cannot also be a folder of another file.
	for _, f := range out {
		for _, g := range out {
			if strings.HasPrefix(g.Path, f.Path+"/") {
				return nil, fmt.Errorf("%s is a file and also a folder of %s", f.Path, g.Path)
			}
		}
	}
	index := false
	for _, f := range out {
		switch f.Path {
		case pages.IndexFile:
			index = strings.TrimSpace(f.Content) != ""
		case pages.MetaFile:
			if err := checkPageMeta(f.Content); err != nil {
				return nil, err
			}
		}
	}
	if !index {
		return nil, fmt.Errorf("a page needs a %s that is not empty", pages.IndexFile)
	}
	return out, nil
}

func checkPageMeta(text string) error {
	if len(text) > maxPageMeta {
		return fmt.Errorf("%s is larger than %d bytes", pages.MetaFile, maxPageMeta)
	}
	var m struct {
		Title       string `json:"title"`
		Icon        string `json:"icon"`
		Description string `json:"description"`
		Widget      *struct {
			Size  string `json:"size"`
			Title string `json:"title"`
		} `json:"widget"`
	}
	if err := json.NewDecoder(bytes.NewReader([]byte(text))).Decode(&m); err != nil {
		return fmt.Errorf("%s is not a JSON object: %v", pages.MetaFile, err)
	}
	switch {
	case utf8.RuneCountInString(m.Title) > maxPageTitle:
		return fmt.Errorf("the title in %s is longer than %d characters", pages.MetaFile, maxPageTitle)
	case utf8.RuneCountInString(m.Description) > maxPageDesc:
		return fmt.Errorf("the description in %s is longer than %d characters", pages.MetaFile, maxPageDesc)
	case m.Icon != "" && !pageIconRE.MatchString(m.Icon):
		return fmt.Errorf("the icon in %s is a name of lowercase letters, digits and dashes", pages.MetaFile)
	case m.Widget != nil && !pages.ValidWidgetSize(m.Widget.Size):
		return fmt.Errorf("the widget size in %s is small, medium or large", pages.MetaFile)
	}
	return nil
}

func clipText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

// registerChangeKinds adds the kinds that go beyond a file name to the host.
func (c *Changes) registerChangeKinds() error {
	for _, k := range []proposals.Kind{skillKind, pageKind, dashboardKind} {
		if err := c.kinds.Register(k); err != nil {
			return err
		}
	}
	return nil
}
