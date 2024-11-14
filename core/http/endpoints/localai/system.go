package localai

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
)

// SystemInformations returns the system informations
// @Summary Show the LocalAI instance information
// @Success 200 {object} schema.SystemInformationResponse "Response"
// @Router /system [get]
func SystemInformations(ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(*fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		availableBackends, err := ml.ListAvailableBackends(appConfig.AssetsDestination)
		if err != nil {
			return err
		}
		loadedModels := ml.ListModels()
		for b := range appConfig.ExternalGRPCBackends {
			availableBackends = append(availableBackends, b)
		}

		sysmodels := []schema.SysInfoModel{}
		for _, m := range loadedModels {
			sysmodels = append(sysmodels, schema.SysInfoModel{ID: m.ID})
		}
		return c.JSON(
			schema.SystemInformationResponse{
				Backends: availableBackends,
				Models:   sysmodels,
			},
		)
	}
}
