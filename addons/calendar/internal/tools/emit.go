// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/addons/calendar/internal/store"
)

// The event types of the calendar. The manifest declares them under
// capabilities.events, with the paths of the payload below.
const (
	EventCreated = "calendar.event.created"
	EventUpdated = "calendar.event.updated"
	EventDeleted = "calendar.event.deleted"
)

// Emitter reports a change of the calendar to the host (the events.emit host
// operation) for the live call operationID. The host decides who made the
// change; the calendar only says what changed.
type Emitter interface {
	Emit(ctx context.Context, operationID, typ string, payload any, dedupe string) error
}

// eventPayload is what the host and the agents read of a changed event. next
// is the start of the next occurrence that has not begun (RFC 3339): the time
// a reminder is for. It is absent when no occurrence is left.
type eventPayload struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Next  string `json:"next,omitempty"`
	// Following are the starts of the occurrences after next, for a
	// repeating event: the host moves a reminder on to them by itself.
	Following []string      `json:"following,omitempty"`
	Start     string        `json:"start,omitempty"`
	End       string        `json:"end,omitempty"`
	AllDay    bool          `json:"allDay"`
	Zone      string        `json:"zone,omitempty"`
	Place     string        `json:"place,omitempty"`
	Attendees string        `json:"attendees,omitempty"`
	Notes     string        `json:"notes,omitempty"`
	Repeat    *store.Repeat `json:"repeat,omitempty"`
}

// followingCount is how many occurrences after the next one an event
// carries: the most the host reads.
const followingCount = 64

// emit reports one change. A failure is logged and never fails the change:
// the calendar is the owner's data, and a watch that misses one change still
// has the next.
func (r *Runtime) emit(ctx context.Context, operationID, owner, typ string, e store.Event) {
	if r.Events == nil || operationID == "" {
		return
	}
	p := eventPayload{ID: e.ID, Title: e.Title}
	if typ != EventDeleted {
		p.Start, p.End, p.AllDay, p.Zone, p.Place, p.Attendees, p.Notes, p.Repeat = e.Start, e.End, e.AllDay, e.Zone, e.Place, e.Attendees, e.Notes, e.Repeat
		starts, err := r.Store.NextStarts(ctx, owner, e, followingCount+1)
		if err == nil && len(starts) > 0 {
			p.Next = starts[0].Format(time.RFC3339)
			for _, t := range starts[1:] {
				p.Following = append(p.Following, t.Format(time.RFC3339))
			}
		}
	}
	dedupe := e.ID + "@" + typ + "@" + e.Updated + "@" + p.Next
	if err := r.Events.Emit(ctx, operationID, typ, map[string]any{"event": p}, dedupe); err != nil {
		xlog.Debug("calendar: the change was not reported", "type", typ, "error", err)
	}
}
