// SPDX-License-Identifier: MIT
package packages

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/zipformat"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// rawEntry is one entry of a hand-built zip.
type rawEntry struct {
	name string
	data []byte
	mode fs.FileMode
}

// rawZip writes a zip whose digest manifest lists the files as given, with no
// check, so a test can make every kind of hostile or damaged file.
func rawZip(zm zipformat.Manifest, entries ...rawEntry) []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	raw, err := json.Marshal(zm)
	Expect(err).NotTo(HaveOccurred())
	all := append([]rawEntry{{name: zipformat.ManifestName, data: raw, mode: 0o644}}, entries...)
	for _, e := range all {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		h.SetMode(e.mode)
		w, err := zw.CreateHeader(h)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Write(e.data)
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(zw.Close()).To(Succeed())
	return b.Bytes()
}

func listed(entries ...rawEntry) []zipformat.File {
	var out []zipformat.File
	for _, e := range entries {
		if e.mode.IsRegular() {
			s := sha256.Sum256(e.data)
			out = append(out, zipformat.File{Path: e.name, SHA256: hex.EncodeToString(s[:]), Size: int64(len(e.data))})
		}
	}
	return out
}

func writeZip(b []byte) string {
	p := filepath.Join(GinkgoT().TempDir(), "x.zip")
	Expect(os.WriteFile(p, b, 0o600)).To(Succeed())
	return p
}

var _ = Describe("local file packages", func() {
	var manifest []byte
	var good []rawEntry
	var inst *Installer
	meta := func(files ...zipformat.File) zipformat.Manifest {
		return zipformat.Manifest{Schema: zipformat.Schema, ID: "sample", Version: "1.2.3", OS: "linux", Arch: "amd64", Executable: "bin/sample", Files: files}
	}
	// goodZip is a zip of the sample fixture, extra entries added to both the
	// zip and the digest manifest.
	goodZip := func(extra ...rawEntry) []byte {
		entries := append(append([]rawEntry{}, good...), extra...)
		return rawZip(meta(listed(entries...)...), entries...)
	}
	BeforeEach(func() {
		var e error
		manifest, e = os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		var m contract.Manifest
		Expect(json.Unmarshal(manifest, &m)).To(Succeed())
		good = []rawEntry{{"manifest.json", manifest, 0o644}, {"bin/sample", []byte("executable"), 0o755}}
		root := GinkgoT().TempDir()
		DeferCleanup(func() {
			_ = filepath.Walk(root, func(p string, info os.FileInfo, e error) error {
				if e == nil && info.IsDir() {
					_ = os.Chmod(p, 0o700)
				}
				return nil
			})
		})
		inst = &Installer{Root: root, Host: contract.HostCompatibility{Host: 1, API: contract.HostAPIVersion, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}}
	})
	refused := func(b []byte, code string) {
		GinkgoHelper()
		_, e := inst.StageFile(context.Background(), writeZip(b), "x.zip", nil)
		Expect(e).To(HaveOccurred())
		Expect(errors.Is(e, ErrPackage)).To(BeTrue())
		var fe *FileError
		Expect(errors.As(e, &fe)).To(BeTrue(), "error %v", e)
		Expect(fe.Code).To(Equal(code))
		// A refused file leaves no stage behind.
		ents, _ := os.ReadDir(inst.Root)
		Expect(ents).To(BeEmpty())
	}

	It("stages a zip like a library package and records that it is unsigned", func() {
		p := writeZip(goodZip())
		s, e := inst.StageFile(context.Background(), p, "../my pkg.zip", nil)
		Expect(e).NotTo(HaveOccurred())
		rc := s.Receipt()
		raw, _ := os.ReadFile(p)
		sum := sha256.Sum256(raw)
		Expect(rc.Digest).To(Equal("sha256:" + hex.EncodeToString(sum[:])))
		Expect(rc.SourceID).To(Equal("local-file"))
		Expect(rc.Verification).To(Equal("sha256"))
		Expect(rc.Origin).To(Equal("local file, unsigned"))
		Expect(rc.Reference).To(Equal("file:my_pkg.zip"))
		Expect(rc.Manifest.ID).To(Equal("sample"))
		out, e := inst.Commit(context.Background(), s)
		Expect(e).NotTo(HaveOccurred())
		info, e := os.Stat(filepath.Join(out.Directory, "bin/sample"))
		Expect(e).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o500)))
		Expect(filepath.Join(out.Directory, zipformat.ManifestName)).NotTo(BeAnExistingFile())
		Expect(filepath.Join(out.Directory, "receipt.json")).To(BeAnExistingFile())
	})

	It("describes a zip without writing it", func() {
		p := writeZip(goodZip())
		sum, e := InspectFile(context.Background(), p, 0, 0)
		Expect(e).NotTo(HaveOccurred())
		Expect(sum.ID).To(Equal("sample"))
		Expect(sum.Version).To(Equal("1.2.3"))
		Expect(sum.Files).To(Equal(2))
		Expect(sum.SHA256).To(HaveLen(64))
	})

	DescribeTable("refuses an unsafe path",
		func(name string) {
			refused(goodZip(rawEntry{name, []byte("x"), 0o644}), FileUnsafePath)
		},
		Entry("parent directory", "../evil"),
		Entry("nested parent directory", "a/../../evil"),
		Entry("absolute path", "/etc/evil"),
		Entry("backslash", "a\\evil"),
		Entry("drive letter", "C:evil"),
		Entry("current directory prefix", "./evil"),
		Entry("double slash", "a//evil"),
		Entry("NUL byte", "evil\x00.txt"),
	)

	It("refuses a path that repeats, also when the case differs", func() {
		refused(goodZip(rawEntry{"ui/A.js", []byte("a"), 0o644}, rawEntry{"ui/a.js", []byte("b"), 0o644}), FileUnsafePath)
	})

	It("refuses a symbolic link", func() {
		refused(goodZip(rawEntry{"ui/link", []byte("/etc/passwd"), fs.ModeSymlink | 0o777}), FileUnsupportedType)
	})

	It("refuses an executable bit on a file that is not the executable", func() {
		refused(goodZip(rawEntry{"tool", []byte("x"), 0o755}), FileBadMode)
	})

	It("refuses a zip whose digest list does not match", func() {
		entries := append([]rawEntry{}, good...)
		z := meta(listed(entries...)...)
		entries[1].data = []byte("tampered!!")
		refused(rawZip(z, entries...), FileDigestMismatch)
	})

	It("refuses a missing and an extra file", func() {
		listedAll := listed(append(append([]rawEntry{}, good...), rawEntry{"ui/x.js", []byte("x"), 0o644})...)
		refused(rawZip(meta(listedAll...), good...), FileDigestMismatch)
		refused(rawZip(meta(listed(good...)...), append(append([]rawEntry{}, good...), rawEntry{"ui/x.js", []byte("x"), 0o644})...), FileDigestMismatch)
	})

	It("refuses a size in the list that the file does not have", func() {
		files := listed(good...)
		files[0].Size++
		refused(rawZip(meta(files...), good...), FileDigestMismatch)
	})

	It("refuses a zip for another platform before it writes anything", func() {
		z := meta(listed(good...)...)
		z.Arch = "arm64"
		_, e := inst.StageFile(context.Background(), writeZip(rawZip(z, good...)), "x.zip", nil)
		var fe *FileError
		Expect(errors.As(e, &fe)).To(BeTrue())
		Expect(fe.Code).To(Equal(FileWrongPlatform))
		Expect(fe.Msg).To(ContainSubstring("linux/arm64"))
	})

	It("refuses a manifest.json that disagrees with the digest manifest", func() {
		z := meta(listed(good...)...)
		z.Version = "9.9.9"
		refused(rawZip(z, good...), FileBadManifest)
	})

	It("refuses a file that is not a zip, and a zip without a digest manifest", func() {
		refused([]byte("not a zip"), FileBadZip)
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		w, _ := zw.Create("manifest.json")
		_, _ = w.Write(manifest)
		Expect(zw.Close()).To(Succeed())
		refused(b.Bytes(), FileBadManifest)
	})

	It("refuses a zip above the size limit", func() {
		inst.MaxBytes = 600
		raw := goodZip(rawEntry{"ui/noise.bin", randomBytes(2048), 0o644})
		refused(raw, FileTooLarge)
	})

	It("refuses files that together expand past the limit", func() {
		inst.MaxBytes = 4096
		a := rawEntry{"ui/a.bin", bytes.Repeat([]byte("a"), 3000), 0o644}
		b := rawEntry{"ui/b.bin", bytes.Repeat([]byte("b"), 3000), 0o644}
		refused(goodZip(a, b), FileTooLarge)
	})

	It("refuses a file that compresses like a bomb", func() {
		zeros := rawEntry{"ui/zeros.bin", make([]byte, 8<<20), 0o644}
		refused(goodZip(zeros), FileRatio)
	})

	It("refuses more files than the limit", func() {
		inst.MaxFiles = 3
		var extra []rawEntry
		for _, n := range []string{"ui/a", "ui/b", "ui/c"} {
			extra = append(extra, rawEntry{n, []byte(n), 0o644})
		}
		refused(goodZip(extra...), FileTooManyFiles)
	})

	It("refuses a path that is a file in one entry and a directory in another", func() {
		_, e := inst.StageFile(context.Background(), writeZip(goodZip(rawEntry{"ui", []byte("x"), 0o644}, rawEntry{"ui/a.js", []byte("x"), 0o644})), "x.zip", nil)
		Expect(e).To(HaveOccurred())
		ents, _ := os.ReadDir(inst.Root)
		Expect(ents).To(BeEmpty())
	})

	It("does not read a path through a symbolic link planted in the staging area", func() {
		p := filepath.Join(GinkgoT().TempDir(), "link.zip")
		target := writeZip(goodZip())
		Expect(os.Symlink(target, p)).To(Succeed())
		_, e := inst.StageFile(context.Background(), p, "x.zip", nil)
		Expect(e).To(HaveOccurred())
		Expect(strings.Contains(e.Error(), "regular")).To(BeTrue())
	})
})

func randomBytes(n int) []byte {
	b := make([]byte, n)
	var x uint32 = 2463534242
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x)
	}
	return b
}
