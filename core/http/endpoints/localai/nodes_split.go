package localai

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// SplitNodes answers a backend node with the model-split workers attached to
// it. An error that wraps nodes.ErrSplitUnavailable means that this frontend
// cannot say (it does not share over P2P): the node keeps the list it has.
type SplitNodes interface {
	SplitAssignment(ctx context.Context, nodeID string) (workerctl.SplitAssignment, error)
	// DetachSplitWorkers attaches the split workers of a node that is no
	// longer a split server to no node, with a warning, and returns how
	// many it detached.
	DetachSplitWorkers(ctx context.Context, nodeID string) (int, error)
}

// WithSplitNodes serves POST /api/node/{id}/split.
func WithSplitNodes(s SplitNodes) RegisterOption {
	return func(o *registerOptions) { o.split = s }
}

// NodeSplitEndpoint takes the split report of a backend node, stores it for
// every frontend, and answers with the model-split workers attached to the
// node. The node calls it at every heartbeat interval.
//
// A node that joined over P2P serves its workers on the host it joined with,
// so the peer of its report must be the peer it is pinned to. A node that
// registered with the token is bound to the first peer it reports, until it
// registers again; a report of another peer gets 409.
//
// A 409 without a binding error means that this frontend cannot answer: the
// node keeps its list. A node whose binding is refused keeps it too, and logs
// the error.
func NodeSplitEndpoint(registry *nodes.NodeRegistry, options ...RegisterOption) echo.HandlerFunc {
	opts := newRegisterOptions(options)
	return func(c echo.Context) error {
		if opts.split == nil {
			return c.JSON(http.StatusConflict, nodeError(http.StatusConflict, nodes.ErrSplitUnavailable.Error()))
		}
		ctx := c.Request().Context()
		id := c.Param("id")
		var rep workerctl.SplitReport
		if err := c.Bind(&rep); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid split report"))
		}
		node, err := registry.Get(ctx, id)
		if err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		if node.NodeType == nodes.NodeTypeAgent {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "an agent node runs no models"))
		}
		if node.PeerID != "" && rep.PeerID != "" && rep.PeerID != node.PeerID {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "a node that joined over P2P serves its split workers on the peer it joined with"))
		}
		if rep.PeerID != "" && node.PeerID == "" {
			// A node that registered with the token binds its split host to
			// the first peer it reports: the relays admit that peer for it,
			// so a holder of the token must not move the node to its own.
			if err := registry.PinSplitPeer(ctx, id, rep.PeerID); err != nil {
				if errors.Is(err, nodes.ErrSplitPeerPinned) {
					return c.JSON(http.StatusConflict, nodeError(http.StatusConflict, err.Error()))
				}
				xlog.Warn("Failed to bind the split host of a node", "id", id, "error", err)
				return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to bind the split host of the node"))
			}
		}
		if rep.PeerID != "" {
			if err := registry.SetSplitReport(ctx, id, rep); err != nil {
				xlog.Warn("Failed to store the split report of a node", "id", id, "error", err)
			}
		}
		a, err := opts.split.SplitAssignment(ctx, id)
		if errors.Is(err, nodes.ErrSplitUnavailable) {
			return c.JSON(http.StatusConflict, nodeError(http.StatusConflict, err.Error()))
		}
		if err != nil {
			xlog.Warn("Failed to list the split workers of a node", "id", id, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to list the split workers of the node"))
		}
		if a.Workers == nil {
			a.Workers = []workerctl.SplitAssigned{}
		}
		return c.JSON(http.StatusOK, a)
	}
}

// SetSplitServerRequest is the body of PUT /api/nodes/{id}/split-server.
type SetSplitServerRequest struct {
	// SplitServer declares the node a split server, or clears it.
	SplitServer *bool `json:"split_server"`
}

// SetSplitServerResponse is the answer of PUT /api/nodes/{id}/split-server.
type SetSplitServerResponse struct {
	SplitServer bool `json:"split_server"`
	// Detached is the number of split workers that were attached to the node
	// and are now attached to no node.
	Detached int `json:"detached"`
}

// SetSplitServerEndpoint declares a backend node a split server, or clears
// the declaration.
//
//	@Summary	Declare a node a split server, or clear it
//	@Description	A split server is a backend node that serves model-split workers (llama.cpp RPC and MLX helpers): it runs a P2P host with open ports, the frontends admit it on the gated relays they administer, and the helpers attached to it join the next model it loads. A node becomes one only by this admin action or by the --split-server flag of the worker; the admin's choice wins over the flag until DELETE. Clearing it detaches the split workers of the node (they wait, attached to no node, with a warning), removes its relay admission and stops its P2P host at its next exchange. Admin only.
//	@Tags		Nodes
//	@Accept		json
//	@Produce	json
//	@Param		id		path	string					true	"Node ID"
//	@Param		request	body	SetSplitServerRequest	true	"Declaration"
//	@Success	200	{object}	SetSplitServerResponse
//	@Failure	400	{object}	map[string]any	"agent node, or no split_server field"
//	@Failure	404	{object}	map[string]any	"node not found"
//	@Router		/api/nodes/{id}/split-server [put]
func SetSplitServerEndpoint(registry *nodes.NodeRegistry, options ...RegisterOption) echo.HandlerFunc {
	opts := newRegisterOptions(options)
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		node, err := registry.Get(ctx, id)
		if err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		if node.NodeType == nodes.NodeTypeAgent {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "an agent node runs no models, so it cannot be a split server"))
		}
		var req SetSplitServerRequest
		if err := c.Bind(&req); err != nil || req.SplitServer == nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "split_server (true or false) is required"))
		}
		if err := registry.SetSplitServer(ctx, id, *req.SplitServer); err != nil {
			xlog.Error("Failed to set the split server declaration", "node", id, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to set the split server declaration"))
		}
		out := SetSplitServerResponse{SplitServer: *req.SplitServer}
		if !*req.SplitServer && opts.split != nil {
			n, err := opts.split.DetachSplitWorkers(ctx, id)
			if err != nil {
				// Every frontend detaches them at its next reload anyway.
				xlog.Warn("Failed to detach the split workers of a node", "node", id, "error", err)
			}
			out.Detached = n
		}
		xlog.Info("Split server declaration changed by the admin", "node", node.Name, "split_server", out.SplitServer, "detached", out.Detached)
		return c.JSON(http.StatusOK, out)
	}
}

// ResetSplitServerEndpoint hands the split server declaration of a node back
// to the --split-server flag of the worker.
//
//	@Summary	Hand the split server declaration back to the worker flag
//	@Description	Clears the admin's choice: the next registration of the worker sets the declaration from its --split-server flag. The current value stays until then. Admin only.
//	@Tags		Nodes
//	@Produce	json
//	@Param		id	path	string	true	"Node ID"
//	@Success	200	{object}	map[string]bool
//	@Failure	404	{object}	map[string]any	"node not found"
//	@Router		/api/nodes/{id}/split-server [delete]
func ResetSplitServerEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		if _, err := registry.Get(ctx, id); err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		if err := registry.ResetSplitServer(ctx, id); err != nil {
			xlog.Error("Failed to reset the split server declaration", "node", id, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to reset the split server declaration"))
		}
		return c.JSON(http.StatusOK, map[string]bool{"reset": true})
	}
}
