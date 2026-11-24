package upgraderun

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("heldBackOf", func() {
	ended := func(items ...Item) Run { return Run{ID: "r", State: RunFinished, Items: items} }
	kokoro := func(phase Phase, key string) Item {
		return Item{Key: "gallery:kokoro", To: "2", ExpectedTo: "2", Phase: phase, ErrorKey: key}
	}

	It("lets the newest attempt of a release decide", func() {
		held := heldBackOf([]Run{ended(kokoro(PhaseFailed, ErrKeyDownload)), ended(kokoro(PhaseRolledBack, ErrKeyStart))})
		Expect(held.Holds("gallery:kokoro", "2")).To(BeFalse(), "a newer try failed for a reason of the moment")
		held = heldBackOf([]Run{ended(kokoro(PhaseRolledBack, ErrKeyStart)), ended(kokoro(PhaseUpdated, ""))})
		Expect(held.Holds("gallery:kokoro", "2")).To(BeTrue())
	})

	It("ignores a run that still runs", func() {
		r := ended(kokoro(PhaseRolledBack, ErrKeyStart))
		r.State = RunRunning
		Expect(heldBackOf([]Run{r})).To(BeEmpty())
	})
})
