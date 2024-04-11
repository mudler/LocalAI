package services

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/embedded"
	"github.com/go-skynet/LocalAI/pkg/downloader"
	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)

type GalleryService struct {
	modelPath string
	sync.Mutex
	C        chan gallery.GalleryOp
	statuses map[string]*gallery.GalleryOpStatus
}

func NewGalleryService(modelPath string) *GalleryService {
	return &GalleryService{
		modelPath: modelPath,
		C:         make(chan gallery.GalleryOp),
		statuses:  make(map[string]*gallery.GalleryOpStatus),
	}
}

func (g *GalleryService) UpdateStatus(s string, op *gallery.GalleryOpStatus) {
	g.Lock()
	defer g.Unlock()
	g.statuses[s] = op
}

func (g *GalleryService) GetStatus(s string) *gallery.GalleryOpStatus {
	g.Lock()
	defer g.Unlock()

	return g.statuses[s]
}

func (g *GalleryService) GetAllStatus() map[string]*gallery.GalleryOpStatus {
	g.Lock()
	defer g.Unlock()

	return g.statuses
}

func (g *GalleryService) Start(c context.Context, cl *config.BackendConfigLoader) {
	go func() {
		for {
			select {
			case <-c.Done():
				return
			case op := <-g.C:
				utils.ResetDownloadTimers()

				g.UpdateStatus(op.Id, &gallery.GalleryOpStatus{Message: "processing", Progress: 0})

				// updates the status with an error
				updateError := func(e error) {
					g.UpdateStatus(op.Id, &gallery.GalleryOpStatus{Error: e, Processed: true, Message: "error: " + e.Error()})
				}

				// displayDownload displays the download progress
				progressCallback := func(fileName string, current string, total string, percentage float64) {
					g.UpdateStatus(op.Id, &gallery.GalleryOpStatus{Message: "processing", FileName: fileName, Progress: percentage, TotalFileSize: total, DownloadedFileSize: current})
					utils.DisplayDownloadFunction(fileName, current, total, percentage)
				}

				var err error
				// if the request contains a gallery name, we apply the gallery from the gallery list
				if op.GalleryName != "" {
					if strings.Contains(op.GalleryName, "@") {
						err = gallery.InstallModelFromGallery(op.Galleries, op.GalleryName, g.modelPath, op.Req, progressCallback)
					} else {
						err = gallery.InstallModelFromGalleryByName(op.Galleries, op.GalleryName, g.modelPath, op.Req, progressCallback)
					}
				} else if op.ConfigURL != "" {
					PreloadModelsConfigurations(op.ConfigURL, g.modelPath, op.ConfigURL)
					err = cl.Preload(g.modelPath)
				} else {
					err = prepareModel(g.modelPath, op.Req, progressCallback)
				}

				if err != nil {
					updateError(err)
					continue
				}

				// Reload models
				err = cl.LoadBackendConfigsFromPath(g.modelPath)
				if err != nil {
					updateError(err)
					continue
				}

				err = cl.Preload(g.modelPath)
				if err != nil {
					updateError(err)
					continue
				}

				g.UpdateStatus(op.Id, &gallery.GalleryOpStatus{Processed: true, Message: "completed", Progress: 100})
			}
		}
	}()
}

type galleryModel struct {
	gallery.GalleryModel `yaml:",inline"` // https://github.com/go-yaml/yaml/issues/63
	ID                   string           `json:"id"`
}

func processRequests(modelPath string, galleries []gallery.Gallery, requests []galleryModel) error {
	var err error
	for _, r := range requests {
		utils.ResetDownloadTimers()
		if r.ID == "" {
			err = prepareModel(modelPath, r.GalleryModel, utils.DisplayDownloadFunction)
		} else {
			if strings.Contains(r.ID, "@") {
				err = gallery.InstallModelFromGallery(
					galleries, r.ID, modelPath, r.GalleryModel, utils.DisplayDownloadFunction)
			} else {
				err = gallery.InstallModelFromGalleryByName(
					galleries, r.ID, modelPath, r.GalleryModel, utils.DisplayDownloadFunction)
			}
		}
	}
	return err
}

func ApplyGalleryFromFile(modelPath, s string, cl *config.BackendConfigLoader, galleries []gallery.Gallery) error {
	dat, err := os.ReadFile(s)
	if err != nil {
		return err
	}
	var requests []galleryModel

	if err := yaml.Unmarshal(dat, &requests); err != nil {
		return err
	}

	return processRequests(modelPath, galleries, requests)
}

func ApplyGalleryFromString(modelPath, s string, cl *config.BackendConfigLoader, galleries []gallery.Gallery) error {
	var requests []galleryModel
	err := json.Unmarshal([]byte(s), &requests)
	if err != nil {
		return err
	}

	return processRequests(modelPath, galleries, requests)
}

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
				log.Error().Err(err).Msg("error resolving model content")
				continue
			}

			log.Debug().Msgf("[startup] resolved embedded model: %s", url)
			md5Name := utils.MD5(url)
			modelDefinitionFilePath := filepath.Join(modelPath, md5Name) + ".yaml"
			if err := os.WriteFile(modelDefinitionFilePath, modelYAML, os.ModePerm); err != nil {
				log.Error().Err(err).Str("filepath", modelDefinitionFilePath).Msg("error writing model definition")
			}
		case downloader.LooksLikeURL(url):
			log.Debug().Msgf("[startup] resolved model to download: %s", url)

			// md5 of model name
			md5Name := utils.MD5(url)

			// check if file exists
			if _, err := os.Stat(filepath.Join(modelPath, md5Name)); errors.Is(err, os.ErrNotExist) {
				modelDefinitionFilePath := filepath.Join(modelPath, md5Name) + ".yaml"
				err := downloader.DownloadFile(url, modelDefinitionFilePath, "", func(fileName, current, total string, percent float64) {
					utils.DisplayDownloadFunction(fileName, current, total, percent)
				})
				if err != nil {
					log.Error().Err(err).Str("url", url).Str("filepath", modelDefinitionFilePath).Msg("error downloading model")
				}
			}
		default:
			if _, err := os.Stat(url); err == nil {
				log.Debug().Msgf("[startup] resolved local model: %s", url)
				// copy to modelPath
				md5Name := utils.MD5(url)

				modelYAML, err := os.ReadFile(url)
				if err != nil {
					log.Error().Err(err).Str("filepath", url).Msg("error reading model definition")
					continue
				}

				modelDefinitionFilePath := filepath.Join(modelPath, md5Name) + ".yaml"
				if err := os.WriteFile(modelDefinitionFilePath, modelYAML, os.ModePerm); err != nil {
					log.Error().Err(err).Str("filepath", modelDefinitionFilePath).Msg("error loading model: %s")
				}
			} else {
				log.Warn().Msgf("[startup] failed resolving model '%s'", url)
			}
		}
	}
}

func prepareModel(modelPath string, req gallery.GalleryModel, downloadStatus func(string, string, string, float64)) error {

	config, err := gallery.GetGalleryConfigFromURL(req.URL)
	if err != nil {
		return err
	}

	config.Files = append(config.Files, req.AdditionalFiles...)

	return gallery.InstallModel(modelPath, req.Name, &config, req.Overrides, downloadStatus)
}
