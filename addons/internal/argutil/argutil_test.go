// SPDX-License-Identifier: MIT
package argutil_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mudler/LocalAI/addons/internal/argutil"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestArgutil(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Argutil suite")
}

var _ = Describe("argutil", func() {
	It("refuses with a bad request", func() {
		Expect(errors.Is(argutil.Bad("x %d", 1), protocol.ErrBadRequest)).To(BeTrue())
	})

	It("splits the tool name from the arguments", func() {
		tool, args, err := argutil.SplitArgs(json.RawMessage(`{"tool":"t","a":1}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(tool).To(Equal("t"))
		Expect(args).To(HaveKey("a"))
		Expect(args).NotTo(HaveKey("tool"))
		_, _, err = argutil.SplitArgs(json.RawMessage(`[]`))
		Expect(err).To(HaveOccurred())
		_, _, err = argutil.SplitArgs(json.RawMessage(`{"a":1}`))
		Expect(err).To(HaveOccurred())
	})

	It("rejects an unknown argument", func() {
		var v struct{ A int }
		Expect(argutil.DecodeArgs(map[string]json.RawMessage{"A": json.RawMessage("1")}, &v)).To(Succeed())
		Expect(argutil.DecodeArgs(map[string]json.RawMessage{"B": json.RawMessage("1")}, &v)).To(HaveOccurred())
	})

	It("checks a line", func() {
		Expect(argutil.CheckLine("name", "ok", 5, true)).To(Succeed())
		Expect(argutil.CheckLine("name", " ", 5, true)).To(HaveOccurred())
		Expect(argutil.CheckLine("name", " ", 5, false)).To(Succeed())
		Expect(argutil.CheckLine("name", "toolong", 5, true)).To(HaveOccurred())
		Expect(argutil.CheckLine("name", "a\nb", 5, true)).To(HaveOccurred())
	})

	It("checks a cursor", func() {
		Expect(argutil.CheckCursor("")).To(Succeed())
		Expect(argutil.CheckCursor("abc_-=")).To(Succeed())
		Expect(argutil.CheckCursor("a b")).To(HaveOccurred())
	})

	It("cuts at a character boundary", func() {
		out, omitted := argutil.Cut("aé", 2)
		Expect(out).To(Equal("a"))
		Expect(omitted).To(Equal(2))
		r, cut := argutil.Runes("héllo", 2)
		Expect(r).To(Equal("hé"))
		Expect(cut).To(BeTrue())
	})

	It("never gives a zero digest and separates parts", func() {
		Expect(argutil.Sum64()).NotTo(BeZero())
		Expect(argutil.Sum64("ab", "c")).NotTo(Equal(argutil.Sum64("a", "bc")))
	})

	It("does not escape HTML", func() {
		Expect(string(argutil.PlainJSON(map[string]string{"a": "<b>"}))).To(Equal(`{"a":"<b>"}`))
	})
})
