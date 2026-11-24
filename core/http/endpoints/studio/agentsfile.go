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
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/projects"
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
	// Version 5 is a project agent: an older server would import it as an
	// agent of the whole home, with folders it cannot resolve.
	maxAgentFileVersion = 5
)

// agentFileVersion is the version an export writes: 1 for an agent without a
// folder or tool switches, so older servers keep importing it, 2 for one with
// one folder, 3 for one with several, 4 for one with tool switches and 5 for
// a project agent, which wins over the others.
func agentFileVersion(folders int, hasSwitches, hasProject bool) int {
	switch {
	case hasProject:
		return 5
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
// @Description Returns the definition as a JSON file (version, kind localai-agent, agent). It holds no ID, owner or time. An agent of a project is version 5: it carries projectRef (id, name and slug of the project it was made in, a hint only) and folders as the project-relative paths files (with its mode) and memory (read); it holds no node ID and no path of your files, and a server that knows only versions up to 4 refuses it. An agent with one folder is version 2 and carries folder {path, mode}; an agent with several is version 3 and carries folders [{path, mode}], the folder's home-relative path now (empty when the folder is missing, not chosen, or outside your home, even if readable); importing an empty path preserves the scope as unset with a warning; a server that knows only version 1 refuses it. An agent with switches of built-in tool groups (builtins_off, builtins_on) is version 4. An agent without a folder or switches is version 1. The builtins list (builtin grants) is an extra field of any version: a server that does not know it ignores it, so the agent comes back without the grants. POST /api/chats/agents/import reads it. This replaces the old /api/agents/{name}/export.
// @Tags chats
// @Param id path string true "the agent's id"
// @Success 200 {object} schema.ChatsAgentExport "The agent file"
// @Router /api/chats/agents/{id}/export [get]
func (h *Handlers) ExportAgent(c echo.Context) error {
	a, err := h.svc.OwnAgent(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	w := schema.ChatsAgentWrite{Name: a.Name, Description: a.Description, Instructions: a.Instructions, Model: a.Model,
		MCPServers: a.MCPServers, Addons: a.Addons, Collections: a.Collections, Skills: a.Skills, Network: a.Network, SandboxProfile: a.SandboxProfile, Memory: a.Memory, MemorySave: a.MemorySave,
		BuiltinsOff: a.BuiltinsOff, BuiltinsOn: a.BuiltinsOn, Builtins: a.Builtins}
	if a.ProjectID != "" {
		return h.exportProjectAgent(c, a, w)
	}
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
	return c.JSON(http.StatusOK, schema.ChatsAgentExport{Version: agentFileVersion(len(a.FolderList()), len(a.BuiltinsOff)+len(a.BuiltinsOn) > 0, false), Kind: agentExportKind, ExportedAt: time.Now().UTC(), Agent: w})
}

// exportProjectAgent writes a project agent as a version 5 file. The folders
// are the two project-relative paths, never a node or a path of the files of
// the exporter, and the project is named for a hint only.
func (h *Handlers) exportProjectAgent(c echo.Context, a agents.Agent, w schema.ChatsAgentWrite) error {
	ref := &schema.ChatsAgentProjectRef{ID: a.ProjectID}
	if ps := h.svc.Projects(); ps != nil {
		// A project that cannot be read now (deleted, broken) leaves the id
		// alone in the reference: the name is not known.
		if r, err := ps.Get(c.Request().Context(), h.svc.Principal(userID(c)), a.ProjectID); err == nil {
			ref = &schema.ChatsAgentProjectRef{ID: r.Project.ID, Name: r.Project.Name, Slug: r.Project.Slug}
		}
	}
	w.ProjectRef = ref
	filesMode := agents.FolderWrite
	if list := a.FolderList(); len(list) > 0 && list[0].Mode == agents.FolderRead {
		filesMode = agents.FolderRead
	}
	w.Folders = []schema.ChatsAgentFolderWrite{{Path: projectFilesRel, Mode: filesMode}, {Path: projectMemoryRel, Mode: agents.FolderRead}}
	c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf("attachment; filename=%q", fileSafe(a.Name)+".json"))
	return c.JSON(http.StatusOK, schema.ChatsAgentExport{Version: agentFileVersion(2, false, true), Kind: agentExportKind, ExportedAt: time.Now().UTC(), Agent: w})
}

// The project-relative folder paths of a version 5 file.
const (
	projectFilesRel  = "files"
	projectMemoryRel = "memory"
)

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
// @Description Send the export file (version 1 to 5) as the JSON body, or as the file field of a multipart form. A bare agent definition (the body of POST /api/chats/agents) is accepted too, but not one that carries project_id or projectRef (400). A version 5 file is a project agent: send project_id (one of your own active projects: 404 for an unknown or another person's, 400 for an archived one) or create_project true (a project is made from the name in the file) as query parameters, or as form fields of a multipart import; never inside the file, whose own project fields are ignored. The query string wins over a form field, and create_project is the value of that one parameter. Without either the answer is 409 with the error type project_required and the project's name in project_name. The agent is made a project agent of that project with its files and memory folders; the file's files entry only chooses the files mode (read or write), memory is always read, and a project id inside the file's agent is ignored. With project_id, a file of an older version is imported as a project agent too, its folders ignored with a warning. The agent is always new: a name you already use gets a number, and a model this server does not have, or you may not use, is dropped with a warning. A folder is looked up by its path in your files; when you have no such folder the agent keeps the mode without a folder (folder state unset) and does not start until you choose one, and a warning says so.
// @Tags chats
// @Accept json,mpfd
// @Param request body schema.ChatsAgentExport true "the agent file"
// @Param file formData file false "the agent file (multipart)"
// @Param project_id query string false "one of your own active projects to make the agent a project agent of"
// @Param create_project query bool false "make a project from the project name in the file and put the agent in it"
// @Success 201 {object} schema.ChatsAgentImportResult "The new agent and what the import changed"
// @Failure 409 {object} schema.ChatsAgentProjectRequired "The file is a project agent and no project was chosen (error type project_required)"
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
	file, err := parseAgentFile(raw, maxAgentFileVersion)
	if err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	}
	def := file.Def
	// Only the request chooses the project: a query parameter or a form field
	// of a multipart import. Never the file: nothing in it, at the top level or
	// inside the agent, maps the agent or makes a project. A query parameter
	// wins over a form field, and create_project is the value of that one
	// parameter, not an OR of the sources.
	projectID := strings.TrimSpace(requestParam(c, "project_id"))
	create := truthy(requestParam(c, "create_project"))
	if projectID != "" && create {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "send project_id or create_project, not both")
	}
	if create && !file.ProjectFile {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "create_project needs a version 5 file of a project agent")
	}
	if file.ProjectFile && projectID == "" && !create {
		name := ""
		if def.ProjectRef != nil {
			name = def.ProjectRef.Name
		}
		msg := "this agent belongs to a project: choose one of your projects (project_id) or create one (create_project)"
		if name != "" {
			msg = fmt.Sprintf("this agent belongs to the project %q in the file: choose one of your projects (project_id) or create one (create_project)", name)
		}
		return c.JSON(http.StatusConflict, schema.ChatsAgentProjectRequired{Error: schema.ChatsAgentProjectRequiredError{
			Message: msg, Code: http.StatusConflict, Type: "project_required", ProjectName: name,
		}})
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
		MCPServers: def.MCPServers, Addons: def.Addons, Collections: def.Collections, Skills: def.Skills, Network: def.Network, SandboxProfile: def.SandboxProfile, Memory: def.Memory, MemorySave: def.MemorySave,
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
	if len(def.Resources) > 0 {
		warnings = append(warnings, "The resources the file lists belong to the server it came from, so the agent has none. Choose them in the agent.")
	}
	madeProject := ""
	if projectID != "" || create {
		_, mws, merr := studiosvc.ProjectImportFilesMode(rowsIn)
		if merr != nil {
			return failFor(c, merr)
		}
		warnings = append(warnings, mws...)
		if create {
			// Judge the agent before anything is made for it, so a refused
			// agent leaves no project behind.
			if cerr := h.svc.CheckImportedAgent(c.Request().Context(), userID(c), a); cerr != nil {
				return failFor(c, cerr)
			}
			ps, ok := h.projectsHost(c)
			if !ok {
				return nil
			}
			name := ""
			if def.ProjectRef != nil {
				name = strings.TrimSpace(def.ProjectRef.Name)
			}
			if name == "" {
				name = strings.TrimSpace(def.Name)
			}
			p, perr := ps.Create(c.Request().Context(), h.svc.Principal(userID(c)), projects.CreateInput{Name: name})
			if perr != nil {
				return failProject(c, perr)
			}
			projectID, madeProject = p.ID, p.ID
			warnings = append(warnings, fmt.Sprintf("a project %q was created for the agent", p.Name))
		}
		a.ProjectID = projectID
	}
	saved, err := h.svc.ImportAgentFolders(c.Request().Context(), userID(c), a, rowsIn)
	if err != nil {
		if madeProject != "" {
			// The project exists only for this agent: do not leave it empty.
			if derr := h.svc.Projects().Delete(c.Request().Context(), h.svc.Principal(userID(c)), madeProject); derr != nil {
				xlog.Warn("studio: could not remove the project made for a refused import", "project", madeProject, "error", derr)
			}
		}
		return failFor(c, err)
	}
	if saved.Name != strings.TrimSpace(def.Name) {
		warnings = append(warnings, fmt.Sprintf("you already have an agent named %q, so the new one is named %q", strings.TrimSpace(def.Name), saved.Name))
	}
	for i, f := range saved.FolderList() {
		if saved.ProjectID != "" {
			break
		}
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

// requestParam is a parameter of the request: the query string, else a form
// field of a multipart import. The uploaded file is never a source.
func requestParam(c echo.Context, name string) string {
	if q := c.QueryParams(); q.Has(name) {
		return q.Get(name)
	}
	return c.FormValue(name)
}

// truthy reads a flag of a query or a form.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// agentFile is a parsed agent file.
type agentFile struct {
	// Def is the definition. Its ProjectID is always empty: a file never sets
	// the project of an agent. Its ProjectRef is set only by a version 5 file.
	Def schema.ChatsAgentWrite
	// ProjectFile is true for a version 5 file: it is always the file of a
	// project agent, with or without a projectRef, and an import needs a
	// project for it.
	ProjectFile bool
}

// readAgentFile reads an export file, or a bare agent definition.
func readAgentFile(raw []byte) (schema.ChatsAgentWrite, error) {
	return readAgentFileUpTo(raw, maxAgentFileVersion)
}

// readAgentFileUpTo reads an export file of version 1 to newest, or a bare
// definition. With newest 4 it is the check of a server from before project
// agents; with newest 1, of a server from before folders.
func readAgentFileUpTo(raw []byte, newest int) (schema.ChatsAgentWrite, error) {
	f, err := parseAgentFile(raw, newest)
	return f.Def, err
}

func parseAgentFile(raw []byte, newest int) (agentFile, error) {
	var doc struct {
		Version int                     `json:"version"`
		Kind    string                  `json:"kind"`
		Agent   *schema.ChatsAgentWrite `json:"agent"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return agentFile{}, errors.New("the body is not a valid agent file")
	}
	if doc.Agent != nil {
		if doc.Version < 1 || doc.Version > newest || (doc.Kind != "" && doc.Kind != agentExportKind) {
			return agentFile{}, errors.New("this agent file has a version or kind this server does not know")
		}
		def := *doc.Agent
		// The agent of a file never says which project it is in.
		def.ProjectID = ""
		if doc.Version < 5 {
			def.ProjectRef = nil
		}
		return agentFile{Def: def, ProjectFile: doc.Version >= 5}, nil
	}
	var bare schema.ChatsAgentWrite
	if err := json.Unmarshal(raw, &bare); err != nil || strings.TrimSpace(bare.Name) == "" {
		return agentFile{}, errors.New("the body is not a valid agent file")
	}
	if bare.ProjectID != "" || bare.ProjectRef != nil {
		return agentFile{}, errors.New("a bare agent definition cannot name a project: import a version 5 file, or send project_id with the request")
	}
	return agentFile{Def: bare}, nil
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
