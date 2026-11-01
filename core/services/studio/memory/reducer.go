// SPDX-License-Identifier: MIT
package memory

import "strings"

func result(s State, id string, effects ...Effect) Transition { return Transition{s, effects, id, ""} }
func ignore(s State, id, reason string) Transition            { return Transition{s, nil, id, reason} }
func finish(s State, id string) Transition {
	return result(s, id, PublishStatus{jobOf(s).Identity.Key(), stateName(s)})
}
func retry(j Job, now int64) Transition {
	if j.Attempts >= 3 || now >= j.Expires {
		return finish(Failed{j, "attempts exhausted or expired"}, "R06-exhausted")
	}
	delays := []int64{1, 5, 30}
	due := now + delays[j.Attempts-1]
	return result(RetryWaiting{j, due}, "R06-wait", ScheduleRetry{j.Identity.Key(), due}, PublishStatus{j.Identity.Key(), "RetryWaiting"})
}

// Reduce never executes effects. Granted events are trusted attestations from a
// fenced shell, not credentials. Each mutation consumes a fresh authorization.
func Reduce(s State, e Event) Transition {
	j := jobOf(s)
	env := eventEnvelope(e)
	if env.Key != j.Identity.Key() {
		return ignore(s, "R02-stale", "different capture key")
	}
	if _, ok := e.(DuplicateDelivered); ok {
		return ignore(s, "R01-duplicate", "accepted identity already exists")
	}
	if terminal(s) {
		if f, ok := s.(Failed); ok {
			if _, ok := e.(AuthorizedRetry); ok && env.Generation > j.Generation && env.Owner != "" {
				j = f.Job
				j.Attempts = 0
				j.Expires = env.Now + 10000
				return claim(j, env, "R08-retry")
			}
		}
		return ignore(s, "R07-absorbed", "terminal records and deletion tombstones never recreate entries")
	}
	if _, ok := e.(Revoked); ok {
		return finish(Skipped{j, "current policy revoked; no destination fallback"}, "R03-revoked")
	}
	// Capture lifetime dominates renewable phase deadlines and retry due times.
	// Keep key/dedup/terminal/policy handling above this global expiry guard.
	if env.Now >= j.Expires {
		return finish(Failed{j, "capture expired"}, "R06-exhausted")
	}
	if _, ok := e.(RestartRecovered); ok {
		if w, active := workOf(s); active && env.Now < w.Deadline {
			return ignore(s, "R02-live", "claim still live")
		}
		if env.Generation <= j.Generation || env.Owner == "" {
			return ignore(s, "R02-stale", "recovery requires a newer fence")
		}
		if waiting, ok := s.(RetryWaiting); ok && env.Now < waiting.Due {
			return ignore(s, "R06-early", "recovery arrived before retry due time")
		}
		if j.Attempts >= 3 {
			return finish(Failed{j, "recovery exhausted"}, "R06-exhausted")
		}
		return claim(j, env, "R06-recover")
	}
	if w, active := workOf(s); active {
		if env.Owner != w.Fence.Owner || env.Generation != w.Fence.Generation || env.Operation != w.Fence.Operation {
			return ignore(s, "R02-stale", "owner, generation or operation mismatch")
		}
		if _, ok := e.(Timeout); ok {
			if env.Now < w.Deadline {
				return ignore(s, "R06-early", "deadline not reached")
			}
			return retry(j, env.Now)
		}
		if env.Now >= w.Deadline {
			return ignore(s, "R02-expired", "result arrived after operation deadline; await timeout or recovery")
		}
	}
	switch v := s.(type) {
	case Queued:
		switch e.(type) {
		case ClaimGranted:
			if env.Generation > j.Generation && env.Owner != "" {
				return claim(j, env, "R02-claim")
			}
		case ClaimDenied:
			return ignore(s, "R02-denied", "another frontend owns the claim")
		}
	case Authorizing:
		switch e.(type) {
		case AuthorizationDenied:
			return finish(Skipped{j, "current authorization denied"}, "R03-denied")
		case AuthorizationGranted:
			switch input := j.Input.(type) {
			case SummaryInput:
				if strings.TrimSpace(input.Text) == "" {
					return finish(Failed{j, "empty input"}, "R04-empty")
				}
				w := nextWork(v.Work, "summary", env.Now)
				return result(Transforming{w}, "R03-transform", Summarize{w})
			case ContentInput:
				p := FrozenPayload{input.Text, digest(input.Text)}
				if strings.TrimSpace(p.Text) == "" {
					return finish(Failed{j, "empty input"}, "R04-empty")
				}
				w := nextWork(v.Work, "payload", env.Now)
				w.Job.Input = PendingPayload{p}
				return result(PersistingPayload{w, p}, "R04-content", PersistPayload{w, p})
			case PendingPayload:
				w := nextWork(v.Work, "payload", env.Now)
				return result(PersistingPayload{w, input.Payload}, "R04-resume", PersistPayload{w, input.Payload})
			case FrozenPayload:
				w := nextWork(v.Work, "write", env.Now)
				return result(Writing{w}, "R05-write", ReconcileOrWrite{w})
			}
		}
	case Transforming:
		switch ev := e.(type) {
		case SummarySucceeded:
			if strings.TrimSpace(ev.Text) == "" {
				return finish(Failed{j, "empty summary"}, "R04-empty")
			}
			w := nextWork(v.Work, "payload", env.Now)
			p := FrozenPayload{ev.Text, digest(ev.Text)}
			w.Job.Input = PendingPayload{p}
			return result(PersistingPayload{w, p}, "R04-summary", PersistPayload{w, p})
		case SummaryFailed:
			return finish(Failed{j, "summary failed; no content fallback"}, "R04-failed")
		}
	case PersistingPayload:
		switch e.(type) {
		case PayloadPersisted:
			w := v.Work
			w.Job.Input = v.Payload
			w = nextWork(w, "authorize", env.Now)
			return result(Authorizing{w}, "R04-durable", CheckAuthorization{w})
		case PayloadPersistFailed:
			return retry(j, env.Now)
		}
	case Writing:
		switch ev := e.(type) {
		case WriteSucceeded:
			if ev.EntryID == "" {
				return finish(Failed{j, "missing exact entry identifier"}, "R05-invalid")
			}
			return finish(Saved{j, ev.EntryID}, "R05-saved")
		case WriteDeleted:
			return finish(Skipped{j, "backend tombstone; do not recreate"}, "R07-deleted")
		case WriteFailed:
			return retry(j, env.Now)
		}
	case RetryWaiting:
		switch e.(type) {
		case RetryTick:
			if env.Now >= v.Due {
				return result(Queued{j}, "R06-ready", PublishStatus{j.Identity.Key(), "Queued"})
			}
		}
	case Saved, Failed, Skipped: // Terminal handling is above, before any recovery.
	}
	return ignore(s, "R02-unexpected", "event is not enabled in "+stateName(s))
}
func claim(j Job, env Envelope, id string) Transition {
	if env.Now >= j.Expires || j.Attempts >= 3 {
		return finish(Failed{j, "capture expired or exhausted"}, "R06-exhausted")
	}
	j.Attempts++
	j.Generation = env.Generation
	env.Operation = "authorize"
	w := Work{j, env, env.Now + 60}
	return result(Authorizing{w}, id, CheckAuthorization{w}, PublishStatus{j.Identity.Key(), "Authorizing"})
}
