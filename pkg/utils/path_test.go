package utils_test

import (
	"os"
	"path/filepath"

	. "github.com/mudler/LocalAI/pkg/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("utils/path tests", func() {
	Describe("VerifyPath", func() {
		It("accepts a simple file directly inside the base path", func() {
			Expect(VerifyPath("model.bin", "/srv/models")).To(Succeed())
		})

		It("accepts a nested subdirectory inside the base path", func() {
			Expect(VerifyPath("subdir/model.bin", "/srv/models")).To(Succeed())
		})

		It("accepts traversal sequences that stay inside the base", func() {
			// "a/b/../c" collapses to "a/c", still strictly inside the base,
			// so the verifier should permit it.
			Expect(VerifyPath("a/b/../c", "/srv/models")).To(Succeed())
		})

		It("rejects a single parent-traversal that escapes the base", func() {
			Expect(VerifyPath("../etc/passwd", "/srv/models")).ToNot(Succeed())
		})

		It("rejects compound traversal that climbs above the base", func() {
			Expect(VerifyPath("a/../../etc/passwd", "/srv/models")).ToNot(Succeed())
		})

		It("rejects a deeply-escaping path that lands on the filesystem root", func() {
			Expect(VerifyPath("../../etc/passwd", "/srv/models")).ToNot(Succeed())
		})

		It("rejects the base path itself", func() {
			// Documents that VerifyPath requires a strict descendant: an
			// empty user input resolves to the base directory and is
			// rejected, which is the safer default for a download helper
			// that expects a target file inside the base.
			Expect(VerifyPath("", "/srv/models")).ToNot(Succeed())
		})

		It("treats an absolute-looking user input as relative to the base", func() {
			// filepath.Join discards no segments here: the result is
			// "/srv/models/etc/passwd", which is still inside the base.
			// This protects callers that forward untrusted user paths
			// directly to the verifier.
			Expect(VerifyPath("/etc/passwd", "/srv/models")).To(Succeed())
		})

		It("is purely lexical and does not follow symlinks", func() {
			// VerifyPath uses filepath.Clean, not filepath.EvalSymlinks,
			// so a symlink that escapes the base is not detected here.
			// Callers who must defend against symlink escapes need to
			// EvalSymlinks before delegating to VerifyPath. This test
			// pins the current contract so the trade-off stays explicit.
			tmpDir := GinkgoT().TempDir()
			base := filepath.Join(tmpDir, "base")
			outside := filepath.Join(tmpDir, "outside")
			Expect(os.Mkdir(base, 0o755)).To(Succeed())
			Expect(os.Mkdir(outside, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o600)).To(Succeed())
			Expect(os.Symlink(outside, filepath.Join(base, "escape"))).To(Succeed())

			Expect(VerifyPath("escape/secret.txt", base)).To(Succeed())
		})
	})

	Describe("InTrustedRoot", func() {
		It("accepts a strict descendant of the trusted root", func() {
			Expect(InTrustedRoot("/srv/models/file", "/srv/models")).To(Succeed())
		})

		It("accepts a deeply nested descendant", func() {
			Expect(InTrustedRoot("/srv/models/a/b/c/file", "/srv/models")).To(Succeed())
		})

		It("rejects the trusted root itself", func() {
			// The implementation walks up before comparing, so the input
			// path must have at least one component beneath the root.
			Expect(InTrustedRoot("/srv/models", "/srv/models")).ToNot(Succeed())
		})

		It("rejects a sibling directory that shares the parent", func() {
			Expect(InTrustedRoot("/srv/other/file", "/srv/models")).ToNot(Succeed())
		})

		It("rejects an unrelated absolute path", func() {
			Expect(InTrustedRoot("/etc/passwd", "/srv/models")).ToNot(Succeed())
		})
	})

	Describe("SanitizeFileName", func() {
		It("returns the original name when nothing is unsafe", func() {
			Expect(SanitizeFileName("model.bin")).To(Equal("model.bin"))
		})

		It("strips leading directory components", func() {
			Expect(SanitizeFileName("subdir/model.bin")).To(Equal("model.bin"))
		})

		It("strips absolute path prefixes", func() {
			Expect(SanitizeFileName("/etc/passwd")).To(Equal("passwd"))
		})

		It("collapses parent-traversal sequences and keeps only the leaf", func() {
			Expect(SanitizeFileName("../etc/passwd")).To(Equal("passwd"))
		})

		It("removes embedded .. sequences that Clean+Base alone do not catch", func() {
			// After Clean+Base "foo..bar" survives unchanged; the explicit
			// ReplaceAll on ".." in the implementation is the last line of
			// defence against filenames that look benign but still contain
			// traversal markers.
			Expect(SanitizeFileName("foo..bar")).To(Equal("foobar"))
		})

		It("returns an empty string when the input is only a parent reference", func() {
			Expect(SanitizeFileName("..")).To(Equal(""))
		})
	})

	Describe("GenerateUniqueFileName", func() {
		It("returns the bare filename when no collision exists", func() {
			tmpDir := GinkgoT().TempDir()
			Expect(GenerateUniqueFileName(tmpDir, "model", ".bin")).To(Equal("model.bin"))
		})

		It("suffixes with _2 when the bare filename already exists", func() {
			tmpDir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(tmpDir, "model.bin"), nil, 0o600)).To(Succeed())

			Expect(GenerateUniqueFileName(tmpDir, "model", ".bin")).To(Equal("model_2.bin"))
		})

		It("advances the counter past every existing collision", func() {
			tmpDir := GinkgoT().TempDir()
			for _, name := range []string{"model.bin", "model_2.bin", "model_3.bin"} {
				Expect(os.WriteFile(filepath.Join(tmpDir, name), nil, 0o600)).To(Succeed())
			}

			Expect(GenerateUniqueFileName(tmpDir, "model", ".bin")).To(Equal("model_4.bin"))
		})

		It("preserves an empty extension when generating the suffixed name", func() {
			tmpDir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(tmpDir, "README"), nil, 0o600)).To(Succeed())

			Expect(GenerateUniqueFileName(tmpDir, "README", "")).To(Equal("README_2"))
		})
	})
})
