package localai

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
	"github.com/rs/zerolog/log"
)

type ModelGalleryEndpointService struct {
	galleries      []config.Gallery
	modelPath      string
	galleryApplier *services.GalleryService
}

type GalleryModel struct {
	ID        string `json:"id"`
	ConfigURL string `json:"config_url"`
	gallery.GalleryModel
}

func CreateModelGalleryEndpointService(galleries []config.Gallery, modelPath string, galleryApplier *services.GalleryService) ModelGalleryEndpointService {
	return ModelGalleryEndpointService{
		galleries:      galleries,
		modelPath:      modelPath,
		galleryApplier: galleryApplier,
	}
}

// GetOpStatusEndpoint returns the job status
// @Summary Returns the job status
// @Success 200 {object} gallery.GalleryOpStatus "Response"
// @Router /models/jobs/{uuid} [get]
func (mgs *ModelGalleryEndpointService) GetOpStatusEndpoint() func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		status := mgs.galleryApplier.GetStatus(c.Params("uuid"))
		if status == nil {
			return fmt.Errorf("could not find any status for ID")
		}
		return c.JSON(status)
	}
}

// GetAllStatusEndpoint returns all the jobs status progress
// @Summary Returns all the jobs status progress
// @Success 200 {object} map[string]gallery.GalleryOpStatus "Response"
// @Router /models/jobs [get]
func (mgs *ModelGalleryEndpointService) GetAllStatusEndpoint() func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		return c.JSON(mgs.galleryApplier.GetAllStatus())
	}
}

// ApplyModelGalleryEndpoint installs a new model to a LocalAI instance from the model gallery
// @Summary Install models to LocalAI.
// @Param request body GalleryModel true "query params"
// @Success 200 {object} schema.GalleryResponse "Response"
// @Router /models/apply [post]
func (mgs *ModelGalleryEndpointService) ApplyModelGalleryEndpoint() func(c *fiber.Ctx) error {
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
		mgs.galleryApplier.C <- gallery.GalleryOp{
			Req:              input.GalleryModel,
			Id:               uuid.String(),
			GalleryModelName: input.ID,
			Galleries:        mgs.galleries,
			ConfigURL:        input.ConfigURL,
		}
		return c.JSON(schema.GalleryResponse{ID: uuid.String(), StatusURL: c.BaseURL() + "/models/jobs/" + uuid.String()})
	}
}

// DeleteModelGalleryEndpoint lets delete models from a LocalAI instance
// @Summary delete models to LocalAI.
// @Param name	path string	true	"Model name"
// @Success 200 {object} schema.GalleryResponse "Response"
// @Router /models/delete/{name} [post]
func (mgs *ModelGalleryEndpointService) DeleteModelGalleryEndpoint() func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		modelName := c.Params("name")

		mgs.galleryApplier.C <- gallery.GalleryOp{
			Delete:           true,
			GalleryModelName: modelName,
		}

		uuid, err := uuid.NewUUID()
		if err != nil {
			return err
		}

		return c.JSON(schema.GalleryResponse{ID: uuid.String(), StatusURL: c.BaseURL() + "/models/jobs/" + uuid.String()})
	}
}

// ListModelFromGalleryEndpoint list the available models for installation from the active galleries
// @Summary List installable models.
// @Success 200 {object} []gallery.GalleryModel "Response"
// @Router /models/available [get]
func (mgs *ModelGalleryEndpointService) ListModelFromGalleryEndpoint() func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		log.Debug().Msgf("Listing models from galleries: %+v", mgs.galleries)

		models, err := gallery.AvailableGalleryModels(mgs.galleries, mgs.modelPath)
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

// ListModelGalleriesEndpoint list the available galleries configured in LocalAI
// @Summary List all Galleries
// @Success 200 {object} []config.Gallery "Response"
// @Router /models/galleries [get]
// NOTE: This is different (and much simpler!) than above! This JUST lists the model galleries that have been loaded, not their contents!
func (mgs *ModelGalleryEndpointService) ListModelGalleriesEndpoint() func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		log.Debug().Msgf("Listing model galleries %+v", mgs.galleries)
		dat, err := json.Marshal(mgs.galleries)
		if err != nil {
			return err
		}
		return c.Send(dat)
	}
}

// AddModelGalleryEndpoint adds a gallery in LocalAI
// @Summary Adds a gallery in LocalAI
// @Param request body config.Gallery true "Gallery details"
// @Success 200 {object} []config.Gallery "Response"
// @Router /models/galleries [post]
func (mgs *ModelGalleryEndpointService) AddModelGalleryEndpoint() func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		input := new(config.Gallery)
		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}
		if slices.ContainsFunc(mgs.galleries, func(gallery config.Gallery) bool {
			return gallery.Name == input.Name
		}) {
			return fmt.Errorf("%s already exists", input.Name)
		}
		dat, err := json.Marshal(mgs.galleries)
		if err != nil {
			return err
		}
		log.Debug().Msgf("Adding %+v to gallery list", *input)
		mgs.galleries = append(mgs.galleries, *input)
		return c.Send(dat)
	}
}

// RemoveModelGalleryEndpoint remove a gallery in LocalAI
// @Summary removes a gallery from LocalAI
// @Param request body config.Gallery true "Gallery details"
// @Success 200 {object} []config.Gallery "Response"
// @Router /models/galleries [delete]
func (mgs *ModelGalleryEndpointService) RemoveModelGalleryEndpoint() func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		input := new(config.Gallery)
		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}
		if !slices.ContainsFunc(mgs.galleries, func(gallery config.Gallery) bool {
			return gallery.Name == input.Name
		}) {
			return fmt.Errorf("%s is not currently registered", input.Name)
		}
		mgs.galleries = slices.DeleteFunc(mgs.galleries, func(gallery config.Gallery) bool {
			return gallery.Name == input.Name
		})
		dat, err := json.Marshal(mgs.galleries)
		if err != nil {
			return err
		}
		return c.Send(dat)
	}
}
