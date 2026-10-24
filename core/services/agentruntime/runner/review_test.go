// SPDX-License-Identifier: MIT
package runner

import (
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Review regressions", func() {
	It("compares every state payload field rather than closure addresses", func() {
		mutations := map[string]func(*dataFields){
			"principal":               func(f *dataFields) { f.id.fields().principal += "x" },
			"conversation":            func(f *dataFields) { f.id.fields().conversation += "x" },
			"turn":                    func(f *dataFields) { f.id.fields().turn += "x" },
			"attempt":                 func(f *dataFields) { f.id.fields().attempt++ },
			"epoch":                   func(f *dataFields) { f.id.fields().epoch++ },
			"identity absent":         func(f *dataFields) { f.id = identity{} },
			"attached":                func(f *dataFields) { f.attached = !f.attached },
			"outcome":                 func(f *dataFields) { f.outcome = stopped },
			"input":                   func(f *dataFields) { f.input[0]++ },
			"ciphertext":              func(f *dataFields) { f.ciphertext = []byte("forged") },
			"nil ciphertext to empty": func(f *dataFields) { f.ciphertext = []byte{} },
		}
		d := fixture()
		s := running{d}
		e := ev("Stream", d)
		canonical := PureReduce(s, e)
		Expect(sameTransition(canonical, PureReduce(s, e))).To(BeTrue())
		for name, mutate := range mutations {
			r := PureReduce(s, e)
			f := r.state.payload()
			mutate(f.fields())
			r.state = makeState(r.state.phase(), f)
			Expect(sameTransition(canonical, r)).To(BeFalse(), name)
			Expect(sameTransition(r, canonical)).To(BeFalse(), name)
			Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid), name)
		}
		r := PureReduce(s, e)
		r.state = quiescent{r.state.payload()}
		Expect(sameTransition(canonical, r)).To(BeFalse())
		Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid))
	})
	It("preserves exact nil versus empty state buffers and effect lists", func() {
		d := fixture()
		d.fields().ciphertext = []byte("cipher")
		s := settling{d}
		e := ev("SettleSucceeded", d)
		canonical := PureReduce(s, e)
		for _, field := range []string{"input", "ciphertext"} {
			r := PureReduce(s, e)
			f := r.state.payload()
			if field == "input" {
				f.fields().input = []byte{}
			} else {
				f.fields().ciphertext = []byte{}
			}
			r.state = idle{f}
			Expect(CheckInvariants(r.state)).To(Succeed())
			Expect(sameTransition(canonical, r)).To(BeFalse(), field)
			Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid), field)
		}
		d = fixture()
		e = ev("Detach", d)
		r := PureReduce(running{d}, e)
		canonical = PureReduce(running{d}, e)
		Expect(r.effects).To(BeNil())
		r.effects = []effect{}
		Expect(sameTransition(canonical, r)).To(BeFalse())
		Expect(CheckTransition(running{d}, e, r)).To(MatchError(errInvalid))
	})
	It("compares every effect identity and content field for all ten variants", func() {
		mutations := map[string]func(*effectFields){
			"principal":       func(f *effectFields) { f.id.fields().principal += "x" },
			"conversation":    func(f *effectFields) { f.id.fields().conversation += "x" },
			"turn":            func(f *effectFields) { f.id.fields().turn += "x" },
			"attempt":         func(f *effectFields) { f.id.fields().attempt++ },
			"epoch":           func(f *effectFields) { f.id.fields().epoch++ },
			"identity absent": func(f *effectFields) { f.id = identity{} },
			"content":         func(f *effectFields) { f.content = []byte("forged") },
		}
		d := fixture()
		for _, name := range []string{"RestoreSession", "StartTurn", "CancelSession", "AwaitQuiescence", "ExportAndSeal", "SettleCiphertext", "StartNext", "EmitSafe", "CloseSession", "HoldInput"} {
			base := transition{state: running{d.clone()}, effects: []effect{makeEffect(name, d)}, rule: "rule", guard: "guard"}
			copy := transition{state: running{d.clone()}, effects: []effect{makeEffect(name, d)}, rule: "rule", guard: "guard"}
			Expect(sameTransition(base, copy)).To(BeTrue(), name)
			for field, mutate := range mutations {
				f := makeEffect(name, d)
				// The trusted accessor owns a clone; construct the chosen variant
				// explicitly around the corrupted payload for this negative probe.
				payload := f.payload()
				mutate(payload.fields())
				candidate := replaceEffectPayload(f, payload)
				copy.effects = []effect{candidate}
				Expect(sameTransition(base, copy)).To(BeFalse(), name+"/"+field)
				Expect(sameTransition(copy, base)).To(BeFalse(), name+"/"+field)
			}
			copy.effects = []effect{replaceEffectPayload(makeEffect(name, d), ownEffectData(&effectFields{id: d.fields().id, content: []byte{}}))}
			Expect(sameTransition(base, copy)).To(BeFalse(), name+"/nil-empty")
		}
	})
	It("rejects reordered replaced missing and extra effects and forged labels", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		canonical := PureReduce(s, e)
		Expect(canonical.effects).To(HaveLen(2))
		for _, mutation := range []string{"order", "variant", "missing", "extra", "rule", "guard", "effect content", "effect nil-empty"} {
			r := PureReduce(s, e)
			switch mutation {
			case "order":
				r.effects[0], r.effects[1] = r.effects[1], r.effects[0]
			case "variant":
				r.effects[0] = makeEffect("HoldInput", d)
			case "missing":
				r.effects = r.effects[:1]
			case "extra":
				r.effects = append(r.effects, makeEffect("HoldInput", d))
			case "rule":
				r.rule += "x"
			case "guard":
				r.guard += "x"
			case "effect content", "effect nil-empty":
				f := r.effects[0].payload()
				f.fields().content = []byte{}
				if mutation == "effect content" {
					f.fields().content = []byte("forged")
				}
				r.effects[0] = replaceEffectPayload(r.effects[0], f)
			}
			Expect(sameTransition(canonical, r)).To(BeFalse(), mutation)
			Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid), mutation)
		}
	})

	It("protects safe logging holders even through private reflective wrappers", func() {
		d := fixture()
		r := PureReduce(sealing{d}, ev("SealSucceeded", d))
		for _, v := range []any{d, d.fields().id, running{d}, ev("Stream", d), r, r.effects, r.effects[0]} {
			h := safeLog(v)
			for _, container := range []any{h, []any{h}, map[string]any{"value": h}, struct{ Value any }{h}, struct{ value any }{h}, struct{ value logHolder }{h}} {
				for _, f := range []string{"%v", "%+v", "%#v"} {
					text := fmt.Sprintf(f, container)
					Expect(text).NotTo(ContainSubstring("private-"))
					Expect(text).NotTo(ContainSubstring("112 114 105 118 97 116 101"))
					Expect(text).NotTo(ContainSubstring("0x70, 0x72, 0x69, 0x76"))
				}
				b, err := json.Marshal(container)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(b)).NotTo(ContainSubstring("private-"))
			}
		}
	})
	It("redacts raw private-field wrappers without relying on nested Formatter", func() {
		d := fixture()
		for _, raw := range []any{struct{ state runnerState }{running{d}}, struct{ value data }{d}} {
			for _, f := range []string{"%v", "%+v", "%#v"} {
				text := fmt.Sprintf(f, raw)
				Expect(text).NotTo(ContainSubstring("private-"))
				Expect(text).NotTo(ContainSubstring("112 114 105 118 97 116 101"))
				Expect(text).NotTo(ContainSubstring("0x70, 0x72, 0x69, 0x76"))
			}
		}
	})
	It("keeps raw containers opaque for every fmt verb including invalid fallbacks", func() {
		d := fixture()
		e := ev("Stream", d)
		r := PureReduce(sealing{d}, ev("SealSucceeded", d))
		values := []any{d, d.fields().id, running{d}, e, e.payload(), r.effects[0], r.effects[0].payload(), r, r.effects}
		containers := []any{struct{ value data }{d}, struct{ value identity }{d.fields().id}, struct{ state runnerState }{running{d}}, struct{ value event }{e}, struct{ value effect }{r.effects[0]}, struct{ value transition }{r}}
		for _, value := range values {
			containers = append(containers, value, []any{value}, map[string]any{"value": value}, struct{ Value any }{value}, struct{ value any }{value})
		}
		// All documented verbs, plus every ASCII letter to exercise invalid verbs.
		formats := []string{"%v", "%+v", "%#v", "%#q", "% x", "%#x", "%#X", "%+q", "%.100s"}
		for _, verb := range "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ%" {
			formats = append(formats, "%"+string(verb))
		}
		for i, container := range containers {
			for _, format := range formats {
				text := fmt.Sprintf(format, container)
				for _, secret := range []string{"private-", "112 114 105 118 97 116 101", "0x70, 0x72, 0x69, 0x76", "70726976617465", "160 162 151 166", "1110000 1110010"} {
					Expect(text).NotTo(ContainSubstring(secret), "container=%d format=%s", i, format)
				}
			}
			b, err := json.Marshal(container)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).NotTo(ContainSubstring("private-"))
		}
	})
	It("rejects typed-nil restoreSession effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{(*restoreSessionEffect)(nil)}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects unsupported pointer restoreSession effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{&restoreSessionEffect{}}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects typed-nil startTurn effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{(*startTurnEffect)(nil)}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects unsupported pointer startTurn effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{&startTurnEffect{}}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects typed-nil cancelSession effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{(*cancelSessionEffect)(nil)}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects unsupported pointer cancelSession effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{&cancelSessionEffect{}}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects typed-nil awaitQuiescence effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{(*awaitQuiescenceEffect)(nil)}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects unsupported pointer awaitQuiescence effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{&awaitQuiescenceEffect{}}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects typed-nil exportAndSeal effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{(*exportAndSealEffect)(nil)}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects unsupported pointer exportAndSeal effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{&exportAndSealEffect{}}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects typed-nil settleCiphertext effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{(*settleCiphertextEffect)(nil)}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects unsupported pointer settleCiphertext effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{&settleCiphertextEffect{}}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects typed-nil startNext effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{(*startNextEffect)(nil)}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects unsupported pointer startNext effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{&startNextEffect{}}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects typed-nil emitSafe effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{(*emitSafeEffect)(nil)}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects unsupported pointer emitSafe effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{&emitSafeEffect{}}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects typed-nil closeSession effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{(*closeSessionEffect)(nil)}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects unsupported pointer closeSession effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{&closeSessionEffect{}}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects typed-nil holdInput effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{(*holdInputEffect)(nil)}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects unsupported pointer holdInput effects without panic", func() {
		d := fixture()
		s := running{d}
		e := ev("Stop", d)
		r := PureReduce(s, e)
		r.effects = []effect{&holdInputEffect{}}
		Expect(func() { Expect(CheckTransition(s, e, r)).To(MatchError(errInvalid)) }).NotTo(Panic())
	})
	It("rejects invalid phase shapes before effects can release FIFO", func() {
		d := fixture()
		invalid := []runnerState{cancelling{d}}
		x := d.clone()
		x.fields().outcome = stopped
		invalid = append(invalid, idle{x}, restoring{x}, running{x}, quiescent{x})
		x = d.clone()
		x.fields().ciphertext = []byte("cipher")
		invalid = append(invalid, idle{x}, restoring{x}, running{x}, quiescent{x}, sealing{x}, cancelling{x})
		x = d.clone()
		x.fields().input = nil
		invalid = append(invalid, restoring{x}, running{x}, cancelling{x}, quiescent{x}, sealing{x}, settling{x})
		for _, s := range invalid {
			Expect(CheckInvariants(s)).NotTo(Succeed(), "phase %s", s.phase())
			for _, n := range []string{"QuiescenceConfirmed", "SealSucceeded", "SettleSucceeded"} {
				e := ev(n, d)
				r := PureReduce(s, e)
				Expect(r.rule).To(Equal("RR001"))
				Expect(r.effects).To(BeEmpty())
				Expect(CheckTransition(s, e, r)).NotTo(Succeed())
			}
		}
	})
	It("holds an empty settled attempt under uncertainty without empty restore", func() {
		d := fixture()
		d.fields().ciphertext = []byte("cipher")
		settled := step(settling{d}, ev("SettleSucceeded", d))
		Expect(effectNames(settled)).To(Equal([]string{"StartNext"}))
		s := step(settled.state, ev("UnknownJournal", d)).state
		Expect(s.phase()).To(Equal("Reconciliation"))
		v := ev("Resume", d).payload()
		v.fields().id.fields().attempt++
		v.fields().reconciled = true
		r := step(s, makeEvent("Resume", v))
		Expect(r.state.phase()).To(Equal("Reconciliation"))
		Expect(r.effects).To(BeEmpty())
		Expect(r.state.payload().fields().id.equal(d.fields().id)).To(BeTrue())
	})
	It("retains failure and interruption ciphertext until a legitimate fresh resume", func() {
		for _, outcome := range []completion{finished, stopped, leaseLost} {
			d := fixture()
			d.fields().outcome = outcome
			d.fields().ciphertext = []byte("cipher")
			for _, n := range []string{"SettleFailed", "UnknownJournal"} {
				r := step(settling{d}, ev(n, d))
				Expect(r.state.payload().fields().ciphertext).To(Equal(d.fields().ciphertext))
				Expect(r.state.payload().fields().input).To(Equal(d.fields().input))
				Expect(effectNames(r)).NotTo(ContainElement("StartNext"))
				v := ev("Resume", d).payload()
				v.fields().id.fields().attempt++
				v.fields().reconciled = true
				resumed := step(r.state, makeEvent("Resume", v))
				Expect(resumed.state.phase()).To(Equal("Restoring"))
				Expect(resumed.state.payload().fields().input).To(Equal(d.fields().input))
				Expect(resumed.state.payload().fields().ciphertext).To(BeEmpty())
			}
		}
	})
})

// replaceEffectPayload preserves the concrete variant in malformed-payload probes.
func replaceEffectPayload(e effect, d effectData) effect {
	switch e.(type) {
	case restoreSessionEffect:
		return restoreSessionEffect{d}
	case startTurnEffect:
		return startTurnEffect{d}
	case cancelSessionEffect:
		return cancelSessionEffect{d}
	case awaitQuiescenceEffect:
		return awaitQuiescenceEffect{d}
	case exportAndSealEffect:
		return exportAndSealEffect{d}
	case settleCiphertextEffect:
		return settleCiphertextEffect{d}
	case startNextEffect:
		return startNextEffect{d}
	case emitSafeEffect:
		return emitSafeEffect{d}
	case closeSessionEffect:
		return closeSessionEffect{d}
	case holdInputEffect:
		return holdInputEffect{d}
	}
	panic("unknown test effect")
}
