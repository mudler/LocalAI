package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/startup"
	"github.com/mudler/LocalAI/pkg/utils"
	"gopkg.in/yaml.v2"
)

type GalleryService struct {
	appConfig *config.ApplicationConfig
	sync.Mutex
	C        chan gallery.GalleryOp
	statuses map[string]*gallery.GalleryOpStatus
}

func NewGalleryService(appConfig *config.ApplicationConfig) *GalleryService {
	return &GalleryService{
		appConfig: appConfig,
		C:         make(chan gallery.GalleryOp),
		statuses:  make(map[string]*gallery.GalleryOpStatus),
	}
}

func prepareModel(modelPath string, req gallery.GalleryModel, downloadStatus func(string, string, string, float64), enforceScan bool) error {

	config, err := gallery.GetGalleryConfigFromURL(req.URL, modelPath)
	if err != nil {
		return err
	}

	config.Files = append(config.Files, req.AdditionalFiles...)

	return gallery.InstallModel(modelPath, req.Name, &config, req.Overrides, downloadStatus, enforceScan)
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
				var updateError func(e error)
				if !g.appConfig.OpaqueErrors {
					updateError = func(e error) {
						g.UpdateStatus(op.Id, &gallery.GalleryOpStatus{Error: e, Processed: true, Message: "error: " + e.Error()})
					}
				} else {
					updateError = func(_ error) {
						g.UpdateStatus(op.Id, &gallery.GalleryOpStatus{Error: fmt.Errorf("an error occurred"), Processed: true})
					}
				}

				// displayDownload displays the download progress
				progressCallback := func(fileName string, current string, total string, percentage float64) {
					g.UpdateStatus(op.Id, &gallery.GalleryOpStatus{Message: "processing", FileName: fileName, Progress: percentage, TotalFileSize: total, DownloadedFileSize: current})
					utils.DisplayDownloadFunction(fileName, current, total, percentage)
				}

				var err error

				// delete a model
				if op.Delete {
					modelConfig := &config.BackendConfig{}

					// Galleryname is the name of the model in this case
					dat, err := os.ReadFile(filepath.Join(g.appConfig.ModelPath, op.GalleryModelName+".yaml"))
					if err != nil {
						updateError(err)
						continue
					}
					err = yaml.Unmarshal(dat, modelConfig)
					if err != nil {
						updateError(err)
						continue
					}

					files := []string{}
					// Remove the model from the config
					if modelConfig.Model != "" {
						files = append(files, modelConfig.ModelFileName())
					}

					if modelConfig.MMProj != "" {
						files = append(files, modelConfig.MMProjFileName())
					}

					err = gallery.DeleteModelFromSystem(g.appConfig.ModelPath, op.GalleryModelName, files)
					if err != nil {
						updateError(err)
						continue
					}
				} else {
					// if the request contains a gallery name, we apply the gallery from the gallery list
					if op.GalleryModelName != "" {
						err = gallery.InstallModelFromGallery(op.Galleries, op.GalleryModelName, g.appConfig.ModelPath, op.Req, progressCallback, g.appConfig.EnforcePredownloadScans)
					} else if op.ConfigURL != "" {
						err = startup.InstallModels(op.Galleries, op.ConfigURL, g.appConfig.ModelPath, g.appConfig.EnforcePredownloadScans, progressCallback, op.ConfigURL)
						if err != nil {
							updateError(err)
							continue
						}
						err = cl.Preload(g.appConfig.ModelPath)
					} else {
						err = prepareModel(g.appConfig.ModelPath, op.Req, progressCallback, g.appConfig.EnforcePredownloadScans)
					}
				}

				if err != nil {
					updateError(err)
					continue
				}

				// Reload models
				err = cl.LoadBackendConfigsFromPath(g.appConfig.ModelPath)
				if err != nil {
					updateError(err)
					continue
				}

				err = cl.Preload(g.appConfig.ModelPath)
				if err != nil {
					updateError(err)
					continue
				}

				g.UpdateStatus(op.Id,
					&gallery.GalleryOpStatus{
						Deletion:         op.Delete,
						Processed:        true,
						GalleryModelName: op.GalleryModelName,
						Message:          "completed",
						Progress:         100})
			}
		}
	}()
}

type galleryModel struct {
	gallery.GalleryModel `yaml:",inline"` // https://github.com/go-yaml/yaml/issues/63
	ID                   string           `json:"id"`
}

func processRequests(modelPath string, enforceScan bool, galleries []config.Gallery, requests []galleryModel) error {
	var err error
	for _, r := range requests {
		utils.ResetDownloadTimers()
		if r.ID == "" {
			err = prepareModel(modelPath, r.GalleryModel, utils.DisplayDownloadFunction, enforceScan)

		} else {
			err = gallery.InstallModelFromGallery(
				galleries, r.ID, modelPath, r.GalleryModel, utils.DisplayDownloadFunction, enforceScan)
		}
	}
	return err
}

func ApplyGalleryFromFile(modelPath, s string, enforceScan bool, galleries []config.Gallery) error {
	dat, err := os.ReadFile(s)
	if err != nil {
		return err
	}
	var requests []galleryModel

	if err := yaml.Unmarshal(dat, &requests); err != nil {
		return err
	}

	return processRequests(modelPath, enforceScan, galleries, requests)
}

func ApplyGalleryFromString(modelPath, s string, enforceScan bool, galleries []config.Gallery) error {
	var requests []galleryModel
	err := json.Unmarshal([]byte(s), &requests)
	if err != nil {
		return err
	}

	return processRequests(modelPath, enforceScan, galleries, requests)
}
