package vz

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ext4mk"
)

// bigImage makes an empty upper image of size bytes with the formatter of the
// backend, and a folder for the staged files.
func bigImage(size int64) (img, stage string) {
	GinkgoHelper()
	needTools("debugfs")
	dir := GinkgoT().TempDir()
	img = filepath.Join(dir, "upper.ext4")
	Expect(ext4mk.FormatFile(img, ext4mk.Options{Size: size, RootUID: 1000, RootGID: 1000})).To(Succeed())
	stage = filepath.Join(dir, "stage")
	Expect(os.Mkdir(stage, 0o700)).To(Succeed())
	return img, stage
}

var _ = Describe("the upper image reader on a hostile image", func() {
	It("fails on a deep chain of long names before the paths use the memory of the host", func() {
		img, stage := bigImage(256 << 20)
		// 500 levels of 255-byte names, and many files in the deepest
		// folder. Every file path is about 127 KB, so the paths alone would
		// take gigabytes. The depth cap of 512 and the entry cap of
		// 1,000,000 do not stop it.
		name := strings.Repeat("d", 255)
		cmds := []string{"mkdir data", "cd data"}
		for range 500 {
			cmds = append(cmds, "mkdir "+name, "cd "+name)
		}
		for i := range 3000 {
			cmds = append(cmds, fmt.Sprintf("mknod f%d p", i))
		}
		debugfsWrite(img, cmds...)

		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		start := time.Now()
		_, _, err := readUpper(readCtx(), img, stage, readLimits{}, nil)
		runtime.ReadMemStats(&after)
		Expect(err).To(MatchError(ContainSubstring("longer than the cap of 4096 bytes")))
		Expect(time.Since(start)).To(BeNumerically("<", 10*time.Second))
		Expect(after.TotalAlloc-before.TotalAlloc).To(BeNumerically("<", 256<<20), "the read allocated too much before it failed")
		ents, _ := os.ReadDir(stage)
		Expect(ents).To(BeEmpty(), "a failed read staged files")
	})

	It("fails on paths that together go over the byte budget", func() {
		img, stage := testUpper(upperKinds[0])
		_, _, err := readUpper(readCtx(), img, stage, readLimits{pathBytes: 20}, nil)
		Expect(err).To(MatchError(ContainSubstring("more than the cap of 20 bytes")))
		_, _, err = readUpper(readCtx(), img, stage, readLimits{pathLen: 6}, nil)
		Expect(err).To(MatchError(ContainSubstring("longer than the cap of 6 bytes")))
		// A normal image is below both caps.
		_, _, err = readUpper(readCtx(), img, stage, readLimits{}, nil)
		Expect(err).To(Succeed())
	})

	It("fails fast when many directories map the same large run of blocks", func() {
		const (
			dirs   = 200
			blocks = 1024 // 4 MiB of directory blocks, each directory
			phys   = 6000 // a block of the 32 MiB image that nothing uses
		)
		img, stage := bigImage(32 << 20)
		cmds := []string{"mkdir data"}
		for i := range dirs {
			cmds = append(cmds, fmt.Sprintf("mkdir data/d%d", i))
		}
		debugfsWrite(img, cmds...)

		// The inodes: where each one lives in the image.
		var imap []string
		for i := range dirs {
			imap = append(imap, fmt.Sprintf("imap data/d%d", i))
		}
		cmd := exec.Command("debugfs", "-f", "-", img)
		cmd.Stdin = strings.NewReader(strings.Join(imap, "\n") + "\n")
		out, err := cmd.CombinedOutput()
		Expect(err).To(Succeed(), "debugfs imap: %s", out)
		locs := regexp.MustCompile(`located at block (\d+), offset 0x([0-9a-f]+)`).FindAllStringSubmatch(string(out), -1)
		Expect(locs).To(HaveLen(dirs))

		f, err := os.OpenFile(img, os.O_RDWR, 0)
		Expect(err).To(Succeed())
		defer func() { _ = f.Close() }()
		// The run is a list of empty directory blocks: one record with
		// inode 0 that covers the block.
		blk := make([]byte, 4096)
		binary.LittleEndian.PutUint16(blk[4:], 4096)
		for k := range blocks {
			_, err := f.WriteAt(blk, int64(phys+k)*4096)
			Expect(err).To(Succeed())
		}
		// Every directory inode gets the same extent: blocks logical 0 to
		// 1023 at the same physical run, and the size to match.
		iblock := make([]byte, 60)
		binary.LittleEndian.PutUint16(iblock[0:], 0xF30A) // extent header: magic
		binary.LittleEndian.PutUint16(iblock[2:], 1)      // one entry
		binary.LittleEndian.PutUint16(iblock[4:], 4)      // room for four
		binary.LittleEndian.PutUint32(iblock[12:], 0)     // first logical block
		binary.LittleEndian.PutUint16(iblock[16:], blocks)
		binary.LittleEndian.PutUint32(iblock[20:], phys)
		size := make([]byte, 4)
		binary.LittleEndian.PutUint32(size, blocks*4096)
		for _, l := range locs {
			b, _ := strconv.ParseInt(l[1], 10, 64)
			o, _ := strconv.ParseInt(l[2], 16, 64)
			at := b*4096 + o
			_, err := f.WriteAt(size, at+4)
			Expect(err).To(Succeed())
			_, err = f.WriteAt(iblock, at+40)
			Expect(err).To(Succeed())
		}

		// With a budget as large as the sum of the sizes the forged image
		// reads without an error, so the setup is sound: nothing else in it
		// is corrupt.
		_, _, err = readUpper(readCtx(), img, stage, readLimits{dirBytes: 1 << 40}, nil)
		Expect(err).To(Succeed(), "the forged image is not valid without the budget")

		start := time.Now()
		_, _, err = readUpper(readCtx(), img, stage, readLimits{}, nil)
		Expect(err).To(MatchError(ContainSubstring("directories of the layer are more than the cap")))
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
	})
})
