package chv

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// synthInode is a 256-byte inode with the fields that the parser reads.
func synthInode(extra uint16) []byte {
	raw := make([]byte, 256)
	le := binary.LittleEndian
	le.PutUint16(raw[0x00:], 0o100644)
	le.PutUint16(raw[0x80:], extra)
	le.PutUint32(raw[0x84:], 0x11111111) // ctime_extra
	le.PutUint32(raw[0x88:], 0x22222222) // mtime_extra
	le.PutUint32(raw[0x98:], 0x33333333) // i_version_hi
	return raw
}

var _ = Describe("upper reader limits and parsers", func() {
	It("caps the number of entries", func() {
		needTools("mke2fs", "debugfs")
		_, r := testUpper()
		r.lim = readLimits{entries: 3}
		_, _, err := r.read(readCtx(), nil)
		Expect(err).To(MatchError(ContainSubstring("more than 3 entries")), "an entry cap of 3")
	})

	It("caps the depth", func() {
		needTools("mke2fs", "debugfs")
		_, r := testUpper()
		r.lim = readLimits{depth: 1}
		_, _, err := r.read(readCtx(), nil)
		Expect(err).To(MatchError(ContainSubstring("deeper than 1")), "a depth cap of 1 with data/w/d")
	})

	It("caps the bytes of one path and of all paths", func() {
		needTools("mke2fs", "debugfs")
		_, r := testUpper()
		r.lim = readLimits{pathLen: 6}
		_, _, err := r.read(readCtx(), nil)
		Expect(err).To(MatchError(ContainSubstring("longer than the cap of 6 bytes")))
		r.lim = readLimits{pathBytes: 20}
		_, _, err = r.read(readCtx(), nil)
		Expect(err).To(MatchError(ContainSubstring("more than the cap of 20 bytes")))
	})

	It("fails on a deep chain of long names before the paths use the memory of the host", func() {
		needTools("mke2fs", "debugfs")
		dir := GinkgoT().TempDir()
		img := filepath.Join(dir, "upper.ext4")
		Expect(os.WriteFile(img, nil, 0o600)).To(Succeed())
		Expect(os.Truncate(img, 256<<20)).To(Succeed())
		out, err := exec.Command("mke2fs", "-q", "-F", "-t", "ext4", "-O", "^has_journal,^metadata_csum", "-E", "root_owner=1000:1000", img).CombinedOutput()
		Expect(err).To(Succeed(), "mke2fs: %s", out)
		// 500 levels of 255-byte names and many files in the deepest folder:
		// every file path is about 127 KB.
		name := strings.Repeat("d", 255)
		cmds := []string{"mkdir data", "cd data"}
		for range 500 {
			cmds = append(cmds, "mkdir "+name, "cd "+name)
		}
		for i := range 3000 {
			cmds = append(cmds, fmt.Sprintf("mknod f%d p", i))
		}
		debugfsWrite(img, cmds...)
		stage := filepath.Join(dir, "stage")
		Expect(os.Mkdir(stage, 0o700)).To(Succeed())
		r := upperReader{argv: []string{"debugfs", "-c", "-f", "-", img}, stage: stage, host: stage}
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		start := time.Now()
		_, _, err = r.read(readCtx(), nil)
		runtime.ReadMemStats(&after)
		Expect(err).To(MatchError(ContainSubstring("longer than the cap of 4096 bytes")))
		Expect(time.Since(start)).To(BeNumerically("<", 30*time.Second))
		Expect(after.TotalAlloc-before.TotalAlloc).To(BeNumerically("<", 256<<20), "the read allocated too much before it failed")
		ents, _ := os.ReadDir(stage)
		Expect(ents).To(BeEmpty(), "a failed read staged files")
	})

	It("caps the output", func() {
		needTools("mke2fs", "debugfs")
		_, r := testUpper()
		r.lim = readLimits{output: 16}
		_, _, err := r.read(readCtx(), nil)
		Expect(err).To(MatchError(ContainSubstring("over 16 bytes")), "an output cap of 16 bytes")
	})

	It("fills the read limit defaults", func() {
		l := readLimits{}.withDefaults()
		Expect(l.depth).To(Equal(512), "defaults: %+v", l)
		Expect(l.entries).To(Equal(1000000), "defaults: %+v", l)
		Expect(l.output).To(BeEquivalentTo(512<<20), "defaults: %+v", l)
		Expect(l.pathLen).To(Equal(4096), "defaults: %+v", l)
		Expect(l.pathBytes).To(BeEquivalentTo(128<<20), "defaults: %+v", l)
		got := (readLimits{depth: 3}).withDefaults()
		Expect(got.depth).To(Equal(3), "a set field must stay: %+v", got)
		Expect(got.entries).To(Equal(1000000), "a set field must stay: %+v", got)
	})

	It("checks the stderr of debugfs", func() {
		ea := []command{{text: "ea_get -x <13> trusted.overlay.opaque"}}
		missing := eaNotFound + " while getting extended attribute\n"
		banner := "debugfs 1.47.2 (1-Jan-2025)\n"
		for _, tc := range []struct {
			name   string
			stderr string
			cmds   []command
			ok     bool
		}{
			{"banner only", banner, nil, true},
			{"a missing key", banner + missing, ea, true},
			{"a corrupt attribute", "ea_get: Corrupt extended attribute block\n", ea, false},
			{"more misses than ea_get", missing + missing, ea, false},
			{"a miss without ea_get", missing, []command{{text: "ls -p <13>"}}, false},
			{"a failed dump", "dump: Cannot write file\n", nil, false},
		} {
			err := checkStderr([]byte(tc.stderr), tc.cmds)
			Expect(err == nil).To(Equal(tc.ok), "%s: err %v, want ok %v", tc.name, err, tc.ok)
		}
	})

	It("sets a default debugfs deadline and keeps a caller deadline", func() {
		ctx, cancel := debugfsContext(context.Background())
		defer cancel()
		dl, ok := ctx.Deadline()
		Expect(ok).To(BeTrue(), "no default deadline: %v %v", dl, ok)
		Expect(time.Until(dl)).To(BeNumerically("<=", debugfsTimeout), "default deadline %v", dl)
		Expect(time.Until(dl)).To(BeNumerically(">=", debugfsTimeout-time.Minute), "default deadline %v", dl)
		parent, pc := context.WithTimeout(context.Background(), time.Hour)
		defer pc()
		ctx2, c2 := debugfsContext(parent)
		defer c2()
		dl2, _ := ctx2.Deadline()
		pd, _ := parent.Deadline()
		Expect(dl2.Equal(pd)).To(BeTrue(), "a caller deadline was replaced: %v, want %v", dl2, pd)
	})

	It("bounds the inode numbers by the superblock stats", func() {
		body := "Block count:              8192\nBlock size:               1024\nInode count:              2048\nFirst inode:              11\nInode size: 256\n"
		b, rest, err := parseSuperStats([]byte(body))
		Expect(err).To(Succeed())
		Expect(rest).To(BeNil())
		Expect(b).To(Equal(fsBounds{firstIno: 11, inodes: 2048}))
		for _, bad := range []string{
			"Inode count: 2048\n",
			"First inode: 11\n",
			"Inode count: 2048\nFirst inode: 1\n",
			"Inode count: 5\nFirst inode: 11\n",
			"Inode count: x\nFirst inode: 11\n",
		} {
			_, _, err := parseSuperStats([]byte(bad))
			Expect(err).To(HaveOccurred(), "%q was accepted", bad)
		}
		for ino, want := range map[uint64]bool{1: false, 2: true, 10: false, 11: true, 2048: true, 2049: false} {
			Expect(b.inodeInRange(ino)).To(Equal(want), "inode %d in range", ino)
		}
	})

	It("reads the extra inode fields that follow extra_isize", func() {
		for _, tc := range []struct {
			extra                   uint16
			ctime, mtime, versionHi bool
		}{
			{0x00, false, false, false},
			{0x08, true, false, false},
			{0x0C, true, true, false},
			{0x1C, true, true, true},
		} {
			in, _, err := parseInodeDump(dumpText(synthInode(tc.extra)))
			Expect(err).To(Succeed())
			Expect(in.CtimeExtra == 0x11111111).To(Equal(tc.ctime), "extra %#x: ctime_extra", tc.extra)
			Expect(in.MtimeExtra == 0x22222222).To(Equal(tc.mtime), "extra %#x: mtime_extra", tc.extra)
			Expect(in.Version>>32 == 0x33333333).To(Equal(tc.versionHi), "extra %#x: i_version_hi", tc.extra)
		}
	})

	It("detects a bad inline xattr area", func() {
		le := binary.LittleEndian
		empty := synthInode(32) // the area after 128+32 is zero
		ok := synthInode(32)
		le.PutUint32(ok[0xA0:], xattrMagic)
		ok[0xA4] = 0x0e
		bad := synthInode(32)
		bad[0xA4] = 0x0e // entries without the magic
		for _, tc := range []struct {
			name string
			raw  []byte
			want bool
		}{
			{"an empty area", empty, false},
			{"entries with the magic", ok, false},
			{"entries without the magic", bad, true},
		} {
			Expect(inlineXattrBad(tc.raw)).To(Equal(tc.want), tc.name)
		}
	})
})
