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
	"github.com/mudler/LocalAI/core/services/studio/workspace"
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

// readChatFile reads a file of the chat's access, at most limit bytes. A
// chat of a scoped agent reads only inside its folders.
func (s *Service) readChatFile(ctx context.Context, x session.Access, p string, limit int64) ([]byte, error) {
	r, n, err := s.d.Files.Store().Open(ctx, x.Actor(), p)
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

// turnFiles are the files that a turn of a chat made or changed, among those
// the chat's access reaches. turn is the number of the turn; 0 means it is
// not known, and the files changed since the turn began are taken instead.
func (c *Connectors) turnFiles(ctx context.Context, b connectors.Binding, x session.Access, chatID string, turn int, since time.Time) []session.ChatFile {
	if !c.svc.d.Files.Available() {
		return nil
	}
	cf, err := c.svc.mgr.FilesIn(ctx, principal(b.Owner), chatID, x.Place())
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
			out = append(out, f)
		}
	}
	return out
}

// outFile is a file on its way to a sender. made says that the turn made or
// changed it, so the sender may be told "I made"; else the agent attached it.
type outFile struct {
	path, name string
	made       bool
}

// outgoing lists what goes with the answer: the files the turn made or
// changed, then the files the agent attached, each path once.
func (c *Connectors) outgoing(ctx context.Context, b connectors.Binding, x session.Access, chatID string, turn int, since time.Time) []outFile {
	var out []outFile
	seen := map[string]bool{}
	for _, f := range c.turnFiles(ctx, b, x, chatID, turn, since) {
		if !seen[f.Path] {
			seen[f.Path] = true
			out = append(out, outFile{path: f.Path, name: contract.SafeFileName(f.Name), made: true})
		}
	}
	for _, p := range c.takeAttached(x.Actor().Principal, chatID) {
		if !seen[p] {
			seen[p] = true
			out = append(out, outFile{path: p, name: contract.SafeFileName(path.Base(p))})
		}
	}
	return out
}

// sendFiles hands the files of a turn to the sender after the answer: those
// the turn made and those the agent attached. A file that cannot go is named
// in one plain message, with the reason.
func (c *Connectors) sendFiles(b connectors.Binding, token string, m contract.ConnectorMessage, chatID string, turn int, since time.Time) {
	if !b.SendsFiles() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// The files go with the chat's access, resolved now: a scoped agent's
	// reply sends nothing from outside its folders, whatever was marked.
	x, err := c.svc.ChatAccess(ctx, b.Owner, chatID)
	if err != nil {
		c.log.Warn("studio: the files of a connector turn are not sent", "binding", b.ID, "error", err)
		return
	}
	files := c.outgoing(ctx, b, x, chatID, turn, since)
	var notes []string
	for i, f := range files {
		if i == maxFilesPerTurn {
			notes = append(notes, fmt.Sprintf("%d more files are in the chat's files.", len(files)-i))
			break
		}
		mimeType, ok := outboundType(f.name)
		if !ok {
			if f.made {
				notes = append(notes, fmt.Sprintf("I made %s, but files of this type are not sent here.", f.name))
			} else {
				notes = append(notes, fmt.Sprintf("%s was not sent: files of this type are not sent here.", f.name))
			}
			continue
		}
		data, err := c.svc.readChatFile(ctx, x, f.path, contract.MaxConnectorFileBytes)
		switch {
		case errors.Is(err, workspace.ErrNotFound):
			continue // gone since, or outside the chat's access: not named
		case errors.Is(err, errFileTooLarge):
			notes = append(notes, fmt.Sprintf("%s is too large to send here (the limit is 1 MB). It is in the chat's files.", f.name))
			continue
		case err != nil:
			c.log.Warn("studio: could not read a file for a connector", "binding", b.ID, "error", err)
			notes = append(notes, fmt.Sprintf("I could not read %s.", f.name))
			continue
		case len(data) == 0:
			continue // an empty file says nothing, and platforms refuse it
		}
		err = c.send(ctx, b, contract.ConnectorRequest{Op: contract.ConnectorOpSendFile, Token: token, Chat: m.Chat, Thread: m.Thread,
			File: &contract.ConnectorFile{Name: f.name, MIME: mimeType, Data: data}})
		if err != nil {
			c.log.Warn("studio: could not send a file to a connector", "binding", b.ID, "kind", b.Kind, "error", err)
			notes = append(notes, fmt.Sprintf("I could not send %s%s.", f.name, sendReason(err)))
		}
	}
	if len(notes) > 0 {
		c.say(b, token, m.Chat, m.Thread, strings.Join(notes, "\n"))
	}
}

// maxReasonRunes bounds the platform's reason in a message to the sender.
const maxReasonRunes = 160

// sendReason is ": <why>" when the add-on says why the platform refused a
// file (a missing permission, say), cut to one short line, and "" otherwise.
func sendReason(err error) string {
	var ce *contract.ConnectorError
	if !errors.As(err, &ce) {
		return ""
	}
	why := strings.Join(strings.Fields(ce.Message), " ")
	if r := []rune(why); len(r) > maxReasonRunes {
		why = string(r[:maxReasonRunes]) + "…"
	}
	if why == "" {
		return ""
	}
	return ": " + why
}
