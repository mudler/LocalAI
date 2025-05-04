package assets

import (
	"os"

	rice "github.com/GeertJohan/go.rice"
	"github.com/rs/zerolog/log"
)

func ListFiles(content *rice.Box) (files []string) {
	err := content.Walk("", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		files = append(files, path)
		return nil
	})
	if err != nil {
		log.Error().Err(err).Msg("error walking the rice box")
	}
	return
}
