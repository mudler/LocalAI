package openai

import (
	"encoding/json"
	"fmt"

	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

func EditEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		modelFile, input, err := readInput(c, o, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		config, input, err := readConfig(modelFile, input, cm, o.Loader, o.Debug, o.Threads, o.ContextSize, o.F16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("Parameter Config: %+v", config)

		templateFile := config.Model

		if config.TemplateConfig.Edit != "" {
			templateFile = config.TemplateConfig.Edit
		}

		var result []Choice
		for _, i := range config.InputStrings {
			// A model can have a "file.bin.tmpl" file associated with a prompt template prefix
			templatedInput, err := o.Loader.EvaluateTemplateForPrompt(model.EditPromptTemplate, templateFile, model.PromptTemplateData{
				Input:       i,
				Instruction: input.Instruction,
			})
			if err == nil {
				i = templatedInput
				log.Debug().Msgf("Template found, input modified to: %s", i)
			}

			r, err := ComputeChoices(input, i, config, o, o.Loader, func(s string, c *[]Choice) {
				*c = append(*c, Choice{Text: s})
			}, nil)
			if err != nil {
				return err
			}

			result = append(result, r...)
		}

		resp := &OpenAIResponse{
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: result,
			Object:  "edit",
		}

		jsonResult, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}
