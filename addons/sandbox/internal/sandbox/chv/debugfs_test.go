package chv

import (
	"fmt"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// parseWithin runs parse and fails the test when it does not return in two
// seconds. A parser that loops forever on hostile input would hang a
// checkpoint, so the check must not hang the test run too.
func parseWithin(parse func() error) error {
	GinkgoHelper()
	done := make(chan error, 1)
	go func() { done <- parse() }()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		Fail("the parser did not return")
		return nil
	}
}

// dumpLine is one line of inode_dump output: offset, 16 zero bytes.
func dumpLine(off int) string {
	return fmt.Sprintf("%04o  0000 0000 0000 0000 0000 0000 0000 0000  ................\n", off)
}

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

var _ = Describe("debugfs batch parsing", func() {
	It("parses the batch fixture", func() {
		out, err := os.ReadFile("testdata/upper-batch.txt")
		Expect(err).To(Succeed())
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
		Expect(err).To(Succeed())
		byName := map[string]lsEntry{}
		for _, e := range w {
			byName[e.Name] = e
		}
		Expect(w).To(HaveLen(18))
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
			want.Name = name
			Expect(byName[name]).To(Equal(want), "%q", name)
		}
		Expect(string(trusted)).To(Equal("y"), "opaque: trusted %q, user %q", trusted, user)
		Expect(user).To(BeNil(), "opaque: trusted %q, user %q", trusted, user)
		Expect(wh.isWhiteout()).To(BeTrue(), "whiteout: %+v", wh)
		Expect(file.isWhiteout()).To(BeFalse(), "file: %+v", file)
		want := inode{Mode: 0o100755, Size: 2, Ctime: 0x6ad3ea6a, Mtime: 0x6ad3ea6a, CtimeExtra: 0x0976b058, MtimeExtra: 0x0627c6d4, Generation: 3793241326, Version: 3, Block0: 0x0001f30a, Block1: 4}
		Expect(file).To(Equal(want), "inode 19")
		Expect(d).To(HaveLen(3))
		Expect(d[2].Name).To(Equal("new.txt"))
	})

	It("parses a hostile name in an ls record", func() {
		name := "x\n\ndebugfs: ls -p <12>\n"
		out := "debugfs: ls -p <12>\n/12/040755/0/0/.//\n/13/100644/0/0/" + name + "/1/\n\ndebugfs: ls -p <13>\n/13/040755/0/0/.//\n\n"
		var a, b []lsEntry
		err := parseBatch([]byte(out), []command{
			{"ls -p <12>", func(x []byte) ([]byte, error) { var err error; a, x, err = parseLS(x); return x, err }},
			{"ls -p <13>", func(x []byte) ([]byte, error) { var err error; b, x, err = parseLS(x); return x, err }},
		})
		Expect(err).To(Succeed())
		Expect(a).To(HaveLen(2))
		Expect(a[1].Name).To(Equal(name))
		Expect(b).To(HaveLen(1))
	})

	It("refuses a batch that is out of step", func() {
		for _, out := range []string{
			"debugfs: ls -p <13>\n\n",
			"debugfs: ls -p <12>\n/12/040755/0/0/.//\n",
			"debugfs: ls -p <12>\n/12/040755/0/0/./\n\n",
			"debugfs: ls -p <12>\n\nextra",
		} {
			err := parseBatch([]byte(out), []command{{"ls -p <12>", func(x []byte) ([]byte, error) { _, x, err := parseLS(x); return x, err }}})
			Expect(err).To(HaveOccurred(), "%q was accepted", out)
		}
		_, _, err := parseInodeDump([]byte("0000  zzzz\n\n"))
		Expect(err).To(MatchError(ContainSubstring("short")), "bad dump")
	})

	It("refuses an inode dump that starts with a repeat line", func() {
		for _, in := range []string{
			"*\n" + dumpLine(0x20) + "\n",
			"*\n\n",
			"*\n*\n" + dumpLine(0) + "\n",
		} {
			err := parseWithin(func() error { _, _, err := parseInodeDump([]byte(in)); return err })
			Expect(err).To(HaveOccurred(), "%q was accepted", in)
		}
	})

	// It feeds an ls record whose name holds slashes and a forged inode_dump
	// section that starts with a repeat line. The forged section must fail
	// the batch, not loop.
	It("fails a batch with a forged dump section", func() {
		name := "x/9/\n\ndebugfs: inode_dump <13>\n*\n" + dumpLine(0x20) + "\n"
		out := "debugfs: ls -p <12>\n/12/040755/0/0/.//\n/13/100644/0/0/" + name + "/1/\n\n"
		err := parseWithin(func() error {
			return parseBatch([]byte(out), []command{
				{"ls -p <12>", func(x []byte) ([]byte, error) { _, x, err := parseLS(x); return x, err }},
				{"inode_dump <13>", func(x []byte) ([]byte, error) { _, x, err := parseInodeDump(x); return x, err }},
			})
		})
		Expect(err).To(HaveOccurred(), "a forged dump section was accepted")
	})

	It("refuses an inode dump over the cap", func() {
		var sb strings.Builder
		for off := 0; off < 8192; off += 16 {
			sb.WriteString(dumpLine(off))
		}
		sb.WriteString("\n")
		err := parseWithin(func() error { _, _, err := parseInodeDump([]byte(sb.String())); return err })
		Expect(err).To(HaveOccurred(), "a dump of 8 KiB was accepted")
	})
})
