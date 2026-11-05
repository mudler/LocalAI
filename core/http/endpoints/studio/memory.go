package studio

import (
	"bytes"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
)

var memoryRules = []errRule{
	fixed(http.StatusServiceUnavailable, "unavailable", "the memory settings cannot be stored on this server", studiosvc.ErrMemorySettings),
	fixed(http.StatusNotFound, typeNotFound, "there are no notes to export", studiosvc.ErrNoNotes),
	fixed(http.StatusNotFound, typeNotFound, "this provider has no export; use its own site", studiosvc.ErrProviderNoExport),
	fixed(http.StatusNotFound, typeNotFound, "this provider has no forget; delete the notes in Files, or use its own site", studiosvc.ErrNoForget, studiosvc.ErrProviderNoRole),
	fixed(http.StatusServiceUnavailable, "unavailable", "the provider is resting after repeated failures; try again in a minute", studiosvc.ErrProviderCoolingDown),
	fixed(http.StatusBadGateway, typeUpstream, "the provider did not answer", studiosvc.ErrProviderCall),
}

func failMemory(c echo.Context, err error) error { return mapErr(c, err, memoryRules, failFor) }

func memorySettingsOut(m studiosvc.MemorySettings) schema.ChatsMemorySettings {
	return schema.ChatsMemorySettings{Enabled: m.Enabled, EnabledSet: m.EnabledSet, SaveMode: m.SaveMode, MessagingSave: m.MessagingSave,
		SaveModeSet: m.SaveModeSet, Provider: m.Provider, Available: m.Available}
}

func memoryStateOut(m studiosvc.MemoryReason) schema.ChatsMemoryState {
	out := schema.ChatsMemoryState{On: m.On, Code: m.Code, BlockedBy: m.By, Reason: m.Reason, Provider: m.Provider, ProviderTitle: m.ProviderTitle}
	if m.Fix != nil {
		out.Fix = &schema.ChatsMemoryFix{Kind: m.Fix.Kind, Target: m.Fix.Target, Field: m.Fix.Field}
	}
	return out
}

// GetMemory reads your memory choices.
// @Summary Read your memory settings.
// @Description Memory is the short notes the assistant reads before it answers and may save. enabled is the switch for all your chats and agents: a private chat (the memory group of a chat, see PUT /api/chats/{id}/builtins), an agent that turns memory off, or this switch off each make memory off in a chat, and the narrowest wins; a saved agent cannot widen your choice. enabled_set is false until you choose; before that memory is on, unless you had disabled the Memory add-on to stop it. save_mode is how a note is saved: auto (save it and tell me), ask (the default), always (always confirm: like ask, nothing can loosen it, and the model cannot undo or forget by itself) or never. It applies as the strictest rule together with the rule of the saved agent (memory_save: always and never only tighten it; auto lets that one agent save at once when your level is ask, while always and never of yours still hold) and the rules of the host. A chat from a messaging app asks, unless messaging_save is paired_private and the sender is paired and the chat is one-to-one; the row cannot loosen always or never. Your notes stay and can be listed, exported and deleted while memory is off.
// @Tags chats
// @Success 200 {object} schema.ChatsMemorySettings "Your memory settings"
// @Router /api/chats/memory [get]
func (h *Handlers) GetMemory(c echo.Context) error {
	m, err := h.svc.MemorySettings(c.Request().Context(), userID(c))
	if err != nil {
		return failMemory(c, err)
	}
	return c.JSON(http.StatusOK, memorySettingsOut(m))
}

// PutMemory changes your memory choices.
// @Summary Change your memory settings.
// @Description Sets enabled (memory On or Off for every chat and agent), save_mode (auto, ask, always or never) and messaging_save (ask, paired_private or never). A field left out stays as it is. Off means nothing is put in the prompt, the model has no tool or skill for notes, nothing is proposed or saved (also by triggers and other runs that start by themselves) and the model is told in one line that memory is off. The settings are kept per person with the account, so every frontend of a cluster reads the same answer. 400 for a value that is not allowed.
// @Tags chats
// @Accept json
// @Param request body schema.ChatsMemoryRequest true "the choices to change"
// @Success 200 {object} schema.ChatsMemorySettings "Your memory settings now in force"
// @Failure 400 {object} schema.ErrorResponse "A value is not allowed"
// @Failure 503 {object} schema.ErrorResponse "The settings cannot be stored on this server"
// @Router /api/chats/memory [put]
func (h *Handlers) PutMemory(c echo.Context) error {
	req, ok := bind[schema.ChatsMemoryRequest](c, "the body must be {enabled, save_mode, messaging_save}")
	if !ok {
		return nil
	}
	m, err := h.svc.SetMemory(c.Request().Context(), userID(c), studiosvc.MemoryUpdate{Enabled: req.Enabled, SaveMode: req.SaveMode, MessagingSave: req.MessagingSave})
	if err != nil {
		return failMemory(c, err)
	}
	return c.JSON(http.StatusOK, memorySettingsOut(m))
}

// ExportMemory downloads your notes.
// @Summary Download your memory notes as a zip file.
// @Description The Markdown files of your memory folders (yours, each agent's and each messaging conversation's) with their paths. scope (user, agent or channel) and key (the agent ID or the conversation folder name, as GET /api/chats/context-files lists them) narrow it to one group. It works while memory is off. 404 when there is nothing to export.
// @Tags chats
// @Produce application/zip
// @Param scope query string false "user, agent or channel"
// @Param key query string false "the agent ID or the conversation folder, with scope"
// @Success 200 {file} file "A zip file of the notes"
// @Failure 404 {object} schema.ErrorResponse "No notes"
// @Router /api/chats/memory/export [get]
func (h *Handlers) ExportMemory(c echo.Context) error {
	var buf bytes.Buffer
	if _, err := h.svc.ExportMemory(c.Request().Context(), userID(c), &buf, c.QueryParam("scope"), c.QueryParam("key")); err != nil {
		return failMemory(c, err)
	}
	c.Response().Header().Set(echo.HeaderContentDisposition, `attachment; filename="memory-notes.zip"`)
	return c.Blob(http.StatusOK, "application/zip", buf.Bytes())
}

// ForgetMemory asks the provider that serves your memory to forget notes.
// @Summary Forget notes at the provider that serves your memory.
// @Description Calls the forget tool of the add-on you picked as the provider of your memory. scope (user, agent or channel) and key narrow it to one group; empty forgets everything the provider keeps. resource names the resource the tool works on, when it has one. The change is applied at once, or stored as a proposal that you confirm on the Add-ons page (done is false then). The built-in provider has none: delete its notes in Files. 404 when the provider has no forget, 503 while the host leaves a failing provider alone, 502 when it does not answer.
// @Tags chats
// @Produce json
// @Param scope query string false "user, agent or channel"
// @Param key query string false "the agent ID or the conversation folder, with scope"
// @Param resource query string false "the resource the forget tool works on"
// @Success 200 {object} schema.ChatsMemoryForget "What the provider did"
// @Failure 404 {object} schema.ErrorResponse "No forget"
// @Failure 502 {object} schema.ErrorResponse "The provider did not answer"
// @Failure 503 {object} schema.ErrorResponse "The provider is resting"
// @Router /api/chats/memory [delete]
func (h *Handlers) ForgetMemory(c echo.Context) error {
	r, err := h.svc.ForgetMemory(c.Request().Context(), userID(c), c.QueryParam("scope"), c.QueryParam("key"), c.QueryParam("resource"))
	if err != nil {
		return failMemory(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsMemoryForget{Done: r.Done, Proposal: r.Proposal})
}
