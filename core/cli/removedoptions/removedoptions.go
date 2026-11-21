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
	"slices"
	"strconv"
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
	// Soft marks a finding that only warns: the name is a bare one that
	// other programs may set for their own use, such as TOKEN, so it is no
	// reason to stop.
	Soft bool
}

// Line is the one-line form of the finding.
func (f Finding) Line() string {
	if f.Soft {
		return fmt.Sprintf("%s %s was used by the removed P2P layer: %s (ignored)", f.Kind, f.Name, f.Replacement)
	}
	return fmt.Sprintf("%s %s was removed: %s", f.Kind, f.Name, f.Replacement)
}

// Fatal returns the findings that stop the start.
func Fatal(fs []Finding) []Finding {
	var out []Finding
	for _, f := range fs {
		if !f.Soft {
			out = append(out, f)
		}
	}
	return out
}

// Warnings returns the findings that only warn.
func Warnings(fs []Finding) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Soft {
			out = append(out, f)
		}
	}
	return out
}

// ErrRemovedCommand is returned by a removed command that was not stopped
// earlier. It never succeeds.
var ErrRemovedCommand = errors.New("this command was removed; see 'local-ai worker --help' and 'local-ai p2p --help'")

const (
	useMode    = "use --p2p-mode connect (dial out to remotes) or --p2p-mode share (also accept paired devices; needs authentication), env LOCALAI_P2P_MODE"
	invite     = "P2P tokens are gone; devices and nodes join with an invite created under Swarm, P2P"
	noop       = "no replacement; the option only applied to the removed token network"
	federation = "federation is replaced by federation groups of P2P remotes: add the other instance with a remote invite (Models, Remotes) and group it under Swarm, P2P, Federation"
	logLevel   = "no replacement; the P2P layer logs at the level of the server (--log-level)"
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
	{[]string{"--p2p-network-id", "--peer-2-peer-network-id"}, invite},
	{[]string{"--p2p-dht-interval", "--p2p-otp-interval"}, noop},
	{[]string{"--p2p-enable-limits"}, "no replacement; limits are set on the relay (local-ai p2p relay)"},
	{[]string{"--federated"}, federation},
	{[]string{"--random-worker", "--target-worker"}, federation},
}

var envRules = []rule{
	{[]string{"LOCALAI_P2P", "P2P"}, useMode},
	{[]string{"LOCALAI_P2P_TOKEN", "P2P_TOKEN", "LOCALAI_TOKEN", "TOKEN"}, invite},
	{[]string{"LOCALAI_P2P_NETWORK_ID", "P2P_NETWORK_ID"}, invite},
	{[]string{"LOCALAI_P2P_DHT_INTERVAL", "P2P_DHT_INTERVAL", "LOCALAI_P2P_OTP_INTERVAL", "P2P_OTP_INTERVAL"}, noop},
	{[]string{"LOCALAI_P2P_ENABLE_LIMITS"}, "no replacement; limits are set on the relay (local-ai p2p relay)"},
	{[]string{"LOCALAI_P2P_DISABLE_DHT"}, "the public DHT is the runtime setting p2p_public_dht (Settings, P2P)"},
	{[]string{"LOCALAI_P2P_DHT_ANNOUNCE_MADDRS"}, "no replacement; the server announces the addresses it listens on (--p2p-listen)"},
	{[]string{"LOCALAI_P2P_LOGLEVEL", "LOCALAI_P2P_LIB_LOGLEVEL"}, logLevel},
	{[]string{"LOCALAI_FEDERATED", "FEDERATED"}, federation},
	{[]string{"LOCALAI_RANDOM_WORKER", "RANDOM_WORKER", "LOCALAI_TARGET_WORKER", "TARGET_WORKER"}, federation},
}

// genericBare are the bare names that other programs may set for their own use,
// and whether the removed code read them as a boolean. A bare name that is not
// here is specific enough to fail on. A generic one fails only when it is a
// boolean that is true; every other value of it only warns. The names with the
// LOCALAI_ prefix are always failures.
var genericBare = map[string]bool{
	"TOKEN": false, "P2P": true, "FEDERATED": true, "RANDOM_WORKER": true, "TARGET_WORKER": false,
}

var settingRules = []rule{
	{[]string{"p2p_token"}, invite},
	{[]string{"p2p_network_id"}, invite},
	{[]string{"federated"}, federation},
}

const (
	llamaWorker = "pair the worker with an invite instead: local-ai worker llama-cpp-rpc --join <invite link> (make the invite under Nodes, Add a node, Model-split worker)"
	mlxWorker   = "pair the worker with an invite instead: local-ai worker mlx-distributed --join <invite link> (make the invite under Nodes, Add a node, Model-split worker)"
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
				out = append(out, Finding{Kind: Flag, Name: n, Replacement: r.replacement})
			}
		}
	}
	if in.Env != nil {
		for _, r := range envRules {
			for _, n := range r.names {
				v, ok := in.Env(n)
				if !ok || v == "" {
					continue
				}
				if isBool, generic := genericBare[n]; generic {
					b, err := strconv.ParseBool(v)
					switch {
					case isBool && err == nil && !b:
						// P2P=false is a plain no.
						continue
					case !isBool || err != nil || !b:
						out = append(out, Finding{Env, n, r.replacement, true})
						continue
					}
				}
				out = append(out, Finding{Kind: Env, Name: n, Replacement: r.replacement})
			}
		}
	}
	for _, r := range settingRules {
		for _, n := range r.names {
			if raw, ok := in.Settings[n]; ok && settingIsSet(raw) {
				out = append(out, Finding{Kind: Setting, Name: n, Replacement: r.replacement})
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
		return Finding{Kind: Command, Name: "local-ai federated", Replacement: federation}, true
	case "explorer":
		return Finding{Kind: Command, Name: "local-ai explorer", Replacement: "the Explorer was removed; there is no replacement"}, true
	case "worker", "p2p-worker":
		// The word after a flag may be the value of the flag, so look at the
		// words that follow, not only the next one.
		if slices.Contains(cmds[1:], "p2p-llama-cpp-rpc") {
			return Finding{Kind: Command, Name: cmds[0] + " p2p-llama-cpp-rpc", Replacement: llamaWorker}, true
		}
		if slices.Contains(cmds[1:], "p2p-mlx") {
			return Finding{Kind: Command, Name: cmds[0] + " p2p-mlx", Replacement: mlxWorker}, true
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

// readingCommands are the other commands, which may read the removed options.
var readingCommands = map[string]bool{
	"run": true, "worker": true, "p2p-worker": true, "cluster": true, "federated": true, "explorer": true,
}

// commandOf returns the command word and the words that follow it, flags left
// out, and whether options are checked for that command: they are unless the
// command is one of those that never read them. The command is the first word
// that names a command. A word before it is the value of a flag (as in
// `--address :8080 models list`), and no word of a flag value can be taken for
// the command. With no command word the command is `run`, which reads them.
func commandOf(args []string) (words []string, applies bool) {
	start := -1
	for i, a := range args {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") && (otherCommands[a] || readingCommands[a]) {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, true
	}
	for _, a := range args[start:] {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
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
	data, err := os.ReadFile(filepath.Join(dir, "runtime_settings.json")) // #nosec G304 -- dir is the data directory the operator set, not a request value
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
// per failing finding and what to do next. It is empty when no finding fails.
func Message(fs []Finding) string {
	fs = Fatal(fs)
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

// Warning is the text printed for the findings that only warn: one line each.
// It is empty when there is none.
func Warning(fs []Finding) string {
	fs = Warnings(fs)
	if len(fs) == 0 {
		return ""
	}
	lines := make([]string, 0, len(fs))
	for _, f := range fs {
		lines = append(lines, "warning: "+f.Line())
	}
	return strings.Join(lines, "\n")
}
