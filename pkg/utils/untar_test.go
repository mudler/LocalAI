package utils_test

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
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

	It("rejects tar hardlinks that overwrite a file outside the destination", func() {
		tmpDir := GinkgoT().TempDir()
		archivePath := filepath.Join(tmpDir, "model.tar.gz")
		extractPath := filepath.Join(tmpDir, "models")
		outsidePath := filepath.Join(tmpDir, "outside.txt")

		Expect(os.WriteFile(outsidePath, []byte("original"), 0o600)).To(Succeed())
		Expect(writeTarGzArchiveWithHardlinkedFile(archivePath, "payload.bin", "../outside.txt", "overwritten")).To(Succeed())

		err := ExtractArchive(archivePath, extractPath)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsafe path"))

		contents, readErr := os.ReadFile(outsidePath)
		Expect(readErr).ToNot(HaveOccurred())
		Expect(string(contents)).To(Equal("original"))
	})

	It("extracts tar hardlinks that stay inside the destination", func() {
		tmpDir := GinkgoT().TempDir()
		archivePath := filepath.Join(tmpDir, "model.tar.gz")
		extractPath := filepath.Join(tmpDir, "models")

		Expect(writeTarGzArchiveWithInternalHardlink(archivePath, "model.bin", "alias.bin", "weights")).To(Succeed())

		Expect(ExtractArchive(archivePath, extractPath)).To(Succeed())

		extracted, err := os.ReadFile(filepath.Join(extractPath, "alias.bin"))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(extracted)).To(Equal("weights"))
	})

	It("rejects tar hardlinks that point outside the destination", func() {
		tmpDir := GinkgoT().TempDir()
		archivePath := filepath.Join(tmpDir, "model.tar")
		extractPath := filepath.Join(tmpDir, "models")

		Expect(writeTarArchiveWithHardlink(archivePath, "payload.bin", "../escaped.txt")).To(Succeed())

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

func writeTarArchiveWithHardlink(path, name, linkname string) (err error) {
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

	return writer.WriteHeader(&tar.Header{
		Name:     name,
		Linkname: linkname,
		Typeflag: tar.TypeLink,
		Mode:     0o600,
	})
}

func writeTarGzArchiveWithHardlinkedFile(path, name, linkname, contents string) (err error) {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
	}()

	compressor := gzip.NewWriter(out)
	defer func() {
		if closeErr := compressor.Close(); err == nil {
			err = closeErr
		}
	}()

	writer := tar.NewWriter(compressor)
	defer func() {
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
	}()

	if err := writer.WriteHeader(&tar.Header{
		Name:     name,
		Linkname: linkname,
		Typeflag: tar.TypeLink,
		Mode:     0o600,
	}); err != nil {
		return err
	}

	data := []byte(contents)
	if err := writer.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0o600,
		Size: int64(len(data)),
	}); err != nil {
		return err
	}
	_, err = writer.Write(data)

	return err
}

func writeTarGzArchiveWithInternalHardlink(path, targetName, linkName, contents string) (err error) {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
	}()

	compressor := gzip.NewWriter(out)
	defer func() {
		if closeErr := compressor.Close(); err == nil {
			err = closeErr
		}
	}()

	writer := tar.NewWriter(compressor)
	defer func() {
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
	}()

	data := []byte(contents)
	if err := writer.WriteHeader(&tar.Header{
		Name: targetName,
		Mode: 0o600,
		Size: int64(len(data)),
	}); err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		return err
	}

	return writer.WriteHeader(&tar.Header{
		Name:     linkName,
		Linkname: targetName,
		Typeflag: tar.TypeLink,
		Mode:     0o600,
	})
}
