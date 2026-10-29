package studio

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// maxImportBody caps the JSON body of a chat import.
const maxImportBody = 64 << 20

func settingsJSON(s session.ChatSettings) *schema.ChatsSettings {
	if s == (session.ChatSettings{}) {
		return nil
	}
	return &schema.ChatsSettings{SystemPrompt: s.SystemPrompt, Temperature: s.Temperature, TopP: s.TopP, TopK: s.TopK,
		ReasoningEffort: s.ReasoningEffort, ContextSize: s.ContextSize}
}

func settingsFrom(s schema.ChatsSettings) session.ChatSettings {
	return session.ChatSettings{SystemPrompt: s.SystemPrompt, Temperature: s.Temperature, TopP: s.TopP, TopK: s.TopK,
		ReasoningEffort: s.ReasoningEffort, ContextSize: s.ContextSize}
}

// checkModel answers a request that names a model the user may not use.
func (h *Handlers) checkModel(c echo.Context, model string) error {
	if h.models == nil {
		return nil
	}
	switch err := h.models.Check(c.Request().Context(), auth.GetUser(c), model); {
	case errors.Is(err, ErrUnknownModel):
		return fail(c, http.StatusBadRequest, "invalid_request_error", "unknown model")
	case errors.Is(err, ErrModelNotAllowed):
		return fail(c, http.StatusForbidden, "authorization_error", "you may not use this model")
	default:
		return err
	}
}

// Update changes a chat.
// @Summary Rename a chat, move it to another model or change its settings.
// @Description The settings (system prompt, temperature, top_p, top_k, reasoning effort, context size) are kept with the chat and apply from its next turn. A new title or model needs a chat with no turn running (409 otherwise).
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsUpdateRequest true "the fields to change"
// @Success 200 {object} schema.ChatsConversation "The chat after the change"
// @Router /api/chats/{id} [patch]
func (h *Handlers) Update(c echo.Context) error {
	req, ok := bind[schema.ChatsUpdateRequest](c, msgNotJSON)
	if !ok {
		return nil
	}
	if req.Title == nil && req.Model == nil && req.Settings == nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "name at least one of title, model and settings")
	}
	if req.Model != nil {
		if err := h.checkModel(c, strings.TrimSpace(*req.Model)); err != nil || c.Response().Committed {
			return err
		}
	}
	in := studiosvc.UpdateInput{Title: req.Title, Model: req.Model}
	if req.Settings != nil {
		set := settingsFrom(*req.Settings)
		in.Settings = &set
	}
	user := userID(c)
	rec, err := h.svc.Update(c.Request().Context(), user, c.Param("id"), in)
	if err != nil {
		return failFor(c, err)
	}
	out, err := h.describe(c, user, rec)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, out)
}

// Regenerate answers the last message again.
// @Summary Regenerate the last answer.
// @Description Removes everything the model said after your last message (all your messages of that turn stay), then starts a new turn. Files that the removed turn changed stay changed. 409 while a turn runs.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsSendResponse "The new turn started"
// @Router /api/chats/{id}/regenerate [post]
func (h *Handlers) Regenerate(c echo.Context) error {
	res, err := h.svc.Regenerate(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsSendResponse{Started: res.Started, PendingID: res.PendingID})
}

// EditMessage edits a sent message and runs it again.
// @Summary Edit a message you sent and run the chat again from it.
// @Description Replaces the text of your message with the given sequence number, removes everything after it, and starts a new turn. 409 while a turn runs; 400 when the line is not one of your messages.
// @Tags chats
// @Param id path string true "conversation id"
// @Param seq path int true "sequence number of the message in the transcript"
// @Param request body schema.ChatsEditRequest true "the new text"
// @Success 200 {object} schema.ChatsSendResponse "The new turn started"
// @Router /api/chats/{id}/messages/{seq}/edit [post]
func (h *Handlers) EditMessage(c echo.Context) error {
	seq, err := strconv.ParseInt(c.Param("seq"), 10, 64)
	if err != nil || seq <= 0 {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "seq must be a positive number")
	}
	var req schema.ChatsEditRequest
	if err := c.Bind(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "text is required")
	}
	res, err := h.svc.EditAndRerun(c.Request().Context(), userID(c), c.Param("id"), seq, req.Text)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsSendResponse{Started: res.Started, PendingID: res.PendingID})
}

// Fork copies a chat.
// @Summary Fork a chat from a message.
// @Description Copies the transcript up to and including the given line into a new chat with the same model, settings and tool switches. The original is not changed.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsForkRequest false "where to cut and the new title"
// @Success 201 {object} schema.ChatsConversation "The new chat"
// @Router /api/chats/{id}/fork [post]
func (h *Handlers) Fork(c echo.Context) error {
	var req schema.ChatsForkRequest
	if c.Request().ContentLength != 0 {
		if err := c.Bind(&req); err != nil {
			return fail(c, http.StatusBadRequest, "invalid_request_error", "the body is not valid JSON")
		}
	}
	if req.FromSeq < 0 {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "from_seq must not be negative")
	}
	user := userID(c)
	rec, err := h.svc.Fork(c.Request().Context(), user, c.Param("id"), req.FromSeq, req.Title)
	if err != nil {
		return failFor(c, err)
	}
	out, err := h.describe(c, user, rec)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusCreated, out)
}

// Search finds chats.
// @Summary Search your chats.
// @Description Finds chats whose title or messages contain the text, ignoring case. The newest chats come first. It reads the newest 500 chats.
// @Tags chats
// @Param q query string true "text to find"
// @Param limit query int false "most chats to return (default 50, at most 200)"
// @Success 200 {object} schema.ChatsChatSearchResponse "The matching chats"
// @Router /api/chats/search [get]
func (h *Handlers) Search(c echo.Context) error {
	q := strings.TrimSpace(c.QueryParam("q"))
	if q == "" {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "q is required")
	}
	user := userID(c)
	hits, err := h.svc.Search(c.Request().Context(), user, q, intParam(c, "limit", defaultListLimit, maxListLimit))
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsChatSearchResponse{Hits: make([]schema.ChatsChatHit, 0, len(hits))}
	for _, hit := range hits {
		conv, err := h.describe(c, user, hit.Chat)
		if err != nil {
			return failFor(c, err)
		}
		out.Hits = append(out.Hits, schema.ChatsChatHit{Chat: conv, InTitle: hit.InTitle, Seq: hit.Seq, Snippet: hit.Snippet})
	}
	return c.JSON(http.StatusOK, out)
}

func exportJSON(e studiosvc.ChatExport) schema.ChatsExport {
	out := schema.ChatsExport{Version: e.Version, ExportedAt: e.ExportedAt, Title: e.Title, Model: e.Model, Created: e.Created,
		Updated: e.Updated, Messages: make([]schema.ChatsExportMessage, 0, len(e.Messages))}
	if s := settingsJSON(e.Settings); s != nil {
		out.Settings = *s
	}
	for _, m := range e.Messages {
		out.Messages = append(out.Messages, schema.ChatsExportMessage{Role: m.Role, Text: m.Text, At: m.At})
	}
	return out
}

func exportFrom(e schema.ChatsExport) studiosvc.ChatExport {
	out := studiosvc.ChatExport{Version: e.Version, ExportedAt: e.ExportedAt, Title: e.Title, Model: e.Model, Settings: settingsFrom(e.Settings),
		Created: e.Created, Updated: e.Updated, Messages: make([]studiosvc.ChatExportMessage, 0, len(e.Messages))}
	for _, m := range e.Messages {
		out.Messages = append(out.Messages, studiosvc.ChatExportMessage{Role: m.Role, Text: m.Text, At: m.At})
	}
	return out
}

// Export returns a chat as a file.
// @Summary Export a chat.
// @Description Returns the chat as Markdown (format=markdown, the default) or as JSON (format=json), which POST /api/chats/import reads. Files, tool state and checkpoints are not exported.
// @Tags chats
// @Param id path string true "conversation id"
// @Param format query string false "markdown (default) or json"
// @Produce text/markdown,json
// @Success 200 {object} schema.ChatsExport "The chat (JSON format)"
// @Router /api/chats/{id}/export [get]
func (h *Handlers) Export(c echo.Context) error {
	format := strings.ToLower(c.QueryParam("format"))
	if format != "" && format != "markdown" && format != "json" {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "format must be markdown or json")
	}
	exp, err := h.svc.Export(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	stamp := time.Now().UTC().Format("2006-01-02")
	if format == "json" {
		c.Response().Header().Set(echo.HeaderContentDisposition, `attachment; filename="chat-`+stamp+`.json"`)
		return c.JSON(http.StatusOK, exportJSON(exp))
	}
	c.Response().Header().Set(echo.HeaderContentDisposition, `attachment; filename="chat-`+stamp+`.md"`)
	return c.Blob(http.StatusOK, "text/markdown; charset=utf-8", []byte(exp.Markdown()))
}

// Import stores an exported chat.
// @Summary Import an exported chat.
// @Description Creates a new chat from a JSON export. The model of the file is used unless the request names another; it must be one you may use. The chat has no files and no tool state; the next message starts it.
// @Tags chats
// @Param request body schema.ChatsImportRequest true "the export and an optional model"
// @Success 201 {object} schema.ChatsConversation "The new chat"
// @Failure 400 {object} schema.ErrorResponse "The body is not a valid export"
// @Failure 413 {object} schema.ErrorResponse "The export is too large"
// @Router /api/chats/import [post]
func (h *Handlers) Import(c echo.Context) error {
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxImportBody)
	req, ok := bind[schema.ChatsImportRequest](c, "the body is not a valid chat export")
	if !ok {
		return nil
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = strings.TrimSpace(req.Chat.Model)
	}
	if model == "" {
		return failFor(c, studiosvc.ErrNoModel)
	}
	if err := h.checkModel(c, model); err != nil || c.Response().Committed {
		return err
	}
	user := userID(c)
	rec, err := h.svc.ImportExport(c.Request().Context(), user, exportFrom(req.Chat), model)
	if err != nil {
		return failFor(c, err)
	}
	out, err := h.describe(c, user, rec)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusCreated, out)
}
