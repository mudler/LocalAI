// Package removedoptions finds the command line flags, environment variables,
// commands and runtime settings of the removed edgevpn P2P layer, the Explorer
// and the old federation, and says what replaces each one.
//
// The old options fail loudly. The command line parser ignores unknown
// environment variables and a removed flag only gives "unknown flag", so the
// check reads the arguments, the environment and the settings file itself and
// names every option that is still set, one line each, with its replacement.
package removedoptions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Kind says where a removed option was found.
type Kind string

const (
	Flag    Kind = "flag"
	Env     Kind = "environment variable"
	Setting Kind = "runtime setting"
	Command Kind = "command"
)

// Finding is one removed option that is still in use.
type Finding struct {
	Kind        Kind
	Name        string
	Replacement string
}

// Line is the one-line form of the finding.
func (f Finding) Line() string {
	return fmt.Sprintf("%s %s was removed: %s", f.Kind, f.Name, f.Replacement)
}

// ErrRemovedCommand is returned by a removed command that was not stopped
// earlier. It never succeeds.
var ErrRemovedCommand = errors.New("this command was removed; see 'local-ai worker --help' and 'local-ai p2p --help'")

const (
	useMode = "use --p2p-mode connect (dial out to remotes) or --p2p-mode share (also accept paired devices; needs authentication), env LOCALAI_P2P_MODE"
	invite  = "P2P tokens are gone; devices and nodes join with an invite created under Swarm, P2P"
	noop    = "no replacement; the option only applied to the removed token network"
)

type rule struct {
	names       []string
	replacement string
}

// flagRules and envRules are the removed options of `run`, `federated`, the P2P
// workers and the Explorer. The bare names (TOKEN, P2P, FEDERATED) were read
// only by the removed code.
var flagRules = []rule{
	{[]string{"--p2p"}, useMode},
	{[]string{"--p2p-token", "--p2ptoken"}, invite},
	{[]string{"--p2p-network-id"}, invite},
	{[]string{"--p2p-dht-interval", "--p2p-otp-interval"}, noop},
	{[]string{"--p2p-enable-limits"}, "no replacement; limits are set on the relay (local-ai p2p relay)"},
	{[]string{"--federated"}, "federation is replaced by federated-instance invites, created under Swarm, P2P"},
	{[]string{"--random-worker", "--target-worker"}, "federation is replaced by federated-instance invites, created under Swarm, P2P"},
}

var envRules = []rule{
	{[]string{"LOCALAI_P2P", "P2P"}, useMode},
	{[]string{"LOCALAI_P2P_TOKEN", "P2P_TOKEN", "LOCALAI_TOKEN", "TOKEN"}, invite},
	{[]string{"LOCALAI_P2P_NETWORK_ID", "P2P_NETWORK_ID"}, invite},
	{[]string{"LOCALAI_P2P_DHT_INTERVAL", "P2P_DHT_INTERVAL", "LOCALAI_P2P_OTP_INTERVAL", "P2P_OTP_INTERVAL"}, noop},
	{[]string{"LOCALAI_P2P_ENABLE_LIMITS"}, "no replacement; limits are set on the relay (local-ai p2p relay)"},
	{[]string{"LOCALAI_P2P_DISABLE_DHT"}, "the public DHT is the runtime setting p2p_public_dht (Settings, P2P)"},
	{[]string{"LOCALAI_P2P_DHT_ANNOUNCE_MADDRS"}, "no replacement; the server announces the addresses it listens on (--p2p-listen)"},
	{[]string{"LOCALAI_FEDERATED", "FEDERATED"}, "federation is replaced by federated-instance invites, created under Swarm, P2P"},
	{[]string{"LOCALAI_RANDOM_WORKER", "RANDOM_WORKER", "LOCALAI_TARGET_WORKER", "TARGET_WORKER"}, "federation is replaced by federated-instance invites, created under Swarm, P2P"},
}

var settingRules = []rule{
	{[]string{"p2p_token"}, invite},
	{[]string{"p2p_network_id"}, invite},
	{[]string{"federated"}, "federation is replaced by federated-instance invites, created under Swarm, P2P"},
}

const (
	llamaWorker = "run the standalone worker instead: local-ai worker llama-cpp-rpc (see the distributed mode docs)"
	mlxWorker   = "run the standalone worker instead: local-ai worker mlx-distributed (see the MLX distributed docs)"
)

// Input is what the check reads. Env is os.LookupEnv in production. Settings is
// the runtime settings file as raw JSON, nil when there is none.
type Input struct {
	Args     []string
	Env      func(string) (string, bool)
	Settings map[string]json.RawMessage
}

// Check returns every removed option that is in use, in a stable order:
// commands, flags, environment variables, then settings. Flags, environment
// variables and settings are not checked for the commands that never read them
// (`models`, `chat`, ...): a bare name such as TOKEN in the environment of
// `local-ai models list` is not a finding.
func Check(in Input) []Finding {
	var out []Finding
	cmds, applies := commandOf(in.Args)
	if f, ok := removedCommand(cmds); ok {
		out = append(out, f)
	}
	if !applies {
		return out
	}
	args := in.Args
	for _, r := range flagRules {
		for _, n := range r.names {
			if hasFlag(args, n) {
				out = append(out, Finding{Flag, n, r.replacement})
			}
		}
	}
	if in.Env != nil {
		for _, r := range envRules {
			for _, n := range r.names {
				if v, ok := in.Env(n); ok && v != "" {
					out = append(out, Finding{Env, n, r.replacement})
				}
			}
		}
	}
	for _, r := range settingRules {
		for _, n := range r.names {
			if raw, ok := in.Settings[n]; ok && settingIsSet(raw) {
				out = append(out, Finding{Setting, n, r.replacement})
			}
		}
	}
	return out
}

func removedCommand(cmds []string) (Finding, bool) {
	if len(cmds) == 0 {
		return Finding{}, false
	}
	switch cmds[0] {
	case "federated":
		return Finding{Command, "local-ai federated", "federation is replaced by federated-instance invites, created under Swarm, P2P"}, true
	case "explorer":
		return Finding{Command, "local-ai explorer", "the Explorer was removed; there is no replacement"}, true
	case "worker", "p2p-worker":
		if len(cmds) > 1 {
			switch cmds[1] {
			case "p2p-llama-cpp-rpc":
				return Finding{Command, cmds[0] + " p2p-llama-cpp-rpc", llamaWorker}, true
			case "p2p-mlx":
				return Finding{Command, cmds[0] + " p2p-mlx", mlxWorker}, true
			}
		}
	}
	return Finding{}, false
}

// otherCommands are the commands that never read the removed options. A bare
// name such as TOKEN in the environment of one of them is not a finding.
var otherCommands = map[string]bool{
	"models": true, "backends": true, "chat": true, "tts": true, "sound-generation": true, "transcript": true,
	"util": true, "agent-worker": true, "mcp-server": true, "completion": true, "addons": true, "library": true,
	"benchmark": true, "p2p": true,
}

// commandOf returns the first two words of the command line that are not
// flags, and whether options are checked for that command: they are unless the
// first word is one of the commands that never read them. A flag value is
// taken for a word when the flag comes first; that word is not a command name,
// so the check still applies.
func commandOf(args []string) (words []string, applies bool) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		words = append(words, a)
		if len(words) == 2 {
			break
		}
	}
	if len(words) == 0 {
		return nil, true
	}
	return words, !otherCommands[words[0]]
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}

func settingIsSet(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "", "null", "false", `""`:
		return false
	}
	return true
}

// ReadSettings reads runtime_settings.json from dir. A missing or unreadable
// file is not an error: the startup check must not stop a server that has no
// settings file.
func ReadSettings(dir string) map[string]json.RawMessage {
	if dir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "runtime_settings.json"))
	if err != nil {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return m
}

// Message is the text printed before the process exits with status 1: one line
// per finding and what to do next.
func Message(fs []Finding) string {
	if len(fs) == 0 {
		return ""
	}
	lines := make([]string, 0, len(fs)+2)
	lines = append(lines, "LocalAI cannot start: the following options of the removed P2P layer, Explorer or federation are still set.")
	for _, f := range fs {
		lines = append(lines, "  - "+f.Line())
	}
	lines = append(lines, "Remove them from your command line, environment files and runtime settings, and start again.")
	return strings.Join(lines, "\n")
}

// Names returns the sorted names of the findings, for tests and logs.
func Names(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	sort.Strings(out)
	return out
}
