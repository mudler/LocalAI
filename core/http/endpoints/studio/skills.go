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
	case skills.LayerPersonal, skills.LayerShared, skills.LayerAddon, skills.LayerBuiltin, skills.LayerAgent:
		return skills.Layer(s), true
	}
	return "", false
}

func editable(c echo.Context, l skills.Layer) bool {
	return l == skills.LayerPersonal || (l == skills.LayerShared && isAdmin(c))
}

// ListSkills lists the skills.
// @Summary List your skills.
// @Description Skills from every layer: .skills folders of your files, your personal skills, the shared ones, the ones of the add-ons you may use now (addon, with the add-on in addon and addon_title) and the built-in ones. A chat with a saved agent also lists the skills of that agent (agent). The nearest wins; a skill that another overrides says so in shadowed. With chat set, the chat's switches are applied. A skill of the active memory provider says provider "memory" and locked: the host grants it to a chat that reads the notes, so an agent does not list it.
// @Tags chats
// @Param chat query string false "a chat id, to apply its switches"
// @Param agents query bool false "true: add the skills of your saved agents (layer agent), for the Skills page; ignored with chat"
// @Success 200 {object} schema.ChatsSkillList "The skills"
// @Router /api/chats/skills [get]
func (h *Handlers) ListSkills(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
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
	out := schema.ChatsSkillList{Skills: make([]schema.ChatsSkill, 0, len(list)), Notes: notes}
	for _, sk := range list {
		out.Skills = append(out.Skills, schema.ChatsSkill{
			ID: studiosvc.SkillID(sk), Name: sk.Name, Description: sk.Description, Layer: string(sk.Layer), Path: sk.GuestPath,
			Addon: sk.Addon, AddonTitle: sk.AddonTitle, Agent: sk.Agent, AgentName: sk.AgentName,
			Removable: sk.Layer == skills.LayerAgent, ShadowedBy: sk.ShadowedBy, Fallback: sk.Fallback,
			Enabled: !sk.Off, Shadowed: string(sk.Shadowed), Invalid: sk.Invalid, Editable: editable(c, sk.Layer),
			Provider: sk.Provider, Locked: sk.Locked,
			New: sk.Layer == skills.LayerPersonal && fc.f.SkillIsNew(c.Request().Context(), fc.p, sk.Name),
		})
	}
	return c.JSON(http.StatusOK, out)
}

// CreateSkill creates a skill.
// @Summary Create a skill.
// @Description A skill is a folder with a SKILL.md: a name, a one-line description and the procedure the model reads when a task matches. The personal layer is yours; the shared layer needs an administrator.
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
		return fail(c, http.StatusBadRequest, "invalid_request_error", "layer must be personal or shared")
	}
	if err := fc.f.WriteSkill(c.Request().Context(), skillActor(c, fc, layer), layer, req.Name, req.Description, req.Body); err != nil {
		return failFile(c, err)
	}
	return h.skillDetail(c, fc, layer, req.Name)
}

// GetSkill reads a skill.
// @Summary Read a skill.
// @Tags chats
// @Param layer path string true "personal, shared, agent, addon or builtin"
// @Param name path string true "the skill's name"
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
func writable(l skills.Layer) bool { return l == skills.LayerPersonal || l == skills.LayerShared }

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
// @Param layer path string true "personal or shared"
// @Param name path string true "the skill's name"
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
		return fail(c, http.StatusBadRequest, "invalid_request_error", "layer must be personal or shared")
	}
	var req schema.ChatsSkillUpdate
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "description and body are required")
	}
	if err := fc.f.WriteSkill(c.Request().Context(), skillActor(c, fc, layer), layer, c.Param("name"), req.Description, req.Body); err != nil {
		return failFile(c, err)
	}
	return h.skillDetail(c, fc, layer, c.Param("name"))
}

// DeleteSkill moves a skill to the Trash.
// @Summary Delete a skill.
// @Description The skill's folder goes to the Trash of your files, where it can be restored. Shared skills need an administrator. A skill of the agent layer is deleted with the agent query parameter.
// @Tags chats
// @Param layer path string true "personal, shared or agent"
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
	if !ok || !writable(layer) {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "layer must be personal, shared or agent")
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
