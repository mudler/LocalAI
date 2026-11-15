package ext4ro

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// pattern returns n bytes that do not compress and do not repeat on a block
// boundary, so a block that the reader maps to the wrong place shows.
func pattern(n int, seed uint32) []byte {
	b := make([]byte, n)
	x := seed*2654435761 + 1
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x >> 11)
	}
	return b
}

func write(dir, name string, data []byte, mode os.FileMode) {
	GinkgoHelper()
	p := filepath.Join(dir, name)
	Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
	Expect(os.WriteFile(p, data, mode)).To(Succeed())
	Expect(os.Chmod(p, mode)).To(Succeed())
}

// sparseFile writes pieces at offsets in a file of the given size.
func sparseFile(dir, name string, size int64, pieces map[int64][]byte) {
	GinkgoHelper()
	p := filepath.Join(dir, name)
	Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
	f, err := os.Create(p)
	Expect(err).To(Succeed())
	Expect(f.Truncate(size)).To(Succeed())
	for off, b := range pieces {
		_, err := f.WriteAt(b, off)
		Expect(err).To(Succeed())
	}
	Expect(f.Close()).To(Succeed())
}

// makeTree fills dir with a tree that covers the file kinds and the names
// that the reader must handle.
func makeTree(dir string) {
	GinkgoHelper()
	write(dir, "empty", nil, 0o644)
	write(dir, "one", []byte("x"), 0o644)
	write(dir, "hello.txt", []byte("hello\n"), 0o644)
	for i, n := range []int{59, 60, 61, 1023, 1024, 1025, 4095, 4096, 4097, 65536, 65537, 300000} {
		write(dir, fmt.Sprintf("size/f%d", n), pattern(n, uint32(i+1)), 0o640)
	}
	sparseFile(dir, "sparse/holes", 1<<20, map[int64][]byte{
		0: pattern(100, 50), 100000: pattern(5000, 51), 700000: pattern(1, 52), 1<<20 - 10: pattern(10, 53),
	})
	sparseFile(dir, "sparse/tail-hole", 5<<20, map[int64][]byte{0: []byte("start")})
	sparseFile(dir, "sparse/all-hole", 3<<20, nil)
	write(dir, "a/b/c/d/e/f/deep.txt", []byte("deep\n"), 0o600)
	Expect(os.MkdirAll(filepath.Join(dir, "emptydir"), 0o755)).To(Succeed())
	Expect(os.Symlink("hello.txt", filepath.Join(dir, "links-short"))).To(Succeed())
	Expect(os.MkdirAll(filepath.Join(dir, "links"), 0o755)).To(Succeed())
	for _, n := range []int{1, 59, 60, 61, 100, 300, 1000} {
		Expect(os.Symlink(strings.Repeat("t", n), filepath.Join(dir, "links", fmt.Sprintf("len%d", n)))).To(Succeed())
	}
	Expect(os.Symlink("../nowhere/x", filepath.Join(dir, "links", "dangling"))).To(Succeed())
	Expect(os.Link(filepath.Join(dir, "hello.txt"), filepath.Join(dir, "hard-link"))).To(Succeed())
	write(dir, "names/"+strings.Repeat("n", 255), []byte("long name"), 0o644)
	write(dir, "names/with space", []byte("space"), 0o644)
	write(dir, "names/ünï-名前", []byte("unicode"), 0o644)
	write(dir, "names/a\nb", []byte("newline"), 0o644)
	write(dir, "perm/exec", []byte("#!/bin/sh\n"), 0o755)
	write(dir, "perm/secret", []byte("s"), 0o600)
	write(dir, "perm/setuid", []byte("s"), 0o755)
	Expect(os.Chmod(filepath.Join(dir, "perm/setuid"), 0o755|os.ModeSetuid)).To(Succeed())
	Expect(os.Chmod(filepath.Join(dir, "perm"), 0o755|os.ModeSticky)).To(Succeed())
	for i := range 300 {
		write(dir, fmt.Sprintf("many/file-%04d", i), []byte{byte(i)}, 0o644)
	}
}

// treeScript adds what mke2fs -d cannot: devices, a fifo, attributes and
// symlinks that debugfs makes.
func treeScript(dir string) []string {
	GinkgoHelper()
	vals := filepath.Join(dir, "vals")
	Expect(os.MkdirAll(vals, 0o755)).To(Succeed())
	cap20 := []byte{0x01, 0, 0, 0x02, 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	acl := []byte{1, 0, 0, 0, 1, 0, 6, 0, 4, 0, 4, 0, 32, 0, 5, 0, 0xe8, 3, 0, 0}
	Expect(os.WriteFile(filepath.Join(vals, "cap"), cap20, 0o600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(vals, "acl"), acl, 0o600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(vals, "big"), pattern(700, 7), 0o600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(vals, "empty"), nil, 0o600)).To(Succeed())
	return []string{
		"mknod w c 0 0",
		// mknod takes a name, not a path: a path makes an entry with "/" in
		// its name. That is the forged record that the reader rejects.
		"cd emptydir",
		"mknod w2 c 0 0",
		"cd /",
		"mknod pipe p",
		"mknod chr c 4 64",
		"mknod blk b 8 1",
		"mknod bigdev c 4 300",
		"mknod bigdev2 c 300 7",
		"symlink dbg-fast target-of-a-fast-link",
		"symlink dbg-slow " + strings.Repeat("s", 90),
		"ea_set / trusted.overlay.opaque y",
		"ea_set emptydir user.x hello",
		"ea_set -f " + filepath.Join(vals, "cap") + " hello.txt security.capability",
		"ea_set -f " + filepath.Join(vals, "big") + " hello.txt user.big",
		"ea_set -f " + filepath.Join(vals, "empty") + " hello.txt user.empty",
		"ea_set -r -f " + filepath.Join(vals, "acl") + " emptydir system.posix_acl_access",
		"ea_set -r -f " + filepath.Join(vals, "acl") + " emptydir system.posix_acl_default",
		"ea_set size/f1024 user.one 1",
		"ea_set size/f1024 user.two 22",
		"ea_set size/f1024 trusted.three 333",
		"ea_set size/f1024 security.selinux system_u:object_r:etc_t:s0",
	}
}

type variant struct {
	name string
	args []string
	// hostSkip lists paths where mke2fs -d itself makes a wrong file, so the
	// host folder is not the truth. The debugfs check still covers them.
	hostSkip []string
}

var variants = []variant{
	{"ext4-default", []string{"-t", "ext4", "-O", "^has_journal"}, nil},
	{"ext4-1k", []string{"-t", "ext4", "-O", "^has_journal", "-b", "1024"}, nil},
	{"ext4-2k", []string{"-t", "ext4", "-O", "^has_journal", "-b", "2048"}, nil},
	{"ext4-64k", []string{"-t", "ext4", "-O", "^has_journal", "-b", "65536"}, nil},
	{"ext4-no64bit", []string{"-t", "ext4", "-O", "^has_journal,^64bit"}, nil},
	{"ext4-nocsum", []string{"-t", "ext4", "-O", "^has_journal,^metadata_csum"}, nil},
	{"ext4-nodirindex", []string{"-t", "ext4", "-O", "^has_journal,^dir_index"}, nil},
	{"ext4-inode128", []string{"-t", "ext4", "-O", "^has_journal", "-I", "128"}, nil},
	{"ext4-inode512", []string{"-t", "ext4", "-O", "^has_journal", "-I", "512"}, nil},
	// With inline_data, mke2fs -d loses the trailing hole of a sparse file:
	// debugfs shows the size 4096 for a 5 MiB file.
	{"ext4-inline", []string{"-t", "ext4", "-O", "^has_journal,inline_data"}, []string{"/sparse/tail-hole"}},
	{"ext4-noextent", []string{"-t", "ext4", "-O", "^has_journal,^extent,^64bit"}, nil},
	{"ext4-noflex", []string{"-t", "ext4", "-O", "^has_journal,^flex_bg"}, nil},
	{"ext4-nosparse", []string{"-t", "ext4", "-O", "^has_journal,^sparse_super,^resize_inode"}, nil},
	{"ext4-metabg", []string{"-t", "ext4", "-O", "^has_journal,meta_bg,^resize_inode", "-g", "512", "-b", "1024"}, nil},
	{"ext4-nofiletype", []string{"-t", "ext4", "-O", "^has_journal,^filetype"}, nil},
	{"ext4-hugefile", []string{"-t", "ext4", "-O", "^has_journal,huge_file"}, nil},
	{"ext2-1k", []string{"-t", "ext2", "-b", "1024"}, nil},
	{"ext2-4k", []string{"-t", "ext2", "-b", "4096"}, nil},
	{"ext2-rev0", []string{"-t", "ext2", "-r", "0", "-b", "1024"}, nil},
	{"ext3-nojournal", []string{"-t", "ext3", "-O", "^has_journal"}, nil},
}

func treeSpec(v variant) imageSpec {
	return imageSpec{name: v.name, size: 32 << 20, args: v.args, tree: makeTree, script: treeScript}
}

var _ = Describe("a tree that mke2fs built, read back", func() {
	for _, v := range variants {
		It("matches the host folder and debugfs: "+v.name, func() {
			spec := treeSpec(v)
			DeferCleanup(releaseImage, v.name)
			img := buildImage(spec)
			data := readImage(img)
			f := mustOpen(data)
			nodes, err := walkFS(f, defaultWalk)
			Expect(err).To(Succeed())
			Expect(len(nodes)).To(BeNumerically(">", 350))
			host := walkHost(filepath.Join(suiteDir, v.name, "src"))
			for _, p := range v.hostSkip {
				delete(host, p)
			}
			compareWithHost(nodes, host)
			compareWithDebugfs(img, f, nodes, true)
			checkTreeFacts(f, nodes, v)
		})
	}
})

// checkTreeFacts checks what the debugfs script made, by name, so a reader
// that is wrong in the same way as debugfs would still fail.
func checkTreeFacts(f *FS, nodes map[string]*node, v variant) {
	GinkgoHelper()
	checkFeatures(f, nodes, v)
	Expect(nodes["/w"].In.IsWhiteout()).To(BeTrue(), "w is a whiteout")
	Expect(nodes["/emptydir/w2"].In.IsWhiteout()).To(BeTrue())
	Expect(nodes["/chr"].In.IsWhiteout()).To(BeFalse())
	Expect(nodes["/chr"].In.DevMajor()).To(BeEquivalentTo(4))
	Expect(nodes["/chr"].In.DevMinor()).To(BeEquivalentTo(64))
	Expect(nodes["/blk"].In.DevMajor()).To(BeEquivalentTo(8))
	Expect(nodes["/blk"].In.DevMinor()).To(BeEquivalentTo(1))
	Expect(nodes["/bigdev"].In.DevMajor()).To(BeEquivalentTo(4), "new device encoding")
	Expect(nodes["/bigdev"].In.DevMinor()).To(BeEquivalentTo(300))
	Expect(nodes["/bigdev2"].In.DevMajor()).To(BeEquivalentTo(300))
	Expect(nodes["/bigdev2"].In.DevMinor()).To(BeEquivalentTo(7))
	Expect(nodes["/pipe"].In.Mode & sIFMT).To(BeEquivalentTo(0o010000))
	Expect(nodes["/hello.txt"].Ino).To(Equal(nodes["/hard-link"].Ino))
	Expect(nodes["/hello.txt"].In.Links).To(BeEquivalentTo(2))
	Expect(nodes["/dbg-fast"].Target).To(Equal("target-of-a-fast-link"))
	Expect(nodes["/dbg-slow"].Target).To(Equal(strings.Repeat("s", 90)))
	Expect(nodes["/links/len1000"].Target).To(Equal(strings.Repeat("t", 1000)))
	Expect(nodes["/names/"+strings.Repeat("n", 255)]).NotTo(BeNil())
	Expect(nodes["/names/a\nb"]).NotTo(BeNil())
	if strings.HasSuffix(v.name, "rev0") {
		// Revision 0 has no extended attributes: debugfs ignores ea_set.
		Expect(f.FirstIno()).To(BeEquivalentTo(11))
		return
	}
	Expect(nodes[""].Xattrs).To(HaveKeyWithValue("trusted.overlay.opaque", "79"))
	Expect(nodes["/emptydir"].Xattrs).To(HaveKeyWithValue("user.x", "68656c6c6f"))
	Expect(nodes["/emptydir"].Xattrs).To(HaveKey("system.posix_acl_access"))
	Expect(nodes["/emptydir"].Xattrs).To(HaveKey("system.posix_acl_default"))
	Expect(nodes["/hello.txt"].Xattrs).To(HaveKeyWithValue("security.capability", "01000002ffffffff0000000000000000"+"00000000"))
	Expect(nodes["/hello.txt"].Xattrs).To(HaveKeyWithValue("user.empty", ""))
	Expect(nodes["/hello.txt"].Xattrs["user.big"]).To(HaveLen(1400))
	Expect(nodes["/size/f1024"].Xattrs).To(HaveLen(4))
	Expect(nodes["/size/f1024"].Xattrs).To(HaveKeyWithValue("security.selinux", fmt.Sprintf("%x", "system_u:object_r:etc_t:s0")))
	Expect(nodes["/sparse/all-hole"].Sum).To(Equal(zeroSum(3 << 20)))
	Expect(nodes["/many/file-0299"].In.Size).To(BeEquivalentTo(1))
	if strings.Contains(v.name, "inline") {
		inl := 0
		for _, n := range nodes {
			if n.In.Flags&flagInlineData != 0 {
				inl++
			}
		}
		Expect(inl).To(BeNumerically(">", 5), "an inline_data image has inline inodes")
	}
	if v.name == "ext4-1k" {
		// The 128-byte inode image has no extra fields.
		Expect(f.BlockSize()).To(Equal(1024))
	}
	if v.name == "ext4-inode128" {
		Expect(nodes["/hello.txt"].In.CtimeExtra).To(BeZero())
	}
}

func zeroSum(n int) string {
	return sumHex(bytes.Repeat([]byte{0}, n))
}

// The i_block bytes of an inode, and a helper that finds where an inode is.
func inodeOffset(f *FS, ino uint64) int64 {
	GinkgoHelper()
	t, err := f.inodeTable((ino - 1) / f.ipg)
	Expect(err).To(Succeed())
	return int64(t*f.bs + ((ino-1)%f.ipg)*f.isz)
}

// checkFeatures checks that the image has the layout that the variant asks
// for, so a variant cannot pass without testing what its name says.
func checkFeatures(f *FS, nodes map[string]*node, v variant) {
	GinkgoHelper()
	extents, maxBlockMapSize := 0, int64(0)
	for _, n := range nodes {
		if n.In.IsRegular() && n.In.Flags&flagInlineData == 0 {
			if n.In.Flags&flagExtents != 0 {
				extents++
			} else if n.In.Size > maxBlockMapSize {
				maxBlockMapSize = n.In.Size
			}
		}
	}
	switch v.name {
	case "ext4-1k", "ext2-1k", "ext2-rev0", "ext4-metabg":
		Expect(f.BlockSize()).To(Equal(1024))
	case "ext4-2k":
		Expect(f.BlockSize()).To(Equal(2048))
	case "ext4-64k":
		Expect(f.BlockSize()).To(Equal(65536))
	case "ext2-4k":
		Expect(f.BlockSize()).To(Equal(4096))
	}
	if strings.HasPrefix(v.name, "ext2") || v.name == "ext3-nojournal" || v.name == "ext4-noextent" {
		Expect(extents).To(BeZero(), "block-mapped image")
		Expect(maxBlockMapSize).To(BeNumerically(">=", 300000))
		Expect(f.incompat & incompatExtents).To(BeZero())
	} else {
		Expect(extents).To(BeNumerically(">", 10))
	}
	Expect(f.incompat&incompat64Bit != 0).To(Equal(v.name == "ext4-default" || v.name == "ext4-1k" || v.name == "ext4-2k" || v.name == "ext4-64k" ||
		v.name == "ext4-nocsum" || v.name == "ext4-nodirindex" || v.name == "ext4-inode128" || v.name == "ext4-inode512" || v.name == "ext4-inline" ||
		v.name == "ext4-noflex" || v.name == "ext4-nosparse" || v.name == "ext4-metabg" || v.name == "ext4-nofiletype" || v.name == "ext4-hugefile"), "64bit in %s", v.name)
	if v.name == "ext4-metabg" {
		Expect(f.incompat & incompatMetaBG).NotTo(BeZero())
		Expect(f.groups).To(BeNumerically(">", 48), "four meta groups")
	}
	if v.name == "ext4-nofiletype" || v.name == "ext2-rev0" {
		Expect(f.incompat & incompatFiletype).To(BeZero())
	}
	if v.name == "ext4-inode128" || v.name == "ext2-rev0" {
		Expect(f.isz).To(BeEquivalentTo(128))
	}
	if v.name == "ext4-inode512" {
		Expect(f.isz).To(BeEquivalentTo(512))
	}
	if v.name == "ext4-nosparse" {
		Expect(f.roCompat & roCompatSparseSuper).To(BeZero())
	}
}
