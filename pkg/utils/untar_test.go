package utils_test

import (
	"archive/tar"
	"archive/zip"
	"os"
	"path/filepath"

	. "github.com/mudler/LocalAI/pkg/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("utils/archive tests", func() {
	It("extracts regular nested zip members", func() {
		tmpDir := GinkgoT().TempDir()
		archivePath := filepath.Join(tmpDir, "model.zip")
		extractPath := filepath.Join(tmpDir, "models")

		Expect(writeZipArchive(archivePath, map[string]string{
			"nested/model.yaml": "name: test",
		})).To(Succeed())

		Expect(ExtractArchive(archivePath, extractPath)).To(Succeed())

		extracted, err := os.ReadFile(filepath.Join(extractPath, "nested", "model.yaml"))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(extracted)).To(Equal("name: test"))
	})

	It("rejects zip members that escape the destination", func() {
		tmpDir := GinkgoT().TempDir()
		archivePath := filepath.Join(tmpDir, "model.zip")
		extractPath := filepath.Join(tmpDir, "models")

		Expect(writeZipArchive(archivePath, map[string]string{
			"../escaped.txt": "escaped",
		})).To(Succeed())

		err := ExtractArchive(archivePath, extractPath)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsafe path"))
		Expect(filepath.Join(tmpDir, "escaped.txt")).ToNot(BeAnExistingFile())
	})

	It("rejects tar members that escape the destination", func() {
		tmpDir := GinkgoT().TempDir()
		archivePath := filepath.Join(tmpDir, "model.tar")
		extractPath := filepath.Join(tmpDir, "models")

		Expect(writeTarArchive(archivePath, map[string]string{
			"../escaped.txt": "escaped",
		})).To(Succeed())

		err := ExtractArchive(archivePath, extractPath)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsafe path"))
		Expect(filepath.Join(tmpDir, "escaped.txt")).ToNot(BeAnExistingFile())
	})
})

func writeZipArchive(path string, files map[string]string) (err error) {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
	}()

	writer := zip.NewWriter(out)
	defer func() {
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
	}()

	for name, contents := range files {
		fileWriter, err := writer.Create(name)
		if err != nil {
			return err
		}
		if _, err := fileWriter.Write([]byte(contents)); err != nil {
			return err
		}
	}

	return nil
}

func writeTarArchive(path string, files map[string]string) (err error) {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
	}()

	writer := tar.NewWriter(out)
	defer func() {
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
	}()

	for name, contents := range files {
		data := []byte(contents)
		if err := writer.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o600,
			Size: int64(len(data)),
		}); err != nil {
			return err
		}
		if _, err := writer.Write(data); err != nil {
			return err
		}
	}

	return nil
}
