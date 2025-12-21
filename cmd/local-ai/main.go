package main

import (
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
	"github.com/joho/godotenv"
	"github.com/mudler/LocalAI/core/cli"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/xlog"

	_ "github.com/mudler/LocalAI/swagger"
)

func main() {
	var err error

	// Initialize xlog at a level of INFO, we will set the desired level after we parse the CLI options
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text"))

	// handle loading environment variables from .env files
	envFiles := []string{".env", "localai.env"}
	homeDir, err := os.UserHomeDir()
	if err == nil {
		envFiles = append(envFiles, filepath.Join(homeDir, "localai.env"), filepath.Join(homeDir, ".config/localai.env"))
	}
	envFiles = append(envFiles, "/etc/localai.env")

	for _, envFile := range envFiles {
		if _, err := os.Stat(envFile); err == nil {
			xlog.Debug("env file found, loading environment variables from file", "envFile", envFile)
			err = godotenv.Load(envFile)
			if err != nil {
				xlog.Error("failed to load environment variables from file", "error", err, "envFile", envFile)
				continue
			}
		}
	}

	// Actually parse the CLI options
	ctx := kong.Parse(&cli.CLI,
		kong.Description(
			`  LocalAI is a drop-in replacement OpenAI API for running LLM, GPT and genAI models locally on CPU, GPUs with consumer grade hardware.

For a list of all available models run local-ai models list

Copyright: Ettore Di Giacinto

Version: ${version}
`,
		),
		kong.UsageOnError(),
		kong.Vars{
			"basepath":  kong.ExpandPath("."),
			"galleries": `[{"name":"localai", "url":"github:mudler/LocalAI/gallery/index.yaml@master"}]`,
			"backends":  `[{"name":"localai", "url":"github:mudler/LocalAI/backend/index.yaml@master"}]`,
			"version":   internal.PrintableVersion(),
		},
	)

	// Configure the logging level before we run the application
	// This is here to preserve the existing --debug flag functionality
	logLevel := "info"
	if cli.CLI.Debug && cli.CLI.LogLevel == nil {
		logLevel = "debug"
		cli.CLI.LogLevel = &logLevel
	}

	if cli.CLI.LogLevel == nil {
		cli.CLI.LogLevel = &logLevel
	}

	// Set xlog logger with the desired level and text format
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel(*cli.CLI.LogLevel), *cli.CLI.LogFormat))

	// Run the thing!
	err = ctx.Run(&cli.CLI.Context)
	if err != nil {
		xlog.Fatal("Error running the application", "error", err)
	}
}
