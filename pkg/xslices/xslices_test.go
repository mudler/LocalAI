// SPDX-License-Identifier: MIT
package xslices_test

import (
	"encoding/json"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/xslices"
)

func TestXslices(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Xslices Suite")
}

var _ = Describe("NonNil", func() {
	It("encodes a nil list as an empty JSON array", func() {
		var none []string
		encoded, err := json.Marshal(xslices.NonNil(none))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(encoded)).To(Equal("[]"))
	})

	It("keeps the items of a list", func() {
		encoded, err := json.Marshal(xslices.NonNil([]string{"a"}))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(encoded)).To(Equal(`["a"]`))
	})

	It("keeps the type of a named list and makes it non-nil", func() {
		type names []string
		Expect(xslices.NonNil(names(nil))).ToNot(BeNil())
	})
})
