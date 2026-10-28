// SPDX-License-Identifier: MIT

// Package remote is the workspace store of the sandbox add-on. The add-on does
// not hold the files of a user: the host does. This store answers the two
// calls a sandbox backend makes, Materialize and CommitUpper, by asking the
// host through its workspace operations, and refuses every other call, so a
// backend cannot reach a file the host did not hand over.
//
// A call belongs to one request of one user. The tools put the operation ID of
// that request in the context with WithOperation; the host runs the operation
// as the user of that request.
package remote

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Engine is the host operation channel.
type Engine interface {
	Do(ctx context.Context, operationID, op string, args, out any) error
}

// Store implements workspace.Store over the host operations.
type Store struct {
	eng     Engine
	dataDir string
	n       atomic.Uint64
}

var _ workspace.Store = (*Store)(nil)

// New returns a store. dataDir is the data directory of the add-on: every
// directory a call names must be inside it.
func New(eng Engine, dataDir string) *Store {
	return &Store{eng: eng, dataDir: filepath.Clean(dataDir)}
}

type opKey struct{}

// WithOperation returns a context that carries the operation ID of the request
// being served.
func WithOperation(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, opKey{}, id)
}

func operation(ctx context.Context) (string, error) {
	id, _ := ctx.Value(opKey{}).(string)
	if id == "" {
		return "", errors.New("remote: no request is being served, so there is no user to act for")
	}
	return id, nil
}

// rel turns a directory of this add-on into the path the host operations take:
// relative, with slashes, strictly inside the data directory.
func (s *Store) rel(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("remote: %q is not inside the data directory", dir)
	}
	r, err := filepath.Rel(s.dataDir, filepath.Clean(dir))
	if err != nil || r == "." || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("remote: %q is not inside the data directory", dir)
	}
	return filepath.ToSlash(r), nil
}

type mountArg struct {
	Path  string `json:"path"`
	At    string `json:"at"`
	Write bool   `json:"write"`
}

func mountsOf(v workspace.View) []mountArg {
	out := make([]mountArg, 0, len(v.Mounts))
	for _, m := range v.Mounts {
		out = append(out, mountArg{Path: m.Path, At: m.At, Write: m.Mode >= workspace.ModeWrite})
	}
	return out
}

// View records the mounts. The host checks them against what the user may
// reach when they are used; the paths are relative to the user's files.
func (s *Store) View(_ context.Context, a workspace.Actor, reqs []workspace.MountRequest) (workspace.View, error) {
	if len(reqs) == 0 {
		return workspace.View{}, fmt.Errorf("%w: a view needs a mount", workspace.ErrInvalid)
	}
	v := workspace.View{Actor: a}
	for _, r := range reqs {
		v.Mounts = append(v.Mounts, workspace.Mount{Path: r.Path, At: r.At, Mode: r.Mode})
	}
	return v, nil
}

// Materialize asks the host to build the read-only tree of the view in dir.
func (s *Store) Materialize(ctx context.Context, v workspace.View, dir string) (int64, error) {
	op, err := operation(ctx)
	if err != nil {
		return 0, err
	}
	rel, err := s.rel(dir)
	if err != nil {
		return 0, err
	}
	var got struct {
		Revision int64 `json:"revision"`
	}
	if err := s.eng.Do(ctx, op, "workspace.materialize", map[string]any{"mounts": mountsOf(v), "dir": rel}, &got); err != nil {
		return 0, err
	}
	return got.Revision, nil
}

// CommitUpper writes the upper layer as an archive in a folder of the data
// directory and asks the host to commit it as one change set. The folder is
// removed when the call returns.
func (s *Store) CommitUpper(ctx context.Context, v workspace.View, _ workspace.Actor, base int64, upper workspace.UpperSource, o workspace.UpperOptions) (workspace.ChangeSet, []workspace.Conflict, error) {
	dir := filepath.Join(s.dataDir, "commit", fmt.Sprintf("%d-%d", time.Now().UnixNano(), s.n.Add(1)))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	defer func() {
		_ = os.RemoveAll(dir)
		// The parent goes too when no other commit is using it.
		_ = os.Remove(filepath.Dir(dir))
	}()
	f, err := os.OpenFile(filepath.Join(dir, "upper.tar"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	n, significant, werr := writeArchive(ctx, upper, f)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return workspace.ChangeSet{}, nil, werr
	}
	if n == 0 {
		// Nothing to commit needs no user and no request: a sandbox that
		// sleeps or stops after a quiet time has an empty layer.
		return workspace.ChangeSet{}, nil, nil
	}
	op, err := operation(ctx)
	if err != nil {
		if significant == 0 {
			// Only folders, which the backends send again at every checkpoint
			// and the last call already committed. With no request there is no
			// user to commit for, and nothing new to lose.
			return workspace.ChangeSet{}, nil, nil
		}
		return workspace.ChangeSet{}, nil, err
	}
	rel, err := s.rel(dir)
	if err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	args := map[string]any{"mounts": mountsOf(v), "dir": rel, "base": base, "title": o.Title, "own": o.Own}
	var got struct {
		Revision  int64 `json:"revision"`
		Conflicts []struct {
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"conflicts"`
	}
	if err := s.eng.Do(ctx, op, "workspace.commit", args, &got); err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	var conflicts []workspace.Conflict
	for _, c := range got.Conflicts {
		conflicts = append(conflicts, workspace.Conflict{Path: c.Path, Reason: c.Reason})
	}
	return workspace.ChangeSet{Rev: got.Revision, Title: o.Title}, conflicts, nil
}

// ChangesSince lists the change sets after rev that touch the user's files.
func (s *Store) ChangesSince(ctx context.Context, _ workspace.Actor, rev int64, limit int) ([]workspace.ChangeSet, error) {
	op, err := operation(ctx)
	if err != nil {
		return nil, err
	}
	var got struct {
		Changes []struct {
			Revision int64  `json:"revision"`
			Title    string `json:"title"`
		} `json:"changes"`
	}
	if err := s.eng.Do(ctx, op, "workspace.changes", map[string]any{"since": rev, "limit": limit}, &got); err != nil {
		return nil, err
	}
	out := make([]workspace.ChangeSet, 0, len(got.Changes))
	for _, c := range got.Changes {
		out = append(out, workspace.ChangeSet{Rev: c.Revision, Title: c.Title})
	}
	return out, nil
}

// writeArchive writes the layer in the form workspace.ArchiveSource reads: a
// regular file, a folder, a symbolic link, a whiteout as the character device
// 0/0, and the overlay attributes as PAX records.
func writeArchive(ctx context.Context, upper workspace.UpperSource, w io.Writer) (n, significant int, err error) {
	tw := tar.NewWriter(w)
	err = upper.Walk(ctx, func(e workspace.UpperEntry) error {
		n++
		if e.Kind != workspace.EntryDir || len(e.Xattrs) > 0 {
			significant++
		}
		h := &tar.Header{Name: e.Path, Mode: int64(e.Mode.Perm()), Format: tar.FormatPAX}
		for k, v := range e.Xattrs {
			if h.PAXRecords == nil {
				h.PAXRecords = map[string]string{}
			}
			h.PAXRecords["SCHILY.xattr."+k] = string(v)
		}
		switch e.Kind {
		case workspace.EntryFile:
			if e.Open == nil {
				return fmt.Errorf("remote: the file %s has no content", e.Path)
			}
			rc, err := e.Open()
			if err != nil {
				return err
			}
			defer func() { _ = rc.Close() }()
			body, size, err := sizedBody(rc)
			if err != nil {
				return err
			}
			h.Typeflag, h.Size = tar.TypeReg, size
			if err := tw.WriteHeader(h); err != nil {
				return err
			}
			// Stream the file, so a large file of the guest is not held in
			// memory. A file that shrank after its size was read cannot fill
			// the header, so the archive fails instead of going out torn.
			if copied, err := io.Copy(tw, io.LimitReader(body, size)); err != nil {
				return err
			} else if copied != size {
				return fmt.Errorf("remote: %s changed while it was read: %w", e.Path, io.ErrUnexpectedEOF)
			}
			return nil
		case workspace.EntryDir:
			h.Typeflag, h.Name = tar.TypeDir, strings.TrimSuffix(e.Path, "/")+"/"
		case workspace.EntryWhiteout:
			h.Typeflag, h.Devmajor, h.Devminor = tar.TypeChar, 0, 0
		case workspace.EntrySymlink:
			h.Typeflag, h.Linkname = tar.TypeSymlink, "."
		default:
			// A device, socket or pipe is not kept. The host reads it as "other".
			h.Typeflag, h.Devmajor, h.Devminor = tar.TypeChar, 1, 3
		}
		return tw.WriteHeader(h)
	})
	if err != nil {
		return n, significant, err
	}
	return n, significant, tw.Close()
}

// sizedBody returns the content of an open file and its size. A file that
// reports its own size, as an *os.File does, is streamed. Anything else is read
// into memory to learn the size.
func sizedBody(rc io.ReadCloser) (io.Reader, int64, error) {
	if st, ok := rc.(interface{ Stat() (fs.FileInfo, error) }); ok {
		if fi, err := st.Stat(); err == nil && fi.Mode().IsRegular() {
			return rc, fi.Size(), nil
		}
	}
	b, err := io.ReadAll(rc)
	return bytes.NewReader(b), int64(len(b)), err
}

// The calls below are the ones a backend must not make. They answer
// workspace.ErrInvalid and ask nobody.

func refused(what string) error {
	return fmt.Errorf("%w: the sandbox add-on cannot %s; the host keeps the files", workspace.ErrInvalid, what)
}

func (*Store) Put(context.Context, workspace.Actor, string, io.Reader, workspace.PutOptions) (workspace.Node, error) {
	return workspace.Node{}, refused("write a file")
}

func (*Store) Mkdir(context.Context, workspace.Actor, string) (workspace.Node, error) {
	return workspace.Node{}, refused("make a folder")
}

func (*Store) Move(context.Context, workspace.Actor, string, string) (workspace.Node, error) {
	return workspace.Node{}, refused("move a file")
}

func (*Store) Delete(context.Context, workspace.Actor, string) error { return refused("delete a file") }

func (*Store) Open(context.Context, workspace.Actor, string) (io.ReadSeekCloser, workspace.Node, error) {
	return nil, workspace.Node{}, refused("open a file")
}

func (*Store) Stat(context.Context, workspace.Actor, string) (workspace.Node, error) {
	return workspace.Node{}, refused("look at a file")
}

func (*Store) List(context.Context, workspace.Actor, string) ([]workspace.Node, error) {
	return nil, refused("list a folder")
}

func (*Store) Begin(context.Context, workspace.Actor, string) (workspace.Tx, error) {
	return nil, refused("open a change set")
}

func (*Store) History(context.Context, workspace.Actor, workspace.HistoryQuery) ([]workspace.ChangeSet, error) {
	return nil, refused("read the history")
}

func (*Store) Undo(context.Context, workspace.Actor, int64, workspace.UndoOptions) (workspace.ChangeSet, []workspace.Conflict, error) {
	return workspace.ChangeSet{}, nil, refused("undo a change")
}

func (*Store) Grant(context.Context, workspace.Actor, string, string, workspace.Mode) error {
	return refused("grant access")
}

func (*Store) Revoke(context.Context, workspace.Actor, string, string) error {
	return refused("revoke access")
}

func (*Store) ReadablePrefixes(context.Context, workspace.Actor) ([]string, error) {
	return nil, refused("list the folders of a user")
}
