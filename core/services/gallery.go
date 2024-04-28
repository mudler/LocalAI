package services

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/go-skynet/LocalAI/pkg/startup"
	"github.com/go-skynet/LocalAI/pkg/utils"
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

func prepareModel(modelPath string, req gallery.GalleryModel, cl *config.BackendConfigLoader, downloadStatus func(string, string, string, float64)) error {

	config, err := gallery.GetGalleryConfigFromURL(req.URL)
	if err != nil {
		return err
	}

	config.Files = append(config.Files, req.AdditionalFiles...)

	return gallery.InstallModel(modelPath, req.Name, &config, req.Overrides, downloadStatus)
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

				// delete a model
				if op.Delete {
					modelConfig := &config.BackendConfig{}
					// Galleryname is the name of the model in this case
					dat, err := os.ReadFile(filepath.Join(g.modelPath, op.GalleryName+".yaml"))
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

					err = gallery.DeleteModelFromSystem(g.modelPath, op.GalleryName, files)
				} else {
					// if the request contains a gallery name, we apply the gallery from the gallery list
					if op.GalleryName != "" {
						if strings.Contains(op.GalleryName, "@") {
							err = gallery.InstallModelFromGallery(op.Galleries, op.GalleryName, g.modelPath, op.Req, progressCallback)
						} else {
							err = gallery.InstallModelFromGalleryByName(op.Galleries, op.GalleryName, g.modelPath, op.Req, progressCallback)
						}
					} else if op.ConfigURL != "" {
						startup.PreloadModelsConfigurations(op.ConfigURL, g.modelPath, op.ConfigURL)
						err = cl.Preload(g.modelPath)
					} else {
						err = prepareModel(g.modelPath, op.Req, cl, progressCallback)
					}
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

				g.UpdateStatus(op.Id,
					&gallery.GalleryOpStatus{
						Deletion:  op.Delete,
						Processed: true,
						Message:   "completed",
						Progress:  100})
			}
		}
	}()
}

type galleryModel struct {
	gallery.GalleryModel `yaml:",inline"` // https://github.com/go-yaml/yaml/issues/63
	ID                   string           `json:"id"`
}

func processRequests(modelPath, s string, cm *config.BackendConfigLoader, galleries []gallery.Gallery, requests []galleryModel) error {
	var err error
	for _, r := range requests {
		utils.ResetDownloadTimers()
		if r.ID == "" {
			err = prepareModel(modelPath, r.GalleryModel, cm, utils.DisplayDownloadFunction)

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

	return processRequests(modelPath, s, cl, galleries, requests)
}

func ApplyGalleryFromString(modelPath, s string, cl *config.BackendConfigLoader, galleries []gallery.Gallery) error {
	var requests []galleryModel
	err := json.Unmarshal([]byte(s), &requests)
	if err != nil {
		return err
	}

	return processRequests(modelPath, s, cl, galleries, requests)
}
