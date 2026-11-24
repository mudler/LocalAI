package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const attachFileName = "attach_file"

const attachFileDescription = "Give the person a file that already exists in your files: it is sent to them with your answer. " +
	"Files you make or change in a turn are attached without this call; use it for a file from an earlier turn or from a folder you were given. " +
	"path is the exact path that your file tools or bash showed, such as /workspace/home/Docs/plan.pdf. " +
	"Only this call sends a file: never write a file name or a paperclip line as if it were sent. " +
	"A file that cannot be attached comes back with the reason: tell the person plainly."

const attachFileSchema = `{"type":"object","properties":{"path":{"type":"string","description":"The path of the file, such as /workspace/chat/report.docx."}},"required":["path"]}`

// attachTools returns attach_file for a chat that a connector started whose
// binding sends files. Any other chat has no such tool: a person in Studio
// sees the files of the chat there. The tool only marks the file; sendFiles
// reads and sends it after the answer, through the send_file op that any
// connector add-on serves.
func (s *Service) attachTools(ctx context.Context, x session.Access, rec session.ChatRecord) []session.Tool {
	c := s.connectors
	if c == nil || rec.Origin != session.OriginConnector || rec.Kind == session.KindSubAgent || !s.d.Files.Available() {
		return nil
	}
	b, err := c.o.Store.GetBinding(ctx, rec.OriginID)
	if err != nil || !b.SendsFiles() {
		return nil
	}
	return []session.Tool{{
		Name: attachFileName, Description: attachFileDescription, Schema: json.RawMessage(attachFileSchema),
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			return c.attachFile(ctx, x, b, raw)
		},
	}}
}

// attachFile checks the file against the chat's access and the limits of a
// sender, and marks it for the answer. The check is repeated when the file is
// sent, because the file or the access can change in between; this one lets
// the model tell the person at once why a file cannot go.
func (c *Connectors) attachFile(ctx context.Context, x session.Access, b connectors.Binding, raw json.RawMessage) (string, error) {
	in, err := session.DecodeArgs[struct{ Path string }](raw)
	if err != nil {
		return "", err
	}
	t, err := x.Place().Resolve(in.Path)
	if err != nil {
		return "", notFound(err, in.Path)
	}
	n, err := c.svc.d.Files.Store().Stat(ctx, x.Actor(), t.Abs)
	if err != nil {
		return "", notFound(err, in.Path)
	}
	name := contract.SafeFileName(n.Name)
	limits := c.FileLimits(ctx, b)
	switch {
	case n.Kind == workspace.KindDir:
		return "", fmt.Errorf("%s is a folder, not a file; attach the files in it one by one", in.Path)
	case !outboundAllowed(name):
		return "", fmt.Errorf("%s is not sent: files of this type (%s) are not sent here. Documents, data, pictures, sound, video and zip files are", name, path.Ext(name))
	case n.Size == 0:
		return "", fmt.Errorf("%s is empty, so there is nothing to send", name)
	case limits.Send.Bytes <= 0:
		return "", fmt.Errorf("files cannot be sent on this platform: paste short content in the answer instead")
	case n.Size > limits.Send.Bytes:
		return "", refuseAttachTooBig(name, n.Size, limits)
	}
	key := key(x.Actor().Principal, x.Actor().ChatID)
	c.amu.Lock()
	defer c.amu.Unlock()
	marked := c.attached[key]
	if slices.Contains(marked, t.Abs) {
		return name + " is attached already.", nil
	}
	if len(marked) >= maxFilesPerTurn {
		return "", fmt.Errorf("at most %d files go with one answer, and %d are marked already; tell the person which one is left out", maxFilesPerTurn, len(marked))
	}
	if c.attached == nil {
		c.attached = map[string][]string{}
	}
	c.attached[key] = append(marked, t.Abs)
	return name + " is attached: it is sent to the person after your answer. Do not give them a path.", nil
}

// takeAttached returns the files that the chat's turn marked, in the order of
// the calls, and forgets them.
func (c *Connectors) takeAttached(principal, chatID string) []string {
	c.amu.Lock()
	defer c.amu.Unlock()
	k := key(principal, chatID)
	out := c.attached[k]
	delete(c.attached, k)
	return out
}

// outboundAllowed says whether a file of this name goes to a sender.
func outboundAllowed(name string) bool {
	_, ok := outboundType(name)
	return ok
}

// notFound tells the model that a path is not a file it can reach. A path
// outside the chat's access and a path that does not exist look the same.
func notFound(err error, p string) error {
	if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrReadOnly) {
		return fmt.Errorf("%s was not found", p)
	}
	return fmt.Errorf("%s cannot be attached: %w", p, err)
}
