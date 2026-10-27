// Package studio has the HTTP handlers of the chats API: conversations the
// server keeps, with steering, sub-agents and live events.
package studio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Errors a ModelChecker returns.
var (
	// ErrUnknownModel means no model has this name.
	ErrUnknownModel = errors.New("unknown model")
	// ErrModelNotAllowed means the user may not use the model.
	ErrModelNotAllowed = errors.New("model not allowed")
)

// ModelChecker says whether a user may start a conversation with a model.
type ModelChecker interface {
	Check(ctx context.Context, user *auth.User, model string) error
}

// ModelCheckerFunc adapts a function to ModelChecker.
type ModelCheckerFunc func(ctx context.Context, user *auth.User, model string) error

// Check implements ModelChecker.
func (f ModelCheckerFunc) Check(ctx context.Context, user *auth.User, model string) error {
	return f(ctx, user, model)
}

// Handlers serves the conversation API.
type Handlers struct {
	svc    *studiosvc.Service
	models ModelChecker
}

// New returns the handlers. A nil models skips the model check.
func New(svc *studiosvc.Service, models ModelChecker) *Handlers {
	return &Handlers{svc: svc, models: models}
}

// maxAttachBytes caps one message with its attachments.
const maxAttachBytes = 1 << 30

const (
	defaultListLimit = 50
	maxListLimit     = 200
	defaultMessages  = 200
	maxMessages      = 1000
	heartbeat        = 15 * time.Second
)

func userID(c echo.Context) string {
	if u := auth.GetUser(c); u != nil {
		return u.ID
	}
	return ""
}

func fail(c echo.Context, status int, typ, msg string) error {
	return c.JSON(status, schema.ErrorResponse{Error: &schema.APIError{Message: msg, Code: status, Type: typ}})
}

// failFor maps a service error to a response. Another user's conversation is
// the same answer as a missing one.
func failFor(c echo.Context, err error) error {
	switch {
	case errors.Is(err, session.ErrNotFound):
		return fail(c, http.StatusNotFound, "not_found", "conversation not found")
	case errors.Is(err, store.ErrDelivered):
		return fail(c, http.StatusConflict, "conflict", "the message was already delivered to the model")
	case errors.Is(err, studiosvc.ErrBadSkillName), errors.Is(err, studiosvc.ErrBadAddonID):
		return fail(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	case errors.Is(err, studiosvc.ErrFilesWhileRunning):
		return fail(c, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, files.ErrUnavailable):
		return fail(c, http.StatusConflict, "files_off", err.Error())
	case errors.Is(err, workspace.ErrTooLarge):
		return fail(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "an attachment is too large")
	case errors.Is(err, studiosvc.ErrSubAgentReadOnly):
		return fail(c, http.StatusConflict, "conflict", "a sub-agent conversation takes no messages from you")
	case errors.Is(err, studiosvc.ErrNoModel):
		return fail(c, http.StatusBadRequest, "invalid_request_error", err.Error())
	case errors.Is(err, agents.ErrNotFound):
		return fail(c, http.StatusNotFound, "not_found", "agent not found")
	case errors.Is(err, agents.ErrNameTaken):
		return fail(c, http.StatusConflict, "conflict", "you already have an agent with this name")
	case errors.Is(err, agents.ErrInvalid):
		return fail(c, http.StatusBadRequest, "invalid_request_error", strings.TrimPrefix(err.Error(), agents.ErrInvalid.Error()+": "))
	case errors.Is(err, studiosvc.ErrNotificationsOff):
		return fail(c, http.StatusConflict, "notifications_off", err.Error())
	case errors.Is(err, studiosvc.ErrAgentsOff):
		return fail(c, http.StatusConflict, "agents_off", err.Error())
	default:
		xlog.Error("studio: request failed", "path", c.Request().URL.Path, "error", err)
		return fail(c, http.StatusInternalServerError, "server_error", "the request failed")
	}
}

func conversation(rec session.ChatRecord, info store.TurnInfo) schema.ChatsConversation {
	return schema.ChatsConversation{
		ID: rec.ID, Title: rec.Title, Model: rec.Model, Kind: rec.Kind, ParentID: rec.ParentID,
		AgentID: rec.AgentID, Origin: rec.Origin, OriginID: rec.OriginID,
		Created: rec.Created, Updated: rec.Updated, Running: info.Running, LastTurn: string(info.Last),
	}
}

func (h *Handlers) describe(c echo.Context, user string, rec session.ChatRecord) (schema.ChatsConversation, error) {
	info, err := h.svc.Turn(c.Request().Context(), user, rec.ID)
	if err != nil {
		return schema.ChatsConversation{}, err
	}
	return conversation(rec, info), nil
}

// Create starts a conversation.
// @Summary Start a chat.
// @Tags chats
// @Param request body schema.ChatsCreateRequest true "model and optional title"
// @Success 201 {object} schema.ChatsConversation "The new conversation"
// @Router /api/chats [post]
func (h *Handlers) Create(c echo.Context) error {
	var req schema.ChatsCreateRequest
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "the body is not valid JSON")
	}
	model := req.Model
	if req.AgentID != "" {
		ag, err := h.svc.Agent(c.Request().Context(), userID(c), req.AgentID)
		if err != nil {
			return failFor(c, err)
		}
		if ag.Model != "" {
			model = ag.Model
		}
	}
	if model == "" {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "model is required")
	}
	if h.models != nil {
		switch err := h.models.Check(c.Request().Context(), auth.GetUser(c), model); {
		case errors.Is(err, ErrUnknownModel):
			return fail(c, http.StatusBadRequest, "invalid_request_error", "unknown model")
		case errors.Is(err, ErrModelNotAllowed):
			return fail(c, http.StatusForbidden, "authorization_error", "you may not use this model")
		case err != nil:
			return failFor(c, err)
		}
	}
	user := userID(c)
	rec, err := h.svc.Create(c.Request().Context(), user, studiosvc.CreateInput{Title: req.Title, Model: req.Model, AgentID: req.AgentID})
	if err != nil {
		return failFor(c, err)
	}
	out, err := h.describe(c, user, rec)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusCreated, out)
}

// List returns the user's conversations, newest first.
// @Summary List chats.
// @Tags chats
// @Param limit query int false "most conversations to return (default 50, at most 200)"
// @Param kind query string false "with chat, sub-agent runs are left out and the limit counts chats"
// @Success 200 {object} schema.ChatsConversationList "Conversations, including sub-agent runs unless kind=chat"
// @Router /api/chats [get]
func (h *Handlers) List(c echo.Context) error {
	limit := intParam(c, "limit", defaultListLimit, maxListLimit)
	user := userID(c)
	list := h.svc.List
	if c.QueryParam("kind") == "chat" {
		list = h.svc.ListChats
	}
	recs, err := list(c.Request().Context(), user, limit)
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsConversationList{Conversations: make([]schema.ChatsConversation, 0, len(recs)), Persistent: h.svc.Persistent(), Files: h.svc.Files().Available(), FilesReason: h.svc.Files().Unavailable()}
	for _, rec := range recs {
		conv, err := h.describe(c, user, rec)
		if err != nil {
			return failFor(c, err)
		}
		out.Conversations = append(out.Conversations, conv)
	}
	return c.JSON(http.StatusOK, out)
}

// Get returns one conversation with its waiting messages.
// @Summary Get a chat.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsConversationDetail "The conversation and its pending messages"
// @Router /api/chats/{id} [get]
func (h *Handlers) Get(c echo.Context) error {
	user, id := userID(c), c.Param("id")
	rec, err := h.svc.Get(c.Request().Context(), user, id)
	if err != nil {
		return failFor(c, err)
	}
	conv, err := h.describe(c, user, rec)
	if err != nil {
		return failFor(c, err)
	}
	pend, err := h.svc.Pending(c.Request().Context(), user, id)
	if err != nil {
		return failFor(c, err)
	}
	// Opening a chat is how a run's notifications are read.
	h.svc.MarkChatNotificationsRead(c.Request().Context(), user, id)
	out := schema.ChatsConversationDetail{ChatsConversation: conv, Pending: make([]schema.ChatsPending, 0, len(pend))}
	for _, p := range pend {
		out.Pending = append(out.Pending, schema.ChatsPending{ID: p.ID, Text: p.Text, Seq: p.Seq})
	}
	return c.JSON(http.StatusOK, out)
}

// Delete removes a conversation.
// @Summary Delete a chat.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} map[string]bool "deleted"
// @Router /api/chats/{id} [delete]
func (h *Handlers) Delete(c echo.Context) error {
	if err := h.svc.Delete(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"deleted": true})
}

// Messages returns the transcript.
// @Summary Read a chat's transcript.
// @Tags chats
// @Param id path string true "conversation id"
// @Param after query int false "only lines after this sequence number"
// @Param limit query int false "most lines to return (default 200, at most 1000)"
// @Success 200 {object} schema.ChatsMessageList "Transcript lines in order"
// @Router /api/chats/{id}/messages [get]
func (h *Handlers) Messages(c echo.Context) error {
	after, _ := strconv.ParseInt(c.QueryParam("after"), 10, 64)
	entries, err := h.svc.Messages(c.Request().Context(), userID(c), c.Param("id"), after, intParam(c, "limit", defaultMessages, maxMessages))
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsMessageList{Messages: make([]schema.ChatsMessage, 0, len(entries))}
	for _, e := range entries {
		out.Messages = append(out.Messages, schema.ChatsMessage{
			Seq: e.Seq, Turn: e.Turn, Role: e.Role, Text: e.Text, DurMs: e.Dur.Milliseconds(), At: e.At,
		})
	}
	return c.JSON(http.StatusOK, out)
}

// Send gives the conversation a message.
// @Summary Send a message to a chat.
// @Description With no turn running the message starts one. With a turn running it waits and reaches the model, with any others, at its next request.
// @Tags chats
// @Param id path string true "conversation id"
// @Description Send JSON {"text"}, or multipart/form-data with a text field and files to attach. Files are saved in the chat's folder of your workspace and go only with a message that starts a turn.
// @Accept json,mpfd
// @Param request body schema.ChatsSendRequest true "the message"
// @Param files formData file false "files to attach (multipart)"
// @Success 200 {object} schema.ChatsSendResponse "Whether a turn started, or the id of the waiting message"
// @Router /api/chats/{id}/messages [post]
func (h *Handlers) Send(c echo.Context) error {
	var (
		text string
		up   []session.Upload
	)
	if strings.HasPrefix(c.Request().Header.Get("Content-Type"), "multipart/form-data") {
		c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxAttachBytes)
		form, err := c.MultipartForm()
		if err != nil {
			return fail(c, http.StatusBadRequest, "invalid_request_error", "the attachments could not be read")
		}
		defer func() { _ = form.RemoveAll() }()
		if v := form.Value["text"]; len(v) > 0 {
			text = v[0]
		}
		for _, fh := range form.File["files"] {
			f, err := fh.Open()
			if err != nil {
				return fail(c, http.StatusBadRequest, "invalid_request_error", "an attachment could not be read")
			}
			defer f.Close()
			up = append(up, session.Upload{Name: fh.Filename, Body: f})
		}
	} else {
		var req schema.ChatsSendRequest
		if err := c.Bind(&req); err != nil {
			return fail(c, http.StatusBadRequest, "invalid_request_error", "text is required")
		}
		text = req.Text
	}
	if text == "" && len(up) == 0 {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "text is required")
	}
	res, err := h.svc.SendWithFiles(c.Request().Context(), userID(c), c.Param("id"), text, up)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsSendResponse{Started: res.Started, PendingID: res.PendingID})
}

// EditPending changes a waiting message.
// @Summary Edit a message that waits for the model.
// @Tags chats
// @Param id path string true "conversation id"
// @Param pid path string true "pending message id"
// @Param request body schema.ChatsPendingEdit true "the new text"
// @Success 200 {object} map[string]bool "updated"
// @Router /api/chats/{id}/pending/{pid} [patch]
func (h *Handlers) EditPending(c echo.Context) error {
	var req schema.ChatsPendingEdit
	if err := c.Bind(&req); err != nil || req.Text == "" {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "text is required")
	}
	if err := h.svc.EditPending(c.Request().Context(), userID(c), c.Param("id"), c.Param("pid"), req.Text); err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"updated": true})
}

// CancelPending removes a waiting message.
// @Summary Cancel a message that waits for the model.
// @Tags chats
// @Param id path string true "conversation id"
// @Param pid path string true "pending message id"
// @Success 200 {object} map[string]bool "cancelled"
// @Router /api/chats/{id}/pending/{pid} [delete]
func (h *Handlers) CancelPending(c echo.Context) error {
	if err := h.svc.CancelPending(c.Request().Context(), userID(c), c.Param("id"), c.Param("pid")); err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"cancelled": true})
}

// SendNow starts a turn from the waiting messages.
// @Summary Send the waiting messages now.
// @Description After a stop or a failed turn the waiting messages stay put. This starts a turn from them.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} map[string]bool "sent"
// @Router /api/chats/{id}/send-now [post]
func (h *Handlers) SendNow(c echo.Context) error {
	if err := h.svc.SendNow(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"sent": true})
}

// Stop ends the running turn.
// @Summary Stop the running turn.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} map[string]bool "stopped"
// @Router /api/chats/{id}/stop [post]
func (h *Handlers) Stop(c echo.Context) error {
	if err := h.svc.Stop(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"stopped": true})
}

// Events streams the conversation's live events.
// @Summary Stream a chat's events.
// @Description Server-sent events: text, reasoning, tool_start, tool_end, checkpoint, turn_start, turn_end, error, pending_delivered, sub_agent_done and compacted. Events are live only; read the transcript for what came before.
// @Tags chats
// @Param id path string true "conversation id"
// @Produce text/event-stream
// @Success 200 {object} schema.ChatsEvent "An event stream"
// @Router /api/chats/{id}/events [get]
func (h *Handlers) Events(c echo.Context) error {
	events, cancel, err := h.svc.Events(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	defer cancel()

	w := c.Response()
	w.Header().Set(echo.HeaderContentType, "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	w.Flush()

	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-c.Request().Context().Done():
			return nil
		case <-tick.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return nil
			}
			w.Flush()
		case ev, ok := <-events:
			if !ok {
				return nil // the subscriber fell behind; the client reconnects
			}
			raw, err := json.Marshal(eventJSON(ev))
			if err != nil {
				continue
			}
			if _, err := w.Write([]byte("event: " + string(ev.Kind) + "\ndata: " + string(raw) + "\n\n")); err != nil {
				return nil
			}
			w.Flush()
		}
	}
}

func eventJSON(ev session.Event) schema.ChatsEvent {
	out := schema.ChatsEvent{Kind: string(ev.Kind), Turn: ev.Turn, Text: ev.Text, IDs: ev.IDs, Before: ev.Before, After: ev.After}
	if ev.Tool != nil {
		out.Tool = &schema.ChatsToolEvent{Name: ev.Tool.Name, Summary: ev.Tool.Summary, OK: ev.Tool.OK, Output: ev.Tool.Output}
	}
	return out
}

func intParam(c echo.Context, name string, def, max int) int {
	n, err := strconv.Atoi(c.QueryParam(name))
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
