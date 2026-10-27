package workspace

import "strings"

// GenericTitle names a change set by its kind alone, without paths.
func GenericTitle(cs ChangeSet) string {
	switch cs.Kind {
	case ChangeUndo:
		return "Undo"
	case ChangeAgent:
		return "Agent changes"
	case ChangeUpload:
		return "Upload"
	}
	switch {
	case strings.HasPrefix(cs.Title, "Move "):
		return "Move"
	case strings.HasPrefix(cs.Title, "Delete "):
		return "Delete"
	}
	return "Changes"
}

// Within reports whether p is dir or a path below it.
func Within(p, dir string) bool {
	return p == dir || dir == "/" || strings.HasPrefix(p, dir+"/")
}
