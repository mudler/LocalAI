package main

import (
	"context"
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
			&cli.IntFlag{
				Name:        "threads",
				DefaultText: "Number of threads used for parallel computation. Usage of the number of physical cores in the system is suggested.",
				EnvVars:     []string{"THREADS"},
				Value:       4,
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
			&cli.StringFlag{
				Name:        "addressv2",
				DefaultText: "Bind address for the API server (DEBUG v2 TEST)",
				EnvVars:     []string{"ADDRESS_V2"},
				Value:       ":8085",
			},
			&cli.StringFlag{
				Name:        "image-path",
				DefaultText: "Image directory",
				EnvVars:     []string{"IMAGE_PATH"},
				Value:       "",
			},
			&cli.IntFlag{
				Name:        "context-size",
				DefaultText: "Default context size of the model",
				EnvVars:     []string{"CONTEXT_SIZE"},
				Value:       512,
			},
			&cli.IntFlag{
				Name:        "upload-limit",
				DefaultText: "Default upload-limit. MB",
				EnvVars:     []string{"UPLOAD_LIMIT"},
				Value:       15,
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

			if av2 := ctx.String("addressv2"); av2 != "" {

				v2ConfigManager := apiv2.NewConfigManager()
				registered, cfgErr := v2ConfigManager.LoadConfigDirectory(ctx.String("config-path"))

				if cfgErr != nil {
					panic("failed to load config directory todo better handler here")
				}

				for i, reg := range registered {
					log.Log().Msgf("%d: %+v", i, reg)

					testField, exists := v2ConfigManager.GetConfig(reg)
					if exists {
						log.Log().Msgf("!! %s: %s", testField.GetRegistration().Endpoint, testField.GetLocalPaths().Model)
					}

				}

				v2Server := apiv2.NewLocalAINetHTTPServer(v2ConfigManager, loader, ctx.String("addressv2"))

				log.Log().Msgf("NEW v2 test: %+v", v2Server)
			}
			return api.App(context.Background(), ctx.String("config-file"), loader, ctx.Int("upload-limit"), ctx.Int("threads"), ctx.Int("context-size"), ctx.Bool("f16"), ctx.Bool("debug"), false, ctx.String("image-path")).Listen(ctx.String("address"))
		},
	}

	err = app.Run(os.Args)
	if err != nil {
		log.Error().Msgf("error: %s", err.Error())
		os.Exit(1)
	}
}
