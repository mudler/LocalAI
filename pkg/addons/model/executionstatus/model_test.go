// SPDX-License-Identifier: MIT
package executionstatus

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"testing"

	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestExecutionStatus(t *testing.T) {
	RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Execution status model")
}

var _ = ginkgo.Describe("Executable status model", func() {
	for _, trace := range Traces() {
		ginkgo.It(fmt.Sprintf("trace family %d: %s", trace.Family, trace.Name), func() {
			steps := RunTrace(Unbound{}, trace.Events)
			Expect(steps).To(HaveLen(len(trace.Want)), trace.Name)
			for i, step := range steps {
				Expect(step.Rule).To(Equal(trace.Want[i].Rule), "%s step %d", trace.Name, i)
				Expect(step.State).To(Equal(trace.Want[i].State), "%s step %d", trace.Name, i)
				Expect(step.Effects).To(Equal(trace.Want[i].Effects), "%s step %d", trace.Name, i)
				Expect(step.Guard).NotTo(BeEmpty())
			}
			Expect(Project(steps[len(steps)-1].State)).To(Equal(trace.Value), trace.Name)
		})
	}
	ginkgo.It("includes all eleven normative trace families", func() {
		families := map[int]bool{}
		for _, trace := range Traces() {
			families[trace.Family] = true
		}
		Expect(families).To(HaveLen(11))
	})

	ginkgo.It("enumerates every declared marker constructor (Go sums are not sealed)", func() {
		f, err := parser.ParseFile(token.NewFileSet(), "types.go", nil, 0)
		Expect(err).NotTo(HaveOccurred())
		for marker, examples := range map[string][]any{"isState": stateExamples(), "isEvent": eventExamples(), "isEffect": {Read{}, Cancel{}, Publish{}}} {
			declared := map[string]bool{}
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if ok && fn.Name.Name == marker {
					declared[fn.Recv.List[0].Type.(*ast.Ident).Name] = true
				}
			}
			listed := map[string]bool{}
			for _, x := range examples {
				listed[reflect.TypeOf(x).Name()] = true
			}
			Expect(listed).To(Equal(declared), marker)
		}
		Expect(AllStates()).To(HaveLen(7))
		Expect(AllEvents()).To(HaveLen(7))
		Expect(Rules()).To(Equal([]Rule{R1, R2, R3, R4, R5, R6, R7, R9, R10, R11, I}))
	})

	ginkgo.It("checks all 49 matrix cells with an independent representative rule oracle", func() {
		matrix := [][]Rule{
			{R1, I, I, I, I, R6, R10}, {I, R2, I, I, I, R6, R10}, {I, R7, R3, R4, R5, R6, R10},
			{I, R7, I, I, I, R6, R10}, {I, R7, I, I, I, R6, R10}, {R1, I, I, I, I, R6, R10}, {I, I, I, I, I, I, I},
		}
		states := AllStates()
		events := AllEvents()
		for i, s := range states {
			for j, e := range events {
				tr := Reduce(s, e)
				Expect(tr.Rule).To(Equal(matrix[i][j]), "%T x %T", s, e)
				Expect(tr.Guard).NotTo(BeEmpty())
			}
		}
	})

	ginkgo.It("explores bounded histories with safety, lifetime, matrix and liveness checks", func() {
		report, err := Explore(5)
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Cells).To(HaveLen(49))
		Expect(report.States).To(BeNumerically(">", 100))
		Expect(report.Transitions).To(BeNumerically(">", 10000))
		Expect(report.LivenessChecks).To(Equal(report.States))
		_, err = fmt.Fprintf(ginkgo.GinkgoWriter, "BFS: states=%d transitions=%d cells=%d liveness=%d\n", report.States, report.Transitions, len(report.Cells), report.LivenessChecks)
		Expect(err).NotTo(HaveOccurred())
	})

	ginkgo.It("covers deadline guard splits for Reply, Failure, Tick and expired Probe", func() {
		b := sampleBinding()
		p := Pending{b, "q", 10}
		for _, now := range []int64{9, 10, 11} {
			for _, payload := range []Payload{InvalidPayload(), ValidPayload(Value{Available, Isolated})} {
				tr := Reduce(p, Reply{b, "q", payload, now})
				want := R5
				if now < 10 {
					want = R4
					if payload.valid {
						want = R3
					}
				}
				Expect(tr.Rule).To(Equal(want))
				if want == R5 {
					Expect(tr.State).To(Equal(SettledUnknown{b, "q", Timeout}))
				}
			}
			want := R5
			if now < 10 {
				want = R4
			}
			Expect(Reduce(p, Failure{b, "q", "failed", now}).Rule).To(Equal(want))
			want = R5
			if now < 10 {
				want = I
			}
			Expect(Reduce(p, Tick{now}).Rule).To(Equal(want))
			want = R9
			if now < 10 {
				want = R2
			}
			Expect(Reduce(Unobserved{b, "q"}, Probe{b, "q", now, 10}).Rule).To(Equal(want))
		}
	})
	ginkgo.It("rejects invalid local payload enums and conservatively projects nonavailable values", func() {
		b := sampleBinding()
		for _, v := range []Value{{"invalid", Isolated}, {Available, "invalid"}, {"", ""}} {
			Expect(Reduce(Pending{b, "q", 10}, Reply{b, "q", ValidPayload(v), 1}).Rule).To(Equal(R4))
		}
		for _, r := range []Readiness{Unavailable, UnknownReadiness} {
			for _, i := range []Isolation{Isolated, Host, UnknownIsolation} {
				tr := Reduce(Pending{b, "q", 10}, Reply{b, "q", ValidPayload(Value{r, i}), 1})
				Expect(tr.Rule).To(Equal(R3))
				Expect(Project(tr.State)).To(Equal(Value{r, UnknownIsolation}))
			}
		}
	})

	ginkgo.It("distinguishes every binding component and Q, even at the deadline", func() {
		b := sampleBinding()
		pending := Pending{b, "q", 10}
		typ := reflect.TypeOf(b)
		for i := 0; i < typ.NumField(); i++ {
			other := b
			v := reflect.ValueOf(&other).Elem().Field(i)
			v.SetString(v.String() + "-other")
			for _, e := range []Event{Probe{other, "q", 10, 20}, Reply{other, "q", ValidPayload(Value{Available, Isolated}), 10}, Failure{other, "q", "failed", 10}} {
				Expect(Reduce(pending, e)).To(Equal(Transition{pending, nil, I, "ignored: pair or identity guard"}), fmt.Sprint(e))
			}
		}
		for _, s := range []State{Unobserved{b, "q"}, pending, Observed{b, "q", Value{Available, Isolated}}, SettledUnknown{b, "q", "failed"}} {
			Expect(Reduce(s, Bind{b, "other", Eligible}).Rule).To(Equal(I))
			Expect(Reduce(s, Probe{b, "other", 1, 10}).Rule).To(Equal(I))
		}
	})
})

func stateExamples() []any {
	var out []any
	for _, s := range AllStates() {
		out = append(out, s)
	}
	return out
}
func eventExamples() []any {
	var out []any
	for _, e := range AllEvents() {
		out = append(out, e)
	}
	return out
}
