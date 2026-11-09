package studio

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/coordinator"
)

const typeCoordinatorsOff = "coordinators_off"

// coordinators is the guard of the coordinator routes: the host, or the answer
// of a server that has none.
func (h *Handlers) coordinators(c echo.Context) (*studiosvc.Coordinators, bool) {
	return feature(c, h.svc.Coordinators(), func(c echo.Context) error {
		return failFor(c, studiosvc.ErrCoordinatorsOff)
	})
}

func actorOf(c echo.Context) studiosvc.CoordinatorActor {
	return studiosvc.CoordinatorActor{User: userID(c), Admin: isAdmin(c)}
}

func seconds(d time.Duration) int { return int(d / time.Second) }

func timeOut(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func usageOut(u studiosvc.CoordinatorUsage) schema.ChatsCoordinatorUsage {
	return schema.ChatsCoordinatorUsage{RunsToday: u.RunsToday, SentToday: u.SentToday, RunsLimit: u.RunsLimit, SendLimit: u.SendLimit}
}

func contactsOut(cs []coordinator.Contact) []schema.ChatsCoordinatorContact {
	out := make([]schema.ChatsCoordinatorContact, 0, len(cs))
	for _, ct := range cs {
		out = append(out, schema.ChatsCoordinatorContact{ID: ct.ID, Name: ct.Name, Description: ct.Description,
			Kind: string(ct.Kind), User: ct.User, Binding: ct.Binding, Chat: ct.Chat, Sender: ct.Sender})
	}
	return out
}

// coordinatorOut is the owner's view, with the defaults of the caps and
// limits filled in.
func (h *Handlers) coordinatorOut(c echo.Context, v studiosvc.CoordinatorView) schema.ChatsCoordinator {
	cfg := v.Config.WithDefaults()
	agent := h.agentFull(c, v.Agent)
	agent.Coordinator = true
	return schema.ChatsCoordinator{
		ChatsAgent: agent, ChatID: cfg.ChatID, Page: cfg.Page, Contacts: contactsOut(cfg.Contacts),
		WakeEvery: seconds(cfg.WakeEvery), WakeNote: cfg.WakeNote,
		SendPerMinute: cfg.SendPerMinute, SendPerDay: cfg.SendPerDay, MaxHelpers: cfg.MaxHelpers, MaxDepth: cfg.MaxDepth,
		Paused: cfg.Paused, PausedBy: cfg.PausedBy, PausedAt: timeOut(cfg.PausedAt),
		NextWake: timeOut(cfg.NextWake), LastWake: timeOut(cfg.LastWake), Usage: usageOut(v.Usage),
	}
}

func adminCoordinatorOut(v studiosvc.CoordinatorView) schema.ChatsAdminCoordinator {
	cfg := v.Config
	return schema.ChatsAdminCoordinator{
		ID: v.Agent.ID, Owner: cfg.Principal, Name: v.Agent.Name, Description: v.Agent.Description, Model: v.Agent.Model,
		ChatID: cfg.ChatID, Contacts: len(cfg.Contacts), WakeEvery: seconds(cfg.WakeEvery),
		Paused: cfg.Paused, PausedBy: cfg.PausedBy, PausedAt: timeOut(cfg.PausedAt),
		NextWake: timeOut(cfg.NextWake), LastWake: timeOut(cfg.LastWake), Usage: usageOut(v.Usage),
		Created: cfg.Created, Updated: cfg.Updated,
	}
}

// coordinatorIn reads the body: the agent, after the model check, and the
// config. When ok is false the refusal is written already.
func (h *Handlers) coordinatorIn(c echo.Context) (in studiosvc.CoordinatorInput, ok bool, err error) {
	var req schema.ChatsCoordinatorWrite
	if err := c.Bind(&req); err != nil {
		return in, false, fail(c, http.StatusBadRequest, typeInvalid, msgNotJSON)
	}
	a, _, ok, err := h.agentOf(c, schema.ChatsAgentWrite{Name: req.Name, Description: req.Description,
		Instructions: req.Instructions, Model: req.Model, MCPServers: req.MCPServers, Addons: req.Addons,
		Collections: req.Collections, Skills: req.Skills, Network: req.Network, Memory: req.Memory,
		MemorySave: req.MemorySave, BuiltinsOff: req.BuiltinsOff, BuiltinsOn: req.BuiltinsOn, Builtins: req.Builtins})
	if !ok {
		return in, false, err
	}
	cfg := coordinator.Config{
		Page: req.Page, WakeEvery: time.Duration(req.WakeEvery) * time.Second, WakeNote: req.WakeNote,
		SendPerMinute: req.SendPerMinute, SendPerDay: req.SendPerDay, MaxHelpers: req.MaxHelpers, MaxDepth: req.MaxDepth,
	}
	for _, ct := range req.Contacts {
		cfg.Contacts = append(cfg.Contacts, coordinator.Contact{ID: ct.ID, Name: ct.Name, Description: ct.Description,
			Kind: coordinator.ContactKind(ct.Kind), User: ct.User, Binding: ct.Binding, Chat: ct.Chat, Sender: ct.Sender})
	}
	return studiosvc.CoordinatorInput{Agent: a, Config: cfg}, true, nil
}

// CreateCoordinator makes a coordinator.
// @Summary Create a coordinator.
// @Description A coordinator is a saved agent with one long-lived chat that wakes on a schedule and on events (a reply, a finished helper), starts helpers and contacts only the people listed in contacts. The server enforces the limits: send caps per minute and day, a daily budget of wake-started runs, a ceiling on helpers, one switch to pause everything, and an audit log. The agent gets the act.files grant so it can write its page. Errors: 400 with the reason for a bad contact, schedule or a value above the operator ceiling; 404 coordinators_off when this server has no coordinators.
// @Tags chats
// @Param request body schema.ChatsCoordinatorWrite true "the coordinator"
// @Success 201 {object} schema.ChatsCoordinator "The coordinator"
// @Failure 400 {object} schema.ErrorResponse "The definition, a contact or a limit is not valid"
// @Failure 404 {object} schema.ErrorResponse "Coordinators are off on this server"
// @Router /api/chats/coordinators [post]
func (h *Handlers) CreateCoordinator(c echo.Context) error {
	host, ok := h.coordinators(c)
	if !ok {
		return nil
	}
	in, ok, err := h.coordinatorIn(c)
	if !ok {
		return err
	}
	v, err := host.Create(c.Request().Context(), userID(c), in)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusCreated, h.coordinatorOut(c, v))
}

// ListCoordinators lists the coordinators of the caller.
// @Summary List your coordinators.
// @Tags chats
// @Success 200 {object} schema.ChatsCoordinatorList "Your coordinators, by name"
// @Failure 404 {object} schema.ErrorResponse "Coordinators are off on this server"
// @Router /api/chats/coordinators [get]
func (h *Handlers) ListCoordinators(c echo.Context) error {
	host, ok := h.coordinators(c)
	if !ok {
		return nil
	}
	list, err := host.List(c.Request().Context(), userID(c))
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsCoordinatorList{Coordinators: make([]schema.ChatsCoordinator, 0, len(list))}
	for _, v := range list {
		out.Coordinators = append(out.Coordinators, h.coordinatorOut(c, v))
	}
	return c.JSON(http.StatusOK, out)
}

// GetCoordinator reads a coordinator of the caller.
// @Summary Read one of your coordinators.
// @Tags chats
// @Param id path string true "the coordinator's agent id"
// @Success 200 {object} schema.ChatsCoordinator "The coordinator"
// @Failure 404 {object} schema.ErrorResponse "No such coordinator, or it is another user's"
// @Router /api/chats/coordinators/{id} [get]
func (h *Handlers) GetCoordinator(c echo.Context) error {
	host, ok := h.coordinators(c)
	if !ok {
		return nil
	}
	v, err := host.Get(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, h.coordinatorOut(c, v))
}

// UpdateCoordinator replaces a coordinator of the caller.
// @Summary Replace one of your coordinators.
// @Description The body replaces the agent and the settings. Its chat, its pause state and its wake times stay as they are. A builtins_on and builtins_off the body does not send keep the stored ones.
// @Tags chats
// @Param id path string true "the coordinator's agent id"
// @Param request body schema.ChatsCoordinatorWrite true "the coordinator"
// @Success 200 {object} schema.ChatsCoordinator "The coordinator"
// @Failure 400 {object} schema.ErrorResponse "The definition, a contact or a limit is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such coordinator, or it is another user's"
// @Router /api/chats/coordinators/{id} [put]
func (h *Handlers) UpdateCoordinator(c echo.Context) error {
	host, ok := h.coordinators(c)
	if !ok {
		return nil
	}
	in, ok, err := h.coordinatorIn(c)
	if !ok {
		return err
	}
	v, err := host.Update(c.Request().Context(), userID(c), c.Param("id"), in)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, h.coordinatorOut(c, v))
}

// PauseCoordinator pauses a coordinator.
// @Summary Pause a coordinator.
// @Description The one switch: it stops all outbound messages at once, cancels the coordinator's run and helpers, and holds every message to its chat until resume. The owner or an administrator may press it; another user gets 404. Pressing it again on a paused coordinator repeats the stop and changes nothing else.
// @Tags chats
// @Param id path string true "the coordinator's agent id"
// @Success 200 {object} schema.ChatsAdminCoordinator "The coordinator, paused"
// @Failure 404 {object} schema.ErrorResponse "No such coordinator, or it is another user's"
// @Router /api/chats/coordinators/{id}/pause [post]
func (h *Handlers) PauseCoordinator(c echo.Context) error {
	return h.setPaused(c, true)
}

// ResumeCoordinator resumes a coordinator.
// @Summary Resume a coordinator.
// @Description Lifts the pause and starts the messages held meanwhile, as one turn. The owner or an administrator may press it; another user gets 404.
// @Tags chats
// @Param id path string true "the coordinator's agent id"
// @Success 200 {object} schema.ChatsAdminCoordinator "The coordinator, running"
// @Failure 404 {object} schema.ErrorResponse "No such coordinator, or it is another user's"
// @Router /api/chats/coordinators/{id}/resume [post]
func (h *Handlers) ResumeCoordinator(c echo.Context) error {
	return h.setPaused(c, false)
}

func (h *Handlers) setPaused(c echo.Context, paused bool) error {
	host, ok := h.coordinators(c)
	if !ok {
		return nil
	}
	var (
		v   studiosvc.CoordinatorView
		err error
	)
	if paused {
		v, err = host.Pause(c.Request().Context(), actorOf(c), c.Param("id"))
	} else {
		v, err = host.Resume(c.Request().Context(), actorOf(c), c.Param("id"))
	}
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, adminCoordinatorOut(v))
}

// auditQuery reads the filters of an audit request. When ok is false the
// refusal is written.
func auditQuery(c echo.Context) (q coordinator.AuditQuery, ok bool, err error) {
	q.Kind, q.Outcome = c.QueryParam("kind"), c.QueryParam("outcome")
	bad := func(msg string) (coordinator.AuditQuery, bool, error) {
		return q, false, fail(c, http.StatusBadRequest, typeInvalid, msg)
	}
	for _, f := range []struct {
		name string
		dst  *time.Time
	}{{"since", &q.Since}, {"until", &q.Until}} {
		if v := c.QueryParam(f.name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return bad(f.name + " must be a time in RFC 3339, like 2026-11-08T10:00:00Z")
			}
			*f.dst = t
		}
	}
	if v := c.QueryParam("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return bad("before must be the next_before of a previous page")
		}
		q.Before = n
	}
	if v := c.QueryParam("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return bad("limit must be a positive number")
		}
		q.Limit = n
	}
	return q, true, nil
}

func (h *Handlers) auditOut(entries []coordinator.AuditEntry, limit int) schema.ChatsCoordinatorAuditList {
	out := schema.ChatsCoordinatorAuditList{Entries: make([]schema.ChatsCoordinatorAuditEntry, 0, len(entries))}
	for _, e := range entries {
		out.Entries = append(out.Entries, schema.ChatsCoordinatorAuditEntry{ID: e.ID, At: e.At, CoordinatorID: e.CoordinatorID,
			Owner: e.Principal, Kind: e.Kind, Outcome: e.Outcome, Reason: e.Reason, Actor: e.Actor, Target: e.Target,
			Via: e.Via, Length: e.Length, Hash: e.Hash})
	}
	if limit <= 0 {
		limit = coordinator.DefaultAuditLimit
	}
	if limit > coordinator.MaxAuditLimit {
		limit = coordinator.MaxAuditLimit
	}
	if n := len(entries); n > 0 && n == limit {
		out.NextBefore = entries[n-1].ID
	}
	return out
}

// CoordinatorAudit reads the audit log of one coordinator.
// @Summary Read the audit log of one of your coordinators.
// @Description Every message it sent, every attempt that was denied or blocked, wakes, pauses and helper events, newest first. Rows never hold the text of a message, only its length and hash. Kept 90 days by default (an operator setting). An administrator may read any coordinator's log.
// @Tags chats
// @Param id path string true "the coordinator's agent id"
// @Param kind query string false "only this kind" Enums(contact, wake, pause, resume, helper)
// @Param outcome query string false "only this outcome" Enums(sent, denied, info)
// @Param since query string false "from this time on (RFC 3339, inclusive)"
// @Param until query string false "before this time (RFC 3339)"
// @Param before query int false "the next_before of the previous page"
// @Param limit query int false "page size, default 50, at most 500"
// @Success 200 {object} schema.ChatsCoordinatorAuditList "A page of the log"
// @Failure 400 {object} schema.ErrorResponse "A filter is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such coordinator, or it is another user's"
// @Router /api/chats/coordinators/{id}/audit [get]
func (h *Handlers) CoordinatorAudit(c echo.Context) error {
	host, ok := h.coordinators(c)
	if !ok {
		return nil
	}
	q, ok, err := auditQuery(c)
	if !ok {
		return err
	}
	by := actorOf(c)
	if err := host.Authorize(c.Request().Context(), by, c.Param("id")); err != nil {
		return failFor(c, err)
	}
	q.CoordinatorID = c.Param("id")
	entries, err := host.Audit(c.Request().Context(), by, q)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, h.auditOut(entries, q.Limit))
}

// AdminListCoordinators lists the coordinators of every user.
// @Summary List the coordinators of every user (administrators).
// @Description With the owner, the pause state, the next wake and what each used today. The instructions and contacts are not returned.
// @Tags chats
// @Success 200 {object} schema.ChatsAdminCoordinatorList "Every user's coordinators, by owner and name"
// @Failure 403 {object} schema.ErrorResponse "Not an administrator"
// @Router /api/chats/admin/coordinators [get]
func (h *Handlers) AdminListCoordinators(c echo.Context) error {
	host, ok := h.coordinators(c)
	if !ok {
		return nil
	}
	list, err := host.ListAll(c.Request().Context())
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsAdminCoordinatorList{Coordinators: make([]schema.ChatsAdminCoordinator, 0, len(list))}
	for _, v := range list {
		out.Coordinators = append(out.Coordinators, adminCoordinatorOut(v))
	}
	return c.JSON(http.StatusOK, out)
}

// AdminPauseCoordinator pauses the coordinator of any user.
// @Summary Pause the coordinator of any user (administrators).
// @Description Same as the owner's pause: all outbound stops, the run and helpers are cancelled, messages are held until resume.
// @Tags chats
// @Param id path string true "the coordinator's agent id"
// @Success 200 {object} schema.ChatsAdminCoordinator "The coordinator, paused"
// @Failure 403 {object} schema.ErrorResponse "Not an administrator"
// @Failure 404 {object} schema.ErrorResponse "No such coordinator"
// @Router /api/chats/admin/coordinators/{id}/pause [post]
func (h *Handlers) AdminPauseCoordinator(c echo.Context) error { return h.setPaused(c, true) }

// AdminResumeCoordinator resumes the coordinator of any user.
// @Summary Resume the coordinator of any user (administrators).
// @Tags chats
// @Param id path string true "the coordinator's agent id"
// @Success 200 {object} schema.ChatsAdminCoordinator "The coordinator, running"
// @Failure 403 {object} schema.ErrorResponse "Not an administrator"
// @Failure 404 {object} schema.ErrorResponse "No such coordinator"
// @Router /api/chats/admin/coordinators/{id}/resume [post]
func (h *Handlers) AdminResumeCoordinator(c echo.Context) error { return h.setPaused(c, false) }

// AdminCoordinatorAudit reads the audit log of every coordinator.
// @Summary Read the audit log of every coordinator (administrators).
// @Description Newest first, for all users, or for one coordinator with the coordinator filter. Rows never hold the text of a message.
// @Tags chats
// @Param coordinator query string false "only this coordinator's agent id"
// @Param kind query string false "only this kind" Enums(contact, wake, pause, resume, helper)
// @Param outcome query string false "only this outcome" Enums(sent, denied, info)
// @Param since query string false "from this time on (RFC 3339, inclusive)"
// @Param until query string false "before this time (RFC 3339)"
// @Param before query int false "the next_before of the previous page"
// @Param limit query int false "page size, default 50, at most 500"
// @Success 200 {object} schema.ChatsCoordinatorAuditList "A page of the log"
// @Failure 400 {object} schema.ErrorResponse "A filter is not valid"
// @Failure 403 {object} schema.ErrorResponse "Not an administrator"
// @Router /api/chats/admin/coordinators/audit [get]
func (h *Handlers) AdminCoordinatorAudit(c echo.Context) error {
	host, ok := h.coordinators(c)
	if !ok {
		return nil
	}
	q, ok, err := auditQuery(c)
	if !ok {
		return err
	}
	q.CoordinatorID = c.QueryParam("coordinator")
	entries, err := host.Audit(c.Request().Context(), studiosvc.CoordinatorActor{User: userID(c), Admin: true}, q)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, h.auditOut(entries, q.Limit))
}
