// SPDX-License-Identifier: MIT
package store

import (
	"encoding/json"
	"fmt"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func fsFixture(n int) (fsState, fsCommand) {
	r := fsRow{Key: fsKey{"alice", "chat"}, Owner: "owner-a", Epoch: 1, Expires: 10, Authority: fsLive, Revision: 2,
		Active:     &Input{InputID: "active", Payload: []byte("secret-input"), Ordinal: 1},
		Checkpoint: Checkpoint{Context: []byte("secret-old")}, Events: []Event{{Sequence: 1, TurnID: "prior", Kind: "safe"}}}
	for i := 0; i < n; i++ {
		r.Pending = append(r.Pending, Input{InputID: fmt.Sprintf("queue-%d", i), Ordinal: uint64(i + 2), Payload: []byte("secret-queue")})
	}
	s := fsOwnState([]fsRow{r, {Key: fsKey{"bob", "chat"}, Owner: "owner-b", Epoch: 2, Expires: 10, Authority: fsLive}})
	e := fsOwnCommand(fsRequest{Key: r.Key, Owner: r.Owner, Epoch: 1, Now: 9, ExpectedRevision: 2, TurnID: "active", Mode: fsComplete, Transaction: fsCommit,
		Checkpoint: Checkpoint{Version: 1, Context: []byte("secret-new"), Artifacts: []byte("secret-artifacts"), Pruning: []byte("secret-pruning"), TranscriptCursor: []byte("secret-cursor")}, Events: []Event{{Sequence: 999, Kind: "safe", Payload: []byte("safe-event")}, {Kind: "checkpoint"}}})
	return s, e
}
func fsChangeCommand(e fsCommand, change func(*fsRequest)) fsCommand {
	f := e.fields()
	change(&f)
	return fsOwnCommand(f)
}
func fsChangeState(s fsState, change func([]fsRow)) fsState {
	f := s.fields()
	change(f)
	return fsOwnState(f)
}
func fsStep(s fsState, e fsCommand, rule string) fsDecision {
	d := fsReduce(s, e)
	Expect(d.fields().Rule).To(Equal(rule))
	Expect(fsCheckTransition(s, e, d)).To(Succeed())
	return d
}

var _ = Describe("Fenced settlement decision model", func() {
	It("commits checkpoint events revision settlement and oldest FIFO claim together", func() {
		s, e := fsFixture(2)
		d := fsStep(s, e, "FS101").fields()
		r := d.State.fields()[0]
		Expect(r.Revision).To(Equal(uint64(3)))
		Expect(r.Settled).To(Equal([]string{"active"}))
		Expect(r.Active.InputID).To(Equal("queue-0"))
		Expect(r.Pending[0].InputID).To(Equal("queue-1"))
		Expect(r.Checkpoint).To(Equal(e.fields().Checkpoint))
		Expect(r.Events[1].Sequence).To(Equal(uint64(2)))
		Expect(r.Events[2].Sequence).To(Equal(uint64(3)))
		Expect(r.Events[1].TurnID).To(Equal("active"))
		Expect(d.Result.Next.InputID).To(Equal("queue-0"))
	})
	It("completes an empty FIFO without inventing a handoff", func() {
		s, e := fsFixture(0)
		d := fsStep(s, e, "FS101").fields()
		Expect(d.Result.Next).To(BeNil())
		Expect(d.State.fields()[0].Active).To(BeNil())
	})
	It("holds the running input and entire FIFO while committing checkpoint and events", func() {
		s, e := fsFixture(2)
		e = fsChangeCommand(e, func(f *fsRequest) { f.Mode = fsHold })
		d := fsStep(s, e, "FS102").fields()
		Expect(d.Result.Next).To(BeNil())
		Expect(d.State.fields()[0].Active).To(Equal(s.fields()[0].Active))
		Expect(d.State.fields()[0].Pending).To(Equal(s.fields()[0].Pending))
		Expect(d.State.fields()[0].Settled).To(BeEmpty())
		Expect(d.State.fields()[0].Revision).To(Equal(uint64(3)))
	})
	It("rolls back the complete candidate without any observable write", func() {
		s, e := fsFixture(2)
		e = fsChangeCommand(e, func(f *fsRequest) { f.Transaction = fsRollback })
		d := fsStep(s, e, "FS090").fields()
		Expect(fsCanonicalEqual(s, d.State)).To(BeTrue())
		Expect(d.Result).To(BeNil())
	})
	It("rolls back a hold mutation failure without publishing a result", func() {
		s, e := fsFixture(2)
		e = fsChangeCommand(e, func(f *fsRequest) { f.Mode = fsHold; f.Transaction = fsMutationFailed })
		d := fsStep(s, e, "FS090").fields()
		Expect(fsCanonicalEqual(s, d.State)).To(BeTrue())
		Expect(d.Result).To(BeNil())
	})
	It("rejects exact persisted expiry and allows persisted renewal", func() {
		s, e := fsFixture(1)
		e = fsChangeCommand(e, func(f *fsRequest) { f.Now = 10 })
		fsStep(s, e, "FS015")
		s = fsChangeState(s, func(r []fsRow) { r[0].Expires = 11 })
		fsStep(s, e, "FS101")
	})
	It("rejects expired released and stolen authority", func() {
		for _, status := range []fsAuthority{fsExpired, fsReleased, fsStolen} {
			s, e := fsFixture(1)
			s = fsChangeState(s, func(r []fsRow) { r[0].Authority = status })
			fsStep(s, e, "FS012")
		}
	})
	It("rejects wrong owner and wrong epoch independently", func() {
		s, e := fsFixture(1)
		fsStep(s, fsChangeCommand(e, func(f *fsRequest) { f.Owner = "owner-b" }), "FS013")
		fsStep(s, fsChangeCommand(e, func(f *fsRequest) { f.Epoch = 2 }), "FS014")
	})
	It("scopes lookup to the full lease key rather than request payload", func() {
		s, e := fsFixture(1)
		fsStep(s, fsChangeCommand(e, func(f *fsRequest) { f.Key = fsKey{"alice", "other"} }), "FS010")
		d := fsStep(s, e, "FS101")
		Expect(d.fields().State.fields()[1]).To(Equal(s.fields()[1]))
	})
	It("rejects tombstones stale revisions and stale turns", func() {
		s, e := fsFixture(1)
		fsStep(fsChangeState(s, func(r []fsRow) { r[0].Deleted = true }), e, "FS016")
		fsStep(s, fsChangeCommand(e, func(f *fsRequest) { f.ExpectedRevision-- }), "FS017")
		fsStep(s, fsChangeCommand(e, func(f *fsRequest) { f.TurnID = "old" }), "FS018")
	})
	It("requires an existing unsettled active input", func() {
		s, e := fsFixture(0)
		fsStep(fsChangeState(s, func(r []fsRow) { r[0].Active = nil }), e, "FS018")
		Expect(fsCheckState(fsChangeState(s, func(r []fsRow) { r[0].Settled = []string{"active"} }))).NotTo(Succeed())
	})
	It("rejects zero unknown modes and invalid transaction observations", func() {
		s, e := fsFixture(1)
		for _, mode := range []fsMode{0, 99} {
			fsStep(s, fsChangeCommand(e, func(f *fsRequest) { f.Mode = mode }), "FS001")
		}
		fsStep(s, fsChangeCommand(e, func(f *fsRequest) { f.Transaction = 99 }), "FS001")
	})
	It("rejects numeric exhaustion and mismatched event turn", func() {
		s, e := fsFixture(1)
		s = fsChangeState(s, func(r []fsRow) { r[0].Revision = fsMax })
		e = fsChangeCommand(e, func(f *fsRequest) { f.ExpectedRevision = fsMax })
		fsStep(s, e, "FS019")
		s, e = fsFixture(1)
		fsStep(s, fsChangeCommand(e, func(f *fsRequest) { f.Events[0].TurnID = "wrong" }), "FS001")
	})
	It("owns constructor inputs accessor outputs and every returned buffer", func() {
		s, e := fsFixture(2)
		before := s.clone()
		request := e.clone()
		raw := s.fields()
		raw[0].Active.Payload[0] = 'X'
		raw[0].Pending[0].Payload[0] = 'X'
		raw[0].Checkpoint.Context[0] = 'X'
		raw[0].Events[0].Kind = "changed"
		q := e.fields()
		q.Checkpoint.Context[0] = 'X'
		q.Events[0].Payload[0] = 'X'
		Expect(fsCanonicalEqual(s, before)).To(BeTrue())
		Expect(e.fields()).To(Equal(request.fields()))
		d := fsStep(s, e, "FS101")
		out := d.fields()
		out.State.fields()[0].Active.Payload[0] = 'X'
		out.Result.Next.Payload[0] = 'X'
		Expect(fsCheckTransition(s, e, d)).To(Succeed())
	})
	It("copies the original constructor snapshots before callers mutate them", func() {
		s, e := fsFixture(2)
		rows := s.fields()
		req := e.fields()
		owned := fsOwnState(rows)
		command := fsOwnCommand(req)
		rows[0].Active.Payload[0] = 'X'
		rows[0].Active.PayloadHash = []byte("changed")
		rows[0].Pending[0].Payload[0] = 'X'
		rows[0].Checkpoint.Context[0] = 'X'
		rows[0].Events[0].TurnID = "changed"
		req.Checkpoint.Context[0] = 'X'
		req.Events[0].Payload[0] = 'X'
		Expect(fsCanonicalEqual(s, owned)).To(BeTrue())
		Expect(command.fields()).To(Equal(e.fields()))
	})
	It("rejects malformed snapshots and independently checks disjoint ordered records", func() {
		s, e := fsFixture(2)
		for _, change := range []func([]fsRow){func(r []fsRow) { r[1].Key = r[0].Key }, func(r []fsRow) { r[0].Pending[0].InputID = "active" }, func(r []fsRow) { r[0].Pending[1].Ordinal = 1 }, func(r []fsRow) { r[0].Events[0].Sequence = 2 }, func(r []fsRow) { r[0].Authority = 0 }, func(r []fsRow) { r[0].Settled = []string{"active"} }} {
			bad := fsChangeState(s, change)
			Expect(fsCheckState(bad)).NotTo(Succeed())
			fsStep(bad, e, "FS002")
		}
	})

	It("preserves nil versus empty buffers and handles zero owned values safely", func() {
		Expect(fsState{}.fields()).To(BeNil())
		Expect(fsCommand{}.fields()).To(Equal(fsRequest{}))
		fsStep(fsState{}, fsCommand{}, "FS001")
		s, e := fsFixture(0)
		s = fsChangeState(s, func(r []fsRow) { r[0].Checkpoint.Context = []byte{}; r[0].Pending = []Input{} })
		e = fsChangeCommand(e, func(f *fsRequest) { f.Transaction = fsRollback })
		d := fsStep(s, e, "FS090")
		Expect(fsCanonicalEqual(s, d.fields().State)).To(BeTrue())
		Expect(d.fields().State.fields()[0].Checkpoint.Context).NotTo(BeNil())
		Expect(d.fields().State.fields()[0].Pending).NotTo(BeNil())
	})
	It("redacts direct nested invalid-verb JSON and wrapped-error diagnostics", func() {
		s, e := fsFixture(1)
		d := fsReduce(s, e)
		for _, v := range []any{s, e, d, struct {
			state    fsState
			command  fsCommand
			decision fsDecision
		}{s, e, d}} {
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d", "%f"} {
				blob, err := json.Marshal(v)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(blob)).NotTo(ContainSubstring("secret"))
				text := fmt.Sprintf(verb, v)
				Expect(text).NotTo(ContainSubstring("secret"))
				Expect(text).NotTo(ContainSubstring("alice"))
				Expect(fmt.Sprintf(verb, fmt.Errorf("outer: %w", fsDiagnostic{s}))).NotTo(ContainSubstring("secret"))
			}
			Expect(fmt.Sprintf("%+v", fmt.Errorf("outer: %w", fsDiagnostic{s}))).NotTo(ContainSubstring("secret"))
		}
	})
	It("rejects forged success rejection rules results and partial writes", func() {
		s, e := fsFixture(2)
		good := fsReduce(s, e).fields()
		for _, change := range []func(*fsOutcome){func(f *fsOutcome) { f.Rule = "FS013" }, func(f *fsOutcome) { f.State = s }, func(f *fsOutcome) { f.Result = nil }, func(f *fsOutcome) { f.State = fsChangeState(f.State, func(r []fsRow) { r[1].Revision++ }) }, func(f *fsOutcome) { f.Result.Next = nil }, func(f *fsOutcome) { f.State = fsChangeState(f.State, func(r []fsRow) { r[0].Events[1].Sequence = 99 }) }} {
			f := good
			f.State = f.State.clone()
			f.Result = fsCloneResult(f.Result)
			change(&f)
			Expect(fsCheckTransition(s, e, fsOwnDecision(f))).NotTo(Succeed())
		}
		bad := fsChangeCommand(e, func(f *fsRequest) { f.Owner = "other" })
		Expect(fsCheckTransition(s, bad, fsOwnDecision(good))).NotTo(Succeed())
	})
	It("traces rollback retry hold complete duplicate and FIFO in order", func() {
		s, e := fsFixture(2)
		rules := []string{}
		step := func(x fsCommand) {
			d := fsReduce(s, x)
			Expect(fsCheckTransition(s, x, d)).To(Succeed())
			rules = append(rules, d.fields().Rule)
			s = d.fields().State
		}
		step(fsChangeCommand(e, func(f *fsRequest) { f.Transaction = fsRollback }))
		step(fsChangeCommand(e, func(f *fsRequest) { f.Mode = fsHold }))
		step(fsChangeCommand(e, func(f *fsRequest) { f.ExpectedRevision = 3 }))
		step(e)
		Expect(rules).To(Equal([]string{"FS090", "FS102", "FS101", "FS017"}))
		Expect(s.fields()[0].Active.InputID).To(Equal("queue-0"))
		Expect(s.fields()[0].Revision).To(Equal(uint64(4)))
	})
	It("traces takeover and release rejecting the old holder without a queue claim", func() {
		s, e := fsFixture(1)
		rules := []string{}
		s = fsChangeState(s, func(r []fsRow) { r[0].Owner = "owner-b"; r[0].Epoch = 2 })
		for _, x := range []fsCommand{e, fsChangeCommand(e, func(f *fsRequest) { f.Owner = "owner-b" }), fsChangeCommand(e, func(f *fsRequest) { f.Owner = "owner-b"; f.Epoch = 2 })} {
			d := fsReduce(s, x)
			Expect(fsCheckTransition(s, x, d)).To(Succeed())
			rules = append(rules, d.fields().Rule)
		}
		s = fsChangeState(s, func(r []fsRow) { r[0].Authority = fsReleased })
		d := fsStep(s, e, "FS012")
		Expect(fsCanonicalEqual(s, d.fields().State)).To(BeTrue())
		Expect(rules).To(Equal([]string{"FS013", "FS014", "FS101"}))
	})
	It("checks a finite cheap frontier and separate guard witness matrices", func() { fsBoundedCheck() })
})

type fsDiagnostic struct{ s fsState }

func (fsDiagnostic) Error() string { return "settlement model rejected" }

// Bounds: 24 independent seeds (two keys, two owners, epochs 1-2,
// FIFO 0-2), depth three; two modes and commit/rollback edges. Hold cycles are
// bounded by depth, NOT silently pruned as if they proved eventual completion.
// Counters start at revision two; at most three commits/six new events per
// prefix. Separate one-axis witness matrices cover guards without multiplying
// every stale/authority/error combination into the frontier. This establishes
// safety and existence of terminal/waiting successors, not fairness or SQL.
func fsBoundedCheck() {
	seeds, edges, prefixes, witnesses := 0, 0, 0, 0
	rules := map[string]int{}
	var walk func(fsState, fsCommand, int)
	walk = func(s fsState, e fsCommand, depth int) {
		prefixes++
		Expect(fsCheckState(s)).To(Succeed())
		if depth == 0 {
			return
		}
		for _, mode := range []fsMode{fsComplete, fsHold} {
			for _, tx := range []fsTransaction{fsCommit, fsRollback} {
				x := fsChangeCommand(e, func(f *fsRequest) { f.Mode = mode; f.Transaction = tx })
				d := fsReduce(s, x)
				Expect(fsCheckTransition(s, x, d)).To(Succeed())
				edges++
				rules[d.fields().Rule]++
				next := d.fields().State
				// Existential progress: Complete consumes exactly one active
				// input; Hold is an explicit waiting outcome, never a handoff.
				if tx == fsCommit {
					if mode == fsComplete {
						Expect(len(next.fields()[0].Settled)).To(Equal(len(s.fields()[0].Settled) + 1))
					} else {
						Expect(d.fields().Result.Next).To(BeNil())
					}
				}
				r := next.fields()[0]
				if r.Active != nil {
					fresh := fsChangeCommand(e, func(f *fsRequest) { f.ExpectedRevision = r.Revision; f.TurnID = r.Active.InputID })
					walk(next, fresh, depth-1)
				}
			}
		}
	}
	for key := 0; key < 2; key++ {
		for owner := 0; owner < 2; owner++ {
			for epoch := uint64(1); epoch <= 2; epoch++ {
				for n := 0; n <= 2; n++ {
					s, e := fsFixture(n)
					s = fsChangeState(s, func(r []fsRow) {
						if key == 1 {
							r[0].Key.Conversation = "other"
						}
						if owner == 1 {
							r[0].Owner = "owner-b"
						}
						r[0].Epoch = epoch
					})
					r := s.fields()[0]
					e = fsChangeCommand(e, func(f *fsRequest) { f.Key = r.Key; f.Owner = r.Owner; f.Epoch = epoch })
					seeds++
					walk(s, e, 3)
					// Every status, stale/current revision and turn, both modes and both
					// transaction outcomes are witnessed separately from the depth frontier.
					for _, mode := range []fsMode{fsComplete, fsHold} {
						for _, tx := range []fsTransaction{fsCommit, fsRollback} {
							base := fsChangeCommand(e, func(f *fsRequest) { f.Mode = mode; f.Transaction = tx })
							cases := []struct {
								s    fsState
								e    fsCommand
								rule string
							}{
								{s, base, map[bool]string{true: "FS090", false: map[fsMode]string{fsComplete: "FS101", fsHold: "FS102"}[mode]}[tx == fsRollback]},
								{s, fsChangeCommand(base, func(f *fsRequest) { f.ExpectedRevision-- }), "FS017"},
								{s, fsChangeCommand(base, func(f *fsRequest) { f.TurnID = "stale" }), "FS018"},
								{s, fsChangeCommand(base, func(f *fsRequest) { f.Owner = "wrong" }), "FS013"},
								{s, fsChangeCommand(base, func(f *fsRequest) { f.Epoch = 3 - epoch }), "FS014"},
								{s, fsChangeCommand(base, func(f *fsRequest) { f.Now = 10 }), "FS015"},
								{s, fsChangeCommand(base, func(f *fsRequest) { f.Key.Conversation = "missing" }), "FS010"},
								{fsChangeState(s, func(r []fsRow) { r[0].Deleted = true }), base, "FS016"},
								{fsChangeState(s, func(r []fsRow) { r[0].Active = nil }), base, "FS018"},
							}
							for _, status := range []fsAuthority{fsExpired, fsReleased, fsStolen} {
								cases = append(cases, struct {
									s    fsState
									e    fsCommand
									rule string
								}{fsChangeState(s, func(r []fsRow) { r[0].Authority = status }), base, "FS012"})
							}
							for _, c := range cases {
								d := fsStep(c.s, c.e, c.rule)
								witnesses++
								rules[c.rule]++
								if d.fields().Result == nil {
									Expect(fsCanonicalEqual(c.s, d.fields().State)).To(BeTrue())
								}
							}
						}
					}
				}
			}
		}
	}
	Expect(seeds).To(Equal(24))
	Expect(witnesses).To(Equal(1152))
	Expect(edges).To(Equal(1728))
	Expect(prefixes).To(Equal(1584))
	for _, rule := range []string{"FS010", "FS012", "FS013", "FS014", "FS015", "FS016", "FS017", "FS018", "FS090", "FS101", "FS102"} {
		Expect(rules[rule]).To(BeNumerically(">", 0))
	}
	// Canonical comparison must accept key order only, not reorder the FIFO.
	s, _ := fsFixture(2)
	rows := s.fields()
	rows[0], rows[1] = rows[1], rows[0]
	Expect(fsCanonicalEqual(s, fsOwnState(rows))).To(BeTrue())
	rows = s.fields()
	rows[0].Pending[0], rows[0].Pending[1] = rows[0].Pending[1], rows[0].Pending[0]
	Expect(fsCanonicalEqual(s, fsOwnState(rows))).To(BeFalse())
	Expect(reflect.DeepEqual(s.fields(), rows)).To(BeFalse())
	fmt.Fprintf(GinkgoWriter, "FS bounds seeds=%d prefixes=%d edges=%d guard-witnesses=%d depth=3 queue=0..2 owners=2 keys=2 epochs=1..2 rules=%v\n", seeds, prefixes, edges, witnesses, rules)
}
