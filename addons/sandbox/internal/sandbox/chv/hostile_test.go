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
	"strings"
	"testing"
	"time"
)

// plainName and forgedName have the same length (20 bytes). Written over
// plainName in a directory block, forgedName makes ls -p print a second
// record, with inode 1.
const (
	plainName  = "abcdefghijklmnopqrst"
	forgedName = "x/1/\n/1/040755/0/0/y"
)

// hostileUpper is testUpper plus the file plainName in data/w.
func hostileUpper(t *testing.T) (string, upperReader) {
	t.Helper()
	img, r := testUpper(t)
	debugfsWrite(t, img, "write "+filepath.Join(filepath.Dir(img), "src", "new.txt")+" data/w/"+plainName)
	return img, r
}

func readCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// TestReadUpperLoop: a directory hard link (w/d/again is w/d) must fail the
// read at once, and name the path.
func TestReadUpperLoop(t *testing.T) {
	needTools(t, "mke2fs", "debugfs")
	img, r := testUpper(t)
	debugfsWrite(t, img, "link data/w/d data/w/d/again")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	_, _, err := r.read(ctx, nil)
	if err == nil || !strings.Contains(err.Error(), "w/d/again") {
		t.Fatalf("a directory loop: %v (after %v)", err, time.Since(start))
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("the loop took %v", d)
	}
}

// TestReadUpperForgedRecord: a name with "/" forges a record with inode 1,
// which is below first_ino. The read must fail on it.
func TestReadUpperForgedRecord(t *testing.T) {
	needTools(t, "mke2fs", "debugfs")
	img, r := hostileUpper(t)
	patchName(t, img, plainName, forgedName)
	_, _, err := r.read(readCtx(t), nil)
	if err == nil || !strings.Contains(err.Error(), "inode 1,") {
		t.Fatalf("a forged record with inode 1: %v", err)
	}
}

// TestReadUpperEmptyName: an entry with an empty name would land at the
// wrong path. The read must fail.
func TestReadUpperEmptyName(t *testing.T) {
	needTools(t, "mke2fs", "debugfs")
	img, r := hostileUpper(t)
	setNameLen(t, img, plainName, 0)
	_, _, err := r.read(readCtx(t), nil)
	if err == nil || !strings.Contains(err.Error(), "empty name") {
		t.Fatalf("an empty name: %v", err)
	}
}

// TestReadUpperDamagedXattr: the EA magic of the opaque folder w/d is zeroed.
// debugfs then says "not found", but the entry is still there. The read must
// fail rather than treat w/d as not opaque.
func TestReadUpperDamagedXattr(t *testing.T) {
	needTools(t, "mke2fs", "debugfs")
	img, r := testUpper(t)
	zeroXattrMagic(t, img, dirInode(t, img, "/data/w/d"))
	_, _, err := r.read(readCtx(t), nil)
	if err == nil || !strings.Contains(err.Error(), "damaged") || !strings.Contains(err.Error(), "w/d") {
		t.Fatalf("a folder with a zeroed EA magic: %v", err)
	}
}

// TestReadUpperClearsStage: a file staged by an earlier read must not survive
// into the next one.
func TestReadUpperClearsStage(t *testing.T) {
	needTools(t, "mke2fs", "debugfs")
	_, r := testUpper(t)
	stale := filepath.Join(r.stage, "999")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.read(readCtx(t), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("a stale staged file survived the read: %v", err)
	}
}

var (
	reBlockSize = regexp.MustCompile(`Block size:\s+(\d+)`)
	reInodeNum  = regexp.MustCompile(`Inode: (\d+)`)
	reImap      = regexp.MustCompile(`located at block (\d+), offset 0x([0-9a-fA-F]+)`)
)

func debugfsRead(t *testing.T, img, cmd string) string {
	t.Helper()
	out, err := exec.Command("debugfs", "-c", "-R", cmd, img).CombinedOutput()
	if err != nil {
		t.Fatalf("debugfs %s: %v\n%s", cmd, err, out)
	}
	return string(out)
}

// dirInode returns the inode number of path in img.
func dirInode(t *testing.T, img, path string) uint64 {
	t.Helper()
	m := reInodeNum.FindStringSubmatch(debugfsRead(t, img, "stat "+path))
	if m == nil {
		t.Fatalf("no inode for %s", path)
	}
	n, _ := strconv.ParseUint(m[1], 10, 64)
	return n
}

// zeroXattrMagic overwrites the magic that starts the in-inode attribute area
// of inode ino, in place, leaving the entries.
func zeroXattrMagic(t *testing.T, img string, ino uint64) {
	t.Helper()
	m := reBlockSize.FindStringSubmatch(debugfsRead(t, img, "show_super_stats -h"))
	if m == nil {
		t.Fatal("no block size")
	}
	bs, _ := strconv.ParseInt(m[1], 10, 64)
	m = reImap.FindStringSubmatch(debugfsRead(t, img, fmt.Sprintf("imap <%d>", ino)))
	if m == nil {
		t.Fatalf("no location for inode %d", ino)
	}
	blk, _ := strconv.ParseInt(m[1], 10, 64)
	off, _ := strconv.ParseInt(m[2], 16, 64)
	f, err := os.OpenFile(img, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	raw := make([]byte, 256)
	if _, err := f.ReadAt(raw, blk*bs+off); err != nil {
		t.Fatal(err)
	}
	magicAt := 128 + int(binary.LittleEndian.Uint16(raw[0x80:]))
	if binary.LittleEndian.Uint32(raw[magicAt:]) != 0xEA020000 { // EXT4_XATTR_MAGIC
		t.Fatalf("inode %d has no attribute magic to zero", ino)
	}
	if _, err := f.WriteAt(make([]byte, 4), blk*bs+off+int64(magicAt)); err != nil {
		t.Fatal(err)
	}
}

// patchName replaces the one occurrence of from in img with to, which must
// have the same length.
func patchName(t *testing.T, img, from, to string) {
	t.Helper()
	if len(from) != len(to) {
		t.Fatal("patchName: lengths differ")
	}
	data, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(data, []byte(from))
	if i < 0 || bytes.Contains(data[i+1:], []byte(from)) {
		t.Fatalf("name %q is not in the image once", from)
	}
	copy(data[i:], to)
	if err := os.WriteFile(img, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// setNameLen sets the name_len byte of the directory entry of name in img.
func setNameLen(t *testing.T, img, name string, n byte) {
	t.Helper()
	data, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(data, []byte(name))
	if i < 2 || bytes.Contains(data[i+1:], []byte(name)) {
		t.Fatalf("name %q is not in the image once", name)
	}
	data[i-2] = n
	if err := os.WriteFile(img, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
