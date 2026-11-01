// SPDX-License-Identifier: MIT
package music

import (
	"fmt"
	"reflect"
	"testing"
)

// Check every success prefix, including terminal, rather than only the two
// original leak positions. Handles are stage-local; reuse in stage two is legal.
func TestMusicWorkflowIssuedRunCleanup(t *testing.T) {
	for _, op := range []operation{transcription, generation, cover} {
		s, _ := Preflight(fixture(op))
		ws := successSteps(op)
		for cut := 0; cut <= len(ws); cut++ {
			for _, st := range []stage{transcribe, generate} {
				for _, terminalize := range []bool{false, true} {
					t.Run(fmt.Sprintf("%d/%d/%d/%t", op, cut, st, terminalize), func(t *testing.T) {
						current := s
						if terminalize {
							current = Reduce(current, Cancel{}).State
						}
						x, terminal := stateSession(current)
						_, runningNow := x.Phase.(running)
						if !terminal && st == x.Stage && runningNow {
							return
						} // still the authoritative first result
						for _, artifact := range []uint8{2, 3, 8} {
							e := TaskCompleted{identified(corr(st), "run"), "replacement", true, artifact}
							tr := Reduce(current, e)
							owned := x.Runs&(1<<st) != 0
							if owned {
								if !reflect.DeepEqual(names(tr.Effects), []string{"music.CleanArtifact"}) || tr.Guard != "late owned artifact; idempotent quarantine cleanup" {
									t.Fatalf("owned artifact not cleaned: %#v", tr)
								}
								clean := tr.Effects[0].(CleanArtifact)
								if clean.Stage != st || clean.Artifact != artifact {
									t.Fatalf("wrong cleanup authority: %#v", clean)
								}
								after, _ := stateSession(tr.State)
								if after.ABC != x.ABC || after.Ownership != x.Ownership || reflect.TypeOf(tr.State) != reflect.TypeOf(current) {
									t.Fatal("stale result changed authority")
								}
							} else if len(tr.Effects) != 0 || !reflect.DeepEqual(tr.State, current) {
								t.Fatalf("unissued run had effects: %#v", tr)
							}
							duplicate := Reduce(tr.State, e)
							if len(duplicate.Effects) != 0 || !reflect.DeepEqual(duplicate.State, tr.State) {
								t.Fatalf("duplicate logical cleanup: %#v", duplicate)
							}
							current = tr.State
						}
					})
				}
			}
			if cut < len(ws) {
				s = Reduce(s, ws[cut].Event).State
			}
		}
	}
}

func TestMusicWorkflowExactIgnoreReasons(t *testing.T) {
	s, _ := Preflight(fixture(cover))
	cases := []struct {
		e      Event
		reason string
	}{
		{nil, "invalid event; fail closed"},
		{unknownEvent{}, "invalid event; fail closed"},
		{Admitted{correlation{"foreign", transcribe, "admit"}}, "foreign request or stage ownership"},
		{Admitted{correlation{"request-a", 9, "admit"}}, "foreign request or stage ownership"},
		{Admitted{correlation{"request-a", 0, "stale"}}, "stale or mismatched effect ID"},
		{DeadlineExpired{2}, "deadline has not expired"},
		{TaskCompleted{correlation{"request-a", transcribe, "run"}, "ignored", true, 2}, "foreign request or stage ownership"},
	}
	for _, c := range cases {
		tr := Reduce(s, c.e)
		if tr.Rule != "R9" || tr.Guard != c.reason || len(tr.Effects) != 0 || !reflect.DeepEqual(tr.State, s) {
			t.Fatalf("%#v: %#v", c.e, tr)
		}
	}
	s = Reduce(s, Start{}).State
	tr := Reduce(s, AdmissionDenied{identified(corr(transcribe), "admit")})
	if tr.Rule != "R7" || tr.Guard != "stage failed; retain exact captured ABC" || tr.State.(Failed).Code != "capacity_429" {
		t.Fatalf("denial: %#v", tr)
	}
	for _, e := range []Event{Admitted{correlation{"request-a", transcribe, "stale"}}, TaskCompleted{correlation{"request-a", transcribe, "stale"}, "", true, 2}} {
		tr = Reduce(s, e)
		if tr.Rule != "R9" || tr.Guard != "stale or mismatched effect ID" || len(tr.Effects) != 0 {
			t.Fatalf("stale: %#v", tr)
		}
	}
}

func TestMusicWorkflowLatePublicationCleanupAuthority(t *testing.T) {
	// Revoke is a persistent tombstone command, not a one-shot delete. The
	// future shell must enforce it for resources completing after cancellation.
	// Here prove it is issued before any terminal late resource acknowledgements.
	ws := successSteps(generation)
	for _, cut := range []int{4, 5, 6} {
		for _, fail := range []bool{false, true} {
			s, _ := Preflight(fixture(generation))
			for _, w := range ws[:cut] {
				s = Reduce(s, w.Event).State
			}
			x, _ := stateSession(s)
			var e Event = Cancel{}
			if fail {
				e = EffectFailed{identified(corr(generate), phaseID(x.Phase))}
			}
			tr := Reduce(s, e)
			revoked := false
			for _, eff := range tr.Effects {
				if r, ok := eff.(Revoke); ok {
					revoked = r.Request == x.Request.ID && r.Stage == generate && r.ID == "revoke"
				}
			}
			if !revoked {
				t.Fatalf("missing persistent cleanup authority cut=%d fail=%t", cut, fail)
			}
			for _, late := range []Event{OutputFetched{identified(corr(generate), "fetch"), true}, OutputPrepared{identified(corr(generate), "prepare"), true}, OutputPublished{identified(corr(generate), "commit"), "late-output"}} {
				next := Reduce(tr.State, late)
				if next.Guard != "terminal publication cannot supersede commit authority" || len(next.Effects) != 0 || !reflect.DeepEqual(next.State, tr.State) {
					t.Fatalf("late resource escaped tombstone: %#v", next)
				}
			}
		}
	}
}
