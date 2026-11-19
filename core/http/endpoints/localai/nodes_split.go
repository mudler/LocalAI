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
// so the peer of its report must be the peer it is pinned to.
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
