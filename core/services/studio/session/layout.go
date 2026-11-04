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
	"github.com/mudler/LocalAI/core/services/studio/workspace/workfiles"
)

// maxNameBytes is the workspace limit for one name.
const maxNameBytes = 255

// Workspace layout of a user.
const (
	// UsersRoot holds one home folder per principal: /users/<principal>.
	UsersRoot = "/users"
	// ChatsDir is the folder of a home folder that holds chat uploads.
	ChatsDir = workfiles.ChatsDir
	// SettingsDir is the folder of a home folder that holds the settings
	// files LocalAI reads (persona, profile, shortcuts, Home, look).
	SettingsDir = workfiles.SettingsDir
	// GuestRoot is the working directory of every tool in the guest.
	GuestRoot = "/workspace"
	// GuestHome is the mount name of the home folder: /workspace/home.
	GuestHome = "home"
	// GuestSharedPrefix starts the mount name of a shared folder:
	// /workspace/shared-<name>. A mount name is one path segment.
	GuestSharedPrefix = "shared-"
	// GuestFolderPrefix starts the mount name of an agent's folder after the
	// first: /workspace/folder-<name>. The first folder is "home".
	GuestFolderPrefix = "folder-"
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
	for line := range strings.SplitSeq(text, "\n") {
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

// SkillLayer is a folder of skills that every user reads and none writes, by
// workspace path, with its mount name in a chat's view (/workspace/<At>).
type SkillLayer struct{ Path, At string }

// SkillLayers are the read-only skill layers: the shared skills an
// administrator gives to everyone, the built-in skills and the skills of the
// installed add-ons. The file tools read them at these names, and the
// commands see them at the same names, so a skill's scripts run in place.
var SkillLayers = []SkillLayer{
	{Path: "/skills", At: GuestSharedPrefix + "skills"},
	{Path: "/builtin-skills", At: GuestSharedPrefix + "builtin-skills"},
	{Path: "/addon-skills", At: GuestSharedPrefix + "addon-skills"},
}

// SkillLayerMounts returns read-only mounts of the skill layers the actor can
// read. A layer that is missing or not granted is skipped: the commands then
// see no such folder, as before the layer existed.
func SkillLayerMounts(ctx context.Context, st workspace.Store, a workspace.Actor) ([]workspace.MountRequest, error) {
	var out []workspace.MountRequest
	for _, l := range SkillLayers {
		req := workspace.MountRequest{Path: l.Path, At: l.At, Mode: workspace.ModeRead}
		if _, err := st.View(ctx, a, []workspace.MountRequest{req}); err != nil {
			if missingFolder(err) {
				continue
			}
			return nil, err
		}
		out = append(out, req)
	}
	return out, nil
}

// SharedRoot is a folder outside the home folder that the user may use.
type SharedRoot struct {
	Path string
	Mode workspace.Mode // the mode to ask for; the store clamps it to the grant
}

// BuildView returns the view of a chat: primary (the home, or the agent's
// folder of a scoped chat) at "home", the chat folder at "chat" when chat is
// not "", and each shared root at "shared-<name>". The store caps each mount
// at the actor's mode, so a fenced actor gets its folder at the agent's mode.
// A fenced actor gets the shared folders read only. A shared root that the
// store refuses (the grant is gone, or it is outside the fence) is skipped
// and logged. A primary folder that cannot be mounted is ErrNoHome; a chat
// folder that cannot be mounted is an error too, since the resolver makes it
// before it fences.
func BuildView(ctx context.Context, st workspace.Store, a workspace.Actor, primary string, chat string, shared []SharedRoot, log *slog.Logger) (workspace.View, error) {
	return BuildViewFolders(ctx, st, a, primary, nil, chat, shared, log)
}

// reservedMountNames are the names no folder may take: the home, the chat
// folder and the skill layers.
func reservedMountNames() map[string]bool {
	used := map[string]bool{workspace.FoldName(GuestHome): true, workspace.FoldName(GuestChat): true}
	for _, l := range SkillLayers {
		used[workspace.FoldName(l.At)] = true
	}
	return used
}

// FolderMountNames returns the mount name of each further folder of an agent,
// in order: "folder-<name>", with " 2", " 3" and so on when two folders have
// the same name. The same call gives the same names, so the code that builds
// the view and the code that tells the model about it agree.
func FolderMountNames(paths []string) []string {
	used := reservedMountNames()
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		base := strings.TrimRight(fitBytes(GuestFolderPrefix+path.Base(p), maxNameBytes-len(" 999")), " ")
		at, err := uniqueName(base, func(n string) (bool, error) { return used[workspace.FoldName(n)], nil })
		if err != nil {
			at = fmt.Sprintf("%s%d", GuestFolderPrefix, len(out)+1)
		}
		used[workspace.FoldName(at)] = true
		out = append(out, at)
	}
	return out
}

// BuildViewFolders is BuildView for an agent with several folders: primary is
// the first and extra are the others, mounted at "folder-<name>". The store
// caps each mount at the fence's mode. An extra folder the store refuses is
// skipped and logged.
func BuildViewFolders(ctx context.Context, st workspace.Store, a workspace.Actor, primary string, extra []string, chat string, shared []SharedRoot, log *slog.Logger) (workspace.View, error) {
	home := HomePath(a.Principal)
	reqs := []workspace.MountRequest{{Path: primary, At: GuestHome, Mode: workspace.ModeWrite}}
	v, err := st.View(ctx, a, reqs)
	if err != nil {
		if missingFolder(err) {
			return workspace.View{}, fmt.Errorf("%w: %s", ErrNoHome, primary)
		}
		return workspace.View{}, err
	}
	if chat != "" {
		cv, err := st.View(ctx, a, []workspace.MountRequest{{Path: chat, At: GuestChat, Mode: workspace.ModeWrite}})
		if err != nil {
			return workspace.View{}, fmt.Errorf("session: mount the chat folder: %w", err)
		}
		v.Mounts = append(v.Mounts, cv.Mounts...)
	}
	fenced := workspace.HasFence(a)
	// The names of the skill layers and of the chat folder are reserved: the
	// file tools read them there, so a shared folder named skills gets
	// "shared-skills 2".
	used := reservedMountNames()
	for i, at := range FolderMountNames(extra) {
		ev, err := st.View(ctx, a, []workspace.MountRequest{{Path: extra[i], At: at, Mode: workspace.ModeWrite}})
		if err != nil {
			if missingFolder(err) {
				log.Warn("session: skipping a folder of the agent", "path", extra[i], "error", err)
				continue
			}
			return workspace.View{}, err
		}
		used[workspace.FoldName(at)] = true
		v.Mounts = append(v.Mounts, ev.Mounts...)
	}
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
		if fenced {
			mode = workspace.ModeRead
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
				if fenced {
					v.NotMounted++
				}
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

// ScopedPrompt says how LayoutPrompt names the folders: Scoped is set for
// a chat whose agent has a folder.
type ScopedPrompt struct {
	Scoped bool
}

// PromptScope is the ScopedPrompt of a view: scoped when its actor has a
// fence or it mounts the chat folder.
func PromptScope(v workspace.View) ScopedPrompt {
	return ScopedPrompt{Scoped: NewPlace(v).Scoped()}
}

// LayoutPrompt describes the guest layout for the system prompt. A scoped
// chat hears only of its folders, by the name the user gave the agent's
// folder, and gets no hint to build a page: pages live outside its folder.
func LayoutPrompt(v workspace.View, scoped ScopedPrompt) string {
	if scoped.Scoped {
		return scopedLayoutPrompt(v)
	}
	var b strings.Builder
	b.WriteString("The user's files are in these folders; use these paths with the file tools:\n")
	for _, m := range v.Mounts {
		access := accessWords(m.Mode)
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

func accessWords(m workspace.Mode) string {
	if m < workspace.ModeWrite {
		return "read only"
	}
	return "read and write"
}

func scopedLayoutPrompt(v workspace.View) string {
	home := ""
	if validPrincipal(v.Actor.Principal) {
		home = HomePath(v.Actor.Principal)
	}
	var b strings.Builder
	b.WriteString("The files you can use are in these folders; use these paths with the file tools:\n")
	for _, m := range v.Mounts {
		access := accessWords(m.Mode)
		switch {
		case m.At == GuestHome && (m.Path == home || home == ""):
			fmt.Fprintf(&b, "- %s/%s: the user's home folder (%s).\n", GuestRoot, m.At, access)
		case m.At == GuestHome:
			fmt.Fprintf(&b, "- %s/%s: the folder %q of the user (%s). You cannot see the user's other files.\n", GuestRoot, m.At, path.Base(m.Path), access)
		case strings.HasPrefix(m.At, GuestFolderPrefix):
			fmt.Fprintf(&b, "- %s/%s: the folder %q of the user (%s). You cannot see the user's other files.\n", GuestRoot, m.At, path.Base(m.Path), access)
		case m.At == GuestChat:
			fmt.Fprintf(&b, "- %s/%s: this chat's own folder (%s). Files the user attaches are here; save working files in %s/%s/%s/.\n", GuestRoot, m.At, access, GuestRoot, m.At, workfiles.Dir)
		default:
			fmt.Fprintf(&b, "- %s/%s: a folder shared with the user (%s).\n", GuestRoot, m.At, access)
		}
	}
	if v.NotMounted > 0 {
		fmt.Fprintf(&b, "%d shared folders are not mounted: they did not fit the access limit of this chat.\n", v.NotMounted)
	}
	b.WriteString("There is nothing else under " + GuestRoot + ". Every change is saved, and the user can undo it.")
	return b.String()
}
