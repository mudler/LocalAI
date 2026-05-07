package localai

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// nodeError builds a schema.ErrorResponse for node endpoints.
func nodeError(code int, message string) schema.ErrorResponse {
	return schema.ErrorResponse{
		Error: &schema.APIError{
			Code:    code,
			Message: message,
			Type:    "node_error",
		},
	}
}

// ListNodesEndpoint returns all registered backend nodes.
func ListNodesEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeList, err := registry.ListWithExtras(ctx)
		if err != nil {
			xlog.Error("Failed to list nodes", "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to list nodes"))
		}
		return c.JSON(http.StatusOK, nodeList)
	}
}

// GetNodeEndpoint returns a single node by ID.
func GetNodeEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		node, err := registry.Get(ctx, id)
		if err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		return c.JSON(http.StatusOK, node)
	}
}

// RegisterNodeRequest is the request body for registering a new worker node.
type RegisterNodeRequest struct {
	Name          string `json:"name"`
	NodeType      string `json:"node_type,omitempty"` // "backend" (default) or "agent"
	Address       string `json:"address"`
	HTTPAddress   string `json:"http_address,omitempty"`
	Token         string `json:"token,omitempty"`
	TotalVRAM     uint64 `json:"total_vram,omitempty"`
	AvailableVRAM uint64 `json:"available_vram,omitempty"`
	TotalRAM      uint64 `json:"total_ram,omitempty"`
	AvailableRAM  uint64 `json:"available_ram,omitempty"`
	GPUVendor     string            `json:"gpu_vendor,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	// MaxReplicasPerModel is the per-node cap on replicas of any single model.
	// Workers older than this field omit it; we coerce 0 → 1 below to preserve
	// historical single-replica behavior.
	MaxReplicasPerModel int `json:"max_replicas_per_model,omitempty"`
}

// RegisterNodeEndpoint registers a new backend node.
// expectedToken is the registration token configured on the frontend (may be empty to disable auth).
// autoApprove controls whether new nodes go directly to "healthy" or require admin approval.
func RegisterNodeEndpoint(registry *nodes.NodeRegistry, expectedToken string, autoApprove bool, authDB *gorm.DB, hmacSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req RegisterNodeRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid request body"))
		}

		// Validate registration token if one is configured on the frontend
		if expectedToken != "" {
			if req.Token == "" {
				return c.JSON(http.StatusUnauthorized, nodeError(http.StatusUnauthorized, "registration token required"))
			}
			expectedHash := sha256.Sum256([]byte(expectedToken))
			providedHash := sha256.Sum256([]byte(req.Token))
			if subtle.ConstantTimeCompare(expectedHash[:], providedHash[:]) != 1 {
				return c.JSON(http.StatusUnauthorized, nodeError(http.StatusUnauthorized, "invalid registration token"))
			}
		}

		// Determine node type
		nodeType := req.NodeType
		if nodeType == "" {
			nodeType = nodes.NodeTypeBackend
		}
		if nodeType != nodes.NodeTypeBackend && nodeType != nodes.NodeTypeAgent {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest,
				fmt.Sprintf("invalid node_type %q; must be %q or %q", nodeType, nodes.NodeTypeBackend, nodes.NodeTypeAgent)))
		}

		// Backend workers require address; agent workers don't serve gRPC
		if req.Name == "" {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "name is required"))
		}
		if nodeType == nodes.NodeTypeBackend && req.Address == "" {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "address is required for backend workers"))
		}
		if len(req.Name) > 255 {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "name exceeds 255 characters"))
		}
		if len(req.Address) > 512 {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "address exceeds 512 characters"))
		}
		if len(req.HTTPAddress) > 512 {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "http_address exceeds 512 characters"))
		}

		// Hash the token for storage (if provided)
		var tokenHash string
		if req.Token != "" {
			h := sha256.Sum256([]byte(req.Token))
			tokenHash = hex.EncodeToString(h[:])
		}

		// Coerce 0 → 1 for backward compat with workers that don't send the field.
		// GORM's `default:1` only fires for a missing column; once Go zero-values
		// reach the struct field they're written as 0 unless explicitly set here.
		maxReplicasPerModel := req.MaxReplicasPerModel
		if maxReplicasPerModel < 1 {
			maxReplicasPerModel = 1
		}

		node := &nodes.BackendNode{
			Name:                req.Name,
			NodeType:            nodeType,
			Address:             req.Address,
			HTTPAddress:         req.HTTPAddress,
			TokenHash:           tokenHash,
			TotalVRAM:           req.TotalVRAM,
			AvailableVRAM:       req.AvailableVRAM,
			TotalRAM:            req.TotalRAM,
			AvailableRAM:        req.AvailableRAM,
			GPUVendor:           req.GPUVendor,
			MaxReplicasPerModel: maxReplicasPerModel,
		}

		ctx := c.Request().Context()
		if err := registry.Register(ctx, node, autoApprove); err != nil {
			xlog.Error("Failed to register node", "name", req.Name, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to register node"))
		}

		// Merge worker-supplied labels into the node's existing label set,
		// then apply auto-labels. Using SetNodeLabels here would have
		// delete-then-recreate semantics — every worker re-register would
		// wipe any UI-added label (since PR #9583 the worker always sends
		// the auto-mirror `node.replica-slots`, which made the latent bug
		// from PR #9186 trigger universally instead of only for workers
		// with `--node-labels` set).
		for k, v := range req.Labels {
			if err := registry.SetNodeLabel(ctx, node.ID, k, v); err != nil {
				xlog.Warn("Failed to set node label", "node", node.ID, "key", k, "error", err)
			}
		}
		registry.ApplyAutoLabels(ctx, node.ID, node)

		response := map[string]any{
			"id":         node.ID,
			"name":       node.Name,
			"node_type":  node.NodeType,
			"status":     node.Status,
			"created_at": node.CreatedAt,
		}

		// Provision API key for agent workers that are approved (not pending).
		// On re-registration of a previously approved node, revoke old + provision new.
		if nodeType == nodes.NodeTypeAgent && authDB != nil && node.Status != nodes.StatusPending {
			// Use a transaction so that if provisioning fails after revoking old creds,
			// the old credentials are not lost.
			txErr := authDB.Transaction(func(tx *gorm.DB) error {
				if node.AuthUserID != "" {
					if err := tx.Exec("DELETE FROM users WHERE id = ?", node.AuthUserID).Error; err != nil {
						return fmt.Errorf("revoking old credentials: %w", err)
					}
					node.AuthUserID = ""
					node.APIKeyID = ""
				}
				plaintext, err := provisionAgentWorkerKey(ctx, tx, registry, node, hmacSecret)
				if err != nil {
					return err
				}
				response["api_token"] = plaintext
				return nil
			})
			if txErr != nil {
				xlog.Warn("Failed to auto-provision API key for agent worker", "node", node.Name, "error", txErr)
			}
		}

		return c.JSON(http.StatusCreated, response)
	}
}

// ApproveNodeEndpoint approves a pending node, setting its status to healthy.
// For agent workers, it also provisions an API key so they can call the inference API.
func ApproveNodeEndpoint(registry *nodes.NodeRegistry, authDB *gorm.DB, hmacSecret string) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		if err := registry.ApproveNode(ctx, id); err != nil {
			xlog.Error("Failed to approve node", "id", id, "error", err)
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "failed to approve node"))
		}
		node, err := registry.Get(ctx, id)
		if err != nil {
			return c.JSON(http.StatusOK, map[string]string{"message": "node approved"})
		}

		response := map[string]any{
			"id":        node.ID,
			"name":      node.Name,
			"node_type": node.NodeType,
			"status":    node.Status,
			"message":   "node approved",
		}

		// Provision API key for newly approved agent workers
		if node.NodeType == nodes.NodeTypeAgent && authDB != nil && node.AuthUserID == "" {
			if plaintext, err := provisionAgentWorkerKey(ctx, authDB, registry, node, hmacSecret); err != nil {
				xlog.Warn("Failed to provision API key on approval", "node", node.Name, "error", err)
			} else {
				response["api_token"] = plaintext
			}
		}

		return c.JSON(http.StatusOK, response)
	}
}

// provisionAgentWorkerKey creates a dedicated user and API key for an agent worker node.
// Returns the plaintext API key on success.
func provisionAgentWorkerKey(ctx context.Context, authDB *gorm.DB, registry *nodes.NodeRegistry, node *nodes.BackendNode, hmacSecret string) (string, error) {
	workerUser := &auth.User{
		ID:        uuid.New().String(),
		Name:      "agent-worker:" + node.Name,
		Provider:  auth.ProviderAgentWorker,
		Subject:   node.ID,
		Role:      "user",
		Status:    "active",
		CreatedAt: time.Now(),
	}
	if err := authDB.Create(workerUser).Error; err != nil {
		return "", fmt.Errorf("creating agent worker user: %w", err)
	}

	plaintext, apiKey, err := auth.CreateAPIKey(authDB, workerUser.ID, "agent-worker:"+node.Name, "user", hmacSecret, nil)
	if err != nil {
		return "", fmt.Errorf("creating API key: %w", err)
	}

	node.AuthUserID = workerUser.ID
	node.APIKeyID = apiKey.ID
	if err := registry.UpdateAuthRefs(ctx, node.ID, workerUser.ID, apiKey.ID); err != nil {
		xlog.Warn("Failed to update auth refs on node", "node", node.Name, "error", err)
	}

	// Grant collections feature so the worker can store/retrieve KB data on behalf of users.
	perm := &auth.UserPermission{
		ID:          uuid.New().String(),
		UserID:      workerUser.ID,
		Permissions: auth.PermissionMap{auth.FeatureCollections: true},
	}
	if err := authDB.Create(perm).Error; err != nil {
		xlog.Warn("Failed to grant collections permission to agent worker", "node", node.Name, "error", err)
	}

	xlog.Info("Provisioned API key for agent worker", "node", node.Name, "user", workerUser.ID)
	return plaintext, nil
}

// DeregisterNodeEndpoint removes a backend node permanently (admin use).
func DeregisterNodeEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		if err := registry.Deregister(ctx, id); err != nil {
			xlog.Error("Failed to deregister node", "id", id, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to deregister node"))
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "node deregistered"})
	}
}

// DeactivateNodeEndpoint marks a node as offline without deleting it.
// Used by workers on graceful shutdown to preserve approval status across restarts.
func DeactivateNodeEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		if err := registry.MarkOffline(ctx, id); err != nil {
			xlog.Error("Failed to deactivate node", "id", id, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to deactivate node"))
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "node set to offline"})
	}
}

// HeartbeatEndpoint updates the heartbeat for a node.
func HeartbeatEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")

		// Parse optional VRAM update from body
		var update nodes.HeartbeatUpdate
		_ = c.Bind(&update) // best-effort — empty body is fine

		var updatePtr *nodes.HeartbeatUpdate
		if update.AvailableVRAM != nil || update.TotalVRAM != nil || update.AvailableRAM != nil || update.GPUVendor != "" {
			updatePtr = &update
		}

		ctx := c.Request().Context()
		if err := registry.Heartbeat(ctx, id, updatePtr); err != nil {
			xlog.Warn("Heartbeat failed for node", "id", id, "error", err)
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "heartbeat received"})
	}
}

// GetNodeModelsEndpoint returns the models loaded on a node.
func GetNodeModelsEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		models, err := registry.GetNodeModels(ctx, id)
		if err != nil {
			xlog.Error("Failed to get node models", "id", id, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to get node models"))
		}
		return c.JSON(http.StatusOK, models)
	}
}

// DrainNodeEndpoint sets a node to draining status (no new requests).
func DrainNodeEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		if err := registry.MarkDraining(ctx, id); err != nil {
			xlog.Error("Failed to drain node", "id", id, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to drain node"))
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "node set to draining"})
	}
}

// ResumeNodeEndpoint sets a draining node back to healthy status.
func ResumeNodeEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		if err := registry.MarkHealthy(ctx, id); err != nil {
			xlog.Error("Failed to resume node", "id", id, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to resume node"))
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "node resumed"})
	}
}

// InstallBackendOnNodeEndpoint triggers backend installation on a worker node via NATS.
// Backend can be either a gallery ID (resolved against BackendGalleries) or a
// direct URI install (URI + Name + optional Alias) — same shape as the
// standalone /api/backends/install-external path, just scoped to one node.
func InstallBackendOnNodeEndpoint(unloader nodes.NodeCommandSender) echo.HandlerFunc {
	return func(c echo.Context) error {
		if unloader == nil {
			return c.JSON(http.StatusServiceUnavailable, nodeError(http.StatusServiceUnavailable, "NATS not configured"))
		}
		nodeID := c.Param("id")
		var req struct {
			Backend          string `json:"backend"`
			BackendGalleries string `json:"backend_galleries,omitempty"`
			URI              string `json:"uri,omitempty"`
			Name             string `json:"name,omitempty"`
			Alias            string `json:"alias,omitempty"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid request body"))
		}
		// Either a gallery backend name or a direct URI must be supplied.
		if req.Backend == "" && req.URI == "" {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "backend name or uri required"))
		}
		// Admin-driven backend install: not tied to a specific replica slot
		// (no model is being loaded). Pass replica 0 to match the worker's
		// admin process-key convention (`backend#0`). force=false so the
		// worker's fast path takes over if the backend is already running —
		// upgrades go through the dedicated /api/backends/upgrade path.
		reply, err := unloader.InstallBackend(nodeID, req.Backend, "", req.BackendGalleries, req.URI, req.Name, req.Alias, 0, false)
		if err != nil {
			xlog.Error("Failed to install backend on node", "node", nodeID, "backend", req.Backend, "uri", req.URI, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to install backend on node"))
		}
		if !reply.Success {
			xlog.Error("Backend install failed on node", "node", nodeID, "backend", req.Backend, "uri", req.URI, "error", reply.Error)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "backend installation failed"))
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "backend installed"})
	}
}

// DeleteBackendOnNodeEndpoint deletes a backend from a worker node via NATS.
func DeleteBackendOnNodeEndpoint(unloader nodes.NodeCommandSender) echo.HandlerFunc {
	return func(c echo.Context) error {
		if unloader == nil {
			return c.JSON(http.StatusServiceUnavailable, nodeError(http.StatusServiceUnavailable, "NATS not configured"))
		}
		nodeID := c.Param("id")
		var req struct {
			Backend string `json:"backend"`
		}
		if err := c.Bind(&req); err != nil || req.Backend == "" {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "backend name required"))
		}
		reply, err := unloader.DeleteBackend(nodeID, req.Backend)
		if err != nil {
			xlog.Error("Failed to delete backend on node", "node", nodeID, "backend", req.Backend, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to delete backend on node"))
		}
		if !reply.Success {
			xlog.Error("Backend delete failed on node", "node", nodeID, "backend", req.Backend, "error", reply.Error)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "backend deletion failed"))
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "backend deleted"})
	}
}

// ListBackendsOnNodeEndpoint lists installed backends on a worker node via NATS.
func ListBackendsOnNodeEndpoint(unloader nodes.NodeCommandSender) echo.HandlerFunc {
	return func(c echo.Context) error {
		if unloader == nil {
			return c.JSON(http.StatusServiceUnavailable, nodeError(http.StatusServiceUnavailable, "NATS not configured"))
		}
		nodeID := c.Param("id")
		reply, err := unloader.ListBackends(nodeID)
		if err != nil {
			xlog.Error("Failed to list backends on node", "node", nodeID, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to list backends on node"))
		}
		if reply.Error != "" {
			xlog.Error("List backends failed on node", "node", nodeID, "error", reply.Error)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to list backends on node"))
		}
		return c.JSON(http.StatusOK, reply.Backends)
	}
}

// UnloadModelOnNodeEndpoint unloads a model from a worker node (gRPC Free) via NATS.
func UnloadModelOnNodeEndpoint(unloader nodes.NodeCommandSender, registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		if unloader == nil {
			return c.JSON(http.StatusServiceUnavailable, nodeError(http.StatusServiceUnavailable, "NATS not configured"))
		}
		nodeID := c.Param("id")
		var req struct {
			ModelName string `json:"model_name"`
		}
		if err := c.Bind(&req); err != nil || req.ModelName == "" {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "model_name required"))
		}
		if err := unloader.UnloadModelOnNode(nodeID, req.ModelName); err != nil {
			xlog.Error("Failed to unload model on node", "node", nodeID, "model", req.ModelName, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to unload model on node"))
		}
		// Also stop the backend process
		if err := unloader.StopBackend(nodeID, req.ModelName); err != nil {
			xlog.Error("Failed to stop backend after model unload", "node", nodeID, "model", req.ModelName, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "model unloaded but backend stop failed"))
		}
		// Remove every replica of this model on the node from the registry.
		registry.RemoveAllNodeModelReplicas(c.Request().Context(), nodeID, req.ModelName)
		return c.JSON(http.StatusOK, map[string]string{"message": "model unloaded"})
	}
}

// DeleteModelOnNodeEndpoint deletes model files from a worker node via NATS.
func DeleteModelOnNodeEndpoint(unloader nodes.NodeCommandSender, registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		if unloader == nil {
			return c.JSON(http.StatusServiceUnavailable, nodeError(http.StatusServiceUnavailable, "NATS not configured"))
		}
		nodeID := c.Param("id")
		var req struct {
			ModelName string `json:"model_name"`
		}
		if err := c.Bind(&req); err != nil || req.ModelName == "" {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "model_name required"))
		}
		// Unload model first if loaded
		if err := unloader.UnloadModelOnNode(nodeID, req.ModelName); err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to unload model before deletion"))
		}
		if err := unloader.StopBackend(nodeID, req.ModelName); err != nil {
			// Non-fatal — backend process may not be running
			xlog.Warn("StopBackend failed during model deletion (non-fatal)", "node", nodeID, "model", req.ModelName, "error", err)
		}
		registry.RemoveAllNodeModelReplicas(c.Request().Context(), nodeID, req.ModelName)
		return c.JSON(http.StatusOK, map[string]string{"message": "model deleted from node"})
	}
}

// NodeBackendLogsListEndpoint proxies a request to a worker node's /v1/backend-logs
// endpoint to list model IDs that have backend logs.
func NodeBackendLogsListEndpoint(registry *nodes.NodeRegistry, registrationToken string) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		node, err := registry.Get(ctx, nodeID)
		if err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}

		if node.HTTPAddress == "" {
			return c.JSON(http.StatusBadGateway, nodeError(http.StatusBadGateway, "node has no HTTP address"))
		}

		resp, err := proxyHTTPToWorker(node.HTTPAddress, "/v1/backend-logs", registrationToken)
		if err != nil {
			return c.JSON(http.StatusBadGateway, nodeError(http.StatusBadGateway, fmt.Sprintf("failed to reach worker: %v", err)))
		}
		defer resp.Body.Close()

		c.Response().Header().Set("Content-Type", "application/json")
		c.Response().WriteHeader(resp.StatusCode)
		io.Copy(c.Response(), resp.Body)
		return nil
	}
}

// NodeBackendLogsLinesEndpoint proxies a request to a worker node's
// /v1/backend-logs/{modelId} endpoint to get buffered log lines.
func NodeBackendLogsLinesEndpoint(registry *nodes.NodeRegistry, registrationToken string) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		modelID := c.Param("modelId")

		node, err := registry.Get(ctx, nodeID)
		if err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}

		if node.HTTPAddress == "" {
			return c.JSON(http.StatusBadGateway, nodeError(http.StatusBadGateway, "node has no HTTP address"))
		}

		path := "/v1/backend-logs/" + url.PathEscape(modelID)
		resp, err := proxyHTTPToWorker(node.HTTPAddress, path, registrationToken)
		if err != nil {
			return c.JSON(http.StatusBadGateway, nodeError(http.StatusBadGateway, fmt.Sprintf("failed to reach worker: %v", err)))
		}
		defer resp.Body.Close()

		c.Response().Header().Set("Content-Type", "application/json")
		c.Response().WriteHeader(resp.StatusCode)
		io.Copy(c.Response(), resp.Body)
		return nil
	}
}

// NodeBackendLogsWSEndpoint proxies a WebSocket connection to a worker node's
// /v1/backend-logs/{modelId}/ws endpoint for real-time log streaming.
func NodeBackendLogsWSEndpoint(registry *nodes.NodeRegistry, registrationToken string) echo.HandlerFunc {
	browserUpgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // no origin header = same-origin or non-browser
			}
			// Parse origin URL and compare host with request host
			u, err := url.Parse(origin)
			if err != nil {
				return false
			}
			return u.Host == r.Host
		},
	}

	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		modelID := c.Param("modelId")

		node, err := registry.Get(ctx, nodeID)
		if err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}

		// Upgrade browser connection
		browserWS, err := browserUpgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}

		// Dial the worker WebSocket
		workerURL := fmt.Sprintf("ws://%s/v1/backend-logs/%s/ws", node.HTTPAddress, url.PathEscape(modelID))
		workerHeaders := http.Header{}
		if registrationToken != "" {
			workerHeaders.Set("Authorization", "Bearer "+registrationToken)
		}

		workerDialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
		workerWS, _, err := workerDialer.Dial(workerURL, workerHeaders)
		if err != nil {
			browserWS.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "failed to connect to worker"))
			browserWS.Close()
			return nil
		}

		// Use sync.OnceFunc wrappers to avoid double-close and ensure each
		// goroutine can safely close the *other* connection to unblock
		// its peer's ReadMessage call.
		done := make(chan struct{})
		closeWorker := sync.OnceFunc(func() { workerWS.Close() })
		closeBrowser := sync.OnceFunc(func() { browserWS.Close() })

		// Worker → Browser
		go func() {
			defer close(done)
			defer closeBrowser() // unblock Browser→Worker goroutine
			for {
				msgType, msg, err := workerWS.ReadMessage()
				if err != nil {
					return
				}
				if err := browserWS.WriteMessage(msgType, msg); err != nil {
					return
				}
			}
		}()

		// Browser → Worker (mainly for close detection)
		go func() {
			defer closeWorker() // unblock Worker→Browser goroutine
			for {
				msgType, msg, err := browserWS.ReadMessage()
				if err != nil {
					return
				}
				if err := workerWS.WriteMessage(msgType, msg); err != nil {
					return
				}
			}
		}()

		<-done
		closeWorker()
		closeBrowser()
		return nil
	}
}

// GetNodeLabelsEndpoint returns labels for a specific node.
func GetNodeLabelsEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		labels, err := registry.GetNodeLabels(ctx, nodeID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to get labels"))
		}
		// Convert to map for cleaner API response
		result := make(map[string]string)
		for _, l := range labels {
			result[l.Key] = l.Value
		}
		return c.JSON(http.StatusOK, result)
	}
}

// UpdateMaxReplicasPerModelRequest is the body for the per-node replica cap endpoint.
type UpdateMaxReplicasPerModelRequest struct {
	// Value is the new per-model replica cap on this node. Must be >= 1.
	Value int `json:"value"`
}

// UpdateMaxReplicasPerModelEndpoint sets the per-node cap on how many replicas
// of any one model can be loaded concurrently. The corresponding
// `node.replica-slots` auto-label is refreshed so existing AND-selectors keep
// matching, and any unsatisfiable scheduling cooldowns are cleared so the
// reconciler retries on the next tick.
//
// This is a transient admin override — a worker re-registration restores the
// value the worker was started with (--max-replicas-per-model). For permanent
// fleet changes, change the worker flag.
//
// @Summary Update a node's max replicas per model
// @Tags Nodes
// @Param id path string true "Node ID"
// @Param request body UpdateMaxReplicasPerModelRequest true "New value"
// @Success 200 {object} map[string]int
// @Failure 400 {object} map[string]any "value must be >= 1"
// @Failure 404 {object} map[string]any "node not found"
// @Router /api/nodes/{id}/max-replicas-per-model [put]
func UpdateMaxReplicasPerModelEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		if _, err := registry.Get(ctx, nodeID); err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		var req UpdateMaxReplicasPerModelRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid request body"))
		}
		if req.Value < 1 {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "value must be >= 1"))
		}
		if err := registry.UpdateMaxReplicasPerModel(ctx, nodeID, req.Value); err != nil {
			xlog.Error("Failed to update max_replicas_per_model", "node", nodeID, "value", req.Value, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to update max replicas per model"))
		}
		return c.JSON(http.StatusOK, map[string]int{"max_replicas_per_model": req.Value})
	}
}

// ResetMaxReplicasPerModelEndpoint clears the admin override on a node, so
// the next worker re-registration is allowed to update the value from its
// CLI flag again. The current value is left in place until the worker calls
// register.
//
// @Summary Reset a node's max replicas per model to the worker default
// @Tags Nodes
// @Param id path string true "Node ID"
// @Success 200 {object} map[string]bool
// @Failure 404 {object} map[string]any "node not found"
// @Router /api/nodes/{id}/max-replicas-per-model [delete]
func ResetMaxReplicasPerModelEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		if _, err := registry.Get(ctx, nodeID); err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		if err := registry.ResetMaxReplicasPerModel(ctx, nodeID); err != nil {
			xlog.Error("Failed to reset max_replicas_per_model override", "node", nodeID, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to reset override"))
		}
		return c.JSON(http.StatusOK, map[string]bool{"reset": true})
	}
}

// SetNodeLabelsEndpoint replaces all labels for a node.
func SetNodeLabelsEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		if _, err := registry.Get(ctx, nodeID); err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		var labels map[string]string
		if err := c.Bind(&labels); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid request body"))
		}
		if err := registry.SetNodeLabels(ctx, nodeID, labels); err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to set labels"))
		}
		return c.JSON(http.StatusOK, labels)
	}
}

// MergeNodeLabelsEndpoint adds/updates labels without removing existing ones.
func MergeNodeLabelsEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		if _, err := registry.Get(ctx, nodeID); err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		var labels map[string]string
		if err := c.Bind(&labels); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid request body"))
		}
		for k, v := range labels {
			if err := registry.SetNodeLabel(ctx, nodeID, k, v); err != nil {
				return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to merge labels"))
			}
		}
		// Return updated labels
		updated, _ := registry.GetNodeLabels(ctx, nodeID)
		result := make(map[string]string)
		for _, l := range updated {
			result[l.Key] = l.Value
		}
		return c.JSON(http.StatusOK, result)
	}
}

// DeleteNodeLabelEndpoint removes a single label from a node.
func DeleteNodeLabelEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		key := c.Param("key")
		if key == "" {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "label key is required"))
		}
		if err := registry.RemoveNodeLabel(ctx, nodeID, key); err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to remove label"))
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// ListSchedulingEndpoint returns all model scheduling configs.
func ListSchedulingEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		configs, err := registry.ListModelSchedulings(ctx)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to list scheduling configs"))
		}
		return c.JSON(http.StatusOK, configs)
	}
}

// GetSchedulingEndpoint returns the scheduling config for a specific model.
func GetSchedulingEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		modelName := c.Param("model")
		config, err := registry.GetModelScheduling(ctx, modelName)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to get scheduling config"))
		}
		if config == nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "no scheduling config for model"))
		}
		return c.JSON(http.StatusOK, config)
	}
}

// SetSchedulingRequest is the request body for creating/updating a scheduling config.
type SetSchedulingRequest struct {
	ModelName    string            `json:"model_name"`
	NodeSelector map[string]string `json:"node_selector,omitempty"`
	MinReplicas  int               `json:"min_replicas"`
	MaxReplicas  int               `json:"max_replicas"`
}

// SetSchedulingEndpoint creates or updates a model scheduling config.
func SetSchedulingEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		var req SetSchedulingRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid request body"))
		}
		if req.ModelName == "" {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "model_name is required"))
		}
		if req.MinReplicas < 0 {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "min_replicas must be >= 0"))
		}
		if req.MaxReplicas < 0 {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "max_replicas must be >= 0"))
		}
		if req.MaxReplicas > 0 && req.MinReplicas > req.MaxReplicas {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "min_replicas must be <= max_replicas"))
		}

		// Serialize node selector to JSON
		var selectorJSON string
		if len(req.NodeSelector) > 0 {
			b, err := json.Marshal(req.NodeSelector)
			if err != nil {
				return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid node_selector"))
			}
			selectorJSON = string(b)
		}

		config := &nodes.ModelSchedulingConfig{
			ModelName:    req.ModelName,
			NodeSelector: selectorJSON,
			MinReplicas:  req.MinReplicas,
			MaxReplicas:  req.MaxReplicas,
		}
		if err := registry.SetModelScheduling(ctx, config); err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to set scheduling config"))
		}
		return c.JSON(http.StatusOK, config)
	}
}

// DeleteSchedulingEndpoint removes a model scheduling config.
func DeleteSchedulingEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		modelName := c.Param("model")
		if err := registry.DeleteModelScheduling(ctx, modelName); err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to delete scheduling config"))
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// proxyHTTPToWorker makes a GET request to a worker's HTTP server with bearer token auth.
func proxyHTTPToWorker(httpAddress, path, token string) (*http.Response, error) {
	reqURL := fmt.Sprintf("http://%s%s", httpAddress, path)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	return client.Do(req)
}
