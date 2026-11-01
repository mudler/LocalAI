// SPDX-License-Identifier: MIT
package music

import (
	"strconv"
	"unicode/utf8"
)

// Preflight consumes facts established by trusted adapters, not caller assertions.
// Parsing, authorization and capability probes perform no inference here.
func Preflight(r request) (State, string) {
	if r.ID == "" || r.Principal == "" || !r.Feature || !r.Quota || !r.InputsValid || r.Now >= r.Deadline || r.Deadline-r.Now > 900 {
		return nil, "preflight"
	}
	switch r.Operation {
	case transcription:
		if !r.Transcription.permits() {
			return nil, "preflight"
		}
	case generation:
		if !r.Generation.permits() || !r.StorageReady {
			return nil, "preflight"
		}
	case cover:
		if !r.Transcription.permits() || !r.Generation.permits() || !r.StorageReady || r.ABC != "" {
			return nil, "preflight"
		}
	default:
		return nil, "preflight"
	}
	if r.ABC != "" && !validABC(r.ABC) {
		return nil, "preflight"
	}
	s := session{Request: r, ABC: r.ABC}
	if r.ABC != "" {
		s.ABCSource = "provided"
	}
	return Validated{s}, ""
}

// This is a result envelope guard, not the notation parser. InputsValid and
// TaskCompleted.Valid certify the stricter parser/model-context checks.
func validABC(s string) bool {
	if s == "" || len(s) > 256*1024 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r == 0 {
			return false
		}
	}
	return true
}
func stateSession(s State) (session, bool) {
	switch v := s.(type) {
	case Validated:
		return v.Session, false
	case Transcribing:
		return v.Session, false
	case Generating:
		return v.Session, false
	case Succeeded:
		return v.Session, true
	case Failed:
		return v.Session, true
	case Cancelled:
		return v.Session, true
	}
	panic("unknown music state")
}
func withSession(s State, x session) State {
	switch v := s.(type) {
	case Validated:
		return Validated{x}
	case Transcribing:
		return Transcribing{x}
	case Generating:
		return Generating{x}
	case Succeeded:
		v.Session = x
		return v
	case Failed:
		v.Session = x
		return v
	case Cancelled:
		v.Session = x
		return v
	}
	panic("unknown music state")
}
func cmd(s session, id string) command { return command{s.Request.ID, s.Stage, id} }
func active(s session) State {
	if s.Stage == transcribe {
		return Transcribing{s}
	}
	return Generating{s}
}
func transition(s State, rule, guard string, e ...Effect) Transition {
	return Transition{s, e, rule, guard}
}
func ignored(s State, why string) Transition { return transition(s, "R9", why) }
func correlationOf(e Event) (correlation, bool) {
	switch v := e.(type) {
	case Start, Cancel, DeadlineExpired:
		return correlation{}, false
	case Admitted:
		return v.correlation, true
	case AdmissionDenied:
		return v.correlation, true
	case TaskCompleted:
		return v.correlation, true
	case TaskFailed:
		return v.correlation, true
	case StageSettled:
		return v.correlation, true
	case InputCleaned:
		return v.correlation, true
	case GenerationChecked:
		return v.correlation, true
	case OutputFetched:
		return v.correlation, true
	case OutputPrepared:
		return v.correlation, true
	case OutputPublished:
		return v.correlation, true
	case EffectFailed:
		return v.correlation, true
	case CleanupAcknowledged:
		return v.correlation, true
	}
	return correlation{}, false
}

// Each stage has one bounded command of each kind; there are no retries.
func eventID(e Event) string {
	switch e.(type) {
	case Admitted, AdmissionDenied:
		return "admit"
	case TaskCompleted, TaskFailed:
		return "run"
	case StageSettled:
		return "stop"
	case InputCleaned:
		return "input"
	case GenerationChecked:
		return "recheck"
	case OutputFetched:
		return "fetch"
	case OutputPrepared:
		return "prepare"
	case OutputPublished:
		return "commit"
	case CleanupAcknowledged:
		return "cleanup"
	}
	return ""
}
func phaseID(p phase) string {
	switch p.(type) {
	case admitting:
		return "admit"
	case running:
		return "run"
	case settling:
		return "stop"
	case cleaningInput:
		return "input"
	case rechecking:
		return "recheck"
	case fetching:
		return "fetch"
	case preparing:
		return "prepare"
	case committing:
		return "commit"
	}
	return ""
}
func knownEvent(e Event) bool {
	switch e.(type) {
	case Start, Admitted, AdmissionDenied, TaskCompleted, TaskFailed, StageSettled, InputCleaned, GenerationChecked, OutputFetched, OutputPrepared, OutputPublished, EffectFailed, Cancel, DeadlineExpired, CleanupAcknowledged:
		return true
	}
	return false
}
func stopping(o ownership) ownership {
	if o == admissionPending {
		return admissionStopPending
	}
	return stopPending
}
func failure(s session, code string) Transition {
	effects := []Effect{Revoke{cmd(s, "revoke")}, CleanInput{cmd(s, "cleanup")}, Respond{cmd(s, "response")}}
	if s.Ownership == admissionPending || s.Ownership == laneOwned {
		s.Ownership = stopping(s.Ownership)
		effects = append([]Effect{Stop{cmd(s, "stop")}}, effects...)
	}
	return transition(Failed{s, code}, "R7", "stage failed; retain exact captured ABC", effects...)
}
func begin(s session, st stage, rule string) Transition {
	s.Stage = st
	s.Phase = admitting{}
	s.Ownership = admissionPending
	model := s.Request.Generation.Model
	if st == transcribe {
		model = s.Request.Transcription.Model
	}
	return transition(active(s), rule, "authorized preflight; immediate admission only", Admit{cmd(s, "admit"), model})
}

// Reduce is the sole terminal/publication authority. The caller must serialize
// delivery of events and execution of CommitPublication against cancellation.
// No effect executes here; time is supplied by DeadlineExpired.
func Reduce(state State, event Event) Transition {
	s, terminal := stateSession(state)
	if !knownEvent(event) {
		return ignored(state, "invalid event; fail closed")
	}
	// A previously issued run can still produce quarantined resources after its
	// stage has released. It can never supply a new result or restart work.
	if v, ok := event.(TaskCompleted); ok && v.Request == s.Request.ID && v.ID == "run" && v.Stage >= transcribe && v.Stage <= generate && s.Runs&(1<<v.Stage) != 0 {
		_, runningNow := s.Phase.(running)
		if terminal || v.Stage != s.Stage || !runningNow {
			if v.Artifact > 0 && v.Artifact <= 8 {
				bit := uint16(1) << v.Artifact
				if s.Cleaned[v.Stage]&bit == 0 {
					s.Cleaned[v.Stage] |= bit
					c := command{s.Request.ID, v.Stage, "late-" + strconv.Itoa(int(v.Artifact))}
					return transition(withSession(state, s), "R9", "late owned artifact; idempotent quarantine cleanup", CleanArtifact{c, v.Artifact})
				}
			}
			return ignored(state, "duplicate owned completion; result authority already consumed")
		}
	}
	if c, ok := correlationOf(event); ok && (c.Request != s.Request.ID || c.Stage != s.Stage) {
		return ignored(state, "foreign request or stage ownership")
	}
	if c, ok := correlationOf(event); ok {
		want := eventID(event)
		if _, failed := event.(EffectFailed); failed {
			want = phaseID(s.Phase)
		}
		if c.ID == "" || c.ID != want {
			return ignored(state, "stale or mismatched effect ID")
		}
	}
	if terminal {
		return terminalEvent(state, s, event)
	}
	switch v := event.(type) {
	case Cancel, DeadlineExpired:
		if d, ok := event.(DeadlineExpired); ok && d.Now < s.Request.Deadline {
			return ignored(state, "deadline has not expired")
		}
		reason := "client_disconnect"
		if _, ok := event.(DeadlineExpired); ok {
			reason = "deadline"
		}
		effects := []Effect{Revoke{cmd(s, "revoke")}, CleanInput{cmd(s, "cleanup")}, Respond{cmd(s, "response")}}
		if s.Ownership == admissionPending || s.Ownership == laneOwned {
			s.Ownership = stopping(s.Ownership)
			effects = append([]Effect{Stop{cmd(s, "stop")}}, effects...)
		}
		return transition(Cancelled{s, reason}, "R8", "cancellation precedes serialized success commit", effects...)
	case Start:
		if _, ok := state.(Validated); ok {
			if s.Request.Operation == generation {
				return begin(s, generate, "R2")
			}
			return begin(s, transcribe, "R1")
		}
	case Admitted:
		if _, ok := s.Phase.(admitting); ok && s.Ownership == admissionPending {
			s.Phase = running{}
			s.Ownership = laneOwned
			s.Runs |= 1 << s.Stage
			rule := "R1"
			if s.Stage == generate {
				rule = "R2"
			}
			return transition(active(s), rule, "matching immediate admission granted", Run{cmd(s, "run"), s.ABC})
		}
	case AdmissionDenied:
		if _, ok := s.Phase.(admitting); ok {
			s.Ownership = unowned
			return failure(s, "capacity_429")
		}
	case TaskCompleted:
		if _, ok := s.Phase.(running); ok {
			// The accepted artifact remains under Stop/fetch cleanup authority.
			if v.Artifact > 0 && v.Artifact <= 8 {
				s.Cleaned[s.Stage] |= uint16(1) << v.Artifact
			}
			good := v.Valid
			if s.Stage == transcribe {
				good = good && validABC(v.ABC)
				if good {
					s.ABC = v.ABC
					s.ABCSource = "transcribed"
				}
			} else if v.ABC != "" {
				good = good && validABC(v.ABC)
				if good && s.ABC == "" {
					s.ABC = v.ABC
					s.ABCSource = "generated"
				}
			}
			s.Phase = settling{outcome{Failed: !good}}
			s.Ownership = stopPending
			return transition(active(s), "R3", "result captured; quiescence and release still required", Stop{cmd(s, "stop")})
		}
	case TaskFailed:
		if _, ok := s.Phase.(running); ok {
			s.Phase = settling{outcome{Failed: true}}
			s.Ownership = stopPending
			return transition(active(s), "R3", "failure captured; wait for actual quiescence", Stop{cmd(s, "stop")})
		}
	case StageSettled:
		if p, ok := s.Phase.(settling); ok && s.Ownership == stopPending {
			s.Ownership = unowned
			if p.Outcome.Failed {
				return failure(s, "backend")
			}
			if s.Stage == transcribe {
				s.Phase = cleaningInput{}
				rule := "R4"
				if s.Request.Operation == cover {
					rule = "R5"
				}
				return transition(active(s), rule, "quiescence and slot release acknowledged; clean source", CleanInput{cmd(s, "input")})
			}
			s.Phase = fetching{}
			return transition(active(s), "R6", "generation released; fetch and validate bounded audio", FetchValidate{cmd(s, "fetch")})
		}
	case InputCleaned:
		if _, ok := s.Phase.(cleaningInput); ok {
			if s.Request.Operation == transcription {
				return transition(Succeeded{s, ""}, "R4", "released stage and source cleanup acknowledged", Respond{cmd(s, "response")})
			}
			s.Phase = rechecking{}
			return transition(active(s), "R5", "first-stage resources gone; recheck generation facts", CheckGeneration{cmd(s, "recheck")})
		}
	case GenerationChecked:
		if _, ok := s.Phase.(rechecking); ok {
			if !v.Facts.permits() || v.Facts.Model != s.Request.Generation.Model {
				return failure(s, "generation_preflight")
			}
			return begin(s, generate, "R5")
		}
	case OutputFetched:
		if _, ok := s.Phase.(fetching); ok {
			if !v.Valid {
				return failure(s, "fetch_validation")
			}
			s.Phase = preparing{}
			return transition(active(s), "R6", "bounded audio validated; prepare private audio and manifest", PreparePublication{cmd(s, "prepare")})
		}
	case OutputPrepared:
		if _, ok := s.Phase.(preparing); ok {
			if !v.Valid {
				return failure(s, "publication_prepare")
			}
			s.Phase = committing{}
			return transition(active(s), "R6", "prepared only; serialized commit still required", CommitPublication{cmd(s, "commit")})
		}
	case OutputPublished:
		if _, ok := s.Phase.(committing); ok {
			if v.Output == "" {
				return failure(s, "publication_commit")
			}
			return transition(Succeeded{s, v.Output}, "R10", "live request; private audio and durable manifest committed", Respond{cmd(s, "response")})
		}
	case EffectFailed:
		switch s.Phase.(type) {
		case admitting, running, cleaningInput, rechecking, fetching, preparing, committing:
			return failure(s, "effect")
		}
	case CleanupAcknowledged:
	}
	return ignored(state, "event not enabled in current tagged phase")
}
func terminalEvent(state State, s session, event Event) Transition {
	switch event.(type) {
	case AdmissionDenied:
		if s.Ownership == admissionStopPending {
			s.Ownership = unowned
			return transition(withSession(state, s), "R9", "cancelled pending admission denied; no lane acquired")
		}
	case StageSettled:
		if s.Ownership != unowned {
			s.Ownership = unowned
			return transition(withSession(state, s), "R9", "terminal ownership release acknowledged once")
		}
	case Admitted:
		// Stop was already issued for pending admission. The runner must honor it
		// even if acquisition completes after cancellation.
		return ignored(state, "terminal admission cannot start inference; stop already requested")
	case OutputPublished, OutputPrepared, OutputFetched:
		// Revoke is issued by terminal entry; repeating a message cannot issue a
		// second commit or response, or revoke an already committed success.
		return ignored(state, "terminal publication cannot supersede commit authority")
	case Start, TaskCompleted, TaskFailed, InputCleaned, GenerationChecked, EffectFailed, Cancel, DeadlineExpired, CleanupAcknowledged:
	}
	return ignored(state, "terminal duplicate or late acknowledgement")
}
