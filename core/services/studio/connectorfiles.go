package studio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// maxFilesPerTurn is how many files of one turn go to a sender; the others
// stay in the chat.
const maxFilesPerTurn = 5

// outboundTypes are the files that go to a sender, by extension, with the
// type the platform gets. Documents, data, pictures, sound, video and
// archives go; programs, scripts and pages that a client could run do not.
var outboundTypes = map[string]string{
	".txt": "text/plain", ".md": "text/markdown", ".log": "text/plain",
	".csv": "text/csv", ".tsv": "text/tab-separated-values",
	".json": "application/json", ".yaml": "application/yaml", ".yml": "application/yaml", ".xml": "application/xml",
	".pdf":  "application/pdf",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".odt":  "application/vnd.oasis.opendocument.text", ".ods": "application/vnd.oasis.opendocument.spreadsheet",
	".odp": "application/vnd.oasis.opendocument.presentation",
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg", ".m4a": "audio/mp4",
	".mp4": "video/mp4", ".webm": "video/webm",
	".zip": "application/zip",
}

// outboundType is the type a file goes with, and false for a file that does
// not go.
func outboundType(name string) (string, bool) {
	t, ok := outboundTypes[strings.ToLower(path.Ext(name))]
	return t, ok
}

var errFileTooLarge = errors.New("the file is too large")

// readChatFile reads a file of the user's workspace, at most limit bytes.
func (s *Service) readChatFile(ctx context.Context, user, p string, limit int64) ([]byte, error) {
	r, n, err := s.d.Files.Store().Open(ctx, s.d.Files.Actor(principal(user)), p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	if n.Size > limit {
		return nil, errFileTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errFileTooLarge
	}
	return data, nil
}

// turnFiles are the files that a turn of a chat made or changed. turn is the
// number of the turn; 0 means it is not known, and the files changed since
// the turn began are taken instead.
func (c *Connectors) turnFiles(ctx context.Context, b connectors.Binding, chatID string, turn int, since time.Time) []session.ChatFile {
	if !c.svc.d.Files.Available() {
		return nil
	}
	cf, err := c.svc.ChatFiles(ctx, b.Owner, chatID)
	if err != nil {
		c.log.Warn("studio: could not list the files of a connector turn", "binding", b.ID, "error", err)
		return nil
	}
	var out []session.ChatFile
	for _, f := range cf.Files {
		if f.Origin == session.FileFromUser {
			continue
		}
		if (turn > 0 && f.Turn == turn) || (turn == 0 && !f.At.Before(since)) {
			out = append(out, f.ChatFile)
		}
	}
	return out
}

// sendFiles hands the files of a turn to the sender after the answer. A file
// that cannot go is named in one plain message, with the reason.
func (c *Connectors) sendFiles(b connectors.Binding, token string, m contract.ConnectorMessage, chatID string, turn int, since time.Time) {
	if !b.SendsFiles() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	files := c.turnFiles(ctx, b, chatID, turn, since)
	var notes []string
	for i, f := range files {
		if i == maxFilesPerTurn {
			notes = append(notes, fmt.Sprintf("%d more files are in the chat's files.", len(files)-i))
			break
		}
		name := contract.SafeFileName(f.Name)
		mimeType, ok := outboundType(name)
		if !ok {
			notes = append(notes, fmt.Sprintf("I made %s, but files of this type are not sent here.", name))
			continue
		}
		data, err := c.svc.readChatFile(ctx, b.Owner, f.Path, contract.MaxConnectorFileBytes)
		switch {
		case errors.Is(err, errFileTooLarge):
			notes = append(notes, fmt.Sprintf("%s is too large to send here (the limit is 1 MB). It is in the chat's files.", name))
			continue
		case err != nil:
			c.log.Warn("studio: could not read a file for a connector", "binding", b.ID, "error", err)
			notes = append(notes, fmt.Sprintf("I could not read %s.", name))
			continue
		case len(data) == 0:
			continue // an empty file says nothing, and platforms refuse it
		}
		err = c.send(ctx, b, contract.ConnectorRequest{Op: contract.ConnectorOpSendFile, Token: token, Chat: m.Chat, Thread: m.Thread,
			File: &contract.ConnectorFile{Name: name, MIME: mimeType, Data: data}})
		if err != nil {
			c.log.Warn("studio: could not send a file to a connector", "binding", b.ID, "kind", b.Kind, "error", err)
			notes = append(notes, fmt.Sprintf("I could not send %s.", name))
		}
	}
	if len(notes) > 0 {
		c.say(b, token, m.Chat, m.Thread, strings.Join(notes, "\n"))
	}
}
