package coordinator_test

import (
	"errors"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/coordinator"
)

func ext(id string) coordinator.Contact {
	return coordinator.Contact{ID: id, Kind: coordinator.ContactExternal, Binding: "b", Chat: "c"}
}

var _ = Describe("coordinator config", func() {
	invalid := func(c coordinator.Config) {
		GinkgoHelper()
		err := coordinator.Validate(c)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, coordinator.ErrInvalid)).To(BeTrue(), "%v", err)
	}

	It("Validate refuses a bad contact id, a duplicate id, more than 50 contacts", func() {
		for _, id := range []string{"", "Ops", "-a", "a b", strings.Repeat("a", 41)} {
			invalid(coordinator.Config{Contacts: []coordinator.Contact{ext(id)}})
		}
		invalid(coordinator.Config{Contacts: []coordinator.Contact{ext("a"), ext("a")}})
		many := make([]coordinator.Contact, 0, 51)
		for i := range 51 {
			many = append(many, ext(fmt.Sprintf("c%d", i)))
		}
		invalid(coordinator.Config{Contacts: many})
		Expect(coordinator.Validate(coordinator.Config{Contacts: many[:50]})).To(Succeed())
		long := ext("a")
		long.Name = strings.Repeat("é", 81)
		invalid(coordinator.Config{Contacts: []coordinator.Contact{long}})
		long.Name, long.Description = strings.Repeat("é", 80), strings.Repeat("x", 201)
		invalid(coordinator.Config{Contacts: []coordinator.Contact{long}})
	})

	It("Validate wants User for a user contact and Binding and Chat for an external one", func() {
		invalid(coordinator.Config{Contacts: []coordinator.Contact{{ID: "a", Kind: coordinator.ContactUser}}})
		invalid(coordinator.Config{Contacts: []coordinator.Contact{{ID: "a", Kind: coordinator.ContactExternal, Binding: "b"}}})
		invalid(coordinator.Config{Contacts: []coordinator.Contact{{ID: "a", Kind: coordinator.ContactExternal, Chat: "c"}}})
		invalid(coordinator.Config{Contacts: []coordinator.Contact{{ID: "a", Kind: "other", User: "u"}}})
		Expect(coordinator.Validate(coordinator.Config{Contacts: []coordinator.Contact{
			{ID: "a", Kind: coordinator.ContactUser, User: "u"}, ext("b")}})).To(Succeed())
	})

	It("Validate bounds WakeEvery, the caps and helper limits, WakeNote and Page", func() {
		Expect(coordinator.Validate(coordinator.Config{})).To(Succeed())
		for _, d := range []time.Duration{5 * time.Minute, 24 * time.Hour} {
			Expect(coordinator.Validate(coordinator.Config{WakeEvery: d})).To(Succeed())
		}
		invalid(coordinator.Config{WakeEvery: 4*time.Minute + 59*time.Second})
		invalid(coordinator.Config{WakeEvery: 24*time.Hour + time.Second})
		invalid(coordinator.Config{WakeEvery: -time.Minute})
		Expect(coordinator.Validate(coordinator.Config{SendPerMinute: 60, SendPerDay: 5000, MaxHelpers: 64, MaxDepth: 4})).To(Succeed())
		invalid(coordinator.Config{SendPerMinute: 61})
		invalid(coordinator.Config{SendPerDay: 5001})
		invalid(coordinator.Config{MaxHelpers: 65})
		invalid(coordinator.Config{MaxDepth: 5})
		invalid(coordinator.Config{SendPerMinute: -1})
		Expect(coordinator.Validate(coordinator.Config{WakeNote: strings.Repeat("x", 4000)})).To(Succeed())
		invalid(coordinator.Config{WakeNote: strings.Repeat("x", 4001)})
		Expect(coordinator.Validate(coordinator.Config{Page: "board-1"})).To(Succeed())
		for _, p := range []string{"Board", "a/b", "..", "-x", "a b"} {
			invalid(coordinator.Config{Page: p})
		}
	})

	It("WithDefaults fills 10, 100, 8, 2 and leaves set values", func() {
		d := coordinator.Config{}.WithDefaults()
		Expect([]int{d.SendPerMinute, d.SendPerDay, d.MaxHelpers, d.MaxDepth}).To(Equal([]int{10, 100, 8, 2}))
		s := coordinator.Config{SendPerMinute: 3, SendPerDay: 4, MaxHelpers: 5, MaxDepth: 1}.WithDefaults()
		Expect([]int{s.SendPerMinute, s.SendPerDay, s.MaxHelpers, s.MaxDepth}).To(Equal([]int{3, 4, 5, 1}))
	})

	It("Contact finds a contact by id", func() {
		c := coordinator.Config{Contacts: []coordinator.Contact{ext("a"), ext("b")}}
		got, ok := c.Contact("b")
		Expect(ok).To(BeTrue())
		Expect(got.ID).To(Equal("b"))
		_, ok = c.Contact("z")
		Expect(ok).To(BeFalse())
	})

	It("builds the counter buckets in UTC", func() {
		t := time.Date(2026, 11, 8, 23, 59, 0, 0, time.FixedZone("x", -2*3600)) // 01:59 UTC on the 9th
		Expect(coordinator.MinuteBucket(t)).To(Equal("min:202611090159"))
		Expect(coordinator.DayBucket("send", t)).To(Equal("send:2026-11-09"))
		Expect(coordinator.DayBucket("runs", t)).To(Equal("runs:2026-11-09"))
	})
})
