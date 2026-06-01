package p2p

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/clusterrouting"
)

var _ = Describe("buildFederatedCandidates", func() {
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	onlineSeen := ref.Add(-10 * time.Second)
	offlineSeen := ref.Add(-2 * time.Minute)

	It("excludes offline nodes", func() {
		nodes := []schema.NodeData{
			{ID: "online", LastSeen: onlineSeen},
			{ID: "offline", LastSeen: offlineSeen},
		}
		cands := buildFederatedCandidates(nodes, map[string]int{}, ref)
		Expect(cands).To(HaveLen(1))
		Expect(cands[0].NodeID).To(Equal("online"))
	})

	It("maps the request counter to InFlight and defaults missing entries to zero", func() {
		nodes := []schema.NodeData{
			{ID: "a", LastSeen: onlineSeen},
			{ID: "b", LastSeen: onlineSeen},
		}
		cands := buildFederatedCandidates(nodes, map[string]int{"a": 4}, ref)
		byID := map[string]int{}
		for _, c := range cands {
			byID[c.NodeID] = c.InFlight
		}
		Expect(byID["a"]).To(Equal(4))
		Expect(byID["b"]).To(Equal(0))
	})

	It("carries gossiped AvailableVRAM into the candidate", func() {
		nodes := []schema.NodeData{
			{ID: "gpu", LastSeen: onlineSeen, AvailableVRAM: 24_000_000_000},
		}
		cands := buildFederatedCandidates(nodes, map[string]int{}, ref)
		Expect(cands[0].AvailableVRAM).To(Equal(uint64(24_000_000_000)))
	})

	It("produces candidates the shared policy ranks by least in-flight then most VRAM", func() {
		// busy-big has the most VRAM but is busy, so it must lose. Among the two
		// idle peers, the one with more free VRAM wins (VRAM breaks the tie).
		nodes := []schema.NodeData{
			{ID: "busy-big", LastSeen: onlineSeen, AvailableVRAM: 80_000_000_000},
			{ID: "idle-small", LastSeen: onlineSeen, AvailableVRAM: 8_000_000_000},
			{ID: "idle-big", LastSeen: onlineSeen, AvailableVRAM: 24_000_000_000},
		}
		cands := buildFederatedCandidates(nodes, map[string]int{"busy-big": 3}, ref)
		best := clusterrouting.PickBestReplica(cands)
		Expect(best).ToNot(BeNil())
		Expect(best.NodeID).To(Equal("idle-big"))
	})
})
