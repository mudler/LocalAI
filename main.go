package main

import (
	"os"
	"runtime"

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
			&cli.IntFlag{
				Name:        "threads",
				DefaultText: "Number of threads used for parallel computation. Usage of the number of physical cores in the system is suggested.",
				EnvVars:     []string{"THREADS"},
				Value:       runtime.NumCPU(),
			},
			&cli.StringFlag{
				Name:        "models-path",
				DefaultText: "Path containing models used for inferencing",
				EnvVars:     []string{"MODELS_PATH"},
				Value:       path,
			},
			&cli.StringFlag{
				Name:        "address",
				DefaultText: "Bind address for the API server.",
				EnvVars:     []string{"ADDRESS"},
				Value:       ":8080",
			},
			&cli.IntFlag{
				Name:        "context-size",
				DefaultText: "Default context size of the model",
				EnvVars:     []string{"CONTEXT_SIZE"},
				Value:       512,
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
			zerolog.SetGlobalLevel(zerolog.InfoLevel)
			if ctx.Bool("debug") {
				zerolog.SetGlobalLevel(zerolog.DebugLevel)
			}
			return api.Start(model.NewModelLoader(ctx.String("models-path")), ctx.String("address"), ctx.Int("threads"), ctx.Int("context-size"), ctx.Bool("f16"))
		},
	}

	err = app.Run(os.Args)
	if err != nil {
		log.Error().Msgf("error: %s", err.Error())
		os.Exit(1)
	}
}
