package studio

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// isAdmin reports whether the caller may change the shared skills. With
// authentication off there is one user, who is the administrator.
func isAdmin(c echo.Context) bool {
	u := auth.GetUser(c)
	return u == nil || u.Role == auth.RoleAdmin
}

// skillActor is who a skill call acts as: the user, or for the shared layer the
// administrator's identity in the workspace, when the caller is one.
func skillActor(c echo.Context, fc fileCall, layer skills.Layer) string {
	if layer == skills.LayerShared && isAdmin(c) {
		return files.AdminPrincipal
	}
	return fc.p
}

func parseLayer(s string) (skills.Layer, bool) {
	switch skills.Layer(s) {
	case skills.LayerPersonal, skills.LayerShared, skills.LayerAddon, skills.LayerBuiltin, skills.LayerAgent, skills.LayerProject:
		return skills.Layer(s), true
	}
	return "", false
}

// editable says whether the caller may change skills of a layer. A skill of
// the project layer is listed only to the owner of the project, who writes it.
func editable(c echo.Context, l skills.Layer) bool {
	return l == skills.LayerPersonal || l == skills.LayerProject || (l == skills.LayerShared && isAdmin(c))
}

// projectBlockedNote is the note of a project skill list that is off.
const projectBlockedNote = "project skills are off while this folder has other writers"

// ListSkills lists the skills.
// @Summary List your skills.
// @Description Skills from every layer: .skills folders of your files, the skills of the chat's project (project), your personal skills, the shared ones, the ones of the add-ons you may use now (addon, with the add-on in addon and addon_title) and the built-in ones. A chat with a saved agent also lists the skills of that agent (agent). The nearest wins; a skill that another overrides says so in shadowed. With chat set, the chat's switches are applied. A skill of the active memory provider says provider "memory" and locked: the host grants it to a chat that reads the notes, so an agent does not list it.
// @Tags chats
// @Param chat query string false "a chat id, to apply its switches"
// @Param agents query bool false "true: add the skills of your saved agents (layer agent), for the Skills page; ignored with chat"
// @Param layer query string false "project: list only the skills of the project given in project"
// @Param project query string false "a project's id, with layer=project"
// @Success 200 {object} schema.ChatsSkillList "The skills"
// @Router /api/chats/skills [get]
func (h *Handlers) ListSkills(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	if c.QueryParam("layer") == string(skills.LayerProject) {
		return h.listProjectSkills(c, fc)
	}
	list, notes, err := h.svc.Skills(c.Request().Context(), userID(c), c.QueryParam("chat"))
	if err != nil {
		return failFor(c, err)
	}
	// The Skills page asks for the skills of the saved agents too. A chat's
	// own list already has the layer of its agent.
	if c.QueryParam("agents") == "true" && c.QueryParam("chat") == "" {
		more, err := h.svc.AgentSkills(c.Request().Context(), userID(c))
		if err != nil {
			return failFor(c, err)
		}
		list = append(list, more...)
	}
	return c.JSON(http.StatusOK, h.skillList(c, fc, list, notes))
}

// skillList is the response for a list of skills.
func (h *Handlers) skillList(c echo.Context, fc fileCall, list []skills.Skill, notes []string) schema.ChatsSkillList {
	out := schema.ChatsSkillList{Skills: make([]schema.ChatsSkill, 0, len(list)), Notes: notes}
	for _, sk := range list {
		out.Skills = append(out.Skills, schema.ChatsSkill{
			ID: studiosvc.SkillID(sk), Name: sk.Name, Description: sk.Description, Layer: string(sk.Layer), Path: sk.GuestPath,
			Addon: sk.Addon, AddonTitle: sk.AddonTitle, Agent: sk.Agent, AgentName: sk.AgentName, Project: sk.Project,
			Removable: sk.Layer == skills.LayerAgent, ShadowedBy: sk.ShadowedBy, Fallback: sk.Fallback,
			Enabled: !sk.Off, Shadowed: string(sk.Shadowed), Invalid: sk.Invalid, Editable: editable(c, sk.Layer),
			Provider: sk.Provider, Locked: sk.Locked,
			New: sk.Layer == skills.LayerPersonal && fc.f.SkillIsNew(c.Request().Context(), fc.p, sk.Name),
		})
	}
	return out
}

// listProjectSkills lists the skills of one project of the caller, for the
// project page.
func (h *Handlers) listProjectSkills(c echo.Context, fc fileCall) error {
	list, blocked, err := h.svc.ProjectSkills(c.Request().Context(), userID(c), c.QueryParam("project"))
	if err != nil {
		return failProject(c, err)
	}
	var notes []string
	if blocked {
		notes = []string{projectBlockedNote}
	}
	out := h.skillList(c, fc, list, notes)
	out.SkillsBlocked = blocked
	return c.JSON(http.StatusOK, out)
}

// projectSkillDir resolves the project of a skill call to the folder name its
// skills live under. It is the caller's own project: a project of another
// person, shared or not, is not found. On failure it has written the response.
func (h *Handlers) projectSkillDir(c echo.Context, id string, write bool) (string, bool) {
	dir, err := h.svc.ProjectSkillDir(c.Request().Context(), userID(c), id, write)
	if err != nil {
		_ = failProject(c, err)
		return "", false
	}
	return dir, true
}

// CreateSkill creates a skill.
// @Summary Create a skill.
// @Description A skill is a folder with a SKILL.md: a name, a one-line description and the procedure the model reads when a task matches. The personal layer is yours; the shared layer needs an administrator. The project layer (layer project, with project set to the project's id) holds the skills of one project of yours: its chats and agents get them, and only you write them. It is off while another person may write the project folder, and a write is then refused with 409.
// @Tags chats
// @Param request body schema.ChatsSkillWrite true "the skill"
// @Success 200 {object} schema.ChatsSkillDetail "The skill"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/skills [post]
func (h *Handlers) CreateSkill(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	var req schema.ChatsSkillWrite
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "layer, name, description and body are required")
	}
	layer, ok := parseLayer(req.Layer)
	if !ok || !writable(layer) {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "layer must be personal, shared or project")
	}
	if layer == skills.LayerProject {
		return h.writeProjectSkill(c, fc, req.Project, req.Name, req.Description, req.Body)
	}
	if err := fc.f.WriteSkill(c.Request().Context(), skillActor(c, fc, layer), layer, req.Name, req.Description, req.Body); err != nil {
		return failFile(c, err)
	}
	return h.skillDetail(c, fc, layer, req.Name)
}

// GetSkill reads a skill.
// @Summary Read a skill.
// @Tags chats
// @Param layer path string true "personal, shared, project, agent, addon or builtin"
// @Param name path string true "the skill's name"
// @Param project query string false "the project's ID, for the project layer"
// @Param addon query string false "the add-on's ID, for the addon layer"
// @Param agent query string false "the saved agent's ID, for the agent layer"
// @Success 200 {object} schema.ChatsSkillDetail "The skill with its text"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/skills/{layer}/{name} [get]
func (h *Handlers) GetSkill(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	layer, ok := parseLayer(c.Param("layer"))
	if !ok {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "layer must be personal, shared, addon or builtin")
	}
	if layer == skills.LayerAddon {
		name := c.Param("name")
		desc, body, err := fc.f.ReadAddonSkill(c.Request().Context(), fc.p, c.QueryParam("addon"), name)
		if err != nil {
			return failFile(c, err)
		}
		return c.JSON(http.StatusOK, schema.ChatsSkillDetail{Name: name, Layer: string(layer), Description: desc, Body: body})
	}
	if layer == skills.LayerProject {
		dir, ok := h.projectSkillDir(c, c.QueryParam("project"), false)
		if !ok {
			return nil
		}
		return h.projectSkillDetail(c, fc, dir, c.Param("name"))
	}
	if layer == skills.LayerAgent {
		name := c.Param("name")
		desc, body, err := fc.f.ReadAgentSkill(c.Request().Context(), fc.p, c.QueryParam("agent"), name)
		if err != nil {
			return failFile(c, err)
		}
		return c.JSON(http.StatusOK, schema.ChatsSkillDetail{Name: name, Layer: string(layer), Description: desc, Body: body})
	}
	return h.skillDetail(c, fc, layer, c.Param("name"))
}

// writable reports a layer whose skills are changed through this API.
func writable(l skills.Layer) bool {
	return l == skills.LayerPersonal || l == skills.LayerShared || l == skills.LayerProject
}

// writeProjectSkill creates or replaces a skill of a project of the caller and
// returns it. Only the owner writes the layer; an archived project and a
// project folder that another person may write are refused.
func (h *Handlers) writeProjectSkill(c echo.Context, fc fileCall, project, name, description, body string) error {
	dir, ok := h.projectSkillDir(c, project, true)
	if !ok {
		return nil
	}
	if err := fc.f.WriteProjectSkill(c.Request().Context(), fc.p, dir, name, description, body); err != nil {
		return failFile(c, err)
	}
	return h.projectSkillDetail(c, fc, dir, name)
}

func (h *Handlers) projectSkillDetail(c echo.Context, fc fileCall, dir, name string) error {
	desc, body, err := fc.f.ReadProjectSkill(c.Request().Context(), fc.p, dir, name)
	if err != nil {
		return failFile(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsSkillDetail{Name: name, Layer: string(skills.LayerProject), Description: desc, Body: body, Editable: true})
}

func (h *Handlers) skillDetail(c echo.Context, fc fileCall, layer skills.Layer, name string) error {
	desc, body, err := fc.f.ReadSkill(c.Request().Context(), fc.p, layer, name)
	if err != nil {
		return failFile(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsSkillDetail{Name: name, Layer: string(layer), Description: desc, Body: body, Editable: editable(c, layer)})
}

// UpdateSkill replaces a skill's description and text.
// @Summary Change a skill.
// @Tags chats
// @Param layer path string true "personal, shared or project"
// @Param name path string true "the skill's name"
// @Param project query string false "the project's ID, for the project layer"
// @Param request body schema.ChatsSkillUpdate true "the new description and body"
// @Success 200 {object} schema.ChatsSkillDetail "The skill"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/skills/{layer}/{name} [put]
func (h *Handlers) UpdateSkill(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	layer, ok := parseLayer(c.Param("layer"))
	if !ok || !writable(layer) {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "layer must be personal, shared or project")
	}
	var req schema.ChatsSkillUpdate
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "description and body are required")
	}
	if layer == skills.LayerProject {
		return h.writeProjectSkill(c, fc, c.QueryParam("project"), c.Param("name"), req.Description, req.Body)
	}
	if err := fc.f.WriteSkill(c.Request().Context(), skillActor(c, fc, layer), layer, c.Param("name"), req.Description, req.Body); err != nil {
		return failFile(c, err)
	}
	return h.skillDetail(c, fc, layer, c.Param("name"))
}

// DeleteSkill moves a skill to the Trash.
// @Summary Delete a skill.
// @Description The skill's folder goes to the Trash of your files, where it can be restored. Shared skills need an administrator. A skill of the agent layer is deleted with the agent query parameter, one of the project layer with the project query parameter.
// @Tags chats
// @Param layer path string true "personal, shared, project or agent"
// @Param project query string false "the project's ID, for the project layer"
// @Param agent query string false "the saved agent's ID, for the agent layer"
// @Param name path string true "the skill's name"
// @Success 200 {object} map[string]bool "deleted"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/skills/{layer}/{name} [delete]
func (h *Handlers) DeleteSkill(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	layer, ok := parseLayer(c.Param("layer"))
	if ok && layer == skills.LayerAgent {
		if err := fc.f.DeleteAgentSkill(c.Request().Context(), fc.p, c.QueryParam("agent"), c.Param("name")); err != nil {
			return failFile(c, err)
		}
		return c.JSON(http.StatusOK, map[string]bool{"deleted": true})
	}
	if ok && layer == skills.LayerProject {
		dir, ok := h.projectSkillDir(c, c.QueryParam("project"), true)
		if !ok {
			return nil
		}
		if err := fc.f.DeleteProjectSkill(c.Request().Context(), fc.p, dir, c.Param("name")); err != nil {
			return failFile(c, err)
		}
		return c.JSON(http.StatusOK, map[string]bool{"deleted": true})
	}
	if !ok || !writable(layer) {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "layer must be personal, shared, project or agent")
	}
	if err := fc.f.DeleteSkill(c.Request().Context(), skillActor(c, fc, layer), layer, c.Param("name")); err != nil {
		return failFile(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"deleted": true})
}

// SetChatSkills sets the skills a chat has switched off.
// @Summary Switch skills on and off for a chat.
// @Description The list replaces the earlier one. An entry is a skill name, which turns off the skill that wins so that one of the same name further away shows through, or the id "layer:name" from the skill list, which turns off the skill of that layer. The next turn of the chat gets the index without the skills that are off.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsSkillSwitches true "the names or ids of the skills that are off"
// @Success 200 {object} schema.ChatsSkillSwitches "The switches now in force"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/skills [put]
func (h *Handlers) SetChatSkills(c echo.Context) error {
	var req schema.ChatsSkillSwitches
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "off is required")
	}
	if err := h.svc.SetSkillsOff(c.Request().Context(), userID(c), c.Param("id"), req.Off); err != nil {
		return failFor(c, err)
	}
	rec, err := h.svc.Get(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	off := rec.SkillsOff
	if off == nil {
		off = []string{}
	}
	return c.JSON(http.StatusOK, schema.ChatsSkillSwitches{Off: off})
}

// maxSkillImportBody is the largest zip file an import reads. The archive may
// unpack to more than this, up to skills.DefaultPackBytes.
const maxSkillImportBody = 20 << 20

// reservedSkillName says whether a name belongs to a skill that ships with the
// product: a core skill, a document slot, or a file of the built-in tree.
func reservedSkillName(name string) bool {
	if contract.ProtectedSkill(name) || contract.ReplaceableSkill(name) {
		return true
	}
	_, err := fs.Stat(builtinskills.FS(), name)
	return err == nil
}

// ImportSkills imports skills in the Agent Skills format from a zip archive.
// @Summary Import skills from a zip archive.
// @Description Send a zip file as the file field of a multipart form. It holds either one skill (SKILL.md at the top) or one folder per skill, each with a SKILL.md and optional scripts/, references/ and assets/, as the Agent Skills specification (agentskills.io) defines. The skills go to your personal layer. The whole archive is checked first and nothing is stored when any part is not valid: the name and description rules of the specification, paths that leave the skill folder, absolute paths, links, duplicate or case-colliding names, and size limits (500 entries, 5 MiB a file, 20 MiB in all). A name that exists already in your skills is refused with 409, and so is the name of a skill that ships with LocalAI; the import never replaces a skill. Files, scripts included, are only stored: the import runs nothing. An imported skill is listed with new set until a chat uses it for the first time.
// @Tags chats
// @Accept mpfd
// @Param file formData file true "the zip archive"
// @Success 201 {object} schema.ChatsSkillImportResult "The stored skills"
// @Failure 400 {object} schema.ErrorResponse "The archive is not a valid skill archive"
// @Failure 409 {object} schema.ErrorResponse "A skill of that name exists already"
// @Failure 413 {object} schema.ErrorResponse "The archive is over a size limit"
// @Router /api/chats/skills/import [post]
func (h *Handlers) ImportSkills(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxSkillImportBody+(1<<20))
	fh, err := c.FormFile("file")
	if err != nil {
		var httpErr *echo.HTTPError
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) || (errors.As(err, &httpErr) && httpErr.Code == http.StatusRequestEntityTooLarge) {
			return fail(c, http.StatusRequestEntityTooLarge, typeInvalid, "the zip file is larger than 20 MiB")
		}
		return fail(c, http.StatusBadRequest, "invalid_request_error", "send the zip file as the file field")
	}
	f, err := fh.Open()
	if err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "the file could not be read")
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxSkillImportBody+1))
	if err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "the file could not be read")
	}
	if len(raw) > maxSkillImportBody {
		return fail(c, http.StatusRequestEntityTooLarge, typeInvalid, "the zip file is larger than 20 MiB")
	}
	packs, err := skills.ReadPacks(bytes.NewReader(raw), int64(len(raw)), skills.PackLimits{})
	if err != nil {
		return failFile(c, err)
	}
	rev, err := fc.f.ImportSkills(c.Request().Context(), fc.p, packs, reservedSkillName)
	if err != nil {
		return failFile(c, err)
	}
	out := schema.ChatsSkillImportResult{Rev: rev, Skills: make([]schema.ChatsSkillImported, 0, len(packs))}
	for _, p := range packs {
		item := schema.ChatsSkillImported{Name: p.Name, Description: p.Description, Files: len(p.Files), Bytes: p.Bytes()}
		for _, pf := range p.Files {
			if strings.HasPrefix(pf.Path, "scripts/") {
				item.Scripts++
			}
		}
		out.Skills = append(out.Skills, item)
	}
	return c.JSON(http.StatusCreated, out)
}

// ExportSkill downloads one of your skills as a zip archive.
// @Summary Export a skill as a zip archive.
// @Description The archive follows the Agent Skills layout: one folder named after the skill, with SKILL.md and the files below it (scripts/, references/, assets/). Importing it again gives the same skill. Only skills of your personal layer can be exported.
// @Tags chats
// @Produce application/zip
// @Param name path string true "the skill's name"
// @Success 200 {file} file "The zip archive"
// @Failure 404 {object} schema.ErrorResponse "No such skill"
// @Router /api/chats/skills/personal/{name}/export [get]
func (h *Handlers) ExportSkill(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	pack, err := fc.f.ReadSkillPack(c.Request().Context(), fc.p, c.Param("name"))
	if err != nil {
		return failFile(c, err)
	}
	data, err := skills.WriteZip(pack)
	if err != nil {
		return failFile(c, err)
	}
	c.Response().Header().Set("Content-Disposition", `attachment; filename="`+pack.Name+`.zip"`)
	return c.Blob(http.StatusOK, "application/zip", data)
}
