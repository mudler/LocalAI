package api

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	json "github.com/json-iterator/go"

	"github.com/go-skynet/LocalAI/pkg/gallery"
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

func newGalleryApplier(modelPath string) *galleryApplier {
	return &galleryApplier{
		modelPath: modelPath,
		C:         make(chan galleryOp),
		statuses:  make(map[string]*galleryOpStatus),
	}
}

// prepareModel applies a
func prepareModel(modelPath string, req gallery.GalleryModel, cm *ConfigMerger, downloadStatus func(string, string, string, float64)) error {

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

func (g *galleryApplier) start(c context.Context, cm *ConfigMerger) {
	go func() {
		for {
			select {
			case <-c.Done():
				return
			case op := <-g.C:
				g.updateStatus(op.id, &galleryOpStatus{Message: "processing", Progress: 0})

				// updates the status with an error
				updateError := func(e error) {
					g.updateStatus(op.id, &galleryOpStatus{Error: e, Processed: true, Message: "error: " + e.Error()})
				}

				// displayDownload displays the download progress
				progressCallback := func(fileName string, current string, total string, percentage float64) {
					g.updateStatus(op.id, &galleryOpStatus{Message: "processing", Progress: percentage, TotalFileSize: total, DownloadedFileSize: current})
					displayDownload(fileName, current, total, percentage)
				}

				var err error
				// if the request contains a gallery name, we apply the gallery from the gallery list
				if op.galleryName != "" {
					err = gallery.InstallModelFromGallery(op.galleries, op.galleryName, g.modelPath, op.req, progressCallback)
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

var lastProgress time.Time = time.Now()
var startTime time.Time = time.Now()

func displayDownload(fileName string, current string, total string, percentage float64) {
	currentTime := time.Now()

	if currentTime.Sub(lastProgress) >= 5*time.Second {

		lastProgress = currentTime

		// calculate ETA based on percentage and elapsed time
		var eta time.Duration
		if percentage > 0 {
			elapsed := currentTime.Sub(startTime)
			eta = time.Duration(float64(elapsed)*(100/percentage) - float64(elapsed))
		}

		if total != "" {
			log.Debug().Msgf("Downloading %s: %s/%s (%.2f%%) ETA: %s", fileName, current, total, percentage, eta)
		} else {
			log.Debug().Msgf("Downloading: %s", current)
		}
	}
}

type galleryModel struct {
	gallery.GalleryModel
	ID string `json:"id"`
}

func ApplyGalleryFromFile(modelPath, s string, cm *ConfigMerger, galleries []gallery.Gallery) error {
	dat, err := os.ReadFile(s)
	if err != nil {
		return err
	}
	return ApplyGalleryFromString(modelPath, string(dat), cm, galleries)
}

func ApplyGalleryFromString(modelPath, s string, cm *ConfigMerger, galleries []gallery.Gallery) error {
	var requests []galleryModel
	err := json.Unmarshal([]byte(s), &requests)
	if err != nil {
		return err
	}

	for _, r := range requests {
		if r.ID == "" {
			err = prepareModel(modelPath, r.GalleryModel, cm, displayDownload)
		} else {
			err = gallery.InstallModelFromGallery(galleries, r.ID, modelPath, r.GalleryModel, displayDownload)
		}
	}

	return err
}

func getOpStatus(g *galleryApplier) func(c *fiber.Ctx) error {
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

func applyModelGallery(modelPath string, cm *ConfigMerger, g chan galleryOp, galleries []gallery.Gallery) func(c *fiber.Ctx) error {
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

func listModelFromGallery(galleries []gallery.Gallery, basePath string) func(c *fiber.Ctx) error {
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
