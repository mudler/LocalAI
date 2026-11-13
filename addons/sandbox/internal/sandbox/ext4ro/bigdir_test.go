package ext4ro

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// bigDirTree makes a folder of n entries with names of different lengths.
func bigDirTree(n int) func(string) {
	return func(dir string) {
		GinkgoHelper()
		d := filepath.Join(dir, "many")
		Expect(os.MkdirAll(d, 0o755)).To(Succeed())
		for i := range n {
			name := fmt.Sprintf("file-%07d", i)
			if i%7 == 0 {
				name += strings.Repeat("x", i%90)
			}
			f, err := os.Create(filepath.Join(d, name))
			Expect(err).To(Succeed())
			Expect(f.Close()).To(Succeed())
		}
	}
}

func checkBigDir(img string, n int, wantLevels int) {
	GinkgoHelper()
	f := mustOpen(readImage(img))
	root, err := f.ReadDir(rootIno, 100)
	Expect(err).To(Succeed())
	var many uint64
	for _, k := range root {
		if k.Name == "many" {
			many = k.Ino
		}
	}
	Expect(many).NotTo(BeZero())
	in, err := f.Stat(many)
	Expect(err).To(Succeed())
	Expect(in.Flags&0x1000).NotTo(BeZero(), "the directory is an htree")
	// The tool is the truth for the levels of the index.
	out := debugfsBatch(img, []string{"htree_dump /many"})[0]
	Expect(out).To(ContainSubstring(fmt.Sprintf("Indirect levels: %d", wantLevels)))

	kids, err := f.ReadDir(many, n)
	Expect(err).To(Succeed())
	Expect(kids).To(HaveLen(n))
	got := make([]string, len(kids))
	for i, k := range kids {
		got[i] = k.Name
		Expect(k.FileType).To(BeEquivalentTo(1), "regular file type of %q", k.Name)
	}
	ents, err := os.ReadDir(filepath.Join(filepath.Dir(img), "src", "many"))
	Expect(err).To(Succeed())
	want := make([]string, len(ents))
	for i, e := range ents {
		want[i] = e.Name()
	}
	sort.Strings(got)
	sort.Strings(want)
	Expect(got).To(Equal(want))
	_, err = f.ReadDir(many, n-1)
	Expect(err).To(MatchError(ErrLimit))
	// debugfs agrees on the count.
	ls := parseLS(debugfsBatch(img, []string{"ls -p /many"})[0])
	Expect(ls).To(HaveLen(n + 2))
}

var _ = Describe("large directories", func() {
	It("lists 5000 entries of an htree", func() {
		spec := imageSpec{name: "dir5000", size: 64 << 20, args: []string{"-t", "ext4", "-O", "^has_journal", "-b", "1024", "-N", "6000"}, tree: bigDirTree(5000), fsck: true}
		img := buildImage(spec)
		DeferCleanup(releaseImage, spec.name)
		checkBigDir(img, 5000, 1)
	})

	It("lists 30000 entries of an htree", func() {
		spec := imageSpec{name: "dir30000", size: 64 << 20, args: []string{"-t", "ext4", "-O", "^has_journal", "-b", "1024", "-N", "30100"}, tree: bigDirTree(30000), fsck: true}
		img := buildImage(spec)
		DeferCleanup(releaseImage, spec.name)
		checkBigDir(img, 30000, 1)
	})
})
