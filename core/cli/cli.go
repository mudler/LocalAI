package cli

import "embed"

type Context struct {
	Debug    bool    `env:"LOCALAI_DEBUG,DEBUG" default:"false" hidden:"" help:"DEPRECATED, use --log-level=debug instead. Enable debug logging"`
	LogLevel *string `env:"LOCALAI_LOG_LEVEL" enum:"error,warn,info,debug,trace" help:"Set the level of logs to output [${enum}]"`

	// This field is not a command line argument/flag, the struct tag excludes it from the parsed CLI
	BackendAssets embed.FS `kong:"-"`
}

var CLI struct {
	Context `embed:""`

	Run        RunCMD        `cmd:"" help:"Run LocalAI, this the default command if no other command is specified. Run 'local-ai run --help' for more information" default:"withargs"`
	Models     ModelsCMD     `cmd:"" help:"Manage LocalAI models and definitions"`
	TTS        TTSCMD        `cmd:"" help:"Convert text to speech"`
	Transcript TranscriptCMD `cmd:"" help:"Convert audio to text"`
}
