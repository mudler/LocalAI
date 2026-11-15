package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// A restart can cut a tool call in two: the call ran, or began to, and the pod
// died before its result line was stored. The transcript is the checkpoint of
// a turn, and it holds no row for a call in flight. Running the call again
// could do its work twice (send a message, write a file, start a job).
//
// So a call of a tool that may change something leaves a RoleToolIntent line
// before it runs. The result line follows when the call ends. A turn that is
// resumed reads the transcript: an intent with no result after it is a call
// whose outcome is not known, and the model is told so, to check before it
// repeats the step. A tool the host marked Tool.ReadOnly leaves no line: running
// it twice is harmless.

// readOnlyTools are the tools that only read, by name, for the ones that
// nothing else classifies: the sandbox tools, the file tools, the helper-model
// tools, the collection tools and the search and read tools of the host. It is
// an explicit list on purpose: a tool that is not here and not marked
// Tool.ReadOnly is treated as one that may change something. Add-on tools carry
// their own kind (read, write, action) and are marked when the chat is built;
// the built-in tools that the registry declares ReadOnly are marked there too.
var readOnlyTools = map[string]bool{
	"read": true, "glob": true, "grep": true,
	"list_files": true, "read_file": true, "search_files": true, "read_document": true,
	"describe_image": true, "transcribe_audio": true,
	"list_collections": true, "search_collection": true,
	"web_search": true, "web_fetch": true,
	"search_chats": true, "read_chat": true, "context_read": true,
	"load_skill": true, "list_agents": true, "list_triggers": true,
	// They wait for something else and change nothing themselves.
	"wait_agents": true, "wait_for": true,
}

// ReadOnlyTool reports whether the tool name is on the list of tools that only
// read, so that a call needs no intent line.
func ReadOnlyTool(name string) bool { return readOnlyTools[name] }

// intentArgsMax bounds how much of the arguments the intent line keeps. The
// transcript already keeps a summary of every call of this size.
const intentArgsMax = 200

// Intent is a tool call that started and has no result in the transcript.
type Intent struct {
	Tool string
	Args string // the start of the arguments, on one line
}

// intentLine is the transcript line of a call about to run.
func intentLine(turn int, name string, args json.RawMessage) Entry {
	return Entry{Turn: turn, Role: RoleToolIntent, Text: name, Note: strings.Join(strings.Fields(CapText(string(args), intentArgsMax)), " ")}
}

// noteIntent stores the intent line of a call. A failed store is logged by
// appendEntry and does not stop the call: the call is what the person asked for.
func (c *Chat) noteIntent(ctx context.Context, name string, args json.RawMessage) {
	c.appendEntry(ctx, intentLine(c.turnNow(), name, args))
}

// UnresolvedIntents returns the calls of the turns since the last message of the
// person that have an intent line and no result line after it, oldest first. A
// result line is matched by the tool's name ("name: summary"), one for each
// intent, so two calls of one tool with one result leave one unresolved.
func UnresolvedIntents(entries []Entry) []Intent {
	es := SinceCleared(entries)
	start := 0
	for i, e := range es {
		if e.Role == RoleUser {
			start = i + 1
		}
	}
	var open []Intent
	for _, e := range es[start:] {
		switch e.Role {
		case RoleToolIntent:
			open = append(open, Intent{Tool: e.Text, Args: e.Note})
		case RoleTool:
			// The first open intent of the same tool is resolved by this result.
			for i, in := range open {
				if strings.HasPrefix(e.Text, in.Tool+": ") {
					open = append(open[:i], open[i+1:]...)
					break
				}
			}
		}
	}
	return open
}

// ResumePromptUnknown is ResumePrompt for a turn that was cut inside calls
// that may have changed something. It leaves out "do that step again": the
// model must check first. %s is the list of the calls.
const ResumePromptUnknown = resumeHead +
	"These calls started, but their results were lost, so it is not known whether they took effect: %s. " +
	"Check the current state first. Repeat a call only when it did not take effect, then finish the request.)"

// resumeText is what the model gets when a turn goes on after an error: the
// plain note, or the one that names the calls whose outcome is not known.
func resumeText(open []Intent, cut string) string {
	note := resumeTextIntents(open)
	if cut == "" {
		return note
	}
	return note + "\n\n" + cutNote(cut)
}

func resumeTextIntents(open []Intent) string {
	if len(open) == 0 {
		return ResumePrompt
	}
	items := make([]string, len(open))
	for i, in := range open {
		items[i] = in.Tool
		if in.Args != "" {
			items[i] += " " + in.Args
		}
	}
	return fmt.Sprintf(ResumePromptUnknown, strings.Join(items, "; "))
}
