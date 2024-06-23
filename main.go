package main

import (
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/joho/godotenv"
	"github.com/mudler/LocalAI/core/cli"
	"github.com/mudler/LocalAI/internal"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	_ "github.com/mudler/LocalAI/swagger"
)

func main() {
	var err error

	// Initialize zerolog at a level of INFO, we will set the desired level after we parse the CLI options
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	// Catch signals from the OS requesting us to exit
	go func() {
		c := make(chan os.Signal, 1) // we need to reserve to buffer size 1, so the notifier are not blocked
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		os.Exit(1)
	}()

	// handle loading environment variabled from .env files
	envFiles := []string{".env", "localai.env"}
	homeDir, err := os.UserHomeDir()
	if err == nil {
		envFiles = append(envFiles, filepath.Join(homeDir, "localai.env"), filepath.Join(homeDir, ".config/localai.env"))
	}
	envFiles = append(envFiles, "/etc/localai.env")

	for _, envFile := range envFiles {
		if _, err := os.Stat(envFile); err == nil {
			log.Info().Str("envFile", envFile).Msg("env file found, loading environment variables from file")
			err = godotenv.Load(envFile)
			if err != nil {
				log.Error().Err(err).Str("envFile", envFile).Msg("failed to load environment variables from file")
				continue
			}
		}
	}

	// Actually parse the CLI options
	ctx := kong.Parse(&cli.CLI,
		kong.Description(
			`  LocalAI is a drop-in replacement OpenAI API for running LLM, GPT and genAI models locally on CPU, GPUs with consumer grade hardware.

Some of the models compatible are:
  - Vicuna
  - Koala
  - GPT4ALL
  - GPT4ALL-J
  - Cerebras
  - Alpaca
  - StableLM (ggml quantized)

For a list of compatible models, check out: https://localai.io/model-compatibility/index.html

Copyright: Ettore Di Giacinto

Version: ${version}
`,
		),
		kong.UsageOnError(),
		kong.Vars{
			"basepath":         kong.ExpandPath("."),
			"remoteLibraryURL": "https://raw.githubusercontent.com/mudler/LocalAI/master/embedded/model_library.yaml",
			"galleries":        `[{"name":"localai", "url":"github:mudler/LocalAI/gallery/index.yaml@master"}]`,
			"version":          internal.PrintableVersion(),
		},
	)

	// Configure the logging level before we run the application
	// This is here to preserve the existing --debug flag functionality
	logLevel := "info"
	if cli.CLI.Debug && cli.CLI.LogLevel == nil {
		logLevel = "debug"
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		cli.CLI.LogLevel = &logLevel
	}

	if cli.CLI.LogLevel == nil {
		cli.CLI.LogLevel = &logLevel
	}

	switch *cli.CLI.LogLevel {
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
		log.Info().Msg("Setting logging to error")
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
		log.Info().Msg("Setting logging to warn")
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
		log.Info().Msg("Setting logging to info")
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		log.Debug().Msg("Setting logging to debug")
	case "trace":
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
		log.Trace().Msg("Setting logging to trace")
	}

	// Populate the application with the embedded backend assets
	cli.CLI.Context.BackendAssets = backendAssets

	// Run the thing!
	err = ctx.Run(&cli.CLI.Context)
	if err != nil {
		log.Fatal().Err(err).Msg("Error running the application")
	}
}
