package cliContext

type Context struct {
	Debug        bool    `env:"LOCALAI_DEBUG,DEBUG" default:"false" hidden:"" help:"DEPRECATED, use --log-level=debug instead. Enable debug logging"`
	LogLevel     *string `env:"LOCALAI_LOG_LEVEL" enum:"error,warn,info,debug,trace" help:"Set the level of logs to output [${enum}]"`
	LogFormat    *string `env:"LOCALAI_LOG_FORMAT" default:"default" enum:"default,text,json" help:"Set the format of logs to output [${enum}]"`
	LogDedupLogs *bool   `env:"LOCALAI_LOG_DEDUP" negatable:"" help:"Deduplicate consecutive identical log lines (auto-detected for terminals, use --log-dedup-logs to force on or --no-log-dedup-logs to force off)"`
}
