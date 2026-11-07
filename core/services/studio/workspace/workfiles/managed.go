package workfiles

import "strings"

// UsersRoot holds one home folder per principal: /users/<principal>.
const UsersRoot = "/users"

// The folders of a home whose files the host reads with the owner's own
// authority, apart from ChatsDir, PagesDir and SettingsDir: the host puts
// their text into the owner's chats, or runs it as the owner's page.
const (
	// SkillsDir holds skills. The host loads the one at the top of the home
	// (the personal layer) and every one below a folder of the home (the
	// folder layer).
	SkillsDir = ".skills"
	// MemoryDir holds the notes of the built-in memory, which the host puts
	// into the prompt of the owner's chats.
	MemoryDir = "Memory"
)

// Managed reports whether rel, a path inside a home ("/a/b" or "a/b"; "/" is
// the home), is or is inside a folder that the host reads with the owner's
// authority or keeps for itself: LocalAI/ (settings, agent profiles and agent
// skills), Chats/, Memory/, pages/, or a .skills folder at any depth. Nobody
// else may get a grant there: a person cannot share such a folder, and a
// folder that holds a grant cannot be moved there. Names are compared without
// case, as the workspace compares them for uniqueness.
func Managed(rel string) bool {
	segs := strings.Split(strings.Trim(rel, "/"), "/")
	if len(segs) == 0 || segs[0] == "" {
		return false
	}
	for _, top := range []string{SettingsDir, ChatsDir, MemoryDir, PagesDir} {
		if strings.EqualFold(segs[0], top) {
			return true
		}
	}
	for _, s := range segs {
		if strings.EqualFold(s, SkillsDir) {
			return true
		}
	}
	return false
}

// HomeOf splits a workspace path into the principal whose home holds it and
// the path inside that home ("/" for the home itself). ok is false for a path
// outside every home, /users included.
func HomeOf(p string) (principal, rel string, ok bool) {
	rest, found := strings.CutPrefix(p, UsersRoot+"/")
	if !found || rest == "" {
		return "", "", false
	}
	principal, rel, _ = strings.Cut(rest, "/")
	if principal == "" {
		return "", "", false
	}
	return principal, "/" + rel, true
}

// GrantsMayMove reports whether a folder that holds a grant, or has one on a
// folder below it, may move from the workspace path from to the path to. A
// grant moves with its folder, so the move must not take it into another
// home (where the owner of that home did not give it and its giver can no
// longer take it back) or out of a home or into one, and must not take it
// into a folder that Managed reports.
func GrantsMayMove(from, to string) bool {
	fromOwner, _, fromHome := HomeOf(from)
	toOwner, toRel, toHome := HomeOf(to)
	if fromHome != toHome || fromOwner != toOwner {
		return false
	}
	return !toHome || !Managed(toRel)
}
