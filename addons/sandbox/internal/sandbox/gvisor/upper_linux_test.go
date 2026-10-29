//go:build linux

package gvisor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// bigLimits are limits that no test layer reaches.
var bigLimits = readLimits{stage: 1 << 40}

func write(p, body string) {
	Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
	Expect(os.WriteFile(p, []byte(body), 0o644)).To(Succeed())
}

// summary lists "path kind" for the entries, sorted, with the content of
// files read from their Open.
func summary(src stagedSource) []string {
	var out []string
	err := src.Walk(context.Background(), func(e workspace.UpperEntry) error {
		line := e.Path + " " + map[workspace.EntryKind]string{
			workspace.EntryFile: "file", workspace.EntryDir: "dir", workspace.EntryWhiteout: "whiteout", workspace.EntrySymlink: "symlink",
		}[e.Kind]
		if e.Kind == workspace.EntryFile {
			r, err := e.Open()
			if err != nil {
				return err
			}
			b, _ := io.ReadAll(r)
			r.Close()
			line += " " + string(b)
		}
		out = append(out, line)
		return nil
	})
	Expect(err).ToNot(HaveOccurred())
	sort.Strings(out)
	return out
}

var _ = Describe("upper layer", func() {
	It("sends only what changed", func() {
		ctx := context.Background()
		root, stage := filepath.Join(GinkgoT().TempDir(), "data"), GinkgoT().TempDir()
		write(filepath.Join(root, "a.txt"), "a1")
		write(filepath.Join(root, "d", "b.txt"), "b1")

		src, next, err := readUpper(ctx, root, stage, sandbox.UpperIndex{}, bigLimits)
		Expect(err).ToNot(HaveOccurred())
		Expect(strings.Join(summary(src), "|")).To(Equal("a.txt file a1|d dir|d/b.txt file b1"), "first read")
		// The staged copies do not follow later writes to the live files.
		write(filepath.Join(root, "a.txt"), "a2-longer")
		Expect(strings.Join(summary(src), "|")).To(ContainSubstring("a.txt file a1"), "staged content changed")

		// Nothing changed since the index was made: only the folder is sent again
		// (a folder has no key). Then the edit of a.txt is sent, the rest is not.
		src, next2, err := readUpper(ctx, root, GinkgoT().TempDir(), next, bigLimits)
		Expect(err).ToNot(HaveOccurred())
		Expect(strings.Join(summary(src), "|")).To(Equal("a.txt file a2-longer|d dir"), "second read")
		// A file that an earlier checkpoint committed and the guest deleted
		// since: overlayfs leaves no whiteout for it, the index does.
		Expect(os.Remove(filepath.Join(root, "d", "b.txt"))).To(Succeed())
		src, _, err = readUpper(ctx, root, GinkgoT().TempDir(), next2, bigLimits)
		Expect(err).ToNot(HaveOccurred())
		Expect(strings.Join(summary(src), "|")).To(Equal("d dir|d/b.txt whiteout"), "third read")
	})

	It("reads a missing folder as empty", func() {
		src, next, err := readUpper(context.Background(), filepath.Join(GinkgoT().TempDir(), "none"), GinkgoT().TempDir(), sandbox.UpperIndex{}, bigLimits)
		Expect(err).ToNot(HaveOccurred())
		Expect(src).To(BeEmpty())
		Expect(next).To(BeEmpty())
	})

	It("changes the stat key with the file", func() {
		p := filepath.Join(GinkgoT().TempDir(), "f")
		write(p, "one")
		st1, _ := os.Lstat(p)
		k1 := statKey(st1)
		Expect(k1).ToNot(BeEmpty(), "empty key")
		// The same content with a different modification time is a different key,
		// whatever the clock granularity is.
		Expect(os.Chtimes(p, time.Unix(1_000_000_000, 0), time.Unix(1_000_000_000, 0))).To(Succeed())
		st2, _ := os.Lstat(p)
		k2 := statKey(st2)
		Expect(k2).ToNot(Equal(k1), "key did not change with the mtime")
		// A different size is a different key.
		write(p, "three")
		st3, _ := os.Lstat(p)
		k3 := statKey(st3)
		Expect(k3).ToNot(Equal(k2), "key did not change with the size")
		Expect(k3).ToNot(Equal(k1), "key did not change with the size")
		// An unchanged file keeps its key.
		st4, _ := os.Lstat(p)
		Expect(statKey(st4)).To(Equal(statKey(st3)), "key of an unchanged file changed")
	})

	It("counts blocks in dirUsage", func() {
		dir := GinkgoT().TempDir()
		write(filepath.Join(dir, "a"), strings.Repeat("x", 100000))
		n, capped := dirUsage(dir, 100)
		Expect(n).To(BeNumerically(">=", 100000))
		Expect(capped).To(BeFalse())
		n, capped = dirUsage(filepath.Join(dir, "missing"), 100)
		Expect(n).To(BeZero(), "usage of a missing folder")
		Expect(capped).To(BeFalse())
	})

	It("stops dirUsage at the entry cap", func() {
		dir := GinkgoT().TempDir()
		for _, n := range []string{"a", "b", "c", "d"} {
			write(filepath.Join(dir, n), "")
		}
		// The root and four files are five entries.
		_, capped := dirUsage(dir, 5)
		Expect(capped).To(BeFalse(), "five entries are within a cap of five")
		_, capped = dirUsage(dir, 4)
		Expect(capped).To(BeTrue(), "five entries are over a cap of four")
	})

	It("sets the disk flag at the entry cap and drops stale readings", func() {
		dir := GinkgoT().TempDir()
		s := &sbx{dir: dir}
		upper := filepath.Join(dir, "upper")
		write(filepath.Join(upper, "a"), "")
		in := newInst(1)
		s.measureDisk(in, 1<<30)
		Expect(s.diskFull.Load()).To(BeFalse(), "a small layer is full")
		s.measureDisk(in, -1) // over the limit
		Expect(s.diskFull.Load()).To(BeTrue(), "a layer over the limit is not full")
		// A refresh stops the old instance and clears the flag. A reading of the
		// old instance that finishes later must not set it again.
		s.diskFull.Store(false)
		close(in.quit)
		s.measureDisk(in, -1)
		Expect(s.diskFull.Load()).To(BeFalse(), "a reading of a stopped instance set the flag")
	})

	It("uses the upper caps of chv", func() {
		Expect(maxUpperDepth).To(BeEquivalentTo(512))
		Expect(maxUpperEntries).To(BeEquivalentTo(1000000))
		got := (readLimits{}).withDefaults()
		Expect(got.depth).To(BeEquivalentTo(512), "defaults: %+v", got)
		Expect(got.entries).To(BeEquivalentTo(1000000), "defaults: %+v", got)
	})

	It("fails over the entry cap and names it", func() {
		root := filepath.Join(GinkgoT().TempDir(), "data")
		for _, n := range []string{"a", "b", "c", "d"} {
			write(filepath.Join(root, n), "x")
		}
		lim := readLimits{entries: 4, stage: 1 << 20}
		_, _, err := readUpper(context.Background(), root, GinkgoT().TempDir(), sandbox.UpperIndex{}, lim)
		Expect(err).ToNot(HaveOccurred(), "four entries within a cap of four")
		stage := GinkgoT().TempDir()
		lim.entries = 3
		_, _, err = readUpper(context.Background(), root, stage, sandbox.UpperIndex{}, lim)
		Expect(err).To(MatchError(ContainSubstring("more than 3 entries")))
		ents, _ := os.ReadDir(stage)
		Expect(ents).To(BeEmpty(), "a failed read staged files")
	})

	It("fails over the depth cap and names it", func() {
		root := filepath.Join(GinkgoT().TempDir(), "data")
		write(filepath.Join(root, "a", "b", "c", "f"), "x")
		lim := readLimits{depth: 4, stage: 1 << 20}
		_, _, err := readUpper(context.Background(), root, GinkgoT().TempDir(), sandbox.UpperIndex{}, lim)
		Expect(err).ToNot(HaveOccurred(), "four levels within a cap of four")
		lim.depth = 3
		_, _, err = readUpper(context.Background(), root, GinkgoT().TempDir(), sandbox.UpperIndex{}, lim)
		Expect(err).To(MatchError(ContainSubstring("deeper than 3 levels")))
	})

	It("fails over the default depth cap", func() {
		root := filepath.Join(GinkgoT().TempDir(), "data")
		deep := root
		for range maxUpperDepth + 1 {
			deep = filepath.Join(deep, "d")
		}
		if err := os.MkdirAll(deep, 0o755); err != nil {
			Skip(fmt.Sprintf("cannot make %d levels: %v", maxUpperDepth+1, err))
		}
		_, _, err := readUpper(context.Background(), root, GinkgoT().TempDir(), sandbox.UpperIndex{}, readLimits{stage: 1 << 20})
		Expect(err).To(MatchError(ContainSubstring("deeper than 512 levels")))
	})

	It("fails over the stage limit", func() {
		root := filepath.Join(GinkgoT().TempDir(), "data")
		write(filepath.Join(root, "a"), strings.Repeat("x", 600))
		write(filepath.Join(root, "b"), strings.Repeat("y", 600))
		stage := GinkgoT().TempDir()
		_, _, err := readUpper(context.Background(), root, stage, sandbox.UpperIndex{}, readLimits{stage: 1000})
		Expect(err).To(MatchError(ContainSubstring("stage limit of 1000 bytes")))
		ents, _ := os.ReadDir(stage)
		Expect(ents).To(BeEmpty(), "a failed read staged files")
		_, _, err = readUpper(context.Background(), root, GinkgoT().TempDir(), sandbox.UpperIndex{}, readLimits{stage: 1200})
		Expect(err).ToNot(HaveOccurred(), "exactly the limit")
		// Files that are not sent (unchanged) do not count.
		_, next, err := readUpper(context.Background(), root, GinkgoT().TempDir(), sandbox.UpperIndex{}, readLimits{stage: 1200})
		Expect(err).ToNot(HaveOccurred())
		_, _, err = readUpper(context.Background(), root, GinkgoT().TempDir(), next, readLimits{stage: 1})
		Expect(err).ToNot(HaveOccurred(), "unchanged files counted against the limit")
	})
})
