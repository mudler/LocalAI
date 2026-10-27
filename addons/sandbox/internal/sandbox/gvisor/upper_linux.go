//go:build linux

package gvisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// statKey is the change key of an upper layer file, built from the host
// stat: device, inode, size, change time and modification time, the times in
// nanoseconds. The guest's writes go to the host file at once, so the host
// stat sees every change.
func statKey(info os.FileInfo) string {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "" // an empty key means "always send"
	}
	return fmt.Sprintf("%d:%d:%d:%d.%d:%d.%d", st.Dev, st.Ino, st.Size,
		st.Ctim.Sec, st.Ctim.Nsec, st.Mtim.Sec, st.Mtim.Nsec)
}

// stagedSource lists entries whose file content is already copied.
type stagedSource []workspace.UpperEntry

func (s stagedSource) Walk(ctx context.Context, fn func(workspace.UpperEntry) error) error {
	for _, e := range s {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

// The caps of one read of the upper layer, the same as under chv. A command
// can make millions of empty files, which the disk watcher counts as zero
// blocks, so the read bounds itself.
const (
	maxUpperDepth   = 512     // directory levels
	maxUpperEntries = 1000000 // entries of the walk
)

// readLimits bounds one read of the upper layer. A zero field takes its
// default, except stage: zero there means no byte can be staged.
type readLimits struct {
	depth   int   // directory levels, default maxUpperDepth
	entries int   // entries of the walk, default maxUpperEntries
	stage   int64 // file bytes that one read may stage on the host
}

func (l readLimits) withDefaults() readLimits {
	if l.depth == 0 {
		l.depth = maxUpperDepth
	}
	if l.entries == 0 {
		l.entries = maxUpperEntries
	}
	return l
}

// readUpper reads the upper layer below root and returns the entries to
// commit and the index to save after the commit. Content of the files to
// send is copied into stage. The caller pauses the sandbox first: the read
// is then a consistent picture, and the commit, which runs after the sandbox
// resumed, reads the copies and not the live files. A layer with more levels
// or entries than the caps, or more file bytes to stage than lim.stage, fails
// the read with an error that names the cap; nothing is committed then.
func readUpper(ctx context.Context, root, stage string, ix sandbox.UpperIndex, lim readLimits) (stagedSource, sandbox.UpperIndex, error) {
	lim = lim.withDefaults()
	var entries []sandbox.IndexedEntry
	sizes := map[string]int64{}
	if _, err := os.Stat(root); err == nil {
		err := workspace.DirSource{Root: root}.Walk(ctx, func(e workspace.UpperEntry) error {
			if len(entries) >= lim.entries {
				return fmt.Errorf("gvisor: upper layer has more than %d entries (cap %d)", lim.entries, lim.entries)
			}
			if d := strings.Count(e.Path, "/") + 1; d > lim.depth {
				return fmt.Errorf("gvisor: upper layer is deeper than %d levels (cap %d, at %q)", lim.depth, lim.depth, e.Path)
			}
			ie := sandbox.IndexedEntry{UpperEntry: e}
			if e.Kind == workspace.EntryFile {
				info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(e.Path)))
				if err != nil {
					return err
				}
				ie.Key = statKey(info)
				sizes[e.Path] = info.Size()
			}
			entries = append(entries, ie)
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	send, next := ix.Plan(entries)
	var used int64
	for _, e := range send {
		if e.Kind != workspace.EntryFile {
			continue
		}
		size := sizes[e.Path]
		if size > lim.stage-used {
			return nil, nil, fmt.Errorf("gvisor: staging %q (%d bytes) goes over the stage limit of %d bytes (%d already staged)", e.Path, size, lim.stage, used)
		}
		used += size
	}
	out := make(stagedSource, 0, len(send))
	for i, e := range send {
		if e.Kind == workspace.EntryFile {
			dst := filepath.Join(stage, strconv.Itoa(i))
			if err := copyFile(ctx, filepath.Join(root, filepath.FromSlash(e.Path)), dst); err != nil {
				return nil, nil, fmt.Errorf("gvisor: staging %s: %w", e.Path, err)
			}
			e.Open = func() (io.ReadCloser, error) { return os.Open(dst) }
		}
		out = append(out, e)
	}
	return out, next, nil
}

// ficlone is the FICLONE ioctl: share the data blocks of a file.
const ficlone = 0x40049409

// copyFile copies src to dst. It shares the blocks when the file system can
// (reflink), and copies the bytes otherwise.
func copyFile(ctx context.Context, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, out.Fd(), ficlone, in.Fd()); e == 0 {
		return out.Close()
	}
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			out.Close()
			return err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				return werr
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return rerr
		}
	}
	return out.Close()
}
