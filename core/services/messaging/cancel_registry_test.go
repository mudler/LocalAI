package messaging_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/messaging"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CancelRegistry", func() {
	var r messaging.CancelRegistry

	BeforeEach(func() {
		r = messaging.CancelRegistry{}
	})

	It("registers a cancel function and invokes it on Cancel", func() {
		called := false
		_, cancel := context.WithCancel(context.Background())

		r.Register("job-1", func() {
			called = true
			cancel()
		})

		ok := r.Cancel("job-1")
		Expect(ok).To(BeTrue())
		Expect(called).To(BeTrue())
	})

	It("returns false when cancelling an unknown key", func() {
		ok := r.Cancel("nonexistent")
		Expect(ok).To(BeFalse())
	})

	It("prevents cancel after deregister", func() {
		called := false

		r.Register("job-2", func() {
			called = true
		})

		r.Deregister("job-2")

		ok := r.Cancel("job-2")
		Expect(ok).To(BeFalse())
		Expect(called).To(BeFalse())
	})

	It("overwrites previous registration", func() {
		firstCalled := false
		secondCalled := false

		r.Register("job-3", func() {
			firstCalled = true
		})
		r.Register("job-3", func() {
			secondCalled = true
		})

		ok := r.Cancel("job-3")
		Expect(ok).To(BeTrue())
		Expect(firstCalled).To(BeFalse())
		Expect(secondCalled).To(BeTrue())
	})
})
