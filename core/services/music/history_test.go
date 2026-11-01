// SPDX-License-Identifier: MIT
package music

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Check every success prefix, including terminal, rather than only the two
// original leak positions. Handles are stage-local; reuse in stage two is legal.
var _ = Describe("issued history", Label("traces"), func() {
	It("IssuedRunCleanup", func() {
		for _, op := range []operation{transcription, generation, cover} {
			s, _ := Preflight(fixture(op))
			ws := successSteps(op)
			for cut := 0; cut <= len(ws); cut++ {
				for _, st := range []stage{transcribe, generate} {
					for _, terminalize := range []bool{false, true} {
						func() {
							By(fmt.Sprintf("operation=%d prefix=%d stage=%d cancelled=%t", op, cut, st, terminalize))
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
									Expect(names(tr.Effects)).To(Equal([]string{"music.CleanArtifact"}), "owned artifact not cleaned: %#v", tr)
									Expect(tr.Guard).To(Equal("late owned artifact; idempotent quarantine cleanup"))
									clean := tr.Effects[0].(CleanArtifact)
									Expect(clean.Stage).To(Equal(st))
									Expect(clean.Artifact).To(Equal(artifact))
									after, _ := stateSession(tr.State)
									Expect(after.ABC).To(Equal(x.ABC))
									Expect(after.Ownership).To(Equal(x.Ownership))
									Expect(tr.State).To(BeAssignableToTypeOf(current))
								} else {
									Expect(tr.Effects).To(BeEmpty(), "unissued run had effects: %#v", tr)
									Expect(tr.State).To(Equal(current))
								}
								duplicate := Reduce(tr.State, e)
								Expect(duplicate.Effects).To(BeEmpty(), "duplicate logical cleanup: %#v", duplicate)
								Expect(duplicate.State).To(Equal(tr.State))
								current = tr.State
							}
						}()
					}
				}
				if cut < len(ws) {
					s = Reduce(s, ws[cut].Event).State
				}
			}
		}
	})
})

var _ = Describe("issued history", Label("traces"), func() {
	It("ExactIgnoreReasons", func() {
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
			Expect(tr.Rule).To(Equal("R9"), "%#v", c.e)
			Expect(tr.Guard).To(Equal(c.reason))
			Expect(tr.Effects).To(BeEmpty())
			Expect(tr.State).To(Equal(s))
		}
		s = Reduce(s, Start{}).State
		tr := Reduce(s, AdmissionDenied{identified(corr(transcribe), "admit")})
		Expect(tr.Rule).To(Equal("R7"))
		Expect(tr.Guard).To(Equal("stage failed; retain exact captured ABC"))
		Expect(tr.State.(Failed).Code).To(Equal("capacity_429"))
		for _, e := range []Event{Admitted{correlation{"request-a", transcribe, "stale"}}, TaskCompleted{correlation{"request-a", transcribe, "stale"}, "", true, 2}} {
			tr = Reduce(s, e)
			Expect(tr.Rule).To(Equal("R9"))
			Expect(tr.Guard).To(Equal("stale or mismatched effect ID"))
			Expect(tr.Effects).To(BeEmpty())
		}
	})
})

var _ = Describe("issued history", Label("traces"), func() {
	It("LatePublicationCleanupAuthority", func() {
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
				Expect(revoked).To(BeTrue(), "missing persistent cleanup authority cut=%d fail=%t", cut, fail)
				for _, late := range []Event{OutputFetched{identified(corr(generate), "fetch"), true}, OutputPrepared{identified(corr(generate), "prepare"), true}, OutputPublished{identified(corr(generate), "commit"), "late-output"}} {
					next := Reduce(tr.State, late)
					Expect(next.Guard).To(Equal("terminal publication cannot supersede commit authority"))
					Expect(next.Effects).To(BeEmpty())
					Expect(next.State).To(Equal(tr.State))
				}
			}
		}
	})
})
