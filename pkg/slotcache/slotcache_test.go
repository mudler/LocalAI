package slotcache_test

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/mudler/LocalAI/pkg/slotcache"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidateFilename", func() {
	DescribeTable("accepts a plain file name",
		func(name string) { Expect(ValidateFilename(name)).To(Succeed()) },
		Entry("simple", "prompt.bin"),
		Entry("dashes and underscores", "ha-system_prompt.v2.bin"),
		Entry("no extension", "prompt"),
		Entry("non-ASCII letters", "caf\u00e9.bin"),
		Entry("255 bytes", strings.Repeat("a", 255)),
	)

	DescribeTable("rejects a name that is not a single component",
		func(name string) { Expect(ValidateFilename(name)).To(MatchError(ErrInvalidFilename)) },
		Entry("empty", ""),
		Entry("dot", "."),
		Entry("dot dot", ".."),
		Entry("parent traversal", "../secret"),
		Entry("hidden traversal", "a/../../secret"),
		Entry("forward slash", "a/b"),
		Entry("backslash", `a\b`),
		Entry("absolute path", "/etc/passwd"),
		Entry("drive path", `C:\x`),
		Entry("NUL byte", "a\x00b"),
		Entry("NUL then separator", "a\x00/b"),
		Entry("newline", "a\nb"),
		Entry("DEL", "a\x7fb"),
		Entry("C1 control", "a\u0085b"),
		Entry("leading space", " a"),
		Entry("trailing space", "a "),
		Entry("trailing dot", "a."),
		Entry("wildcard", "a*b"),
		Entry("pipe", "a|b"),
		Entry("division slash look-alike", "a\u2215b"),
		Entry("fullwidth dot look-alike", "a\uff0eb"),
		Entry("overlong slash", "a\xc0\xafb"),
		Entry("invalid UTF-8", "a\xffb"),
		Entry("replacement character", "a\ufffdb"),
		Entry("256 bytes", strings.Repeat("a", 256)),
	)
})

var _ = Describe("Resolve", func() {
	var dir string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		// TempDir may sit behind a symlink (for example /var on macOS).
		real, err := filepath.EvalSymlinks(dir)
		Expect(err).ToNot(HaveOccurred())
		dir = real
	})

	It("returns a path directly inside the directory", func() {
		p, err := Resolve(dir, "prompt.bin")
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(filepath.Join(dir, "prompt.bin")))
	})

	It("refuses an empty directory", func() {
		_, err := Resolve("", "prompt.bin")
		Expect(err).To(HaveOccurred())
	})

	It("refuses traversal and absolute names", func() {
		for _, name := range []string{"../x", "a/../../x", filepath.Join(dir, "x"), "/etc/passwd"} {
			_, err := Resolve(dir, name)
			Expect(err).To(HaveOccurred(), name)
		}
	})

	It("works when the directory does not exist on this host", func() {
		p, err := Resolve(filepath.Join(dir, "missing"), "prompt.bin")
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(filepath.Join(dir, "missing", "prompt.bin")))
	})

	It("refuses a symbolic link that points outside the directory", func() {
		outside := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600)).To(Succeed())
		if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "link.bin")); err != nil {
			Skip("symbolic links are not available: " + err.Error())
		}
		_, err := Resolve(dir, "link.bin")
		Expect(err).To(HaveOccurred())
	})

	It("accepts a symbolic link that stays inside the directory", func() {
		Expect(os.WriteFile(filepath.Join(dir, "real.bin"), []byte("x"), 0o600)).To(Succeed())
		if err := os.Symlink(filepath.Join(dir, "real.bin"), filepath.Join(dir, "alias.bin")); err != nil {
			Skip("symbolic links are not available: " + err.Error())
		}
		p, err := Resolve(dir, "alias.bin")
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(filepath.Join(dir, "real.bin")))
	})
})

var _ = Describe("DirFromOptions", func() {
	It("returns empty when the option is absent", func() {
		Expect(DirFromOptions([]string{"cache_ram:1024", "use_jinja"})).To(BeEmpty())
		Expect(DirFromOptions(nil)).To(BeEmpty())
	})

	It("returns the value, keeping any colon in it", func() {
		Expect(DirFromOptions([]string{"slot_save_path:/var/cache/slots"})).To(Equal("/var/cache/slots"))
		Expect(DirFromOptions([]string{`slot_save_path:C:\slots`})).To(Equal(`C:\slots`))
	})

	It("lets a later option win, as the backend does", func() {
		Expect(DirFromOptions([]string{"slot_save_path:/a", "slot_save_path:/b"})).To(Equal("/b"))
	})

	It("treats the bare flag form as unset", func() {
		Expect(DirFromOptions([]string{"slot_save_path"})).To(BeEmpty())
	})
})
