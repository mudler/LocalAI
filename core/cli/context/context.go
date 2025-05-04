package cliContext

import (
	rice "github.com/GeertJohan/go.rice"
)

type Context struct {
	Debug    bool    `env:"LOCALAI_DEBUG,DEBUG" default:"false" hidden:"" help:"DEPRECATED, use --log-level=debug instead. Enable debug logging"`
	LogLevel *string `env:"LOCALAI_LOG_LEVEL" enum:"error,warn,info,debug,trace" help:"Set the level of logs to output [${enum}]"`

	// This field is not a command line argument/flag, the struct tag excludes it from the parsed CLI
	BackendAssets *rice.Box `kong:"-"`
}
