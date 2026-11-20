package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("processTreeOf", func() {
	It("returns the root and every descendant", func() {
		snapshot := []processSnapshotEntry{
			{PID: 10, PPID: 1, Created: 100},
			{PID: 11, PPID: 10, Created: 110},
			{PID: 12, PPID: 11, Created: 120},
			{PID: 20, PPID: 1, Created: 100},
		}
		Expect(processTreeOf(10, snapshot)).To(Equal([]int{10, 11, 12}))
	})

	It("skips a child that is older than its recorded parent (PID reuse)", func() {
		snapshot := []processSnapshotEntry{
			{PID: 10, PPID: 1, Created: 500},
			{PID: 30, PPID: 10, Created: 100},
		}
		Expect(processTreeOf(10, snapshot)).To(Equal([]int{10}))
	})

	It("returns only the root when it is not in the snapshot", func() {
		Expect(processTreeOf(42, nil)).To(Equal([]int{42}))
	})

	It("does not loop on a cycle", func() {
		snapshot := []processSnapshotEntry{
			{PID: 10, PPID: 11},
			{PID: 11, PPID: 10},
		}
		Expect(processTreeOf(10, snapshot)).To(Equal([]int{10, 11}))
	})
})
