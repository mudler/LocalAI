// Package logsetup installs the xlog process logger for a command entry point.
// Only the code that owns the process (a main package or a CLI command) may
// call it: a service shares its process with others and must not replace the
// logger.
package logsetup

import (
	"log/slog"
	"os"

	"github.com/mudler/xlog"
)

// LogSettings are the logging options of the main CLI: the level, the format
// and the deduplication of repeated lines.
type LogSettings struct {
	// Level is "error", "warn", "info", "debug" or "trace". Empty means "info".
	Level string
	// Format is "default", "text" or "json". Empty means "default".
	Format string
	// Dedup forces deduplication of consecutive identical lines on or off.
	// Nil leaves it to the terminal detection of the logger.
	Dedup *bool
}

// LogFlags are the logging flags of the small localai-relay binary. They
// have the names, environment variables and values of the global flags of
// `local-ai`.
type LogFlags struct {
	Debug        bool    `env:"LOCALAI_DEBUG,DEBUG" default:"false" hidden:"" help:"DEPRECATED, use --log-level=debug instead. Enable debug logging"`
	LogLevel     *string `env:"LOCALAI_LOG_LEVEL" enum:"error,warn,info,debug,trace" help:"Set the level of logs to output [${enum}]"`
	LogFormat    *string `env:"LOCALAI_LOG_FORMAT" default:"default" enum:"default,text,json" help:"Set the format of logs to output [${enum}]"`
	LogDedupLogs *bool   `env:"LOCALAI_LOG_DEDUP" negatable:"" help:"Deduplicate consecutive identical log lines (auto-detected for terminals, use --log-dedup-logs to force on or --no-log-dedup-logs to force off)"`
}

// Settings returns the flags as LogSettings, with the same handling of the
// deprecated --debug flag as the main CLI.
func (f LogFlags) Settings() LogSettings {
	return NewLogSettings(f.Debug, f.LogLevel, f.LogFormat, f.LogDedupLogs)
}

// NewLogSettings builds LogSettings from the values of the global flags of
// `local-ai`. A set level wins over the deprecated debug flag.
func NewLogSettings(debug bool, level, format *string, dedup *bool) LogSettings {
	s := LogSettings{Level: "info", Dedup: dedup}
	switch {
	case level != nil:
		s.Level = *level
	case debug:
		s.Level = "debug"
	}
	if format != nil {
		s.Format = *format
	}
	return s
}

// SetLogger installs the xlog logger the way the main CLI does. With stderr
// set, every log line goes to standard error, so that standard output can
// carry machine output only.
func SetLogger(s LogSettings, stderr bool) {
	level := xlog.LogLevel(s.Level)
	if s.Level == "" {
		level = xlog.LogLevel("info")
	}
	format := s.Format
	if format == "" {
		format = "default"
	}
	if stderr {
		h := xlog.NewHandler(format, os.Stderr, &slog.HandlerOptions{Level: level.ToSlogLevel()})
		xlog.SetLogger(xlog.NewLoggerWithHandler(h, level))
		return
	}
	var opts []xlog.LoggerOption
	if s.Dedup != nil {
		if *s.Dedup {
			opts = append(opts, xlog.WithDedup())
		} else {
			opts = append(opts, xlog.WithoutDedup())
		}
	}
	xlog.SetLogger(xlog.NewLogger(level, format, opts...))
}
