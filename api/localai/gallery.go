package localai

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	json "github.com/json-iterator/go"
	"gopkg.in/yaml.v3"

	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/go-skynet/LocalAI/pkg/utils"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type galleryOp struct {
	req         gallery.GalleryModel
	id          string
	galleries   []gallery.Gallery
	galleryName string
}

type galleryOpStatus struct {
	Error              error   `json:"error"`
	Processed          bool    `json:"processed"`
	Message            string  `json:"message"`
	Progress           float64 `json:"progress"`
	TotalFileSize      string  `json:"file_size"`
	DownloadedFileSize string  `json:"downloaded_size"`
}

type galleryApplier struct {
	modelPath string
	sync.Mutex
	C        chan galleryOp
	statuses map[string]*galleryOpStatus
}

func NewGalleryService(modelPath string) *galleryApplier {
	return &galleryApplier{
		modelPath: modelPath,
		C:         make(chan galleryOp),
		statuses:  make(map[string]*galleryOpStatus),
	}
}

// prepareModel applies a
func prepareModel(modelPath string, req gallery.GalleryModel, cm *config.ConfigLoader, downloadStatus func(string, string, string, float64)) error {

	config, err := gallery.GetGalleryConfigFromURL(req.URL)
	if err != nil {
		return err
	}

	config.Files = append(config.Files, req.AdditionalFiles...)

	return gallery.InstallModel(modelPath, req.Name, &config, req.Overrides, downloadStatus)
}

func (g *galleryApplier) updateStatus(s string, op *galleryOpStatus) {
	g.Lock()
	defer g.Unlock()
	g.statuses[s] = op
}

func (g *galleryApplier) getStatus(s string) *galleryOpStatus {
	g.Lock()
	defer g.Unlock()

	return g.statuses[s]
}

func (g *galleryApplier) Start(c context.Context, cm *config.ConfigLoader) {
	go func() {
		for {
			select {
			case <-c.Done():
				return
			case op := <-g.C:
				utils.ResetDownloadTimers()

				g.updateStatus(op.id, &galleryOpStatus{Message: "processing", Progress: 0})

				// updates the status with an error
				updateError := func(e error) {
					g.updateStatus(op.id, &galleryOpStatus{Error: e, Processed: true, Message: "error: " + e.Error()})
				}

				// displayDownload displays the download progress
				progressCallback := func(fileName string, current string, total string, percentage float64) {
					g.updateStatus(op.id, &galleryOpStatus{Message: "processing", Progress: percentage, TotalFileSize: total, DownloadedFileSize: current})
					utils.DisplayDownloadFunction(fileName, current, total, percentage)
				}

				var err error
				// if the request contains a gallery name, we apply the gallery from the gallery list
				if op.galleryName != "" {
					if strings.Contains(op.galleryName, "@") {
						err = gallery.InstallModelFromGallery(op.galleries, op.galleryName, g.modelPath, op.req, progressCallback)
					} else {
						err = gallery.InstallModelFromGalleryByName(op.galleries, op.galleryName, g.modelPath, op.req, progressCallback)
					}
				} else {
					err = prepareModel(g.modelPath, op.req, cm, progressCallback)
				}

				if err != nil {
					updateError(err)
					continue
				}

				// Reload models
				err = cm.LoadConfigs(g.modelPath)
				if err != nil {
					updateError(err)
					continue
				}

				g.updateStatus(op.id, &galleryOpStatus{Processed: true, Message: "completed", Progress: 100})
			}
		}
	}()
}

type galleryModel struct {
	gallery.GalleryModel `yaml:",inline"` // https://github.com/go-yaml/yaml/issues/63
	ID                   string           `json:"id"`
}

func processRequests(modelPath, s string, cm *config.ConfigLoader, galleries []gallery.Gallery, requests []galleryModel) error {
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

func ApplyGalleryFromFile(modelPath, s string, cm *config.ConfigLoader, galleries []gallery.Gallery) error {
	dat, err := os.ReadFile(s)
	if err != nil {
		return err
	}
	var requests []galleryModel

	if err := yaml.Unmarshal(dat, &requests); err != nil {
		return err
	}

	return processRequests(modelPath, s, cm, galleries, requests)
}

func ApplyGalleryFromString(modelPath, s string, cm *config.ConfigLoader, galleries []gallery.Gallery) error {
	var requests []galleryModel
	err := json.Unmarshal([]byte(s), &requests)
	if err != nil {
		return err
	}

	return processRequests(modelPath, s, cm, galleries, requests)
}

/// Endpoints

func GetOpStatusEndpoint(g *galleryApplier) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		status := g.getStatus(c.Params("uuid"))
		if status == nil {
			return fmt.Errorf("could not find any status for ID")
		}

		return c.JSON(status)
	}
}

type GalleryModel struct {
	ID string `json:"id"`
	gallery.GalleryModel
}

func ApplyModelGalleryEndpoint(modelPath string, cm *config.ConfigLoader, g chan galleryOp, galleries []gallery.Gallery) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		input := new(GalleryModel)
		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		uuid, err := uuid.NewUUID()
		if err != nil {
			return err
		}
		g <- galleryOp{
			req:         input.GalleryModel,
			id:          uuid.String(),
			galleryName: input.ID,
			galleries:   galleries,
		}
		return c.JSON(struct {
			ID        string `json:"uuid"`
			StatusURL string `json:"status"`
		}{ID: uuid.String(), StatusURL: c.BaseURL() + "/models/jobs/" + uuid.String()})
	}
}

func ListModelFromGalleryEndpoint(galleries []gallery.Gallery, basePath string) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		log.Debug().Msgf("Listing models from galleries: %+v", galleries)

		models, err := gallery.AvailableGalleryModels(galleries, basePath)
		if err != nil {
			return err
		}
		log.Debug().Msgf("Models found from galleries: %+v", models)
		for _, m := range models {
			log.Debug().Msgf("Model found from galleries: %+v", m)
		}
		dat, err := json.Marshal(models)
		if err != nil {
			return err
		}
		return c.Send(dat)
	}
}
