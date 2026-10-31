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

// WorkspaceFiles is what the workspace operations need from the files service.
type WorkspaceFiles interface {
	EnsureHome(ctx context.Context, principal string) error
	Abs(principal, rel string) (string, error)
	Actor(principal string) workspace.Actor
	Rel(principal, abs string) string
}

// WorkspaceHost is the host side of the workspace operations. Principal maps
// the actor of the live request to the user whose files are used; DataDir is
// the data directory of an add-on, the only place an operation reads or
// writes on disk.
type WorkspaceHost struct {
	Store     workspace.Store
	Files     WorkspaceFiles
	Principal func(contract.Actor) string
	DataDir   func(addonID string) string
}

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

type mountArg struct {
	Path  string `json:"path"`
	At    string `json:"at"`
	Write bool   `json:"write"`
}

func (h *WorkspaceHost) view(ctx context.Context, c Call, mounts []mountArg) (workspace.View, workspace.Actor, error) {
	if len(mounts) == 0 || len(mounts) > maxMounts {
		return workspace.View{}, workspace.Actor{}, fmt.Errorf("%w: mounts must hold 1 to %d folders", ErrBadRequest, maxMounts)
	}
	p, err := h.principal(c)
	if err != nil {
		return workspace.View{}, workspace.Actor{}, err
	}
	if err := h.Files.EnsureHome(ctx, p); err != nil {
		return workspace.View{}, workspace.Actor{}, fmt.Errorf("%w: files are not available", ErrBadRequest)
	}
	reqs := make([]workspace.MountRequest, 0, len(mounts))
	for _, m := range mounts {
		abs, err := h.Files.Abs(p, m.Path)
		if err != nil {
			return workspace.View{}, workspace.Actor{}, fmt.Errorf("%w: path of a mount", ErrBadRequest)
		}
		mode := workspace.ModeRead
		if m.Write {
			mode = workspace.ModeWrite
		}
		reqs = append(reqs, workspace.MountRequest{Path: abs, At: m.At, Mode: mode})
	}
	a := h.Files.Actor(p)
	v, err := h.Store.View(ctx, a, reqs)
	if err != nil {
		return workspace.View{}, workspace.Actor{}, workspaceErr(err)
	}
	return v, a, nil
}

// workspaceErr maps a store error to what the add-on may see: fixed text only.
func workspaceErr(err error) error {
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, workspace.ErrBadName), errors.Is(err, workspace.ErrInvalid):
		return fmt.Errorf("%w: the folder is not available", ErrBadRequest)
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
// changes lists what changed after a revision. Everything runs as the user of
// the live request. A nil host registers nothing.
func RegisterWorkspace(b *Broker, h *WorkspaceHost) error {
	if h == nil || h.Store == nil || h.Files == nil || h.Principal == nil || h.DataDir == nil {
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
	v, _, err := h.view(ctx, c, a.Mounts)
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
	v, actor, err := h.view(ctx, c, a.Mounts)
	if err != nil {
		return nil, err
	}
	cs, conflicts, err := h.Store.CommitUpper(ctx, v, actor, a.Base, workspace.ArchiveSource(f), workspace.UpperOptions{Title: a.Title, Own: a.Own})
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
	// The paths go back in the add-on's terms, relative to the user's files
	// as its mounts are, so it can name them to the model. A path outside
	// every mount is already a path of the view.
	rel := func(p string) string {
		if r := h.Files.Rel(actor.Principal, p); r != "" {
			return r
		}
		return p
	}
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
	p, err := h.principal(c)
	if err != nil {
		return nil, err
	}
	if err := h.Files.EnsureHome(ctx, p); err != nil {
		return nil, fmt.Errorf("%w: files are not available", ErrBadRequest)
	}
	sets, err := h.Store.ChangesSince(ctx, h.Files.Actor(p), a.Since, a.Limit)
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

// export copies one file of the user to the data directory of the add-on.
func (h *WorkspaceHost) export(ctx context.Context, c Call) (json.RawMessage, error) {
	var a struct {
		Path string `json:"path"`
		To   string `json:"to"`
	}
	if err := decodeArgs(c.Arguments, &a); err != nil {
		return nil, err
	}
	p, err := h.principal(c)
	if err != nil {
		return nil, err
	}
	if err := h.Files.EnsureHome(ctx, p); err != nil {
		return nil, fmt.Errorf("%w: files are not available", ErrBadRequest)
	}
	abs, err := h.Files.Abs(p, a.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: path", ErrBadRequest)
	}
	dstDir, dst, err := h.dataFile(c.AddonID, a.To, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dstDir.Close() }()
	rc, node, err := h.Store.Open(ctx, h.Files.Actor(p), abs)
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

// importFile writes one file of the data directory of the add-on into the
// files of the user.
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
	p, err := h.principal(c)
	if err != nil {
		return nil, err
	}
	if err := h.Files.EnsureHome(ctx, p); err != nil {
		return nil, fmt.Errorf("%w: files are not available", ErrBadRequest)
	}
	abs, err := h.Files.Abs(p, a.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: path", ErrBadRequest)
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
	node, err := h.Store.Put(ctx, h.Files.Actor(p), abs, f, workspace.PutOptions{BaseVersion: a.BaseVersion, Overwrite: a.Overwrite, MaxSize: maxCopyBytes, Title: "Write " + path.Base(abs)})
	if err != nil {
		return nil, workspaceErr(err)
	}
	out := map[string]any{"version": node.Version, "path": h.Files.Rel(p, node.Path)}
	if node.ConflictOf != "" {
		// Someone changed the file after the add-on read it, so the store kept
		// both. conflictOf is the path the add-on asked for.
		out["conflictOf"] = h.Files.Rel(p, node.ConflictOf)
	}
	return json.Marshal(out)
}
