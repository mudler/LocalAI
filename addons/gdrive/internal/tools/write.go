// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/gdrive/internal/driveapi"
	"github.com/mudler/LocalAI/addons/internal/argutil"
	"github.com/mudler/LocalAI/addons/internal/writeflow"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The write tools of the manifest. All of them are write tools: the host
// decides, from the write mode of the person's binding, whether each one is
// refused, stored as a proposal for a person, or applied at once. None of
// them shares a file, changes who can see it, or deletes it for good.
const (
	ToolCreateFile = "create-file"
	ToolUpdateFile = "update-file"
	ToolMoveFile   = "move-file"
	ToolTrashFile  = "trash-file"

	maxNameRunes   = 255
	previewRunes   = 800
	opProperty     = "localaiOp"
	defaultContent = "text/plain"
)

// MaxWriteBody is the most bytes of text the model may put in one file in one
// call. It stays small enough for a person to read before approving.
const MaxWriteBody = 64 << 10

// writeTypes are the media types a created file may have.
var writeTypes = []string{"text/plain", "text/markdown", "text/csv", "application/json"}

// WriteTools lists the write tools.
func WriteTools() []string {
	return []string{ToolCreateFile, ToolUpdateFile, ToolMoveFile, ToolTrashFile}
}

func isWrite(tool string) bool { return slices.Contains(WriteTools(), tool) }

// writeArgs holds the arguments of every write tool; a tool uses its own.
type writeArgs struct {
	Folder    string  `json:"folder"`
	FileID    string  `json:"fileId"`
	Parent    string  `json:"parent"`
	NewParent string  `json:"newParent"`
	Name      string  `json:"name"`
	Content   *string `json:"content"`
	MimeType  string  `json:"mimeType"`
}

type write struct {
	tool string
	writeArgs
}

// requireScope refuses a write when the login does not hold the optional
// scope. The sentence names the scope, not the service's own refusal.
func (c *call) requireScope() error {
	if !c.granted(ScopeWrite) {
		return bad("writing needs the %s scope. Reconnect and allow it", ScopeWriteName)
	}
	return nil
}

func checkContent(s *string, token string) error {
	if s == nil {
		return nil
	}
	if len(*s) > MaxWriteBody || !argutil.Plain(*s) {
		return bad("content is not text or is longer than %d bytes", MaxWriteBody)
	}
	if token != "" && strings.Contains(*s, token) {
		return bad("content looks like a secret so it was not sent")
	}
	return nil
}

// parseWrite checks the arguments of a write tool.
func (c *call) parseWrite(tool string, raw map[string]json.RawMessage) (write, error) {
	var a writeArgs
	w := write{tool: tool}
	if err := argutil.DecodeArgs(raw, &a); err != nil {
		return w, err
	}
	w.writeArgs = a
	if err := c.checkFolder(a.Folder); err != nil {
		return w, err
	}
	only := func(allowed ...string) error {
		given := map[string]bool{"parent": a.Parent != "", "newParent": a.NewParent != "", "name": a.Name != "", "content": a.Content != nil, "mimeType": a.MimeType != "", "fileId": a.FileID != ""}
		for k, set := range given {
			if set && !slices.Contains(allowed, k) {
				return bad("%s is not an argument of this tool", k)
			}
		}
		return nil
	}
	for _, id := range []struct{ what, v string }{{"fileId", a.FileID}, {"parent", a.Parent}, {"newParent", a.NewParent}} {
		if id.v != "" {
			if err := checkID(id.what, id.v); err != nil {
				return w, err
			}
		}
	}
	switch tool {
	case ToolCreateFile:
		if err := only("parent", "name", "content", "mimeType"); err != nil {
			return w, err
		}
		if err := argutil.CheckLine("name", a.Name, maxNameRunes, true); err != nil {
			return w, err
		}
		if a.MimeType != "" && !slices.Contains(writeTypes, a.MimeType) {
			return w, bad("mimeType is one of %s", strings.Join(writeTypes, ", "))
		}
		return w, checkContent(a.Content, c.token)
	case ToolUpdateFile:
		if err := only("fileId", "name", "content"); err != nil {
			return w, err
		}
		if a.FileID == "" {
			return w, bad("fileId is required")
		}
		if a.Name == "" && a.Content == nil {
			return w, bad("give a new name or new content or both")
		}
		if err := argutil.CheckLine("name", a.Name, maxNameRunes, false); err != nil {
			return w, err
		}
		return w, checkContent(a.Content, c.token)
	case ToolMoveFile:
		if err := only("fileId", "newParent"); err != nil {
			return w, err
		}
		if a.FileID == "" || a.NewParent == "" {
			return w, bad("fileId and newParent are required")
		}
	case ToolTrashFile:
		if err := only("fileId"); err != nil {
			return w, err
		}
		if a.FileID == "" {
			return w, bad("fileId is required")
		}
	default:
		return w, bad("unknown tool")
	}
	return w, nil
}

// target is what a write works on, as it is now.
type target struct {
	file *fileMeta
	// parent is the folder a created file goes to, or a moved file goes to.
	parent *fileMeta
}

// inspect reads the target of a write and checks the tool fits it. It returns
// the revision the preview records: a small hash of what makes the write wrong
// if it changed. It is how a stale approval is noticed.
func (c *call) inspect(w write) (target, uint64, error) {
	var t target
	if err := c.requireScope(); err != nil {
		return t, 0, err
	}
	root, err := c.root()
	if err != nil {
		return t, 0, err
	}
	switch w.tool {
	case ToolCreateFile:
		parent := w.Parent
		if parent == "" {
			parent = root
		}
		if t.parent, err = c.folderOf(parent); err != nil {
			return t, 0, err
		}
		if t.parent.Trashed {
			return t, 0, bad("the folder is in the trash")
		}
		return t, argutil.Sum64("create", t.parent.ID, t.parent.ModifiedTime), nil
	}
	if t.file, err = c.inside(w.FileID); err != nil {
		return t, 0, err
	}
	f := t.file
	if f.ID == root {
		return t, 0, bad("the allowed folder itself cannot be changed")
	}
	if len(f.Parents) == 0 {
		return t, 0, bad("a top level item of a drive cannot be changed")
	}
	switch w.tool {
	case ToolUpdateFile:
		if f.Trashed {
			return t, 0, bad("the file is in the trash")
		}
		if w.Content != nil {
			if f.isFolder() || strings.HasPrefix(f.MimeType, nativeType) || !isText(f.MimeType) {
				return t, 0, bad("only the content of a plain text file can be replaced")
			}
		}
	case ToolMoveFile:
		if f.Trashed {
			return t, 0, bad("the file is in the trash")
		}
		if w.NewParent == f.ID {
			return t, 0, bad("a folder cannot move into itself")
		}
		if t.parent, err = c.folderOf(w.NewParent); err != nil {
			return t, 0, err
		}
		if t.parent.Trashed {
			return t, 0, bad("the target folder is in the trash")
		}
		if len(f.Parents) == 1 && f.Parents[0] == t.parent.ID {
			return t, 0, bad("the file is already in that folder")
		}
	case ToolTrashFile:
		if f.isFolder() {
			return t, 0, bad("trash-file works on files and not on folders")
		}
		if f.Trashed {
			return t, 0, bad("the file is already in the trash")
		}
	}
	rev := argutil.Sum64(w.tool, f.ID, f.Version, f.ModifiedTime, f.Name, strings.Join(f.Parents, ","))
	if t.parent != nil {
		rev = argutil.Sum64(w.tool, f.ID, f.Version, f.ModifiedTime, f.Name, strings.Join(f.Parents, ","), t.parent.ID)
	}
	return t, rev, nil
}

// sample is what the person sees before a change: facts built from the
// arguments and the target, never from model prose. It stays small, since a
// chat keeps only a short tool result; the full arguments are in the proposal.
func sample(w write, t target) map[string]any {
	s := map[string]any{"tool": w.tool, "folder": w.Folder, "notice": "This changes a file in the person's Google Drive."}
	switch w.tool {
	case ToolCreateFile:
		s["action"] = "Create a file"
	case ToolUpdateFile:
		s["action"] = "Change a file"
	case ToolMoveFile:
		s["action"] = "Move a file"
	case ToolTrashFile:
		s["action"] = "Move a file to the trash"
	}
	if t.file != nil {
		name, cutName := argutil.Runes(t.file.Name, previewRunes/4)
		s["fileId"], s["targetName"], s["targetType"] = t.file.ID, name, t.file.MimeType
		if cutName {
			s["targetNameCut"] = true
		}
	}
	if t.parent != nil {
		pname, _ := argutil.Runes(t.parent.Name, previewRunes/4)
		if w.tool == ToolMoveFile {
			s["newParent"], s["newParentName"] = t.parent.ID, pname
		} else {
			s["parent"], s["parentName"] = t.parent.ID, pname
		}
	}
	if w.Name != "" {
		s["name"] = w.Name
	}
	if w.tool == ToolCreateFile {
		mt := w.MimeType
		if mt == "" {
			mt = defaultContent
		}
		s["mimeType"] = mt
	}
	if w.Content != nil {
		b, cutB := argutil.Runes(*w.Content, previewRunes)
		s["content"], s["contentBytes"] = b, len(*w.Content)
		if cutB {
			s["contentCut"] = true
		}
	}
	return s
}

// prepare reads the call of a write envelope.
func (r *Runtime) prepare(ctx context.Context, env contract.Envelope) (write, *call, error) {
	tool, raw, err := argutil.SplitArgs(env.Request.Arguments)
	if err != nil || !isWrite(tool) {
		return write{}, nil, bad("unknown tool")
	}
	c := r.newCall(ctx, env)
	if c.token == "" {
		return write{}, nil, bad("no access token for this call")
	}
	// The scope comes first: a model that cannot write learns it from any
	// write tool, before the service is asked anything.
	if err := c.requireScope(); err != nil {
		return write{}, nil, err
	}
	w, err := c.parseWrite(tool, raw)
	return w, c, err
}

// Preview checks a write and describes it. It changes nothing.
func (r *Runtime) Preview(ctx context.Context, env contract.Envelope) (contract.Preview, error) {
	w, c, err := r.prepare(ctx, env)
	if err != nil {
		return contract.Preview{}, err
	}
	t, rev, err := c.inspect(w)
	if err != nil {
		return contract.Preview{}, r.translateWrite(err, false)
	}
	summary := argutil.PlainJSON(map[string]any{"count": 1, "sample": sample(w, t)})
	if len(summary) > 3<<10 {
		return contract.Preview{}, bad("the preview is too large")
	}
	return contract.Preview{OperationID: env.Request.OperationID, Revision: contract.Revision{Schema: 1, Data: rev},
		ExpiresAt: r.Time().Add(24 * time.Hour), Summary: summary}, nil
}

// Apply makes the change once. Renaming, replacing content, moving and
// trashing set a state, so a second attempt changes nothing more. Creating
// does not, so a created file carries a private property that names the
// operation, and a second attempt looks for it first.
func (r *Runtime) Apply(ctx context.Context, env contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	op := env.Request.OperationID
	return r.Runtime.Apply(op, rev, func() (writeflow.Session, error) {
		w, c, err := r.prepare(ctx, env)
		if err != nil {
			return nil, err
		}
		return &session{c: c, w: w, op: op}, nil
	})
}

// session is one checked write of the Drive add-on.
type session struct {
	writeflow.NoRecovery
	c  *call
	w  write
	t  target
	op string
}

func (s *session) Inspect() (uint64, error) {
	t, rev, err := s.c.inspect(s.w)
	s.t = t
	return rev, err
}

// Resume looks for the file that an earlier attempt of a create made.
func (s *session) Resume(started bool) (map[string]any, bool, error) {
	if started && s.w.tool == ToolCreateFile {
		if found, ok := s.c.find(s.op); ok {
			return found, true, nil
		}
	}
	return nil, false, nil
}

func (s *session) Send() (map[string]any, error) { return s.c.send(s.w, s.t, s.op) }

// translateWrite is translate for a definite failure of a write: a limit
// becomes a refusal that says when to try again, since nothing was changed. A
// refusal of a change on a file that the narrow scope does not reach says so.
func (r *Runtime) translateWrite(err error, sent bool) error {
	var limited *contract.ErrRateLimited
	var server *driveapi.ServerError
	if errors.As(err, &limited) {
		return bad("Drive is limiting calls. Nothing sent. Retry in %d s", limited.Seconds())
	}
	if !sent && errors.As(err, &server) {
		return bad("Drive failed to answer. Nothing sent. Retry in %d s", server.RetryAfter)
	}
	if sent && (errors.Is(err, driveapi.ErrNotFound) || errors.Is(err, driveapi.ErrForbidden) || errors.Is(err, driveapi.ErrScope)) {
		return bad("Drive refused it. %s reaches only files made here", ScopeWriteName)
	}
	return r.Translate(err)
}

type created struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	WebViewLink string `json:"webViewLink"`
}

func outOf(cr created) map[string]any {
	out := map[string]any{}
	if cr.ID != "" {
		out["fileId"] = cr.ID
	}
	if u := link(cr.WebViewLink); u != "" {
		out["url"] = u
	}
	return out
}

func multipartRelated(meta any, contentType string, content []byte) ([]byte, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	var nonce [12]byte
	_, _ = rand.Read(nonce[:])
	if err := mw.SetBoundary("localai" + hex.EncodeToString(nonce[:])); err != nil {
		return nil, "", err
	}
	head := textproto.MIMEHeader{"Content-Type": {"application/json; charset=UTF-8"}}
	p, err := mw.CreatePart(head)
	if err != nil {
		return nil, "", err
	}
	if err := json.NewEncoder(p).Encode(meta); err != nil {
		return nil, "", err
	}
	p, err = mw.CreatePart(textproto.MIMEHeader{"Content-Type": {contentType}})
	if err != nil {
		return nil, "", err
	}
	_, _ = p.Write(content)
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "multipart/related; boundary=" + mw.Boundary(), nil
}

// send makes the request for a checked write.
func (c *call) send(w write, t target, op string) (map[string]any, error) {
	api := c.r.API
	q := url.Values{"supportsAllDrives": {"true"}, "fields": {"id,name,webViewLink"}}
	var cr created
	switch w.tool {
	case ToolCreateFile:
		mt := w.MimeType
		if mt == "" {
			mt = defaultContent
		}
		content := ""
		if w.Content != nil {
			content = *w.Content
		}
		meta := map[string]any{"name": w.Name, "parents": []string{t.parent.ID}, "mimeType": mt, "appProperties": map[string]string{opProperty: op}}
		body, ctype, err := multipartRelated(meta, mt, []byte(content))
		if err != nil {
			return nil, driveapi.ErrInvalid
		}
		q.Set("uploadType", "multipart")
		if err := api.SendRaw(c.ctx, http.MethodPost, c.token, "/upload/drive/v3/files", q, ctype, body, &cr); err != nil {
			return nil, err
		}
	case ToolUpdateFile:
		if w.Content != nil {
			mq := url.Values{"supportsAllDrives": {"true"}, "uploadType": {"media"}, "fields": {"id,name,webViewLink"}}
			if err := api.SendRaw(c.ctx, http.MethodPatch, c.token, "/upload/drive/v3/files/"+argutil.IDPath(t.file.ID), mq, t.file.MimeType, []byte(*w.Content), &cr); err != nil {
				return nil, err
			}
		}
		if w.Name != "" {
			if err := api.SendJSON(c.ctx, http.MethodPatch, c.token, "/drive/v3/files/"+argutil.IDPath(t.file.ID), q, map[string]any{"name": w.Name}, &cr); err != nil {
				if w.Content != nil {
					// The content is replaced and the new name was refused.
					// Say exactly that: a retry needs the rename only.
					return nil, &writeflow.Partial{Out: partialRename(t.file.ID, err), Err: err}
				}
				return nil, err
			}
		}
	case ToolMoveFile:
		q.Set("addParents", t.parent.ID)
		q.Set("removeParents", strings.Join(t.file.Parents, ","))
		if err := api.SendJSON(c.ctx, http.MethodPatch, c.token, "/drive/v3/files/"+argutil.IDPath(t.file.ID), q, map[string]any{}, &cr); err != nil {
			return nil, err
		}
	case ToolTrashFile:
		if err := api.SendJSON(c.ctx, http.MethodPatch, c.token, "/drive/v3/files/"+argutil.IDPath(t.file.ID), q, map[string]any{"trashed": true}, &cr); err != nil {
			return nil, err
		}
	}
	return outOf(cr), nil
}

// partialRename is what a receipt says when update-file replaced the content
// and the rename was refused. It holds a fixed sentence and no text of the
// file.
func partialRename(fileID string, err error) map[string]any {
	reason := driveapi.Describe(err)
	var limited *contract.ErrRateLimited
	if errors.As(err, &limited) {
		reason = "Drive is limiting calls"
	}
	return map[string]any{
		"fileId":         fileID,
		"partial":        true,
		"contentUpdated": true,
		"renameRefused":  true,
		"note":           "The content was updated. The rename was refused: " + reason + ". Nothing else changed. To retry, call update-file again with the new name only.",
	}
}

// find looks in Drive for a file made by an earlier attempt of the operation.
// It is read-only.
func (c *call) find(op string) (map[string]any, bool) {
	var cond conditions
	cond.add("appProperties has { key=%s and value=%s }", opProperty, op)
	cond = append(cond, "trashed = false")
	page, err := c.list(cond.String(), "", 5, "")
	if err != nil {
		return nil, false
	}
	if len(page.Files) == 0 {
		return nil, false
	}
	f := page.Files[0]
	return outOf(created{ID: f.ID, Name: f.Name, WebViewLink: f.WebViewLink}), true
}
