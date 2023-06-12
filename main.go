package main

import (
	"fmt"
	"os"
	"path/filepath"

	api "github.com/go-skynet/LocalAI/api"
	apiv2 "github.com/go-skynet/LocalAI/apiv2"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	path, err := os.Getwd()
	if err != nil {
		log.Error().Msgf("error: %s", err.Error())
		os.Exit(1)
	}

	log.Log().Msgf("STARTING!")

	app := &cli.App{
		Name:  "LocalAI",
		Usage: "OpenAI compatible API for running LLaMA/GPT models locally on CPU with consumer grade hardware.",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "f16",
				EnvVars: []string{"F16"},
			},
			&cli.BoolFlag{
				Name:    "debug",
				EnvVars: []string{"DEBUG"},
			},
			&cli.BoolFlag{
				Name:    "cors",
				EnvVars: []string{"CORS"},
			},
			&cli.StringFlag{
				Name:        "models-path",
				DefaultText: "Path containing models used for inferencing",
				EnvVars:     []string{"MODELS_PATH"},
				Value:       filepath.Join(path, "models"),
			},
			&cli.StringFlag{
				Name:        "template-path",
				DefaultText: "Path containing templates used for inferencing",
				EnvVars:     []string{"TEMPLATES_PATH"},
				Value:       filepath.Join(path, "templates"),
			},
			&cli.StringFlag{
				Name:        "config-path",
				DefaultText: "Path containing model/endpoint configurations",
				EnvVars:     []string{"CONFIG_PATH"},
				Value:       filepath.Join(path, "config"),
			},
			&cli.StringFlag{
				Name:        "config-file",
				DefaultText: "Config file",
				EnvVars:     []string{"CONFIG_FILE"},
			},
			&cli.StringFlag{
				Name:        "address",
				DefaultText: "Bind address for the API server.",
				EnvVars:     []string{"ADDRESS"},
				Value:       ":8080",
			},
			// TODO: Cleanup before merge.
			// Intentionally leaving this behind. It's useful to spin up localai_nethttp if needed.
			// &cli.StringFlag{
			// 	Name:        "addressv2",
			// 	DefaultText: "Bind address for the API server (DEBUG v2 TEST)",
			// 	EnvVars:     []string{"ADDRESS_V2"},
			// 	Value:       ":8085",
			// },
			&cli.BoolFlag{
				Name:        "exp-v2",
				DefaultText: "Enable the experimental v2 API server",
				EnvVars:     []string{"EXPERIMENTAL_V2"},
				Value:       true,
			},
			&cli.StringFlag{
				Name:        "image-path",
				DefaultText: "Image directory",
				EnvVars:     []string{"IMAGE_PATH"},
				Value:       "",
			},
			&cli.StringFlag{
				Name:    "cors-allow-origins",
				EnvVars: []string{"CORS_ALLOW_ORIGINS"},
			},
			&cli.IntFlag{
				Name:    "threads",
				Usage:   "Number of threads used for parallel computation. Usage of the number of physical cores in the system is suggested.",
				EnvVars: []string{"THREADS"},
				Value:   4,
			},
			&cli.StringFlag{
				Name:    "preload-models",
				Usage:   "A List of models to apply in JSON at start",
				EnvVars: []string{"PRELOAD_MODELS"},
			},
			&cli.StringFlag{
				Name:    "preload-models-config",
				Usage:   "A List of models to apply at startup. Path to a YAML config file",
				EnvVars: []string{"PRELOAD_MODELS_CONFIG"},
			},
			&cli.StringFlag{
				Name:    "backend-assets-path",
				Usage:   "Path used to extract libraries that are required by some of the backends in runtime.",
				EnvVars: []string{"BACKEND_ASSETS_PATH"},
				Value:   "/tmp/localai/backend_data",
			},
			&cli.IntFlag{
				Name:    "context-size",
				Usage:   "Default context size of the model",
				EnvVars: []string{"CONTEXT_SIZE"},
				Value:   512,
			},
			&cli.IntFlag{
				Name:    "upload-limit",
				Usage:   "Default upload-limit. MB",
				EnvVars: []string{"UPLOAD_LIMIT"},
				Value:   15,
			},
		},
		Description: `
LocalAI is a drop-in replacement OpenAI API which runs inference locally.

Some of the models compatible are:
- Vicuna
- Koala
- GPT4ALL
- GPT4ALL-J
- Cerebras
- Alpaca
- StableLM (ggml quantized)

It uses llama.cpp, ggml and gpt4all as backend with golang c bindings.
`,
		UsageText: `local-ai [options]`,
		Copyright: "go-skynet authors",
		Action: func(ctx *cli.Context) error {
			fmt.Printf("Starting LocalAI using %d threads, with models path: %s\n", ctx.Int("threads"), ctx.String("models-path"))

			loader := model.NewModelLoader(ctx.String("models-path"), ctx.String("templates-path"))

			app, err := api.App(
				api.WithConfigFile(ctx.String("config-file")),
				api.WithJSONStringPreload(ctx.String("preload-models")),
				api.WithYAMLConfigPreload(ctx.String("preload-models-config")),
				api.WithModelLoader(loader),
				api.WithContextSize(ctx.Int("context-size")),
				api.WithDebug(ctx.Bool("debug")),
				api.WithImageDir(ctx.String("image-path")),
				api.WithF16(ctx.Bool("f16")),
				api.WithDisableMessage(false),
				api.WithCors(ctx.Bool("cors")),
				api.WithCorsAllowOrigins(ctx.String("cors-allow-origins")),
				api.WithThreads(ctx.Int("threads")),
				api.WithBackendAssets(backendAssets),
				api.WithBackendAssetsOutput(ctx.String("backend-assets-path")),
				api.WithUploadLimitMB(ctx.Int("upload-limit")),
			)
			if err != nil {
				return err
			}

			// if av2 := ctx.String("addressv2"); av2 != "" {
			if ctx.Bool("exp-v2") {
				v2ConfigManager := apiv2.NewConfigManager()
				registered, cfgErr := v2ConfigManager.LoadConfigDirectory(ctx.String("config-path"))

				if cfgErr != nil {
					panic("failed to load config directory todo better handler here")
				}

				for i, reg := range registered {
					log.Log().Msgf("%d: %+v", i, reg)

					testField, exists := v2ConfigManager.GetConfig(reg)
					if exists {
						log.Log().Msgf("!! %s: %s", testField.GetRegistration().Endpoint, testField.GetLocalSettings().ModelPath)
					}

				}

				// TODO Cleanup before merge, Fiber seems to be a suitable replacement.
				// v2Server := apiv2.NewLocalAINetHTTPServer(v2ConfigManager, loader, ctx.String("addressv2"))

				v2Server := apiv2.RegisterNewLocalAIFiberServer(v2ConfigManager, loader, app)

				log.Log().Msgf("NEW v2 test: %+v", v2Server)
			}

			return app.Listen(ctx.String("address"))
		},
	}

	err = app.Run(os.Args)
	if err != nil {
		log.Error().Msgf("error: %s", err.Error())
		os.Exit(1)
	}
}
