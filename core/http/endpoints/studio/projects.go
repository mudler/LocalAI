package studio

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const (
	// projectChatCountCap bounds the chats counted for ChatCount, so a list of
	// projects stays cheap.
	projectChatCountCap = 500
	// maxMemoryRead is the largest memory index the API reads. A person can
	// write a bigger one by hand; the API does not return more than this.
	maxMemoryRead = 256 << 10
	// maxMemoryBody is the most bytes of a request body the memory write reads.
	// JSON escapes a control character as six bytes, so a note at the cap still
	// fits.
	maxMemoryBody = 6*contract.MaxChangeBytes + 1024
)

// agentCounts counts the user's agents by project id with one read. When the
// agents are off or the read fails every count is zero.
func (h *Handlers) agentCounts(ctx context.Context, user string) map[string]int {
	counts, err := h.svc.AgentCountsByProject(ctx, user)
	if err != nil {
		xlog.Warn("studio: project agents not counted", "error", err)
		return nil
	}
	return counts
}

func (h *Handlers) projectsHost(c echo.Context) (*projects.Service, bool) {
	return feature(c, h.svc.Projects(), func(c echo.Context) error {
		return fail(c, http.StatusConflict, "projects_off", "projects are not available")
	})
}

// chatsAllowed checks the chats feature for a project list whose route is
// gated by the feature of the objects it lists (agents, connectors, triggers).
// The project page is part of the chats, so a person needs both. On failure it
// has written the response and returns false.
func chatsAllowed(c echo.Context) bool {
	if auth.FeatureAllowed(c, auth.FeatureChats) {
		return true
	}
	_ = fail(c, http.StatusForbidden, typeAuthz, "feature not enabled for your account: "+auth.FeatureChats)
	return false
}

var projectRules = []errRule{
	fixed(http.StatusNotFound, typeNotFound, "project not found", projects.ErrNotFound),
	verbatim(http.StatusBadRequest, typeInvalid, projects.ErrInvalid.Error()+": ", projects.ErrInvalid),
	verbatim(http.StatusConflict, typeConflict, "", projects.ErrNameTaken),
	fixed(http.StatusConflict, typeConflict, "the project changed while it was saved; try again", projects.ErrConflict),
	verbatim(http.StatusForbidden, typeAuthz, "", projects.ErrForbidden),
}

func failProject(c echo.Context, err error) error { return mapErr(c, err, projectRules, failFor) }

func defaultsOut(d projects.Defaults) schema.ChatsProjectDefaults {
	return schema.ChatsProjectDefaults{Skills: d.Skills, Addons: d.Addons, Connectors: d.Connectors, MCPServers: d.MCPServers, Collections: d.Collections}
}

func defaultsIn(d schema.ChatsProjectDefaults) projects.Defaults {
	return projects.Defaults{Skills: d.Skills, Addons: d.Addons, Connectors: d.Connectors, MCPServers: d.MCPServers, Collections: d.Collections}
}

// projectOut describes one project. The description and the defaults come from
// the manifest, so a project that cannot be read is listed without them and
// with the status the index holds. ChatCount counts up to projectChatCountCap.
// agentCounts is the number of agents by project id, read once by the caller.
func (h *Handlers) projectOut(ctx context.Context, host *projects.Service, user string, p projects.Project, agentCounts map[string]int) schema.ChatsProject {
	out := schema.ChatsProject{ID: p.ID, Name: p.Name, Slug: p.Slug, Status: string(p.Status), Error: p.Error, Updated: p.Updated, AgentCount: agentCounts[p.ID]}
	if r, err := host.Get(ctx, h.svc.Principal(user), p.ID); err == nil {
		out.Description = r.Manifest.Description
		out.Defaults = defaultsOut(r.Manifest.Defaults)
		out.Attached = defaultsOut(r.Manifest.Attached)
		out.AttachedBlocked = r.AttachedBlocked
	} else {
		xlog.Debug("studio: project manifest not read for the list", "project", p.ID, "error", err)
	}
	if n, err := h.svc.CountChatsInProject(ctx, user, p.ID, projectChatCountCap); err == nil {
		out.ChatCount = n
	} else {
		xlog.Debug("studio: project chats not counted", "project", p.ID, "error", err)
	}
	return out
}

// ListProjects lists your projects.
// @Summary List your projects.
// @Description A project is a folder under Projects/ in your files, with a manifest, a memory and defaults that its chats start with. The list is read from the folders, so a project you added or edited by hand shows up. chat_count counts up to 500 chats; agent_count is the number of your agents that belong to the project. Another user's projects are never listed.
// @Tags chats
// @Success 200 {object} schema.ChatsProjectList "Your projects, sorted by name"
// @Failure 409 {object} schema.ErrorResponse "Projects are not available (projects_off)"
// @Router /api/chats/projects [get]
func (h *Handlers) ListProjects(c echo.Context) error {
	host, ok := h.projectsHost(c)
	if !ok {
		return nil
	}
	user := userID(c)
	list, err := host.List(c.Request().Context(), h.svc.Principal(user))
	if err != nil {
		return failProject(c, err)
	}
	out := schema.ChatsProjectList{Projects: make([]schema.ChatsProject, 0, len(list))}
	counts := h.agentCounts(c.Request().Context(), user)
	for _, p := range list {
		out.Projects = append(out.Projects, h.projectOut(c.Request().Context(), host, user, p, counts))
	}
	return c.JSON(http.StatusOK, out)
}

// CreateProject creates a project.
// @Summary Create a project.
// @Description Makes the folder Projects/<slug> in your files with its manifest and an empty memory, as one change set that one undo removes. The slug comes from the name and is unique among your projects, so two projects can share a name but not a folder name.
// @Tags chats
// @Param request body schema.ChatsProjectWrite true "the project"
// @Success 201 {object} schema.ChatsProject "The project"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 409 {object} schema.ErrorResponse "The name is taken, or projects are not available (projects_off)"
// @Router /api/chats/projects [post]
func (h *Handlers) CreateProject(c echo.Context) error {
	host, ok := h.projectsHost(c)
	if !ok {
		return nil
	}
	req, ok := bind[schema.ChatsProjectWrite](c, msgNotJSON)
	if !ok {
		return nil
	}
	in := projects.CreateInput{}
	if req.Name != nil {
		in.Name = *req.Name
	}
	if req.Description != nil {
		in.Description = *req.Description
	}
	if req.Defaults != nil {
		in.Defaults = defaultsIn(*req.Defaults)
	}
	if req.Attached != nil {
		in.Attached = defaultsIn(*req.Attached)
	}
	user := userID(c)
	p, err := host.Create(c.Request().Context(), h.svc.Principal(user), in)
	if err != nil {
		return failProject(c, err)
	}
	return c.JSON(http.StatusCreated, h.projectOut(c.Request().Context(), host, user, p, h.agentCounts(c.Request().Context(), user)))
}

// GetProject reads a project.
// @Summary Read a project.
// @Description skills_blocked is true when the project's own skills (the skills folder of the project) are off because another person may write the project folder. attached_blocked is true for the same reason when the project file holds an attached block: the block is ignored and attached comes back empty.
// @Tags chats
// @Param id path string true "the project's id"
// @Success 200 {object} schema.ChatsProject "The project"
// @Failure 404 {object} schema.ErrorResponse "No such project, or it is another user's"
// @Router /api/chats/projects/{id} [get]
func (h *Handlers) GetProject(c echo.Context) error {
	host, ok := h.projectsHost(c)
	if !ok {
		return nil
	}
	user := userID(c)
	r, err := host.Get(c.Request().Context(), h.svc.Principal(user), c.Param("id"))
	if err != nil {
		return failProject(c, err)
	}
	out := h.projectOut(c.Request().Context(), host, user, r.Project, h.agentCounts(c.Request().Context(), user))
	out.SkillsBlocked = h.svc.ProjectSkillsBlocked(c.Request().Context(), user, r.ID)
	return c.JSON(http.StatusOK, out)
}

// UpdateProject changes a project.
// @Summary Change a project.
// @Description A field that is left out keeps its value. The folder keeps its name. The defaults are five lists: skills, add-ons, connectors, MCP servers and collections. For each list, a non-empty value means a new chat in the project has only those on; an empty or missing list is no override. Defaults apply to new chats only; the agents of the project are also limited by them. Attached is five lists of the same shape: the tools the project holds. Every chat of the project, new or old, and every agent of it has them on at each turn, with no per-chat step. A chat cannot switch an attached tool off; detach it from the project. The tools are the owner's own, so a person the project is shared with never gets them, and a chat that starts from a messaging connection gets only the attached skills and collections. The attached block is ignored while another person may write the project folder (attached_blocked on a read). A non-empty defaults list is a limit, and an attached entry outside it, an empty or malformed entry, or a connector in the MCP list is refused with a 400 that names the tool and the limit.
// @Tags chats
// @Param id path string true "the project's id"
// @Param request body schema.ChatsProjectWrite true "the fields to change"
// @Success 200 {object} schema.ChatsProject "The project"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such project, or it is another user's"
// @Failure 409 {object} schema.ErrorResponse "The project changed while it was saved"
// @Router /api/chats/projects/{id} [patch]
func (h *Handlers) UpdateProject(c echo.Context) error {
	host, ok := h.projectsHost(c)
	if !ok {
		return nil
	}
	req, ok := bind[schema.ChatsProjectWrite](c, msgNotJSON)
	if !ok {
		return nil
	}
	in := projects.UpdateInput{Name: req.Name, Description: req.Description}
	if req.Defaults != nil {
		d := defaultsIn(*req.Defaults)
		in.Defaults = &d
	}
	if req.Attached != nil {
		a := defaultsIn(*req.Attached)
		in.Attached = &a
	}
	user := userID(c)
	p, err := host.Update(c.Request().Context(), h.svc.Principal(user), c.Param("id"), in)
	if err != nil {
		return failProject(c, err)
	}
	return c.JSON(http.StatusOK, h.projectOut(c.Request().Context(), host, user, p, h.agentCounts(c.Request().Context(), user)))
}

// DeleteProject deletes a project.
// @Summary Delete a project.
// @Description Removes the project folder, with its files and memory, as one change set that one undo restores. Chats that belonged to it stay and read as having no project.
// @Tags chats
// @Param id path string true "the project's id"
// @Success 204 "Deleted"
// @Failure 404 {object} schema.ErrorResponse "No such project, or it is another user's"
// @Router /api/chats/projects/{id} [delete]
func (h *Handlers) DeleteProject(c echo.Context) error {
	host, ok := h.projectsHost(c)
	if !ok {
		return nil
	}
	if err := host.Delete(c.Request().Context(), h.svc.Principal(userID(c)), c.Param("id")); err != nil {
		return failProject(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handlers) setProjectArchived(c echo.Context, archive bool) error {
	host, ok := h.projectsHost(c)
	if !ok {
		return nil
	}
	user := userID(c)
	do := host.Restore
	if archive {
		do = host.Archive
	}
	p, err := do(c.Request().Context(), h.svc.Principal(user), c.Param("id"))
	if err != nil {
		return failProject(c, err)
	}
	return c.JSON(http.StatusOK, h.projectOut(c.Request().Context(), host, user, p, h.agentCounts(c.Request().Context(), user)))
}

// ArchiveProject archives a project.
// @Summary Archive a project.
// @Description An archived project keeps its folder and takes no new chats.
// @Tags chats
// @Param id path string true "the project's id"
// @Success 200 {object} schema.ChatsProject "The project"
// @Failure 404 {object} schema.ErrorResponse "No such project, or it is another user's"
// @Failure 409 {object} schema.ErrorResponse "The project changed while it was saved"
// @Router /api/chats/projects/{id}/archive [post]
func (h *Handlers) ArchiveProject(c echo.Context) error { return h.setProjectArchived(c, true) }

// RestoreProject restores an archived project.
// @Summary Restore an archived project.
// @Tags chats
// @Param id path string true "the project's id"
// @Success 200 {object} schema.ChatsProject "The project"
// @Failure 404 {object} schema.ErrorResponse "No such project, or it is another user's"
// @Failure 409 {object} schema.ErrorResponse "The project changed while it was saved"
// @Router /api/chats/projects/{id}/restore [post]
func (h *Handlers) RestoreProject(c echo.Context) error { return h.setProjectArchived(c, false) }

// GetProjectMemory reads the memory of a project.
// @Summary Read the memory of a project.
// @Description Returns the text of index.md and the topic files next to it. The memory is the folder memory/ of the project.
// @Tags chats
// @Param id path string true "the project's id"
// @Success 200 {object} schema.ChatsProjectMemory "The index and the topic files"
// @Failure 404 {object} schema.ErrorResponse "No such project, or it is another user's"
// @Router /api/chats/projects/{id}/memory [get]
func (h *Handlers) GetProjectMemory(c echo.Context) error {
	host, ok := h.projectsHost(c)
	if !ok {
		return nil
	}
	ctx := c.Request().Context()
	p := h.svc.Principal(userID(c))
	r, err := host.Get(ctx, p, c.Param("id"))
	if err != nil {
		return failProject(c, err)
	}
	f := h.svc.Files()
	out := schema.ChatsProjectMemory{Files: []schema.ChatsProjectMemoryFile{}}
	text, _, err := f.ReadHome(ctx, p, r.MemoryPath+"/index.md", maxMemoryRead)
	if err != nil {
		return failFile(c, err)
	}
	out.Index = text
	abs, err := f.Abs(p, r.MemoryPath)
	if err != nil {
		return failFile(c, err)
	}
	kids, err := f.Store().List(ctx, f.Actor(p), abs)
	if err != nil && !errors.Is(err, workspace.ErrNotFound) {
		return failFile(c, err)
	}
	for _, k := range kids {
		if k.Name == "index.md" || !proposals.TopicFile(k.Name) {
			continue
		}
		out.Files = append(out.Files, schema.ChatsProjectMemoryFile{Name: k.Name, Size: k.Size, Modified: k.Modified})
	}
	slices.SortFunc(out.Files, func(a, b schema.ChatsProjectMemoryFile) int { return strings.Compare(a.Name, b.Name) })
	return c.JSON(http.StatusOK, out)
}

// PutProjectMemory writes one memory file of a project.
// @Summary Write a memory file of a project.
// @Description This is your own edit, so it is written as you, as one change set that undo restores. file is a name such as index.md or food.md: lowercase letters, digits, dot, dash and underscore, ending in .md, at most 64 characters. The content is at most 16 KiB, the same as a note the model proposes. The edit is accepted for an archived project too.
// @Tags chats
// @Param id path string true "the project's id"
// @Param file path string true "the file name, for example index.md"
// @Param request body schema.ChatsProjectMemoryWrite true "the new content"
// @Success 204 "Written"
// @Failure 400 {object} schema.ErrorResponse "The file name or the body is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such project, or it is another user's"
// @Failure 413 {object} schema.ErrorResponse "The content is too large"
// @Router /api/chats/projects/{id}/memory/{file} [put]
func (h *Handlers) PutProjectMemory(c echo.Context) error {
	host, ok := h.projectsHost(c)
	if !ok {
		return nil
	}
	ctx := c.Request().Context()
	p := h.svc.Principal(userID(c))
	r, err := host.Get(ctx, p, c.Param("id"))
	if err != nil {
		return failProject(c, err)
	}
	file := c.Param("file")
	if !proposals.TopicFile(file) {
		return fail(c, http.StatusBadRequest, typeInvalid, "a memory file is a name such as index.md or food.md")
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxMemoryBody)
	req, ok := bind[schema.ChatsProjectMemoryWrite](c, msgNotJSON)
	if !ok {
		return nil
	}
	if len(req.Content) > contract.MaxChangeBytes {
		return fail(c, http.StatusRequestEntityTooLarge, typeInvalid, "the file is too large")
	}
	if _, err := h.svc.Files().WriteHome(ctx, p, r.MemoryPath+"/"+file, req.Content, "Edit the memory of the project "+r.Name, contract.MaxChangeBytes); err != nil {
		return failFile(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// ListProjectChats lists the chats of a project.
// @Summary List the chats of a project.
// @Description Your chats whose project is this one, newest first. limit defaults to 50 and is at most 200.
// @Tags chats
// @Param id path string true "the project's id"
// @Param limit query int false "most chats to return"
// @Success 200 {object} schema.ChatsConversationList "The chats"
// @Failure 404 {object} schema.ErrorResponse "No such project, or it is another user's"
// @Router /api/chats/projects/{id}/chats [get]
func (h *Handlers) ListProjectChats(c echo.Context) error {
	if _, ok := h.projectsHost(c); !ok {
		return nil
	}
	user := userID(c)
	recs, err := h.svc.ListChatsInProject(c.Request().Context(), user, c.Param("id"), intParam(c, "limit", defaultListLimit, maxListLimit))
	if err != nil {
		return failProject(c, err)
	}
	out := schema.ChatsConversationList{Conversations: make([]schema.ChatsConversation, 0, len(recs))}
	for _, rec := range recs {
		conv, err := h.describe(c, user, rec)
		if err != nil {
			return failFor(c, err)
		}
		out.Conversations = append(out.Conversations, conv)
	}
	return c.JSON(http.StatusOK, out)
}

func (h *Handlers) chatOut(c echo.Context, user, id string) error {
	rec, err := h.svc.Get(c.Request().Context(), user, id)
	if err != nil {
		return failFor(c, err)
	}
	out, err := h.describe(c, user, rec)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, out)
}

// SetChatProject moves a chat into a project.
// @Summary Put a chat in a project.
// @Description project_id is the project's id; an empty one takes the chat out of its project. The chat must be idle (409 otherwise). The tool switches of the chat stay as they are: the defaults of a project apply to new chats only. An archived project takes no chats (400).
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsChatProject true "the project"
// @Success 200 {object} schema.ChatsConversation "The chat"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such chat or project, or it is another user's"
// @Failure 409 {object} schema.ErrorResponse "The chat is running, or projects are not available (projects_off)"
// @Router /api/chats/{id}/project [put]
func (h *Handlers) SetChatProject(c echo.Context) error {
	if _, ok := h.projectsHost(c); !ok {
		return nil
	}
	req, ok := bind[schema.ChatsChatProject](c, msgNotJSON)
	if !ok {
		return nil
	}
	user := userID(c)
	if err := h.svc.SetChatProject(c.Request().Context(), user, c.Param("id"), req.ProjectID); err != nil {
		return failProject(c, err)
	}
	return h.chatOut(c, user, c.Param("id"))
}

// SetChatPulledProjects sets the other projects a chat reads from.
// @Summary Set the projects a chat pulls in.
// @Description The list replaces the earlier one. At most 8 projects, none twice, none the chat's own project, each an active project of yours. An empty list clears it. A pulled project is mounted read only at its pulled-* path for the chat's turns; note that your home is still mounted read and write in an ordinary chat.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsChatPulledProjects true "the projects"
// @Success 200 {object} schema.ChatsConversation "The chat"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such chat or project, or it is another user's"
// @Failure 409 {object} schema.ErrorResponse "Projects are not available (projects_off)"
// @Router /api/chats/{id}/projects [put]
func (h *Handlers) SetChatPulledProjects(c echo.Context) error {
	if _, ok := h.projectsHost(c); !ok {
		return nil
	}
	req, ok := bind[schema.ChatsChatPulledProjects](c, msgNotJSON)
	if !ok {
		return nil
	}
	user := userID(c)
	if err := h.svc.SetChatPulled(c.Request().Context(), user, c.Param("id"), req.ProjectIDs); err != nil {
		return failProject(c, err)
	}
	return h.chatOut(c, user, c.Param("id"))
}

// ListProjectAgents lists the agents of a project.
// @Summary List the agents of a project.
// @Description Your saved agents that were created from this project, sorted by name, as brief rows: id, name, description, model, files_mode (read or write), memory_write (the agent holds the grant project.memory.write) and unavailable. An agent is unavailable while its project is archived, deleted or broken, or its folders changed; it works again after a restore or a new save. The list works for an archived project too.
// @Tags chats
// @Param id path string true "the project's id"
// @Success 200 {object} schema.ChatsProjectAgentList "The agents of the project"
// @Failure 404 {object} schema.ErrorResponse "No such project, or it is another user's"
// @Failure 409 {object} schema.ErrorResponse "Projects are not available (projects_off)"
// @Router /api/chats/projects/{id}/agents [get]
func (h *Handlers) ListProjectAgents(c echo.Context) error {
	if !chatsAllowed(c) {
		return nil
	}
	host, ok := h.projectsHost(c)
	if !ok {
		return nil
	}
	ctx := c.Request().Context()
	user := userID(c)
	if _, err := host.Get(ctx, h.svc.Principal(user), c.Param("id")); err != nil {
		return failProject(c, err)
	}
	rows, err := h.svc.ProjectAgents(ctx, user, c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsProjectAgentList{Agents: make([]schema.ChatsProjectAgent, 0, len(rows))}
	for _, r := range rows {
		a := r.Agent
		mode := agents.FolderWrite
		if list := a.FolderList(); len(list) > 0 && list[0].Mode == agents.FolderRead {
			mode = agents.FolderRead
		}
		out.Agents = append(out.Agents, schema.ChatsProjectAgent{ID: a.ID, Name: a.Name, Description: a.Description, Model: a.Model,
			Unavailable: r.Unavailable, FilesMode: mode, MemoryWrite: slices.Contains(a.Builtins, builtins.GrantProjectMemoryWrite)})
	}
	return c.JSON(http.StatusOK, out)
}

// ListProjectConnections lists the messaging connections of a project.
// @Summary List the messaging connections of a project.
// @Description Your messaging connections that are bound to an agent of this project, as brief rows: id, kind, name, agent_id, agent_name and enabled. A token or a list of senders never appears. The list works for an archived project too.
// @Tags chats
// @Param id path string true "the project's id"
// @Success 200 {object} schema.ChatsProjectConnectionList "The connections"
// @Failure 404 {object} schema.ErrorResponse "No such project, or it is another user's"
// @Failure 409 {object} schema.ErrorResponse "Projects are not available (projects_off)"
// @Router /api/chats/projects/{id}/connections [get]
func (h *Handlers) ListProjectConnections(c echo.Context) error {
	if !chatsAllowed(c) {
		return nil
	}
	if _, ok := h.projectsHost(c); !ok {
		return nil
	}
	rows, err := h.svc.ProjectConnections(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failProject(c, err)
	}
	out := schema.ChatsProjectConnectionList{Connections: make([]schema.ChatsProjectConnection, 0, len(rows))}
	for _, b := range rows {
		out.Connections = append(out.Connections, schema.ChatsProjectConnection{ID: b.ID, Kind: b.Kind, Name: b.Name, AgentID: b.AgentID, AgentName: b.AgentName, Enabled: b.Enabled})
	}
	return c.JSON(http.StatusOK, out)
}

// ListProjectTriggers lists the triggers of a project.
// @Summary List the triggers of a project.
// @Description Your triggers that run an agent of this project, as brief rows: id, name, kind, agent_id, agent_name, schedule, folder, enabled, proposed and next (the next run of an enabled schedule trigger). While the project is archived its agents do not start, so its triggers skip their runs; enabled does not change. The list works for an archived project too.
// @Tags chats
// @Param id path string true "the project's id"
// @Success 200 {object} schema.ChatsProjectTriggerList "The triggers"
// @Failure 404 {object} schema.ErrorResponse "No such project, or it is another user's"
// @Failure 409 {object} schema.ErrorResponse "Projects are not available (projects_off)"
// @Router /api/chats/projects/{id}/triggers [get]
func (h *Handlers) ListProjectTriggers(c echo.Context) error {
	if !chatsAllowed(c) {
		return nil
	}
	if _, ok := h.projectsHost(c); !ok {
		return nil
	}
	rows, err := h.svc.ProjectTriggers(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failProject(c, err)
	}
	out := schema.ChatsProjectTriggerList{Triggers: make([]schema.ChatsProjectTrigger, 0, len(rows))}
	for _, t := range rows {
		out.Triggers = append(out.Triggers, schema.ChatsProjectTrigger{ID: t.ID, Name: t.Name, Kind: t.Kind, AgentID: t.AgentID, AgentName: t.AgentName,
			Schedule: t.Schedule, Folder: t.Folder, Enabled: t.Enabled, Proposed: t.Proposed, Next: t.Next})
	}
	return c.JSON(http.StatusOK, out)
}

// ListProjectLoops lists the loops of a project.
// @Summary List the loops of a project.
// @Description Your loops that have not ended, in chats that use an agent of this project (a loop that ended is history and is left out), as brief rows: id, chat_id, chat_title, prompt (cut to 200 characters) and state. While the project is archived its loops skip their runs; state does not change. The list works for an archived project too.
// @Tags chats
// @Param id path string true "the project's id"
// @Success 200 {object} schema.ChatsProjectLoopList "The loops"
// @Failure 404 {object} schema.ErrorResponse "No such project, or it is another user's"
// @Failure 409 {object} schema.ErrorResponse "Projects are not available (projects_off)"
// @Router /api/chats/projects/{id}/loops [get]
func (h *Handlers) ListProjectLoops(c echo.Context) error {
	if _, ok := h.projectsHost(c); !ok {
		return nil
	}
	rows, err := h.svc.ProjectLoops(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failProject(c, err)
	}
	out := schema.ChatsProjectLoopList{Loops: make([]schema.ChatsProjectLoop, 0, len(rows))}
	for _, l := range rows {
		out.Loops = append(out.Loops, schema.ChatsProjectLoop{ID: l.ID, ChatID: l.ChatID, ChatTitle: l.ChatTitle, Prompt: l.Prompt, State: l.State})
	}
	return c.JSON(http.StatusOK, out)
}

// sharedOut describes a shared project for the viewer: the reference, the
// names and the status, never the owner's account, a path or the manifest id,
// and never the owner's tool lists.
func sharedOut(s projects.Shared) schema.ChatsSharedProject {
	return schema.ChatsSharedProject{Ref: s.Ref, Name: s.Name, Description: s.Description, Status: string(s.Status),
		Mode: s.Mode, OwnerName: s.OwnerName, Error: s.Error}
}

// resolveShared reads the shared project that the :ref parameter names, as the
// caller. The colon may arrive as %3A. A broken project resolves with its
// status; any other failure is the one 404 of an unknown project.
func (h *Handlers) resolveShared(c echo.Context, host *projects.Service) (projects.Shared, error) {
	ref := c.Param("ref")
	if strings.Contains(ref, "%") {
		if u, err := url.PathUnescape(ref); err == nil {
			ref = u
		}
	}
	sh, err := host.ResolveSharedRef(c.Request().Context(), h.svc.Principal(userID(c)), ref)
	if errors.Is(err, projects.ErrInvalid) && sh.Ref != "" {
		return sh, nil
	}
	return sh, err
}

// ListSharedProjects lists the projects shared with you.
// @Summary List the projects shared with you.
// @Description The projects of other people whose folder was shared with you, or with everyone, sorted by name. ref is the only identity: use it in the other shared-project routes and in the list a chat pulls from. mode is read or write. An archived or broken project is listed with its status and is not available. The list is read from the grants on every call, so a revoked share is gone at once. The owner's account, paths and tool lists never appear.
// @Tags chats
// @Success 200 {object} schema.ChatsSharedProjectList "The projects shared with you"
// @Failure 409 {object} schema.ErrorResponse "Projects are not available (projects_off)"
// @Router /api/chats/projects/shared [get]
func (h *Handlers) ListSharedProjects(c echo.Context) error {
	host, ok := h.projectsHost(c)
	if !ok {
		return nil
	}
	list, err := host.SharedWith(c.Request().Context(), h.svc.Principal(userID(c)))
	if err != nil {
		return failProject(c, err)
	}
	out := schema.ChatsSharedProjectList{Projects: make([]schema.ChatsSharedProject, 0, len(list))}
	for _, s := range list {
		out.Projects = append(out.Projects, sharedOut(s))
	}
	return c.JSON(http.StatusOK, out)
}

// GetSharedProject reads one project shared with you.
// @Summary Read a project shared with you.
// @Description ref is the reference from the list ("s:12"; the colon may be sent as %3A). A project that is not shared with you, and one that does not exist, give the same 404.
// @Tags chats
// @Param ref path string true "the reference of the shared project"
// @Success 200 {object} schema.ChatsSharedProject "The project"
// @Failure 404 {object} schema.ErrorResponse "No such project, or it is not shared with you"
// @Failure 409 {object} schema.ErrorResponse "Projects are not available (projects_off)"
// @Router /api/chats/projects/shared/{ref} [get]
func (h *Handlers) GetSharedProject(c echo.Context) error {
	host, ok := h.projectsHost(c)
	if !ok {
		return nil
	}
	sh, err := h.resolveShared(c, host)
	if err != nil {
		return failProject(c, err)
	}
	return c.JSON(http.StatusOK, sharedOut(sh))
}

// GetSharedProjectMemory reads the memory of a project shared with you.
// @Summary Read the memory of a project shared with you.
// @Description Returns the text of index.md and the names of the topic files next to it, read as you through your own grant. It is read only. With a write grant a note is proposed in a chat and confirmed by the owner; with a read grant nothing can be proposed. An archived or broken project, a project that is not shared with you and an unknown one give the same 404.
// @Tags chats
// @Param ref path string true "the reference of the shared project"
// @Success 200 {object} schema.ChatsSharedProjectMemory "The index and the topic files"
// @Failure 404 {object} schema.ErrorResponse "No such project, or it is not shared with you"
// @Failure 409 {object} schema.ErrorResponse "Projects are not available (projects_off)"
// @Router /api/chats/projects/shared/{ref}/memory [get]
func (h *Handlers) GetSharedProjectMemory(c echo.Context) error {
	host, ok := h.projectsHost(c)
	if !ok {
		return nil
	}
	sh, err := h.resolveShared(c, host)
	if err != nil {
		return failProject(c, err)
	}
	ctx := c.Request().Context()
	viewer := h.svc.Principal(userID(c))
	f := h.svc.Files()
	// An archived or broken project is listed but not available: its memory
	// is the same 404 as for an unknown one.
	if sh.Status != projects.StatusActive {
		return failProject(c, projects.ErrNotFound)
	}
	// A missing index.md is an empty index, as in the owner's route. A grant
	// that does not reach the memory folder is a 404.
	text, _, err := f.ReadShared(ctx, viewer, sh.MemoryPath+"/index.md", maxMemoryRead)
	if err != nil {
		if errors.Is(err, workspace.ErrInvalid) {
			return failProject(c, projects.ErrNotFound)
		}
		return failFile(c, err)
	}
	out := schema.ChatsSharedProjectMemory{Index: text, Files: []string{}}
	kids, err := f.Store().List(ctx, f.Actor(viewer), sh.MemoryPath)
	if err != nil && !errors.Is(err, workspace.ErrNotFound) {
		return failFile(c, err)
	}
	for _, k := range kids {
		if k.Name != "index.md" && proposals.TopicFile(k.Name) {
			out.Files = append(out.Files, k.Name)
		}
	}
	slices.Sort(out.Files)
	return c.JSON(http.StatusOK, out)
}
