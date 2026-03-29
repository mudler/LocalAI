package openai

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/galleryop"
	model "github.com/mudler/LocalAI/pkg/model"
	"gorm.io/gorm"
)

// ListModelsEndpoint is the OpenAI Models API endpoint https://platform.openai.com/docs/api-reference/models
// @Summary List and describe the various models available in the API.
// @Success 200 {object} schema.ModelsDataResponse "Response"
// @Router /v1/models [get]
func ListModelsEndpoint(bcl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, db ...*gorm.DB) echo.HandlerFunc {
	var authDB *gorm.DB
	if len(db) > 0 {
		authDB = db[0]
	}
	return func(c echo.Context) error {
		// If blank, no filter is applied.
		filter := c.QueryParam("filter")

		// By default, exclude any loose files that are already referenced by a configuration file.
		var policy galleryop.LooseFilePolicy
		excludeConfigured := c.QueryParam("excludeConfigured")
		if excludeConfigured == "" || excludeConfigured == "true" {
			policy = galleryop.SKIP_IF_CONFIGURED
		} else {
			policy = galleryop.ALWAYS_INCLUDE // This replicates current behavior. TODO: give more options to the user?
		}

		filterFn, err := config.BuildNameFilterFn(filter)
		if err != nil {
			return err
		}

		modelNames, err := galleryop.ListModels(bcl, ml, filterFn, policy)
		if err != nil {
			return err
		}

		// Filter models by user's allowlist if auth is enabled
		if authDB != nil {
			if user := auth.GetUser(c); user != nil && user.Role != auth.RoleAdmin {
				perm, err := auth.GetCachedUserPermissions(c, authDB, user.ID)
				if err == nil && perm.AllowedModels.Enabled {
					allowed := map[string]bool{}
					for _, m := range perm.AllowedModels.Models {
						allowed[m] = true
					}
					filtered := make([]string, 0, len(modelNames))
					for _, m := range modelNames {
						if allowed[m] {
							filtered = append(filtered, m)
						}
					}
					modelNames = filtered
				}
			}
		}

		// Map from a slice of names to a slice of OpenAIModel response objects
		dataModels := []schema.OpenAIModel{}
		for _, m := range modelNames {
			dataModels = append(dataModels, schema.OpenAIModel{ID: m, Object: "model"})
		}

		return c.JSON(200, schema.ModelsDataResponse{
			Object: "list",
			Data:   dataModels,
		})
	}
}
