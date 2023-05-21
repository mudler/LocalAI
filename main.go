package main

import (
	"fmt"
	"os"
	"path/filepath"

	api "github.com/go-skynet/LocalAI/api"
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
				Name:    "cors-allow-origins",
				EnvVars: []string{"CORS_ALLOW_ORIGINS"},
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
			return api.App(
				api.WithConfigFile(ctx.String("config-file")),
				api.WithModelLoader(model.NewModelLoader(ctx.String("models-path"))),
				api.WithContextSize(ctx.Int("context-size")),
				api.WithDebug(ctx.Bool("debug")),
				api.WithImageDir(ctx.String("image-path")),
				api.WithF16(ctx.Bool("f16")),
				api.WithCors(ctx.Bool("cors")),
				api.WithCorsAllowOrigins(ctx.String("cors-allow-origins")),
				api.WithThreads(ctx.Int("threads")),
				api.WithUploadLimitMB(ctx.Int("upload-limit"))).Listen(ctx.String("address"))
		},
	}

	err = app.Run(os.Args)
	if err != nil {
		log.Error().Msgf("error: %s", err.Error())
		os.Exit(1)
	}
}
