// Package workfiles is the one place that knows a chat's working files: the
// scripts, helper code, intermediate data, logs and drafts that a chat makes
// on the way to what the user asked for. It names the work folder of a chat,
// tells a working file from a deliverable by its path, and holds the text
// that steers the model to put working files in that folder. The chat's file
// list, the cards of its steps and the tool descriptions all read it, so the
// interface and the model never disagree on what a working file is.
package workfiles

import (
	"path"
	"strings"
)

// Folder names. ChatsDir, PagesDir and SettingsDir are the folders of the
// home that the session and the pages define; tests keep them equal.
const (
	// ChatsDir holds one folder per chat.
	ChatsDir = "Chats"
	// Dir is the work folder inside a chat folder: Chats/<chat>/work.
	Dir = "work"
	// PagesDir holds the user's pages, which are deliverables, code included.
	PagesDir = "pages"
	// SettingsDir holds the settings files LocalAI reads.
	SettingsDir = "LocalAI"
)

// The kinds of a file.
const (
	// Deliverable is what the user asked for, or a file of theirs.
	Deliverable = "deliverable"
	// Working is a file the chat made on the way: a script, a log, a draft.
	Working = "working"
)

// guestHome is the home folder as the model's tools see it, and
// guestSharedPrefix the start of a shared folder there.
const (
	guestHome         = "/workspace/home"
	guestSharedPrefix = "/workspace/shared-"
)

// helperExts are the extensions of scripts, logs and temporary files.
var helperExts = map[string]bool{
	".py": true, ".sh": true, ".js": true, ".ts": true, ".mjs": true, ".rb": true,
	".pl": true, ".ps1": true, ".bat": true, ".log": true, ".tmp": true,
}

// helperDirs are folder names that hold helper files, in any case.
var helperDirs = map[string]bool{"scripts": true, "tmp": true, "scratch": true}

// Folder returns the work folder of a chat as a path of the home ("/" is the
// home), or "" when the chat has no folder name yet.
func Folder(chatFolder string) string {
	if chatFolder == "" {
		return ""
	}
	return "/" + ChatsDir + "/" + chatFolder + "/" + Dir
}

// GuestFolder is Folder as the model's tools see it, with a final "/".
func GuestFolder(chatFolder string) string {
	if chatFolder == "" {
		return ""
	}
	return guestHome + Folder(chatFolder) + "/"
}

// segments splits a path of the home into its names. A guest path of the
// home is read as the same path; shared is true for a shared folder, whose
// files belong to someone's project and are never classified.
func segments(p string) (segs []string, shared bool) {
	switch {
	case strings.HasPrefix(p, guestSharedPrefix):
		return nil, true
	case p == guestHome || strings.HasPrefix(p, guestHome+"/"):
		p = strings.TrimPrefix(p, guestHome)
	}
	p = path.Clean("/" + p)
	if p == "/" {
		return nil, false
	}
	return strings.Split(strings.TrimPrefix(p, "/"), "/"), false
}

// inWorkFolder reports whether the names are below the work folder of a chat.
func inWorkFolder(segs []string) bool {
	return len(segs) >= 4 && strings.EqualFold(segs[0], ChatsDir) && segs[2] == Dir
}

// Kind classifies a file by its path: a path of the home ("/report.docx"),
// a guest path (/workspace/home/..., /workspace/shared-<name>/...). A file
// below the work folder of any chat is Working; so is a script, a log or a
// temporary file by its extension, or a file in a folder named scripts, tmp
// or scratch. Pages, the settings folder and the shared folders hold
// deliverables, whatever the files are. Everything else is a Deliverable.
func Kind(p string) string {
	segs, shared := segments(p)
	switch {
	case shared || len(segs) == 0:
		return Deliverable
	case inWorkFolder(segs):
		return Working
	case segs[0] == PagesDir || segs[0] == SettingsDir:
		return Deliverable
	}
	for _, d := range segs[:len(segs)-1] {
		if helperDirs[strings.ToLower(d)] {
			return Working
		}
	}
	if helperExts[strings.ToLower(path.Ext(segs[len(segs)-1]))] {
		return Working
	}
	return Deliverable
}

// Stray reports whether a file looks like a helper (Kind is Working) but is
// not in a work folder: the chat left a script next to the user's files.
func Stray(p string) bool {
	segs, _ := segments(p)
	return Kind(p) == Working && !inWorkFolder(segs)
}

// ToolHint ends the descriptions of bash and write_file. Tool descriptions
// are the same for every chat, so it points at the prompt, which names the
// folder; with the steering off the prompt names none and the hint is moot.
// The Sandbox add-on has 400 characters for its bash: keep this short.
const ToolHint = "Put helper scripts and temp files in the chat's work folder if the prompt names one."

// Prompt is the block of the system prompt that names the work folder of a
// chat (guest path, ending in "/") and says what goes in it.
func Prompt(guestFolder string) string {
	return "Working files: put scripts, helper code, intermediate data, logs, drafts and anything else the user did not ask for in this chat's work folder, " +
		guestFolder + " (create it with mkdir -p when it is missing). " +
		"Save the deliverable the user asked for (the document, the spreadsheet, the image) where the user said, else in " + guestHome + "/, and nowhere else. " +
		"Never create new top-level folders in the home for scripts. A command you run once needs no file: use python3 -c or a heredoc. " +
		"Pages (" + guestHome + "/" + PagesDir + "/<name>/) and " + guestHome + "/" + SettingsDir + "/ are deliverables: keep them where they are."
}

// Nudge is the line a tool result gets when the call left a helper file
// outside the work folder (guest path, ending in "/").
func Nudge(guestFolder, file string) string {
	return "Note: " + file + " looks like a helper file; helper files belong in " + guestFolder + ". Consider moving it there unless the user asked for it here."
}
