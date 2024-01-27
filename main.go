package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	api "github.com/go-skynet/LocalAI/api"
	"github.com/go-skynet/LocalAI/api/backend"
	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/internal"
	"github.com/go-skynet/LocalAI/metrics"
	"github.com/go-skynet/LocalAI/pkg/gallery"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	progressbar "github.com/schollz/progressbar/v3"
	"github.com/urfave/cli/v2"
)

const (
	remoteLibraryURL = "https://raw.githubusercontent.com/mudler/LocalAI/master/embedded/model_library.yaml"
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
				Name:    "parallel-requests",
				EnvVars: []string{"PARALLEL_REQUESTS"},
				Usage:   "Enable backends to handle multiple requests in parallel. This is for backends that supports multiple requests in parallel, like llama.cpp or vllm",
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
				Name:    "remote-library",
				Usage:   "A LocalAI remote library URL",
				EnvVars: []string{"REMOTE_LIBRARY"},
				Value:   remoteLibraryURL,
			},
			&cli.StringFlag{
				Name:    "preload-models",
				Usage:   "A List of models to apply in JSON at start",
				EnvVars: []string{"PRELOAD_MODELS"},
			},
			&cli.StringSliceFlag{
				Name:    "models",
				Usage:   "A List of models URLs configurations.",
				EnvVars: []string{"MODELS"},
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
				Name:    "enable-watchdog-idle",
				Usage:   "Enable watchdog for stopping idle backends. This will stop the backends if are in idle state for too long.",
				EnvVars: []string{"WATCHDOG_IDLE"},
				Value:   false,
			},
			&cli.BoolFlag{
				Name:    "enable-watchdog-busy",
				Usage:   "Enable watchdog for stopping busy backends that exceed a defined threshold.",
				EnvVars: []string{"WATCHDOG_BUSY"},
				Value:   false,
			},
			&cli.StringFlag{
				Name:    "watchdog-busy-timeout",
				Usage:   "Watchdog timeout. This will restart the backend if it crashes.",
				EnvVars: []string{"WATCHDOG_BUSY_TIMEOUT"},
				Value:   "5m",
			},
			&cli.StringFlag{
				Name:    "watchdog-idle-timeout",
				Usage:   "Watchdog idle timeout. This will restart the backend if it crashes.",
				EnvVars: []string{"WATCHDOG_IDLE_TIMEOUT"},
				Value:   "15m",
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
				options.WithModelLibraryURL(ctx.String("remote-library")),
				options.WithDisableMessage(false),
				options.WithCors(ctx.Bool("cors")),
				options.WithCorsAllowOrigins(ctx.String("cors-allow-origins")),
				options.WithThreads(ctx.Int("threads")),
				options.WithBackendAssets(backendAssets),
				options.WithBackendAssetsOutput(ctx.String("backend-assets-path")),
				options.WithUploadLimitMB(ctx.Int("upload-limit")),
				options.WithApiKeys(ctx.StringSlice("api-keys")),
				options.WithModelsURL(append(ctx.StringSlice("models"), ctx.Args().Slice()...)...),
			}

			idleWatchDog := ctx.Bool("enable-watchdog-idle")
			busyWatchDog := ctx.Bool("enable-watchdog-busy")
			if idleWatchDog || busyWatchDog {
				opts = append(opts, options.EnableWatchDog)
				if idleWatchDog {
					opts = append(opts, options.EnableWatchDogIdleCheck)
					dur, err := time.ParseDuration(ctx.String("watchdog-idle-timeout"))
					if err != nil {
						return err
					}
					opts = append(opts, options.SetWatchDogIdleTimeout(dur))
				}
				if busyWatchDog {
					opts = append(opts, options.EnableWatchDogBusyCheck)
					dur, err := time.ParseDuration(ctx.String("watchdog-busy-timeout"))
					if err != nil {
						return err
					}
					opts = append(opts, options.SetWatchDogBusyTimeout(dur))
				}
			}
			if ctx.Bool("parallel-requests") {
				opts = append(opts, options.EnableParallelBackendRequests)
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

			metrics, err := metrics.SetupMetrics()
			if err != nil {
				return err
			}
			opts = append(opts, options.WithMetrics(metrics))

			app, err := api.App(opts...)
			if err != nil {
				return err
			}

			return app.Listen(ctx.String("address"))
		},
		Commands: []*cli.Command{
			{
				Name:  "models",
				Usage: "List or install models",
				Subcommands: []*cli.Command{
					{
						Name:  "list",
						Usage: "List the models avaiable in your galleries",
						Action: func(ctx *cli.Context) error {
							var galleries []gallery.Gallery
							if err := json.Unmarshal([]byte(ctx.String("galleries")), &galleries); err != nil {
								log.Error().Msgf("unable to load galleries: %s", err.Error())
							}

							models, err := gallery.AvailableGalleryModels(galleries, ctx.String("models-path"))
							if err != nil {
								return err
							}
							for _, model := range models {
								if model.Installed {
									fmt.Printf(" * %s@%s (installed)\n", model.Gallery.Name, model.Name)
								} else {
									fmt.Printf(" - %s@%s\n", model.Gallery.Name, model.Name)
								}
							}
							return nil
						},
					},
					{
						Name:  "install",
						Usage: "Install a model from the gallery",
						Action: func(ctx *cli.Context) error {
							modelName := ctx.Args().First()

							var galleries []gallery.Gallery
							if err := json.Unmarshal([]byte(ctx.String("galleries")), &galleries); err != nil {
								log.Error().Msgf("unable to load galleries: %s", err.Error())
							}

							progressBar := progressbar.NewOptions(
								1000,
								progressbar.OptionSetDescription(fmt.Sprintf("downloading model %s", modelName)),
								progressbar.OptionShowBytes(false),
								progressbar.OptionClearOnFinish(),
							)
							progressCallback := func(fileName string, current string, total string, percentage float64) {
								progressBar.Set(int(percentage * 10))
							}
							err = gallery.InstallModelFromGallery(galleries, modelName, ctx.String("models-path"), gallery.GalleryModel{}, progressCallback)
							if err != nil {
								return err
							}
							return nil
						},
					},
				},
			},
			{
				Name:  "tts",
				Usage: "Convert text to speech",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "backend",
						Value:   "piper",
						Aliases: []string{"b"},
						Usage:   "Backend to run the TTS model",
					},
					&cli.StringFlag{
						Name:     "model",
						Aliases:  []string{"m"},
						Usage:    "Model name to run the TTS",
						Required: true,
					},
					&cli.StringFlag{
						Name:    "output-file",
						Aliases: []string{"o"},
						Usage:   "The path to write the output wav file",
					},
				},
				Action: func(ctx *cli.Context) error {
					modelOption := ctx.String("model")
					if modelOption == "" {
						return errors.New("--model parameter is required")
					}
					backendOption := ctx.String("backend")
					if backendOption == "" {
						backendOption = "piper"
					}
					outputFile := ctx.String("output-file")
					outputDir := ctx.String("backend-assets-path")
					if outputFile != "" {
						outputDir = filepath.Dir(outputFile)
					}

					text := strings.Join(ctx.Args().Slice(), " ")

					opts := &options.Option{
						Loader:            model.NewModelLoader(ctx.String("models-path")),
						Context:           context.Background(),
						AudioDir:          outputDir,
						AssetsDestination: ctx.String("backend-assets-path"),
					}

					defer opts.Loader.StopAllGRPC()

					filePath, _, err := backend.ModelTTS(backendOption, text, modelOption, opts.Loader, opts)
					if err != nil {
						return err
					}
					if outputFile != "" {
						if err := os.Rename(filePath, outputFile); err != nil {
							return err
						}
						fmt.Printf("Generate file %s\n", outputFile)
					} else {
						fmt.Printf("Generate file %s\n", filePath)
					}
					return nil
				},
			},
			{
				Name:  "transcript",
				Usage: "Convert audio to text",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "backend",
						Value:   "whisper",
						Aliases: []string{"b"},
						Usage:   "Backend to run the transcription model",
					},
					&cli.StringFlag{
						Name:    "model",
						Aliases: []string{"m"},
						Usage:   "Model name to run the transcription",
					},
					&cli.StringFlag{
						Name:    "language",
						Aliases: []string{"l"},
						Usage:   "Language of the audio file",
					},
					&cli.IntFlag{
						Name:    "threads",
						Aliases: []string{"t"},
						Usage:   "Threads to use",
						Value:   1,
					},
					&cli.StringFlag{
						Name:    "output-file",
						Aliases: []string{"o"},
						Usage:   "The path to write the output wav file",
					},
				},
				Action: func(ctx *cli.Context) error {
					modelOption := ctx.String("model")
					filename := ctx.Args().First()
					language := ctx.String("language")
					threads := ctx.Int("threads")

					opts := &options.Option{
						Loader:            model.NewModelLoader(ctx.String("models-path")),
						Context:           context.Background(),
						AssetsDestination: ctx.String("backend-assets-path"),
					}

					cl := config.NewConfigLoader()
					if err := cl.LoadConfigs(ctx.String("models-path")); err != nil {
						return err
					}

					c, exists := cl.GetConfig(modelOption)
					if !exists {
						return errors.New("model not found")
					}

					c.Threads = threads

					defer opts.Loader.StopAllGRPC()

					tr, err := backend.ModelTranscription(filename, language, opts.Loader, c, opts)
					if err != nil {
						return err
					}
					for _, segment := range tr.Segments {
						fmt.Println(segment.Start.String(), "-", segment.Text)
					}
					return nil
				},
			},
		},
	}

	err = app.Run(os.Args)
	if err != nil {
		log.Error().Msgf("error: %s", err.Error())
		os.Exit(1)
	}
}
