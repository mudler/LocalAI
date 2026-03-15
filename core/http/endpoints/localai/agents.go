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
	"github.com/mudler/LocalAGI/core/state"
	coreTypes "github.com/mudler/LocalAGI/core/types"
	agiServices "github.com/mudler/LocalAGI/services"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/pkg/utils"
)

func ListAgentsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		statuses := svc.ListAgents()
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
		return c.JSON(http.StatusOK, resp)
	}
}

func CreateAgentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		var cfg state.AgentConfig
		if err := c.Bind(&cfg); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := svc.CreateAgent(&cfg); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, map[string]string{"status": "ok"})
	}
}

func GetAgentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		name := c.Param("name")
		ag := svc.GetAgent(name)
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
		name := c.Param("name")
		var cfg state.AgentConfig
		if err := c.Bind(&cfg); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := svc.UpdateAgent(name, &cfg); err != nil {
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
		name := c.Param("name")
		if err := svc.DeleteAgent(name); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

func GetAgentConfigEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		name := c.Param("name")
		cfg := svc.GetAgentConfig(name)
		if cfg == nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Agent not found"})
		}
		return c.JSON(http.StatusOK, cfg)
	}
}

func PauseAgentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		if err := svc.PauseAgent(c.Param("name")); err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

func ResumeAgentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		if err := svc.ResumeAgent(c.Param("name")); err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

func GetAgentStatusEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		name := c.Param("name")
		history := svc.GetAgentStatus(name)
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
		name := c.Param("name")
		history, err := svc.GetAgentObservables(name)
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
		name := c.Param("name")
		if err := svc.ClearAgentObservables(name); err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{"Name": name, "cleared": true})
	}
}

func ChatWithAgentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
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
		messageID, err := svc.Chat(name, message)
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
		name := c.Param("name")
		manager := svc.GetSSEManager(name)
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
		name := c.Param("name")
		data, err := svc.ExportAgent(name)
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
			if err := svc.ImportAgent(data); err != nil {
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
		if err := svc.ImportAgent(data); err != nil {
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

		// Only serve files from the outputs subdirectory
		outputsDir, _ := filepath.EvalSymlinks(filepath.Clean(svc.OutputsDir()))

		if utils.InTrustedRoot(resolved, outputsDir) != nil {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "access denied"})
		}

		info, err := os.Stat(resolved)
		if err != nil || info.IsDir() {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "file not found"})
		}

		return c.File(resolved)
	}
}
