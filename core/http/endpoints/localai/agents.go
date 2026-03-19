package localai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/LocalAGI/core/state"
	coreTypes "github.com/mudler/LocalAGI/core/types"
	agiServices "github.com/mudler/LocalAGI/services"
)

// getUserID extracts the scoped user ID from the request context.
// Returns empty string when auth is not active (backward compat).
func getUserID(c echo.Context) string {
	user := auth.GetUser(c)
	if user == nil {
		return ""
	}
	return user.ID
}

// isAdminUser returns true if the authenticated user has admin role.
func isAdminUser(c echo.Context) bool {
	user := auth.GetUser(c)
	return user != nil && user.Role == auth.RoleAdmin
}

// wantsAllUsers returns true if the request has ?all_users=true and the user is admin.
func wantsAllUsers(c echo.Context) bool {
	return c.QueryParam("all_users") == "true" && isAdminUser(c)
}

// effectiveUserID returns the user ID to scope operations to.
// SECURITY: Only admins may supply ?user_id=<id> to operate on another user's
// resources. Non-admin callers always get their own ID regardless of query params.
func effectiveUserID(c echo.Context) string {
	if targetUID := c.QueryParam("user_id"); targetUID != "" && isAdminUser(c) {
		return targetUID
	}
	return getUserID(c)
}

func ListAgentsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := getUserID(c)
		statuses := svc.ListAgentsForUser(userID)
		agents := make([]string, 0, len(statuses))
		for name := range statuses {
			agents = append(agents, name)
		}
		sort.Strings(agents)
		resp := map[string]any{
			"agents":     agents,
			"agentCount": len(agents),
			"actions":    len(agiServices.AvailableActions),
			"connectors": len(agiServices.AvailableConnectors),
			"statuses":   statuses,
		}
		if hubURL := svc.AgentHubURL(); hubURL != "" {
			resp["agent_hub_url"] = hubURL
		}

		// Admin cross-user aggregation
		if wantsAllUsers(c) {
			grouped := svc.ListAllAgentsGrouped()
			userGroups := map[string]any{}
			for uid, agentList := range grouped {
				if uid == userID || uid == "" {
					continue
				}
				userGroups[uid] = map[string]any{"agents": agentList}
			}
			if len(userGroups) > 0 {
				resp["user_groups"] = userGroups
			}
		}

		return c.JSON(http.StatusOK, resp)
	}
}

func CreateAgentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := getUserID(c)
		var cfg state.AgentConfig
		if err := c.Bind(&cfg); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := svc.CreateAgentForUser(userID, &cfg); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, map[string]string{"status": "ok"})
	}
}

func GetAgentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := effectiveUserID(c)
		name := c.Param("name")
		ag := svc.GetAgentForUser(userID, name)
		if ag == nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Agent not found"})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"active": !ag.Paused(),
		})
	}
}

func UpdateAgentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := effectiveUserID(c)
		name := c.Param("name")
		var cfg state.AgentConfig
		if err := c.Bind(&cfg); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := svc.UpdateAgentForUser(userID, name, &cfg); err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

func DeleteAgentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := effectiveUserID(c)
		name := c.Param("name")
		if err := svc.DeleteAgentForUser(userID, name); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

func GetAgentConfigEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := effectiveUserID(c)
		name := c.Param("name")
		cfg := svc.GetAgentConfigForUser(userID, name)
		if cfg == nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Agent not found"})
		}
		return c.JSON(http.StatusOK, cfg)
	}
}

func PauseAgentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := effectiveUserID(c)
		if err := svc.PauseAgentForUser(userID, c.Param("name")); err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

func ResumeAgentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := effectiveUserID(c)
		if err := svc.ResumeAgentForUser(userID, c.Param("name")); err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

func GetAgentStatusEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := effectiveUserID(c)
		name := c.Param("name")
		history := svc.GetAgentStatusForUser(userID, name)
		if history == nil {
			history = &state.Status{ActionResults: []coreTypes.ActionState{}}
		}
		entries := []string{}
		for i := len(history.Results()) - 1; i >= 0; i-- {
			h := history.Results()[i]
			actionName := ""
			if h.ActionCurrentState.Action != nil {
				actionName = h.ActionCurrentState.Action.Definition().Name.String()
			}
			entries = append(entries, fmt.Sprintf("Reasoning: %s\nAction taken: %s\nParameters: %+v\nResult: %s",
				h.Reasoning,
				actionName,
				h.ActionCurrentState.Params,
				h.Result))
		}
		return c.JSON(http.StatusOK, map[string]any{
			"Name":    name,
			"History": entries,
		})
	}
}

func GetAgentObservablesEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := effectiveUserID(c)
		name := c.Param("name")
		history, err := svc.GetAgentObservablesForUser(userID, name)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"Name":    name,
			"History": history,
		})
	}
}

func ClearAgentObservablesEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := effectiveUserID(c)
		name := c.Param("name")
		if err := svc.ClearAgentObservablesForUser(userID, name); err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"Name": name, "cleared": true})
	}
}

func ChatWithAgentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := effectiveUserID(c)
		name := c.Param("name")
		var payload struct {
			Message string `json:"message"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request format"})
		}
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Message cannot be empty"})
		}
		messageID, err := svc.ChatForUser(userID, name, message)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusAccepted, map[string]any{
			"status":     "message_received",
			"message_id": messageID,
		})
	}
}

func AgentSSEEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := effectiveUserID(c)
		name := c.Param("name")
		manager := svc.GetSSEManagerForUser(userID, name)
		if manager == nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Agent not found"})
		}
		return services.HandleSSE(c, manager)
	}
}

type agentConfigMetaResponse struct {
	state.AgentConfigMeta
	OutputsDir string `json:"OutputsDir"`
}

func GetAgentConfigMetaEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		return c.JSON(http.StatusOK, agentConfigMetaResponse{
			AgentConfigMeta: svc.GetConfigMeta(),
			OutputsDir:      svc.OutputsDir(),
		})
	}
}

func ExportAgentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := effectiveUserID(c)
		name := c.Param("name")
		data, err := svc.ExportAgentForUser(userID, name)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		c.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.json", name))
		return c.JSONBlob(http.StatusOK, data)
	}
}

func ImportAgentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := getUserID(c)

		// Try multipart form file first
		file, err := c.FormFile("file")
		if err == nil {
			src, err := file.Open()
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "failed to open file"})
			}
			defer src.Close()
			data, err := io.ReadAll(src)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "failed to read file"})
			}
			if err := svc.ImportAgentForUser(userID, data); err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusCreated, map[string]string{"status": "ok"})
		}

		// Try JSON body
		var cfg state.AgentConfig
		if err := json.NewDecoder(c.Request().Body).Decode(&cfg); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request: provide a file or JSON body"})
		}
		data, err := json.Marshal(&cfg)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := svc.ImportAgentForUser(userID, data); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, map[string]string{"status": "ok"})
	}
}

// --- Actions ---

func ListActionsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		return c.JSON(http.StatusOK, map[string]any{
			"actions": svc.ListAvailableActions(),
		})
	}
}

func GetActionDefinitionEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		actionName := c.Param("name")

		var payload struct {
			Config map[string]string `json:"config"`
		}
		if err := json.NewDecoder(c.Request().Body).Decode(&payload); err != nil {
			payload.Config = map[string]string{}
		}

		def, err := svc.GetActionDefinition(actionName, payload.Config)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, def)
	}
}

func ExecuteActionEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		actionName := c.Param("name")

		var payload struct {
			Config map[string]string      `json:"config"`
			Params coreTypes.ActionParams `json:"params"`
		}
		if err := json.NewDecoder(c.Request().Body).Decode(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}

		result, err := svc.ExecuteAction(c.Request().Context(), actionName, payload.Config, payload.Params)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, result)
	}
}

func AgentFileEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()

		requestedPath := c.QueryParam("path")
		if requestedPath == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "no file path specified"})
		}

		// Resolve the real path (follows symlinks, eliminates ..)
		resolved, err := filepath.EvalSymlinks(filepath.Clean(requestedPath))
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "file not found"})
		}

		// Determine the allowed outputs directory — scoped to the user when auth is active
		allowedDir := svc.OutputsDir()
		user := auth.GetUser(c)
		if user != nil {
			allowedDir = filepath.Join(allowedDir, user.ID)
		}

		allowedDirResolved, _ := filepath.EvalSymlinks(filepath.Clean(allowedDir))

		if utils.InTrustedRoot(resolved, allowedDirResolved) != nil {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "access denied"})
		}

		info, err := os.Stat(resolved)
		if err != nil || info.IsDir() {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "file not found"})
		}

		return c.File(resolved)
	}
}
