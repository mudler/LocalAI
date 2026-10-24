// SPDX-License-Identifier: MIT
package executionbridge

import (
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"strings"
)

func fixture() (Call, Facts) {
	c := ownCall(callFields{Principal: "secret-principal", Conversation: "secret-conversation", Turn: "secret-turn", Attempt: 1, Epoch: 1, Invocation: "invocation-1", Protocol: "protocol-1", Helper: "root", Tool: "tool-1", Binding: [32]byte{1}, Provenance: [32]byte{2}, Projection: [32]byte{3}, Revision: 1, AuthRevision: 1, ConfigRevision: 1, Codec: 1, TTL: 30, Mode: 1})
	return c, Facts{Lease: Live, RevisionCurrent: true, TurnCurrent: true, Registered: true, Authorized: true, Durable: true, Normal: true, Projected: true, ReleaseAuthorized: true, OriginAuthorized: true, ExactOrigin: true, OriginCompleted: true, HelperScope: c, RegisteredHelper: "root"}
}
func ev(k EventKind, c Call, f Facts) Event { return NewEvent(k, c, f) }
func step(s State, k EventKind, c Call, f Facts, rule string) Transition {
	e := ev(k, c, f)
	r := Reduce(s, e)
	Expect(r.Rule).To(Equal(rule))
	Expect(Invariant(r.State)).To(Succeed())
	Expect(CheckTransition(s, e, r)).To(Succeed())
	return r
}
func ready(c Call, f Facts) State {
	r := step(Unmapped{}, Admit, c, f, "HB03")
	return step(r.State, Begin, c, f, "HB07").State
}
func executing(c Call, f Facts) State {
	s := ready(c, f)
	s = step(s, Claim, c, f, "HB10").State
	return step(s, Dispatch, c, f, "HB12").State
}

var _ = Describe("Pure host execution contract", func() {
	It("accepts exactly broker policy modes without granting authority or changing identity", func() {
		c, f := fixture()
		for _, mode := range []uint8{0, 1, 2, 3} {
			x := c.fields()
			x.Mode = mode
			call := ownCall(x)
			Expect(call.valid()).To(BeTrue())
			id, err := RequestID(call)
			Expect(err).NotTo(HaveOccurred())
			Expect(id).To(Equal("he1_7690876db51234c4939b27bcfab79d86d86744ac3b53e4747814cb28ca02737e"))
			s := step(Unmapped{}, Admit, call, f, "HB03").State
			Expect(s.payload().fields()).To(Equal(x))
			for _, other := range []uint8{0, 1, 2, 3} {
				if other == mode {
					continue
				}
				y := x
				y.Mode = other
				r := step(s, Admit, ownCall(y), f, "HB05")
				Expect(r.State.payload().fields()).To(Equal(x))
				Expect(r.Effects).To(BeEmpty())
			}
			g := f
			g.Authorized = false
			step(Unmapped{}, Admit, call, g, "HB06")
			g = f
			g.NeedsApproval = true
			step(s, Begin, call, g, "HB08")
		}
		for mode := 4; mode <= 255; mode++ {
			x := c.fields()
			x.Mode = uint8(mode)
			call := ownCall(x)
			Expect(call.valid()).To(BeFalse())
			Expect(Invariant(Claimed{call})).To(HaveOccurred())
			Expect(step(Unmapped{}, Admit, call, f, "HB06").Effects).To(BeEmpty())
		}
	})
	It("releases recorded completion after ordered admission begin and claim without native execution", func() {
		c, f := fixture()
		f.Origin = c
		var s State = Unmapped{}
		var effects []Effect
		for _, edge := range []struct {
			kind EventKind
			rule string
		}{{Admit, "HB03"}, {Begin, "HB07"}, {Claim, "HB10"}, {Replay, "HB23"}} {
			r := step(s, edge.kind, c, f, edge.rule)
			s = r.State
			effects = append(effects, r.Effects...)
		}
		Expect(s).To(BeAssignableToTypeOf(Completed{}))
		Expect(effects).To(Equal([]Effect{PersistAdmission, BeginBroker, ClaimCall, ReleaseReplay}))
		for _, k := range []EventKind{Dispatch, Finish, Replay, Claim} {
			Expect(step(s, k, c, f, "HB21").Effects).To(BeEmpty())
		}
	})
	It("holds recorded replay on every missing authority or origin guard and rejects forged release", func() {
		c, f := fixture()
		f.Origin = c
		s := step(ready(c, f), Claim, c, f, "HB10").State
		for _, change := range []func(*Facts){
			func(x *Facts) { x.Lease = Expired }, func(x *Facts) { x.RevisionCurrent = false },
			func(x *Facts) { x.TurnCurrent = false }, func(x *Facts) { x.Registered = false },
			func(x *Facts) { x.Authorized = false }, func(x *Facts) { x.HelperScope = Call{} },
			func(x *Facts) { x.RegisteredHelper = "forged" }, func(x *Facts) { x.Origin = Call{} },
			func(x *Facts) { y := c.fields(); y.Turn = "wrong"; x.Origin = ownCall(y) },
			func(x *Facts) { x.ExactOrigin = false }, func(x *Facts) { x.OriginCompleted = false },
			func(x *Facts) { x.OriginAuthorized = false }, func(x *Facts) { x.ReleaseAuthorized = false },
		} {
			g := f
			change(&g)
			r := step(s, Replay, c, g, "HB24")
			Expect(r.State).To(BeAssignableToTypeOf(Held{}))
			Expect(r.Effects).To(BeEmpty())
			Expect(step(r.State, Replay, c, f, "HB20").Effects).To(BeEmpty())
			for _, rule := range []string{"HB23", "HB15", "HB13"} {
				Expect(CheckTransition(s, ev(Replay, c, g), result(Completed{c}, rule, ReleaseReplay))).To(HaveOccurred())
			}
			Expect(CheckTransition(s, ev(Replay, c, g), result(s, "HB00"))).To(HaveOccurred())
		}
	})
	It("checks recorded replay certificates independently without Ready or Executing shortcuts", func() {
		c, f := fixture()
		f.Origin = c
		s := Claimed{c}
		e := ev(Replay, c, f)
		Expect(CheckTransition(s, e, result(Completed{c}, "HB23", ReleaseReplay))).To(Succeed())
		Expect(CheckTransition(s, e, result(Held{c}, "HB24"))).To(HaveOccurred())
		for _, effects := range [][]Effect{nil, {Execute}, {ClaimCall}, {Publish}, {ReleaseReplay, Execute}} {
			Expect(CheckTransition(s, e, result(Completed{c}, "HB23", effects...))).To(HaveOccurred())
		}
		for _, before := range []State{Ready{c}, Executing{c}} {
			Expect(step(before, Replay, c, f, "HB00").Effects).To(BeEmpty())
			Expect(CheckTransition(before, e, result(Completed{c}, "HB23", ReleaseReplay))).To(HaveOccurred())
		}
		x := c.fields()
		x.Attempt++
		stale := ev(Replay, ownCall(x), f)
		Expect(Reduce(s, stale).Rule).To(Equal("HB02"))
		Expect(CheckTransition(s, stale, result(Completed{ownCall(x)}, "HB23", ReleaseReplay))).To(HaveOccurred())
		x = c.fields()
		x.Mode = 3
		step(s, Replay, ownCall(x), f, "HB05")
	})
	It("completes only after normal projected durable completion", func() {
		c, f := fixture()
		s := executing(c, f)
		r := step(s, Finish, c, f, "HB13")
		Expect(r.State).To(BeAssignableToTypeOf(Completed{}))
		Expect(r.Effects).To(Equal([]Effect{Publish}))
	})
	It("does not durably admit preliminary policy modifications", func() {
		c, f := fixture()
		r := step(Unmapped{}, Candidate, c, f, "HB01")
		Expect(r.State).To(Equal(Unmapped{}))
		Expect(r.Effects).To(BeEmpty())
	})
	It("converges identical concurrent admission without another write", func() {
		c, f := fixture()
		r := step(Unmapped{}, Admit, c, f, "HB03")
		d := step(r.State, Admit, c, f, "HB04")
		Expect(d.Effects).To(BeEmpty())
		Expect(d.State.payload().equal(c)).To(BeTrue())
	})
	It("holds immutable admission conflicts", func() {
		c, f := fixture()
		s := step(Unmapped{}, Admit, c, f, "HB03").State
		x := c.fields()
		x.Protocol = "changed"
		step(s, Admit, ownCall(x), f, "HB05")
	})
	It("distinguishes equal arguments with regenerated invocation IDs", func() {
		c, _ := fixture()
		x := c.fields()
		x.Invocation = "invocation-2"
		d := ownCall(x)
		a, _ := RequestID(c)
		b, _ := RequestID(d)
		Expect(a).NotTo(Equal(b))
	})
	It("hashes the canonical versioned origin tuple to 68 bytes", func() {
		c, _ := fixture()
		id, err := RequestID(c)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(HaveLen(68))
		Expect(id).To(HavePrefix("he1_"))
		// Independently verified against literal typed JSON bytes with Python's
		// standard-library json/hashlib oracle (103 bytes; numeric 1,1).
		Expect(id).To(Equal("he1_7690876db51234c4939b27bcfab79d86d86744ac3b53e4747814cb28ca02737e"))
	})
	It("does not include immutable binding metadata in correlation IDs", func() {
		c, _ := fixture()
		x := c.fields()
		x.Protocol = "another"
		x.Helper = "helper"
		x.Binding[0] = 7
		x.Revision = 2
		a, _ := RequestID(c)
		b, _ := RequestID(ownCall(x))
		Expect(a).To(Equal(b))
	})
	It("rejects invalid identifier and numeric domains", func() {
		c, _ := fixture()
		for _, change := range []func(*callFields){func(x *callFields) { x.Principal = "" }, func(x *callFields) { x.Invocation = "\xff" }, func(x *callFields) { x.Turn = "a\x00b" }, func(x *callFields) { x.Attempt = 0 }, func(x *callFields) { x.Epoch = 0 }, func(x *callFields) { x.Attempt = 1 << 63 }} {
			x := c.fields()
			change(&x)
			_, err := RequestID(ownCall(x))
			Expect(err).To(HaveOccurred())
		}
	})
	It("rejects stale and forged helper scopes", func() {
		c, f := fixture()
		for _, change := range []func(*Facts){func(x *Facts) { x.Registered = false }, func(x *Facts) { x.RegisteredHelper = "forged" }, func(x *Facts) { y := c.fields(); y.Attempt = 2; x.HelperScope = ownCall(y) }} {
			g := f
			change(&g)
			r := step(Unmapped{}, Admit, c, g, "HB06")
			Expect(r.State).To(BeAssignableToTypeOf(Denied{}))
		}
	})
	It("claims only after durable admission", func() {
		c, f := fixture()
		step(Unmapped{}, Claim, c, f, "HB00")
		f.Durable = false
		step(Unmapped{}, Admit, c, f, "HB06")
	})
	It("rejects all noncurrent lease and store authority before claim", func() {
		c, f := fixture()
		for _, lease := range []LeaseState{Expired, Released, Stolen, Missing} {
			g := f
			g.Lease = lease
			r := step(ready(c, f), Claim, c, g, "HB11")
			Expect(r.State).To(BeAssignableToTypeOf(Held{}))
		}
		for _, revision := range []bool{false, true} {
			g := f
			g.RevisionCurrent = revision
			g.TurnCurrent = !revision
			step(ready(c, f), Claim, c, g, "HB11")
		}
	})
	It("waits for authenticated approval before claiming", func() {
		c, f := fixture()
		s := step(Unmapped{}, Admit, c, f, "HB03").State
		f.NeedsApproval = true
		s = step(s, Begin, c, f, "HB08").State
		step(s, Claim, c, f, "HB00")
		s = step(s, Approve, c, f, "HB09").State
		step(s, Claim, c, f, "HB10")
	})
	It("closes expired waiting approval with no later body", func() {
		c, f := fixture()
		s := step(Unmapped{}, Admit, c, f, "HB03").State
		f.NeedsApproval = true
		s = step(s, Begin, c, f, "HB08").State
		s = step(s, Expire, c, f, "HB18").State
		for _, k := range []EventKind{Approve, Claim, Dispatch, Finish} {
			r := step(s, k, c, f, "HB19")
			Expect(r.Effects).To(BeEmpty())
		}
	})
	It("denies unauthenticated approval", func() {
		c, f := fixture()
		s := step(Unmapped{}, Admit, c, f, "HB03").State
		f.NeedsApproval = true
		s = step(s, Begin, c, f, "HB08").State
		f.Authorized = false
		step(s, Approve, c, f, "HB06")
	})
	It("atomically blocks OTHER dispatched and unknown including legacy", func() {
		c, f := fixture()
		for _, j := range []JournalState{OtherDispatched, OtherUnknown, LegacyDispatched, LegacyUnknown} {
			g := f
			g.Other = j
			r := step(ready(c, f), Claim, c, g, "HB11")
			Expect(r.Effects).To(BeEmpty())
		}
	})
	It("allows resolved or absent other records", func() {
		c, f := fixture()
		for _, j := range []JournalState{NoOther, OtherPrepared, OtherCompleted, OtherDenied} {
			f.Other = j
			step(ready(c, f), Claim, c, f, "HB10")
		}
	})
	It("rechecks authority immediately before native continuation", func() {
		c, f := fixture()
		s := step(ready(c, f), Claim, c, f, "HB10").State
		f.Lease = Expired
		r := step(s, Dispatch, c, f, "HB11")
		Expect(r.Effects).To(BeEmpty())
	})
	It("holds dispatched cancellation without retry or success", func() {
		c, f := fixture()
		s := step(executing(c, f), Cancel, c, f, "HB17").State
		for _, k := range []EventKind{Admit, Claim, Dispatch, Finish, Replay} {
			r := step(s, k, c, f, "HB20")
			Expect(r.Effects).To(BeEmpty())
		}
	})
	It("holds panic projection and journal failure after dispatch", func() {
		c, f := fixture()
		for _, change := range []func(*Facts){func(x *Facts) { x.Normal = false }, func(x *Facts) { x.Projected = false }, func(x *Facts) { x.Durable = false }, func(x *Facts) { x.ReleaseAuthorized = false }, func(x *Facts) { x.Lease = Expired }} {
			g := f
			change(&g)
			r := step(executing(c, f), Finish, c, g, "HB14")
			Expect(r.State).To(BeAssignableToTypeOf(Held{}))
			Expect(r.Effects).To(BeEmpty())
		}
	})
	It("completed duplicate never claims or invokes another body", func() {
		c, f := fixture()
		s := step(executing(c, f), Finish, c, f, "HB13").State
		for _, k := range []EventKind{Admit, Claim, Dispatch, Finish} {
			r := step(s, k, c, f, "HB21")
			Expect(r.Effects).To(BeEmpty())
		}
	})
	It("replays exact authorized completed origin with zero body", func() {
		c, f := fixture()
		x := c.fields()
		x.Attempt = 2
		x.Epoch = 2
		f.Origin = c // Exact immutable origin admission; not current authority.
		target := ownCall(x)
		f.HelperScope = target // Registry scope belongs to the current target.
		r := step(Unmapped{}, Replay, target, f, "HB15")
		Expect(f.Origin.equal(c)).To(BeTrue())
		Expect(f.Origin.fields().Binding).To(Equal(target.fields().Binding))
		Expect(r.State).To(BeAssignableToTypeOf(Completed{}))
		Expect(r.Effects).To(Equal([]Effect{ReleaseReplay}))
	})
	It("rejects forged missing noncompleted and unauthorized replay origin", func() {
		c, f := fixture()
		f.Origin = c
		for _, change := range []func(*Facts){func(x *Facts) { x.ExactOrigin = false }, func(x *Facts) { x.OriginCompleted = false }, func(x *Facts) { x.OriginAuthorized = false }, func(x *Facts) { x.ReleaseAuthorized = false }, func(x *Facts) { y := c.fields(); y.Turn = "other-turn"; x.Origin = ownCall(y) }, func(x *Facts) { x.Lease = Stolen }} {
			g := f
			change(&g)
			r := step(Unmapped{}, Replay, c, g, "HB16")
			Expect(r.State).To(BeAssignableToTypeOf(Held{}))
			Expect(r.Effects).To(BeEmpty())
		}
	})
	It("ignores late callback identity from another attempt", func() {
		c, f := fixture()
		x := c.fields()
		x.Attempt = 2
		r := step(executing(c, f), Finish, ownCall(x), f, "HB02")
		Expect(r.State).To(BeAssignableToTypeOf(Executing{}))
		Expect(r.Effects).To(BeEmpty())
	})
	It("never revives closed interception", func() {
		c, f := fixture()
		s := step(executing(c, f), Close, c, f, "HB18").State
		for k := Candidate; k <= Close; k++ {
			r := step(s, k, c, f, "HB19")
			Expect(r.State).To(BeAssignableToTypeOf(Closed{}))
			Expect(r.Effects).To(BeEmpty())
		}
	})
	It("owns all event and state metadata", func() {
		c, f := fixture()
		e := ev(Admit, c, f)
		x := c.fields()
		x.Binding[0] = 99
		f.HelperScope = Call{}
		r := Reduce(Unmapped{}, e)
		Expect(r.State.payload().fields().Binding[0]).To(Equal(byte(1)))
		y := e.fields()
		y.Call = ownCall(x)
		Expect(e.fields().Call.equal(c)).To(BeTrue())
	})
	It("redacts direct nested private and invalid formatter diagnostics and JSON", func() {
		c, f := fixture()
		e := ev(Admit, c, f)
		r := Reduce(Unmapped{}, e)
		values := []any{c, e, r, r.State, struct{ private any }{c}, struct{ private any }{e}, struct{ private any }{r.State}, []Call{c}, map[string]Call{"safe": c}}
		for _, v := range values {
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x"} {
				text := fmt.Sprintf(verb, v)
				Expect(text).NotTo(ContainSubstring("secret-"))
				Expect(text).NotTo(ContainSubstring("invocation-1"))
				Expect(text).NotTo(ContainSubstring("protocol-1"))
			}
			b, err := json.Marshal(v)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).NotTo(ContainSubstring("secret-"))
		}
	})
	It("independently rejects forged transitions effects rules and states", func() {
		c, f := fixture()
		e := ev(Claim, c, f)
		for _, r := range []Transition{{State: Claimed{c}, Effects: []Effect{ClaimCall}, Rule: "HB10"}, {State: Completed{c}, Effects: []Effect{Publish}, Rule: "HB13"}, {State: Unmapped{}, Effects: []Effect{Execute}, Rule: "HB00"}, {State: Unmapped{}, Rule: "invented"}} {
			Expect(CheckTransition(Unmapped{}, e, r)).To(HaveOccurred())
		}
		Expect(Invariant(Claimed{})).To(HaveOccurred())
		Expect(Invariant(nil)).To(HaveOccurred())
	})
	It("covers ten sealed states and every event pair in bounded traces", func() {
		c, f := fixture()
		states := []State{Unmapped{}, Admitted{c}, WaitingApproval{c}, Ready{c}, Claimed{c}, Executing{c}, Completed{c}, Denied{c}, Held{c}, Closed{c}}
		transitions := 0
		for _, s := range states {
			for k := Candidate; k <= Close; k++ {
				e := ev(k, c, f)
				r := Reduce(s, e)
				Expect(Invariant(r.State)).To(Succeed())
				Expect(CheckTransition(s, e, r)).To(Succeed())
				transitions++
			}
		}
		Expect(transitions).To(Equal(140))
	})
	It("enumerates bounded origin target identities leases records cancellation and replay witnesses", func() {
		// Factored bounds, not a claim of the full Cartesian product: 2 invocations,
		// equal binding/protocol, independent origin/target attempt+epoch 1..2.
		c, f := fixture()
		count := 0
		witnesses := map[string]bool{}
		for oa := uint64(1); oa <= 2; oa++ {
			for oe := uint64(1); oe <= 2; oe++ {
				for ta := uint64(1); ta <= 2; ta++ {
					for te := uint64(1); te <= 2; te++ {
						for inv := 1; inv <= 2; inv++ {
							x := c.fields()
							x.Attempt = oa
							x.Epoch = oe
							x.Invocation = fmt.Sprintf("invocation-%d", inv)
							origin := ownCall(x)
							x.Attempt = ta
							x.Epoch = te
							target := ownCall(x)
							g := f
							g.HelperScope = target
							g.Origin = origin
							r := step(Unmapped{}, Replay, target, g, "HB15")
							witnesses[r.Rule] = true
							count++
						}
					}
				}
			}
		}
		for _, lease := range []LeaseState{Live, Expired, Released, Stolen, Missing} {
			for _, j := range []JournalState{NoOther, OtherPrepared, OtherCompleted, OtherDenied, OtherDispatched, OtherUnknown, LegacyDispatched, LegacyUnknown} {
				g := f
				g.Lease = lease
				g.Other = j
				e := ev(Claim, c, g)
				s := ready(c, f)
				r := Reduce(s, e)
				Expect(CheckTransition(s, e, r)).To(Succeed())
				witnesses[r.Rule] = true
				count++
			}
		}
		for _, k := range []EventKind{Cancel, Expire, Close} {
			r := Reduce(executing(c, f), ev(k, c, f))
			Expect(CheckTransition(executing(c, f), ev(k, c, f), r)).To(Succeed())
			witnesses[r.Rule] = true
			count++
		}
		Expect(count).To(Equal(75))
		Expect(witnesses).To(HaveKey("HB10"))
		Expect(witnesses).To(HaveKey("HB11"))
		Expect(witnesses).To(HaveKey("HB15"))
		Expect(witnesses).To(HaveKey("HB17"))
		Expect(witnesses).To(HaveKey("HB18"))
		GinkgoWriter.Printf("bounded witnesses: %d transitions; %s\n", count, strings.Join([]string{"HB10", "HB11", "HB15", "HB17", "HB18"}, ","))
	})
})
