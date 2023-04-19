package main

import (
	"fmt"
	"os"
	"runtime"

	api "github.com/go-skynet/LocalAI/api"
	model "github.com/go-skynet/LocalAI/pkg/model"

	"github.com/urfave/cli/v2"
)

func main() {
	path, err := os.Getwd()
	if err != nil {
		fmt.Println(err)
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
- Alpaca

It uses llama.cpp and gpt4all as backend, supporting all the models supported by both.
`,
		UsageText: `local-ai [options]`,
		Copyright: "go-skynet authors",
		Action: func(ctx *cli.Context) error {
			return api.Start(model.NewModelLoader(ctx.String("models-path")), ctx.String("address"), ctx.Int("threads"), ctx.Int("context-size"), ctx.Bool("f16"))
		},
	}

	err = app.Run(os.Args)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
