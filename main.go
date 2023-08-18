package main

import (
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	api "github.com/go-skynet/LocalAI/api"
	"github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/internal"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	// clean up process
	go func() {
		c := make(chan os.Signal, 1) // we need to reserve to buffer size 1, so the notifier are not blocked
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		os.Exit(1)
	}()

	path, err := os.Getwd()
	if err != nil {
		log.Error().Msgf("error: %s", err.Error())
		os.Exit(1)
	}

	app := &cli.App{
		Name:    "LocalAI",
		Version: internal.PrintableVersion(),
		Usage:   "OpenAI compatible API for running LLaMA/GPT models locally on CPU with consumer grade hardware.",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "f16",
				EnvVars: []string{"F16"},
			},
			&cli.BoolFlag{
				Name:    "autoload-galleries",
				EnvVars: []string{"AUTOLOAD_GALLERIES"},
			},
			&cli.BoolFlag{
				Name:    "debug",
				EnvVars: []string{"DEBUG"},
			},
			&cli.BoolFlag{
				Name:    "single-active-backend",
				EnvVars: []string{"SINGLE_ACTIVE_BACKEND"},
				Usage:   "Allow only one backend to be running.",
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
				Name:    "threads",
				Usage:   "Number of threads used for parallel computation. Usage of the number of physical cores in the system is suggested.",
				EnvVars: []string{"THREADS"},
				Value:   4,
			},
			&cli.StringFlag{
				Name:    "models-path",
				Usage:   "Path containing models used for inferencing",
				EnvVars: []string{"MODELS_PATH"},
				Value:   filepath.Join(path, "models"),
			},
			&cli.StringFlag{
				Name:    "galleries",
				Usage:   "JSON list of galleries",
				EnvVars: []string{"GALLERIES"},
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
				Name:    "config-file",
				Usage:   "Config file",
				EnvVars: []string{"CONFIG_FILE"},
			},
			&cli.StringFlag{
				Name:    "address",
				Usage:   "Bind address for the API server.",
				EnvVars: []string{"ADDRESS"},
				Value:   ":8080",
			},
			&cli.StringFlag{
				Name:    "image-path",
				Usage:   "Image directory",
				EnvVars: []string{"IMAGE_PATH"},
				Value:   "/tmp/generated/images",
			},
			&cli.StringFlag{
				Name:    "audio-path",
				Usage:   "audio directory",
				EnvVars: []string{"AUDIO_PATH"},
				Value:   "/tmp/generated/audio",
			},
			&cli.StringFlag{
				Name:    "backend-assets-path",
				Usage:   "Path used to extract libraries that are required by some of the backends in runtime.",
				EnvVars: []string{"BACKEND_ASSETS_PATH"},
				Value:   "/tmp/localai/backend_data",
			},
			&cli.StringSliceFlag{
				Name:    "external-grpc-backends",
				Usage:   "A list of external grpc backends",
				EnvVars: []string{"EXTERNAL_GRPC_BACKENDS"},
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
			&cli.StringSliceFlag{
				Name:    "api-keys",
				Usage:   "List of API Keys to enable API authentication. When this is set, all the requests must be authenticated with one of these API keys.",
				EnvVars: []string{"API_KEY"},
			},
			&cli.BoolFlag{
				Name:    "preload-backend-only",
				Usage:   "If set, the api is NOT launched, and only the preloaded models / backends are started. This is intended for multi-node setups.",
				EnvVars: []string{"PRELOAD_BACKEND_ONLY"},
				Value:   false,
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

For a list of compatible model, check out: https://localai.io/model-compatibility/index.html
`,
		UsageText: `local-ai [options]`,
		Copyright: "Ettore Di Giacinto",
		Action: func(ctx *cli.Context) error {

			opts := []options.AppOption{
				options.WithConfigFile(ctx.String("config-file")),
				options.WithJSONStringPreload(ctx.String("preload-models")),
				options.WithYAMLConfigPreload(ctx.String("preload-models-config")),
				options.WithModelLoader(model.NewModelLoader(ctx.String("models-path"))),
				options.WithContextSize(ctx.Int("context-size")),
				options.WithDebug(ctx.Bool("debug")),
				options.WithImageDir(ctx.String("image-path")),
				options.WithAudioDir(ctx.String("audio-path")),
				options.WithF16(ctx.Bool("f16")),
				options.WithStringGalleries(ctx.String("galleries")),
				options.WithDisableMessage(false),
				options.WithCors(ctx.Bool("cors")),
				options.WithCorsAllowOrigins(ctx.String("cors-allow-origins")),
				options.WithThreads(ctx.Int("threads")),
				options.WithBackendAssets(backendAssets),
				options.WithBackendAssetsOutput(ctx.String("backend-assets-path")),
				options.WithUploadLimitMB(ctx.Int("upload-limit")),
				options.WithApiKeys(ctx.StringSlice("api-keys")),
			}

			if ctx.Bool("single-active-backend") {
				opts = append(opts, options.EnableSingleBackend)
			}

			externalgRPC := ctx.StringSlice("external-grpc-backends")
			// split ":" to get backend name and the uri
			for _, v := range externalgRPC {
				backend := v[:strings.IndexByte(v, ':')]
				uri := v[strings.IndexByte(v, ':')+1:]
				opts = append(opts, options.WithExternalBackend(backend, uri))
			}

			if ctx.Bool("autoload-galleries") {
				opts = append(opts, options.EnableGalleriesAutoload)
			}

			if ctx.Bool("preload-backend-only") {
				_, _, err := api.Startup(opts...)
				return err
			}

			app, err := api.App(opts...)
			if err != nil {
				return err
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
