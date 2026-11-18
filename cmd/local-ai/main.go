package main

import (
	"errors"
	"os"

	"github.com/alecthomas/kong"
	"github.com/joho/godotenv"
	"github.com/mudler/LocalAI/core/cli"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"

	_ "github.com/mudler/LocalAI/swagger"
)

func main() {
	var err error

	// Initialize xlog at a level of INFO, we will set the desired level after we parse the CLI options
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text"))

	// The default folders and the env files depend on the OS: the current
	// folder and /etc/localai.env on Linux and macOS, the standard folders on
	// Windows. LOCALAI_BASE_PATH can come from an env file, so resolve the
	// folders again once the files are loaded.
	envFiles := system.ResolveDefaultDirs().EnvFiles
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

	defaultDirs := system.ResolveDefaultDirs()

	// Actually parse the CLI options
	k := kong.Must(&cli.CLI,
		kong.Description(
			`  LocalAI is a drop-in replacement OpenAI API for running LLM, GPT and genAI models locally on CPU, GPUs with consumer grade hardware.

For a list of all available models run local-ai models list

Copyright: Ettore Di Giacinto

Version: ${version}

For documentation and support:
  Documentation: https://localai.io/
  Getting Started: https://localai.io/basics/getting_started/
  GitHub Issues: https://github.com/mudler/LocalAI/issues
`,
		),
		kong.UsageOnError(),
		cli.KongVars(defaultDirs),
	)
	ctx, err := k.Parse(os.Args[1:])
	if err != nil {
		k.FatalIfErrorf(err)
	}

	// Pass Kong model to the completion command for dynamic script generation
	cli.CLI.Completion.SetApplication(k.Model)

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

	// Set xlog logger with the desired level and text format.
	// xlog auto-enables log deduplication when output is a terminal.
	var logOpts []xlog.LoggerOption
	if cli.CLI.LogDedupLogs != nil {
		if *cli.CLI.LogDedupLogs {
			logOpts = append(logOpts, xlog.WithDedup())
		} else {
			logOpts = append(logOpts, xlog.WithoutDedup())
		}
	}

	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel(*cli.CLI.LogLevel), *cli.CLI.LogFormat, logOpts...))

	cli.LogDefaultDirs(defaultDirs)

	// Loaded here rather than in RunCMD so `worker`, `backends install` and
	// `models install` authenticate the same way the server does.
	if err := cli.LoadCredentials(cli.CLI.CredentialsFile); err != nil {
		xlog.Fatal("Error loading credentials", "error", err)
	}

	// Run the thing!
	err = ctx.Run(&cli.CLI.Context)
	if err != nil {
		// A command that has already told the user what went wrong returns
		// only a status. Logging it as well would print a bare "exit status 1"
		// underneath the explanation they just read.
		var reported cli.ExitCodeError
		if errors.As(err, &reported) {
			os.Exit(reported.Code)
		}
		xlog.Fatal("Error running the application", "error", err)
	}
}
