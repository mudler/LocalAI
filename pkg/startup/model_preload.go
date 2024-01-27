package startup

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/go-skynet/LocalAI/embedded"
	"github.com/go-skynet/LocalAI/pkg/downloader"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
)

// PreloadModelsConfigurations will preload models from the given list of URLs
// It will download the model if it is not already present in the model path
// It will also try to resolve if the model is an embedded model YAML configuration
func PreloadModelsConfigurations(modelLibraryURL string, modelPath string, models ...string) {
	for _, url := range models {

		// As a best effort, try to resolve the model from the remote library
		// if it's not resolved we try with the other method below
		if modelLibraryURL != "" {
			lib, err := embedded.GetRemoteLibraryShorteners(modelLibraryURL)
			if err == nil {
				if lib[url] != "" {
					log.Debug().Msgf("[startup] model configuration is defined remotely: %s (%s)", url, lib[url])
					url = lib[url]
				}
			}
		}

		url = embedded.ModelShortURL(url)
		switch {
		case embedded.ExistsInModelsLibrary(url):
			modelYAML, err := embedded.ResolveContent(url)
			// If we resolve something, just save it to disk and continue
			if err != nil {
				log.Error().Msgf("error loading model: %s", err.Error())
				continue
			}

			log.Debug().Msgf("[startup] resolved embedded model: %s", url)
			md5Name := utils.MD5(url)
			if err := os.WriteFile(filepath.Join(modelPath, md5Name)+".yaml", modelYAML, os.ModePerm); err != nil {
				log.Error().Msgf("error loading model: %s", err.Error())
			}
		case downloader.LooksLikeURL(url):
			log.Debug().Msgf("[startup] resolved model to download: %s", url)

			// md5 of model name
			md5Name := utils.MD5(url)

			// check if file exists
			if _, err := os.Stat(filepath.Join(modelPath, md5Name)); errors.Is(err, os.ErrNotExist) {
				err := downloader.DownloadFile(url, filepath.Join(modelPath, md5Name)+".yaml", "", func(fileName, current, total string, percent float64) {
					utils.DisplayDownloadFunction(fileName, current, total, percent)
				})
				if err != nil {
					log.Error().Msgf("error loading model: %s", err.Error())
				}
			}
		default:
			log.Warn().Msgf("[startup] failed resolving model '%s'", url)
		}
	}
}
