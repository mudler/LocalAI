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

// RemovedAgentWorkerOptions are the flags and environment variables of the old
// agent worker, which ran agents and jobs and called the HTTP API. They do
// nothing now and are accepted so a deployment that sets them keeps starting.
type RemovedAgentWorkerOptions struct {
	APIURL          string `env:"LOCALAI_API_URL" hidden:"" help:"Removed; ignored"`
	APIToken        string `env:"LOCALAI_API_TOKEN" hidden:"" help:"Removed; ignored"`
	Subject         string `env:"LOCALAI_AGENT_SUBJECT" hidden:"" help:"Removed; ignored"`
	Queue           string `env:"LOCALAI_AGENT_QUEUE" hidden:"" help:"Removed; ignored"`
	MCPCIJobTimeout string `env:"LOCALAI_MCP_CI_JOB_TIMEOUT" hidden:"" help:"Removed; ignored"`
}

// setOptions returns the first environment variable name of each field of the
// options struct o that has a value.
func setOptions(o any) []string {
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

func warnRemovedOptions(set []string) {
	if len(set) > 0 {
		xlog.Warn("These options were removed with the old agents, jobs and skills; they are ignored", "options", strings.Join(set, ", "),
			"message", "remove them from your configuration; see the chats migration page")
	}
}

// set returns the environment variable names of the removed options that have
// a value.
func (o RemovedAgentOptions) set() []string { return setOptions(o) }

func (o RemovedAgentOptions) warn() { warnRemovedOptions(o.set()) }

func (o RemovedAgentWorkerOptions) set() []string { return setOptions(o) }

func (o RemovedAgentWorkerOptions) warn() { warnRemovedOptions(o.set()) }

// LegacyCollectionsOptions are the old names of the collections options, from
// when the collections shared the settings of the agent pool. They are hidden
// and still work: resolveLegacyCollections copies each one into its new option
// when the new option is not set.
type LegacyCollectionsOptions struct {
	DisableAgents             bool   `env:"LOCALAI_DISABLE_AGENTS" hidden:"" help:"Deprecated: use --disable-chats and --disable-collections"`
	AgentPoolAPIURL           string `env:"LOCALAI_AGENT_POOL_API_URL" hidden:"" help:"Deprecated: use --collections-api-url"`
	AgentPoolAPIKey           string `env:"LOCALAI_AGENT_POOL_API_KEY" hidden:"" help:"Deprecated: use --collections-api-key"`
	AgentPoolDefaultModel     string `env:"LOCALAI_AGENT_POOL_DEFAULT_MODEL" hidden:"" help:"Deprecated: use --collections-default-model"`
	AgentPoolStateDir         string `env:"LOCALAI_AGENT_POOL_STATE_DIR" hidden:"" help:"Deprecated: use --collections-state-dir"`
	AgentPoolVectorEngine     string `env:"LOCALAI_AGENT_POOL_VECTOR_ENGINE" hidden:"" help:"Deprecated: use --collections-vector-engine"`
	AgentPoolEmbeddingModel   string `env:"LOCALAI_AGENT_POOL_EMBEDDING_MODEL" hidden:"" help:"Deprecated: use --collections-embedding-model"`
	AgentPoolDatabaseURL      string `env:"LOCALAI_AGENT_POOL_DATABASE_URL" hidden:"" help:"Deprecated: use --collections-database-url"`
	AgentPoolMaxChunkingSize  int    `env:"LOCALAI_AGENT_POOL_MAX_CHUNKING_SIZE" hidden:"" help:"Deprecated: use --collections-max-chunking-size"`
	AgentPoolChunkOverlap     int    `env:"LOCALAI_AGENT_POOL_CHUNK_OVERLAP" hidden:"" help:"Deprecated: use --collections-chunk-overlap"`
	AgentPoolCollectionDBPath string `env:"LOCALAI_AGENT_POOL_COLLECTION_DB_PATH" hidden:"" help:"Deprecated: use --collections-db-path"`
}

// explicitlySet says whether the operator set an option, to any value: a bool
// option reads false both when it is off and when it was never given, so the
// environment and the command line are asked.
func explicitlySet(env, flag string) bool {
	if _, ok := os.LookupEnv(env); ok {
		return true
	}
	for _, a := range os.Args[1:] {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

// resolveLegacyCollections applies the old names of the collections options.
// An old name sets the new option only when the new option is empty, so the new
// name wins. Each old name that is in use gets one deprecation warning. It
// returns the old names that were in use.
func (r *RunCMD) resolveLegacyCollections() []string {
	var used []string
	use := func(env, replacement string) {
		xlog.Warn("Deprecated option used", "old", env, "new", replacement, "message", "please switch to the new name; the old name will be removed in a future release")
		used = append(used, env)
	}
	str := func(old string, dst *string, env, replacement string) {
		if old == "" {
			return
		}
		use(env, replacement)
		if *dst == "" {
			*dst = old
		}
	}
	num := func(old int, dst *int, env, replacement string) {
		if old == 0 {
			return
		}
		use(env, replacement)
		if *dst == 0 {
			*dst = old
		}
	}
	l := r.LegacyCollectionsOptions
	if l.DisableAgents {
		// On master this one switch turned off the agent pool, which also held
		// the knowledge base. Keep that meaning: it now turns off the chats
		// (agents, triggers, connectors, jobs) and the collections.
		use("LOCALAI_DISABLE_AGENTS", "LOCALAI_DISABLE_CHATS and LOCALAI_DISABLE_COLLECTIONS")
		// A new name that is set, even to false, wins over the old one.
		if !explicitlySet("LOCALAI_DISABLE_CHATS", "--disable-chats") {
			r.DisableChats = true
		}
		if !explicitlySet("LOCALAI_DISABLE_COLLECTIONS", "--disable-collections") {
			r.DisableCollections = true
		}
	}
	str(l.AgentPoolAPIURL, &r.CollectionsAPIURL, "LOCALAI_AGENT_POOL_API_URL", "LOCALAI_COLLECTIONS_API_URL")
	str(l.AgentPoolAPIKey, &r.CollectionsAPIKey, "LOCALAI_AGENT_POOL_API_KEY", "LOCALAI_COLLECTIONS_API_KEY")
	str(l.AgentPoolDefaultModel, &r.CollectionsDefaultModel, "LOCALAI_AGENT_POOL_DEFAULT_MODEL", "LOCALAI_COLLECTIONS_DEFAULT_MODEL")
	str(l.AgentPoolStateDir, &r.CollectionsStateDir, "LOCALAI_AGENT_POOL_STATE_DIR", "LOCALAI_COLLECTIONS_STATE_DIR")
	str(l.AgentPoolVectorEngine, &r.CollectionsVectorEngine, "LOCALAI_AGENT_POOL_VECTOR_ENGINE", "LOCALAI_COLLECTIONS_VECTOR_ENGINE")
	str(l.AgentPoolEmbeddingModel, &r.CollectionsEmbeddingModel, "LOCALAI_AGENT_POOL_EMBEDDING_MODEL", "LOCALAI_COLLECTIONS_EMBEDDING_MODEL")
	str(l.AgentPoolDatabaseURL, &r.CollectionsDatabaseURL, "LOCALAI_AGENT_POOL_DATABASE_URL", "LOCALAI_COLLECTIONS_DATABASE_URL")
	num(l.AgentPoolMaxChunkingSize, &r.CollectionsMaxChunkingSize, "LOCALAI_AGENT_POOL_MAX_CHUNKING_SIZE", "LOCALAI_COLLECTIONS_MAX_CHUNKING_SIZE")
	num(l.AgentPoolChunkOverlap, &r.CollectionsChunkOverlap, "LOCALAI_AGENT_POOL_CHUNK_OVERLAP", "LOCALAI_COLLECTIONS_CHUNK_OVERLAP")
	str(l.AgentPoolCollectionDBPath, &r.CollectionsDBPath, "LOCALAI_AGENT_POOL_COLLECTION_DB_PATH", "LOCALAI_COLLECTIONS_DB_PATH")
	return used
}
