package clusterrouting

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("PickBestReplica", func() {
	// Use a single reference time so every test that wants identical
	// last_used can share it without relying on time.Now() interleavings.
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	It("returns nil for an empty candidate list", func() {
		Expect(PickBestReplica(nil)).To(BeNil())
		Expect(PickBestReplica([]ReplicaCandidate{})).To(BeNil())
	})

	It("returns the only candidate when there is just one", func() {
		only := ReplicaCandidate{NodeID: "only", InFlight: 99, LastUsed: ref, AvailableVRAM: 1}
		pick := PickBestReplica([]ReplicaCandidate{only})
		Expect(pick).ToNot(BeNil())
		Expect(pick.NodeID).To(Equal("only"))
	})

	It("prefers the replica with the lowest in_flight", func() {
		// Without the in-flight tier, the larger-VRAM node would win.
		cs := []ReplicaCandidate{
			{NodeID: "busy-big", InFlight: 3, LastUsed: ref, AvailableVRAM: 24_000_000_000},
			{NodeID: "idle-small", InFlight: 0, LastUsed: ref, AvailableVRAM: 8_000_000_000},
			{NodeID: "mid", InFlight: 1, LastUsed: ref, AvailableVRAM: 16_000_000_000},
		}
		Expect(PickBestReplica(cs).NodeID).To(Equal("idle-small"))
	})

	It("uses oldest last_used as the tiebreaker when in_flight ties", func() {
		// All three tied on in_flight=0. Without last_used, available_vram
		// would pin every pick to the fattest node: the exact bug
		// fix(distributed): round-robin replicas of the same model addressed.
		cs := []ReplicaCandidate{
			{NodeID: "fat-recent", InFlight: 0, LastUsed: ref.Add(2 * time.Second), AvailableVRAM: 24_000_000_000},
			{NodeID: "small-oldest", InFlight: 0, LastUsed: ref, AvailableVRAM: 8_000_000_000},
			{NodeID: "mid-middle", InFlight: 0, LastUsed: ref.Add(1 * time.Second), AvailableVRAM: 16_000_000_000},
		}
		Expect(PickBestReplica(cs).NodeID).To(Equal("small-oldest"))
	})

	It("uses largest available_vram as the final tiebreaker", func() {
		// in_flight tied AND last_used tied: pick the largest GPU.
		cs := []ReplicaCandidate{
			{NodeID: "small", InFlight: 0, LastUsed: ref, AvailableVRAM: 8_000_000_000},
			{NodeID: "fat", InFlight: 0, LastUsed: ref, AvailableVRAM: 24_000_000_000},
			{NodeID: "mid", InFlight: 0, LastUsed: ref, AvailableVRAM: 16_000_000_000},
		}
		Expect(PickBestReplica(cs).NodeID).To(Equal("fat"))
	})

	It("respects tier precedence: in_flight beats last_used beats available_vram", func() {
		// "fat-busy-oldest" wins on neither of the first two tiers; the
		// "small-idle-recent" replica is busy=0 and should beat it despite
		// being newer and smaller.
		cs := []ReplicaCandidate{
			{NodeID: "fat-busy-oldest", InFlight: 5, LastUsed: ref, AvailableVRAM: 80_000_000_000},
			{NodeID: "small-idle-recent", InFlight: 0, LastUsed: ref.Add(time.Hour), AvailableVRAM: 4_000_000_000},
		}
		Expect(PickBestReplica(cs).NodeID).To(Equal("small-idle-recent"))
	})

	It("is stable: returns the first candidate when every field ties", func() {
		// betterReplica returns false on a full tie, so the leading element
		// remains best. Callers shouldn't depend on this for correctness,
		// but pinning the behavior here catches accidental reorderings.
		cs := []ReplicaCandidate{
			{NodeID: "first", InFlight: 0, LastUsed: ref, AvailableVRAM: 8_000_000_000},
			{NodeID: "second", InFlight: 0, LastUsed: ref, AvailableVRAM: 8_000_000_000},
			{NodeID: "third", InFlight: 0, LastUsed: ref, AvailableVRAM: 8_000_000_000},
		}
		Expect(PickBestReplica(cs).NodeID).To(Equal("first"))
	})
})
