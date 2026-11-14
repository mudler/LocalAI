package ext4ro

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("a large sparse image", func() {
	It("opens and walks a 2 GiB image in milliseconds, and streams a 1 TiB file", func() {
		needTools("mke2fs", "debugfs")
		dir, err := os.MkdirTemp(suiteDir, "sparse-")
		Expect(err).To(Succeed())
		DeferCleanup(os.RemoveAll, dir)
		img := filepath.Join(dir, "image")
		fh, err := os.Create(img)
		Expect(err).To(Succeed())
		Expect(fh.Truncate(2 << 30)).To(Succeed())
		Expect(fh.Close()).To(Succeed())
		// Few inodes and lazy tables keep mke2fs from writing much.
		sh("mke2fs", "-q", "-F", "-t", "ext4", "-O", "^has_journal", "-N", "256", "-E", "lazy_itable_init=1,nodiscard", img)
		src := filepath.Join(dir, "small")
		Expect(os.WriteFile(src, []byte("tiny file\n"), 0o600)).To(Succeed())
		// A file of 1 TiB with one mapped block: the size is a field of the
		// inode, so debugfs can set it without any data.
		script := filepath.Join(dir, "script")
		Expect(os.WriteFile(script, []byte(fmt.Sprintf("write %s small\nwrite %s huge\nsif huge size 1099511627776\n", src, src)), 0o600)).To(Succeed())
		sh("debugfs", "-w", "-f", script, img)

		fi, err := os.Stat(img)
		Expect(err).To(Succeed())
		Expect(fi.Size()).To(BeEquivalentTo(2 << 30))
		file, err := os.Open(img)
		Expect(err).To(Succeed())
		DeferCleanup(file.Close)

		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		start := time.Now()
		f, err := Open(file, fi.Size())
		Expect(err).To(Succeed())
		nodes, err := walkFS(f, walkLimits{depth: 8, nodes: 1000, dirEntries: 1000, fileBytes: 1 << 20})
		Expect(err).To(Succeed())
		took := time.Since(start)
		runtime.ReadMemStats(&after)
		GinkgoWriter.Printf("open and walk of the 2 GiB image: %v, %d bytes allocated\n", took, after.TotalAlloc-before.TotalAlloc)
		Expect(took).To(BeNumerically("<", time.Second))
		Expect(after.TotalAlloc - before.TotalAlloc).To(BeNumerically("<", 4<<20))
		Expect(nodes["/small"].In.Size).To(BeEquivalentTo(10))
		huge := nodes["/huge"]
		Expect(huge).NotTo(BeNil())
		Expect(huge.In.Size).To(BeEquivalentTo(int64(1) << 40))

		// Streaming the 1 TiB file: it reads the first block, then zeros.
		// Take 512 MiB and check time and memory. The bytes after the first
		// block are zeros, and no allocation depends on the size.
		fl, err := f.Open(huge.Ino)
		Expect(err).To(Succeed())
		runtime.GC()
		runtime.ReadMemStats(&before)
		start = time.Now()
		n, err := io.CopyN(io.Discard, fl.SectionReader(), 512<<20)
		Expect(err).To(Succeed())
		Expect(n).To(BeEquivalentTo(512 << 20))
		runtime.ReadMemStats(&after)
		GinkgoWriter.Printf("copy of 512 MiB of a 1 TiB file: %v, %d bytes allocated\n", time.Since(start), after.TotalAlloc-before.TotalAlloc)
		Expect(after.TotalAlloc - before.TotalAlloc).To(BeNumerically("<", 4<<20))

		// Random access far from the start.
		p := make([]byte, 4096)
		for i := range p {
			p[i] = 0xAA
		}
		m, err := fl.ReadAt(p, int64(1)<<39)
		Expect(err).To(Succeed())
		Expect(m).To(Equal(4096))
		for _, b := range p {
			Expect(b).To(BeZero())
		}
		m, err = fl.ReadAt(p, int64(1)<<40-100)
		Expect(m).To(Equal(100))
		Expect(err).To(Equal(io.EOF))
		m, err = fl.ReadAt(p, int64(1)<<40)
		Expect(m).To(BeZero())
		Expect(err).To(Equal(io.EOF))
	})
})
