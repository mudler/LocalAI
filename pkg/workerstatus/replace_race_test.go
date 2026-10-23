// SPDX-License-Identifier: MIT
package workerstatus

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A reader that opened the snapshot just before it was replaced holds an inode
// with no links left. That file was complete and owner-only, so it is valid
// evidence; only an extra hard link (more than one) is a reason to refuse it.
var _ = Describe("Snapshot replacement", func() {
	It("stays readable for a reader that opened the previous file", func() {
		for round := 0; round < 200; round++ {
			path, generation, err := NewSession(GinkgoT().TempDir())
			Expect(err).ToNot(HaveOccurred())
			s := Snapshot{PID: 1, Schema: Schema, Generation: generation, Sequence: 1, Time: time.Now(), Registration: "starting", HeartbeatInterval: 10 * time.Second}
			Expect(Write(path, s)).To(Succeed())
			var wg sync.WaitGroup
			stop := make(chan struct{})
			wg.Go(func() {
				defer GinkgoRecover()
				for {
					select {
					case <-stop:
						return
					default:
						s.Sequence++
						Expect(Write(path, s)).To(Succeed())
					}
				}
			})
			for i := 0; i < 40; i++ {
				_, err := Read(path, generation)
				Expect(err).ToNot(HaveOccurred(), "round %d read %d", round, i)
			}
			close(stop)
			wg.Wait()
		}
	})
})
