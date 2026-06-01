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
		cands := buildFederatedCandidates(nodes, map[string]int{}, ref, "")
		Expect(cands).To(HaveLen(1))
		Expect(cands[0].NodeID).To(Equal("online"))
	})

	It("maps the request counter to InFlight and defaults missing entries to zero", func() {
		nodes := []schema.NodeData{
			{ID: "a", LastSeen: onlineSeen},
			{ID: "b", LastSeen: onlineSeen},
		}
		cands := buildFederatedCandidates(nodes, map[string]int{"a": 4}, ref, "")
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
		cands := buildFederatedCandidates(nodes, map[string]int{}, ref, "")
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
		cands := buildFederatedCandidates(nodes, map[string]int{"busy-big": 3}, ref, "")
		best := clusterrouting.PickBestReplica(cands)
		Expect(best).ToNot(BeNil())
		Expect(best.NodeID).To(Equal("idle-big"))
	})
})

var _ = Describe("model-aware candidate building", func() {
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seen := ref.Add(-10 * time.Second)

	It("keeps peers that advertise the requested model", func() {
		nodes := []schema.NodeData{
			{ID: "has", LastSeen: seen, Models: []string{"m1", "m2"}},
			{ID: "hasnot", LastSeen: seen, Models: []string{"other"}},
		}
		cands := buildFederatedCandidates(nodes, map[string]int{}, ref, "m1")
		Expect(cands).To(HaveLen(1))
		Expect(cands[0].NodeID).To(Equal("has"))
	})

	It("keeps peers with an empty (unknown) model set eligible for any model", func() {
		nodes := []schema.NodeData{
			{ID: "unknown", LastSeen: seen, Models: nil},
			{ID: "hasnot", LastSeen: seen, Models: []string{"other"}},
		}
		cands := buildFederatedCandidates(nodes, map[string]int{}, ref, "m1")
		Expect(cands).To(HaveLen(1))
		Expect(cands[0].NodeID).To(Equal("unknown"))
	})

	It("does not filter when the requested model is empty", func() {
		nodes := []schema.NodeData{
			{ID: "a", LastSeen: seen, Models: []string{"x"}},
			{ID: "b", LastSeen: seen, Models: []string{"y"}},
		}
		cands := buildFederatedCandidates(nodes, map[string]int{}, ref, "")
		Expect(cands).To(HaveLen(2))
	})
})

var _ = Describe("extractModel", func() {
	It("reads the JSON body model field", func() {
		body := []byte(`{"model":"llama-3","messages":[]}`)
		Expect(extractModel("/v1/chat/completions", "", body)).To(Equal("llama-3"))
	})

	It("prefers a path/query model over the body", func() {
		body := []byte(`{"model":"frombody"}`)
		Expect(extractModel("/x", "fromquery", body)).To(Equal("fromquery"))
	})

	It("returns empty when no model is present", func() {
		Expect(extractModel("/x", "", []byte(`{"messages":[]}`))).To(Equal(""))
	})

	It("returns empty on non-JSON / unparseable body without panicking", func() {
		Expect(extractModel("/x", "", []byte("--multipart-boundary--"))).To(Equal(""))
	})
})
