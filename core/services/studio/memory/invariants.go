// SPDX-License-Identifier: MIT
package memory

import "fmt"

func CheckInvariants(s State) error {
	j := jobOf(s)
	i := j.Identity
	if i.Principal == "" || i.Destination == "" || i.Agent == "" || i.Chat == "" || i.Source == "" || i.Kind == "" {
		return fmt.Errorf("incomplete immutable identity: %+v", i)
	}
	if j.Attempts < 0 || j.Attempts > 3 || j.Generation < j.Attempts || j.Expires <= 0 {
		return fmt.Errorf("invalid bounded attempt/fence/lifetime: %+v", j)
	}
	if p, ok := j.Input.(FrozenPayload); ok && (p.Text == "" || p.Hash != digest(p.Text)) {
		return fmt.Errorf("invalid frozen payload")
	}
	if w, ok := workOf(s); ok {
		if w.Fence.Key != i.Key() || w.Fence.Owner == "" || w.Fence.Generation != j.Generation || j.Attempts == 0 || w.Fence.Operation == "" || w.Deadline <= w.Fence.Now {
			return fmt.Errorf("invalid active owner/operation: %+v", w)
		}
	}
	switch v := s.(type) {
	case Writing:
		if _, ok := v.Work.Job.Input.(FrozenPayload); !ok {
			return fmt.Errorf("write without durable frozen payload")
		}
	case PersistingPayload:
		if v.Payload.Hash != digest(v.Payload.Text) || v.Payload.Text == "" {
			return fmt.Errorf("invalid pending payload")
		}
	case Saved:
		if v.EntryID == "" {
			return fmt.Errorf("saved without exact entry ID")
		}
	case Queued, Authorizing, Transforming, RetryWaiting, Failed, Skipped:
	}
	return nil
}

func SampleStates() []State {
	j := jobOf(Initial("source-a", ContentInput{Text: "hello"}))
	j.Attempts = 1
	j.Generation = 1
	w := Work{j, Envelope{j.Identity.Key(), "frontend-a", 1, "authorize", 0}, 60}
	p := FrozenPayload{"hello", digest("hello")}
	f := w
	f.Job.Input = p
	return []State{Queued{j}, Authorizing{w}, Transforming{nextWork(w, "summary", 0)}, PersistingPayload{nextWork(w, "payload", 0), p}, Writing{nextWork(f, "write", 0)}, RetryWaiting{j, 1}, Saved{f.Job, "entry"}, Failed{f.Job, "failed"}, Skipped{j, "revoked"}}
}
func SampleEvents() []Event {
	e := sampleEnvelope()
	return []Event{ClaimGranted{e}, ClaimDenied{e}, AuthorizationGranted{e}, AuthorizationDenied{e}, SummarySucceeded{e, "summary"}, SummaryFailed{e}, PayloadPersisted{e}, PayloadPersistFailed{e}, WriteSucceeded{e, "entry"}, WriteFailed{e}, Timeout{e}, RetryTick{e}, Revoked{e}, DuplicateDelivered{e}, RestartRecovered{e}, AuthorizedRetry{e}, WriteDeleted{e}, EntryDeleted{e}}
}
func enabledEvents(s State) []Event {
	env := envelopeOfState(s)
	j := jobOf(s)
	events := []Event{Revoked{env}, DuplicateDelivered{env}}
	for _, owner := range []string{"frontend-a", "frontend-b"} {
		claim := env
		claim.Owner = owner
		claim.Generation = j.Generation + 1
		switch v := s.(type) {
		case Queued:
			events = append(events, ClaimGranted{claim}, ClaimDenied{claim})
			end := env
			end.Now = j.Expires
			events = append(events, Timeout{end})
		case RetryWaiting:
			tick := env
			tick.Now = v.Due
			end := env
			end.Now = j.Expires
			events = append(events, RetryTick{tick}, Timeout{end})
		case Authorizing:
			events = append(events, AuthorizationGranted{env}, AuthorizationDenied{env})
		case Transforming:
			events = append(events, SummarySucceeded{env, "summary"}, SummarySucceeded{env, ""}, SummaryFailed{env})
		case PersistingPayload:
			events = append(events, PayloadPersisted{env}, PayloadPersistFailed{env})
		case Writing:
			events = append(events, WriteSucceeded{env, "entry"}, WriteDeleted{env}, WriteFailed{env})
		case Saved, Failed, Skipped:
		}
		if w, ok := workOf(s); ok {
			end := env
			end.Now = w.Deadline
			events = append(events, Timeout{end})
			claim.Now = w.Deadline
			events = append(events, RestartRecovered{claim})
		}
	}
	return events
}
