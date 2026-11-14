package ext4ro

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The golden images in testdata are made by testdata/gen.sh. These specs
// need no e2fsprogs: they check the reader against the tree that the script
// builds, written out here by hand.

func readAll(f *FS, ino uint64) []byte {
	GinkgoHelper()
	fl, err := f.Open(ino)
	Expect(err).To(Succeed())
	b, err := io.ReadAll(fl.SectionReader())
	Expect(err).To(Succeed())
	return b
}

func goldenSparse() []byte {
	b := make([]byte, 20000)
	b[0], b[15000] = 'A', 'Z'
	return b
}

var _ = Describe("golden images", func() {
	for _, name := range []string{"golden-ext4", "golden-ext2-1k", "golden-inline"} {
		It("reads the tree of "+name, func() {
			f := mustOpen(loadGolden(name + ".img.gz"))
			nodes, err := walkFS(f, defaultWalk)
			Expect(err).To(Succeed())
			get := func(p string) *node {
				GinkgoHelper()
				n := nodes[p]
				Expect(n).NotTo(BeNil(), "%s has no %q", name, p)
				return n
			}
			content := func(p string) string { return string(readAll(f, get(p).Ino)) }

			Expect(content("/a.txt")).To(Equal("hello\n"))
			Expect(content("/dir/b.txt")).To(Equal("bee\n"))
			Expect(content("/dir/sub/c.txt")).To(Equal(strings.Repeat("0123456789", 10)))
			Expect(content("/big")).To(Equal(strings.Repeat("abcdefghi\n", 4000)))
			want := goldenSparse()
			if name == "golden-inline" {
				// With inline_data, mke2fs -d drops the hole after the
				// last data block (the same defect as in the tree specs).
				want = want[:15360]
			}
			Expect(bytes.Equal(readAll(f, get("/sparse").Ino), want)).To(BeTrue(), "content of /sparse")
			Expect(content("/" + strings.Repeat("n", 255))).To(Equal("long\n"))
			Expect(get("/link").Target).To(Equal("a.txt"))
			Expect(get("/dir/longlink").Target).To(Equal(strings.Repeat("x", 70)))
			Expect(get("/hard").Ino).To(Equal(get("/a.txt").Ino))
			Expect(get("/a.txt").In.Links).To(BeEquivalentTo(2))
			Expect(get("/empty").In.IsDir()).To(BeTrue())
			Expect(get("/empty").Kids).To(BeEmpty())
			Expect(get("/w").In.IsWhiteout()).To(BeTrue())
			Expect(get("/opaque").Xattrs).To(HaveKeyWithValue("trusted.overlay.opaque", "79"))
			Expect(get("/a.txt").Xattrs).To(HaveKeyWithValue("user.x", "68656c6c6f"))
			Expect(get("/a.txt").Xattrs).To(HaveKeyWithValue("security.capability", "01000002ffffffff0000000000000000"+"00000000"))
			Expect(get("/big").In.Size).To(BeEquivalentTo(40000))
			Expect(get("/a.txt").In.MtimeSec).To(BeEquivalentTo(1700000000))

			ext := 0
			inl := 0
			for _, n := range nodes {
				if n.In.Flags&flagExtents != 0 {
					ext++
				}
				if n.In.Flags&flagInlineData != 0 {
					inl++
				}
			}
			switch name {
			case "golden-ext2-1k":
				Expect(ext).To(BeZero(), "block map image")
				Expect(f.BlockSize()).To(Equal(1024))
			case "golden-ext4":
				Expect(ext).To(BeNumerically(">", 3))
				Expect(f.BlockSize()).To(Equal(4096))
			case "golden-inline":
				Expect(inl).To(BeNumerically(">", 3), "inline inodes")
			}
		})
	}

	It("lists an htree directory", func() {
		f := mustOpen(loadGolden("golden-htree.img.gz"))
		nodes, err := walkFS(f, defaultWalk)
		Expect(err).To(Succeed())
		many := nodes["/many"]
		Expect(many).NotTo(BeNil())
		Expect(many.In.Flags&0x1000).NotTo(BeZero(), "the directory is indexed")
		Expect(many.In.Size).To(BeNumerically(">", 4096), "the directory has many blocks")
		Expect(many.Kids).To(HaveLen(600))
		seen := map[string]bool{}
		for _, k := range many.Kids {
			Expect(seen[k.Name]).To(BeFalse(), "duplicate %q", k.Name)
			seen[k.Name] = true
		}
		for i := range 600 {
			Expect(seen[fmt.Sprintf("entry-%04d-padding-name", i)]).To(BeTrue(), "entry %d", i)
		}
		// A small cap fails, a cap that fits does not.
		_, err = f.ReadDir(many.Ino, 599)
		Expect(err).To(MatchError(ErrLimit))
		kids, err := f.ReadDir(many.Ino, 600)
		Expect(err).To(Succeed())
		Expect(kids).To(HaveLen(600))
	})

	It("reads the root of a golden image", func() {
		f := mustOpen(loadGolden("golden-ext4.img.gz"))
		in, err := f.Stat(2)
		Expect(err).To(Succeed())
		Expect(in.IsDir()).To(BeTrue())
		Expect(f.InodeInRange(2)).To(BeTrue())
		Expect(f.InodeInRange(1)).To(BeFalse())
		Expect(f.InodeInRange(0)).To(BeFalse())
		Expect(f.InodeInRange(f.FirstIno() - 1)).To(BeFalse())
		Expect(f.InodeInRange(f.FirstIno())).To(BeTrue())
		Expect(f.InodeInRange(f.InodeCount())).To(BeTrue())
		Expect(f.InodeInRange(f.InodeCount() + 1)).To(BeFalse())
		Expect(f.FirstIno()).To(BeEquivalentTo(11))
	})
})
