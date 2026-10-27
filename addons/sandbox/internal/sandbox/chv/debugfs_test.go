package chv

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// testdata/upper-batch.txt is the output of one debugfs batch on an upper
// image that the Linux kernel wrote through overlayfs: new.txt (new, 0755),
// edit.txt (changed), gone.txt (deleted, a whiteout), a.txt renamed to b.txt
// (a whiteout and a copy), d replaced (opaque), link (a symlink), sub (new
// folder), "with space.txt", "new\nline.txt", hard.txt (a hard link of
// b.txt), fifo, big.bin, sparse.bin, keep.txt (chmod 600). Commands:
//
//	ls -p <18>
//	ea_get -x <24> trusted.overlay.opaque
//	ea_get -x <24> user.overlay.opaque
//	inode_dump <21>
//	inode_dump <19>
//	ls -p <24>
func TestParseBatchFixture(t *testing.T) {
	out, err := os.ReadFile("testdata/upper-batch.txt")
	if err != nil {
		t.Fatal(err)
	}
	var w, d []lsEntry
	var trusted, user []byte
	var wh, file inode
	ls := func(dst *[]lsEntry) func([]byte) ([]byte, error) {
		return func(b []byte) ([]byte, error) {
			var err error
			*dst, b, err = parseLS(b)
			return b, err
		}
	}
	ea := func(key string, dst *[]byte) func([]byte) ([]byte, error) {
		return func(b []byte) ([]byte, error) {
			var err error
			*dst, b, err = parseEAGet(key)(b)
			return b, err
		}
	}
	dump := func(dst *inode) func([]byte) ([]byte, error) {
		return func(b []byte) ([]byte, error) {
			var err error
			*dst, b, err = parseInodeDump(b)
			return b, err
		}
	}
	err = parseBatch(out, []command{
		{"ls -p <18>", ls(&w)},
		{"ea_get -x <24> trusted.overlay.opaque", ea("trusted.overlay.opaque", &trusted)},
		{"ea_get -x <24> user.overlay.opaque", ea("user.overlay.opaque", &user)},
		{"inode_dump <21>", dump(&wh)},
		{"inode_dump <19>", dump(&file)},
		{"ls -p <24>", ls(&d)},
	})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]lsEntry{}
	for _, e := range w {
		byName[e.Name] = e
	}
	if len(w) != 18 {
		t.Fatalf("%d entries: %+v", len(w), w)
	}
	for name, want := range map[string]lsEntry{
		"new.txt":        {Ino: 19, Mode: 0o100755, Size: 2},
		"gone.txt":       {Ino: 21, Mode: 0o020000},
		"d":              {Ino: 24, Mode: 0o040755},
		"link":           {Ino: 26, Mode: 0o120777, Size: 7},
		"new\nline.txt":  {Ino: 33, Mode: 0o100644, Size: 3},
		"with space.txt": {Ino: 14, Mode: 0o100644, Size: 3},
		"hard.txt":       {Ino: 22, Mode: 0o100644, Size: 8},
		"fifo":           {Ino: 34, Mode: 0o010644},
		"big.bin":        {Ino: 35, Mode: 0o100644, Size: 300000},
	} {
		got := byName[name]
		want.Name = name
		if got != want {
			t.Errorf("%q = %+v, want %+v", name, got, want)
		}
	}
	if string(trusted) != "y" || user != nil {
		t.Fatalf("opaque: trusted %q, user %q", trusted, user)
	}
	if !wh.isWhiteout() || file.isWhiteout() {
		t.Fatalf("whiteout: %+v, file: %+v", wh, file)
	}
	want := inode{Mode: 0o100755, Size: 2, Ctime: 0x6ad3ea6a, Mtime: 0x6ad3ea6a, CtimeExtra: 0x0976b058, MtimeExtra: 0x0627c6d4, Generation: 3793241326, Version: 3, Block0: 0x0001f30a, Block1: 4}
	if file != want {
		t.Fatalf("inode 19 = %+v, want %+v", file, want)
	}
	if len(d) != 3 || d[2].Name != "new.txt" {
		t.Fatalf("d = %+v", d)
	}
}

func TestParseLSHostileName(t *testing.T) {
	name := "x\n\ndebugfs: ls -p <12>\n"
	out := "debugfs: ls -p <12>\n/12/040755/0/0/.//\n/13/100644/0/0/" + name + "/1/\n\ndebugfs: ls -p <13>\n/13/040755/0/0/.//\n\n"
	var a, b []lsEntry
	err := parseBatch([]byte(out), []command{
		{"ls -p <12>", func(x []byte) ([]byte, error) { var err error; a, x, err = parseLS(x); return x, err }},
		{"ls -p <13>", func(x []byte) ([]byte, error) { var err error; b, x, err = parseLS(x); return x, err }},
	})
	if err != nil || len(a) != 2 || a[1].Name != name || len(b) != 1 {
		t.Fatalf("err %v, a %+v, b %+v", err, a, b)
	}
}

func TestParseBatchOutOfStep(t *testing.T) {
	for _, out := range []string{
		"debugfs: ls -p <13>\n\n",
		"debugfs: ls -p <12>\n/12/040755/0/0/.//\n",
		"debugfs: ls -p <12>\n/12/040755/0/0/./\n\n",
		"debugfs: ls -p <12>\n\nextra",
	} {
		err := parseBatch([]byte(out), []command{{"ls -p <12>", func(x []byte) ([]byte, error) { _, x, err := parseLS(x); return x, err }}})
		if err == nil {
			t.Fatalf("%q was accepted", out)
		}
	}
	if _, _, err := parseInodeDump([]byte("0000  zzzz\n\n")); err == nil || !strings.Contains(err.Error(), "short") {
		t.Fatalf("bad dump: %v", err)
	}
}

// parseWithin runs parse and fails the test when it does not return in two
// seconds. A parser that loops forever on hostile input would hang a
// checkpoint, so the check must not hang the test run too.
func parseWithin(t *testing.T, parse func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- parse() }()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("the parser did not return")
		return nil
	}
}

// dumpLine is one line of inode_dump output: offset, 16 zero bytes.
func dumpLine(off int) string {
	return fmt.Sprintf("%04o  0000 0000 0000 0000 0000 0000 0000 0000  ................\n", off)
}

func TestParseInodeDumpStarFirst(t *testing.T) {
	for _, in := range []string{
		"*\n" + dumpLine(0x20) + "\n",
		"*\n\n",
		"*\n*\n" + dumpLine(0) + "\n",
	} {
		err := parseWithin(t, func() error { _, _, err := parseInodeDump([]byte(in)); return err })
		if err == nil {
			t.Fatalf("%q was accepted", in)
		}
	}
}

// TestParseBatchForgedDump feeds an ls record whose name holds slashes and
// a forged inode_dump section that starts with a repeat line. The forged
// section must fail the batch, not loop.
func TestParseBatchForgedDump(t *testing.T) {
	name := "x/9/\n\ndebugfs: inode_dump <13>\n*\n" + dumpLine(0x20) + "\n"
	out := "debugfs: ls -p <12>\n/12/040755/0/0/.//\n/13/100644/0/0/" + name + "/1/\n\n"
	err := parseWithin(t, func() error {
		return parseBatch([]byte(out), []command{
			{"ls -p <12>", func(x []byte) ([]byte, error) { _, x, err := parseLS(x); return x, err }},
			{"inode_dump <13>", func(x []byte) ([]byte, error) { _, x, err := parseInodeDump(x); return x, err }},
		})
	})
	if err == nil {
		t.Fatal("a forged dump section was accepted")
	}
}

func TestParseInodeDumpOverCap(t *testing.T) {
	var sb strings.Builder
	for off := 0; off < 8192; off += 16 {
		sb.WriteString(dumpLine(off))
	}
	sb.WriteString("\n")
	err := parseWithin(t, func() error { _, _, err := parseInodeDump([]byte(sb.String())); return err })
	if err == nil {
		t.Fatal("a dump of 8 KiB was accepted")
	}
}
