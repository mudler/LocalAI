package runner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// maxConflictLines bounds the paths the note of one call names.
const maxConflictLines = 10

// ConflictNote is what a call's output ends with when the commit did not save
// every path as the command left it: one line a path, with the path as the
// guest sees it and, for a file kept as a copy, where the copy is. The model
// can act on each line, and the user reads the same lines in the step.
func ConflictNote(conflicts []workspace.Conflict, mounts []workspace.Mount) string {
	var b strings.Builder
	for i, c := range conflicts {
		if i == maxConflictLines {
			fmt.Fprintf(&b, "\n[and %d more paths were not saved as the command left them]", len(conflicts)-i)
			break
		}
		p := GuestPath(c.Path, c.Reason, mounts)
		b.WriteString("\n[")
		switch c.Reason {
		case workspace.ReasonChanged:
			if c.Kept != "" {
				fmt.Fprintf(&b, "%s changed in the user's files while the command ran: their version stays at %s, and the command's version was saved as %s", p, p, GuestPath(c.Kept, "", mounts))
			} else {
				fmt.Fprintf(&b, "%s changed in the user's files while the command ran: the command's change to it was not saved", p)
			}
		case workspace.ReasonOutsideView:
			fmt.Fprintf(&b, "%s was not saved: it is outside the user's files. Only what is under %s is kept; write it there", p, strings.Join(writable(mounts), " or "))
		case workspace.ReasonReadOnly:
			fmt.Fprintf(&b, "%s was not saved: that folder is read-only", p)
		case workspace.ReasonNameTaken:
			fmt.Fprintf(&b, "%s was not saved: something else has that name in the user's files now", p)
		case workspace.ReasonParentDeleted:
			fmt.Fprintf(&b, "%s was not saved: its folder was deleted from the user's files meanwhile", p)
		case workspace.ReasonUnsupported:
			fmt.Fprintf(&b, "%s was not saved: links and special files are not kept", p)
		case workspace.ReasonBadName:
			fmt.Fprintf(&b, "%s was not saved: the name is not allowed (no control characters, no . or ..)", p)
		case workspace.ReasonTooLarge:
			fmt.Fprintf(&b, "%s was not saved: the file is too large", p)
		default:
			fmt.Fprintf(&b, "%s was not saved (%s)", p, c.Reason)
		}
		if c.Reason != workspace.ReasonChanged && c.Kept != "" {
			fmt.Fprintf(&b, "; the command's version was saved as %s", GuestPath(c.Kept, "", mounts))
		}
		b.WriteString("]")
	}
	return b.String()
}

// GuestPath names a path of a commit as the guest sees it. A path outside
// every mount (reason outside-view) is a path of the view already; any other
// is a path of the store, inside one of the mounts.
func GuestPath(p, reason string, mounts []workspace.Mount) string {
	if reason == workspace.ReasonOutsideView {
		return GuestRoot + "/" + strings.TrimPrefix(p, "/")
	}
	ms := append([]workspace.Mount(nil), mounts...)
	sort.Slice(ms, func(i, j int) bool { return len(ms[i].Path) > len(ms[j].Path) })
	for _, m := range ms {
		base := strings.TrimSuffix(m.Path, "/")
		switch {
		case p == m.Path || p == base:
			return GuestRoot + "/" + m.At
		case strings.HasPrefix(p, base+"/"):
			return GuestRoot + "/" + m.At + strings.TrimPrefix(p, base)
		}
	}
	return p
}

func writable(mounts []workspace.Mount) []string {
	var out []string
	for _, m := range mounts {
		if m.Mode >= workspace.ModeWrite {
			out = append(out, GuestRoot+"/"+m.At+"/")
		}
	}
	if len(out) == 0 {
		out = []string{GuestRoot + "/home/"}
	}
	return out
}
