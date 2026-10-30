// SPDX-License-Identifier: MIT

// Package tools is the runtime of the office add-on executable. It reads and
// edits Word, Excel, PowerPoint and PDF files of the user it works for. The
// files stay in the host: a tool asks the host to copy a file into the data
// directory of this add-on, works on that copy, and asks the host to take the
// result back, with the version the copy came from, so that a change made by
// someone else in the meantime becomes a second file and is never lost.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/addons/internal/hostclient"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Tool names. They are the entries of capabilities.tools in the manifest.
const (
	ToolReadDocx   = "read-docx"
	ToolReadXlsx   = "read-xlsx"
	ToolReadPptx   = "read-pptx"
	ToolReadPdf    = "read-pdf"
	ToolEditDocx   = "edit-docx"
	ToolEditXlsx   = "edit-xlsx"
	ToolEditPptx   = "edit-pptx"
	ToolCreateDocx = "create-docx"
	ToolCreateXlsx = "create-xlsx"
	ToolCreatePptx = "create-pptx"
	ToolCreatePdf  = "create-pdf"
)

// Tools is the declared tool set, in manifest order.
var Tools = []string{
	ToolReadDocx, ToolReadXlsx, ToolReadPptx, ToolReadPdf,
	ToolEditDocx, ToolEditXlsx, ToolEditPptx,
	ToolCreateDocx, ToolCreateXlsx, ToolCreatePptx, ToolCreatePdf,
}

// Engine is the host operation channel. *hostclient.Client implements it, and
// a nil client answers hostclient.ErrNoHostOps.
type Engine interface {
	Do(ctx context.Context, operationID, op string, args, out any) error
}

var _ Engine = (*hostclient.Client)(nil)

// Bounds. A file the host copies in is at most maxFileBytes; what a read tool
// returns to the model is at most maxTextBytes; one edit call changes at most
// maxEdits things.
const (
	maxFileBytes = 32 << 20
	maxTextBytes = 48 << 10
	maxEdits     = 200
	maxPathBytes = 512
	maxRows      = 200
	maxCols      = 40
	maxPages     = 50
)

var operationShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type runtime struct {
	eng     Engine
	dataDir string
}

// New returns the runtime of the executable. dataDir is the data directory of
// the add-on: the one the host operations name their files in.
func New(eng Engine, dataDir string) contract.Runtime {
	return &runtime{eng: eng, dataDir: dataDir}
}

var errNoTool = errors.New("this tool is not served by the office add-on")

func (*runtime) Preview(context.Context, contract.Envelope) (contract.Preview, error) {
	return contract.Preview{}, errNoTool
}

func (*runtime) Apply(context.Context, contract.Envelope, contract.Revision) (contract.Receipt, error) {
	return contract.Receipt{}, errNoTool
}

func (*runtime) Receipt(context.Context, string) (contract.Receipt, error) {
	return contract.Receipt{}, errNoTool
}

func (*runtime) OpenOutput(context.Context, contract.Envelope, string) (io.ReadCloser, error) {
	return nil, errNoTool
}

func isAction(tool string) bool {
	switch tool {
	case ToolEditDocx, ToolEditXlsx, ToolEditPptx, ToolCreateDocx, ToolCreateXlsx, ToolCreatePptx, ToolCreatePdf:
		return true
	}
	return false
}

// Read serves the read tools.
func (r *runtime) Read(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	tool, err := protocol.ToolOf(env)
	if err != nil {
		return contract.Result{}, err
	}
	if isAction(tool) {
		return contract.Result{}, protocol.ErrBadRequest
	}
	return r.run(ctx, env, tool)
}

// Act serves the action tools.
func (r *runtime) Act(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	tool, err := protocol.ToolOf(env)
	if err != nil {
		return contract.Result{}, err
	}
	if !isAction(tool) {
		return contract.Result{}, protocol.ErrBadRequest
	}
	return r.run(ctx, env, tool)
}

var _ contract.ActRuntime = (*runtime)(nil)

func (r *runtime) run(ctx context.Context, env contract.Envelope, tool string) (contract.Result, error) {
	op := env.Request.OperationID
	if !operationShape.MatchString(op) {
		return contract.Result{}, protocol.ErrBadRequest
	}
	w := &work{r: r, ctx: ctx, op: op}
	defer w.clean()
	var out any
	var err error
	switch tool {
	case ToolReadDocx:
		out, err = w.readDocx(env.Request.Arguments)
	case ToolReadXlsx:
		out, err = w.readXlsx(env.Request.Arguments)
	case ToolReadPptx:
		out, err = w.readPptx(env.Request.Arguments)
	case ToolReadPdf:
		out, err = w.readPdf(env.Request.Arguments)
	case ToolEditDocx:
		out, err = w.editDocx(env.Request.Arguments)
	case ToolEditXlsx:
		out, err = w.editXlsx(env.Request.Arguments)
	case ToolEditPptx:
		out, err = w.editPptx(env.Request.Arguments)
	case ToolCreateDocx:
		out, err = w.createDocx(env.Request.Arguments)
	case ToolCreateXlsx:
		out, err = w.createXlsx(env.Request.Arguments)
	case ToolCreatePptx:
		out, err = w.createPptx(env.Request.Arguments)
	case ToolCreatePdf:
		out, err = w.createPdf(env.Request.Arguments)
	default:
		return contract.Result{}, protocol.ErrNotFound
	}
	var rf *refusal
	switch {
	case errors.As(err, &rf):
		// The model can correct this: say what was wrong, change nothing.
		return contract.Result{Payload: protocol.Payload(map[string]any{"ok": false, "error": rf.msg})}, nil
	case err != nil:
		return contract.Result{}, mapErr(err)
	}
	return contract.Result{Payload: protocol.Payload(out)}, nil
}

// refusal is a problem with the request that the model can fix. It is an
// answer, not a failure: nothing was changed.
type refusal struct{ msg string }

func (r *refusal) Error() string { return r.msg }

func refuse(format string, a ...any) error { return &refusal{msg: fmt.Sprintf(format, a...)} }

// decode reads the arguments strictly: an argument the schema does not name is
// a bad request. v embeds base, which holds the tool name the host adds.
func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return refuse("the arguments do not match the tool: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return refuse("the arguments hold more than one value")
	}
	return nil
}

type base struct {
	Tool string `json:"tool"`
}

// mapErr turns the refusal of a host operation into the protocol error that
// tells the owner what happened. A failure that does not say the request was
// wrong stays as it is, and the owner reads it as uncertain.
func mapErr(err error) error {
	var he *contract.HostError
	if !errors.As(err, &he) {
		return err
	}
	switch he.Code {
	case contract.HostFeature, contract.HostScope:
		return fmt.Errorf("%w: %s", protocol.ErrForbidden, he.Message)
	}
	return err
}

// work is one tool call: a scratch folder in the data directory, named by the
// operation, that is removed when the call ends.
type work struct {
	r   *runtime
	ctx context.Context
	op  string
}

func (w *work) rel(name string) string { return "work/" + w.op + "/" + name }

func (w *work) abs(name string) string {
	return filepath.Join(w.r.dataDir, filepath.FromSlash(w.rel(name)))
}

func (w *work) clean() { _ = os.RemoveAll(filepath.Join(w.r.dataDir, "work", w.op)) }

// checkPath refuses a path that cannot name a file of the type.
func checkPath(p, ext string) error {
	switch {
	case strings.TrimSpace(p) == "":
		return refuse("path is required")
	case len(p) > maxPathBytes || strings.ContainsRune(p, 0):
		return refuse("the path is not valid")
	case !strings.EqualFold(filepath.Ext(p), ext):
		return refuse("the file must end in %s", ext)
	}
	return nil
}

// fetch copies a file of the user into the scratch folder and returns its
// bytes and the version they are.
func (w *work) fetch(path, ext string) ([]byte, int64, error) {
	if err := checkPath(path, ext); err != nil {
		return nil, 0, err
	}
	var got struct {
		Version int64 `json:"version"`
	}
	to := w.rel("in" + ext)
	if err := w.r.eng.Do(w.ctx, w.op, "workspace.export", map[string]string{"path": path, "to": to}, &got); err != nil {
		var he *contract.HostError
		if errors.As(err, &he) && (he.Code == contract.HostBadRequest || he.Code == contract.HostNotFound) {
			return nil, 0, refuse("the file %q was not found or cannot be read", path)
		}
		return nil, 0, err
	}
	fi, err := os.Stat(w.abs("in" + ext))
	if err != nil {
		return nil, 0, err
	}
	if fi.Size() > maxFileBytes {
		return nil, 0, refuse("the file is larger than %d MiB", maxFileBytes>>20)
	}
	data, err := os.ReadFile(w.abs("in" + ext))
	return data, got.Version, err
}

// saved says where a stored file went.
type saved struct {
	OK      bool   `json:"ok"`
	Path    string `json:"path"`
	Version int64  `json:"version"`
	// Conflict is set when the file changed after it was read: the change is
	// saved as a second file at Path, and Requested is the file that changed.
	Conflict  bool   `json:"conflict,omitempty"`
	Requested string `json:"requested,omitempty"`
	Note      string `json:"note,omitempty"`
}

// store takes bytes into the files of the user at path. baseVersion is the
// version the bytes were made from; zero means the file was not there.
func (w *work) store(path, ext string, data []byte, baseVersion int64) (saved, error) {
	if len(data) > maxFileBytes {
		return saved{}, refuse("the result is larger than %d MiB", maxFileBytes>>20)
	}
	name := "out" + ext
	if err := os.MkdirAll(filepath.Dir(w.abs(name)), 0o700); err != nil {
		return saved{}, err
	}
	if err := os.WriteFile(w.abs(name), data, 0o600); err != nil {
		return saved{}, err
	}
	var got struct {
		Version    int64  `json:"version"`
		Path       string `json:"path"`
		ConflictOf string `json:"conflictOf"`
	}
	args := map[string]any{"from": w.rel(name), "path": path, "baseVersion": baseVersion}
	if err := w.r.eng.Do(w.ctx, w.op, "workspace.import", args, &got); err != nil {
		var he *contract.HostError
		if errors.As(err, &he) && he.Code == contract.HostBadRequest {
			return saved{}, refuse("the file cannot be saved at %q", path)
		}
		return saved{}, err
	}
	s := saved{OK: true, Path: got.Path, Version: got.Version}
	if got.ConflictOf != "" {
		s.Conflict, s.Requested = true, got.ConflictOf
		s.Note = fmt.Sprintf("The file %s changed after it was read, so nothing was overwritten. Your version is saved as %s. Tell the user and let them merge the two.", got.ConflictOf, got.Path)
	}
	return s, nil
}

// clip cuts text to budget bytes on a rune boundary and says whether it cut.
func clip(s string, budget int) (string, bool) {
	if len(s) <= budget {
		return s, false
	}
	cut := budget
	for cut > 0 && cut < len(s) && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut], true
}
