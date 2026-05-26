package pii

import (
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Redactor_SetActionConcurrentRedact pins the SetAction copy-on-
// write contract: concurrent SetAction must not race with readers
// iterating an older patterns snapshot. Run with -race to surface the
// regression that motivated the COW (in-place mutation of the
// per-element Action string is not atomic).
var _ = Describe("Redactor", func() {
	It("SetAction concurrent with Redact", func() {
		patterns, err := Compile(DefaultPatterns())
		Expect(err).NotTo(HaveOccurred(), "compile")
		r := NewRedactor(patterns)

		const writers = 4
		const readers = 8
		const iter = 100

		var wg sync.WaitGroup
		stop := make(chan struct{})

		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < iter; i++ {
					select {
					case <-stop:
						return
					default:
					}
					action := ActionMask
					if i%2 == 0 {
						action = ActionBlock
					}
					_ = r.SetAction("email", action)
				}
			}()
		}

		for rd := 0; rd < readers; rd++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				text := "contact alice@example.com please"
				for i := 0; i < iter*2; i++ {
					select {
					case <-stop:
						return
					default:
					}
					_ = r.Redact(text)
				}
			}()
		}

		wg.Wait()
		close(stop)
	})
})
