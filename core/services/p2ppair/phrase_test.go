package p2ppair_test

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2ppair"
)

var _ = Describe("Phrase", func() {
	It("is four words and the same for the same input", func() {
		srv, dev := newPeer(), newPeer()
		p := p2ppair.Phrase(srv, dev, "invite-1")
		Expect(strings.Fields(p)).To(HaveLen(4))
		Expect(p2ppair.Phrase(srv, dev, "invite-1")).To(Equal(p))
	})

	It("changes with each input", func() {
		srv, dev := newPeer(), newPeer()
		base := p2ppair.Phrase(srv, dev, "invite-1")
		Expect(p2ppair.Phrase(newPeer(), dev, "invite-1")).NotTo(Equal(base))
		Expect(p2ppair.Phrase(srv, newPeer(), "invite-1")).NotTo(Equal(base))
		Expect(p2ppair.Phrase(srv, dev, "invite-2")).NotTo(Equal(base))
		Expect(p2ppair.Phrase(dev, srv, "invite-1")).NotTo(Equal(base), "the roles are not interchangeable")
	})

	It("does not collide on a sample of 20000 pairings", func() {
		srv := newPeer()
		devs := make([]string, 200)
		for i := range devs {
			devs[i] = newPeer().String()
		}
		seen := map[string]string{}
		for i := range 20000 {
			id := fmt.Sprintf("invite-%d", i)
			dev := devs[i%len(devs)]
			p := p2ppair.Phrase(srv, mustDecode(dev), id)
			// 40 bits: a collision among 20000 phrases has a chance near 2e-4.
			Expect(seen).NotTo(HaveKey(p), "%s collides with %s", id, seen[p])
			seen[p] = id
		}
	})

	It("uses every word of the list at about the same rate", func() {
		srv, dev := newPeer(), newPeer()
		counts := map[string]int{}
		for i := range 20000 {
			for w := range strings.FieldsSeq(p2ppair.Phrase(srv, dev, fmt.Sprint(i))) {
				counts[w]++
			}
		}
		// 80000 words over 1024 entries: about 78 each.
		Expect(len(counts)).To(Equal(1024))
	})
})

var _ = Describe("word list", func() {
	It("has 1024 distinct, lower case, short words", func() {
		words := p2ppair.WordList()
		Expect(words).To(HaveLen(1024))
		seen := map[string]bool{}
		for _, w := range words {
			Expect(seen[w]).To(BeFalse(), w)
			seen[w] = true
			Expect(w).To(MatchRegexp(`^[a-z]{3,10}$`))
		}
	})
})
