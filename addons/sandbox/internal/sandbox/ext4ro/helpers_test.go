package ext4ro

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// tool finds an e2fsprogs program. The sbin folders are often not in PATH.
func tool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, d := range []string{"/usr/sbin", "/sbin", "/usr/local/sbin"} {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func needTools(names ...string) {
	GinkgoHelper()
	for _, n := range names {
		if tool(n) == "" {
			Skip(n + " (e2fsprogs) is not installed, so the specs that build images cannot run")
		}
	}
}

func sh(name string, args ...string) string {
	GinkgoHelper()
	out, err := runErr(name, args...)
	Expect(err).To(Succeed(), "%s %v: %s", name, args, out)
	return out
}

func runErr(name string, args ...string) (string, error) {
	cmd := exec.Command(tool(name), args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// imageSpec says how to build one image.
type imageSpec struct {
	name string
	size int64    // image size in bytes
	args []string // extra mke2fs arguments
	tree func(dir string)
	// script returns the debugfs -w commands to run after the tree is copied
	// in. It gets the image folder, which can hold files for the commands.
	script func(dir string) []string
	// fsck runs "e2fsck -fyD", which turns large directories into htrees.
	fsck bool
}

var (
	imageMu    sync.Mutex
	imageCache = map[string]string{}
)

// buildImage builds the image once per suite and returns its path.
func buildImage(s imageSpec) string {
	GinkgoHelper()
	needTools("mke2fs", "debugfs")
	imageMu.Lock()
	defer imageMu.Unlock()
	if p, ok := imageCache[s.name]; ok {
		return p
	}
	dir := filepath.Join(suiteDir, s.name)
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	img := filepath.Join(dir, "image")
	f, err := os.Create(img)
	Expect(err).To(Succeed())
	Expect(f.Truncate(s.size)).To(Succeed())
	Expect(f.Close()).To(Succeed())
	args := []string{"-q", "-F"}
	args = append(args, revisionArgs(s.args, img)...)
	if s.tree != nil {
		src := filepath.Join(dir, "src")
		Expect(os.MkdirAll(src, 0o755)).To(Succeed())
		s.tree(src)
		args = append(args, "-d", src)
	}
	args = append(args, img)
	sh("mke2fs", args...)
	if s.script != nil {
		sc := filepath.Join(dir, "script")
		Expect(os.WriteFile(sc, []byte(strings.Join(s.script(dir), "\n")+"\n"), 0o600)).To(Succeed())
		out := sh("debugfs", "-w", "-f", sc, img)
		Expect(out).NotTo(ContainSubstring("not found"), out)
	}
	if s.fsck {
		// e2fsck exits 1 when it fixed something, which is the purpose here.
		_, _ = runErr("e2fsck", "-fyD", img)
	}
	imageCache[s.name] = img
	return img
}

// revisionArgs rewrites the legacy "-r 0" pair into "-E revision=0" when the
// installed mke2fs has dropped the -r option (e2fsprogs 1.47.3 and later). It
// asks mke2fs itself with a dry run, so no version number is parsed.
func revisionArgs(args []string, img string) []string {
	GinkgoHelper()
	i := slices.Index(args, "-r")
	if i < 0 || i+1 >= len(args) {
		return args
	}
	if _, err := runErr("mke2fs", "-n", "-q", "-F", "-r", args[i+1], img); err == nil {
		return args
	}
	out := slices.Clone(args[:i])
	out = append(out, "-E", "revision="+args[i+1])
	return append(out, args[i+2:]...)
}

// releaseImage deletes an image that no other spec needs. The disk of a
// build host can be small, and an image is megabytes.
func releaseImage(name string) {
	imageMu.Lock()
	defer imageMu.Unlock()
	delete(imageCache, name)
	_ = os.RemoveAll(filepath.Join(suiteDir, name))
}

func readImage(path string) []byte {
	GinkgoHelper()
	b, err := os.ReadFile(path)
	Expect(err).To(Succeed())
	return b
}

func openBytes(b []byte) (*FS, error) { return Open(bytes.NewReader(b), int64(len(b))) }

func mustOpen(b []byte) *FS {
	GinkgoHelper()
	f, err := openBytes(b)
	Expect(err).To(Succeed())
	return f
}

// loadGolden reads a gzip image from testdata.
func loadGolden(name string) []byte {
	GinkgoHelper()
	f, err := os.Open(filepath.Join("testdata", name))
	Expect(err).To(Succeed())
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	Expect(err).To(Succeed())
	b, err := io.ReadAll(zr)
	Expect(err).To(Succeed())
	return b
}

// ---------------------------------------------------------------------
// The walk: every reader call that a caller makes, with caps. The specs and
// the corruption tests use it.

type node struct {
	Path   string
	Ino    uint64
	In     Inode
	Target string
	Sum    string // sha256 of the content, for regular files
	Xattrs map[string]string
	Kids   []DirEntry
}

type walkLimits struct {
	depth, nodes, dirEntries int
	fileBytes                int64 // content bytes read per file
}

var defaultWalk = walkLimits{depth: 64, nodes: 200000, dirEntries: 200000, fileBytes: 1 << 40}

var errLoop = errors.New("walk: directory reached twice")

// walkFS lists every inode that is reachable from the root. It fails on the
// first error, and on a directory that it reaches twice.
func walkFS(f *FS, lim walkLimits) (map[string]*node, error) {
	out := map[string]*node{}
	seen := map[uint64]bool{rootIno: true}
	type item struct {
		path  string
		ino   uint64
		depth int
	}
	queue := []item{{"", rootIno, 0}}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if len(out) >= lim.nodes {
			return out, fmt.Errorf("%w: more than %d nodes", ErrLimit, lim.nodes)
		}
		n, err := statNode(f, it.path, it.ino, lim)
		if err != nil {
			return out, fmt.Errorf("%q: %w", it.path, err)
		}
		out[it.path] = n
		if !n.In.IsDir() {
			continue
		}
		if it.depth >= lim.depth {
			return out, fmt.Errorf("%w: deeper than %d", ErrLimit, lim.depth)
		}
		n.Kids, err = f.ReadDir(it.ino, lim.dirEntries)
		if err != nil {
			return out, fmt.Errorf("%q: %w", it.path, err)
		}
		for _, k := range n.Kids {
			kp := it.path + "/" + k.Name
			if _, dup := out[kp]; dup {
				return out, fmt.Errorf("walk: path %q appears twice", kp)
			}
			if kn, err := f.Stat(k.Ino); err == nil && kn.IsDir() {
				if seen[k.Ino] {
					return out, fmt.Errorf("%q: %w", kp, errLoop)
				}
				seen[k.Ino] = true
			}
			queue = append(queue, item{kp, k.Ino, it.depth + 1})
		}
	}
	return out, nil
}

func statNode(f *FS, path string, ino uint64, lim walkLimits) (*node, error) {
	in, err := f.Stat(ino)
	if err != nil {
		return nil, err
	}
	n := &node{Path: path, Ino: ino, In: in}
	xs, err := f.Xattrs(ino)
	if err != nil {
		return nil, err
	}
	if len(xs) > 0 {
		n.Xattrs = map[string]string{}
	}
	for _, x := range xs {
		n.Xattrs[x.Name] = hex.EncodeToString(x.Value)
		if x.Err != nil && !errors.Is(x.Err, ErrUnsupported) {
			return nil, x.Err
		}
	}
	switch {
	case in.IsSymlink():
		if n.Target, err = f.Readlink(ino); err != nil {
			return nil, err
		}
	case in.IsRegular():
		fl, err := f.Open(ino)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		if _, err := io.Copy(h, io.LimitReader(fl.SectionReader(), lim.fileBytes)); err != nil {
			return nil, err
		}
		n.Sum = hex.EncodeToString(h.Sum(nil))
	}
	return n, nil
}

// guarded runs fn with a recover and a deadline. A hang fails the spec.
func guarded(d time.Duration, fn func() error) (err error, hung bool) {
	type result struct {
		err      error
		panicked any
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				ch <- result{panicked: p}
			}
		}()
		ch <- result{err: fn()}
	}()
	select {
	case r := <-ch:
		if r.panicked != nil {
			panic(fmt.Sprintf("the reader panicked: %v", r.panicked))
		}
		return r.err, false
	case <-time.After(d):
		return nil, true
	}
}

// ---------------------------------------------------------------------
// Ground truth from debugfs.

// debugfsBatch runs commands in one debugfs process and returns the output
// of each command.
func debugfsBatch(img string, cmds []string) []string {
	GinkgoHelper()
	sc, err := os.CreateTemp(suiteDir, "cmds-")
	Expect(err).To(Succeed())
	defer func() { _ = os.Remove(sc.Name()) }()
	_, err = sc.WriteString(strings.Join(cmds, "\n") + "\n")
	Expect(err).To(Succeed())
	Expect(sc.Close()).To(Succeed())
	cmd := exec.Command(tool("debugfs"), "-c", "-f", sc.Name(), img)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run() // messages go to stderr; a missing attribute is not an error
	out := stdout.String()
	res := make([]string, len(cmds))
	pos := 0
	for i, c := range cmds {
		hdr := "debugfs: " + c + "\n"
		j := strings.Index(out[pos:], hdr)
		Expect(j).To(BeNumerically(">=", 0), "no output for %q", c)
		start := pos + j + len(hdr)
		end := len(out)
		if i+1 < len(cmds) {
			k := strings.Index(out[start:], "debugfs: "+cmds[i+1]+"\n")
			Expect(k).To(BeNumerically(">=", 0), "no output for %q", cmds[i+1])
			end = start + k
		}
		res[i] = out[start:end]
		pos = end
	}
	return res
}

type lsRecord struct {
	Ino  uint64
	Mode uint32
	UID  uint32
	GID  uint32
	Name string
	Size int64
}

func parseLS(out string) []lsRecord {
	GinkgoHelper()
	var recs []lsRecord
	// A name can hold a newline, so a record can span lines.
	pending := ""
	for _, line := range strings.Split(out, "\n") {
		if line == "" && pending == "" {
			continue
		}
		if pending != "" {
			line = pending + "\n" + line
		}
		f := strings.Split(line, "/")
		if len(f) < 8 {
			pending = line
			continue
		}
		pending = ""
		Expect(f).To(HaveLen(8), "ls -p line %q", line)
		ino, _ := strconv.ParseUint(f[1], 10, 64)
		mode, _ := strconv.ParseUint(f[2], 8, 32)
		uid, _ := strconv.ParseUint(f[3], 10, 32)
		gid, _ := strconv.ParseUint(f[4], 10, 32)
		size, _ := strconv.ParseInt(f[6], 10, 64)
		if ino == 0 {
			// debugfs also prints the empty records (inode 0): the free
			// blocks of a directory. They are not live entries.
			continue
		}
		recs = append(recs, lsRecord{ino, uint32(mode), uint32(uid), uint32(gid), f[5], size})
	}
	return recs
}

type statRecord struct {
	Perm       uint16
	Flags      uint32
	Generation uint32
	VersionHi  uint64
	VersionLo  uint64
	UID, GID   uint32
	Size       int64
	Links      uint16
	Ctime      uint64
	Mtime      uint64
	Atime      uint64
	CtimeExtra uint64
	MtimeExtra uint64
	HasExtra   bool
}

var (
	reStatMode  = regexp.MustCompile(`Mode:\s+([0-7]+)\s+Flags: 0x([0-9a-f]+)`)
	reStatGen   = regexp.MustCompile(`Generation: (\d+)\s+Version: 0x([0-9a-f]+)(?::([0-9a-f]+))?`)
	reStatUser  = regexp.MustCompile(`User:\s+(\d+)\s+Group:\s+(\d+)\s+(?:Project:\s+\d+\s+)?Size: (\d+)`)
	reStatLinks = regexp.MustCompile(`Links: (\d+)`)
	reStatTime  = regexp.MustCompile(`(ctime|atime|mtime): 0x([0-9a-f]+)(?::([0-9a-f]+))?`)
)

func parseStat(out string) statRecord {
	GinkgoHelper()
	var s statRecord
	m := reStatMode.FindStringSubmatch(out)
	Expect(m).NotTo(BeNil(), out)
	p, _ := strconv.ParseUint(m[1], 8, 16)
	s.Perm = uint16(p)
	fl, _ := strconv.ParseUint(m[2], 16, 32)
	s.Flags = uint32(fl)
	m = reStatGen.FindStringSubmatch(out)
	Expect(m).NotTo(BeNil(), out)
	g, _ := strconv.ParseUint(m[1], 10, 32)
	s.Generation = uint32(g)
	if m[3] == "" {
		// A 128-byte inode shows only the low half.
		s.VersionLo, _ = strconv.ParseUint(m[2], 16, 64)
	} else {
		s.VersionHi, _ = strconv.ParseUint(m[2], 16, 64)
		s.VersionLo, _ = strconv.ParseUint(m[3], 16, 64)
	}
	m = reStatUser.FindStringSubmatch(out)
	Expect(m).NotTo(BeNil(), out)
	u, _ := strconv.ParseUint(m[1], 10, 32)
	gg, _ := strconv.ParseUint(m[2], 10, 32)
	s.UID, s.GID = uint32(u), uint32(gg)
	s.Size, _ = strconv.ParseInt(m[3], 10, 64)
	m = reStatLinks.FindStringSubmatch(out)
	Expect(m).NotTo(BeNil(), out)
	l, _ := strconv.ParseUint(m[1], 10, 16)
	s.Links = uint16(l)
	for _, m := range reStatTime.FindAllStringSubmatch(out, -1) {
		sec, _ := strconv.ParseUint(m[2], 16, 64)
		var ex uint64
		if m[3] != "" {
			ex, _ = strconv.ParseUint(m[3], 16, 64)
			s.HasExtra = true
		}
		switch m[1] {
		case "ctime":
			s.Ctime, s.CtimeExtra = sec, ex
		case "mtime":
			s.Mtime, s.MtimeExtra = sec, ex
		case "atime":
			s.Atime = sec
		}
	}
	return s
}

var reEAName = regexp.MustCompile(`^\s+(\S+) \(\d+\)`)

// dbgXattrs returns the attribute names that "ea_list" prints for each
// inode, and the raw values from "ea_get -r -x".
func dbgXattrs(img string, inos []uint64) map[uint64]map[string]string {
	GinkgoHelper()
	var cmds []string
	for _, ino := range inos {
		cmds = append(cmds, fmt.Sprintf("ea_list <%d>", ino))
	}
	outs := debugfsBatch(img, cmds)
	res := map[uint64]map[string]string{}
	var gets []string
	type key struct {
		ino  uint64
		name string
	}
	var keys []key
	for i, o := range outs {
		for _, line := range strings.Split(o, "\n") {
			if m := reEAName.FindStringSubmatch(line); m != nil {
				keys = append(keys, key{inos[i], m[1]})
				gets = append(gets, fmt.Sprintf("ea_get -r -x <%d> %s", inos[i], m[1]))
			}
		}
	}
	if len(gets) == 0 {
		return res
	}
	for i, o := range debugfsBatch(img, gets) {
		k := keys[i]
		first := strings.SplitN(o, "\n", 2)[0]
		_, hx, ok := strings.Cut(first, " = ")
		if !ok {
			// An empty value prints no "= ...".
			Expect(first).To(HaveSuffix(" (0)"), "ea_get output %q", o)
		}
		if res[k.ino] == nil {
			res[k.ino] = map[string]string{}
		}
		res[k.ino][k.name] = strings.ReplaceAll(strings.TrimSpace(hx), " ", "")
	}
	return res
}

// compareWithDebugfs checks every node of the walk against debugfs: the
// fields of "stat", the listing of "ls -p", the attributes and the content
// from "dump".
func compareWithDebugfs(img string, f *FS, nodes map[string]*node, checkContent bool) {
	GinkgoHelper()
	paths := make([]string, 0, len(nodes))
	for p := range nodes {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var inos []uint64
	seenIno := map[uint64]bool{}
	for _, p := range paths {
		if ino := nodes[p].Ino; !seenIno[ino] {
			seenIno[ino] = true
			inos = append(inos, ino)
		}
	}
	var cmds []string
	for _, ino := range inos {
		cmds = append(cmds, fmt.Sprintf("stat <%d>", ino))
	}
	var dirs []uint64
	for _, ino := range inos {
		for _, p := range paths {
			if n := nodes[p]; n.Ino == ino && n.In.IsDir() {
				dirs = append(dirs, ino)
				cmds = append(cmds, fmt.Sprintf("ls -p <%d>", ino))
				break
			}
		}
	}
	outs := debugfsBatch(img, cmds)
	stats := map[uint64]statRecord{}
	for i, ino := range inos {
		stats[ino] = parseStat(outs[i])
	}
	for _, p := range paths {
		n := nodes[p]
		s := stats[n.Ino]
		in := n.In
		Expect(in.Mode&0o7777).To(Equal(s.Perm), "%q: permission bits", p)
		Expect(in.Flags).To(Equal(s.Flags), "%q: flags", p)
		Expect(in.Generation).To(Equal(s.Generation), "%q: generation", p)
		Expect(in.Version>>32).To(Equal(s.VersionHi), "%q: version high", p)
		Expect(in.Version&0xFFFFFFFF).To(Equal(s.VersionLo), "%q: version low", p)
		Expect(in.UID).To(Equal(s.UID), "%q: uid", p)
		Expect(in.GID).To(Equal(s.GID), "%q: gid", p)
		Expect(in.Size).To(Equal(s.Size), "%q: size", p)
		Expect(in.Links).To(Equal(s.Links), "%q: links", p)
		// debugfs prints the seconds as an unsigned 32-bit number and
		// the epoch bits in the extra field.
		Expect(uint64(in.CtimeSec)&0xFFFFFFFF).To(Equal(s.Ctime), "%q: ctime", p)
		Expect(uint64(in.MtimeSec)&0xFFFFFFFF).To(Equal(s.Mtime), "%q: mtime", p)
		Expect(uint64(in.AtimeSec)&0xFFFFFFFF).To(Equal(s.Atime), "%q: atime", p)
		if s.HasExtra {
			Expect(uint64(in.CtimeExtra)).To(Equal(s.CtimeExtra), "%q: ctime extra", p)
			Expect(uint64(in.MtimeExtra)).To(Equal(s.MtimeExtra), "%q: mtime extra", p)
		} else {
			Expect(in.CtimeExtra|in.MtimeExtra).To(BeZero(), "%q: extra fields of a small inode", p)
		}
	}
	// Directory listings.
	for i, ino := range dirs {
		recs := parseLS(outs[len(inos)+i])
		var want []lsRecord
		for _, r := range recs {
			if r.Name != "." && r.Name != ".." {
				want = append(want, r)
			}
		}
		var dir *node
		for _, p := range paths {
			if nodes[p].Ino == ino {
				dir = nodes[p]
				break
			}
		}
		Expect(dir.Kids).To(HaveLen(len(want)), "listing of inode %d", ino)
		for j, r := range want {
			k := dir.Kids[j]
			Expect(k.Name).To(Equal(r.Name), "inode %d entry %d", ino, j)
			Expect(k.Ino).To(Equal(r.Ino), "inode %d entry %q", ino, r.Name)
			kn, err := f.Stat(k.Ino)
			Expect(err).To(Succeed())
			Expect(uint32(kn.Mode)).To(Equal(r.Mode), "entry %q mode", r.Name)
			Expect(kn.UID).To(Equal(r.UID))
			Expect(kn.GID).To(Equal(r.GID))
			if !kn.IsDir() {
				Expect(kn.Size).To(Equal(r.Size), "entry %q size", r.Name)
			}
		}
	}
	// Attributes.
	truth := dbgXattrs(img, inos)
	for _, p := range paths {
		n := nodes[p]
		got := n.Xattrs
		if got == nil {
			got = map[string]string{}
		}
		want := truth[n.Ino]
		if want == nil {
			want = map[string]string{}
		}
		Expect(got).To(Equal(want), "%q: attributes", p)
	}
	if !checkContent {
		return
	}
	// Content and symlink targets.
	dumpDir := filepath.Join(suiteDir, "dump-"+filepath.Base(filepath.Dir(img)))
	Expect(os.MkdirAll(dumpDir, 0o755)).To(Succeed())
	var dumps []string
	for _, ino := range inos {
		for _, p := range paths {
			if n := nodes[p]; n.Ino == ino && n.In.IsRegular() {
				dumps = append(dumps, fmt.Sprintf("dump <%d> %s", ino, filepath.Join(dumpDir, strconv.FormatUint(ino, 10))))
				break
			}
		}
	}
	if len(dumps) > 0 {
		debugfsBatch(img, dumps)
	}
	for _, p := range paths {
		n := nodes[p]
		switch {
		case n.In.IsRegular():
			b, err := os.ReadFile(filepath.Join(dumpDir, strconv.FormatUint(n.Ino, 10)))
			Expect(err).To(Succeed(), "%q", p)
			if n.In.Flags&flagInlineData != 0 && int64(len(b)) > n.In.Size {
				// debugfs dump writes all inline bytes, not only i_size
				// of them. The kernel returns i_size bytes.
				b = b[:n.In.Size]
			}
			if n.In.Flags&flagInlineData != 0 && int64(len(b)) < n.In.Size {
				// A file that mke2fs made from holes only has i_size
				// larger than its inline bytes. The kernel reads the rest
				// as zeros.
				b = append(b, make([]byte, n.In.Size-int64(len(b)))...)
			}
			sum := sha256.Sum256(b)
			Expect(n.Sum).To(Equal(hex.EncodeToString(sum[:])), "%q: content", p)
			Expect(int64(len(b))).To(Equal(n.In.Size), "%q: dumped size", p)
		case n.In.IsSymlink():
			out := debugfsBatch(img, []string{fmt.Sprintf("stat <%d>", n.Ino)})[0]
			// debugfs shows a fast symlink as "Fast link dest: "target"".
			if m := regexp.MustCompile(`Fast link dest: "(.*)"\n`).FindStringSubmatch(out); m != nil {
				Expect(n.Target).To(Equal(m[1]), "%q: target", p)
			} else {
				cat := debugfsBatch(img, []string{fmt.Sprintf("cat <%d>", n.Ino)})[0]
				Expect(cat).To(Equal(n.Target), "%q: slow link target", p)
			}
		}
	}
}

// ---------------------------------------------------------------------
// Ground truth from a host folder, for images that mke2fs -d built.

type hostNode struct {
	typ    string // "dir", "file", "link", "other"
	perm   uint16
	size   int64
	sum    string
	target string
}

func walkHost(root string) map[string]hostNode {
	GinkgoHelper()
	out := map[string]hostNode{}
	var rec func(rel string)
	rec = func(rel string) {
		ents, err := os.ReadDir(filepath.Join(root, rel))
		Expect(err).To(Succeed())
		for _, e := range ents {
			p := rel + "/" + e.Name()
			full := filepath.Join(root, p)
			st, err := os.Lstat(full)
			Expect(err).To(Succeed())
			hn := hostNode{perm: uint16(st.Mode().Perm())}
			switch {
			case st.Mode()&os.ModeSymlink != 0:
				hn.typ = "link"
				hn.target, err = os.Readlink(full)
				Expect(err).To(Succeed())
			case st.IsDir():
				hn.typ = "dir"
			case st.Mode().IsRegular():
				hn.typ = "file"
				hn.size = st.Size()
				b, err := os.ReadFile(full)
				Expect(err).To(Succeed())
				s := sha256.Sum256(b)
				hn.sum = hex.EncodeToString(s[:])
			default:
				hn.typ = "other"
			}
			if st.Mode()&os.ModeSetuid != 0 {
				hn.perm |= 0o4000
			}
			if st.Mode()&os.ModeSetgid != 0 {
				hn.perm |= 0o2000
			}
			if st.Mode()&os.ModeSticky != 0 {
				hn.perm |= 0o1000
			}
			out[p] = hn
			if hn.typ == "dir" {
				rec(p)
			}
		}
	}
	rec("")
	return out
}

// compareWithHost checks the walk against the folder that mke2fs -d copied.
// Entries that the specs add with debugfs are not in the folder, so the
// check goes from the folder to the image only.
func compareWithHost(nodes map[string]*node, host map[string]hostNode) {
	GinkgoHelper()
	for p, h := range host {
		n, ok := nodes[p]
		Expect(ok).To(BeTrue(), "%q is in the folder but not in the image", p)
		switch h.typ {
		case "dir":
			Expect(n.In.IsDir()).To(BeTrue(), "%q is a directory", p)
		case "file":
			Expect(n.In.IsRegular()).To(BeTrue(), "%q is a file", p)
			Expect(n.In.Size).To(Equal(h.size), "%q size", p)
			Expect(n.Sum).To(Equal(h.sum), "%q content", p)
		case "link":
			Expect(n.In.IsSymlink()).To(BeTrue(), "%q is a symlink", p)
			Expect(n.Target).To(Equal(h.target), "%q target", p)
		}
		Expect(n.In.Mode&0o7777).To(Equal(h.perm), "%q permissions", p)
	}
}

func sumHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
