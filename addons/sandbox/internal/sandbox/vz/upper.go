package vz

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ext4ro"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The upper image holds the overlay upper layer in /data (and its work
// folder in /work, and the agent's home in /home, which are not committed).
const upperData = "data"

// The host reads the upper image in Go with package ext4ro. The image is
// hostile: a guest process that has the right to write the file system can
// write any bytes into it. The reader therefore checks every record against
// the superblock and the name rules, and it stops at the caps below. These
// are the same rules and caps as the Cloud Hypervisor backend applies to the
// output of debugfs.
const (
	defaultDepth   = 512
	defaultEntries = 1000000
)

// readLimits bounds one read of an upper image. A zero field takes its
// default. A hostile image that goes past a limit fails the read.
type readLimits struct {
	depth   int   // directory levels, default 512
	entries int   // entries of the walk, default 1,000,000
	stage   int64 // file bytes that one read may stage on the host; zero means the size of the image
}

func (l readLimits) withDefaults() readLimits {
	if l.depth == 0 {
		l.depth = defaultDepth
	}
	if l.entries == 0 {
		l.entries = defaultEntries
	}
	return l
}

const (
	sIFMT  = 0o170000
	sIFDIR = 0o040000
	sIFREG = 0o100000
	sIFLNK = 0o120000
	sIFCHR = 0o020000
)

// overlayOpaque is the attribute that marks an opaque folder. Only the
// trusted attribute counts: the guest mounts overlayfs without userxattr, so
// it ignores user.overlay.opaque, and uid 1000 cannot set a trusted.*
// attribute. Honouring the user attribute would delete files the guest still
// sees.
const overlayOpaque = "trusted.overlay.opaque"

type upperNode struct {
	path string // relative to /data, forward slashes
	ino  uint64
	in   ext4ro.Inode
}

// readUpper lists the upper layer in the image at imagePath, applies the index
// and stages the content of the files to send in stageDir. It returns the
// entries to commit and the next index. stageDir must exist and be empty.
func readUpper(ctx context.Context, imagePath, stageDir string, lim readLimits, ix sandbox.UpperIndex) (workspace.FixtureSource, sandbox.UpperIndex, error) {
	lim = lim.withDefaults()
	f, err := os.Open(imagePath) // #nosec G304 -- a path the add-on builds under its own data or state folder
	if err != nil {
		return nil, nil, fmt.Errorf("vz: upper image: %w", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("vz: upper image: %w", err)
	}
	img, err := ext4ro.Open(f, st.Size())
	if err != nil {
		return nil, nil, fmt.Errorf("vz: upper image: %w", err)
	}
	top, err := img.ReadDir(2, lim.entries)
	if err != nil {
		return nil, nil, fmt.Errorf("vz: upper image: %w", err)
	}
	data := -1
	for i, e := range top {
		if err := checkRecord(img, e, "/"); err != nil {
			return nil, nil, err
		}
		if e.Name == upperData && data < 0 {
			in, err := img.Stat(e.Ino)
			if err != nil {
				return nil, nil, fmt.Errorf("vz: upper image: %w", err)
			}
			if uint32(in.Mode)&sIFMT == sIFDIR {
				data = i
			}
		}
	}
	var entries []sandbox.IndexedEntry
	inodes := map[uint64]ext4ro.Inode{}
	inoByPath := map[string]uint64{}
	if data >= 0 {
		if err := walk(ctx, img, lim, top[data].Ino, &entries, inodes, inoByPath); err != nil {
			return nil, nil, err
		}
	}
	send, nextIx := ix.Plan(entries)
	budget := lim.stage
	if budget == 0 {
		budget = st.Size()
	}
	if err := checkStageBudget(send, inoByPath, inodes, budget); err != nil {
		return nil, nil, err
	}
	// Stage the content of the files to send, one copy per inode.
	staged := map[uint64]bool{}
	for i, e := range send {
		if e.Kind != workspace.EntryFile {
			continue
		}
		ino := inoByPath[e.Path]
		host := filepath.Join(stageDir, strconv.FormatUint(ino, 10))
		size := inodes[ino].Size
		if !staged[ino] {
			staged[ino] = true
			if err := stageFile(ctx, img, ino, size, host); err != nil {
				return nil, nil, fmt.Errorf("vz: upper: staging %q: %w", e.Path, err)
			}
		}
		send[i].Open = func() (io.ReadCloser, error) {
			f, err := os.Open(host) // #nosec G304 -- a path the add-on builds under its own data or state folder
			if err != nil {
				return nil, err
			}
			if st, err := f.Stat(); err != nil || st.Size() != size {
				_ = f.Close()
				return nil, fmt.Errorf("vz: staged copy of %s has the wrong size", e.Path)
			}
			return f, nil
		}
	}
	return workspace.FixtureSource(send), nextIx, nil
}

// stageFile copies the content of one inode to path. The copy has exactly
// size bytes: a hole reads as zeros.
func stageFile(ctx context.Context, img *ext4ro.FS, ino uint64, size int64, path string) error {
	r, err := img.Open(ino)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- a path the add-on builds under its own data or state folder
	if err != nil {
		return err
	}
	_, err = io.Copy(out, io.NewSectionReader(&ctxReaderAt{ctx: ctx, r: r}, 0, size))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// ctxReaderAt stops a long copy when the context ends.
type ctxReaderAt struct {
	ctx context.Context
	r   io.ReaderAt
}

func (c *ctxReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.ReadAt(p, off)
}

// walk lists /data breadth first and appends the entries below it. It fails
// on a directory that is reached twice (a directory hard link), a path that
// appears twice, an inode outside the superblock range, an empty name, a name
// with "/" (a forged record), a damaged attribute area, and on the depth and
// entry caps.
func walk(ctx context.Context, img *ext4ro.FS, lim readLimits, root uint64, entries *[]sandbox.IndexedEntry, inodes map[uint64]ext4ro.Inode, inoByPath map[string]uint64) error {
	rootIn, err := img.Stat(root)
	if err != nil {
		return fmt.Errorf("vz: upper image: %w", err)
	}
	visited := map[uint64]bool{root: true}
	seen := map[string]bool{}
	count := 0
	level := []upperNode{{path: "", ino: root, in: rootIn}}
	for depth := 0; len(level) > 0; depth++ {
		if depth > lim.depth {
			return fmt.Errorf("vz: upper layer is deeper than %d levels (at %q)", lim.depth, level[0].path)
		}
		var next []upperNode
		for _, n := range level {
			if err := ctx.Err(); err != nil {
				return err
			}
			if n.path != "" {
				var x map[string][]byte
				if uint32(n.in.Mode)&sIFMT == sIFDIR {
					v, ok, err := img.Xattr(n.ino, overlayOpaque)
					if err != nil {
						return fmt.Errorf("vz: upper: directory %q has a damaged attribute area: %w", n.path, err)
					}
					if ok {
						x = map[string][]byte{overlayOpaque: v}
					}
				}
				inodes[n.ino] = n.in
				*entries = append(*entries, toEntry(n, x))
				inoByPath[n.path] = n.ino
			}
			if uint32(n.in.Mode)&sIFMT != sIFDIR {
				continue
			}
			kids, err := img.ReadDir(n.ino, lim.entries-count+1)
			if err != nil {
				if errors.Is(err, ext4ro.ErrLimit) {
					return fmt.Errorf("vz: upper layer has more than %d entries", lim.entries)
				}
				return fmt.Errorf("vz: upper: listing %q: %w", n.path, err)
			}
			for _, k := range kids {
				if err := checkRecord(img, k, "/"+n.path); err != nil {
					return err
				}
				p := k.Name
				if n.path != "" {
					p = n.path + "/" + k.Name
				}
				if seen[p] {
					return fmt.Errorf("vz: upper: path %q appears twice", p)
				}
				seen[p] = true
				count++
				if count > lim.entries {
					return fmt.Errorf("vz: upper layer has more than %d entries", lim.entries)
				}
				in, err := img.Stat(k.Ino)
				if err != nil {
					return fmt.Errorf("vz: upper: %q: %w", p, err)
				}
				if uint32(in.Mode)&sIFMT == sIFDIR {
					if visited[k.Ino] {
						return fmt.Errorf("vz: upper: %q is inode %d, which is already listed: a directory loop", p, k.Ino)
					}
					visited[k.Ino] = true
				}
				next = append(next, upperNode{path: p, ino: k.Ino, in: in})
			}
		}
		level = next
	}
	return nil
}

// checkRecord rejects a record that the superblock or the name rules do not
// allow. parent is the path of its folder, for the message.
func checkRecord(img *ext4ro.FS, e ext4ro.DirEntry, parent string) error {
	if !img.InodeInRange(e.Ino) {
		return fmt.Errorf("vz: upper: an entry in %q has inode %d, outside the file system (%d to %d)", parent, e.Ino, img.FirstIno(), img.InodeCount())
	}
	switch {
	case e.Name == "":
		return fmt.Errorf("vz: upper: an entry in %q has an empty name", parent)
	case strings.Contains(e.Name, "/"):
		return fmt.Errorf("vz: upper: an entry in %q has a name with a slash", parent)
	}
	return nil
}

// checkStageBudget fails when the files to send would stage more bytes than
// budget, before any file is copied. Each inode counts once, by its apparent
// size: the copy writes sparse files densely.
func checkStageBudget(send []workspace.UpperEntry, inoByPath map[string]uint64, inodes map[uint64]ext4ro.Inode, budget int64) error {
	var used int64
	counted := map[uint64]bool{}
	for _, e := range send {
		if e.Kind != workspace.EntryFile {
			continue
		}
		ino := inoByPath[e.Path]
		if counted[ino] {
			continue
		}
		counted[ino] = true
		size := inodes[ino].Size
		if size < 0 {
			return fmt.Errorf("vz: upper: file %q has a negative size", e.Path)
		}
		if size > budget-used {
			return fmt.Errorf("vz: upper: staging %q (%d bytes) goes over the stage limit of %d bytes (%d already staged)", e.Path, size, budget, used)
		}
		used += size
	}
	return nil
}

// key changes whenever the content of the file may have changed: the inode
// number, the generation, the size, the change and modification times with
// their extra bits, and the version counter.
func key(ino uint64, n ext4ro.Inode) string {
	return fmt.Sprintf("%d:%d:%d:%x.%x:%x.%x:%x", ino, n.Generation, n.Size, n.CtimeSec, n.CtimeExtra, n.MtimeSec, n.MtimeExtra, n.Version)
}

func toEntry(n upperNode, x map[string][]byte) sandbox.IndexedEntry {
	perm := fs.FileMode(uint32(n.in.Mode) & 0o777)
	e := sandbox.IndexedEntry{UpperEntry: workspace.UpperEntry{Path: n.path}}
	switch uint32(n.in.Mode) & sIFMT {
	case sIFDIR:
		e.Kind, e.Mode, e.Xattrs = workspace.EntryDir, fs.ModeDir|perm, x
	case sIFREG:
		e.Kind, e.Mode, e.Key = workspace.EntryFile, perm, key(n.ino, n.in)
	case sIFLNK:
		e.Kind, e.Mode = workspace.EntrySymlink, fs.ModeSymlink|perm
	case sIFCHR:
		e.Kind, e.Mode = workspace.EntryOther, fs.ModeDevice|fs.ModeCharDevice|perm
		if n.in.IsWhiteout() {
			e.Kind = workspace.EntryWhiteout
		}
	default:
		e.Kind, e.Mode = workspace.EntryOther, fs.ModeIrregular|perm
	}
	return e
}
