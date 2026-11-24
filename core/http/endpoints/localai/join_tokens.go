package localai

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/xlog"
)

// JoinTokens is the store of join tokens. *cluster.JoinTokenStore is one.
type JoinTokens interface {
	Mint(ctx context.Context, req cluster.JoinTokenRequest) (string, *cluster.JoinToken, error)
	Peek(ctx context.Context, token string) (*cluster.JoinToken, error)
	Consume(ctx context.Context, token string, routes ...string) (*cluster.JoinToken, error)
	Refund(ctx context.Context, id string)
	List(ctx context.Context) ([]cluster.JoinToken, error)
	Revoke(ctx context.Context, id, by string) error
}

// JoinTokenView is a join token as an admin reads it. It never holds the secret.
type JoinTokenView struct {
	ID          string     `json:"id"`
	Label       string     `json:"label,omitempty"`
	State       string     `json:"state"` // unused, partly_used, used_up, expired or revoked
	Uses        int        `json:"uses"`
	MaxUses     int        `json:"max_uses"`
	Routes      []string   `json:"routes"` // nats, websocket; empty means any
	AutoApprove bool       `json:"auto_approve"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	RevokedBy   string     `json:"revoked_by,omitempty"`
}

func joinTokenView(t cluster.JoinToken, now time.Time) JoinTokenView {
	routes := t.AllowedRoutes()
	if routes == nil {
		routes = []string{}
	}
	return JoinTokenView{
		ID: t.ID, Label: t.Label, State: t.State(now), Uses: t.Uses, MaxUses: t.MaxUses,
		Routes: routes, AutoApprove: t.AutoApprove, CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt,
		ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt, RevokedBy: t.RevokedBy,
	}
}

// MintJoinTokenRequest is the body of POST /api/cluster/join-tokens. A field
// that is absent takes its default: ten minutes, one use, any connection, and
// the cluster default for auto-approve (off unless the cluster setting
// join_auto_approve is on).
type MintJoinTokenRequest struct {
	// TTLSeconds is how long the token is good for, up to 86400 (24 hours).
	TTLSeconds int `json:"ttl_seconds"`
	// Uses is how many workers may register with it, up to 100. Use more than
	// one for Kubernetes and Compose, where several replicas start together.
	Uses int `json:"uses"`
	// Routes limits the connections of the worker: nats, websocket. Empty allows any.
	Routes []string `json:"routes"`
	// Label is a note for the list, for example the name of the machine.
	Label string `json:"label"`
	// AutoApprove makes the worker that uses the token join with no admin step.
	AutoApprove *bool `json:"auto_approve"`
}

// MintJoinTokenResponse is the answer of POST /api/cluster/join-tokens. It holds
// the plain token and the commands that carry it. This is the only time the
// plain token is shown: the server keeps a hash.
type MintJoinTokenResponse struct {
	JoinTokenView
	// Token is the plain token. It is shown once.
	Token string `json:"token"`
	// Commands are ready to copy, one for each way to run a worker.
	Commands JoinCommands `json:"commands"`
}

func joinError(c echo.Context, status int, msg string) error {
	return c.JSON(status, map[string]string{"error": msg})
}

// MintJoinTokenEndpoint mints a join token.
//
// @Summary Mint a join token for a worker
// @Description Mints a random join token that lets a worker register with the cluster. It is good for 10 minutes and one use unless the request says otherwise (up to 24 hours and 100 uses, for Kubernetes or Compose), it can limit the connections of the worker (nats, websocket; P2P workers use their own invite), and a worker that uses it waits for an admin approval unless auto_approve is true (the default comes from the cluster setting join_auto_approve, off unless set). The server keeps only a hash of the token. The response carries the plain token and ready-to-copy commands for each way to run a worker (install script, Windows, docker, docker compose, kubernetes, manual), built from the external URL of this frontend; it is the only time the plain token is shown. Admin only.
// @Tags Nodes
// @Param request body localai.MintJoinTokenRequest true "token options"
// @Success 201 {object} localai.MintJoinTokenResponse
// @Failure 400 {object} map[string]string "A limit is out of range or a connection is unknown"
// @Failure 503 {object} map[string]string "Distributed mode is not enabled"
// @Router /api/cluster/join-tokens [post]
func MintJoinTokenEndpoint(tokens JoinTokens, settings ClusterSettings) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req MintJoinTokenRequest
		if err := c.Bind(&req); err != nil {
			return joinError(c, http.StatusBadRequest, "invalid request body")
		}
		ctx := c.Request().Context()
		autoApprove := false
		if req.AutoApprove != nil {
			autoApprove = *req.AutoApprove
		} else if all, err := settings.All(ctx); err == nil {
			autoApprove = cluster.JoinAutoApproveFrom(all)
		}
		if req.TTLSeconds < 0 {
			return joinError(c, http.StatusBadRequest, "ttl_seconds must not be negative")
		}
		by := actor(c)
		token, row, err := tokens.Mint(ctx, cluster.JoinTokenRequest{
			TTL:         time.Duration(req.TTLSeconds) * time.Second,
			Uses:        req.Uses,
			Routes:      req.Routes,
			Label:       req.Label,
			AutoApprove: autoApprove,
			CreatedBy:   by,
		})
		if err != nil {
			if errors.Is(err, cluster.ErrJoinBadRequest) {
				return joinError(c, http.StatusBadRequest, err.Error())
			}
			// The error never holds the token: it is minted after the checks.
			xlog.Error("Minting a join token failed", "error", err)
			return joinError(c, http.StatusInternalServerError, "could not mint a join token")
		}
		xlog.Info("Join token minted", "id", row.ID, "by", by, "uses", row.MaxUses, "expires_at", row.ExpiresAt, "auto_approve", row.AutoApprove)
		routes := row.AllowedRoutes()
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(http.StatusCreated, MintJoinTokenResponse{
			JoinTokenView: joinTokenView(*row, time.Now()),
			Token:         token,
			Commands:      BuildJoinCommands(middleware.BaseURL(c), token, routes),
		})
	}
}

// ListJoinTokensEndpoint lists the join tokens.
//
// @Summary List the join tokens
// @Description Lists the join tokens with their state (unused, partly_used, used_up, expired, revoked), the uses left, the connections they allow and who minted them. A token stays in the list for a day after it ends. The secret is never in the list. Admin only.
// @Tags Nodes
// @Success 200 {array} localai.JoinTokenView
// @Failure 503 {object} map[string]string "Distributed mode is not enabled"
// @Router /api/cluster/join-tokens [get]
func ListJoinTokensEndpoint(tokens JoinTokens) echo.HandlerFunc {
	return func(c echo.Context) error {
		rows, err := tokens.List(c.Request().Context())
		if err != nil {
			xlog.Error("Listing join tokens failed", "error", err)
			return joinError(c, http.StatusInternalServerError, "could not list join tokens")
		}
		now := time.Now()
		out := make([]JoinTokenView, 0, len(rows))
		for _, r := range rows {
			out = append(out, joinTokenView(r, now))
		}
		return c.JSON(http.StatusOK, out)
	}
}

// RevokeJoinTokenEndpoint revokes a join token.
//
// @Summary Revoke a join token
// @Description Ends a join token at once. A worker that already registered with it keeps its own credential; revoke the node to end that. Admin only.
// @Tags Nodes
// @Param id path string true "join token id"
// @Success 200 {object} map[string]string
// @Failure 404 {object} map[string]string "No such token"
// @Failure 503 {object} map[string]string "Distributed mode is not enabled"
// @Router /api/cluster/join-tokens/{id} [delete]
func RevokeJoinTokenEndpoint(tokens JoinTokens) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		by := actor(c)
		if err := tokens.Revoke(c.Request().Context(), id, by); err != nil {
			if errors.Is(err, cluster.ErrJoinTokenNotFound) {
				return joinError(c, http.StatusNotFound, "no such join token")
			}
			xlog.Error("Revoking a join token failed", "id", id, "error", err)
			return joinError(c, http.StatusInternalServerError, "could not revoke the join token")
		}
		xlog.Info("Join token revoked", "id", id, "by", by)
		return c.JSON(http.StatusOK, map[string]string{"message": "join token revoked", "id": id})
	}
}
