package localai

import (
	"encoding/json"
	"fmt"

	"github.com/labstack/echo/v4"
	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/rs/zerolog/log"
)

type BackendEndpointService struct {
	galleries         []config.Gallery
	backendPath       string
	backendSystemPath string
	backendApplier    *services.GalleryService
}

type GalleryBackend struct {
	ID string `json:"id"`
}

func CreateBackendEndpointService(galleries []config.Gallery, systemState *system.SystemState, backendApplier *services.GalleryService) BackendEndpointService {
	return BackendEndpointService{
		galleries:         galleries,
		backendPath:       systemState.Backend.BackendsPath,
		backendSystemPath: systemState.Backend.BackendsSystemPath,
		backendApplier:    backendApplier,
	}
}

// GetOpStatusEndpoint returns the job status
// @Summary Returns the job status
// @Success 200 {object} services.GalleryOpStatus "Response"
// @Router /backends/jobs/{uuid} [get]
func (mgs *BackendEndpointService) GetOpStatusEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		status := mgs.backendApplier.GetStatus(c.Param("uuid"))
		if status == nil {
			return fmt.Errorf("could not find any status for ID")
		}
		return c.JSON(200, status)
	}
}

// GetAllStatusEndpoint returns all the jobs status progress
// @Summary Returns all the jobs status progress
// @Success 200 {object} map[string]services.GalleryOpStatus "Response"
// @Router /backends/jobs [get]
func (mgs *BackendEndpointService) GetAllStatusEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(200, mgs.backendApplier.GetAllStatus())
	}
}

// ApplyBackendEndpoint installs a new backend to a LocalAI instance
// @Summary Install backends to LocalAI.
// @Param request body GalleryBackend true "query params"
// @Success 200 {object} schema.BackendResponse "Response"
// @Router /backends/apply [post]
func (mgs *BackendEndpointService) ApplyBackendEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		input := new(GalleryBackend)
		// Get input data from the request body
		if err := c.Bind(input); err != nil {
			return err
		}

		uuid, err := uuid.NewUUID()
		if err != nil {
			return err
		}
		mgs.backendApplier.BackendGalleryChannel <- services.GalleryOp[gallery.GalleryBackend, any]{
			ID:                 uuid.String(),
			GalleryElementName: input.ID,
			Galleries:          mgs.galleries,
		}

		return c.JSON(200, schema.BackendResponse{ID: uuid.String(), StatusURL: fmt.Sprintf("%sbackends/jobs/%s", middleware.BaseURL(c), uuid.String())})
	}
}

// DeleteBackendEndpoint lets delete backends from a LocalAI instance
// @Summary delete backends from LocalAI.
// @Param name	path string	true	"Backend name"
// @Success 200 {object} schema.BackendResponse "Response"
// @Router /backends/delete/{name} [post]
func (mgs *BackendEndpointService) DeleteBackendEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		backendName := c.Param("name")

		mgs.backendApplier.BackendGalleryChannel <- services.GalleryOp[gallery.GalleryBackend, any]{
			Delete:             true,
			GalleryElementName: backendName,
			Galleries:          mgs.galleries,
		}

		uuid, err := uuid.NewUUID()
		if err != nil {
			return err
		}

		return c.JSON(200, schema.BackendResponse{ID: uuid.String(), StatusURL: fmt.Sprintf("%sbackends/jobs/%s", middleware.BaseURL(c), uuid.String())})
	}
}

// ListBackendsEndpoint list the available backends configured in LocalAI
// @Summary List all Backends
// @Success 200 {object} []gallery.GalleryBackend "Response"
// @Router /backends [get]
func (mgs *BackendEndpointService) ListBackendsEndpoint(systemState *system.SystemState) echo.HandlerFunc {
	return func(c echo.Context) error {
		backends, err := gallery.ListSystemBackends(systemState)
		if err != nil {
			return err
		}
		return c.JSON(200, backends.GetAll())
	}
}

// ListModelGalleriesEndpoint list the available galleries configured in LocalAI
// @Summary List all Galleries
// @Success 200 {object} []config.Gallery "Response"
// @Router /backends/galleries [get]
// NOTE: This is different (and much simpler!) than above! This JUST lists the model galleries that have been loaded, not their contents!
func (mgs *BackendEndpointService) ListBackendGalleriesEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		log.Debug().Msgf("Listing backend galleries %+v", mgs.galleries)
		dat, err := json.Marshal(mgs.galleries)
		if err != nil {
			return err
		}
		return c.Blob(200, "application/json", dat)
	}
}

// ListAvailableBackendsEndpoint list the available backends in the galleries configured in LocalAI
// @Summary List all available Backends
// @Success 200 {object} []gallery.GalleryBackend "Response"
// @Router /backends/available [get]
func (mgs *BackendEndpointService) ListAvailableBackendsEndpoint(systemState *system.SystemState) echo.HandlerFunc {
	return func(c echo.Context) error {
		backends, err := gallery.AvailableBackends(mgs.galleries, systemState)
		if err != nil {
			return err
		}
		return c.JSON(200, backends)
	}
}
