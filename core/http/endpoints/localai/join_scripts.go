package localai

import (
	"embed"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/xlog"
)

//go:embed joinscripts/*
var joinScriptFiles embed.FS

// Paths of the join scripts. They are public, and answer only for a join token
// that exists and is neither expired, revoked nor used up. Serving a script
// never spends the token: only a registration does.
const (
	JoinShellPath      = "/join.sh"
	JoinPowerShellPath = "/join.ps1"
	JoinKubernetesPath = "/join.yaml"
)

// JoinScriptKind is one of the join scripts.
type JoinScriptKind string

const (
	JoinScriptShell      JoinScriptKind = "join.sh"
	JoinScriptPowerShell JoinScriptKind = "join.ps1"
	JoinScriptKubernetes JoinScriptKind = "join.yaml"
)

// psQuote quotes a string for a PowerShell single-quoted string.
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// joinPowerShell is the script of Windows. It sets the environment of the
// installer and runs it.
func joinPowerShell(frontend, token, routes string) string {
	var b strings.Builder
	b.WriteString("# LocalAI worker join script for Windows. Run it from an elevated PowerShell:\n")
	b.WriteString("#   irm 'https://ai.example.com/join.ps1?t=<join token>' | iex\n")
	b.WriteString("# The join token is in this file: do not share it. It never goes on a command line.\n")
	b.WriteString("$ErrorActionPreference = 'Stop'\n")
	b.WriteString("$env:LOCALAI_REGISTER_TO = " + psQuote(frontend) + "\n")
	b.WriteString("$env:LOCALAI_JOIN_TOKEN = " + psQuote(token) + "\n")
	if routes != "" {
		b.WriteString("$env:LOCALAI_WORKER_ROUTES = " + psQuote(routes) + "\n")
	}
	b.WriteString("$env:LOCALAI_WORKER = '1'\n")
	b.WriteString("$installUrl = if ($env:LOCALAI_INSTALL_URL) { $env:LOCALAI_INSTALL_URL } else { 'https://localai.io/install.ps1' }\n")
	b.WriteString("irm $installUrl | iex\n")
	return b.String()
}

func renderJoinScript(kind JoinScriptKind, frontend, token string, routes []string) (string, string, error) {
	routeList := strings.Join(routes, ",")
	switch kind {
	case JoinScriptPowerShell:
		return joinPowerShell(frontend, token, routeList), "text/plain; charset=utf-8", nil
	case JoinScriptShell:
		raw, err := joinScriptFiles.ReadFile("joinscripts/join.sh")
		if err != nil {
			return "", "", err
		}
		out := strings.NewReplacer(
			"@@REGISTER_TO@@", shQuote(frontend),
			"@@JOIN_TOKEN@@", shQuote(token),
			"@@ROUTES@@", shQuote(routeList),
		).Replace(string(raw))
		return out, "text/x-shellscript; charset=utf-8", nil
	default:
		raw, err := joinScriptFiles.ReadFile("joinscripts/join.yaml")
		if err != nil {
			return "", "", err
		}
		routesEnv, natsEnv := "", ""
		wr := workerRoutes(routes)
		if wr != "" {
			routesEnv = "            - name: LOCALAI_WORKER_ROUTES\n              value: " + yamlQuote(wr) + "\n"
		}
		for _, r := range routes {
			if r == cluster.JoinRouteNATS {
				// A worker on NATS is dialled at its address: the address of the pod.
				natsEnv = "            - name: POD_IP\n              valueFrom:\n                fieldRef:\n                  fieldPath: status.podIP\n" +
					"            - name: LOCALAI_ADVERTISE_ADDR\n              value: \"$(POD_IP):50051\"\n" +
					"            - name: LOCALAI_ADVERTISE_HTTP_ADDR\n              value: \"$(POD_IP):50050\"\n"
			}
		}
		out := strings.NewReplacer(
			"@@REGISTER_TO_YAML@@", yamlQuote(frontend),
			"@@JOIN_TOKEN_YAML@@", yamlQuote(token),
			"@@ROUTES_ENV@@", routesEnv,
			"@@NATS_ENV@@", natsEnv,
		).Replace(string(raw))
		return out, "application/yaml; charset=utf-8", nil
	}
}

// JoinScriptEndpoint serves a join script for the token in the query "t".
//
// The route is public: whoever holds the token can read the script, and the
// script holds the token. So it answers only for a token that is valid now, with
// the same 404 for a token that is unknown, expired, revoked or used up, and it
// throttles a source that asks for many tokens that are not valid. It does not
// spend the token, and it never writes the token, or the query, to a log.
//
// The optional query "routes" (nats, websocket) limits the connections of the
// worker. It must be within what the token allows.
//
// @Summary Join script for a worker
// @Description Returns the script that installs a worker and joins it to this cluster: join.sh for Linux and macOS (installs the binary through install.sh in worker mode, writes a systemd unit or a launchd daemon, keeps the settings in a root-only file), join.ps1 for Windows, join.yaml for Kubernetes (a Secret and a StatefulSet with a volume for the state directory). Public, but answers only for a join token that exists and is not expired, revoked or used up; the answer is the same 404 for every other token. Serving the script does not spend the token.
// @Tags Nodes
// @Produce plain
// @Param t query string true "join token"
// @Param routes query string false "connections of the worker, comma separated: nats, websocket"
// @Success 200 {string} string "the script"
// @Failure 400 {object} map[string]string "A connection is unknown or the token does not allow it"
// @Failure 404 {object} map[string]string "The token is not valid"
// @Failure 429 {object} map[string]string "Too many tokens that are not valid"
// @Router /join.sh [get]
// @Router /join.ps1 [get]
// @Router /join.yaml [get]
func JoinScriptEndpoint(tokens JoinTokens, limiter *cluster.JoinFailureLimiter, kind JoinScriptKind) echo.HandlerFunc {
	return func(c echo.Context) error {
		source := c.RealIP()
		if limiter != nil && !limiter.Allowed(source) {
			c.Response().Header().Set("Retry-After", "60")
			return joinError(c, http.StatusTooManyRequests, "too many join tokens that are not valid: try again later")
		}
		token := c.QueryParam("t")
		row, err := tokens.Peek(c.Request().Context(), token)
		if err != nil {
			if limiter != nil {
				limiter.Fail(source)
			}
			if !errors.Is(err, cluster.ErrJoinTokenInvalid) {
				xlog.Error("Reading a join token for a script failed", "error", err)
			}
			return joinError(c, http.StatusNotFound, "not found")
		}

		var routes []string
		if q := c.QueryParam("routes"); q != "" {
			routes, err = cluster.NormalizeJoinRoutes(strings.Split(q, ","))
			if err != nil {
				return joinError(c, http.StatusBadRequest, "routes: use nats, websocket or both")
			}
			for _, r := range routes {
				if !row.Allows(r) {
					return joinError(c, http.StatusBadRequest, "this join token does not allow the connection "+r)
				}
			}
		} else {
			routes = row.AllowedRoutes()
		}

		body, contentType, err := renderJoinScript(kind, strings.TrimRight(middleware.BaseURL(c), "/"), token, routes)
		if err != nil {
			xlog.Error("Rendering a join script failed", "error", err)
			return joinError(c, http.StatusInternalServerError, "could not build the script")
		}
		h := c.Response().Header()
		h.Set("Content-Type", contentType)
		// The script holds the token: no cache, no sniffing, no indexing.
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Robots-Tag", "noindex")
		h.Set("Referrer-Policy", "no-referrer")
		return c.String(http.StatusOK, body)
	}
}
