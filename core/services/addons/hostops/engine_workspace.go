// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const (
	featureChats = "chats"
	// actOpTimeout bounds the host operations that move a whole sandbox layer.
	actOpTimeout = 10 * time.Minute
	// upperName is the archive an add-on leaves in its data directory.
	upperName    = "upper.tar"
	maxMounts    = 16
	maxOwnRevs   = 4096
	maxChangeSet = 200
	// maxCopyBytes bounds one file moved by export or import.
	maxCopyBytes = 256 << 20
)

// WorkspaceHost is the host side of the workspace operations. Principal maps
// the actor of the live request to the user whose files are used; DataDir is
// the data directory of an add-on, the only place an operation reads or
// writes on disk.
type WorkspaceHost struct {
	Store     workspace.Store
	Principal func(contract.Actor) string
	DataDir   func(addonID string) string
	// Access resolves what a call may reach. For chatID "" (a call that no
	// chat made: an add-on page, the web interface) it is the user's whole
	// home, as the Files page is. For a chat it is the chat's access: fenced
	// to the agent's folder and the chat folder when the chat's agent has a
	// folder. An error refuses the call; it never falls back to the home.
	Access func(ctx context.Context, principal, chatID string) (session.Access, error)
	// Chat maps the resource of an action request that carries no chat
	// binding (an older frontend forwarded it) to the chat of the user: an
	// action tool always works for one chat, and its resource is the key of
	// that chat. "" (or a nil Chat) means no chat, and the call is refused.
	Chat func(ctx context.Context, principal, resourceID string) string
}

// errNoChat is the refusal of a call whose chat the host cannot resolve: the
// chat is gone, its agent's folder is missing, or the binding names another
// user. It says nothing more, and the call never runs user-wide instead.
var errNoChat = fmt.Errorf("%w: the chat of this call is not available", ErrBadRequest)

// principal maps the actor to the owner of the files. An empty answer from
// Principal means the actor has no files here; the operation is refused
// instead of falling back to some other user's home.
func (h *WorkspaceHost) principal(c Call) (string, error) {
	p := h.Principal(c.Actor)
	if p == "" {
		return "", fmt.Errorf("%w: no files for this user", ErrBadRequest)
	}
	return p, nil
}

// access is what the call may reach: the access of the chat the live request
// was made for, or the user's whole home for a request that no chat made.
func (h *WorkspaceHost) access(ctx context.Context, c Call) (session.Access, error) {
	p, err := h.principal(c)
	if err != nil {
		return session.Access{}, err
	}
	chat := ""
	switch {
	case c.ChatID != "":
		if c.ChatPrincipal != p {
			return session.Access{}, errNoChat
		}
		chat = c.ChatID
	case c.Scope == "act" && c.ResourceID != "":
		if h.Chat != nil {
			chat = h.Chat(ctx, p, c.ResourceID)
		}
		if chat == "" {
			return session.Access{}, errNoChat
		}
	}
	x, err := h.Access(ctx, p, chat)
	switch {
	case err != nil && chat != "":
		return session.Access{}, errNoChat
	case err != nil:
		return session.Access{}, fmt.Errorf("%w: files are not available", ErrBadRequest)
	}
	return x, nil
}

// target maps a path the add-on names to a workspace path of the access, and
// says whether the access may write there. The add-on's "/" is the primary
// folder of the access: the user's home, or the agent's folder of a scoped
// chat. A scoped chat's add-on may also name the other places of the chat's
// layout, such as /workspace/chat/x for the chat folder. A parent reference,
// NUL or a control character is refused; an encoded or look-alike separator is
// part of a name.
func target(x session.Access, p string) (abs string, write bool, err error) {
	pl := x.Place()
	if x.Scoped() {
		t, err := pl.Resolve(p)
		if err != nil {
			return "", false, err
		}
		return t.Abs, t.Write, nil
	}
	primary := pl.Primary()
	if primary == "" {
		return "", false, workspace.ErrNotFound
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	segs, err := workspace.SplitPath(p)
	if err != nil {
		return "", false, err
	}
	return primary + strings.TrimSuffix(workspace.JoinPath(segs), "/"), pl.PrimaryWritable(), nil
}

// addonPath turns a workspace path back into the add-on's terms: a path of the
// primary folder ("/" is the folder), or the guest path of another place of
// the chat (/workspace/chat/x). It never names the user's home or another
// folder by its workspace path.
func addonPath(x session.Access, abs string) string {
	if r := x.Place().Rel(abs); r != "" {
		return r
	}
	if g, ok := x.Place().Guest(abs); ok {
		return g
	}
	return ""
}

type mountArg struct {
	Path  string `json:"path"`
	At    string `json:"at"`
	Write bool   `json:"write"`
}

// view builds the view of the folders the add-on asks for, inside the access.
// When a scoped chat's add-on mounts its "/" at home, the host adds the chat
// folder at chat, so the add-on sees the layout of the chat without a change.
func (h *WorkspaceHost) view(ctx context.Context, x session.Access, mounts []mountArg) (workspace.View, error) {
	if len(mounts) == 0 || len(mounts) > maxMounts {
		return workspace.View{}, fmt.Errorf("%w: mounts must hold 1 to %d folders", ErrBadRequest, maxMounts)
	}
	reqs := make([]workspace.MountRequest, 0, len(mounts)+1)
	homeAsked, homeWrite, chatTaken := false, false, false
	for _, m := range mounts {
		abs, write, err := target(x, m.Path)
		if err != nil {
			return workspace.View{}, fmt.Errorf("%w: path of a mount", ErrBadRequest)
		}
		mode := workspace.ModeRead
		if m.Write && write {
			mode = workspace.ModeWrite
		}
		switch {
		case m.At == session.GuestChat:
			chatTaken = true
		case m.At == session.GuestHome && abs == x.Place().Primary():
			homeAsked, homeWrite = true, m.Write
		}
		reqs = append(reqs, workspace.MountRequest{Path: abs, At: m.At, Mode: mode})
	}
	if chat := x.Place().Chat(); x.Scoped() && homeAsked && !chatTaken && chat != "" {
		mode := workspace.ModeRead
		if homeWrite {
			mode = workspace.ModeWrite
		}
		reqs = append(reqs, workspace.MountRequest{Path: chat, At: session.GuestChat, Mode: mode})
	}
	v, err := x.SubView(ctx, h.Store, reqs)
	if err != nil {
		return workspace.View{}, workspaceErr(err)
	}
	return v, nil
}

// errReadOnly is the refusal of a write to a folder the call may only read.
var errReadOnly = fmt.Errorf("%w: the folder is read-only", ErrBadRequest)

// workspaceErr maps a store error to what the add-on may see: fixed text only.
func workspaceErr(err error) error {
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, workspace.ErrBadName), errors.Is(err, workspace.ErrInvalid), errors.Is(err, session.ErrInvalid):
		return fmt.Errorf("%w: the folder is not available", ErrBadRequest)
	case errors.Is(err, workspace.ErrReadOnly):
		return errReadOnly
	}
	return err
}

// dataRoot opens the data directory of an add-on. Every file operation of the
// add-on goes through the result: an os.Root never follows a link out of its
// folder, even when the add-on, which owns the folder, swaps a folder for a
// link while the operation runs.
func (h *WorkspaceHost) dataRoot(addonID string) (*os.Root, error) {
	dir := h.DataDir(addonID)
	if dir == "" {
		return nil, fmt.Errorf("%w: no data directory", ErrBadRequest)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: no data directory", ErrBadRequest)
	}
	return r, nil
}

// dataDir opens the folder rel inside the data directory of the add-on. It
// refuses an absolute path, a path that names a parent, and any symbolic link
// on the way. The folders are created when create is set. The caller closes
// the result. The result stays bound to the folder it opened, so a later swap
// of one of the folders above it cannot redirect what the caller does.
func (h *WorkspaceHost) dataDir(addonID, rel string, create bool) (*os.Root, error) {
	bad := fmt.Errorf("%w: dir must be a relative folder inside the add-on data directory", ErrBadRequest)
	if rel == "" || strings.ContainsRune(rel, 0) || filepath.IsAbs(rel) || strings.Contains(rel, "\\") {
		return nil, bad
	}
	root, err := h.dataRoot(addonID)
	if err != nil {
		return nil, bad
	}
	defer func() { _ = root.Close() }()
	cur := ""
	for seg := range strings.SplitSeq(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return nil, bad
		}
		cur = path.Join(cur, seg)
		fi, err := root.Lstat(cur)
		switch {
		case err == nil:
			if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
				return nil, bad
			}
		case errors.Is(err, os.ErrNotExist) && create:
			if err := root.Mkdir(cur, 0o700); err != nil {
				return nil, err
			}
		default:
			return nil, bad
		}
	}
	dir, err := root.OpenRoot(rel)
	if err != nil {
		return nil, bad
	}
	return dir, nil
}

// RegisterWorkspace registers the workspace.* operations: materialize builds
// the read-only view of some folders in the data directory of the add-on,
// commit takes the upper layer the add-on left there as one change set, and
// changes lists what changed after a revision. Everything runs with the access
// of the live request: the chat's, when a chat made it, else the user's. A nil
// host registers nothing.
func RegisterWorkspace(b *Broker, h *WorkspaceHost) error {
	if h == nil || h.Store == nil || h.Access == nil || h.Principal == nil || h.DataDir == nil {
		return nil
	}
	specs := []Spec{
		{Op: "workspace.materialize", Scope: "read", Feature: featureChats, Timeout: actOpTimeout, Handler: h.materialize},
		{Op: "workspace.commit", Scope: "act", Feature: featureChats, Idempotent: true, Timeout: actOpTimeout, Handler: h.commit},
		{Op: "workspace.export", Scope: "read", Feature: featureChats, Timeout: actOpTimeout, Handler: h.export},
		{Op: "workspace.import", Scope: "act", Feature: featureChats, Timeout: actOpTimeout, Handler: h.importFile},
		{Op: "workspace.changes", Scope: "read", Feature: featureChats, Handler: h.changes},
		// workspace.admin answers only for an administrator; for anyone else the
		// broker refuses it. An add-on uses it to guard a setting.
		{Op: "workspace.admin", Scope: "read", Feature: featureChats, Admin: true, Handler: func(context.Context, Call) (json.RawMessage, error) {
			return json.RawMessage(`{"admin":true}`), nil
		}},
	}
	for _, s := range specs {
		if err := b.Register(s); err != nil {
			return err
		}
	}
	return nil
}

func (h *WorkspaceHost) materialize(ctx context.Context, c Call) (json.RawMessage, error) {
	var a struct {
		Mounts []mountArg `json:"mounts"`
		Dir    string     `json:"dir"`
	}
	if err := decodeArgs(c.Arguments, &a); err != nil {
		return nil, err
	}
	dir, err := h.dataDir(c.AddonID, a.Dir, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	x, err := h.access(ctx, c)
	if err != nil {
		return nil, err
	}
	v, err := h.view(ctx, x, a.Mounts)
	if err != nil {
		return nil, err
	}
	// The store builds the tree through the opened folder, so a link that the
	// add-on plants inside it cannot send a file outside.
	rs, ok := h.Store.(workspace.RootStore)
	if !ok {
		return nil, errors.New("workspace: the store cannot build a tree in an open folder")
	}
	rev, err := rs.MaterializeRoot(ctx, v, dir)
	if err != nil {
		return nil, workspaceErr(err)
	}
	return json.Marshal(map[string]int64{"revision": rev})
}

func (h *WorkspaceHost) commit(ctx context.Context, c Call) (json.RawMessage, error) {
	var a struct {
		Mounts []mountArg `json:"mounts"`
		Dir    string     `json:"dir"`
		Base   int64      `json:"base"`
		Title  string     `json:"title"`
		Own    []int64    `json:"own"`
	}
	if err := decodeArgs(c.Arguments, &a); err != nil {
		return nil, err
	}
	if len(a.Own) > maxOwnRevs || len(a.Title) > 200 {
		return nil, fmt.Errorf("%w: too many revisions or a long title", ErrBadRequest)
	}
	dir, err := h.dataDir(c.AddonID, a.Dir, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	// The add-on writes this file, so it could make it a link to any archive
	// of the host. Export and import refuse links for the same reason.
	f, err := dir.OpenFile(upperName, os.O_RDONLY|syscallNoFollow, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: no %s in the directory", ErrBadRequest, upperName)
	}
	defer func() { _ = f.Close() }()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a file", ErrBadRequest, upperName)
	}
	x, err := h.access(ctx, c)
	if err != nil {
		return nil, err
	}
	v, err := h.view(ctx, x, a.Mounts)
	if err != nil {
		return nil, err
	}
	// The actor of a chat's access names the chat, so the change set belongs
	// to that chat as a file tool's would: the chat lists the files the
	// command saved.
	cs, conflicts, err := h.Store.CommitUpper(ctx, v, x.Actor(), a.Base, workspace.ArchiveSource(f), workspace.UpperOptions{Title: a.Title, Own: a.Own})
	if err != nil {
		return nil, workspaceErr(err)
	}
	type conflict struct {
		Path   string `json:"path"`
		Reason string `json:"reason"`
		Kept   string `json:"kept,omitempty"`
	}
	out := struct {
		Revision  int64      `json:"revision"`
		Conflicts []conflict `json:"conflicts"`
	}{Revision: cs.Rev, Conflicts: []conflict{}}
	// The paths go back in the add-on's terms, as its mounts are, so it can
	// name them to the model. A path outside every mount is already a path of
	// the view.
	rel := func(p string) string { return addonPath(x, p) }
	for _, k := range conflicts {
		x := conflict{Path: k.Path, Reason: k.Reason}
		if k.Reason != workspace.ReasonOutsideView {
			x.Path = rel(k.Path)
		}
		if k.Kept != "" {
			x.Kept = rel(k.Kept)
		}
		out.Conflicts = append(out.Conflicts, x)
	}
	return json.Marshal(out)
}

func (h *WorkspaceHost) changes(ctx context.Context, c Call) (json.RawMessage, error) {
	var a struct {
		Since int64 `json:"since"`
		Limit int   `json:"limit"`
	}
	if err := decodeArgs(c.Arguments, &a); err != nil {
		return nil, err
	}
	if a.Limit <= 0 || a.Limit > maxChangeSet {
		a.Limit = maxChangeSet
	}
	x, err := h.access(ctx, c)
	if err != nil {
		return nil, err
	}
	// The store lists only the change sets the access's actor may read: for
	// a scoped chat, none outside its folders.
	sets, err := h.Store.ChangesSince(ctx, x.Actor(), a.Since, a.Limit)
	if err != nil {
		return nil, workspaceErr(err)
	}
	type row struct {
		Revision int64  `json:"revision"`
		Title    string `json:"title"`
	}
	rows := make([]row, 0, len(sets))
	for _, s := range sets {
		rows = append(rows, row{Revision: s.Rev, Title: s.Title})
	}
	return json.Marshal(map[string]any{"changes": rows})
}

// dataFile opens the folder that holds the file rel inside the data directory
// of the add-on, and returns it with the name of the file. The folders above
// the file are created when create is set; the file itself must not be a
// link. The caller closes the folder.
func (h *WorkspaceHost) dataFile(addonID, rel string, create bool) (*os.Root, string, error) {
	dir, name := filepath.Split(rel)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "\\\x00") {
		return nil, "", fmt.Errorf("%w: the file must be a relative file inside the add-on data directory", ErrBadRequest)
	}
	var base *os.Root
	var err error
	if dir != "" {
		base, err = h.dataDir(addonID, strings.TrimSuffix(dir, "/"), create)
	} else {
		base, err = h.dataRoot(addonID)
	}
	if err != nil {
		return nil, "", err
	}
	if fi, err := base.Lstat(name); err == nil && !fi.Mode().IsRegular() {
		_ = base.Close()
		return nil, "", fmt.Errorf("%w: the file must be a regular file", ErrBadRequest)
	}
	return base, name, nil
}

// export copies one file the access reaches to the data directory of the
// add-on.
func (h *WorkspaceHost) export(ctx context.Context, c Call) (json.RawMessage, error) {
	var a struct {
		Path string `json:"path"`
		To   string `json:"to"`
	}
	if err := decodeArgs(c.Arguments, &a); err != nil {
		return nil, err
	}
	x, err := h.access(ctx, c)
	if err != nil {
		return nil, err
	}
	abs, _, err := target(x, a.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: path", ErrBadRequest)
	}
	dstDir, dst, err := h.dataFile(c.AddonID, a.To, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dstDir.Close() }()
	rc, node, err := h.Store.Open(ctx, x.Actor(), abs)
	if err != nil {
		return nil, workspaceErr(err)
	}
	defer func() { _ = rc.Close() }()
	if node.Kind != workspace.KindFile || node.Size > maxCopyBytes {
		return nil, fmt.Errorf("%w: not a file, or too large", ErrBadRequest)
	}
	out, err := dstDir.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscallNoFollow, 0o600)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot write the file", ErrBadRequest)
	}
	_, cerr := io.Copy(out, io.LimitReader(rc, maxCopyBytes))
	if e := out.Close(); cerr == nil {
		cerr = e
	}
	if cerr != nil {
		return nil, cerr
	}
	return json.Marshal(map[string]int64{"version": node.Version})
}

// importFile writes one file of the data directory of the add-on into a folder
// the access may write.
func (h *WorkspaceHost) importFile(ctx context.Context, c Call) (json.RawMessage, error) {
	var a struct {
		From        string `json:"from"`
		Path        string `json:"path"`
		BaseVersion int64  `json:"baseVersion"`
		Overwrite   bool   `json:"overwrite"`
	}
	if err := decodeArgs(c.Arguments, &a); err != nil {
		return nil, err
	}
	x, err := h.access(ctx, c)
	if err != nil {
		return nil, err
	}
	abs, write, err := target(x, a.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: path", ErrBadRequest)
	}
	if !write {
		return nil, errReadOnly
	}
	srcDir, src, err := h.dataFile(c.AddonID, a.From, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = srcDir.Close() }()
	f, err := srcDir.OpenFile(src, os.O_RDONLY|syscallNoFollow, 0)
	if err != nil {
		return nil, ErrNotFound
	}
	defer func() { _ = f.Close() }()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() || fi.Size() > maxCopyBytes {
		return nil, fmt.Errorf("%w: not a file, or too large", ErrBadRequest)
	}
	node, err := h.Store.Put(ctx, x.Actor(), abs, f, workspace.PutOptions{BaseVersion: a.BaseVersion, Overwrite: a.Overwrite, MaxSize: maxCopyBytes, Title: "Write " + path.Base(abs)})
	if err != nil {
		return nil, workspaceErr(err)
	}
	out := map[string]any{"version": node.Version, "path": addonPath(x, node.Path)}
	if node.ConflictOf != "" {
		// Someone changed the file after the add-on read it, so the store kept
		// both. conflictOf is the path the add-on asked for.
		out["conflictOf"] = addonPath(x, node.ConflictOf)
	}
	return json.Marshal(out)
}
