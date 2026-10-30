package studio

import (
	"context"
	"encoding/json"
	"path"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// ChatFile is one file of a chat, as the chats API lists it.
type ChatFile struct {
	session.ChatFile
	// Media is true for a file the user attached that the model received as
	// an image or a video inside its message, because the chat's model takes
	// that input natively. Other files reach the model by path only.
	Media bool
}

// ChatFiles is the file index of a chat.
type ChatFiles struct {
	Files []ChatFile
	// Folder is the guest path of the chat folder; "" until a file is attached.
	Folder string
	// Stale are the old chat folder paths that earlier messages name.
	Stale []string
}

// ChatFiles lists the files of a conversation: the files in its folder and
// the files its turns created or changed elsewhere in the home folder, with
// their current paths. Another user's conversation is session.ErrNotFound.
func (s *Service) ChatFiles(ctx context.Context, user, conv string) (ChatFiles, error) {
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return ChatFiles{}, err
	}
	if !s.d.Files.Available() {
		return ChatFiles{Files: []ChatFile{}}, nil
	}
	cf, err := s.mgr.Files(ctx, p, conv)
	if err != nil {
		return ChatFiles{}, err
	}
	var m Modalities
	if s.d.Modalities != nil {
		m = s.d.Modalities(rec.Model)
	}
	out := ChatFiles{Files: make([]ChatFile, 0, len(cf.Files)), Folder: cf.Folder, Stale: cf.Stale}
	for _, f := range cf.Files {
		out.Files = append(out.Files, ChatFile{ChatFile: f, Media: f.Origin == session.FileFromUser && sentAsMedia(m, f)})
	}
	return out, nil
}

// sentAsMedia mirrors nativeMedia: the types and limits that put a file in
// the message as an image or a video.
func sentAsMedia(m Modalities, f session.ChatFile) bool {
	ext := strings.ToLower(path.Ext(f.Name))
	switch {
	case m.Vision && imageTypes[ext]:
		return f.Size <= maxNativeImage
	case m.Video && videoTypes[ext] != "":
		return f.Size <= maxNativeVideo
	}
	return false
}

// filesPrompt is the block of the turn prompt that lists the chat's files. It
// is "" when the chat has none, or when the index cannot be read: a turn
// never fails for it.
func (s *Service) filesPrompt(ctx context.Context, a workspace.Actor) string {
	if !s.d.Files.Available() {
		return ""
	}
	cf, err := s.mgr.Files(ctx, a.Principal, a.ChatID)
	if err != nil {
		s.log.Warn("studio: the chat's files are not in the prompt", "chat", a.ChatID, "error", err)
		return ""
	}
	return session.FilesPrompt(cf)
}

// fileWriteTools are the file tools that change files.
var fileWriteTools = map[string]bool{"write_file": true, "edit_file": true, "move_file": true, "delete_file": true}

// announceFileChanges wraps the file tools that change files so that a
// successful call tells the chat's clients that its files changed.
func (s *Service) announceFileChanges(a workspace.Actor, tools []session.Tool) []session.Tool {
	k := key(a.Principal, a.ChatID)
	for i, t := range tools {
		if !fileWriteTools[t.Name] {
			continue
		}
		run := t.Run
		tools[i].Run = func(ctx context.Context, args json.RawMessage) (string, error) {
			out, err := run(ctx, args)
			if err == nil {
				s.bus.Publish(k, session.Event{Kind: session.KindFiles})
			}
			return out, err
		}
	}
	return tools
}
