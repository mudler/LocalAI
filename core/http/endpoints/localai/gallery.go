package localai

import (
	"encoding/json"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/utils"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/rs/zerolog/log"
)

type ModelGalleryEndpointService struct {
	galleries        []config.Gallery
	backendGalleries []config.Gallery
	modelPath        string
	galleryApplier   *services.GalleryService
}

type GalleryModel struct {
	ID string `json:"id"`
	gallery.GalleryModel
}

func CreateModelGalleryEndpointService(galleries []config.Gallery, backendGalleries []config.Gallery, systemState *system.SystemState, galleryApplier *services.GalleryService) ModelGalleryEndpointService {
	return ModelGalleryEndpointService{
		galleries:        galleries,
		backendGalleries: backendGalleries,
		modelPath:        systemState.Model.ModelsPath,
		galleryApplier:   galleryApplier,
	}
}

// GetOpStatusEndpoint returns the job status
// @Summary Returns the job status
// @Success 200 {object} services.GalleryOpStatus "Response"
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
// @Success 200 {object} map[string]services.GalleryOpStatus "Response"
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
		mgs.galleryApplier.ModelGalleryChannel <- services.GalleryOp[gallery.GalleryModel]{
			Req:                input.GalleryModel,
			ID:                 uuid.String(),
			GalleryElementName: input.ID,
			Galleries:          mgs.galleries,
			BackendGalleries:   mgs.backendGalleries,
		}

		return c.JSON(schema.GalleryResponse{ID: uuid.String(), StatusURL: fmt.Sprintf("%smodels/jobs/%s", utils.BaseURL(c), uuid.String())})
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

		mgs.galleryApplier.ModelGalleryChannel <- services.GalleryOp[gallery.GalleryModel]{
			Delete:             true,
			GalleryElementName: modelName,
		}

		uuid, err := uuid.NewUUID()
		if err != nil {
			return err
		}

		return c.JSON(schema.GalleryResponse{ID: uuid.String(), StatusURL: fmt.Sprintf("%smodels/jobs/%s", utils.BaseURL(c), uuid.String())})
	}
}

// ListModelFromGalleryEndpoint list the available models for installation from the active galleries
// @Summary List installable models.
// @Success 200 {object} []gallery.GalleryModel "Response"
// @Router /models/available [get]
func (mgs *ModelGalleryEndpointService) ListModelFromGalleryEndpoint(systemState *system.SystemState) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		models, err := gallery.AvailableGalleryModels(mgs.galleries, systemState)
		if err != nil {
			log.Error().Err(err).Msg("could not list models from galleries")
			return err
		}

		log.Debug().Msgf("Available %d models from %d galleries\n", len(models), len(mgs.galleries))

		m := []gallery.Metadata{}

		for _, mm := range models {
			m = append(m, mm.Metadata)
		}

		log.Debug().Msgf("Models %#v", m)

		dat, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("could not marshal models: %w", err)
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
