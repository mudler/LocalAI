package studio

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

func (h *Handlers) triggerHost(c echo.Context) (*studiosvc.Triggers, bool) {
	return feature(c, h.svc.Triggers(), func(c echo.Context) error { return failTrigger(c, studiosvc.ErrTriggersOff) })
}

func utcPtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func runOut(host *studiosvc.Triggers, user string, r triggers.Run) schema.ChatsTriggerRun {
	file := r.File
	if file != "" {
		file = host.RelPath(user, file)
	}
	return schema.ChatsTriggerRun{ID: r.ID, Status: string(r.Status), Reason: r.Reason, ChatID: r.ChatID, File: file,
		Due: r.Due.UTC(), Started: utcPtr(r.Started), Ended: utcPtr(r.Ended)}
}

func triggerOut(ctx context.Context, host *studiosvc.Triggers, t triggers.Trigger, withLast bool) schema.ChatsTrigger {
	out := schema.ChatsTrigger{ID: t.ID, Name: t.Name, Prompt: t.Prompt, Kind: string(t.Kind), Schedule: t.Schedule, TimeZone: t.TZ,
		Folder: host.RelFolder(t), QuietSeconds: int(t.Quiet / time.Second), DailyCap: t.CapOrDefault(), Enabled: t.Enabled,
		AgentID: t.AgentID, Model: t.Model, SourceChatID: t.SourceChatID, Summary: host.Summary(t)}
	if next, ok := host.NextRun(t); ok {
		out.NextRun = utcPtr(next)
	}
	if withLast {
		if runs, err := host.TriggerRuns(ctx, t.Owner, t.ID, 1); err == nil && len(runs) == 1 {
			r := runOut(host, t.Owner, runs[0])
			out.LastRun = &r
		}
	}
	return out
}

func triggerIn(req schema.ChatsTriggerWrite) studiosvc.TriggerInput {
	return studiosvc.TriggerInput{Name: req.Name, Prompt: req.Prompt, Kind: triggers.Kind(req.Kind), Schedule: req.Schedule, TZ: req.TimeZone,
		Folder: req.Folder, Quiet: time.Duration(req.QuietSeconds) * time.Second, DailyCap: req.DailyCap,
		AgentID: req.AgentID, Model: req.Model, SourceChatID: req.SourceChatID, Enabled: req.Enabled}
}

var triggerRules = []errRule{
	fixed(http.StatusNotFound, typeNotFound, "trigger not found", triggers.ErrNotFound),
	verbatim(http.StatusBadRequest, typeInvalid, studiosvc.ErrBadTrigger.Error()+": ", studiosvc.ErrBadTrigger),
	verbatim(http.StatusConflict, typeConflict, "", studiosvc.ErrTooManyTriggers, studiosvc.ErrTooManyProposals),
	verbatim(http.StatusConflict, typeTriggersOff, "", studiosvc.ErrTriggersOff),
	fixed(http.StatusServiceUnavailable, typeServerError, "the server is shutting down", studiosvc.ErrTriggersClosed),
}

// failTrigger maps the errors of the trigger service.
func failTrigger(c echo.Context, err error) error { return mapErr(c, err, triggerRules, failFor) }

// ListTriggers lists the triggers.
// @Summary List your triggers.
// @Description A trigger starts a chat as you, by itself, on a schedule or when a file arrives in a folder of your files. Each run is an ordinary chat, marked with its trigger, that you can open and undo.
// @Tags chats
// @Success 200 {object} schema.ChatsTriggerList "Your triggers, sorted by name"
// @Router /api/chats/triggers [get]
func (h *Handlers) ListTriggers(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	list, err := host.ListTriggers(c.Request().Context(), userID(c))
	if err != nil {
		return failTrigger(c, err)
	}
	out := schema.ChatsTriggerList{Triggers: make([]schema.ChatsTrigger, 0, len(list))}
	for _, t := range list {
		out.Triggers = append(out.Triggers, triggerOut(c.Request().Context(), host, t, true))
	}
	return c.JSON(http.StatusOK, out)
}

// CreateTrigger creates a trigger.
// @Summary Create a trigger.
// @Description kind is schedule (schedule: "daily 08:00", "weekdays 09:30", "weekly mon,thu 09:30", "monthly 15 08:00", "once 2026-12-24 18:00" or five cron fields; time_zone: an IANA name) or file (folder: a path inside your files). It runs nothing for the time before it was saved. At most 50 triggers.
// @Tags chats
// @Param request body schema.ChatsTriggerWrite true "the trigger"
// @Success 201 {object} schema.ChatsTrigger "The trigger"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/triggers [post]
func (h *Handlers) CreateTrigger(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	var req schema.ChatsTriggerWrite
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "the body is not valid JSON")
	}
	t, err := host.CreateTrigger(c.Request().Context(), userID(c), triggerIn(req))
	if err != nil {
		return failTrigger(c, err)
	}
	return c.JSON(http.StatusCreated, triggerOut(c.Request().Context(), host, t, false))
}

// GetTrigger reads a trigger.
// @Summary Read a trigger.
// @Tags chats
// @Param id path string true "the trigger's id"
// @Success 200 {object} schema.ChatsTrigger "The trigger"
// @Router /api/chats/triggers/{id} [get]
func (h *Handlers) GetTrigger(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	t, err := host.GetTrigger(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failTrigger(c, err)
	}
	return c.JSON(http.StatusOK, triggerOut(c.Request().Context(), host, t, true))
}

// UpdateTrigger replaces a trigger.
// @Summary Replace a trigger.
// @Description The body replaces every field. A change of when it runs, or switching it on, means it runs nothing for the time before the change.
// @Tags chats
// @Param id path string true "the trigger's id"
// @Param request body schema.ChatsTriggerWrite true "the trigger"
// @Success 200 {object} schema.ChatsTrigger "The trigger"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/triggers/{id} [put]
func (h *Handlers) UpdateTrigger(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	var req schema.ChatsTriggerWrite
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "the body is not valid JSON")
	}
	t, err := host.UpdateTrigger(c.Request().Context(), userID(c), c.Param("id"), triggerIn(req))
	if err != nil {
		return failTrigger(c, err)
	}
	return c.JSON(http.StatusOK, triggerOut(c.Request().Context(), host, t, true))
}

// DeleteTrigger deletes a trigger.
// @Summary Delete a trigger.
// @Description Its queued runs are dropped and a running run is stopped. The chats it made stay.
// @Tags chats
// @Param id path string true "the trigger's id"
// @Success 204 "Deleted"
// @Router /api/chats/triggers/{id} [delete]
func (h *Handlers) DeleteTrigger(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	if err := host.DeleteTrigger(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return failTrigger(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handlers) setEnabled(c echo.Context, on bool) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	t, err := host.SetTriggerEnabled(c.Request().Context(), userID(c), c.Param("id"), on)
	if err != nil {
		return failTrigger(c, err)
	}
	return c.JSON(http.StatusOK, triggerOut(c.Request().Context(), host, t, true))
}

// EnableTrigger switches a trigger on.
// @Summary Switch a trigger on.
// @Tags chats
// @Param id path string true "the trigger's id"
// @Success 200 {object} schema.ChatsTrigger "The trigger"
// @Router /api/chats/triggers/{id}/enable [post]
func (h *Handlers) EnableTrigger(c echo.Context) error { return h.setEnabled(c, true) }

// DisableTrigger switches a trigger off.
// @Summary Switch a trigger off.
// @Tags chats
// @Param id path string true "the trigger's id"
// @Success 200 {object} schema.ChatsTrigger "The trigger"
// @Router /api/chats/triggers/{id}/disable [post]
func (h *Handlers) DisableTrigger(c echo.Context) error { return h.setEnabled(c, false) }

// RunTrigger starts a run now.
// @Summary Run a trigger now.
// @Description Starts a run at once, whether the trigger is on or not. The run is skipped, with its reason, when the trigger has a run going. On a cluster the run may start a few seconds later.
// @Tags chats
// @Param id path string true "the trigger's id"
// @Success 202 {object} schema.ChatsTriggerRun "The run"
// @Router /api/chats/triggers/{id}/run [post]
func (h *Handlers) RunTrigger(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	run, err := host.RunTrigger(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failTrigger(c, err)
	}
	return c.JSON(http.StatusAccepted, runOut(host, userID(c), run))
}

// TriggerRuns lists the runs of a trigger.
// @Summary List the runs of a trigger.
// @Tags chats
// @Param id path string true "the trigger's id"
// @Param limit query int false "most runs to return (default 20, at most 200)"
// @Success 200 {object} schema.ChatsTriggerRunList "Newest first"
// @Router /api/chats/triggers/{id}/runs [get]
func (h *Handlers) TriggerRuns(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	runs, err := host.TriggerRuns(c.Request().Context(), userID(c), c.Param("id"), intParam(c, "limit", 20, 200))
	if err != nil {
		return failTrigger(c, err)
	}
	out := schema.ChatsTriggerRunList{Runs: make([]schema.ChatsTriggerRun, 0, len(runs))}
	for _, r := range runs {
		out.Runs = append(out.Runs, runOut(host, userID(c), r))
	}
	return c.JSON(http.StatusOK, out)
}

// PreviewTrigger shows the next times of a schedule.
// @Summary Preview a schedule.
// @Description Returns the next times a schedule text runs in a time zone, and the schedule in plain words. A bad text is a 400 with the reason.
// @Tags chats
// @Param when query string true "the schedule text"
// @Param tz query string false "an IANA time zone name (default UTC)"
// @Param n query int false "how many times (default 3, at most 10)"
// @Success 200 {object} schema.ChatsTriggerPreview "The next times"
// @Router /api/chats/triggers/preview [get]
func (h *Handlers) PreviewTrigger(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	times, err := host.PreviewTrigger(c.QueryParam("when"), c.QueryParam("tz"), intParam(c, "n", 3, 10))
	if err != nil {
		return failTrigger(c, err)
	}
	sch, _ := triggers.ParseSchedule(c.QueryParam("when"))
	out := schema.ChatsTriggerPreview{Times: make([]time.Time, len(times)), Summary: sch.String()}
	for i, t := range times {
		out.Times[i] = t.UTC()
	}
	return c.JSON(http.StatusOK, out)
}

// ListTriggerProposals lists the proposals that wait for an answer.
// @Summary List trigger proposals.
// @Description A model in a chat can propose a trigger. A proposal is not active and runs nothing; confirm it to turn it into a trigger that is on. The chat shows it as a tool line with a card of kind trigger_proposal whose id is the proposal's id, and a trigger_proposed event announces it.
// @Tags chats
// @Param chat query string false "only the proposals made in this chat"
// @Success 200 {object} schema.ChatsTriggerList "The proposals"
// @Router /api/chats/triggers/proposals [get]
func (h *Handlers) ListTriggerProposals(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	list, err := host.Proposals(c.Request().Context(), userID(c))
	if err != nil {
		return failTrigger(c, err)
	}
	out := schema.ChatsTriggerList{Triggers: make([]schema.ChatsTrigger, 0, len(list))}
	chat := c.QueryParam("chat")
	for _, t := range list {
		if chat != "" && t.SourceChatID != chat {
			continue
		}
		out.Triggers = append(out.Triggers, triggerOut(c.Request().Context(), host, t, false))
	}
	return c.JSON(http.StatusOK, out)
}

// ConfirmTriggerProposal confirms a proposal.
// @Summary Confirm a trigger proposal.
// @Description Turns the proposal into a trigger that is on. time_zone is used when the proposal names none.
// @Tags chats
// @Param id path string true "the proposal's id"
// @Param request body schema.ChatsTriggerConfirm false "the time zone of the person confirming"
// @Success 200 {object} schema.ChatsTrigger "The trigger"
// @Router /api/chats/triggers/proposals/{id}/confirm [post]
func (h *Handlers) ConfirmTriggerProposal(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	var req schema.ChatsTriggerConfirm
	_ = c.Bind(&req)
	t, err := host.ConfirmProposal(c.Request().Context(), userID(c), c.Param("id"), req.TimeZone)
	if err != nil {
		return failTrigger(c, err)
	}
	return c.JSON(http.StatusOK, triggerOut(c.Request().Context(), host, t, true))
}

// EditTriggerProposal changes a proposal.
// @Summary Edit a trigger proposal.
// @Description The body replaces every field of the proposal, as for a trigger. The proposal stays a proposal: it is not active until you confirm it.
// @Tags chats
// @Param id path string true "the proposal's id"
// @Param request body schema.ChatsTriggerWrite true "the new fields"
// @Success 200 {object} schema.ChatsTrigger "The proposal"
// @Router /api/chats/triggers/proposals/{id} [put]
func (h *Handlers) EditTriggerProposal(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	var req schema.ChatsTriggerWrite
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "the body is not valid JSON")
	}
	t, err := host.EditProposal(c.Request().Context(), userID(c), c.Param("id"), triggerIn(req))
	if err != nil {
		return failTrigger(c, err)
	}
	return c.JSON(http.StatusOK, triggerOut(c.Request().Context(), host, t, true))
}

// DismissTriggerProposal deletes a proposal.
// @Summary Dismiss a trigger proposal.
// @Tags chats
// @Param id path string true "the proposal's id"
// @Success 204 "Dismissed"
// @Router /api/chats/triggers/proposals/{id} [delete]
func (h *Handlers) DismissTriggerProposal(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	if err := host.DismissProposal(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return failTrigger(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
