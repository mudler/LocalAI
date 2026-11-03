package session

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// GuestChat is the mount name of a scoped chat's own folder (Chats/<chat>/):
// /workspace/chat. It is reserved like the names of the skill layers.
const GuestChat = "chat"

// ErrReadOnlyPlace is what a write to a place of the view that the chat may
// only read gets: a shared folder, a skill layer or a read-only agent folder.
// The store checks the mode again.
var ErrReadOnlyPlace = errors.New("this folder is read-only for you")

// Target is a path the model wrote, resolved in the workspace.
type Target struct {
	// Abs is the workspace path.
	Abs string
	// Root is the workspace path of the mount that holds Abs: a tool must
	// never move or delete it.
	Root string
	// At is the mount name: /workspace/<At>.
	At string
	// Write is false for a place the chat may only read.
	Write bool
	// Layer is set for a skill layer.
	Layer bool

	home string
}

// Guest is the path the model uses for abs, a path inside the target's
// mount: /workspace/<At>/....
func (t Target) Guest(abs string) string {
	return guestOf(t.At, t.Root, abs)
}

// Show is the path the cards and the change notes use for abs: the path in
// the user's home ("/Projects/Acme/a.md") when abs is inside it, because the
// Files page is user-wide; else the guest path.
func (t Target) Show(abs string) string {
	switch {
	case t.home != "" && abs == t.home:
		return "/"
	case t.home != "" && strings.HasPrefix(abs, t.home+"/"):
		return strings.TrimPrefix(abs, t.home)
	}
	return t.Guest(abs)
}

func guestOf(at, root, abs string) string {
	if abs == root {
		return GuestRoot + "/" + at
	}
	return GuestRoot + "/" + at + strings.TrimPrefix(abs, root)
}

type placeMount struct {
	path, at     string
	write, layer bool
}

// Place maps the paths a model writes to workspace paths, from the mounts of
// the chat's view. "/x", "x", "home/x" and "/workspace/home/x" are x in the
// primary mount (the home, or the agent's folder in a scoped chat);
// "/workspace/<name>/x" is x in the mount of that name, matched exactly. It
// refuses "..", NUL, control characters and a path under /workspace that is
// no mount. It is only a mapping: the store's fence enforces the access, so a
// mistake here gives "not found", never a leak.
type Place struct {
	mounts  []placeMount // the view's mounts, then the skill layers
	primary int          // index of the primary mount, -1 for none
	home    string
	scoped  bool
}

// NewPlace returns the place of a view. The skill layers are added at their
// names when the view has no mount there; the store decides whether the
// actor may read them.
func NewPlace(v workspace.View) Place {
	p := Place{primary: -1, scoped: workspace.HasFence(v.Actor)}
	if validPrincipal(v.Actor.Principal) {
		p.home = HomePath(v.Actor.Principal)
	}
	taken := map[string]bool{}
	for _, m := range v.Mounts {
		if m.At == "" || taken[m.At] {
			continue
		}
		taken[m.At] = true
		if m.At == GuestHome {
			p.primary = len(p.mounts)
		}
		if m.At == GuestChat {
			p.scoped = true
		}
		p.mounts = append(p.mounts, placeMount{path: strings.TrimSuffix(m.Path, "/"), at: m.At, write: m.Mode >= workspace.ModeWrite})
	}
	for _, l := range SkillLayers {
		if !taken[l.At] {
			p.mounts = append(p.mounts, placeMount{path: l.Path, at: l.At, layer: true})
		}
	}
	return p
}

// Primary is the workspace path of the primary mount, or "" when the view
// has none.
func (p Place) Primary() string {
	if p.primary < 0 {
		return ""
	}
	return p.mounts[p.primary].path
}

// PrimaryWritable reports whether the primary mount may be written.
func (p Place) PrimaryWritable() bool {
	return p.primary >= 0 && p.mounts[p.primary].write
}

// Home is the workspace path of the user's home.
func (p Place) Home() string { return p.home }

// Scoped reports whether the view is the view of a chat whose agent has a
// folder: the actor has a fence, or the view mounts the chat folder.
func (p Place) Scoped() bool { return p.scoped }

// Chat is the workspace path of the chat folder mount, or "" when the view
// has none.
func (p Place) Chat() string {
	for _, m := range p.mounts {
		if m.at == GuestChat && !m.layer {
			return m.path
		}
	}
	return ""
}

// Mounts returns the mount names of the view (not the skill layers), in
// order, with their workspace paths.
func (p Place) Mounts() []workspace.MountRequest {
	var out []workspace.MountRequest
	for _, m := range p.mounts {
		if m.layer {
			continue
		}
		mode := workspace.ModeRead
		if m.write {
			mode = workspace.ModeWrite
		}
		out = append(out, workspace.MountRequest{Path: m.path, At: m.at, Mode: mode})
	}
	return out
}

// CheckGuestPath reports whether s is free of the parts a path from the
// model may never have: a ".." segment, NUL, a control character or invalid
// UTF-8. Encoded or look-alike separators are not decoded: they stay part of
// a name.
func CheckGuestPath(s string) error {
	if !utf8.ValidString(s) {
		return workspace.ErrBadName
	}
	for _, r := range s {
		if r == 0 || unicode.IsControl(r) {
			return workspace.ErrBadName
		}
	}
	for seg := range strings.SplitSeq(s, "/") {
		if seg == ".." {
			return workspace.ErrBadName
		}
	}
	return nil
}

// Resolve maps a path the model wrote. An empty path is the primary mount.
func (p Place) Resolve(s string) (Target, error) {
	s = strings.TrimSpace(s)
	if err := CheckGuestPath(s); err != nil {
		return Target{}, err
	}
	guestRel := strings.TrimPrefix(GuestRoot, "/")
	if s == guestRel || strings.HasPrefix(s, guestRel+"/") {
		s = "/" + s
	}
	idx, rest := p.primary, s
	switch {
	case s == GuestRoot || s == GuestRoot+"/":
		rest = ""
	case strings.HasPrefix(s, GuestRoot+"/"):
		name, sub, _ := strings.Cut(strings.TrimPrefix(s, GuestRoot+"/"), "/")
		idx = p.find(name)
		if idx < 0 {
			return Target{}, p.stray(s, strings.TrimPrefix(s, GuestRoot+"/"))
		}
		rest = sub
	case s == GuestHome:
		rest = ""
	case strings.HasPrefix(s, GuestHome+"/"):
		rest = strings.TrimPrefix(s, GuestHome+"/")
	}
	if idx < 0 {
		return Target{}, workspace.ErrNotFound
	}
	m := p.mounts[idx]
	segs, err := workspace.SplitPath("/" + strings.TrimPrefix(rest, "/"))
	if err != nil {
		return Target{}, err
	}
	abs := m.path
	if len(segs) > 0 {
		abs = m.path + workspace.JoinPath(segs)
	}
	if idx == p.primary {
		if c, ok := p.chatAlias(segs); ok {
			return c, nil
		}
	}
	return Target{Abs: abs, Root: m.path, At: m.at, Write: m.write, Layer: m.layer, home: p.home}, nil
}

// chatAlias resolves the home spelling of a scoped chat's own folder
// (/workspace/home/Chats/<chat>/x) in the chat mount. The attachment lists of
// the messages and the transcripts name the chat's files so, and in a scoped
// chat the primary mount is the agent's folder, where that path does not
// exist. Only the chat folder itself is reached this way: it is in the fence.
func (p Place) chatAlias(segs []string) (Target, bool) {
	if !p.scoped || p.home == "" || len(segs) < 2 {
		return Target{}, false
	}
	idx := p.find(GuestChat)
	if idx < 0 || p.mounts[idx].layer {
		return Target{}, false
	}
	m := p.mounts[idx]
	abs := p.home + workspace.JoinPath(segs)
	if abs != m.path && !strings.HasPrefix(abs, m.path+"/") {
		return Target{}, false
	}
	return Target{Abs: abs, Root: m.path, At: m.at, Write: m.write, home: p.home}, true
}

// find returns the index of the mount named exactly name, or -1.
func (p Place) find(name string) int {
	if name == "" {
		return -1
	}
	for i, m := range p.mounts {
		if m.at == name {
			return i
		}
	}
	return -1
}

// stray is the error of a path below /workspace that is no mount
// (/workspace/newsapp, say). Taken as a path in the primary folder it would
// make a folder named "workspace" there, so it is refused with the right
// spelling.
func (p Place) stray(s, rest string) error {
	home := GuestRoot + "/" + GuestHome
	msg := fmt.Sprintf("%s is not a place of the user's files: files live under %s/. Did you mean %s/%s?", s, home, home, rest)
	if !p.scoped {
		msg += fmt.Sprintf(" To make an app the user can open, build a Page: %s/pages/<name>/index.html", home)
	}
	return errors.New(msg)
}

// Guest maps a workspace path to the path the model uses for it, in the
// mount that holds it most closely. It is false outside every mount.
func (p Place) Guest(abs string) (string, bool) {
	best := -1
	for i, m := range p.mounts {
		if abs != m.path && !strings.HasPrefix(abs, m.path+"/") {
			continue
		}
		if best < 0 || len(m.path) > len(p.mounts[best].path) {
			best = i
		}
	}
	if best < 0 {
		return "", false
	}
	m := p.mounts[best]
	return guestOf(m.at, m.path, abs), true
}

// Rel turns a workspace path inside the primary mount into a path of that
// mount ("/" is the mount), or "" outside it.
func (p Place) Rel(abs string) string {
	root := p.Primary()
	switch {
	case root == "":
		return ""
	case abs == root:
		return "/"
	case strings.HasPrefix(abs, root+"/"):
		return strings.TrimPrefix(abs, root)
	}
	return ""
}
