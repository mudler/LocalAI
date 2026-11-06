package studio

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
)

// folderOut is the folder of an agent as the API shows it, or nil for an
// agent that works on all the user's files.
func folderOut(st studiosvc.AgentFolderState, ok bool) *schema.ChatsAgentFolder {
	if !ok {
		return nil
	}
	return &schema.ChatsAgentFolder{Path: st.Path, Name: st.Name, Mode: st.Mode, Effective: st.Effective, State: st.State}
}

// foldersIn is the folder choice of a write as a list: folders when sent, else
// the one folder. Nil means the body has none, which keeps the stored folders.
// {"folders": []} is an empty, non-nil list and clears them.
func foldersIn(req schema.ChatsAgentWrite) *[]studiosvc.AgentFolderWrite {
	if req.Folders == nil && req.Folder == nil {
		return nil
	}
	out := []studiosvc.AgentFolderWrite{}
	if req.Folders != nil {
		for _, f := range req.Folders {
			out = append(out, studiosvc.AgentFolderWrite{Path: f.Path, Mode: f.Mode})
		}
	} else if req.Folder.Path != "" {
		out = append(out, studiosvc.AgentFolderWrite{Path: req.Folder.Path, Mode: req.Folder.Mode})
	}
	return &out
}

// folderIn is the folder choice of a write, or nil when the body has none.
func folderIn(f *schema.ChatsAgentFolderWrite) *studiosvc.AgentFolderWrite {
	if f == nil {
		return nil
	}
	return &studiosvc.AgentFolderWrite{Path: f.Path, Mode: f.Mode}
}

// agentFull is agentOut with the folder resolved for the user.
func (h *Handlers) agentFull(c echo.Context, a agents.Agent) schema.ChatsAgent {
	out := agentOut(a)
	h.addFolders(c, &out, a, h.svc.AgentFolderRows(c.Request().Context(), userID(c), []agents.Agent{a})[a.ID])
	return out
}

// addFolders puts the folder rows of an agent, and the notice, in its output.
func (h *Handlers) addFolders(c echo.Context, out *schema.ChatsAgent, a agents.Agent, rows []studiosvc.AgentFolderState) {
	if len(rows) == 0 {
		return
	}
	out.Folder = folderOut(rows[0], true)
	for _, r := range rows {
		out.Folders = append(out.Folders, *folderOut(r, true))
	}
	if n := h.svc.SharedNotMounted(c.Request().Context(), userID(c), a); n > 0 {
		out.FolderNotice = &schema.ChatsAgentFolderNotice{NotMounted: n}
	}
}

func agentOut(a agents.Agent) schema.ChatsAgent {
	nz := func(l []string) []string {
		if l == nil {
			return []string{}
		}
		return l
	}
	return schema.ChatsAgent{ID: a.ID, Name: a.Name, Description: a.Description, Instructions: a.Instructions, Model: a.Model,
		MCPServers: nz(a.MCPServers), Addons: nz(a.Addons), Collections: nz(a.Collections), Skills: nz(a.Skills),
		Network: networkOf(a.Network), Memory: a.Memory, MemorySave: a.MemorySave, BuiltinsOff: nz(a.BuiltinsOff), BuiltinsOn: nz(a.BuiltinsOn), Builtins: nz(a.Builtins), Connections: a.Connections, Resources: a.Resources, Created: a.Created, Updated: a.Updated}
}

// agentIn reads the body. A model the user may not use is refused here, so a
// saved agent never names one the service would refuse at the first turn.
// When ok is false the refusal is already written and err is the write result,
// so the caller returns err without writing a second body.
func (h *Handlers) agentIn(c echo.Context) (a agents.Agent, folders *[]studiosvc.AgentFolderWrite, ok bool, err error) {
	var req schema.ChatsAgentWrite
	if err := c.Bind(&req); err != nil {
		return a, nil, false, fail(c, http.StatusBadRequest, "invalid_request_error", "the body is not valid JSON")
	}
	if req.Model != "" && h.models != nil {
		switch err := h.models.Check(c.Request().Context(), auth.GetUser(c), req.Model); {
		case errors.Is(err, ErrUnknownModel):
			return a, nil, false, fail(c, http.StatusBadRequest, "invalid_request_error", "unknown model")
		case errors.Is(err, ErrModelNotAllowed):
			return a, nil, false, fail(c, http.StatusForbidden, "authorization_error", "you may not use this model")
		case err != nil:
			return a, nil, false, failFor(c, err)
		}
	}
	return agents.Agent{Name: req.Name, Description: req.Description, Instructions: req.Instructions, Model: req.Model,
			MCPServers: req.MCPServers, Addons: req.Addons, Collections: req.Collections, Skills: req.Skills, Network: req.Network, Memory: req.Memory, MemorySave: req.MemorySave,
			BuiltinsOff: req.BuiltinsOff, BuiltinsOn: req.BuiltinsOn, Builtins: req.Builtins, Connections: req.Connections, Resources: req.Resources},
		foldersIn(req), true, nil
}

// networkOf is the network preset of an agent as the API shows it: off when
// none was set.
func networkOf(n string) string {
	if n == "" {
		return "off"
	}
	return n
}

// ListAgents lists the saved agents.
// @Summary List your saved agents.
// @Tags chats
// @Success 200 {object} schema.ChatsAgentList "Your agents, sorted by name"
// @Router /api/chats/agents [get]
func (h *Handlers) ListAgents(c echo.Context) error {
	list, err := h.svc.Agents(c.Request().Context(), userID(c))
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsAgentList{Agents: make([]schema.ChatsAgent, 0, len(list))}
	rows := h.svc.AgentFolderRows(c.Request().Context(), userID(c), list)
	for _, a := range list {
		o := agentOut(a)
		h.addFolders(c, &o, a, rows[a.ID])
		out.Agents = append(out.Agents, o)
	}
	return c.JSON(http.StatusOK, out)
}

// CreateAgent saves a new agent.
// @Summary Save an agent.
// @Description An agent is a definition: instructions and exact lists of the MCP servers, add-ons, collections and skills a chat with it may use. A chat gets what the lists name and you may use at that moment, never more. Start a chat with it by passing agent_id to POST /api/chats. folders [{path, mode}] gives the agent up to 8 folders of your files, each read or write (folder {path, mode} is the same with one entry); without them the agent works on all your files.
// @Tags chats
// @Param request body schema.ChatsAgentWrite true "the agent"
// @Success 201 {object} schema.ChatsAgent "The saved agent"
// @Failure 400 {object} schema.ErrorResponse "The definition or the folder is not valid"
// @Router /api/chats/agents [post]
func (h *Handlers) CreateAgent(c echo.Context) error {
	a, folders, ok, err := h.agentIn(c)
	if !ok {
		return err
	}
	saved, err := h.svc.SaveAgentWithFolders(c.Request().Context(), userID(c), a, folders)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusCreated, h.agentFull(c, saved))
}

// GetAgent reads a saved agent.
// @Summary Read a saved agent.
// @Tags chats
// @Param id path string true "the agent's id"
// @Success 200 {object} schema.ChatsAgent "The agent"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/agents/{id} [get]
func (h *Handlers) GetAgent(c echo.Context) error {
	a, err := h.svc.Agent(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, h.agentFull(c, a))
}

// UpdateAgent replaces a saved agent.
// @Summary Replace a saved agent.
// @Description The body replaces every field but folder: a body without folder or folders keeps the folders the agent has, so a client that does not know the fields cannot widen the agent, and folders [] (or folder {path: ""}) clears them. Chats that already run with the agent use the new definition from their next turn.
// @Tags chats
// @Param id path string true "the agent's id"
// @Param request body schema.ChatsAgentWrite true "the agent"
// @Success 200 {object} schema.ChatsAgent "The saved agent"
// @Failure 400 {object} schema.ErrorResponse "The definition or the folder is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/agents/{id} [put]
func (h *Handlers) UpdateAgent(c echo.Context) error {
	a, folders, ok, err := h.agentIn(c)
	if !ok {
		return err
	}
	a.ID = c.Param("id")
	saved, err := h.svc.SaveAgentWithFolders(c.Request().Context(), userID(c), a, folders)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, h.agentFull(c, saved))
}

// DeleteAgent deletes a saved agent.
// @Summary Delete a saved agent.
// @Description Chats that ran with it keep their history; their next turn runs without the agent's tools. The agent's own memory notes (Memory/agents/{id}) are not part of it: memory says what to do with them. keep (the default) leaves them, listed under Deleted agents on the Memory page until you delete them. delete moves them to the Trash. export answers 200 with a zip file of the notes and then moves them to the Trash (204 when the agent has none). A value that is not one of these is a 400 and nothing is deleted.
// @Tags chats
// @Param id path string true "the agent's id"
// @Param memory query string false "what to do with the agent's own notes" Enums(keep, export, delete) default(keep)
// @Success 204 "Deleted"
// @Success 200 {file} file "Deleted; a zip file of the agent's notes (memory=export)"
// @Failure 400 {object} schema.ErrorResponse "memory is not keep, export or delete"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/agents/{id} [delete]
func (h *Handlers) DeleteAgent(c echo.Context) error {
	zipped, err := h.svc.DeleteAgentWithNotes(c.Request().Context(), userID(c), c.Param("id"), c.QueryParam("memory"))
	if err != nil {
		return failFor(c, err)
	}
	if zipped != nil {
		c.Response().Header().Set(echo.HeaderContentDisposition, `attachment; filename="agent-memory-notes.zip"`)
		return c.Blob(http.StatusOK, "application/zip", zipped)
	}
	return c.NoContent(http.StatusNoContent)
}
