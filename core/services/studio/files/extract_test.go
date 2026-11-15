package files_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mholt/archives"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

type entry struct {
	name string
	body string
	kind byte // 0 file, 'd' dir, 's' symlink, 'h' hard link, 'c' device
}

func zipOf(es ...entry) []byte {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, e := range es {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		switch e.kind {
		case 'd':
			h.Name = strings.TrimSuffix(e.name, "/") + "/"
			h.SetMode(0o755 | 1<<31)
		case 's':
			h.SetMode(0o777 | 1<<27) // fs.ModeSymlink
		}
		fw, err := w.CreateHeader(h)
		Expect(err).NotTo(HaveOccurred())
		_, _ = fw.Write([]byte(e.body))
	}
	Expect(w.Close()).To(Succeed())
	return b.Bytes()
}

func tarOf(es ...entry) []byte {
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, e := range es {
		h := &tar.Header{Name: e.name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(e.body)), Format: tar.FormatGNU}
		switch e.kind {
		case 'd':
			h.Typeflag, h.Size, h.Mode = tar.TypeDir, 0, 0o755
		case 's':
			h.Typeflag, h.Size, h.Linkname = tar.TypeSymlink, 0, "/etc/passwd"
		case 'h':
			h.Typeflag, h.Size, h.Linkname = tar.TypeLink, 0, "a.txt"
		case 'c':
			h.Typeflag, h.Size = tar.TypeChar, 0
			h.Devmajor, h.Devminor = 1, 3
		}
		Expect(w.WriteHeader(h)).To(Succeed())
		if h.Size > 0 {
			_, _ = w.Write([]byte(e.body))
		}
	}
	Expect(w.Close()).To(Succeed())
	return b.Bytes()
}

func compress(c archives.Compressor, raw []byte) []byte {
	var b bytes.Buffer
	w, err := c.OpenWriter(&b)
	Expect(err).NotTo(HaveOccurred())
	_, err = w.Write(raw)
	Expect(err).NotTo(HaveOccurred())
	Expect(w.Close()).To(Succeed())
	return b.Bytes()
}

// rarStored builds a RAR5 archive that stores its files without compression.
func rarStored(names []string, bodies []string) []byte {
	vint := func(n uint64) []byte {
		var o []byte
		for n >= 0x80 {
			o = append(o, byte(n)|0x80)
			n >>= 7
		}
		return append(o, byte(n))
	}
	block := func(hdr []byte, data string) []byte {
		h := append(vint(uint64(len(hdr))), hdr...)
		var c [4]byte
		binary.LittleEndian.PutUint32(c[:], crc32.ChecksumIEEE(h))
		return append(append(c[:], h...), data...)
	}
	out := []byte{0x52, 0x61, 0x72, 0x21, 0x1a, 0x07, 0x01, 0x00}
	out = append(out, block(append(append(vint(1), vint(0)...), vint(0)...), "")...)
	for i, n := range names {
		var h []byte
		h = append(h, vint(2)...)                      // file header
		h = append(h, vint(2)...)                      // header flags: data area
		h = append(h, vint(uint64(len(bodies[i])))...) // data size
		h = append(h, vint(4)...)                      // file flags: CRC32 present
		h = append(h, vint(uint64(len(bodies[i])))...) // unpacked size
		h = append(h, vint(0o644)...)                  // attributes
		var c [4]byte
		binary.LittleEndian.PutUint32(c[:], crc32.ChecksumIEEE([]byte(bodies[i])))
		h = append(h, c[:]...)
		h = append(h, vint(0)...) // compression: version 0, stored
		h = append(h, vint(1)...) // host OS: unix
		h = append(h, vint(uint64(len(n)))...)
		h = append(h, n...)
		out = append(out, block(h, bodies[i])...)
	}
	return append(out, block(append(append(vint(5), vint(0)...), vint(0)...), "")...)
}

var _ = Describe("Extract here", func() {
	ctx := context.Background()
	var (
		f   *files.Service
		act workspace.Actor
	)
	BeforeEach(func() {
		f = newFiles()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		act = f.Actor("alice")
	})
	put := func(rel string, data []byte) {
		abs, err := f.Abs("alice", rel)
		Expect(err).NotTo(HaveOccurred())
		_, err = f.Store().Put(ctx, act, abs, bytes.NewReader(data), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	read := func(rel string) (string, bool) {
		abs, _ := f.Abs("alice", rel)
		r, _, err := f.Store().Open(ctx, act, abs)
		if err != nil {
			return "", false
		}
		defer func() { _ = r.Close() }()
		b, _ := io.ReadAll(r)
		return string(b), true
	}
	list := func(rel string) []string {
		abs, _ := f.Abs("alice", rel)
		kids, err := f.Store().List(ctx, act, abs)
		if err != nil {
			return nil
		}
		var out []string
		for _, k := range kids {
			out = append(out, k.Name)
		}
		return out
	}
	tree := []entry{{"docs/", "", 'd'}, {"docs/a.txt", "alpha", 0}, {"docs/sub/b.txt", "beta", 0}, {"c.txt", "gamma", 0}}
	expectTree := func(root string) {
		for p, want := range map[string]string{"docs/a.txt": "alpha", "docs/sub/b.txt": "beta", "c.txt": "gamma"} {
			got, ok := read(root + "/" + p)
			Expect(ok).To(BeTrue(), "%s/%s missing", root, p)
			Expect(got).To(Equal(want))
		}
	}

	Describe("the formats", func() {
		raw := tarOf(tree...)
		cases := map[string][]byte{
			"t.zip":     zipOf(tree...),
			"t.tar":     raw,
			"t.tar.gz":  compress(archives.Gz{}, raw),
			"t.tgz":     compress(archives.Gz{}, raw),
			"t.tar.bz2": mustRead("testdata/tree.tar.bz2"),
			"t.tar.xz":  compress(archives.Xz{}, raw),
			"t.tar.zst": compress(archives.Zstd{}, raw),
			"t.tar.br":  compress(archives.Brotli{}, raw),
			"t.tar.lz4": compress(archives.Lz4{}, raw),
		}
		// bzip2 has no writer in the libraries, so that case is a fixture made
		// with the system tar.
		for name, data := range cases {
			name, data := name, data
			It("extracts "+name+" into a folder named after it", func() {
				put("/"+name, data)
				res, err := f.Extract(ctx, "alice", "/"+name, "", files.ExtractLimits{})
				Expect(err).NotTo(HaveOccurred())
				want := files.ExtractFolderName(name)
				Expect(res.Dest).To(Equal("/" + want))
				Expect(res.Files).To(Equal(3))
				Expect(res.Bytes).To(Equal(int64(len("alpha") + len("beta") + len("gamma"))))
				expectTree("/" + want)
			})
		}
		It("extracts a 7z archive when 7z is allowed, and refuses it by default", func() {
			data, err := os.ReadFile("testdata/small.7z")
			Expect(err).NotTo(HaveOccurred())
			put("/small.7z", data)
			_, err = f.Extract(ctx, "alice", "/small.7z", "", files.ExtractLimits{})
			Expect(err).To(MatchError(files.ErrArchiveUnsupported))
			res, err := f.Extract(ctx, "alice", "/small.7z", "", files.ExtractLimits{SevenZipRar: true})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Files).To(Equal(10))
			got, ok := read("/small/01")
			Expect(ok).To(BeTrue())
			Expect(got).To(HaveLen(3572))
		})
		It("extracts a RAR archive when RAR is allowed", func() {
			put("/r.rar", rarStored([]string{"x/one.txt", "two.txt"}, []string{"11", "222"}))
			_, err := f.Extract(ctx, "alice", "/r.rar", "", files.ExtractLimits{})
			Expect(err).To(MatchError(files.ErrArchiveUnsupported))
			res, err := f.Extract(ctx, "alice", "/r.rar", "", files.ExtractLimits{SevenZipRar: true})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Files).To(Equal(2))
			got, _ := read("/r/x/one.txt")
			Expect(got).To(Equal("11"))
		})
		It("refuses a plain compressed file and a file that is no archive", func() {
			put("/a.txt.gz", compress(archives.Gz{}, []byte("hello")))
			_, err := f.Extract(ctx, "alice", "/a.txt.gz", "", files.ExtractLimits{})
			Expect(err).To(MatchError(files.ErrArchiveUnsupported))
			put("/n.zip", []byte("not an archive at all"))
			_, err = f.Extract(ctx, "alice", "/n.zip", "", files.ExtractLimits{})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("the destination", func() {
		It("uses name 2 when the default folder exists, and honours a given folder", func() {
			put("/report.tar.gz", compress(archives.Gz{}, tarOf(tree...)))
			r1, err := f.Extract(ctx, "alice", "/report.tar.gz", "", files.ExtractLimits{})
			Expect(err).NotTo(HaveOccurred())
			r2, err := f.Extract(ctx, "alice", "/report.tar.gz", "", files.ExtractLimits{})
			Expect(err).NotTo(HaveOccurred())
			Expect(r1.Dest).To(Equal("/report"))
			Expect(r2.Dest).To(Equal("/report 2"))
			r3, err := f.Extract(ctx, "alice", "/report.tar.gz", "/out/here", files.ExtractLimits{})
			Expect(err).NotTo(HaveOccurred())
			Expect(r3.Dest).To(Equal("/out/here"))
			expectTree("/out/here")
		})
		It("refuses a managed folder and a file in the way", func() {
			put("/t.zip", zipOf(tree...))
			_, err := f.Extract(ctx, "alice", "/t.zip", "/Chats/x", files.ExtractLimits{})
			Expect(err).To(HaveOccurred())
			put("/blocker", []byte("x"))
			_, err = f.Extract(ctx, "alice", "/t.zip", "/blocker", files.ExtractLimits{})
			Expect(err).To(MatchError(workspace.ErrExists))
		})
		It("skips entries that would land in a managed folder", func() {
			put("/t.zip", zipOf(entry{"Memory/n.md", "x", 0}, entry{"ok.txt", "y", 0}))
			res, err := f.Extract(ctx, "alice", "/t.zip", "/", files.ExtractLimits{})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Skipped).To(ContainElement(files.ExtractSkip{Path: "Memory/n.md", Reason: files.SkipManaged}))
			_, ok := read("/ok.txt")
			Expect(ok).To(BeTrue())
		})
	})

	Describe("hostile archives", func() {
		refused := func(name string, data []byte, lim files.ExtractLimits, want error) {
			put("/"+name, data)
			_, err := f.Extract(ctx, "alice", "/"+name, "", lim)
			ExpectWithOffset(1, err).To(MatchError(want))
			stem := files.ExtractFolderName(name)
			ExpectWithOffset(1, list("/")).NotTo(ContainElement(stem), "a refused archive left a folder")
		}
		It("refuses zip slip, absolute paths and NUL, writing nothing", func() {
			refused("slip.zip", zipOf(entry{"ok.txt", "x", 0}, entry{"../../evil.txt", "x", 0}), files.ExtractLimits{}, files.ErrArchiveBad)
			refused("abs.zip", zipOf(entry{"/etc/evil", "x", 0}), files.ExtractLimits{}, files.ErrArchiveBad)
			refused("dots.tar", tarOf(entry{"a/../../b", "x", 0}), files.ExtractLimits{}, files.ErrArchiveBad)
			refused("nul.zip", zipOf(entry{"a\x00b", "x", 0}), files.ExtractLimits{}, files.ErrArchiveBad)
			refused("win.zip", zipOf(entry{"a\\..\\..\\b", "x", 0}), files.ExtractLimits{}, files.ErrArchiveBad)
		})
		It("skips symlinks, hard links and devices and reports them", func() {
			put("/l.tar", tarOf(entry{"a.txt", "a", 0}, entry{"s", "", 's'}, entry{"h", "", 'h'}, entry{"dev", "", 'c'}))
			res, err := f.Extract(ctx, "alice", "/l.tar", "", files.ExtractLimits{})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Files).To(Equal(1))
			Expect(res.Skipped).To(ConsistOf(
				files.ExtractSkip{Path: "s", Reason: files.SkipSymlink},
				files.ExtractSkip{Path: "h", Reason: files.SkipHardLink},
				files.ExtractSkip{Path: "dev", Reason: files.SkipSpecial}))
			Expect(list("/l")).To(Equal([]string{"a.txt"}))
			put("/z.zip", zipOf(entry{"a.txt", "a", 0}, entry{"s", "/etc/passwd", 's'}))
			res, err = f.Extract(ctx, "alice", "/z.zip", "", files.ExtractLimits{})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Skipped).To(ConsistOf(files.ExtractSkip{Path: "s", Reason: files.SkipSymlink}))
		})
		It("refuses more entries than the cap, for zip before the directory is read and for tar", func() {
			var es []entry
			for i := 0; i < 50; i++ {
				es = append(es, entry{name: "f" + string(rune('a'+i%26)) + string(rune('a'+i/26)), body: "x"})
			}
			refused("many.zip", zipOf(es...), files.ExtractLimits{MaxEntries: 10}, files.ErrArchiveLimit)
			refused("many.tar", tarOf(es...), files.ExtractLimits{MaxEntries: 10}, files.ErrArchiveLimit)
		})
		It("refuses a zip bomb by the total bytes, with a small heap", func() {
			var raw bytes.Buffer
			fw, _ := flate.NewWriter(&raw, flate.BestCompression)
			zero := make([]byte, 1<<20)
			for i := 0; i < 200; i++ {
				_, _ = fw.Write(zero)
			}
			_ = fw.Close()
			var b bytes.Buffer
			zw := zip.NewWriter(&b)
			w, _ := zw.CreateRaw(&zip.FileHeader{Name: "zeros", Method: zip.Deflate, CRC32: 0, CompressedSize64: uint64(raw.Len()), UncompressedSize64: 200 << 20})
			_, _ = w.Write(raw.Bytes())
			_ = zw.Close()
			put("/bomb.zip", b.Bytes())
			peak := heapPeak(func() {
				_, err := f.Extract(ctx, "alice", "/bomb.zip", "", files.ExtractLimits{MaxBytes: 8 << 20})
				Expect(err).To(MatchError(files.ErrArchiveLimit))
			})
			Expect(peak).To(BeNumerically("<", 150<<20), "the bomb used too much memory")
			Expect(list("/")).NotTo(ContainElement("bomb"))
		})
		It("refuses a high ratio even under the byte cap", func() {
			raw := make([]byte, 100<<20)
			refused("ratio.tar.gz", compress(archives.Gz{}, tarOf(entry{"zeros", string(raw), 0})), files.ExtractLimits{MaxBytes: 1 << 30}, files.ErrArchiveLimit)
		})
		It("skips a file over the per-file cap and keeps the rest", func() {
			put("/big.zip", zipOf(entry{"big", strings.Repeat("x", 4096), 0}, entry{"small", "s", 0}))
			res, err := f.Extract(ctx, "alice", "/big.zip", "", files.ExtractLimits{MaxFileBytes: 1024})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Skipped).To(ConsistOf(files.ExtractSkip{Path: "big", Reason: files.SkipTooLarge}))
			Expect(list("/big")).To(Equal([]string{"small"}))
		})
		It("skips a name that is not UTF-8 and a duplicate name", func() {
			put("/n.zip", zipOf(entry{"bad\xff.txt", "x", 0}, entry{"ok.txt", "1", 0}, entry{"ok.txt", "2", 0}))
			res, err := f.Extract(ctx, "alice", "/n.zip", "", files.ExtractLimits{})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.SkippedCount).To(Equal(2))
			Expect(res.Skipped[0].Reason).To(Equal(files.SkipNotUTF8))
			Expect(res.Skipped[1].Reason).To(Equal(files.SkipDuplicate))
			Expect(list("/n")).To(Equal([]string{"ok.txt"}))
		})
		It("does not extract a nested archive", func() {
			put("/o.zip", zipOf(entry{"inner.zip", string(zipOf(entry{"deep.txt", "d", 0})), 0}))
			_, err := f.Extract(ctx, "alice", "/o.zip", "", files.ExtractLimits{})
			Expect(err).NotTo(HaveOccurred())
			Expect(list("/o")).To(Equal([]string{"inner.zip"}))
		})
		It("stops at the deadline and writes nothing", func() {
			put("/t.zip", zipOf(tree...))
			_, err := f.Extract(ctx, "alice", "/t.zip", "", files.ExtractLimits{Timeout: time.Nanosecond})
			Expect(err).To(MatchError(files.ErrArchiveTimeout))
			Expect(list("/")).NotTo(ContainElement("t"))
		})
		It("stops when the request is cancelled", func() {
			put("/t.zip", zipOf(tree...))
			c, cancel := context.WithCancel(ctx)
			cancel()
			_, err := f.Extract(c, "alice", "/t.zip", "", files.ExtractLimits{})
			Expect(err).To(MatchError(context.Canceled))
		})
	})

	Describe("names that are taken", func() {
		It("keeps the existing file and saves the new one as a copy", func() {
			put("/dst/a.txt", []byte("old"))
			put("/t.zip", zipOf(entry{"a.txt", "new", 0}, entry{"b.txt", "fresh", 0}))
			res, err := f.Extract(ctx, "alice", "/t.zip", "/dst", files.ExtractLimits{})
			Expect(err).NotTo(HaveOccurred())
			got, _ := read("/dst/a.txt")
			Expect(got).To(Equal("old"))
			Expect(res.Conflicts).To(HaveLen(1))
			Expect(res.Conflicts[0].Path).To(Equal("/dst/a.txt"))
			Expect(res.Conflicts[0].Kept).To(Equal("/dst/a (from alice).txt"))
			copyOf, ok := read(res.Conflicts[0].Kept)
			Expect(ok).To(BeTrue(), "no copy at %q", res.Conflicts[0].Kept)
			Expect(copyOf).To(Equal("new"))
			Expect(res.Files).To(Equal(2))
		})
		It("shows what the commit does at the latest revision: it overwrites (so the extract uses base 0)", func() {
			put("/dst2/a.txt", []byte("old"))
			home := session.HomePath("alice")
			view, err := f.Store().View(ctx, act, []workspace.MountRequest{{Path: home, At: "h", Mode: workspace.ModeWrite}})
			Expect(err).NotTo(HaveOccurred())
			hist, err := f.Store().History(ctx, act, workspace.HistoryQuery{Path: home, Limit: 1})
			Expect(err).NotTo(HaveOccurred())
			latest := hist[0].Rev
			src := workspace.FixtureSource{workspace.FixtureDir("h"), workspace.FixtureDir("h/dst2"), workspace.FixtureFile("h/dst2/a.txt", "new")}
			_, conflicts, err := f.Store().CommitUpper(ctx, view, act, latest, src, workspace.UpperOptions{Title: "x"})
			Expect(err).NotTo(HaveOccurred())
			Expect(conflicts).To(BeEmpty())
			got, _ := read("/dst2/a.txt")
			Expect(got).To(Equal("new"))
		})
		It("treats names that differ only by case as taken", func() {
			put("/t.zip", zipOf(entry{"A.txt", "1", 0}, entry{"a.txt", "2", 0}))
			res, err := f.Extract(ctx, "alice", "/t.zip", "", files.ExtractLimits{})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Conflicts).To(HaveLen(1))
			Expect(list("/t")).To(HaveLen(2))
		})
	})

	It("is one change set that Undo reverses whole", func() {
		put("/t.tar.gz", compress(archives.Gz{}, tarOf(tree...)))
		res, err := f.Extract(ctx, "alice", "/t.tar.gz", "", files.ExtractLimits{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Rev).To(BeNumerically(">", 0))
		expectTree("/t")
		_, _, err = f.Store().Undo(ctx, act, res.Rev, workspace.UndoOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(list("/")).NotTo(ContainElement("t"))
		_, ok := read("/t/c.txt")
		Expect(ok).To(BeFalse())
	})

	It("does not reach another user's files", func() {
		Expect(f.EnsureHome(ctx, "bob")).To(Succeed())
		put("/t.zip", zipOf(tree...))
		_, err := f.Extract(ctx, "bob", "/t.zip", "", files.ExtractLimits{})
		Expect(err).To(MatchError(workspace.ErrNotFound))
	})
})

func mustRead(p string) []byte {
	b, err := os.ReadFile(p)
	Expect(err).NotTo(HaveOccurred())
	return b
}

// heapPeak runs fn and returns the highest heap in use that a sampler saw.
func heapPeak(fn func()) uint64 {
	var peak atomic.Uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			if m.HeapInuse > peak.Load() {
				peak.Store(m.HeapInuse)
			}
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	fn()
	close(stop)
	<-done
	return peak.Load()
}
