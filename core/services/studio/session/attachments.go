package session

import (
	"fmt"
	"path"
	"strings"
)

// Attachment is a file the user attached to a message. The transcript keeps
// it apart from the words of the message: the person sees the words and the
// files, and the model gets the words followed by the list of guest paths,
// which ComposeMessage writes when the message goes to the model.
type Attachment struct {
	// Path is the path inside the user's home when the file was attached,
	// such as "/Chats/2026-10-29 Budget/q1.csv": a path of the files API.
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size,omitempty"`
	Mime string `json:"mime,omitempty"`
	// Version is the version of the file that the message carried.
	Version int64 `json:"version,omitempty"`
	// Missing is set on a read when the file is no longer in the user's files.
	// It is never stored.
	Missing bool `json:"-"`
}

// GuestPath is the path the model's tools use for the file.
func (a Attachment) GuestPath() string { return guestHomeRoot + a.Path }

// guestHomeRoot is the home folder as the model's tools see it.
const guestHomeRoot = GuestRoot + "/" + GuestHome

// attachedHead starts the list of files that ComposeMessage writes.
const attachedHead = "The user attached these files:\n"

// ComposeMessage is the text the model gets for a user message: the words,
// then note (the text read from the files, such as a voice note's transcript),
// then the guest paths of the files. A message sent before the transcript kept
// the files apart holds this text as it is.
func ComposeMessage(text, note string, atts []Attachment) string {
	body := strings.TrimSpace(text + note)
	if len(atts) == 0 {
		return body
	}
	var b strings.Builder
	if body != "" {
		b.WriteString(body + "\n\n")
	}
	b.WriteString(attachedHead)
	for _, a := range atts {
		b.WriteString("- " + a.GuestPath() + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// SplitMessage is ComposeMessage reversed: it takes the lists of files and the
// text read from them out of text and returns the words that are left. It
// reads one message or several joined by blank lines, as waiting messages are
// joined into one turn. Text that only looks like a list stays in body. An
// attachment it returns has no size and no version: the text does not say.
func SplitMessage(text string) (body, note string, atts []Attachment) {
	var kept, notes strings.Builder
	from, search := 0, 0
	for {
		i := strings.Index(text[search:], attachedHead)
		if i < 0 {
			break
		}
		at := search + i
		search = at + len(attachedHead)
		// The list starts the message or follows a blank line.
		start := at
		if at > 0 {
			if !strings.HasSuffix(text[:at], "\n\n") || at-2 < from {
				continue
			}
			start = at - 2
		}
		found, end := listAt(text, at+len(attachedHead))
		if len(found) == 0 {
			continue
		}
		// The text read from these files sits just before their list.
		seg := text[from:start]
		cut := len(seg)
		for _, a := range found {
			for _, m := range readMarkers(a.Name) {
				if j := markerAt(seg, m); j >= 0 && j < cut {
					cut = j
				}
			}
		}
		if cut < len(seg) {
			kept.WriteString(strings.TrimRight(seg[:cut], "\n"))
			notes.WriteString("\n\n" + seg[cut:])
		} else {
			kept.WriteString(seg)
		}
		atts = append(atts, found...)
		from, search = end, end
	}
	if len(atts) == 0 {
		return text, "", nil
	}
	kept.WriteString(text[from:])
	return strings.TrimSpace(kept.String()), notes.String(), atts
}

// listAt reads the lines "- <guest path>" that start at pos and returns the
// files and the end of the last line.
func listAt(text string, pos int) ([]Attachment, int) {
	var out []Attachment
	end := pos
	for pos < len(text) {
		line, next := text[pos:], len(text)
		if nl := strings.IndexByte(line, '\n'); nl >= 0 {
			line, next = line[:nl], pos+nl+1
		}
		g, ok := strings.CutPrefix(line, "- ")
		if !ok || !strings.HasPrefix(g, guestHomeRoot+"/") {
			break
		}
		rel := strings.TrimPrefix(g, guestHomeRoot)
		name := path.Base(rel)
		out = append(out, Attachment{Path: rel, Name: name, Mime: MimeOf(name)})
		end, pos = pos+len(line), next
	}
	return out, end
}

// readMarkers are the starts of the notes that the media service adds for a
// file: a voice note's transcript and a document's text.
func readMarkers(name string) []string {
	return []string{
		"[Voice note " + name + ", transcribed: ",
		"[Text of " + name + ":\n",
		"[Document " + name + " has no text layer.",
		"[Document " + name + " contains no text.",
	}
}

// markerAt is the first index of m in seg at the start of seg or after a
// blank line, or -1.
func markerAt(seg, m string) int {
	for off := 0; off < len(seg); {
		j := strings.Index(seg[off:], m)
		if j < 0 {
			return -1
		}
		j += off
		if j == 0 || strings.HasSuffix(seg[:j], "\n\n") {
			return j
		}
		off = j + 1
	}
	return -1
}

// DisplayEntry is the entry as the person reads it. A user message stored
// before the transcript kept the files apart holds the list of files in its
// text; DisplayEntry moves the list to Attachments and the text read from the
// files to Note. Any other entry is returned as it is.
func DisplayEntry(e Entry) Entry {
	if e.Role != RoleUser || len(e.Attachments) > 0 {
		return e
	}
	body, note, atts := SplitMessage(e.Text)
	if len(atts) == 0 {
		return e
	}
	e.Text, e.Note, e.Attachments = body, e.Note+note, atts
	return e
}

// ModelText is the text the model gets for an entry: for a user message, the
// words, the text read from the files and the list of the files.
func ModelText(e Entry) string {
	text := e.Text
	if e.Role == RoleUser && (len(e.Attachments) > 0 || e.Note != "") {
		text = ComposeMessage(e.Text, e.Note, e.Attachments)
	}
	if e.Role == RoleUser && e.Loop != nil {
		text = LoopPreamble(*e.Loop) + "\n\n" + text
	}
	return text
}

// LoopPreamble is the line the model reads before the prompt of a loop run,
// so that it knows nobody typed the message just now. A self-paced run also
// learns how to ask for the next run.
func LoopPreamble(m LoopMark) string {
	s := fmt.Sprintf("[Loop run %d: this message repeats by itself; the person did not type it now.", m.Run)
	if m.SelfPaced {
		s += " When this run is done, call schedule_next to say when to run again, or loop_done when the task is complete. If you call neither, the loop stops."
	}
	return s + "]"
}
