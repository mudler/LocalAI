// SPDX-License-Identifier: MIT
package toolbroker

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Verification tooling stays in tests: the production model has no runtime I/O.
type TraceStep struct {
	Before, Event, After, RuleID, Guard string
	Effects                             []string
}

func NewTraceStep(before State, e Event, t Transition) TraceStep {
	fx := []string{}
	for _, v := range t.Effects {
		fx = append(fx, fmt.Sprintf("%T", v))
	}
	return TraceStep{Before: PhaseName(before.phase), Event: EventName(e), After: PhaseName(t.State.phase), RuleID: t.RuleID, Guard: t.Guard, Effects: fx}
}
func WriteTraceViewer(dir string, trace []TraceStep) error {
	data, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "trace.json"), data, 0600); err != nil {
		return err
	}
	page := `<!doctype html><meta charset="utf-8"><title>ToolBroker reducer trace</title><style>body{font:18px system-ui;max-width:900px;margin:40px auto}pre{white-space:pre-wrap}button{padding:8px}</style><h1>Actual Reduce trace</h1><p>Recorded pure reducer output; no reimplemented browser state machine. Authorization model only, not external execution safety.</p><button id="prev">Previous</button> <button id="next">Next</button><pre id="step"></pre><script type="application/json" id="trace">` + string(data) + `</script><script>const trace=JSON.parse(document.getElementById('trace').textContent);let i=0;function show(){document.getElementById('step').textContent='Step '+i+' of '+(trace.length-1)+'\n'+JSON.stringify(trace[i],null,2)}document.getElementById('prev').onclick=()=>{i=Math.max(0,i-1);show()};document.getElementById('next').onclick=()=>{i=Math.min(trace.length-1,i+1);show()};show()</script>`
	// JSON encoder escapes HTML-sensitive chars. No model/secret data is serialized.
	_ = html.EscapeString
	return os.WriteFile(filepath.Join(dir, "viewer.html"), []byte(page), 0600)
}

type ModelReport struct {
	Phases, Events                        []string
	Pairs, Visited, Edges, Configurations int
	Bound                                 string
}

func (r ModelReport) String() string {
	return fmt.Sprintf("BOUNDED MODEL: %s; configurations=%d visited=%d edges=%d phase/event witnesses=%d phases=%d events=%d; all states pass safety and reverse-reachable terminal/wait liveness (not unbounded proof)", r.Bound, r.Configurations, r.Visited, r.Edges, r.Pairs, len(r.Phases), len(r.Events))
}

func allEvents(b Binding, n int64) []Event {
	i := Input{Binding: b, Now: n}
	return []Event{Requested{i}, AdmissionPassed{Input: i, Evidence: Admission{true, true, true, true, true}}, AdmissionRejected{i}, ApprovalGranted{Input: i, Approval: Approval{Binding: b, Actor: HumanActor, ActorID: "human", ExpiresAt: 9}}, ApprovalDenied{i}, Expired{i}, Cancelled{i}, AccessRevoked{i}, LeaseLost{i}, AttemptReplaced{i}, ClaimRequested{i}, PreparedObserved{Input: i, Status: RecordPrepared}, DispatchGranted{Input: i, Fresh: true}, DispatchRejected{i}, InvokeRequested{i}, ExecutionStarted{i}, ExecutionCompleted{Input: i, Output: NewOutput([]byte(`{"ok":true}`))}, ExecutionUncertain{i}, JournalCompleted{i}, JournalFailed{i}, RecoveryObserved{Input: i, Status: RecordCompleted, Output: NewOutput([]byte(`{"ok":true}`))}}
}
func expandedEvents(b Binding, n int64) []Event {
	out := allEvents(b, n)
	i := Input{Binding: b, Now: n}
	for _, status := range []RecordStatus{RecordDispatched, RecordCompleted, RecordUnknown, RecordConflict} {
		out = append(out, PreparedObserved{Input: i, Status: status}, RecoveryObserved{Input: i, Status: status})
	}
	out = append(out, DispatchGranted{Input: i, Fresh: false})
	for k := 0; k < 5; k++ {
		a := Admission{true, true, true, true, true}
		switch k {
		case 0:
			a.Current = false
		case 1:
			a.Registered = false
		case 2:
			a.Selected = false
		case 3:
			a.Resource = false
		case 4:
			a.Helper = false
		}
		out = append(out, AdmissionPassed{Input: i, Evidence: a})
	}
	for _, bad := range BindingMismatchWitnesses(b) {
		out = append(out, ExecutionCompleted{Input: Input{Binding: bad, Now: n}}, ApprovalGranted{Input: i, Approval: Approval{Binding: bad, Actor: HumanActor, ActorID: "human", ExpiresAt: 9}})
	}
	out = append(out, ApprovalGranted{Input: i, Approval: Approval{Binding: b, Actor: HostActor, ActorID: "host", ExpiresAt: 9}}, ApprovalGranted{Input: i, Approval: Approval{Binding: b, Actor: HumanActor, ActorID: "human", ExpiresAt: 12}})
	return out
}

// Each finite configuration is explored to a fixed point, not just a depth
// cutoff. Time alphabet before/at/after expiry and immutable request variants
// are explicit bounds. Reverse graph search proves existence of a terminal or
// waiting path for every visited node, not fairness under arbitrary scheduling.
func CheckBoundedModel() (ModelReport, error) {
	r := ModelReport{Bound: "principals=2 conversations=2 requestIDs=2 attempts=2 epochs=2 policies=4 taint=2; times={1,9,10}; approvalTTL=8; approvalExpiry=9; all 16 binding-field mismatches"}
	phases, events, pairs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for principal := 0; principal < 2; principal++ {
		for conversation := 0; conversation < 2; conversation++ {
			for request := 0; request < 2; request++ {
				for attempt := 1; attempt <= 2; attempt++ {
					for epoch := 1; epoch <= 2; epoch++ {
						for policy := RequireApproval; policy <= AllowAll; policy++ {
							for taint := 0; taint < 2; taint++ {
								b, err := NewBinding(Scope{fmt.Sprint("p", principal), fmt.Sprint("c", conversation), "turn", uint64(attempt), uint64(epoch)}, fmt.Sprint("r", request), "tool", []byte("args"), Revisions{Catalog: "1", Config: "1", Authorization: "1", Resource: "1", Proposal: "1", ParentGrant: "1", Taint: "1"}, "provider")
								if err != nil {
									return r, err
								}
								c := ModelConfig{Binding: b, Policy: policy, ApprovalTTL: 8, ExternalTaintedWrite: taint == 1}
								r.Configurations++
								initial := NewState(c)
								queue := []State{initial}
								seen := map[string]int{stateKey(initial): 0}
								reverse := [][]int{{}}
								paths := [][]string{{}}
								// Precompute input alphabet once per immutable configuration.
								alphabet := []Event{}
								for _, n := range []int64{1, 9, 10} {
									alphabet = append(alphabet, expandedEvents(b, n)...)
								}
								for head := 0; head < len(queue); head++ {
									s := queue[head]
									if err = CheckInvariants(s); err != nil {
										return r, fmt.Errorf("bounded invariant: path %s", strings.Join(paths[head], ","))
									}
									phases[PhaseName(s.phase)] = true
									for _, e := range alphabet {
										events[EventName(e)] = true
										pairs[PhaseName(s.phase)+"/"+EventName(e)] = true
										t, err := Reduce(s, e)
										if err != nil {
											return r, fmt.Errorf("bounded reduction: %s + %s path %s: %w", stateKey(s), EventName(e), strings.Join(paths[head], ","), err)
										}
										if err = CheckTransition(s, e, t); err != nil {
											return r, err
										}
										r.Edges++
										key := stateKey(t.State)
										idx, ok := seen[key]
										if !ok {
											idx = len(queue)
											seen[key] = idx
											queue = append(queue, t.State)
											reverse = append(reverse, nil)
											path := append([]string{}, paths[head]...)
											paths = append(paths, append(path, EventName(e)+":"+t.RuleID))
										}
										reverse[idx] = append(reverse[idx], head)
									}
								}
								live := make([]bool, len(queue))
								front := []int{}
								for idx, s := range queue {
									switch s.phase.(type) {
									case Completed, Denied, Interrupted, Unknown, WaitingApproval:
										live[idx] = true
										front = append(front, idx)
									case New, Checking, Authorized, Claiming, Claimed, Executing:
									}
								}
								for head := 0; head < len(front); head++ {
									for _, prev := range reverse[front[head]] {
										if !live[prev] {
											live[prev] = true
											front = append(front, prev)
										}
									}
								}
								for idx, ok := range live {
									if !ok {
										return r, fmt.Errorf("bounded liveness: path %s", strings.Join(paths[idx], ","))
									}
								}
								r.Visited += len(queue)
							}
						}
					}
				}
			}
		}
	}
	for name := range phases {
		r.Phases = append(r.Phases, name)
	}
	for name := range events {
		r.Events = append(r.Events, name)
	}
	sort.Strings(r.Phases)
	sort.Strings(r.Events)
	r.Pairs = len(pairs)
	return r, nil
}
