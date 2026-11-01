// SPDX-License-Identifier: MIT
package memory

import "strings"

type Trace struct {
	Name    string
	Initial State
	Events  []Event
	Rules   []string
	Effects [][]string
	Final   string
}

func RunTrace(initial State, events []Event) []Transition {
	out := make([]Transition, 0, len(events))
	s := initial
	for _, e := range events {
		tr := Reduce(s, e)
		out = append(out, tr)
		s = tr.State
	}
	return out
}

// The rule/effect oracle is independent of reducer output. Event envelopes use
// replay state to target the operation, with explicit stale-delivery actions.
func ExampleTraces() []Trace {
	content := "claim auth persist auth write"
	summary := "claim auth summary persist auth write"
	specs := []struct {
		name                  string
		input                 Input
		actions, rules, final string
	}{
		{"content happy path", ContentInput{"hello"}, content, "R02-claim R04-content R04-durable R05-write R05-saved", "Saved"},
		{"summary happy path", SummaryInput{"hello"}, summary, "R02-claim R03-transform R04-summary R04-durable R05-write R05-saved", "Saved"},
		{"summary failure", SummaryInput{"hello"}, "claim auth summary-fail", "R02-claim R03-transform R04-failed", "Failed"},
		{"empty summary", SummaryInput{"hello"}, "claim auth empty", "R02-claim R03-transform R04-empty", "Failed"},
		{"transient write retry", ContentInput{"hello"}, "claim auth persist auth fail tick claim auth write", "R02-claim R04-content R04-durable R05-write R06-wait R06-ready R02-claim R05-write R05-saved", "Saved"},
		{"restart respects retry backoff", ContentInput{"hello"}, "claim auth persist auth fail early-recover recover auth write", "R02-claim R04-content R04-durable R05-write R06-wait R06-early R06-recover R05-write R05-saved", "Saved"},
		{"timeout exhaustion", ContentInput{"hello"}, "claim timeout tick claim timeout tick claim timeout", "R02-claim R06-wait R06-ready R02-claim R06-wait R06-ready R02-claim R06-exhausted", "Failed"},
		{"two frontend claims and stale owner", ContentInput{"hello"}, "denied claim competitor timeout recover stale auth persist auth write", "R02-denied R02-claim R02-stale R06-wait R06-recover R02-stale R04-content R04-durable R05-write R05-saved", "Saved"},
		{"out of order results", SummaryInput{"hello"}, "early-write claim early-summary auth early-write summary persist auth write late-summary late-write", "R02-unexpected R02-claim R02-unexpected R03-transform R02-unexpected R04-summary R04-durable R05-write R05-saved R07-absorbed R07-absorbed", "Saved"},
		{"duplicate incoming acceptance", ContentInput{"hello"}, "duplicate claim duplicate auth persist auth write duplicate", "R01-duplicate R02-claim R01-duplicate R04-content R04-durable R05-write R05-saved R01-duplicate", "Saved"},
		{"crash after backend commit before queue success", ContentInput{"hello"}, "claim auth persist auth recover auth write", "R02-claim R04-content R04-durable R05-write R06-recover R05-write R05-saved", "Saved"},
		{"revocation during summary", SummaryInput{"hello"}, "claim auth revoke summary", "R02-claim R03-transform R03-revoked R07-absorbed", "Skipped"},
		{"revocation during queue wait", ContentInput{"hello"}, "revoke claim", "R03-revoked R07-absorbed", "Skipped"},
		{"destination edit without rerouting", ContentInput{"hello"}, "claim auth persist deny", "R02-claim R04-content R04-durable R03-denied", "Skipped"},
		{"deleted entry non recreation", ContentInput{"hello"}, "claim auth persist auth recover auth tombstone recover duplicate", "R02-claim R04-content R04-durable R05-write R06-recover R05-write R07-deleted R07-absorbed R01-duplicate", "Skipped"},
		{"authorized retry preserves key and payload", ContentInput{"hello"}, "claim auth persist auth timeout tick claim auth timeout tick claim auth timeout manual auth write", "R02-claim R04-content R04-durable R05-write R06-wait R06-ready R02-claim R05-write R06-wait R06-ready R02-claim R05-write R06-exhausted R08-retry R05-write R05-saved", "Saved"},
	}
	out := []Trace{}
	for _, spec := range specs {
		t := Trace{Name: spec.name, Initial: Initial("source-a", spec.input), Rules: strings.Fields(spec.rules), Final: spec.final}
		s := t.Initial
		for _, action := range strings.Fields(spec.actions) {
			env := envelopeOfState(s)
			var e Event
			switch action {
			case "claim":
				e = ClaimGranted{env}
			case "denied":
				e = ClaimDenied{env}
			case "auth":
				e = AuthorizationGranted{env}
			case "deny":
				e = AuthorizationDenied{env}
			case "summary", "late-summary":
				e = SummarySucceeded{env, "summary"}
			case "summary-fail":
				e = SummaryFailed{env}
			case "empty":
				e = SummarySucceeded{env, ""}
			case "persist":
				e = PayloadPersisted{env}
			case "write", "late-write":
				e = WriteSucceeded{env, "exact-entry"}
			case "fail":
				e = WriteFailed{env}
			case "duplicate":
				e = DuplicateDelivered{env}
			case "revoke":
				e = Revoked{env}
			case "tombstone":
				e = WriteDeleted{env}
			case "timeout":
				if w, ok := workOf(s); ok {
					env.Now = w.Deadline
				}
				e = Timeout{env}
			case "tick":
				env.Now = s.(RetryWaiting).Due
				e = RetryTick{env}
			case "recover", "early-recover":
				if w, ok := workOf(s); ok {
					env.Now = w.Deadline
				}
				if waiting, ok := s.(RetryWaiting); ok && action == "recover" {
					env.Now = waiting.Due
				}
				env.Generation = jobOf(s).Generation + 1
				env.Owner = "frontend-b"
				e = RestartRecovered{env}
			case "manual":
				env.Generation = jobOf(s).Generation + 1
				e = AuthorizedRetry{env}
			case "competitor", "stale":
				env.Owner = "frontend-b"
				if action == "stale" {
					env.Owner = "frontend-a"
					env.Generation = 1
				}
				e = AuthorizationGranted{env}
			case "early-write":
				e = WriteSucceeded{env, "premature"}
			case "early-summary":
				e = SummarySucceeded{env, "premature"}
			}
			t.Events = append(t.Events, e)
			s = Reduce(s, e).State
		}
		for _, id := range t.Rules {
			t.Effects = append(t.Effects, expectedEffects(id))
		}
		out = append(out, t)
	}
	return out
}
func expectedEffects(id string) []string {
	switch id {
	case "R02-claim", "R06-recover", "R08-retry":
		return []string{"CheckAuthorization", "PublishStatus"}
	case "R03-transform":
		return []string{"Summarize"}
	case "R04-content", "R04-summary", "R04-resume":
		return []string{"PersistPayload"}
	case "R04-durable":
		return []string{"CheckAuthorization"}
	case "R05-write":
		return []string{"ReconcileOrWrite"}
	case "R06-wait":
		return []string{"ScheduleRetry", "PublishStatus"}
	case "R05-saved", "R03-revoked", "R03-denied", "R04-empty", "R04-failed", "R05-invalid", "R06-exhausted", "R06-ready", "R07-deleted":
		return []string{"PublishStatus"}
	}
	return []string{}
}
