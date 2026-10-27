package cli

import (
	"os"
	"reflect"
	"strings"

	"github.com/mudler/xlog"
)

// deprecatedFlags maps old flag names to their new replacements.
var deprecatedFlags = map[string]string{
	"--p2ptoken": "--p2p-token",
}

// warnDeprecatedFlags checks os.Args for any deprecated flag names and logs
// a warning directing the user to the new name. Old flags continue to work
// via kong aliases, so this is purely informational.
func warnDeprecatedFlags() {
	for _, arg := range os.Args[1:] {
		// Strip any =value suffix to match flag names like --p2ptoken=xyz
		flag := arg
		if idx := strings.Index(flag, "="); idx != -1 {
			flag = flag[:idx]
		}
		if newName, ok := deprecatedFlags[flag]; ok {
			xlog.Warn("Deprecated flag used", "old", flag, "new", newName, "message", "please switch to the new flag name; the old name will be removed in a future release")
		}
	}
}

// RemovedAgentOptions are the flags and environment variables of the old agent
// pool, agent jobs, skills and chat history. The runtime for them is gone, so
// they do nothing, but they are still accepted: a deployment that sets one keeps
// starting. warn names the ones that were set, in one message.
type RemovedAgentOptions struct {
	AgentJobRetentionDays          string `env:"LOCALAI_AGENT_JOB_RETENTION_DAYS,AGENT_JOB_RETENTION_DAYS" hidden:"" help:"Removed; ignored"`
	AgentPoolMultimodalModel       string `env:"LOCALAI_AGENT_POOL_MULTIMODAL_MODEL" hidden:"" help:"Removed; ignored"`
	AgentPoolTranscriptionModel    string `env:"LOCALAI_AGENT_POOL_TRANSCRIPTION_MODEL" hidden:"" help:"Removed; ignored"`
	AgentPoolTranscriptionLanguage string `env:"LOCALAI_AGENT_POOL_TRANSCRIPTION_LANGUAGE" hidden:"" help:"Removed; ignored"`
	AgentPoolTTSModel              string `env:"LOCALAI_AGENT_POOL_TTS_MODEL" hidden:"" help:"Removed; ignored"`
	AgentPoolTimeout               string `env:"LOCALAI_AGENT_POOL_TIMEOUT" hidden:"" help:"Removed; ignored"`
	AgentPoolEnableSkills          bool   `env:"LOCALAI_AGENT_POOL_ENABLE_SKILLS" hidden:"" help:"Removed; ignored"`
	AgentPoolCustomActionsDir      string `env:"LOCALAI_AGENT_POOL_CUSTOM_ACTIONS_DIR" hidden:"" help:"Removed; ignored"`
	AgentPoolEnableLogs            bool   `env:"LOCALAI_AGENT_POOL_ENABLE_LOGS" hidden:"" help:"Removed; ignored"`
	AgentHubURL                    string `env:"LOCALAI_AGENT_HUB_URL" hidden:"" help:"Removed; ignored"`
}

// set returns the environment variable names of the removed options that have
// a value.
func (o RemovedAgentOptions) set() []string {
	var out []string
	v, t := reflect.ValueOf(o), reflect.TypeOf(o)
	for i := 0; i < t.NumField(); i++ {
		f := v.Field(i)
		if (f.Kind() == reflect.Bool && !f.Bool()) || (f.Kind() == reflect.String && f.String() == "") {
			continue
		}
		names := strings.Split(t.Field(i).Tag.Get("env"), ",")
		out = append(out, names[0])
	}
	return out
}

func (o RemovedAgentOptions) warn() {
	if set := o.set(); len(set) > 0 {
		xlog.Warn("These options were removed with the old agents, jobs and skills; they are ignored", "options", strings.Join(set, ", "),
			"message", "remove them from your configuration; see the chats migration page")
	}
}
