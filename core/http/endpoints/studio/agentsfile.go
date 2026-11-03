package studio

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
)

const (
	agentExportKind    = "localai-agent"
	maxAgentImportBody = 1 << 20
	// maxAgentFileVersion is the newest version of the agent file this server
	// reads. Version 2 is an agent with a folder, version 3 an agent with
	// several: each got its own version so that an older server refuses the
	// file instead of importing the agent with more access than it had.
	// Version 4 is an agent with switches of built-in tool groups, for the
	// same reason: an older server would drop them and give the agent every
	// tool.
	maxAgentFileVersion = 4
)

// agentFileVersion is the version an export writes: 1 for an agent without a
// folder or tool switches, so older servers keep importing it, 2 for one with
// one folder, 3 for one with several, and 4 for one with tool switches.
func agentFileVersion(folders int, hasSwitches bool) int {
	switch {
	case hasSwitches:
		return 4
	case folders > 1:
		return 3
	case folders == 1:
		return 2
	}
	return 1
}

// ExportAgent returns a saved agent as a file.
// @Summary Export a saved agent.
// @Description Returns the definition as a JSON file (version, kind localai-agent, agent). It holds no ID, owner or time. An agent with one folder is version 2 and carries folder {path, mode}; an agent with several is version 3 and carries folders [{path, mode}], the folder's home-relative path now (empty when the folder is missing, not chosen, or outside your home, even if readable); importing an empty path preserves the scope as unset with a warning; a server that knows only version 1 refuses it. An agent with switches of built-in tool groups (builtins_off, builtins_on) is version 4. An agent without a folder or switches is version 1. The builtins list (builtin grants) is an extra field of any version: a server that does not know it ignores it, so the agent comes back without the grants. POST /api/chats/agents/import reads it. This replaces the old /api/agents/{name}/export.
// @Tags chats
// @Param id path string true "the agent's id"
// @Success 200 {object} schema.ChatsAgentExport "The agent file"
// @Router /api/chats/agents/{id}/export [get]
func (h *Handlers) ExportAgent(c echo.Context) error {
	a, err := h.svc.Agent(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	w := schema.ChatsAgentWrite{Name: a.Name, Description: a.Description, Instructions: a.Instructions, Model: a.Model,
		MCPServers: a.MCPServers, Addons: a.Addons, Collections: a.Collections, Skills: a.Skills, Network: a.Network,
		BuiltinsOff: a.BuiltinsOff, BuiltinsOn: a.BuiltinsOn, Builtins: a.Builtins}
	rows := h.svc.AgentFolderRows(c.Request().Context(), userID(c), []agents.Agent{a})[a.ID]
	for i, f := range a.FolderList() {
		// Imports resolve paths in the home only. A followed root outside
		// it must stay scoped but unset, not bind to a same-spelled home path.
		exportPath := ""
		if abs, live := h.svc.FolderResolver().PathOf(c.Request().Context(), h.svc.Principal(userID(c)), f.Node); live {
			exportPath = h.svc.Files().Rel(h.svc.Principal(userID(c)), abs)
		}
		mode := f.Mode
		if i < len(rows) {
			mode = rows[i].Mode
		}
		w.Folders = append(w.Folders, schema.ChatsAgentFolderWrite{Path: exportPath, Mode: mode})
	}
	if len(w.Folders) == 1 {
		// One folder stays in the version 2 shape, which older servers read.
		w.Folder, w.Folders = &w.Folders[0], nil
	}
	c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf("attachment; filename=%q", fileSafe(a.Name)+".json"))
	return c.JSON(http.StatusOK, schema.ChatsAgentExport{Version: agentFileVersion(len(a.FolderList()), len(a.BuiltinsOff)+len(a.BuiltinsOn) > 0), Kind: agentExportKind, ExportedAt: time.Now().UTC(), Agent: w})
}

// fileSafe keeps a name usable as a file name in a header.
func fileSafe(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "agent"
	}
	return b.String()
}

// ImportAgent saves an agent from a file.
// @Summary Import a saved agent.
// @Description Send the export file (version 1, 2 for an agent with a folder, or 3 for an agent with several) as the JSON body, or as the file field of a multipart form. A bare agent definition (the body of POST /api/chats/agents) is accepted too. The agent is always new: a name you already use gets a number, and a model this server does not have, or you may not use, is dropped with a warning. A folder is looked up by its path in your files; when you have no such folder the agent keeps the mode without a folder (folder state unset) and does not start until you choose one, and a warning says so.
// @Tags chats
// @Accept json,mpfd
// @Param request body schema.ChatsAgentExport true "the agent file"
// @Param file formData file false "the agent file (multipart)"
// @Success 201 {object} schema.ChatsAgentImportResult "The new agent and what the import changed"
// @Router /api/chats/agents/import [post]
func (h *Handlers) ImportAgent(c echo.Context) error {
	var raw []byte
	if strings.HasPrefix(c.Request().Header.Get("Content-Type"), "multipart/form-data") {
		fh, err := c.FormFile("file")
		if err != nil {
			return fail(c, http.StatusBadRequest, "invalid_request_error", "send the agent file as the file field")
		}
		f, err := fh.Open()
		if err != nil {
			return fail(c, http.StatusBadRequest, "invalid_request_error", "the file could not be read")
		}
		defer func() { _ = f.Close() }()
		if raw, err = io.ReadAll(io.LimitReader(f, maxAgentImportBody+1)); err != nil {
			return fail(c, http.StatusBadRequest, "invalid_request_error", "the file could not be read")
		}
	} else {
		var err error
		if raw, err = io.ReadAll(io.LimitReader(c.Request().Body, maxAgentImportBody+1)); err != nil {
			return fail(c, http.StatusBadRequest, "invalid_request_error", "the body could not be read")
		}
	}
	if len(raw) > maxAgentImportBody {
		return fail(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "the agent file is larger than 1 MiB")
	}
	def, err := readAgentFile(raw)
	if err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	var warnings []string
	if def.Model != "" && h.models != nil {
		switch merr := h.models.Check(c.Request().Context(), auth.GetUser(c), def.Model); {
		case errors.Is(merr, ErrUnknownModel), errors.Is(merr, ErrModelNotAllowed):
			warnings = append(warnings, fmt.Sprintf("the model %q is not available to you here, so the agent has no model", def.Model))
			def.Model = ""
		case merr != nil:
			return failFor(c, merr)
		}
	}
	a := agents.Agent{Name: def.Name, Description: def.Description, Instructions: def.Instructions, Model: def.Model,
		MCPServers: def.MCPServers, Addons: def.Addons, Collections: def.Collections, Skills: def.Skills, Network: def.Network,
		BuiltinsOff: def.BuiltinsOff, BuiltinsOn: def.BuiltinsOn, Builtins: def.Builtins}
	var rowsIn []studiosvc.AgentFolderWrite
	switch {
	case def.Folders != nil:
		for _, f := range def.Folders {
			rowsIn = append(rowsIn, studiosvc.AgentFolderWrite{Path: f.Path, Mode: f.Mode})
		}
	case def.Folder != nil:
		rowsIn = []studiosvc.AgentFolderWrite{{Path: def.Folder.Path, Mode: def.Folder.Mode}}
	}
	saved, err := h.svc.ImportAgentFolders(c.Request().Context(), userID(c), a, rowsIn)
	if err != nil {
		return failFor(c, err)
	}
	if saved.Name != strings.TrimSpace(def.Name) {
		warnings = append(warnings, fmt.Sprintf("you already have an agent named %q, so the new one is named %q", strings.TrimSpace(def.Name), saved.Name))
	}
	for i, f := range saved.FolderList() {
		if f.Node != 0 {
			continue
		}
		if rowsIn[i].Path != "" {
			warnings = append(warnings, fmt.Sprintf("The folder %s was not found. Choose a folder before you use this agent.", rowsIn[i].Path))
		} else {
			warnings = append(warnings, "The file names no folder. Choose a folder before you use this agent.")
		}
	}
	return c.JSON(http.StatusCreated, schema.ChatsAgentImportResult{Agent: h.agentFull(c, saved), Warnings: warnings})
}

// readAgentFile reads an export file, or a bare agent definition.
func readAgentFile(raw []byte) (schema.ChatsAgentWrite, error) {
	return readAgentFileUpTo(raw, maxAgentFileVersion)
}

// readAgentFileUpTo reads an export file of version 1 to newest, or a bare
// definition. With newest 1 it is the check of a server from before folders.
func readAgentFileUpTo(raw []byte, newest int) (schema.ChatsAgentWrite, error) {
	var doc struct {
		Version int                     `json:"version"`
		Kind    string                  `json:"kind"`
		Agent   *schema.ChatsAgentWrite `json:"agent"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return schema.ChatsAgentWrite{}, errors.New("the body is not a valid agent file")
	}
	if doc.Agent != nil {
		if doc.Version < 1 || doc.Version > newest || (doc.Kind != "" && doc.Kind != agentExportKind) {
			return schema.ChatsAgentWrite{}, errors.New("this agent file has a version or kind this server does not know")
		}
		return *doc.Agent, nil
	}
	var bare schema.ChatsAgentWrite
	if err := json.Unmarshal(raw, &bare); err != nil || strings.TrimSpace(bare.Name) == "" {
		return schema.ChatsAgentWrite{}, errors.New("the body is not a valid agent file")
	}
	return bare, nil
}

// AgentTemplates lists the starter agents.
// @Summary List the starter agents.
// @Description Ready-made definitions to start from. They grant nothing; save one, then grant it tools. POST /api/chats/agents/templates/{id}/create saves one for you.
// @Tags chats
// @Success 200 {object} schema.ChatsAgentTemplateList "The starter agents"
// @Router /api/chats/agents/templates [get]
func (h *Handlers) AgentTemplates(c echo.Context) error {
	out := schema.ChatsAgentTemplateList{Templates: []schema.ChatsAgentTemplate{}}
	for _, t := range agents.Templates() {
		out.Templates = append(out.Templates, schema.ChatsAgentTemplate{ID: t.ID, Name: t.Name, Description: t.Description, Instructions: t.Instructions})
	}
	return c.JSON(http.StatusOK, out)
}

// BuiltinGrants lists the builtin grants an agent can be given.
// @Summary List the builtin grants of a saved agent.
// @Description Each grant has a name (the value for builtins in an agent), a title, a description and sensitive. A sensitive grant changes what the agent may do or reach later (profile, skills, pages, dashboard, new channels, actions of unattended runs); none is on by default. A chat of the agent holds only the grants you may use at that moment.
// @Tags chats
// @Success 200 {object} schema.ChatsBuiltinGrantList "The grants, in display order"
// @Router /api/chats/agents/builtin-grants [get]
func (h *Handlers) BuiltinGrants(c echo.Context) error {
	out := schema.ChatsBuiltinGrantList{Grants: []schema.ChatsBuiltinGrant{}}
	for _, g := range h.svc.BuiltinGrants() {
		out.Grants = append(out.Grants, schema.ChatsBuiltinGrant{Name: g.Name, Title: g.Title, Description: g.Description, Sensitive: g.Sensitive})
	}
	return c.JSON(http.StatusOK, out)
}

// CreateAgentFromTemplate saves a starter agent.
// @Summary Save a starter agent as your own.
// @Tags chats
// @Param id path string true "the template's id"
// @Param request body schema.ChatsAgentTemplateUse false "a name to use instead of the template's"
// @Success 201 {object} schema.ChatsAgent "The saved agent"
// @Router /api/chats/agents/templates/{id}/create [post]
func (h *Handlers) CreateAgentFromTemplate(c echo.Context) error {
	var req schema.ChatsAgentTemplateUse
	if c.Request().ContentLength != 0 {
		if err := c.Bind(&req); err != nil {
			return fail(c, http.StatusBadRequest, "invalid_request_error", "the body is not valid JSON")
		}
	}
	a, err := h.svc.CreateAgentFromTemplate(c.Request().Context(), userID(c), c.Param("id"), req.Name)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusCreated, agentOut(a))
}

// AgentRuns returns the run history of a saved agent.
// @Summary Show what an agent has run.
// @Description Lists your chats that ran with the agent, newest first: the ones you had, the ones its triggers started and the ones its connectors started, with how each ended. Open one with GET /api/chats/{chat_id}.
// @Tags chats
// @Param id path string true "the agent's id"
// @Param limit query int false "most runs to list (default 50, at most 200)"
// @Success 200 {object} schema.ChatsAgentHistory "The runs and the counts"
// @Router /api/chats/agents/{id}/runs [get]
func (h *Handlers) AgentRuns(c echo.Context) error {
	hist, err := h.svc.AgentRuns(c.Request().Context(), userID(c), c.Param("id"), intParam(c, "limit", defaultListLimit, maxListLimit))
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsAgentHistory{Runs: make([]schema.ChatsAgentRun, 0, len(hist.Runs)), Total: hist.Total, Failed: hist.Failed, Running: hist.Running}
	for _, r := range hist.Runs {
		out.Runs = append(out.Runs, schema.ChatsAgentRun{ChatID: r.Chat.ID, Title: r.Chat.Title, Model: r.Chat.Model, Origin: r.Chat.Origin,
			OriginID: r.Chat.OriginID, Created: r.Chat.Created, Updated: r.Chat.Updated, Running: r.Turn.Running, LastTurn: string(r.Turn.Last)})
	}
	return c.JSON(http.StatusOK, out)
}
