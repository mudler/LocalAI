// SPDX-License-Identifier: MIT
package executionbridge

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Paired host execution shell", func() {
	It("rejects absent trusted dependencies rather than granting by default", func() {
		b, err := New(Scope{}, Dependencies{})
		Expect(err).To(HaveOccurred())
		Expect(b).To(BeNil())
	})
	It("fails closed without private projection operation context", func() {
		_, err := operationFrom(context.Background())
		Expect(err).To(HaveOccurred())
	})
})
