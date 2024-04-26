package assets

import (
	"embed"
	"io/fs"

	"github.com/rs/zerolog/log"
)

func ListFiles(content embed.FS) (files []string) {
	err := fs.WalkDir(content, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		files = append(files, path)
		return nil
	})
	if err != nil {
		log.Error().Err(err).Msg("error walking the embedded filesystem")
	}
	return
}
