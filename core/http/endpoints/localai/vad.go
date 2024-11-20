package localai

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	fiberContext "github.com/mudler/LocalAI/core/http/ctx"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

// VADEndpoint is Voice-Activation-Detection endpoint
// @Summary	Detect voice fragments in an audio stream
// @Accept json
// @Param		request	body		schema.VADRequest	true	"query params"
// @Success 200 {object} proto.VADResponse "Response"
// @Router		/vad [post]
func VADEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input := new(schema.VADRequest)

		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		modelFile, err := fiberContext.ModelFromContext(c, cl, ml, input.Model, false)
		if err != nil {
			modelFile = input.Model
			log.Warn().Msgf("Model not found in context: %s", input.Model)
		}

		cfg, err := cl.LoadBackendConfigFileByName(modelFile, appConfig.ModelPath,
			config.LoadOptionDebug(appConfig.Debug),
			config.LoadOptionThreads(appConfig.Threads),
			config.LoadOptionContextSize(appConfig.ContextSize),
			config.LoadOptionF16(appConfig.F16),
		)

		if err != nil {
			log.Err(err)
			modelFile = input.Model
			log.Warn().Msgf("Model not found in context: %s", input.Model)
		} else {
			modelFile = cfg.Model
		}
		log.Debug().Msgf("Request for model: %s", modelFile)

		opts := backend.ModelOptions(*cfg, appConfig, model.WithBackendString(cfg.Backend), model.WithModel(modelFile))

		vadModel, err := ml.Load(opts...)
		if err != nil {
			return err
		}
		req := proto.VADRequest{
			Audio: input.Audio,
		}
		resp, err := vadModel.VAD(c.Context(), &req)
		if err != nil {
			return err
		}

		return c.JSON(resp)
	}
}
