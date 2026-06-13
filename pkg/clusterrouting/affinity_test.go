package clusterrouting

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("PickWithAffinity", func() {
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	It("returns nil for an empty candidate list", func() {
		Expect(PickWithAffinity(nil, "x", 2)).To(BeNil())
	})

	It("falls back to PickBestReplica when no preferred node is given", func() {
		cs := []ReplicaCandidate{
			{NodeID: "busy", InFlight: 3, LastUsed: ref, AvailableVRAM: 80},
			{NodeID: "idle", InFlight: 0, LastUsed: ref, AvailableVRAM: 8},
		}
		Expect(PickWithAffinity(cs, "", 2).NodeID).To(Equal("idle"))
	})

	It("honors the preferred node when it is within the in-flight slack of the least-loaded", func() {
		cs := []ReplicaCandidate{
			{NodeID: "cold", InFlight: 0, LastUsed: ref, AvailableVRAM: 80},
			{NodeID: "warm", InFlight: 2, LastUsed: ref, AvailableVRAM: 8},
		}
		Expect(PickWithAffinity(cs, "warm", 2).NodeID).To(Equal("warm"))
	})

	It("ignores the preferred node when it is beyond the slack and falls back to load+VRAM", func() {
		cs := []ReplicaCandidate{
			{NodeID: "cold", InFlight: 0, LastUsed: ref, AvailableVRAM: 80},
			{NodeID: "warm", InFlight: 5, LastUsed: ref, AvailableVRAM: 8},
		}
		Expect(PickWithAffinity(cs, "warm", 2).NodeID).To(Equal("cold"))
	})

	It("falls back to load+VRAM when the preferred node is not among the candidates", func() {
		cs := []ReplicaCandidate{
			{NodeID: "a", InFlight: 1, LastUsed: ref, AvailableVRAM: 8},
			{NodeID: "b", InFlight: 1, LastUsed: ref, AvailableVRAM: 24},
		}
		Expect(PickWithAffinity(cs, "ghost", 2).NodeID).To(Equal("b"))
	})
})
