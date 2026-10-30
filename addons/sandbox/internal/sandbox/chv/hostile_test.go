package chv

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// plainName and forgedName have the same length (20 bytes). Written over
// plainName in a directory block, forgedName makes ls -p print a second
// record, with inode 1.
const (
	plainName  = "abcdefghijklmnopqrst"
	forgedName = "x/1/\n/1/040755/0/0/y"
)

// hostileUpper is testUpper plus the file plainName in data/w.
func hostileUpper() (string, upperReader) {
	GinkgoHelper()
	img, r := testUpper()
	debugfsWrite(img, "write "+filepath.Join(filepath.Dir(img), "src", "new.txt")+" data/w/"+plainName)
	return img, r
}

func readCtx() context.Context {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	DeferCleanup(cancel)
	return ctx
}

var _ = Describe("hostile upper images", func() {
	// A directory hard link (w/d/again is w/d) must fail the read at once,
	// and name the path.
	It("fails on a directory loop", func() {
		needTools("mke2fs", "debugfs")
		img, r := testUpper()
		debugfsWrite(img, "link data/w/d data/w/d/again")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		start := time.Now()
		_, _, err := r.read(ctx, nil)
		Expect(err).To(MatchError(ContainSubstring("w/d/again")), "a directory loop (after %v)", time.Since(start))
		Expect(time.Since(start)).To(BeNumerically("<=", 10*time.Second), "the loop took too long")
	})

	// A name with "/" forges a record with inode 1, which is below
	// first_ino. The read must fail on it.
	It("fails on a forged record", func() {
		needTools("mke2fs", "debugfs")
		img, r := hostileUpper()
		patchName(img, plainName, forgedName)
		_, _, err := r.read(readCtx(), nil)
		Expect(err).To(MatchError(ContainSubstring("inode 1,")), "a forged record with inode 1")
	})

	// An entry with an empty name would land at the wrong path. The read
	// must fail.
	It("fails on an empty name", func() {
		needTools("mke2fs", "debugfs")
		img, r := hostileUpper()
		setNameLen(img, plainName, 0)
		_, _, err := r.read(readCtx(), nil)
		Expect(err).To(MatchError(ContainSubstring("empty name")), "an empty name")
	})

	// The EA magic of the opaque folder w/d is zeroed. debugfs then says
	// "not found", but the entry is still there. The read must fail rather
	// than treat w/d as not opaque.
	It("fails on a damaged xattr", func() {
		needTools("mke2fs", "debugfs")
		img, r := testUpper()
		zeroXattrMagic(img, dirInode(img, "/data/w/d"))
		_, _, err := r.read(readCtx(), nil)
		Expect(err).To(MatchError(And(ContainSubstring("damaged"), ContainSubstring("w/d"))), "a folder with a zeroed EA magic")
	})

	// A file staged by an earlier read must not survive into the next one.
	It("clears the stage", func() {
		needTools("mke2fs", "debugfs")
		_, r := testUpper()
		stale := filepath.Join(r.stage, "999")
		Expect(os.WriteFile(stale, []byte("stale"), 0o600)).To(Succeed())
		_, _, err := r.read(readCtx(), nil)
		Expect(err).To(Succeed())
		_, err = os.Stat(stale)
		Expect(os.IsNotExist(err)).To(BeTrue(), "a stale staged file survived the read: %v", err)
	})
})

var (
	reBlockSize = regexp.MustCompile(`Block size:\s+(\d+)`)
	reInodeNum  = regexp.MustCompile(`Inode: (\d+)`)
	reImap      = regexp.MustCompile(`located at block (\d+), offset 0x([0-9a-fA-F]+)`)
)

func debugfsRead(img, cmd string) string {
	GinkgoHelper()
	out, err := exec.Command("debugfs", "-c", "-R", cmd, img).CombinedOutput()
	Expect(err).To(Succeed(), "debugfs %s: %s", cmd, out)
	return string(out)
}

// dirInode returns the inode number of path in img.
func dirInode(img, path string) uint64 {
	GinkgoHelper()
	m := reInodeNum.FindStringSubmatch(debugfsRead(img, "stat "+path))
	Expect(m).NotTo(BeNil(), "no inode for %s", path)
	n, _ := strconv.ParseUint(m[1], 10, 64)
	return n
}

// zeroXattrMagic overwrites the magic that starts the in-inode attribute area
// of inode ino, in place, leaving the entries.
func zeroXattrMagic(img string, ino uint64) {
	GinkgoHelper()
	m := reBlockSize.FindStringSubmatch(debugfsRead(img, "show_super_stats -h"))
	Expect(m).NotTo(BeNil(), "no block size")
	bs, _ := strconv.ParseInt(m[1], 10, 64)
	m = reImap.FindStringSubmatch(debugfsRead(img, fmt.Sprintf("imap <%d>", ino)))
	Expect(m).NotTo(BeNil(), "no location for inode %d", ino)
	blk, _ := strconv.ParseInt(m[1], 10, 64)
	off, _ := strconv.ParseInt(m[2], 16, 64)
	f, err := os.OpenFile(img, os.O_RDWR, 0)
	Expect(err).To(Succeed())
	defer func() { _ = f.Close() }()
	raw := make([]byte, 256)
	_, err = f.ReadAt(raw, blk*bs+off)
	Expect(err).To(Succeed())
	magicAt := 128 + int(binary.LittleEndian.Uint16(raw[0x80:]))
	Expect(binary.LittleEndian.Uint32(raw[magicAt:])).To(BeEquivalentTo(0xEA020000), "inode %d has no attribute magic to zero", ino) // EXT4_XATTR_MAGIC
	_, err = f.WriteAt(make([]byte, 4), blk*bs+off+int64(magicAt))
	Expect(err).To(Succeed())
}

// patchName replaces the one occurrence of from in img with to, which must
// have the same length.
func patchName(img, from, to string) {
	GinkgoHelper()
	Expect(to).To(HaveLen(len(from)), "patchName: lengths differ")
	data, err := os.ReadFile(img)
	Expect(err).To(Succeed())
	i := bytes.Index(data, []byte(from))
	Expect(i >= 0 && !bytes.Contains(data[i+1:], []byte(from))).To(BeTrue(), "name %q is not in the image once", from)
	copy(data[i:], to)
	Expect(os.WriteFile(img, data, 0o600)).To(Succeed())
}

// setNameLen sets the name_len byte of the directory entry of name in img.
func setNameLen(img, name string, n byte) {
	GinkgoHelper()
	data, err := os.ReadFile(img)
	Expect(err).To(Succeed())
	i := bytes.Index(data, []byte(name))
	Expect(i >= 2 && !bytes.Contains(data[i+1:], []byte(name))).To(BeTrue(), "name %q is not in the image once", name)
	data[i-2] = n
	Expect(os.WriteFile(img, data, 0o600)).To(Succeed())
}
