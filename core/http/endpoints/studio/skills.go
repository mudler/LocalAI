package studio

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/skills"
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
	case skills.LayerPersonal, skills.LayerShared, skills.LayerBuiltin:
		return skills.Layer(s), true
	}
	return "", false
}

func editable(c echo.Context, l skills.Layer) bool {
	return l == skills.LayerPersonal || (l == skills.LayerShared && isAdmin(c))
}

// ListSkills lists the skills.
// @Summary List your skills.
// @Description Skills from every layer: .skills folders of your files, your personal skills, the shared ones and the built-in ones. The nearest wins; a skill that another overrides says so in shadowed. With chat set, the chat's switches are applied.
// @Tags chats
// @Param chat query string false "a chat id, to apply its switches"
// @Success 200 {object} schema.ChatsSkillList "The skills"
// @Router /api/chats/skills [get]
func (h *Handlers) ListSkills(c echo.Context) error {
	if _, ok := h.files(c); !ok {
		return nil
	}
	list, notes, err := h.svc.Skills(c.Request().Context(), userID(c), c.QueryParam("chat"))
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsSkillList{Skills: make([]schema.ChatsSkill, 0, len(list)), Notes: notes}
	for _, sk := range list {
		out.Skills = append(out.Skills, schema.ChatsSkill{
			ID: studiosvc.SkillID(sk), Name: sk.Name, Description: sk.Description, Layer: string(sk.Layer), Path: sk.GuestPath,
			Enabled: !sk.Off, Shadowed: string(sk.Shadowed), Invalid: sk.Invalid, Editable: editable(c, sk.Layer),
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
	if !ok || layer == skills.LayerBuiltin {
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
// @Param layer path string true "personal, shared or builtin"
// @Param name path string true "the skill's name"
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
		return fail(c, http.StatusBadRequest, "invalid_request_error", "layer must be personal, shared or builtin")
	}
	return h.skillDetail(c, fc, layer, c.Param("name"))
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
	if !ok || layer == skills.LayerBuiltin {
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
// @Description The skill's folder goes to the Trash of your files, where it can be restored. Shared skills need an administrator.
// @Tags chats
// @Param layer path string true "personal or shared"
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
	if !ok || layer == skills.LayerBuiltin {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "layer must be personal or shared")
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
