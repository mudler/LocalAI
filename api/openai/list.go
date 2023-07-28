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

		skipBinIfModelConfigured := false
		var filterFn func(name string) bool
		filter := c.Query("filter")
		if filter == "" {
			// If filter param isn't specified at all, take our best guess at presenting a useful list to our users.
			// 1) Remove any raw model files from the list that are already configured
			skipBinIfModelConfigured = true
			// 2) Don't filter out anything else
			filterFn = func(_ string) bool { return true }
		} else {
			// If filter _IS_ specified, we should use it to create the filterFn
			rxp, err := regexp.Compile(filter)
			if err != nil {
				return err
			}
			filterFn = func(name string) bool {
				return rxp.MatchString(name)
			}
		}

		for _, c := range cm.GetAllConfigs() {
			if skipBinIfModelConfigured {
				mm[c.Model] = nil
			}
			if filterFn(c.Name) {
				dataModels = append(dataModels, OpenAIModel{ID: c.Name, Object: "model"})
			}
		}

		for _, m := range models {
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
