package ext4mk_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ext4mk"
)

// tool finds an e2fsprogs program. The programs often live in a directory
// that is not in PATH for a normal user, so look there too.
func tool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, dir := range []string{"/sbin", "/usr/sbin", "/usr/local/sbin", "/opt/homebrew/opt/e2fsprogs/sbin", "/usr/local/opt/e2fsprogs/sbin"} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	Skip(name + " is not installed (package e2fsprogs): the check against the real tool cannot run")
	return ""
}

// run runs a program with a time limit and returns its output and exit code.
func run(name string, args ...string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- fixed test programs
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return out.String(), ee.ExitCode()
	}
	Expect(err).NotTo(HaveOccurred(), "%s %v: %s", name, args, out.String())
	return out.String(), 0
}

func expectClean(path string) {
	out, code := run(tool("e2fsck"), "-fn", path)
	Expect(code).To(BeZero(), "e2fsck:\n%s", out)
	for _, bad := range []string{"WARNING", "Error", "differ", "Inode bitmap", "Block bitmap", "Illegal", "FIXED", "unattached"} {
		Expect(out).NotTo(ContainSubstring(bad), "e2fsck:\n%s", out)
	}
	Expect(out).To(ContainSubstring("Pass 5: Checking group summary information"))
}

func debugfs(path string, cmds ...string) string {
	out, code := run(tool("debugfs"), "-R", strings.Join(cmds, "\n"), path)
	Expect(code).To(BeZero(), "debugfs %v:\n%s", cmds, out)
	return out
}

// header returns the key and value lines of dumpe2fs -h.
func header(path string) map[string]string {
	out, _ := run(tool("dumpe2fs"), "-h", path)
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(l, ":"); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

func num(m map[string]string, key string) int64 {
	n, err := strconv.ParseInt(m[key], 10, 64)
	Expect(err).NotTo(HaveOccurred(), "%s = %q", key, m[key])
	return n
}

var _ = Describe("e2fsprogs", func() {
	DescribeTable("accepts the image",
		func(size int64, groups int64) {
			path := newImage(ext4mk.Options{Size: size, RootUID: 1000, RootGID: 1000, Label: "upper"})
			expectClean(path)

			h := header(path)
			Expect(h["Filesystem features"]).To(Equal("ext_attr dir_index filetype extent sparse_super large_file huge_file dir_nlink extra_isize"))
			Expect(h["Filesystem state"]).To(Equal("clean"))
			Expect(h["Filesystem volume name"]).To(Equal("upper"))
			Expect(h["Default mount options"]).To(Equal("user_xattr acl"))
			Expect(h["Block size"]).To(Equal("4096"))
			Expect(h["Inode size"]).To(Equal("256"))
			Expect(h["Reserved block count"]).To(Equal("0"))
			Expect(h["Filesystem OS type"]).To(Equal("Linux"))
			Expect(h["Default directory hash"]).To(Equal("half_md4"))
			s := parse(path)
			defer s.close()
			Expect(int64(s.groups)).To(Equal(groups))
			Expect(num(h, "Block count")).To(BeEquivalentTo(s.blocks))
			Expect(num(h, "Free blocks")).To(BeEquivalentTo(s.freeBlocks))
			Expect(num(h, "Inode count")).To(BeEquivalentTo(s.inodes))
			Expect(num(h, "Free inodes")).To(BeEquivalentTo(s.inodes - 11))
			Expect(num(h, "Inodes per group")).To(BeEquivalentTo(s.ipg))
			Expect(num(h, "Blocks per group")).To(BeEquivalentTo(bpg))

			// The free blocks follow from the layout: the blocks minus the
			// metadata of each group and the two directory blocks.
			var meta int64
			backups := map[uint32]bool{}
			for _, g := range backupGroups(s.groups) {
				backups[g] = true
			}
			for n := uint32(0); n < s.groups; n++ {
				meta += 2 + int64(s.ipg/16)
				if backups[n] {
					meta += 1 + int64(s.gdtBlocks)
				}
			}
			Expect(int64(s.freeBlocks)).To(Equal(int64(s.blocks) - meta - 2))

			Expect(debugfs(path, "ls -l /")).To(And(ContainSubstring("lost+found"), ContainSubstring("1000")))
			Expect(debugfs(path, "stat <11>")).To(ContainSubstring("Links: 2"))
			Expect(debugfs(path, "show_super_stats -h")).To(ContainSubstring("Filesystem magic number:  0xEF53"))
		},
		Entry("16 MiB", 16*MiB, int64(1)),
		Entry("33 MiB", 33*MiB, int64(1)),
		Entry("127 MiB", 127*MiB, int64(1)),
		Entry("128 MiB", 128*MiB, int64(1)),
		Entry("129 MiB", 129*MiB, int64(1)),
		Entry("1 GiB", GiB, int64(8)),
		Entry("1 GiB and a block", GiB+4096, int64(8)),
		Entry("4 GiB, sparse", 4*GiB, int64(32)),
		Entry("64 GiB, sparse", 64*GiB, int64(512)),
	)

	It("sets the root owner", func() {
		path := newImage(ext4mk.Options{Size: 16 * MiB, RootUID: 1000, RootGID: 1001})
		out := debugfs(path, "stat <2>")
		Expect(out).To(MatchRegexp(`User:\s+1000\s+Group:\s+1001`))
		Expect(out).To(ContainSubstring("Mode:  0755"))
		Expect(out).To(ContainSubstring("Type: directory"))
		Expect(out).To(ContainSubstring("Links: 3"))
		Expect(out).To(ContainSubstring("Flags: 0x80000"))
		Expect(out).To(MatchRegexp(`Size of extra inode fields: 32`))
		out = debugfs(path, "stat <11>")
		Expect(out).To(MatchRegexp(`User:\s+0\s+Group:\s+0`))
		Expect(out).To(ContainSubstring("Mode:  0700"))
	})

	// The numbers that mke2fs makes for the same size. The command pins the
	// same features and the default inode ratio (-i 16384): without it mke2fs
	// picks the "small" profile below 512 MiB, with a ratio of 4096.
	//
	// Documented differences, which the tolerances below allow:
	//   - lost+found is one block here. mke2fs makes four, so this file
	//     system has 3 more free blocks.
	//   - A last group that is too small is cut off by both. mke2fs keeps the
	//     inode count it computed before the cut, this package computes it
	//     again, so the inode count can differ by a few per cut group (the
	//     129 MiB case: 8192 against 8256). Each missing 16 inodes is one
	//     more free block, so that case has 4 more free blocks as well.
	//   - mke2fs reserves 5% of the blocks for root. This package reserves
	//     none. The reserved count is not part of the free count, so the
	//     totals still compare.
	DescribeTable("matches mke2fs",
		func(size int64) {
			mke2fs := tool("mke2fs")
			ref := filepath.Join(GinkgoT().TempDir(), "ref.img")
			out, code := run(mke2fs, "-q", "-F", "-t", "ext4", "-O", "^has_journal,^resize_inode,^flex_bg,^metadata_csum,^64bit,^uninit_bg",
				"-b", "4096", "-I", "256", "-i", "16384", ref, fmt.Sprintf("%dK", size/1024))
			Expect(code).To(BeZero(), out)
			mine := newImage(ext4mk.Options{Size: size})

			r, m := header(ref), header(mine)
			Expect(m["Filesystem features"]).To(Equal(r["Filesystem features"]))
			Expect(m["Block size"]).To(Equal(r["Block size"]))
			Expect(m["Inode size"]).To(Equal(r["Inode size"]))
			Expect(m["Blocks per group"]).To(Equal(r["Blocks per group"]))
			Expect(m["Default mount options"]).To(Equal(r["Default mount options"]))

			Expect(num(m, "Block count")).To(Equal(num(r, "Block count")))
			// Free blocks: three more here, from the smaller lost+found, and
			// one more per 16 inodes that this side has fewer in each group.
			itable := (num(r, "Inodes per group") - num(m, "Inodes per group")) / 16 * ((num(m, "Block count") + bpg - 1) / bpg)
			Expect(num(m, "Free blocks") - num(r, "Free blocks")).To(Equal(3 + itable))
			// Free inodes: the same, up to the rounding after a cut group.
			Expect(num(m, "Free inodes") - num(r, "Free inodes")).To(BeNumerically("~", 0, 64))
			if size != 129*MiB {
				Expect(num(m, "Free inodes")).To(Equal(num(r, "Free inodes")))
				Expect(num(m, "Inodes per group")).To(Equal(num(r, "Inodes per group")))
			}
		},
		Entry("16 MiB", 16*MiB),
		Entry("33 MiB", 33*MiB),
		Entry("127 MiB", 127*MiB),
		Entry("128 MiB", 128*MiB),
		Entry("129 MiB", 129*MiB),
		Entry("1 GiB", GiB),
		Entry("4 GiB", 4*GiB),
	)

	It("is usable by another ext4 implementation", func() {
		path := newImage(ext4mk.Options{Size: 64 * MiB, RootUID: 1000, RootGID: 1000})
		dir := GinkgoT().TempDir()
		src := filepath.Join(dir, "payload")
		payload := bytes.Repeat([]byte("sandbox upper layer\n"), 20000) // about 400 KiB, several blocks
		Expect(os.WriteFile(src, payload, 0o600)).To(Succeed())
		small := filepath.Join(dir, "small")
		Expect(os.WriteFile(small, []byte("hello\n"), 0o600)).To(Succeed())

		script := filepath.Join(dir, "script")
		Expect(os.WriteFile(script, []byte(strings.Join([]string{
			"write " + src + " payload",
			"write " + small + " small",
			"mkdir work",
			"mkdir work/deep",
			"write " + small + " work/deep/inner",
			"symlink link payload",
			"symlink longlink /" + strings.Repeat("a/", 40) + "target",
			"mknod null c 0 0",
			"ea_set / user.owner owner-value",
			"ea_set small user.note a-value",
			"",
		}, "\n")), 0o600)).To(Succeed())
		out, code := run(tool("debugfs"), "-w", "-f", script, path)
		Expect(code).To(BeZero(), out)
		Expect(out).NotTo(ContainSubstring("not found"))
		Expect(out).NotTo(ContainSubstring("No free"))

		expectClean(path)

		Expect(debugfs(path, "cat small")).To(ContainSubstring("hello"))
		Expect(debugfs(path, "cat work/deep/inner")).To(ContainSubstring("hello"))
		// Compare the large file byte for byte.
		dump := filepath.Join(dir, "dump")
		debugfs(path, "dump payload "+dump)
		got, err := os.ReadFile(dump)
		Expect(err).NotTo(HaveOccurred())
		Expect(bytes.Equal(got, payload)).To(BeTrue(), "file content differs")

		ls := debugfs(path, "ls -l /")
		for _, name := range []string{"payload", "small", "work", "link", "longlink", "null", "lost+found"} {
			Expect(ls).To(ContainSubstring(name))
		}
		Expect(debugfs(path, "stat link")).To(ContainSubstring(`Fast link dest: "payload"`))
		Expect(debugfs(path, "stat longlink")).To(MatchRegexp(`Type: symlink`))
		Expect(debugfs(path, "stat null")).To(MatchRegexp(`Type: character special`))
		Expect(debugfs(path, "ea_get / user.owner")).To(ContainSubstring("owner-value"))
		Expect(debugfs(path, "ea_get small user.note")).To(ContainSubstring("a-value"))
		Expect(debugfs(path, "ls /work")).To(ContainSubstring("deep"))

		// The totals changed and still agree with the bitmaps.
		h := header(path)
		Expect(num(h, "Free inodes")).To(BeEquivalentTo(num(h, "Inode count") - 11 - 8))
		re := regexp.MustCompile(`(?m)^Filesystem state:\s+clean`)
		Expect(re.MatchString(headerText(path))).To(BeTrue())
	})
})

func headerText(path string) string {
	out, _ := run(tool("dumpe2fs"), "-h", path)
	return out
}
