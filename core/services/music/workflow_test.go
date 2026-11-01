// SPDX-License-Identifier: MIT
package music

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type step struct {
	Event       Event
	Rule, Phase string
	Effects     []string
}

func typeName(v any) string { return fmt.Sprintf("%T", v) }
func names(es []Effect) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, typeName(e))
	}
	return out
}
func corr(st stage) correlation { return correlation{"request-a", st, ""} }
func successSteps(op operation) []step {
	st := transcribe
	rule := "R1"
	if op == generation {
		st = generate
		rule = "R2"
	}
	c := corr(st)
	out := []step{{Start{}, rule, "music.admitting", []string{"music.Admit"}}, {Admitted{identified(c, "admit")}, rule, "music.running", []string{"music.Run"}}, {TaskCompleted{identified(c, "run"), "X:1\r\nK:C\nC D|\n", true, 1}, "R3", "music.settling", []string{"music.Stop"}}}
	if st == transcribe {
		rule = "R4"
		if op == cover {
			rule = "R5"
		}
		out = append(out, step{StageSettled{identified(c, "stop")}, rule, "music.cleaningInput", []string{"music.CleanInput"}})
		if op == transcription {
			return append(out, step{InputCleaned{identified(c, "input")}, "R4", "terminal", []string{"music.Respond"}})
		}
		out = append(out, step{InputCleaned{identified(c, "input")}, "R5", "music.rechecking", []string{"music.CheckGeneration"}}, step{GenerationChecked{identified(c, "recheck"), fixture(cover).Generation}, "R5", "music.admitting", []string{"music.Admit"}})
		c = corr(generate)
		out = append(out, step{Admitted{identified(c, "admit")}, "R2", "music.running", []string{"music.Run"}}, step{TaskCompleted{identified(c, "run"), "", true, 1}, "R3", "music.settling", []string{"music.Stop"}})
	}
	return append(out, step{StageSettled{identified(c, "stop")}, "R6", "music.fetching", []string{"music.FetchValidate"}}, step{OutputFetched{identified(c, "fetch"), true}, "R6", "music.preparing", []string{"music.PreparePublication"}}, step{OutputPrepared{identified(c, "prepare"), true}, "R6", "music.committing", []string{"music.CommitPublication"}}, step{OutputPublished{identified(c, "commit"), "output-a"}, "R10", "terminal", []string{"music.Respond"}})
}
func phaseName(s State) string {
	x, term := stateSession(s)
	if term {
		return "terminal"
	}
	return typeName(x.Phase)
}
func runSteps(g Gomega, op operation, steps []step) State {
	s, code := Preflight(fixture(op))
	g.Expect(code).To(BeEmpty())
	for i, w := range steps {
		before := s
		tr := Reduce(s, w.Event)
		g.Expect(tr.Rule).To(Equal(w.Rule), fmt.Sprint(i))
		g.Expect(tr.Guard).To(Equal(expectedTraceGuard(w)))
		g.Expect(names(tr.Effects)).To(Equal(w.Effects), fmt.Sprint(i))
		g.Expect(phaseName(tr.State)).To(Equal(w.Phase), fmt.Sprint(i))
		x, terminal := stateSession(tr.State)
		if !terminal {
			switch op {
			case transcription:
				g.Expect(tr.State).To(BeAssignableToTypeOf(Transcribing{}))
			case generation:
				g.Expect(tr.State).To(BeAssignableToTypeOf(Generating{}))
			case cover:
				if x.Stage == transcribe {
					g.Expect(tr.State).To(BeAssignableToTypeOf(Transcribing{}))
				} else {
					g.Expect(tr.State).To(BeAssignableToTypeOf(Generating{}))
				}
			}
		}
		b, _ := json.Marshal(map[string]any{"step": i, "event": fmt.Sprintf("%#v", w.Event), "before": fmt.Sprintf("%#v", before), "after": fmt.Sprintf("%#v", tr.State), "rule": tr.Rule, "guard": tr.Guard, "effects": fmt.Sprintf("%#v", tr.Effects), "state": typeName(tr.State)})
		_, err := fmt.Fprintln(GinkgoWriter, "TRACE "+string(b))
		g.Expect(err).NotTo(HaveOccurred())
		if path := os.Getenv("MUSIC_TRACE_FILE"); path != "" {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			g.Expect(err).NotTo(HaveOccurred())
			_, err = fmt.Fprintln(f, string(b))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(f.Close()).To(Succeed())
		}
		s = tr.State
	}
	return s
}

var _ = Describe("pure workflow", func() {
	It("traces all operations, edited score, shared and separate lanes", Label("traces"), func() {
		for _, op := range []operation{transcription, generation, cover} {
			s := runSteps(Default, op, successSteps(op))
			Expect(s).To(BeAssignableToTypeOf(Succeeded{}))
		}
		r := fixture(generation)
		r.ABC = "X:2\nK:D\nD|\n"
		s, _ := Preflight(r)
		for _, w := range successSteps(generation) {
			tr := Reduce(s, w.Event)
			s = tr.State
			x, _ := stateSession(s)
			Expect(x.ABC).To(Equal(r.ABC))
		}
	})
	It("checks safety and fair-ack liveness over a bounded nonvacuous graph", Label("invariants"), func() { checkInvariants(Default) })
	It("covers the sealed vocabulary and pairs", Label("coverage"), func() { checkCoverage(Default) })
	It("replays deterministically without aliased mutable values", Label("determinism"), func() { checkDeterminism(Default) })
	It("keeps captured ABC for every subsequent failure and cancellation", Label("traces"), func() {
		steps := successSteps(cover)
		s, _ := Preflight(fixture(cover))
		for _, w := range steps {
			x, _ := stateSession(s)
			if x.ABC != "" {
				for _, e := range []Event{Cancel{}, DeadlineExpired{100}, EffectFailed{identified(corr(x.Stage), phaseID(x.Phase))}} {
					tr := Reduce(s, e)
					y, _ := stateSession(tr.State)
					Expect(y.ABC).To(Equal(x.ABC))
				}
			}
			s = Reduce(s, w.Event).State
		}
	})
	It("rejects revocation and drift before second admission", Label("traces"), func() {
		s, _ := Preflight(fixture(cover))
		for _, w := range successSteps(cover)[:5] {
			s = Reduce(s, w.Event).State
		}
		for _, f := range []facts{{false, true, true, true, "audio-model"}, {true, false, true, true, "audio-model"}, {true, true, false, true, "audio-model"}, {true, true, true, false, "audio-model"}, {true, true, true, true, "replacement"}} {
			tr := Reduce(s, GenerationChecked{identified(corr(transcribe), "recheck"), f})
			Expect(tr.State).To(BeAssignableToTypeOf(Failed{}))
			for _, e := range tr.Effects {
				Expect(e).NotTo(BeAssignableToTypeOf(Admit{}))
			}
		}
	})
	It("serializes both publication race orders", Label("traces"), func() {
		ws := successSteps(generation)
		s, _ := Preflight(fixture(generation))
		for _, w := range ws[:len(ws)-1] {
			s = Reduce(s, w.Event).State
		}
		cancelled := Reduce(s, Cancel{}).State
		late := Reduce(cancelled, ws[len(ws)-1].Event)
		Expect(late.State).To(BeAssignableToTypeOf(Cancelled{}))
		Expect(late.Effects).To(BeEmpty())
		committed := Reduce(s, ws[len(ws)-1].Event).State
		Expect(Reduce(committed, Cancel{}).State).To(Equal(committed))
	})
})

func events(s State) []Event {
	x, _ := stateSession(s)
	c := correlation{x.Request.ID, x.Stage, ""}
	out := []Event{Start{}, Admitted{identified(c, "admit")}, AdmissionDenied{identified(c, "admit")}, TaskCompleted{identified(c, "run"), "X:1\r\nK:C\nC D|\n", true, 1}, TaskCompleted{identified(c, "run"), "bad", false, 2}, TaskFailed{identified(c, "run")}, StageSettled{identified(c, "stop")}, InputCleaned{identified(c, "input")}, GenerationChecked{identified(c, "recheck"), fixture(cover).Generation}, GenerationChecked{identified(c, "recheck"), facts{}}, OutputFetched{identified(c, "fetch"), true}, OutputFetched{identified(c, "fetch"), false}, OutputPrepared{identified(c, "prepare"), true}, OutputPrepared{identified(c, "prepare"), false}, OutputPublished{identified(c, "commit"), "output-a"}, EffectFailed{identified(c, phaseID(x.Phase))}, Cancel{}, DeadlineExpired{100}, DeadlineExpired{2}, CleanupAcknowledged{identified(c, "cleanup")}}
	base := append([]Event{}, out...)
	for _, e := range base {
		if c, ok := correlationOf(e); ok {
			for _, st := range []stage{transcribe, generate} {
				c.Stage = st
				out = append(out, replaceCorrelation(e, c))
			}
			for _, bad := range []correlation{{"foreign", c.Stage, c.ID}, {c.Request, 9, c.ID}, {c.Request, c.Stage, "stale"}, {c.Request, c.Stage, ""}} {
				out = append(out, replaceCorrelation(e, bad))
			}
		}
	}
	out = append(out, nil, unknownEvent{})
	return out
}
func key(s State) string { return fmt.Sprintf("%#v", s) }
func checkInvariants(g Gomega) {
	type node struct {
		s      State
		path   []string
		issued map[string]bool
	}
	nodeKey := func(n node) string { b, _ := json.Marshal(n.issued); return key(n.s) + string(b) }
	seen := map[string]node{}
	edges := map[string][]string{}
	fair := map[string][]string{}
	queue := []node{}
	for _, op := range []operation{transcription, generation, cover} {
		s, _ := Preflight(fixture(op))
		n := node{s, nil, map[string]bool{}}
		seen[nodeKey(n)] = n
		queue = append(queue, n)
	}
	transitions := 0
	rules := map[string]bool{}
	tags := map[string]bool{}
	cleanups, releases, malformed := 0, 0, 0
	responses := 0
	commits := 0
	for i := 0; i < len(queue); i++ {
		n := queue[i]
		x, terminal := stateSession(n.s)
		tags[typeName(n.s)+"/"+phaseName(n.s)] = true
		g.Expect(len(queue)).To(BeNumerically("<", 30000), fmt.Sprint(n.path))
		for _, e := range events(n.s) {
			tr := Reduce(n.s, e)
			transitions++
			rules[tr.Rule] = true
			y, nextTerminal := stateSession(tr.State)
			g.Expect(tr.Guard).NotTo(BeEmpty(), fmt.Sprint(append(n.path, fmt.Sprintf("%#v", e))))
			if c, ok := correlationOf(e); ok && (c.Request != x.Request.ID || c.Stage < transcribe || c.Stage > generate || c.ID == "" || c.ID == "stale") {
				malformed++
				g.Expect(tr.State).To(Equal(n.s), fmt.Sprint(n.path))
				g.Expect(tr.Effects).To(BeEmpty(), fmt.Sprint(n.path))
			}
			if v, ok := e.(TaskCompleted); ok && v.Request == x.Request.ID && v.ID == "run" && v.Stage >= transcribe && v.Stage <= generate && x.Runs&(1<<v.Stage) != 0 {
				_, runningNow := x.Phase.(running)
				if (terminal || v.Stage != x.Stage || !runningNow) && v.Artifact > 0 && v.Artifact <= 8 && x.Cleaned[v.Stage]&(1<<v.Artifact) == 0 {
					g.Expect(names(tr.Effects)).To(Equal([]string{"music.CleanArtifact"}), fmt.Sprint(n.path))
					g.Expect(y.ABC).To(Equal(x.ABC))
					g.Expect(y.Ownership).To(Equal(x.Ownership))
				}
			}
			if x.Ownership != unowned && y.Ownership == unowned {
				releases++
				switch e.(type) {
				case StageSettled, AdmissionDenied:
				default:
					g.Expect(false).To(BeTrue(), fmt.Sprint(n.path))
				}
			}
			if !terminal && nextTerminal {
				if _, failed := tr.State.(Failed); failed {
					g.Expect(names(tr.Effects)).To(ContainElements("music.Revoke", "music.CleanInput", "music.Respond"))
				}
				if _, cancelled := tr.State.(Cancelled); cancelled {
					g.Expect(names(tr.Effects)).To(ContainElements("music.Revoke", "music.CleanInput", "music.Respond"))
				}
			}
			g.Expect(y.ABC == x.ABC || x.ABC == "").To(BeTrue(), fmt.Sprint(n.path))
			issued := map[string]bool{}
			for k, v := range n.issued {
				issued[k] = v
			}
			for _, eff := range tr.Effects {
				effectKey := fmt.Sprintf("%#v", eff)
				g.Expect(issued[effectKey]).To(BeFalse(), fmt.Sprint(append(n.path, typeName(e))))
				issued[effectKey] = true
				switch eff.(type) {
				case CleanArtifact:
					cleanups++
				case Admit:
					g.Expect(x.Ownership).To(Equal(unowned))
					g.Expect(terminal).To(BeFalse())
				case Run:
					g.Expect(x.Ownership).To(Equal(admissionPending))
					g.Expect(terminal).To(BeFalse())
				case Respond:
					responses++
					g.Expect(terminal).To(BeFalse())
					g.Expect(nextTerminal).To(BeTrue())
				case CommitPublication:
					commits++
					g.Expect(terminal).To(BeFalse())
					g.Expect(x.Ownership).To(Equal(unowned))
				}
				if terminal {
					g.Expect(eff).To(BeAssignableToTypeOf(CleanArtifact{}))
				}
			}
			if terminal {
				g.Expect(reflect.TypeOf(tr.State)).To(Equal(reflect.TypeOf(n.s)))
				switch before := n.s.(type) {
				case Succeeded:
					g.Expect(tr.State.(Succeeded).Output).To(Equal(before.Output))
				case Failed:
					g.Expect(tr.State.(Failed).Code).To(Equal(before.Code))
				case Cancelled:
					g.Expect(tr.State.(Cancelled).Reason).To(Equal(before.Reason))
				}
			}
			if good, ok := tr.State.(Succeeded); ok && good.Session.Request.Operation != transcription {
				g.Expect(good.Output).NotTo(BeEmpty())
				if !terminal {
					g.Expect(e).To(BeAssignableToTypeOf(OutputPublished{}))
					g.Expect(tr.Rule).To(Equal("R10"))
				}
			}
			candidate := node{tr.State, nil, issued}
			k := nodeKey(candidate)
			edges[nodeKey(n)] = append(edges[nodeKey(n)], k)
			// Successful progress cannot use cancellation or failure as an escape.
			switch e.(type) {
			case Cancel, DeadlineExpired, EffectFailed, TaskFailed, AdmissionDenied:
			default:
				if _, bad := tr.State.(Failed); !bad {
					fair[nodeKey(n)] = append(fair[nodeKey(n)], k)
				}
			}
			if _, ok := seen[k]; !ok {
				path := append(append([]string{}, n.path...), fmt.Sprintf("%#v", e))
				child := node{tr.State, path, issued}
				seen[k] = child
				queue = append(queue, child)
			}
		}
	}
	// Backward reachability proves an actual finite fair-ack path to terminal
	// from every reachable node, rather than merely checking a non-self edge.
	live := map[string]bool{}
	for k, n := range seen {
		x, term := stateSession(n.s)
		if term && x.Ownership == unowned {
			live[k] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for k, es := range edges {
			if live[k] {
				continue
			}
			for _, to := range es {
				if live[to] {
					live[k] = true
					changed = true
					break
				}
			}
		}
	}
	for k, n := range seen {
		g.Expect(live[k]).To(BeTrue(), fmt.Sprint(n.path))
	}
	// A distinct backward graph must reach success under fair successful acks.
	success := map[string]bool{}
	for k, n := range seen {
		if _, ok := n.s.(Succeeded); ok {
			success[k] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for k, es := range fair {
			if !success[k] {
				for _, to := range es {
					if success[to] {
						success[k] = true
						changed = true
						break
					}
				}
			}
		}
	}
	eligible := 0
	for k, n := range seen {
		x, terminal := stateSession(n.s)
		if terminal {
			continue
		}
		if p, ok := x.Phase.(settling); ok && p.Outcome.Failed {
			continue
		}
		eligible++
		g.Expect(success[k]).To(BeTrue(), "no successful fair-ack path: "+fmt.Sprint(n.path))
	}
	g.Expect(eligible).To(BeNumerically(">", 20))
	g.Expect(tags).To(HaveLen(15))
	g.Expect(cleanups).To(BeNumerically(">", 20))
	g.Expect(releases).To(BeNumerically(">", 20))
	g.Expect(malformed).To(BeNumerically(">", 100))
	g.Expect(rules).To(HaveLen(10))
	g.Expect(len(seen)).To(BeNumerically(">", 100))
	g.Expect(responses).To(BeNumerically(">", 10))
	g.Expect(commits).To(BeNumerically(">", 0))
	g.Expect(transitions).To(BeNumerically(">", 1000))
	fmt.Printf("BFS states=%d transitions=%d responses=%d commits=%d tags=%d cleanups=%d releases=%d malformed=%d successEligible=%d\n", len(seen), transitions, responses, commits, len(tags), cleanups, releases, malformed, eligible)
}
func checkCoverage(g Gomega) {
	states := []State{Validated{}, Transcribing{}, Generating{}, Succeeded{}, Failed{}, Cancelled{}}
	g.Expect(states).To(HaveLen(6))
	phases := []phase{admitting{}, running{}, settling{}, cleaningInput{}, rechecking{}, fetching{}, preparing{}, committing{}}
	g.Expect(phases).To(HaveLen(8))
	for _, st := range states {
		for _, e := range events(st) {
			tr := Reduce(st, e)
			g.Expect(tr.Rule).To(BeElementOf("R1", "R2", "R3", "R4", "R5", "R6", "R7", "R8", "R9", "R10"))
			g.Expect(tr.Guard).NotTo(BeEmpty())
		}
	}
	for _, op := range []operation{transcription, generation, cover} {
		s, _ := Preflight(fixture(op))
		for _, w := range successSteps(op) {
			for _, e := range events(s) {
				g.Expect(Reduce(s, e).Guard).NotTo(BeEmpty())
			}
			s = Reduce(s, w.Event).State
		}
	}
}
func checkDeterminism(g Gomega) {
	for _, op := range []operation{transcription, generation, cover} {
		s, _ := Preflight(fixture(op))
		for _, w := range successSteps(op) {
			before := key(s)
			a := Reduce(s, w.Event)
			b := Reduce(s, w.Event)
			g.Expect(a).To(Equal(b))
			g.Expect(key(s)).To(Equal(before))
			if len(a.Effects) > 0 {
				a.Effects[0] = nil
				g.Expect(Reduce(s, w.Event)).To(Equal(b))
			}
			s = b.State
		}
	}
}

var _ = Describe("workflow regressions", Label("traces"), func() {
	It("OwnershipRegression", func() {
		g := Default
		s, _ := Preflight(fixture(generation))
		s = Reduce(s, Start{}).State
		s = Reduce(s, Admitted{identified(corr(generate), "admit")}).State
		s = Reduce(s, Cancel{}).State
		tr := Reduce(s, AdmissionDenied{identified(corr(generate), "admit")})
		x, _ := stateSession(tr.State)
		g.Expect(x.Ownership).To(Equal(stopPending), "an admission denial cannot acknowledge native quiescence after acquisition")
	})
})

var _ = Describe("workflow regressions", Label("traces"), func() {
	It("Correlation", func() {
		g := Default
		s, _ := Preflight(fixture(generation))
		s = Reduce(s, Start{}).State
		c := corr(generate)
		c.ID = "run"
		tr := Reduce(s, Admitted{c})
		g.Expect(tr.State).To(Equal(s))
		g.Expect(tr.Effects).To(BeEmpty())
	})
})

func identified(c correlation, id string) correlation { c.ID = id; return c }

// Adversarial vocabulary is deliberately distinct from the successful shell's
// acknowledgements: foreign IDs, stale stages and missing IDs never own work.
var _ = Describe("workflow regressions", Label("traces"), func() {
	It("Adversarial", func() {
		g := Default
		for _, op := range []operation{transcription, generation, cover} {
			s, _ := Preflight(fixture(op))
			for _, w := range successSteps(op) {
				for _, e := range events(s) {
					if c, ok := correlationOf(e); ok {
						for _, bad := range []correlation{{"foreign", c.Stage, c.ID}, {c.Request, 9, c.ID}, {c.Request, c.Stage, "stale"}, {c.Request, c.Stage, ""}} {
							tr := Reduce(s, replaceCorrelation(e, bad))
							g.Expect(tr.State).To(Equal(s))
							g.Expect(tr.Effects).To(BeEmpty())
							g.Expect(tr.Rule).To(Equal("R9"))
							want := "stale or mismatched effect ID"
							x, _ := stateSession(s)
							if bad.Request != x.Request.ID || bad.Stage != x.Stage {
								want = "foreign request or stage ownership"
							}
							g.Expect(tr.Guard).To(Equal(want))
						}
					}
				}
				tr := Reduce(s, w.Event)
				again := Reduce(tr.State, w.Event)
				g.Expect(again.State).To(Equal(tr.State))
				g.Expect(again.Effects).To(BeEmpty())
				s = tr.State
			}
		}
	})
})

func replaceCorrelation(e Event, c correlation) Event {
	switch v := e.(type) {
	case Admitted:
		return Admitted{c}
	case AdmissionDenied:
		return AdmissionDenied{c}
	case TaskCompleted:
		v.correlation = c
		return v
	case TaskFailed:
		return TaskFailed{c}
	case StageSettled:
		return StageSettled{c}
	case InputCleaned:
		return InputCleaned{c}
	case GenerationChecked:
		v.correlation = c
		return v
	case OutputFetched:
		v.correlation = c
		return v
	case OutputPrepared:
		v.correlation = c
		return v
	case OutputPublished:
		v.correlation = c
		return v
	case EffectFailed:
		return EffectFailed{c}
	case CleanupAcknowledged:
		return CleanupAcknowledged{c}
	}
	panic("non-correlated event")
}

var _ = Describe("workflow regressions", Label("traces"), func() {
	It("Boundaries", func() {
		g := Default
		for _, op := range []operation{transcription, generation, cover} {
			s, _ := Preflight(fixture(op))
			for _, w := range successSteps(op) {
				x, terminal := stateSession(s)
				if !terminal {
					for _, e := range []Event{Cancel{}, DeadlineExpired{100}} {
						tr := Reduce(s, e)
						g.Expect(tr.Rule).To(Equal("R8"))
						g.Expect(tr.State).To(BeAssignableToTypeOf(Cancelled{}))
						y, _ := stateSession(tr.State)
						g.Expect(y.ABC).To(Equal(x.ABC))
						if x.Ownership == admissionPending || x.Ownership == laneOwned {
							g.Expect(tr.Effects[0]).To(BeAssignableToTypeOf(Stop{}))
							g.Expect(y.Ownership).NotTo(Equal(unowned))
						}
						for _, later := range successSteps(op) {
							next := Reduce(tr.State, later.Event)
							g.Expect(next.State).To(BeAssignableToTypeOf(Cancelled{}))
							for _, eff := range next.Effects {
								g.Expect(eff).To(BeAssignableToTypeOf(CleanArtifact{}))
							}
							tr = next
						}
					}
					early := Reduce(s, DeadlineExpired{2})
					g.Expect(early.State).To(Equal(s))
					g.Expect(early.Effects).To(BeEmpty())
					if x.Phase != nil {
						failure := Reduce(s, EffectFailed{identified(corr(x.Stage), phaseID(x.Phase))})
						if _, settlingNow := x.Phase.(settling); !settlingNow {
							g.Expect(failure.State).To(BeAssignableToTypeOf(Failed{}))
							y, _ := stateSession(failure.State)
							g.Expect(y.ABC).To(Equal(x.ABC))
						}
					}
				}
				s = Reduce(s, w.Event).State
			}
		}
	})
})

var _ = Describe("workflow regressions", Label("traces"), func() {
	It("StopOnce", func() {
		g := Default
		s, _ := Preflight(fixture(generation))
		for _, w := range successSteps(generation)[:3] {
			s = Reduce(s, w.Event).State
		}
		tr := Reduce(s, Cancel{})
		for _, eff := range tr.Effects {
			g.Expect(eff).NotTo(BeAssignableToTypeOf(Stop{}), "settling already requested this stage's stop")
		}
	})
})

var _ = Describe("bounded branch traces", func() {
	It("traces every cancellation boundary, deadline and publication ordering", Label("traces"), func() {
		for _, op := range []operation{transcription, generation, cover} {
			ws := successSteps(op)
			for cut := 0; cut < len(ws); cut++ {
				for _, cancel := range []Event{Cancel{}, DeadlineExpired{100}} {
					prefix := append([]step{}, ws[:cut]...)
					s, _ := Preflight(fixture(op))
					for _, w := range prefix {
						s = Reduce(s, w.Event).State
					}
					x, _ := stateSession(s)
					effects := []string{"music.Revoke", "music.CleanInput", "music.Respond"}
					if x.Ownership == admissionPending || x.Ownership == laneOwned {
						effects = append([]string{"music.Stop"}, effects...)
					}
					prefix = append(prefix, step{cancel, "R8", "terminal", effects})
					final := runSteps(Default, op, prefix)
					Expect(final).To(BeAssignableToTypeOf(Cancelled{}))
					for _, w := range ws[cut:] {
						tr := Reduce(final, w.Event)
						Expect(tr.State).To(BeAssignableToTypeOf(Cancelled{}))
						final = tr.State
					}
				}
			}
			committed := append(append([]step{}, ws...), step{Cancel{}, "R9", "terminal", []string{}})
			Expect(runSteps(Default, op, committed)).To(BeAssignableToTypeOf(Succeeded{}))
		}
	})
	It("traces capacity denial, pre-ABC failure, revocation and fetch/publication failures", Label("traces"), func() {
		for _, op := range []operation{transcription, generation, cover} {
			ws := successSteps(op)
			s := runSteps(Default, op, []step{ws[0], {AdmissionDenied{identified(corr(func() stage {
				if op == generation {
					return generate
				}
				return transcribe
			}()), "admit")}, "R7", "terminal", []string{"music.Revoke", "music.CleanInput", "music.Respond"}}})
			Expect(s.(Failed).Code).To(Equal("capacity_429"))
			st := transcribe
			if op == generation {
				st = generate
			}
			c := corr(st)
			prefix := append([]step{}, ws[:2]...)
			prefix = append(prefix, step{TaskFailed{identified(c, "run")}, "R3", "music.settling", []string{"music.Stop"}}, step{StageSettled{identified(c, "stop")}, "R7", "terminal", []string{"music.Revoke", "music.CleanInput", "music.Respond"}})
			Expect(runSteps(Default, op, prefix).(Failed).Session.ABC).To(BeEmpty())
		}
		ws := successSteps(cover)
		prefix := append([]step{}, ws[:5]...)
		prefix = append(prefix, step{GenerationChecked{identified(corr(transcribe), "recheck"), facts{}}, "R7", "terminal", []string{"music.Revoke", "music.CleanInput", "music.Respond"}})
		Expect(runSteps(Default, cover, prefix).(Failed).Session.ABC).To(Equal("X:1\r\nK:C\nC D|\n"))
		for _, cut := range []int{6, 9, 10, 11} {
			prefix = append([]step{}, ws[:cut]...)
			s, _ := Preflight(fixture(cover))
			for _, w := range prefix {
				s = Reduce(s, w.Event).State
			}
			x, _ := stateSession(s)
			effects := []string{"music.Revoke", "music.CleanInput", "music.Respond"}
			if x.Ownership == admissionPending || x.Ownership == laneOwned {
				effects = append([]string{"music.Stop"}, effects...)
			}
			prefix = append(prefix, step{EffectFailed{identified(corr(x.Stage), phaseID(x.Phase))}, "R7", "terminal", effects})
			Expect(runSteps(Default, cover, prefix).(Failed).Session.ABC).To(Equal("X:1\r\nK:C\nC D|\n"))
		}
	})
})

// A shell can route the same canonical lane to both stages or separate workers;
// neither topology changes the release-before-admit protocol.
var _ = Describe("conditioning and lane topology", func() {
	It("retains edited bytes and permits genuinely absent generated notation", Label("traces"), func() {
		r := fixture(generation)
		r.ABC = "X:2\r\nT:édit\r\nK:D\n D2 |\n"
		for _, abc := range []string{r.ABC, ""} {
			r.ABC = abc
			s, _ := Preflight(r)
			ws := successSteps(generation)
			for i, w := range ws {
				if completed, ok := w.Event.(TaskCompleted); ok {
					completed.ABC = ""
					w.Event = completed
				}
				tr := Reduce(s, w.Event)
				Expect(tr.Rule).To(Equal(w.Rule), fmt.Sprint(i))
				Expect(names(tr.Effects)).To(Equal(w.Effects))
				Expect(phaseName(tr.State)).To(Equal(w.Phase))
				Expect(tr.Guard).To(Equal(expectedTraceGuard(w)))
				x, _ := stateSession(tr.State)
				Expect(x.ABC).To(Equal(abc))
				s = tr.State
			}
			Expect(s).To(BeAssignableToTypeOf(Succeeded{}))
		}
	})
	It("never holds or requests both lanes for shared or distinct model pools", Label("traces"), func() {
		for _, same := range []bool{false, true} {
			r := fixture(cover)
			if same {
				r.Generation.Model = r.Transcription.Model
			}
			s, _ := Preflight(r)
			for _, w := range successSteps(cover) {
				if checked, ok := w.Event.(GenerationChecked); ok {
					checked.Facts = r.Generation
					w.Event = checked
				}
				before, _ := stateSession(s)
				tr := Reduce(s, w.Event)
				Expect(tr.Rule).To(Equal(w.Rule))
				Expect(names(tr.Effects)).To(Equal(w.Effects))
				Expect(phaseName(tr.State)).To(Equal(w.Phase))
				Expect(tr.Guard).To(Equal(expectedTraceGuard(w)))
				for _, eff := range tr.Effects {
					if _, ok := eff.(Admit); ok {
						Expect(before.Ownership).To(Equal(unowned))
					}
				}
				s = tr.State
			}
			Expect(s).To(BeAssignableToTypeOf(Succeeded{}))
		}
	})
})

type unknownEvent struct{}

func (unknownEvent) isEvent() {}

var _ = Describe("workflow regressions", Label("traces"), func() {
	It("InvalidEvent", func() {
		g := Default
		s, _ := Preflight(fixture(cover))
		for _, e := range []Event{nil, unknownEvent{}} {
			tr := Reduce(s, e)
			g.Expect(tr.State).To(Equal(s))
			g.Expect(tr.Effects).To(BeEmpty())
			g.Expect(tr.Rule).To(Equal("R9"))
			g.Expect(tr.Guard).NotTo(BeEmpty())
		}
	})
})

// Expected explanations are test-owned, not copied from transition output.
func expectedTraceGuard(w step) string {
	switch w.Rule {
	case "R7":
		return "stage failed; retain exact captured ABC"
	case "R8":
		return "cancellation precedes serialized success commit"
	case "R9":
		if _, ok := w.Event.(TaskCompleted); ok {
			if len(w.Effects) > 0 {
				return "late owned artifact; idempotent quarantine cleanup"
			}
			return "duplicate owned completion; result authority already consumed"
		}
		return "terminal duplicate or late acknowledgement"
	case "R10":
		return "live request; private audio and durable manifest committed"
	}
	switch w.Event.(type) {
	case Start, GenerationChecked:
		return "authorized preflight; immediate admission only"
	case Admitted:
		return "matching immediate admission granted"
	case TaskCompleted:
		return "result captured; quiescence and release still required"
	case TaskFailed:
		return "failure captured; wait for actual quiescence"
	case StageSettled:
		if w.Rule == "R6" {
			return "generation released; fetch and validate bounded audio"
		}
		return "quiescence and slot release acknowledged; clean source"
	case InputCleaned:
		if w.Rule == "R4" {
			return "released stage and source cleanup acknowledged"
		}
		return "first-stage resources gone; recheck generation facts"
	case OutputFetched:
		return "bounded audio validated; prepare private audio and manifest"
	case OutputPrepared:
		return "prepared only; serialized commit still required"
	}
	panic("missing expected trace guard")
}

var _ = Describe("issued-stage history traces", func() {
	It("cleans distinct prior-stage artifacts and ignores duplicate handles", Label("traces"), func() {
		ws := successSteps(cover)
		prefix := append([]step{}, ws[:6]...)
		e := TaskCompleted{identified(corr(transcribe), "run"), "not accepted", true, 2}
		prefix = append(prefix, step{e, "R9", "music.admitting", []string{"music.CleanArtifact"}}, step{e, "R9", "music.admitting", []string{}})
		prefix = append(prefix, ws[6:]...)
		Expect(runSteps(Default, cover, prefix)).To(BeAssignableToTypeOf(Succeeded{}))
	})
	It("cleans distinct duplicate artifacts while generation settles", Label("traces"), func() {
		ws := successSteps(generation)
		prefix := append([]step{}, ws[:3]...)
		e := TaskCompleted{identified(corr(generate), "run"), "not accepted", true, 2}
		prefix = append(prefix, step{e, "R9", "music.settling", []string{"music.CleanArtifact"}}, step{e, "R9", "music.settling", []string{}})
		prefix = append(prefix, ws[3:]...)
		Expect(runSteps(Default, generation, prefix)).To(BeAssignableToTypeOf(Succeeded{}))
	})
})
