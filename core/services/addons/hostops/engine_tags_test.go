// SPDX-License-Identifier: MIT
package hostops

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The engine of a job comes from the gallery tag, not from a name in the host.
var _ = Describe("engine selection by gallery tag", func() {
	start := func(l BackendLister, ft *fakeFT, q *fakeQ, op, args string) error {
		b := registerWith(ft, q, l)
		_, err := call(b[op], "named-user", "u1", args)
		return err
	}

	It("uses the first engine of the tag in gallery order when no backend is named", func() {
		ft, q := &fakeFT{}, &fakeQ{}
		l := tagged{engines: map[string][]string{TagFineTuning: {"beta", "alpha"}, TagQuantization: {"delta", "gamma"}}}
		Expect(start(l, ft, q, "finetune.start", `{"model":"org/base","datasetSource":"org/data"}`)).To(Succeed())
		Expect(start(l, ft, q, "quantize.start", `{"model":"org/base"}`)).To(Succeed())
		Expect(ft.started[0].Backend).To(Equal("beta"))
		Expect(q.started[0].Backend).To(Equal("delta"))
	})

	It("accepts a named engine that carries the tag, and refuses one that does not", func() {
		ft := &fakeFT{}
		l := tagged{engines: map[string][]string{TagFineTuning: {"alpha", "beta"}}}
		Expect(start(l, ft, &fakeQ{}, "finetune.start", `{"model":"org/base","datasetSource":"org/data","backend":"beta"}`)).To(Succeed())
		Expect(ft.started[0].Backend).To(Equal("beta"))
		err := start(l, ft, &fakeQ{}, "finetune.start", `{"model":"org/base","datasetSource":"org/data","backend":"trl"}`)
		Expect(err).To(MatchError(ErrBadRequest))
		Expect(err.Error()).To(ContainSubstring("fine-tuning engine trl is not installed"))
	})

	It("refuses a start with no backend when none is installed", func() {
		ft := &fakeFT{}
		err := start(tagged{}, ft, &fakeQ{}, "finetune.start", `{"model":"org/base","datasetSource":"org/data"}`)
		Expect(err).To(MatchError(ErrBadRequest))
		Expect(err.Error()).To(ContainSubstring("no fine-tuning engine is installed"))
		Expect(ft.started).To(BeEmpty())
	})

	It("does not mix the families", func() {
		l := tagged{engines: map[string][]string{TagFineTuning: {"alpha"}, TagQuantization: {"gamma"}}}
		err := start(l, &fakeFT{}, &fakeQ{}, "quantize.start", `{"model":"org/base","backend":"alpha"}`)
		Expect(err).To(MatchError(ErrBadRequest))
	})
})
