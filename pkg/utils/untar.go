package utils

import (
	"archive/tar"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zip"
	"github.com/mholt/archiver/v3"
)

func IsArchive(file string) bool {
	uaIface, err := archiver.ByExtension(file)
	if err != nil {
		return false
	}

	_, ok := uaIface.(archiver.Unarchiver)
	return ok
}

func ExtractArchive(archive, dst string) error {
	uaIface, err := archiver.ByExtension(archive)
	if err != nil {
		return err
	}

	un, ok := uaIface.(archiver.Unarchiver)
	if !ok {
		return fmt.Errorf("format specified by source filename is not an archive format: %s (%T)", archive, uaIface)
	}

	mytar := &archiver.Tar{
		OverwriteExisting:      true,
		MkdirAll:               true,
		ImplicitTopLevelFolder: false,
		ContinueOnError:        true,
	}

	switch v := uaIface.(type) {
	case *archiver.Tar:
		uaIface = mytar
	case *archiver.TarBrotli:
		v.Tar = mytar
	case *archiver.TarBz2:
		v.Tar = mytar
	case *archiver.TarGz:
		v.Tar = mytar
	case *archiver.TarLz4:
		v.Tar = mytar
	case *archiver.TarSz:
		v.Tar = mytar
	case *archiver.TarXz:
		v.Tar = mytar
	case *archiver.TarZstd:
		v.Tar = mytar
	}

	extractRoot, err := filepath.Abs(dst)
	if err != nil {
		return err
	}

	err = archiver.Walk(archive, func(f archiver.File) error {
		if err := validateArchiveMemberPath(extractRoot, archiveMemberName(f)); err != nil {
			return err
		}
		if f.FileInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive contains a symlink")
		}
		return nil
	})

	if err != nil {
		return err
	}

	return un.Unarchive(archive, dst)
}

func archiveMemberName(f archiver.File) string {
	switch h := f.Header.(type) {
	case tar.Header:
		return h.Name
	case *tar.Header:
		return h.Name
	case zip.FileHeader:
		return h.Name
	case *zip.FileHeader:
		return h.Name
	default:
		return f.Name()
	}
}

func validateArchiveMemberPath(root, name string) error {
	if name == "" {
		return fmt.Errorf("archive contains an empty path")
	}

	normalizedName := filepath.FromSlash(strings.ReplaceAll(name, "\\", "/"))
	cleanedName := filepath.Clean(normalizedName)
	if filepath.IsAbs(cleanedName) || cleanedName == ".." || strings.HasPrefix(cleanedName, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("archive contains an unsafe path: %s", name)
	}

	targetPath := filepath.Join(root, cleanedName)
	relativePath, err := filepath.Rel(root, targetPath)
	if err != nil {
		return err
	}
	if relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) || filepath.IsAbs(relativePath) {
		return fmt.Errorf("archive contains an unsafe path: %s", name)
	}

	return nil
}
