package studio

import (
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// watchRuleOut writes the rule of a watch.
func watchRuleOut(w *triggers.WatchRule) *schema.ChatsWatchRule {
	if w == nil {
		return nil
	}
	return &schema.ChatsWatchRule{OffsetMinutes: int(w.Offset / time.Minute), NoDefault: w.NoDefault, AllDay: w.AllDay,
		QuietFrom: w.QuietFrom, QuietTo: w.QuietTo, Tasks: w.Tasks, Types: w.Types}
}

// watchRuleIn reads the rule of a watch.
func watchRuleIn(w *schema.ChatsWatchRule) *triggers.WatchRule {
	if w == nil {
		return nil
	}
	return &triggers.WatchRule{Offset: time.Duration(w.OffsetMinutes) * time.Minute, NoDefault: w.NoDefault, AllDay: w.AllDay,
		QuietFrom: w.QuietFrom, QuietTo: w.QuietTo, Tasks: w.Tasks, Types: w.Types}
}

// requestLanguage is the first language of the request, for the labels the
// add-ons translate.
func requestLanguage(c echo.Context) string {
	if l := c.QueryParam("lang"); l != "" {
		return l
	}
	tag, _, _ := strings.Cut(c.Request().Header.Get("Accept-Language"), ",")
	tag, _, _ = strings.Cut(tag, ";")
	return strings.TrimSpace(tag)
}

// WatchSources lists the add-ons whose events a watch can take.
// @Summary List the add-ons a watch can watch.
// @Description An add-on that reports changes (a calendar, say) declares its event types. A watch takes one of these sources and keeps reminders for its items. The list is empty when no installed add-on reports changes. lang (or Accept-Language) chooses the language of the labels.
// @Tags chats
// @Param lang query string false "language of the labels"
// @Success 200 {object} schema.ChatsWatchSources "The sources"
// @Router /api/chats/triggers/sources [get]
func (h *Handlers) WatchSources(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	srcs, err := host.WatchSources(c.Request().Context())
	if err != nil {
		return failTrigger(c, err)
	}
	lang := requestLanguage(c)
	out := schema.ChatsWatchSources{Sources: make([]schema.ChatsWatchSource, 0, len(srcs))}
	for _, s := range srcs {
		row := schema.ChatsWatchSource{ID: s.ID, Title: s.Title.For(lang), Events: make([]schema.ChatsWatchEventType, 0, len(s.Events))}
		for _, d := range s.Events {
			row.Events = append(row.Events, schema.ChatsWatchEventType{Type: d.Type, Lifecycle: d.Lifecycle, Label: d.Label.For(lang)})
		}
		out.Sources = append(out.Sources, row)
	}
	return c.JSON(http.StatusOK, out)
}

func reminderOut(v studiosvc.ReminderView) schema.ChatsReminder {
	return schema.ChatsReminder{ID: v.ID, Title: v.Title, Link: v.Link, Task: v.Task, Due: utcPtr(v.Due), Start: utcPtr(v.Start),
		ItemTime: utcPtr(v.Anchor), AllDay: v.AllDay, OffsetMinutes: int(v.Offset / time.Minute), LeadMinutes: int(v.Lead / time.Minute),
		At: utcPtr(v.At), SnoozedTo: utcPtr(v.SnoozedTo), Skipped: v.Skipped, Adjusted: v.Adjusted, Snoozable: v.Snoozable}
}

// WatchReminders lists the reminders of a watch.
// @Summary List the reminders of a watch.
// @Description Soonest first. A reminder whose item has begun has no due time and comes last. 400 when the trigger is not a watch.
// @Tags chats
// @Param id path string true "the watch's id"
// @Success 200 {object} schema.ChatsReminderList "The reminders"
// @Failure 404 {object} schema.ErrorResponse "No such watch"
// @Router /api/chats/triggers/{id}/reminders [get]
func (h *Handlers) WatchReminders(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	list, err := host.Reminders(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failTrigger(c, err)
	}
	out := schema.ChatsReminderList{Reminders: make([]schema.ChatsReminder, 0, len(list))}
	for _, v := range list {
		out.Reminders = append(out.Reminders, reminderOut(v))
	}
	return c.JSON(http.StatusOK, out)
}

// PatchWatchReminder changes one reminder of a watch.
// @Summary Snooze, skip, re-time or change the task of a reminder.
// @Description Each field that is set applies. snooze_minutes moves the reminder to now plus that much (at most a day); snoozed_to sets a time or, empty, takes the snooze back; skip true skips the coming time and false takes the skip back; at (2026-11-26 14:45 in the watch's time zone, or RFC 3339) or offset_minutes re-time it; task changes what the run does. The page undoes a change by sending the old values back.
// @Tags chats
// @Param id path string true "the watch's id"
// @Param rid path string true "the reminder's id"
// @Param source query string false "the add-on whose page asks; a reminder of a watch on another add-on is not found"
// @Param request body schema.ChatsReminderPatch true "the change"
// @Success 200 {object} schema.ChatsReminder "The reminder after the change"
// @Failure 400 {object} schema.ErrorResponse "The change is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such watch or reminder"
// @Router /api/chats/triggers/{id}/reminders/{rid} [patch]
func (h *Handlers) PatchWatchReminder(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	var req schema.ChatsReminderPatch
	if err := c.Bind(&req); err != nil {
		return failTrigger(c, studiosvc.ErrBadTrigger)
	}
	p := studiosvc.ReminderPatch{SnoozeFor: time.Duration(req.SnoozeMinutes) * time.Minute, SnoozedTo: req.SnoozedTo, Skip: req.Skip, At: req.At, Task: req.Task,
		Source: c.QueryParam("source")}
	if req.OffsetMinutes != nil {
		off := time.Duration(*req.OffsetMinutes) * time.Minute
		p.Offset = &off
	}
	v, err := host.PatchReminder(c.Request().Context(), userID(c), c.Param("id"), c.Param("rid"), p)
	if err != nil {
		return failTrigger(c, err)
	}
	return c.JSON(http.StatusOK, reminderOut(v))
}

// DeleteWatchReminder cancels one reminder of a watch.
// @Summary Cancel a reminder of a watch.
// @Tags chats
// @Param id path string true "the watch's id"
// @Param rid path string true "the reminder's id"
// @Success 204 "Cancelled"
// @Failure 404 {object} schema.ErrorResponse "No such watch or reminder"
// @Router /api/chats/triggers/{id}/reminders/{rid} [delete]
func (h *Handlers) DeleteWatchReminder(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	if err := host.DeleteReminder(c.Request().Context(), userID(c), c.Param("id"), c.Param("rid")); err != nil {
		return failTrigger(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// SourceReminders lists the reminders of your watches for the items of one
// add-on.
// @Summary List the reminders for the items of an add-on.
// @Description The page of an add-on marks its items that have a reminder (the bell of the calendar). source is the add-on ID; it is required.
// @Tags chats
// @Param source query string true "the add-on ID"
// @Success 200 {object} schema.ChatsSourceReminders "The reminders"
// @Router /api/chats/triggers/reminders [get]
func (h *Handlers) SourceReminders(c echo.Context) error {
	host, ok := h.triggerHost(c)
	if !ok {
		return nil
	}
	list, err := host.SourceReminders(c.Request().Context(), userID(c), c.QueryParam("source"))
	if err != nil {
		return failTrigger(c, err)
	}
	out := schema.ChatsSourceReminders{Reminders: make([]schema.ChatsSourceReminder, 0, len(list))}
	for _, r := range list {
		out.Reminders = append(out.Reminders, schema.ChatsSourceReminder{ItemID: r.ItemID, WatchID: r.WatchID, ChatsReminder: reminderOut(r.View)})
	}
	return c.JSON(http.StatusOK, out)
}
