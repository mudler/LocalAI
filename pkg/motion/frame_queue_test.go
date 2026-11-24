// SPDX-License-Identifier: MIT
package motion

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
	"time"
)

func TestFrameQueue(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Motion frame queue") }

var _ = Describe("Bounded temporal frame queue", func() {
	It("thins across the pending span, preserving endpoints and earlier ties", func() {
		q := &FrameQueue{}
		now := time.Now()
		var released []uint64

		for i := 1; i <= 6; i++ {
			var pressure bool
			released, pressure = q.Offer(QueuedFrame{Sequence: uint64(i), SourceTime: int64(i), Data: []byte{1}, Arrived: now})
			Expect(pressure).To(Equal(i == 6))
		}

		Expect(released).To(Equal([]uint64{2, 4, 5}))
		var kept []uint64

		for {
			f, ok := q.Take()
			if !ok {
				break
			}
			kept = append(kept, f.Sequence)
		}

		Expect(kept).To(Equal([]uint64{1, 3, 6}))
	})
	It("uses timestamp spacing instead of index spacing for irregular captures", func() {
		q := &FrameQueue{}

		for i, stamp := range []int64{0, 1, 2, 50, 99, 100} {
			q.Offer(QueuedFrame{Sequence: uint64(i + 1), SourceTime: stamp, Data: []byte{1}, Arrived: time.Now()})
		}
		var kept []uint64

		for {
			f, ok := q.Take()
			if !ok {
				break
			}
			kept = append(kept, f.Sequence)
		}

		Expect(kept).To(Equal([]uint64{1, 4, 6}))
	})
	It("bounds bytes under repeated large uploads and expires by server arrival time", func() {
		q := &FrameQueue{}
		now := time.Now()
		data := make([]byte, UploadFrameMaxBytes)

		for i := 1; i <= 100; i++ {
			q.Offer(QueuedFrame{Sequence: uint64(i), Data: data, Arrived: now})
			Expect(q.Len()).To(BeNumerically("<=", UploadWindow))
			Expect(q.Bytes()).To(BeNumerically("<", 6<<20))
		}

		Expect(q.Expire(now.Add(UploadMaxQueueAge))).NotTo(BeEmpty())

		Expect(q.Len()).To(BeZero())
		Expect(q.Bytes()).To(BeZero())
		Expect(q.Dropped).To(Equal(uint64(100)))
	})
})
