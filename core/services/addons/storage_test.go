// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// countingDir wraps a real directory and records what the scan asked for and
// was given, so a test can bound the work, not only the answer.
type countingDir struct {
	dirFile
	reads *dirReads
}

type dirReads struct {
	opens, calls, entries, largest int
	badBatch                       bool
}

func (c countingDir) ReadDir(n int) ([]fs.DirEntry, error) {
	c.reads.calls++
	if n <= 0 {
		// A read of the whole directory is what the bound exists to avoid.
		c.reads.badBatch = true
	}
	c.reads.largest = max(c.reads.largest, n)
	out, e := c.dirFile.ReadDir(n)
	c.reads.entries += len(out)
	return out, e
}

func counting(reads *dirReads) func(string) (dirFile, error) {
	return func(p string) (dirFile, error) {
		f, e := os.Open(p)
		if e != nil {
			return nil, e
		}
		reads.opens++
		return countingDir{dirFile: f, reads: reads}, nil
	}
}

func fill(dir string, n int) {
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	for i := 0; i < n; i++ {
		Expect(os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)), []byte("x"), 0o600)).To(Succeed())
	}
}

var _ = Describe("data directory size", func() {
	It("reads a flat directory in batches and stops at the entry bound", func() {
		dir := GinkgoT().TempDir()
		fill(dir, 2000)
		reads := &dirReads{}
		s := &storageScanner{open: counting(reads), limit: 100, batch: 16}
		out := s.size(context.Background(), dir)
		Expect(out.Truncated).To(BeTrue())
		Expect(out.Bytes).To(BeNumerically("<=", 100))
		Expect(out.Bytes).To(BeNumerically(">", 0))
		Expect(reads.badBatch).To(BeFalse(), "never reads a whole directory at once")
		Expect(reads.largest).To(Equal(16))
		Expect(reads.entries).To(BeNumerically("<=", 100+16), "entries read are bounded by the limit plus one batch")
	})

	It("adds up nested files and reports a complete walk as not truncated", func() {
		dir := GinkgoT().TempDir()
		fill(filepath.Join(dir, "a", "b"), 3)
		fill(dir, 2)
		s := &storageScanner{limit: 100, batch: 2}
		out := s.size(context.Background(), dir)
		Expect(out.Bytes).To(Equal(int64(5)))
		Expect(out.Truncated).To(BeFalse())
		Expect(s.size(context.Background(), filepath.Join(dir, "missing")).Bytes).To(BeZero())
	})

	It("never follows or counts a symbolic link", func() {
		outside := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(outside, "big"), make([]byte, 4096), 0o600)).To(Succeed())
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "own"), []byte("abc"), 0o600)).To(Succeed())
		Expect(os.Symlink(outside, filepath.Join(dir, "dirlink"))).To(Succeed())
		Expect(os.Symlink(filepath.Join(outside, "big"), filepath.Join(dir, "filelink"))).To(Succeed())
		Expect(os.Symlink(dir, filepath.Join(dir, "loop"))).To(Succeed())
		out := (&storageScanner{}).size(context.Background(), dir)
		Expect(out.Bytes).To(Equal(int64(3)))
		Expect(out.Truncated).To(BeFalse())
	})

	It("keeps a result for a short time so polling does not walk again", func() {
		dir := GinkgoT().TempDir()
		fill(dir, 3)
		reads := &dirReads{}
		now := time.Unix(1000, 0)
		s := &storageScanner{open: counting(reads), now: func() time.Time { return now }, keep: 10 * time.Second}
		first := s.size(context.Background(), dir)
		Expect(first.Bytes).To(Equal(int64(3)))
		opened := reads.opens
		Expect(opened).To(BeNumerically(">", 0))
		fill(dir+"/more", 2)
		now = now.Add(5 * time.Second)
		Expect(s.size(context.Background(), dir)).To(Equal(first))
		Expect(reads.opens).To(Equal(opened), "a poll inside the window reads nothing")
		now = now.Add(6 * time.Second)
		Expect(s.size(context.Background(), dir).Bytes).To(Equal(int64(5)))
		Expect(reads.opens).To(BeNumerically(">", opened))
	})

	It("stops at the time bound and reports a truncated size", func() {
		dir := GinkgoT().TempDir()
		fill(dir, 50)
		now := time.Unix(0, 0)
		// Every look at the clock costs a second, so the budget runs out.
		s := &storageScanner{batch: 4, budget: 3 * time.Second, now: func() time.Time { now = now.Add(time.Second); return now }}
		out := s.size(context.Background(), dir)
		Expect(out.Truncated).To(BeTrue())
		Expect(out.Bytes).To(BeNumerically("<", 50))
	})

	It("does not keep a result cut short by the caller", func() {
		dir := GinkgoT().TempDir()
		fill(dir, 5)
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		s := &storageScanner{}
		_ = s.size(cancelled, dir)
		Expect(s.size(context.Background(), dir).Bytes).To(Equal(int64(5)))
	})

	It("is what the installed view reports for the data directory", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, actor, command("install", "install"))
		Expect(e).NotTo(HaveOccurred())
		dir := m.journal.Addons["sample"].DataDirectory
		Expect(dir).NotTo(BeEmpty())
		fill(dir, 7)
		reads := &dirReads{}
		m.scanner = storageScanner{open: counting(reads), limit: 4, batch: 2}
		list, e := m.Installed(ctx, actor)
		Expect(e).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Storage.Truncated).To(BeTrue())
		Expect(reads.entries).To(BeNumerically("<=", 4+2))
	})
})
