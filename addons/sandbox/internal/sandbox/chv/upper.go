package chv

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The upper image holds the overlay upper layer in /data (and its work
// folder in /work, and the agent's home in /home, which are not committed).
const upperData = "data"

// readLimits bounds one read of an upper image. A zero field takes its
// default. A hostile image that goes past a limit fails the read.
type readLimits struct {
	depth   int   // directory levels, default 512
	entries int   // entries of the walk, default 1,000,000
	output  int64 // stdout bytes of one debugfs process, default 512 MiB
	// stage is the number of file bytes that one read may stage on the host
	// (the files that UpperIndex.Plan selects). Zero means the size of the
	// upper image file, which is the last element of argv.
	stage int64
}

const (
	defaultDepth   = 512
	defaultEntries = 1000000
	defaultOutput  = 512 << 20
)

func (l readLimits) withDefaults() readLimits {
	if l.depth == 0 {
		l.depth = defaultDepth
	}
	if l.entries == 0 {
		l.entries = defaultEntries
	}
	if l.output == 0 {
		l.output = defaultOutput
	}
	return l
}

// upperReader reads an upper image with debugfs.
type upperReader struct {
	argv  []string   // debugfs command line up to and including the image path
	stage string     // folder for file contents, as debugfs sees it
	host  string     // the same folder on the host
	lim   readLimits // zero fields take the defaults
}

type upperNode struct {
	path string // relative to /data, forward slashes
	ls   lsEntry
}

const (
	sIFMT  = 0o170000
	sIFDIR = 0o040000
	sIFREG = 0o100000
	sIFLNK = 0o120000
	sIFCHR = 0o020000
)

// read lists the upper layer, applies the index and stages the content of
// the files to send. It returns the entries to commit and the next index.
// The stage and host folders are emptied first, so a file staged by an
// earlier read cannot be sent again.
func (r upperReader) read(ctx context.Context, ix sandbox.UpperIndex) (workspace.FixtureSource, sandbox.UpperIndex, error) {
	lim := r.lim.withDefaults()
	// r.stage is the path as debugfs sees it (inside bubblewrap it is not a
	// host path), so the host folder is the one to clear.
	if err := clearFolders(r.host); err != nil {
		return nil, nil, err
	}
	bounds, err := r.bounds(ctx, lim)
	if err != nil {
		return nil, nil, err
	}
	var top []lsEntry
	err = runBatch(ctx, r.argv, []command{{text: "ls -p /", parse: func(b []byte) ([]byte, error) {
		var err error
		top, b, err = parseLS(b)
		return b, err
	}}}, lim.output)
	if err != nil {
		return nil, nil, err
	}
	data := -1
	for i, e := range top {
		if e.Name == "." || e.Name == ".." {
			continue
		}
		if err := checkRecord(e, "/", bounds); err != nil {
			return nil, nil, err
		}
		if e.Name == upperData && e.Mode&sIFMT == sIFDIR {
			data = i
		}
	}
	var entries []sandbox.IndexedEntry
	inodes := map[uint64]inode{}
	inoByPath := map[string]uint64{}
	if data >= 0 {
		if err := r.walk(ctx, lim, bounds, top[data], &entries, inodes, inoByPath); err != nil {
			return nil, nil, err
		}
	}
	send, nextIx := ix.Plan(entries)
	budget := lim.stage
	if budget == 0 {
		st, err := os.Stat(r.argv[len(r.argv)-1])
		if err != nil {
			return nil, nil, fmt.Errorf("chv: upper image size: %w", err)
		}
		budget = st.Size()
	}
	if err := checkStageBudget(send, inoByPath, inodes, budget); err != nil {
		return nil, nil, err
	}
	// Stage the content of the files to send, one dump per inode.
	var dumps []command
	staged := map[uint64]bool{}
	for i, e := range send {
		if e.Kind != workspace.EntryFile {
			continue
		}
		ino := inoByPath[e.Path]
		host := filepath.Join(r.host, itoa(ino))
		size := inodes[ino].Size
		send[i].Open = func() (io.ReadCloser, error) {
			f, err := os.Open(host)
			if err != nil {
				return nil, err
			}
			if st, err := f.Stat(); err != nil || st.Size() != size {
				_ = f.Close()
				return nil, fmt.Errorf("chv: staged copy of %s has the wrong size", e.Path)
			}
			return f, nil
		}
		if !staged[ino] {
			staged[ino] = true
			dumps = append(dumps, command{text: "dump <" + itoa(ino) + "> " + r.stage + "/" + itoa(ino), parse: func(b []byte) ([]byte, error) { return b, nil }})
		}
	}
	if err := runBatch(ctx, r.argv, dumps, lim.output); err != nil {
		return nil, nil, err
	}
	return workspace.FixtureSource(send), nextIx, nil
}

// bounds reads the inode count and the first inode of the superblock once.
func (r upperReader) bounds(ctx context.Context, lim readLimits) (fsBounds, error) {
	var b fsBounds
	err := runBatch(ctx, r.argv, []command{{text: "show_super_stats -h", parse: func(body []byte) ([]byte, error) {
		var err error
		b, _, err = parseSuperStats(body)
		return nil, err
	}}}, lim.output)
	return b, err
}

// walk lists /data breadth first, one debugfs process per level, and appends
// the entries below it. It fails on a directory that is reached twice (a
// directory hard link), a path that appears twice, an inode outside the
// superblock range, an empty name, a name with "/" (a forged record), a
// damaged attribute area, and on the depth and entry caps.
func (r upperReader) walk(ctx context.Context, lim readLimits, b fsBounds, root lsEntry, entries *[]sandbox.IndexedEntry, inodes map[uint64]inode, inoByPath map[string]uint64) error {
	visited := map[uint64]bool{root.Ino: true}
	seen := map[string]bool{}
	count := 0
	level := []upperNode{{path: "", ls: root}}
	for depth := 0; len(level) > 0; depth++ {
		if depth > lim.depth {
			return fmt.Errorf("chv: upper layer is deeper than %d levels (at %q)", lim.depth, level[0].path)
		}
		var cmds []command
		listed := map[uint64][]lsEntry{}
		opaque := map[uint64]map[string][]byte{}
		for _, n := range level {
			n := n
			if n.ls.Mode&sIFMT != sIFDIR {
				if n.path != "" && (n.ls.Mode&sIFMT == sIFREG || n.ls.Mode&sIFMT == sIFCHR) {
					cmds = append(cmds, command{text: "inode_dump <" + itoa(n.ls.Ino) + ">", parse: func(x []byte) ([]byte, error) {
						in, rest, err := parseInodeDump(x)
						inodes[n.ls.Ino] = in
						return rest, err
					}})
				}
				continue
			}
			ino := n.ls.Ino
			cmds = append(cmds, command{text: "ls -p <" + itoa(ino) + ">", parse: func(x []byte) ([]byte, error) {
				kids, rest, err := parseLS(x)
				listed[ino] = kids
				return rest, err
			}})
			if n.path == "" {
				continue
			}
			cmds = append(cmds, command{text: "inode_dump <" + itoa(ino) + ">", parse: func(x []byte) ([]byte, error) {
				in, rest, err := parseInodeDump(x)
				inodes[ino] = in
				return rest, err
			}})
			// Only the trusted attribute: the guest mounts overlayfs without
			// userxattr, so it ignores user.overlay.opaque, and uid 1000
			// cannot set a trusted.* attribute. Honouring the user attribute
			// would delete files the guest still sees.
			for _, key := range []string{"trusted.overlay.opaque"} {
				key := key
				get := parseEAGet(key)
				cmds = append(cmds, command{text: "ea_get -x <" + itoa(ino) + "> " + key, parse: func(x []byte) ([]byte, error) {
					v, rest, err := get(x)
					if v != nil {
						if opaque[ino] == nil {
							opaque[ino] = map[string][]byte{}
						}
						opaque[ino][key] = v
					}
					return rest, err
				}})
			}
		}
		if err := runBatch(ctx, r.argv, cmds, lim.output); err != nil {
			return err
		}
		var next []upperNode
		for _, n := range level {
			if n.path != "" {
				in := inodes[n.ls.Ino]
				if n.ls.Mode&sIFMT == sIFDIR && in.xattrBad {
					return fmt.Errorf("chv: upper: directory %q has a damaged attribute area", n.path)
				}
				*entries = append(*entries, toEntry(n, in, opaque[n.ls.Ino]))
				inoByPath[n.path] = n.ls.Ino
			}
			if n.ls.Mode&sIFMT != sIFDIR {
				continue
			}
			for _, k := range listed[n.ls.Ino] {
				if k.Name == "." || k.Name == ".." {
					continue
				}
				if err := checkRecord(k, n.path, b); err != nil {
					return err
				}
				p := k.Name
				if n.path != "" {
					p = n.path + "/" + k.Name
				}
				if seen[p] {
					return fmt.Errorf("chv: upper: path %q appears twice", p)
				}
				seen[p] = true
				count++
				if count > lim.entries {
					return fmt.Errorf("chv: upper layer has more than %d entries", lim.entries)
				}
				if k.Mode&sIFMT == sIFDIR {
					if visited[k.Ino] {
						return fmt.Errorf("chv: upper: %q is inode %d, which is already listed: a directory loop", p, k.Ino)
					}
					visited[k.Ino] = true
				}
				next = append(next, upperNode{path: p, ls: k})
			}
		}
		level = next
	}
	return nil
}

// checkRecord rejects a record that the superblock or the name rules do not
// allow. parent is the path of its folder, for the message.
func checkRecord(e lsEntry, parent string, b fsBounds) error {
	if !b.inodeInRange(e.Ino) {
		return fmt.Errorf("chv: upper: an entry in %q has inode %d, outside the file system (%d to %d)", parent, e.Ino, b.firstIno, b.inodes)
	}
	switch {
	case e.Name == "":
		return fmt.Errorf("chv: upper: an entry in %q has an empty name", parent)
	case strings.Contains(e.Name, "/"):
		return fmt.Errorf("chv: upper: an entry in %q has a name with a slash", parent)
	}
	return nil
}

// checkStageBudget fails when the files to send would stage more bytes than
// budget, before any file is dumped. Each inode counts once, by the apparent
// size that the dump writes: debugfs writes sparse files densely.
func checkStageBudget(send []workspace.UpperEntry, inoByPath map[string]uint64, inodes map[uint64]inode, budget int64) error {
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
			return fmt.Errorf("chv: upper: file %q has a negative size", e.Path)
		}
		if size > budget-used {
			return fmt.Errorf("chv: upper: staging %q (%d bytes) goes over the stage limit of %d bytes (%d already staged)", e.Path, size, budget, used)
		}
		used += size
	}
	return nil
}

// clearFolders removes everything in the given host folders. An empty path
// is skipped.
func clearFolders(dirs ...string) error {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		ents, err := os.ReadDir(d)
		if err != nil {
			return err
		}
		for _, e := range ents {
			if err := os.RemoveAll(filepath.Join(d, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func toEntry(n upperNode, in inode, x map[string][]byte) sandbox.IndexedEntry {
	perm := fs.FileMode(n.ls.Mode & 0o777)
	e := sandbox.IndexedEntry{UpperEntry: workspace.UpperEntry{Path: n.path}}
	switch n.ls.Mode & sIFMT {
	case sIFDIR:
		e.Kind, e.Mode, e.Xattrs = workspace.EntryDir, fs.ModeDir|perm, x
	case sIFREG:
		e.Kind, e.Mode, e.Key = workspace.EntryFile, perm, in.key(n.ls.Ino)
	case sIFLNK:
		e.Kind, e.Mode = workspace.EntrySymlink, fs.ModeSymlink|perm
	case sIFCHR:
		e.Kind, e.Mode = workspace.EntryOther, fs.ModeDevice|fs.ModeCharDevice|perm
		if in.isWhiteout() {
			e.Kind = workspace.EntryWhiteout
		}
	default:
		e.Kind, e.Mode = workspace.EntryOther, fs.ModeIrregular|perm
	}
	return e
}

func itoa(n uint64) string { return strconv.FormatUint(n, 10) }
