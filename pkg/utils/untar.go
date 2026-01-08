package utils

import (
	"fmt"
	"os"

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

	err = archiver.Walk(archive, func(f archiver.File) error {
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
