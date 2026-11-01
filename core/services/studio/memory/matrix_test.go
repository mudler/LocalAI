// SPDX-License-Identifier: MIT
package memory

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Columns follow SampleEvents; envelopes are matched to each row so that the
// matrix exercises behavior rather than rejecting every pair as stale.
var behavioralRules = map[string]string{
	"Queued":            "R02-claim R02-denied R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R03-revoked R01-duplicate R06-recover R02-unexpected R02-unexpected R02-unexpected",
	"Authorizing":       "R02-unexpected R02-unexpected R04-content R03-denied R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R06-early R02-unexpected R03-revoked R01-duplicate R02-live R02-unexpected R02-unexpected R02-unexpected",
	"Transforming":      "R02-unexpected R02-unexpected R02-unexpected R02-unexpected R04-summary R04-failed R02-unexpected R02-unexpected R02-unexpected R02-unexpected R06-early R02-unexpected R03-revoked R01-duplicate R02-live R02-unexpected R02-unexpected R02-unexpected",
	"PersistingPayload": "R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R04-durable R06-wait R02-unexpected R02-unexpected R06-early R02-unexpected R03-revoked R01-duplicate R02-live R02-unexpected R02-unexpected R02-unexpected",
	"Writing":           "R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R05-saved R06-wait R06-early R02-unexpected R03-revoked R01-duplicate R02-live R02-unexpected R07-deleted R02-unexpected",
	"RetryWaiting":      "R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R02-unexpected R03-revoked R01-duplicate R06-early R02-unexpected R02-unexpected R02-unexpected",
	"Saved":             "R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R01-duplicate R07-absorbed R07-absorbed R07-absorbed R07-absorbed",
	"Failed":            "R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R01-duplicate R07-absorbed R08-retry R07-absorbed R07-absorbed",
	"Skipped":           "R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R07-absorbed R01-duplicate R07-absorbed R07-absorbed R07-absorbed R07-absorbed",
}

func withEnvelope(e Event, env Envelope) Event {
	v := reflect.New(reflect.TypeOf(e))
	v.Elem().Set(reflect.ValueOf(e))
	v.Elem().FieldByName("Envelope").Set(reflect.ValueOf(env))
	return v.Elem().Interface().(Event)
}

var _ = Describe("Behavioral state/event matrix", func() {
	It("pins the rule, effects and state for all 162 matched pairs", func() {
		for _, s := range SampleStates() {
			rules := strings.Fields(behavioralRules[stateName(s)])
			Expect(rules).To(HaveLen(len(SampleEvents())))
			for i, e := range SampleEvents() {
				e = withEnvelope(e, envelopeOfState(s))
				tr := Reduce(s, e)
				Expect(tr.RuleID).To(Equal(rules[i]), "%T + %T", s, e)
				Expect(effectNames(tr.Effects)).To(Equal(expectedEffects(rules[i])), "%T + %T", s, e)
				if strings.HasPrefix(rules[i], "R02-") && rules[i] != "R02-claim" {
					Expect(tr.State).To(Equal(s))
				}
				Expect(CheckInvariants(tr.State)).To(Succeed(), "%T + %T", s, e)
			}
		}
	})
	It("fails when a sealed constructor is added without a matrix row or column", func() {
		files, err := parser.ParseDir(token.NewFileSet(), ".", nil, 0)
		Expect(err).NotTo(HaveOccurred())
		registered := map[string]map[string]bool{"isState": {}, "isEvent": {}}
		for _, s := range SampleStates() {
			registered["isState"][reflect.TypeOf(s).Name()] = true
		}
		for _, e := range SampleEvents() {
			registered["isEvent"][reflect.TypeOf(e).Name()] = true
		}
		counts := map[string]int{}
		for _, file := range files["memory"].Files {
			for _, decl := range file.Decls {
				f, ok := decl.(*ast.FuncDecl)
				if !ok || f.Recv == nil {
					continue
				}
				entries, ok := registered[f.Name.Name]
				if !ok {
					continue
				}
				name := f.Recv.List[0].Type.(*ast.Ident).Name
				Expect(entries).To(HaveKey(name))
				counts[f.Name.Name]++
			}
		}
		Expect(counts["isState"]).To(Equal(len(behavioralRules)))
		Expect(counts["isEvent"]).To(Equal(len(SampleEvents())))
	})
})
