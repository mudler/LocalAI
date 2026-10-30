// Package studio has the HTTP handlers of the chats API: conversations the
// server keeps, with steering, sub-agents and live events.
package studio

import (
	"context"
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
	"github.com/mudler/LocalAI/core/services/studio/pages"
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
	pages  *pages.Service
	trust  *pages.Trust
}

// New returns the handlers. A nil models skips the model check.
func New(svc *studiosvc.Service, models ModelChecker) *Handlers {
	h := &Handlers{svc: svc, models: models}
	if svc != nil {
		h.pages = pages.New(svc.Files())
		store := svc.PageConsents()
		if store == nil {
			store, _ = pages.NewJSONConsents("")
		}
		h.trust = pages.NewTrust(h.pages, store)
	}
	return h
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

// feature is the guard of the endpoints of a feature that can be off: it
// returns host when the feature is on. When host is nil the feature is off: it
// writes the answer of off and returns false, and the handler returns nil, as
// after a failed bind.
func feature[T any](c echo.Context, host *T, off func(echo.Context) error) (*T, bool) {
	if host == nil {
		_ = off(c)
		return nil, false
	}
	return host, true
}

func fail(c echo.Context, status int, typ, msg string) error {
	return c.JSON(status, schema.ErrorResponse{Error: &schema.APIError{Message: msg, Code: status, Type: typ}})
}

// chatRules maps the errors of the chat service. Another user's conversation
// is the same answer as a missing one.
var chatRules = []errRule{
	fixed(http.StatusNotFound, typeNotFound, "conversation not found", session.ErrNotFound),
	fixed(http.StatusConflict, typeConflict, "the message was already delivered to the model", store.ErrDelivered),
	fixed(http.StatusNotFound, typeNotFound, "agent template not found", studiosvc.ErrNoTemplate),
	fixed(http.StatusConflict, typeConflict, "a turn is running; stop it first", studiosvc.ErrChatBusy),
	verbatim(http.StatusBadRequest, typeInvalid, "studio: ", studiosvc.ErrNothingToRegenerate, studiosvc.ErrNotUserMessage,
		studiosvc.ErrInvalidSettings, studiosvc.ErrBadImport, session.ErrInvalid),
	verbatim(http.StatusBadRequest, typeInvalid, "", studiosvc.ErrBadSkillName, studiosvc.ErrBadAddonID, studiosvc.ErrBadMCPSwitch, studiosvc.ErrBadBuiltinGroup),
	verbatim(http.StatusConflict, typeFilesOff, "", files.ErrUnavailable),
	fixed(http.StatusRequestEntityTooLarge, typeInvalid, "an attachment is too large", workspace.ErrTooLarge),
	fixed(http.StatusConflict, typeConflict, "a sub-agent conversation takes no messages from you", studiosvc.ErrSubAgentReadOnly),
	verbatim(http.StatusBadRequest, typeInvalid, "", studiosvc.ErrNoModel),
	fixed(http.StatusNotFound, typeNotFound, "agent not found", agents.ErrNotFound),
	fixed(http.StatusConflict, typeConflict, "you already have an agent with this name", agents.ErrNameTaken),
	verbatim(http.StatusBadRequest, typeInvalid, agents.ErrInvalid.Error()+": ", agents.ErrInvalid),
	verbatim(http.StatusConflict, typeNotifyOff, "", studiosvc.ErrNotificationsOff),
	fixed(http.StatusForbidden, typeAuthz, "the LocalAI assistant needs an administrator", studiosvc.ErrAssistantForbidden),
	fixed(http.StatusServiceUnavailable, "assistant_off", "the LocalAI assistant is switched off on this server", studiosvc.ErrAssistantOff),
	fixed(http.StatusBadRequest, typeInvalid, "an assistant chat cannot name a saved agent", studiosvc.ErrAssistantAgent),
	verbatim(http.StatusConflict, typeAgentsOff, "", studiosvc.ErrAgentsOff),
}

// failFor maps a service error to a response.
func failFor(c echo.Context, err error) error {
	return mapErr(c, err, chatRules, func(c echo.Context, err error) error {
		xlog.Error("studio: request failed", "path", c.Request().URL.Path, "error", err)
		return fail(c, http.StatusInternalServerError, typeServerError, "the request failed")
	})
}

func conversation(rec session.ChatRecord, info store.TurnInfo) schema.ChatsConversation {
	return schema.ChatsConversation{
		ID: rec.ID, Title: rec.Title, Model: rec.Model, Kind: rec.Kind, ParentID: rec.ParentID,
		AgentID: rec.AgentID, Origin: rec.Origin, OriginID: rec.OriginID, Assistant: rec.Assistant,
		Created: rec.Created, Updated: rec.Updated, Running: info.Running, LastTurn: string(info.Last),
		Settings: settingsJSON(rec.Settings),
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
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 403 {object} schema.ErrorResponse "Refused"
// @Router /api/chats [post]
func (h *Handlers) Create(c echo.Context) error {
	var req schema.ChatsCreateRequest
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "the body is not valid JSON")
	}
	if req.Assistant {
		// The service checks the role again from its user lookup; this answer
		// comes first so a refusal never depends on that lookup being set.
		if u := auth.GetUser(c); u != nil && u.Role != auth.RoleAdmin {
			return failFor(c, studiosvc.ErrAssistantForbidden)
		}
	}
	if req.AgentID != "" && !req.Assistant && !auth.FeatureAllowed(c, auth.FeatureAgents) {
		return fail(c, http.StatusForbidden, "authorization_error", "feature not enabled for your account: "+auth.FeatureAgents)
	}
	model := req.Model
	if req.AgentID != "" && !req.Assistant {
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
	rec, err := h.svc.Create(c.Request().Context(), user, studiosvc.CreateInput{Title: req.Title, Model: req.Model, AgentID: req.AgentID, Assistant: req.Assistant})
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
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
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
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
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
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/messages [get]
func (h *Handlers) Messages(c echo.Context) error {
	after, _ := strconv.ParseInt(c.QueryParam("after"), 10, 64)
	entries, err := h.svc.Messages(c.Request().Context(), userID(c), c.Param("id"), after, intParam(c, "limit", defaultMessages, maxMessages))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, messageList(h.svc.Principal(userID(c)), entries))
}

// homePaths turns the workspace paths of a checkpoint line into paths inside
// the user's home, one a line, and drops the others.
func homePaths(principal, text string) string {
	home := session.HomePath(principal)
	var out []string
	for _, p := range strings.Split(text, "\n") {
		if rel, ok := strings.CutPrefix(p, home+"/"); ok && rel != "" {
			out = append(out, "/"+rel)
		}
	}
	return strings.Join(out, "\n")
}

func messageList(principal string, entries []session.Entry) schema.ChatsMessageList {
	out := schema.ChatsMessageList{Messages: make([]schema.ChatsMessage, 0, len(entries))}
	for _, e := range entries {
		m := schema.ChatsMessage{
			Seq: e.Seq, Turn: e.Turn, Role: e.Role, Text: e.Text, DurMs: e.Dur.Milliseconds(), At: e.At,
		}
		if e.Card != nil {
			m.App = appJSON(e.Card.App, true)
			m.Card = &schema.ChatsToolCard{Kind: e.Card.Kind, ID: e.Card.ID}
		}
		if e.Role == session.RoleCheckpoint {
			m.Rev, m.Text = e.Rev, homePaths(principal, e.Text)
		}
		out.Messages = append(out.Messages, m)
	}
	return out
}

// Send gives the conversation a message.
// @Summary Send a message to a chat.
// @Description With no turn running the message starts one. With a turn running it waits and reaches the model, with any others, at its next request.
// @Tags chats
// @Param id path string true "conversation id"
// @Description Send JSON {"text"}, or multipart/form-data with a text field and files to attach. Files are saved in the chat's folder of your workspace. With a turn running the files are saved at once and the message waits like any other, with their paths in its text.
// @Accept json,mpfd
// @Param request body schema.ChatsSendRequest true "the message"
// @Param files formData file false "files to attach (multipart)"
// @Success 200 {object} schema.ChatsSendResponse "Whether a turn started, or the id of the waiting message"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/messages [post]
func (h *Handlers) Send(c echo.Context) error {
	var (
		text string
		up   []session.Upload
		refs []schema.ChatsMCPRef
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
		if v := form.Value["context"]; len(v) > 0 {
			var err error
			if refs, err = parseContext(v[0]); err != nil {
				return fail(c, http.StatusBadRequest, "invalid_request_error", "context must be a JSON list of prompts and resources")
			}
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
		text, refs = req.Text, req.Context
	}
	if text == "" && len(up) == 0 && len(refs) == 0 {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "text is required")
	}
	var err error
	if text, err = h.svc.WithMCPContext(c.Request().Context(), userID(c), c.Param("id"), text, mcpRefs(refs)); err != nil {
		return failMCPContent(c, err)
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
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
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
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
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
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
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
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/stop [post]
func (h *Handlers) Stop(c echo.Context) error {
	if err := h.svc.Stop(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"stopped": true})
}

// Events streams the conversation's live events.
// @Summary Stream a chat's events.
// @Description Server-sent events: text, reasoning, tool_start, tool_end, checkpoint, turn_start, turn_end, error, pending_delivered, sub_agent_done, compacted and files. Events are live only; read the transcript for what came before.
// @Tags chats
// @Param id path string true "conversation id"
// @Produce text/event-stream
// @Success 200 {object} schema.ChatsEvent "An event stream"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/events [get]
func (h *Handlers) Events(c echo.Context) error {
	events, cancel, err := h.svc.Events(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	defer cancel()

	return streamSSE(c, events, func(ev session.Event) (string, any) { return string(ev.Kind), eventJSON(ev) })
}

func eventJSON(ev session.Event) schema.ChatsEvent {
	out := schema.ChatsEvent{Kind: string(ev.Kind), Turn: ev.Turn, Text: ev.Text, IDs: ev.IDs, Before: ev.Before, After: ev.After,
		Code: ev.Code, Model: ev.Model, Retry: ev.Retry, Detail: ev.Detail}
	if u := ev.Usage; u != nil {
		out.Usage = &schema.ChatsUsage{
			Used: u.Used, Limit: u.Limit, Completion: u.Completion, TokensPerSecond: u.TokensPerSecond,
			Model: u.Model, Node: u.Node, PromptMs: u.PromptMillis,
		}
	}
	if ev.Tool != nil {
		out.Tool = &schema.ChatsToolEvent{Name: ev.Tool.Name, Summary: ev.Tool.Summary, OK: ev.Tool.OK, Output: ev.Tool.Output, Seq: ev.Tool.Seq}
		if ev.Tool.Card != nil {
			out.Tool.App = appJSON(ev.Tool.Card.App, false)
			out.Tool.Card = &schema.ChatsToolCard{Kind: ev.Tool.Card.Kind, ID: ev.Tool.Card.ID}
		}
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
