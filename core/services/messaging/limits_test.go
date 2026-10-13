package messaging_test

import (
	"errors"
	"maps"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/messaging/messagingtest"
)

var _ = Describe("The conformance suite and the subject rules", func() {
	It("has a subject for exactly the broadcast roots that the rules serve", func() {
		want := messaging.BroadcastRootsForTest()
		slices.Sort(want)
		got := slices.Sorted(maps.Keys(messagingtest.BroadcastRootSubjects))
		Expect(got).To(Equal(want), "add a row to messagingtest.BroadcastRootSubjects for each new broadcast root")
	})

	It("uses subjects that the rules accept, under the root that names them", func() {
		for root, subject := range messagingtest.BroadcastRootSubjects {
			Expect(messaging.ValidateSubject(subject)).To(Succeed(), subject)
			Expect(subject).To(HavePrefix(root+"."), subject)
		}
	})
})

var _ = Describe("CheckBroadcastSize", func() {
	It("accepts the bound itself", func() {
		Expect(messaging.CheckBroadcastSize("jobs.a.result", messaging.MaxBroadcastBytes)).To(Succeed())
	})

	It("refuses one byte more, with a class that a caller can match", func() {
		err := messaging.CheckBroadcastSize("jobs.a.result", messaging.MaxBroadcastBytes+1)
		Expect(errors.Is(err, messaging.ErrPayloadTooLarge)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("jobs.a.result"))
	})
})
