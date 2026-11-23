package localai

import (
	"net/url"
	"strings"
)

// JoinCommands are the ready-to-copy ways to start a worker with a join token.
// Each one carries the plain token, so the response that holds them is shown
// once and never stored.
type JoinCommands struct {
	// InstallScript installs the binary and a service on a Linux or macOS host.
	InstallScript string `json:"install_script"`
	// Windows installs the binary and a service on a Windows host (PowerShell).
	Windows string `json:"windows"`
	// Docker runs the worker as one container.
	Docker string `json:"docker"`
	// DockerCompose is a compose file with one worker service.
	DockerCompose string `json:"docker_compose"`
	// Kubernetes applies a Secret and a StatefulSet.
	Kubernetes string `json:"kubernetes"`
	// Manual starts a worker from a binary that is already installed.
	Manual string `json:"manual"`
}

// joinWorkerImage is the container image of the worker. The GPU builds have
// their own tags, which the docker and compose commands name in a comment.
const joinWorkerImage = "localai/localai:latest"

// shQuote quotes a string for a POSIX shell.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// yamlQuote quotes a string for a YAML double-quoted scalar.
func yamlQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// workerRoutes maps the names of the connections of a join token to the names of
// the routes of the worker.
func workerRoutes(routes []string) string {
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		if r == "websocket" {
			r = "tunnel"
		}
		out = append(out, r)
	}
	return strings.Join(out, ",")
}

// JoinScriptURL is the address of one of the join scripts (join.sh, join.ps1,
// join.yaml) for a token. base ends with a slash.
func JoinScriptURL(base, script, token string, routes []string) string {
	// The token goes first so that the part of the URL that matters is easy to
	// find. A comma is legal in a query and easier to read than %2C.
	u := strings.TrimRight(base, "/") + "/" + script + "?t=" + url.QueryEscape(token)
	if len(routes) > 0 {
		u += "&routes=" + strings.ReplaceAll(url.QueryEscape(strings.Join(routes, ",")), "%2C", ",")
	}
	return u
}

// BuildJoinCommands builds the commands for a token. base is the external URL of
// this frontend and ends with a slash; routes are the connections the worker
// should hold (nats, websocket), empty for the default.
func BuildJoinCommands(base, token string, routes []string) JoinCommands {
	frontend := strings.TrimRight(base, "/")
	wr := workerRoutes(routes)

	manualEnv := "LOCALAI_REGISTER_TO=" + shQuote(frontend) + " LOCALAI_JOIN_TOKEN=" + shQuote(token)
	if wr != "" {
		manualEnv += " LOCALAI_WORKER_ROUTES=" + shQuote(wr)
	}

	docker := []string{
		"# GPU: use a GPU tag of the image (for example latest-gpu-nvidia-cuda-12) and add --gpus all",
		"docker run -d --name localai-worker --restart unless-stopped --init \\",
		"  -e LOCALAI_REGISTER_TO=" + shQuote(frontend) + " \\",
		"  -e LOCALAI_JOIN_TOKEN=" + shQuote(token) + " \\",
	}
	if wr != "" {
		docker = append(docker, "  -e LOCALAI_WORKER_ROUTES="+shQuote(wr)+" \\")
	}
	docker = append(docker,
		"  -v localai-worker-models:/models -v localai-worker-backends:/backends -v localai-worker-data:/data \\",
		"  "+joinWorkerImage+" worker",
	)

	compose := []string{
		"services:",
		"  localai-worker:",
		"    image: " + joinWorkerImage + "  # GPU: use a GPU tag, for example latest-gpu-nvidia-cuda-12",
		"    command: [\"worker\"]",
		"    init: true",
		"    restart: unless-stopped",
		"    environment:",
		"      LOCALAI_REGISTER_TO: " + yamlQuote(frontend),
		"      LOCALAI_JOIN_TOKEN: " + yamlQuote(token),
	}
	if wr != "" {
		compose = append(compose, "      LOCALAI_WORKER_ROUTES: "+yamlQuote(wr))
	}
	compose = append(compose,
		"    volumes:",
		"      - localai-worker-models:/models",
		"      - localai-worker-backends:/backends",
		"      # The worker keeps its own credential here, so a restart needs no token.",
		"      - localai-worker-data:/data",
		"    # GPU (NVIDIA): add",
		"    #   deploy: {resources: {reservations: {devices: [{driver: nvidia, count: all, capabilities: [gpu]}]}}}",
		"volumes:",
		"  localai-worker-models:",
		"  localai-worker-backends:",
		"  localai-worker-data:",
	)

	return JoinCommands{
		InstallScript: "curl -fsSL " + shQuote(JoinScriptURL(frontend, "join.sh", token, routes)) + " | sudo sh",
		Windows:       "irm " + shQuote(JoinScriptURL(frontend, "join.ps1", token, routes)) + " | iex",
		Docker:        strings.Join(docker, "\n"),
		DockerCompose: strings.Join(compose, "\n"),
		Kubernetes:    "kubectl apply -f " + shQuote(JoinScriptURL(frontend, "join.yaml", token, routes)),
		Manual:        manualEnv + " local-ai worker",
	}
}
