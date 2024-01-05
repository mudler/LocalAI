package assets

import (
	"embed"
	"io/fs"
)

func ListFiles(content embed.FS) (files []string) {
	fs.WalkDir(content, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		files = append(files, path)
		return nil
	})
	return
}
