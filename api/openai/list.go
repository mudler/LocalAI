package openai

import (
	"regexp"

	config "github.com/go-skynet/LocalAI/api/config"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
)

func ListModelsEndpoint(loader *model.ModelLoader, cm *config.ConfigLoader) func(ctx *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		models, err := loader.ListModels()
		if err != nil {
			return err
		}
		var mm map[string]interface{} = map[string]interface{}{}

		dataModels := []OpenAIModel{}

		var filterFn func(name string) bool
		filter := c.Query("filter")

		// If filter is not specified, do not filter the list by model name
		if filter == "" {
			filterFn = func(_ string) bool { return true }
		} else {
			// If filter _IS_ specified, we compile it to a regex which is used to create the filterFn
			rxp, err := regexp.Compile(filter)
			if err != nil {
				return err
			}
			filterFn = func(name string) bool {
				return rxp.MatchString(name)
			}
		}

		// By default, exclude any loose files that are already referenced by a configuration file.
		includeConfigured := c.QueryBool("includeConfigured", false)

		// Start with the known configurations
		for _, c := range cm.GetAllConfigs() {
			if includeConfigured {
				mm[c.Model] = nil
			}

			if filterFn(c.Name) {
				dataModels = append(dataModels, OpenAIModel{ID: c.Name, Object: "model"})
			}
		}

		// Then iterate through the loose files:
		for _, m := range models {
			// And only adds them if they shouldn't be skipped.
			if _, exists := mm[m]; !exists && filterFn(m) {
				dataModels = append(dataModels, OpenAIModel{ID: m, Object: "model"})
			}
		}

		return c.JSON(struct {
			Object string        `json:"object"`
			Data   []OpenAIModel `json:"data"`
		}{
			Object: "list",
			Data:   dataModels,
		})
	}
}
