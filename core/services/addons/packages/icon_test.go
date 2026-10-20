// SPDX-License-Identifier: MIT
package packages

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// png is the smallest byte string with the PNG signature; the host checks the
// signature of the packaged bytes, not whether the image decodes.
func png(extra int) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, extra)...)
}

// archiveFiles builds a package archive with real file contents.
func archiveFiles(manifest []byte, files map[string][]byte, headers ...*tar.Header) []byte {
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	write := func(name string, data []byte, mode int64) {
		Expect(w.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data))})).To(Succeed())
		_, e := w.Write(data)
		Expect(e).NotTo(HaveOccurred())
	}
	write("manifest.json", manifest, 0644)
	write("bin/sample", []byte("executable"), 0755)
	for name, data := range files {
		write(name, data, 0644)
	}
	for _, h := range headers {
		Expect(w.WriteHeader(h)).To(Succeed())
	}
	Expect(w.Close()).To(Succeed())
	return b.Bytes()
}

var _ = Describe("package icons", func() {
	manifest := func(icon string) []byte {
		b, e := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		var m contract.Manifest
		Expect(json.Unmarshal(b, &m)).To(Succeed())
		m.Capabilities.UI = &contract.UI{Entry: "ui/main.js", SDK: contract.VersionRange{Min: 1, Max: 1}, Pages: []string{"home"}, MainPage: "home", Icon: icon, Title: &contract.Localized{Text: "Sample"}}
		b, e = json.Marshal(m)
		Expect(e).NotTo(HaveOccurred())
		return b
	}
	stage := func(icon string, files map[string][]byte, headers ...*tar.Header) (PackageReceipt, error) {
		i, r := fixture(archiveFiles(manifest(icon), files, headers...))
		s, e := i.Stage(context.Background(), r, nil)
		if e != nil {
			return PackageReceipt{}, e
		}
		return i.Commit(context.Background(), s)
	}
	It("records the digest of a packaged immutable icon in the receipt and refuses unusable icons", func() {
		bytesOf := png(100)
		receipt, e := stage("ui/icon.png", map[string][]byte{"ui/main.js": []byte("export {}"), "ui/icon.png": bytesOf})
		Expect(e).NotTo(HaveOccurred())
		sum := sha256.Sum256(bytesOf)
		Expect(receipt.Icon).To(Equal(&PackageIcon{Path: "ui/icon.png", Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(bytesOf)), MediaType: "image/png"}))
		// The committed receipt on disk carries the same record.
		var disk PackageReceipt
		b, e := os.ReadFile(filepath.Join(receipt.Directory, "receipt.json"))
		Expect(e).NotTo(HaveOccurred())
		Expect(json.Unmarshal(b, &disk)).To(Succeed())
		Expect(disk.Icon).To(Equal(receipt.Icon))
		info, e := os.Lstat(filepath.Join(receipt.Directory, "ui/icon.png"))
		Expect(e).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0400)), "the icon is read-only after commit")

		// No icon declared: no record.
		receipt, e = stage("", map[string][]byte{"ui/main.js": []byte("export {}")})
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.Icon).To(BeNil())

		js := map[string][]byte{"ui/main.js": []byte("export {}")}
		with := func(name string, data []byte) map[string][]byte {
			return map[string][]byte{"ui/main.js": js["ui/main.js"], name: data}
		}
		// Declared but not packaged.
		_, e = stage("ui/icon.png", js)
		Expect(e).To(HaveOccurred(), "missing icon")
		// A directory, a link and an empty file are not icons.
		_, e = stage("ui/icon.png", js, &tar.Header{Name: "ui/icon.png", Typeflag: tar.TypeDir, Mode: 0755})
		Expect(e).To(HaveOccurred(), "directory")
		_, e = stage("ui/icon.png", js, &tar.Header{Name: "ui/icon.png", Typeflag: tar.TypeSymlink, Linkname: "main.js", Mode: 0777})
		Expect(e).To(HaveOccurred(), "link")
		_, e = stage("ui/icon.png", with("ui/icon.png", nil))
		Expect(e).To(HaveOccurred(), "empty")
		// The bytes must be the declared type.
		_, e = stage("ui/icon.png", with("ui/icon.png", []byte("<html><script>alert(1)</script></html>")))
		Expect(e).To(HaveOccurred(), "wrong signature")
		_, e = stage("ui/icon.webp", with("ui/icon.webp", png(10)))
		Expect(e).To(HaveOccurred(), "png bytes named webp")
		// The size bound is exact.
		_, e = stage("ui/icon.png", with("ui/icon.png", png(contract.MaxIconBytes-8)))
		Expect(e).NotTo(HaveOccurred(), "an icon of exactly the bound is accepted")
		_, e = stage("ui/icon.png", with("ui/icon.png", png(contract.MaxIconBytes-8+1)))
		Expect(e).To(HaveOccurred(), "an icon one byte over the bound is refused")
	})
})
