package priority_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/priority"
)

func TestPriority(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Priority test suite")
}

var _ = Describe("priority classes", func() {
	DescribeTable("maps service_tier to a class",
		func(tier string, want priority.Class) {
			got, err := priority.ParseServiceTier(tier)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("empty", "", priority.Default),
		Entry("auto", "auto", priority.Default),
		Entry("default", "default", priority.Default),
		Entry("scale", "scale", priority.Default),
		Entry("flex", "flex", priority.Batch),
		Entry("priority", "priority", priority.Interactive),
		Entry("case and space", " Priority ", priority.Interactive),
	)

	It("rejects an unknown tier", func() {
		_, err := priority.ParseServiceTier("urgent")
		Expect(errors.Is(err, priority.ErrInvalidServiceTier)).To(BeTrue())
		_, err = priority.Decide("urgent", true)
		Expect(errors.Is(err, priority.ErrInvalidServiceTier)).To(BeTrue())
	})

	It("lets any caller lower its class", func() {
		c, err := priority.Decide("flex", false)
		Expect(err).ToNot(HaveOccurred())
		Expect(c).To(Equal(priority.Batch))
	})

	It("clamps a raise to Default for a caller that may not raise", func() {
		c, err := priority.Decide("priority", false)
		Expect(err).ToNot(HaveOccurred())
		Expect(c).To(Equal(priority.Default))
	})

	It("lets an administrator raise", func() {
		c, err := priority.Decide("priority", true)
		Expect(err).ToNot(HaveOccurred())
		Expect(c).To(Equal(priority.Interactive))
	})

	It("names the tier that applied", func() {
		Expect(priority.Batch.ServiceTier()).To(Equal("flex"))
		Expect(priority.Default.ServiceTier()).To(Equal("default"))
		Expect(priority.Interactive.ServiceTier()).To(Equal("priority"))
	})

	It("carries the class in the context", func() {
		ctx := context.Background()
		_, ok := priority.FromContext(ctx)
		Expect(ok).To(BeFalse())
		Expect(priority.Of(ctx)).To(Equal(priority.Default))

		ctx = priority.WithClass(ctx, priority.Batch)
		c, ok := priority.FromContext(ctx)
		Expect(ok).To(BeTrue())
		Expect(c).To(Equal(priority.Batch))

		child, cancel := context.WithCancel(ctx)
		defer cancel()
		Expect(priority.Of(child)).To(Equal(priority.Batch))
		Expect(priority.Of(priority.WithClass(child, priority.Interactive))).To(Equal(priority.Interactive))
	})
})
