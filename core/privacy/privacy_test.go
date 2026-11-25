package privacy_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/privacy"
)

func TestPrivacy(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Privacy test suite")
}

var _ = Describe("privacy levels", func() {
	It("parses the names and round-trips them", func() {
		for _, l := range []privacy.Level{privacy.Normal, privacy.MemoryOff, privacy.ZeroRetention} {
			got, err := privacy.ParseLevel(l.String())
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(l))
		}
		_, err := privacy.ParseLevel("private")
		Expect(errors.Is(err, privacy.ErrInvalidLevel)).To(BeTrue())
	})

	It("carries the level in the context and never lowers it", func() {
		ctx := context.Background()
		Expect(privacy.FromContext(ctx)).To(Equal(privacy.Normal))
		ctx = privacy.WithLevel(ctx, privacy.ZeroRetention)
		Expect(privacy.FromContext(ctx)).To(Equal(privacy.ZeroRetention))
		ctx = privacy.WithLevel(ctx, privacy.MemoryOff)
		Expect(privacy.FromContext(ctx)).To(Equal(privacy.ZeroRetention))
		child, cancel := context.WithCancel(ctx)
		defer cancel()
		Expect(privacy.FromContext(context.WithoutCancel(child))).To(Equal(privacy.ZeroRetention))
	})

	DescribeTable("Allows",
		func(l privacy.Level, st privacy.Store, want bool) {
			Expect(privacy.Allows(privacy.WithLevel(context.Background(), l), st)).To(Equal(want))
		},
		Entry("normal keeps memory", privacy.Normal, privacy.StoreMemory, true),
		Entry("normal keeps content", privacy.Normal, privacy.StoreContent, true),
		Entry("memory-off drops memory", privacy.MemoryOff, privacy.StoreMemory, false),
		Entry("memory-off keeps traces", privacy.MemoryOff, privacy.StoreTrace, true),
		Entry("memory-off keeps conversations", privacy.MemoryOff, privacy.StoreConversation, true),
		Entry("zero-retention drops memory", privacy.ZeroRetention, privacy.StoreMemory, false),
		Entry("zero-retention drops conversations", privacy.ZeroRetention, privacy.StoreConversation, false),
		Entry("zero-retention drops traces", privacy.ZeroRetention, privacy.StoreTrace, false),
		Entry("zero-retention drops content", privacy.ZeroRetention, privacy.StoreContent, false),
		Entry("zero-retention keeps counters", privacy.ZeroRetention, privacy.StoreCounters, true),
	)

	Describe("Decide", func() {
		web := func(marker string) privacy.Request {
			return privacy.Request{Marker: marker, WebSession: true, Policy: privacy.PolicyAllow, MaxLevel: privacy.ZeroRetention}
		}
		It("gives a web session its level", func() {
			l, err := privacy.Decide(web("zero-retention"))
			Expect(err).ToNot(HaveOccurred())
			Expect(l).To(Equal(privacy.ZeroRetention))
		})
		It("ignores the marker of an API key", func() {
			r := web("zero-retention")
			r.WebSession = false
			l, err := privacy.Decide(r)
			Expect(err).ToNot(HaveOccurred())
			Expect(l).To(Equal(privacy.Normal))
		})
		It("ignores the marker of an API key even when the policy forbids it", func() {
			r := web("memory-off")
			r.WebSession, r.Policy = false, privacy.PolicyForbid
			_, err := privacy.Decide(r)
			Expect(err).ToNot(HaveOccurred())
		})
		It("refuses a web session when the policy forbids it", func() {
			r := web("memory-off")
			r.Policy = privacy.PolicyForbid
			_, err := privacy.Decide(r)
			Expect(errors.Is(err, privacy.ErrForbidden)).To(BeTrue())
		})
		It("refuses a level above the maximum instead of lowering it", func() {
			r := web("zero-retention")
			r.MaxLevel = privacy.MemoryOff
			_, err := privacy.Decide(r)
			Expect(errors.Is(err, privacy.ErrAboveMax)).To(BeTrue())
			r.Marker = "memory-off"
			l, err := privacy.Decide(r)
			Expect(err).ToNot(HaveOccurred())
			Expect(l).To(Equal(privacy.MemoryOff))
		})
		It("refuses a value that names no level", func() {
			_, err := privacy.Decide(web("everything"))
			Expect(errors.Is(err, privacy.ErrInvalidLevel)).To(BeTrue())
		})
		It("is normal without a marker, whatever the policy", func() {
			r := web("")
			r.Policy = privacy.PolicyForbid
			l, err := privacy.Decide(r)
			Expect(err).ToNot(HaveOccurred())
			Expect(l).To(Equal(privacy.Normal))
		})
	})

	It("parses the policy and the maximum", func() {
		p, err := privacy.ParsePolicy("")
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(privacy.PolicyAllow))
		p, err = privacy.ParsePolicy("Forbid")
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(privacy.PolicyForbid))
		_, err = privacy.ParsePolicy("force")
		Expect(err).To(HaveOccurred())
		m, err := privacy.ParseMaxLevel("")
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(Equal(privacy.ZeroRetention))
		_, err = privacy.ParseMaxLevel("normal")
		Expect(err).To(HaveOccurred())
	})
})
