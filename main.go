package main

import (
	"os"

	api "github.com/go-skynet/LocalAI/api"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/jaypipes/ghw"
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

	threads := 4
	cpu, err := ghw.CPU()
	if err == nil {
		threads = int(cpu.TotalCores)
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
				Value:       threads,
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
			&cli.IntFlag{
				Name:        "memory-threshold",
				DefaultText: "Memory threshold in MB for auto-fit model unloading. 0 disables auto-fit.",
				EnvVars:     []string{"MEMORY_THRESHOLD"},
				Value:       0,
			},
			&cli.BoolFlag{
				Name:        "auto-fit",
				DefaultText: "Enable automatic model unloading when memory threshold is exceeded",
				EnvVars:     []string{"AUTO_FIT"},
				Value:       false,
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
			loader := model.NewModelLoader(ctx.String("models-path"))
			
			// Configure memory management if auto-fit is enabled
			if ctx.Bool("auto-fit") {
				threshold := ctx.Int("memory-threshold")
				if threshold > 0 {
					loader.SetMemoryThreshold(threshold)
					loader.SetAutoFit(true)
					log.Info().Msgf("Auto-fit enabled with memory threshold: %dMB", threshold)
				}
			}
			
			return api.App(loader, ctx.Int("threads"), ctx.Int("context-size"), ctx.Bool("f16"), ctx.Bool("debug"), false, ctx.Bool("auto-fit"), ctx.Int("memory-threshold")).Listen(ctx.String("address"))
		},
	}

	err = app.Run(os.Args)
	if err != nil {
		log.Error().Msgf("error: %s", err.Error())
		os.Exit(1)
	}
}
