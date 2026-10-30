package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// maxNameBytes is the workspace limit for one name.
const maxNameBytes = 255

// Workspace layout of a user.
const (
	// UsersRoot holds one home folder per principal: /users/<principal>.
	UsersRoot = "/users"
	// ChatsDir is the folder of a home folder that holds chat uploads.
	ChatsDir = "Chats"
	// SettingsDir is the folder of a home folder that holds the settings
	// files LocalAI reads (persona, profile, shortcuts, Home, look).
	SettingsDir = "LocalAI"
	// GuestRoot is the working directory of every tool in the guest.
	GuestRoot = "/workspace"
	// GuestHome is the mount name of the home folder: /workspace/home.
	GuestHome = "home"
	// GuestSharedPrefix starts the mount name of a shared folder:
	// /workspace/shared-<name>. A mount name is one path segment.
	GuestSharedPrefix = "shared-"
	// DefaultTitle is the title of a new chat.
	DefaultTitle = "New chat"
	// MaxTitleRunes is the longest title of a chat, in characters.
	MaxTitleRunes = 80
)

// HomePath returns the home folder of principal.
func HomePath(principal string) string { return UsersRoot + "/" + principal }

// ChatsPath returns the folder that holds the uploads of the principal's chats.
func ChatsPath(principal string) string { return HomePath(principal) + "/" + ChatsDir }

// CleanTitle drops format characters (for example bidi overrides), folds
// white space and cuts a chat title to 80 runes without a trailing space. It
// does not change the Unicode normal form. It returns "" for a title without
// a visible character.
func CleanTitle(s string) string {
	var b strings.Builder
	pending := false // a space is owed before the next visible rune
	for _, r := range s {
		if unicode.In(r, unicode.Cf) {
			continue
		}
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			pending = true
			continue
		}
		if pending && b.Len() > 0 {
			b.WriteByte(' ')
		}
		pending = false
		b.WriteRune(r)
	}
	out := b.String()
	if utf8.RuneCountInString(out) > MaxTitleRunes {
		out = string([]rune(out)[:MaxTitleRunes])
	}
	return strings.TrimRight(out, " ")
}

// fitBytes returns the longest prefix of s that has at most n bytes and ends
// on a rune boundary.
func fitBytes(s string, n int) string {
	end := 0
	for end < len(s) {
		_, size := utf8.DecodeRuneInString(s[end:])
		if end+size > n {
			break
		}
		end += size
	}
	return s[:end]
}

// TitleFrom makes the title of a chat from its first message: the first line
// that has text, cleaned, cut to MaxTitleRunes at a word boundary with an
// ellipsis. It returns "" when the message has no usable text.
func TitleFrom(text string) string {
	for _, line := range strings.Split(text, "\n") {
		t := CleanTitle(line)
		if t == "" {
			continue
		}
		r := []rune(t)
		if len(r) <= MaxTitleRunes {
			return t
		}
		cut := string(r[:MaxTitleRunes-1])
		if i := strings.LastIndex(cut, " "); i > MaxTitleRunes/2 {
			cut = cut[:i]
		}
		return strings.TrimRight(cut, " ,.;:") + "…"
	}
	return ""
}

// FolderName returns the name of a chat folder: the creation date and the
// title, for example "2026-10-18 Budget". A "/" in the title becomes "-". The
// title is cut to the byte budget of a name at a rune boundary.
func FolderName(created time.Time, title string) string {
	t := strings.ReplaceAll(CleanTitle(title), "/", "-")
	if t == "" {
		t = DefaultTitle
	}
	prefix := created.UTC().Format("2006-01-02") + " "
	return prefix + strings.TrimRight(fitBytes(t, maxNameBytes-len(prefix)), " ")
}

// uniqueName returns base, or base with " 2", " 3" ... when taken reports it.
func uniqueName(base string, taken func(name string) (bool, error)) (string, error) {
	for i := 1; i < 1000; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s %d", base, i)
		}
		t, err := taken(name)
		if err != nil {
			return "", err
		}
		if !t {
			return name, nil
		}
	}
	return "", fmt.Errorf("session: no free folder name for %q", base)
}

// SharedRoot is a folder outside the home folder that the user may use.
type SharedRoot struct {
	Path string
	Mode workspace.Mode // the mode to ask for; the store clamps it to the grant
}

// BuildView returns the view of a chat: the home folder read-write at
// "home", and each shared root at "shared-<name>". A shared root that the
// store refuses (the grant is gone) is skipped and logged. A missing home
// folder is ErrNoHome.
func BuildView(ctx context.Context, st workspace.Store, a workspace.Actor, shared []SharedRoot, log *slog.Logger) (workspace.View, error) {
	home := HomePath(a.Principal)
	reqs := []workspace.MountRequest{{Path: home, At: GuestHome, Mode: workspace.ModeWrite}}
	v, err := st.View(ctx, a, reqs)
	if err != nil {
		if missingFolder(err) {
			return workspace.View{}, fmt.Errorf("%w: %s", ErrNoHome, home)
		}
		return workspace.View{}, err
	}
	used := map[string]bool{workspace.FoldName(GuestHome): true}
	for _, s := range shared {
		p := path.Clean("/" + strings.Trim(s.Path, "/"))
		// Skip the home folder, a folder inside it, the root and every
		// ancestor of home: a mount there would expose other folders.
		if p == "/" || p == home || strings.HasPrefix(p, home+"/") || strings.HasPrefix(home, p+"/") {
			continue
		}
		mode := s.Mode
		if mode != workspace.ModeRead {
			mode = workspace.ModeWrite
		}
		// Leave room for the " N" suffix that uniqueName may add.
		base := fitBytes(GuestSharedPrefix+path.Base(p), maxNameBytes-len(" 999"))
		base = strings.TrimRight(base, " ")
		at, err := uniqueName(base, func(n string) (bool, error) { return used[workspace.FoldName(n)], nil })
		if err != nil {
			return workspace.View{}, err
		}
		sv, err := st.View(ctx, a, []workspace.MountRequest{{Path: p, At: at, Mode: mode}})
		if err != nil {
			if missingFolder(err) {
				log.Warn("session: skipping a shared folder", "path", p, "error", err)
				continue
			}
			return workspace.View{}, err
		}
		used[workspace.FoldName(at)] = true
		v.Mounts = append(v.Mounts, sv.Mounts...)
	}
	return v, nil
}

// missingFolder reports whether a View error means the folder cannot be
// mounted: it is gone, the actor has no grant, or it is not a folder.
// Other errors, such as an I/O failure or a cancelled context, are returned.
func missingFolder(err error) bool {
	return errors.Is(err, workspace.ErrNotFound) ||
		errors.Is(err, workspace.ErrInvalid) ||
		errors.Is(err, workspace.ErrBadName)
}

// LayoutPrompt describes the guest layout for the system prompt.
func LayoutPrompt(v workspace.View) string {
	var b strings.Builder
	b.WriteString("The user's files are in these folders; use these paths with the file tools:\n")
	for _, m := range v.Mounts {
		access := "read and write"
		if m.Mode < workspace.ModeWrite {
			access = "read only"
		}
		switch {
		case m.At == GuestHome:
			fmt.Fprintf(&b, "- %s/%s: the user's home folder (%s). Files the user uploads to a chat are in %s/%s/%s/.\n", GuestRoot, m.At, access, GuestRoot, m.At, ChatsDir)
		default:
			fmt.Fprintf(&b, "- %s/%s: a folder shared with the user (%s).\n", GuestRoot, m.At, access)
		}
	}
	b.WriteString("There is nothing else under " + GuestRoot + ": a path such as " + GuestRoot + "/myapp is not a folder of the user; write " + GuestRoot + "/" + GuestHome + "/myapp instead. " +
		"To make an app the user can open, build a Page: " + GuestRoot + "/" + GuestHome + "/pages/<name>/index.html. Every change is saved, and the user can undo it.")
	return b.String()
}
