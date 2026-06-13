package p2p

import (
	"bufio"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
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
		Expect(extractModel("", body)).To(Equal("llama-3"))
	})

	It("prefers a path/query model over the body", func() {
		body := []byte(`{"model":"frombody"}`)
		Expect(extractModel("fromquery", body)).To(Equal("fromquery"))
	})

	It("returns empty when no model is present", func() {
		Expect(extractModel("", []byte(`{"messages":[]}`))).To(Equal(""))
	})

	It("returns empty on non-JSON / unparseable body without panicking", func() {
		Expect(extractModel("", []byte("--multipart-boundary--"))).To(Equal(""))
	})
})

var _ = Describe("affinityPreferred", func() {
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	It("returns the warm peer once a chain has been observed for it", func() {
		cfg := prefixcache.DefaultConfig()
		idx := prefixcache.NewIndex(cfg)
		chain := prefixcache.ExtractChain("m1", `{"model":"m1","messages":[{"role":"system","content":"hello world this is a fairly long shared system prompt"}]}`, cfg)
		idx.Observe("m1", chain, prefixcache.ReplicaKey{NodeID: "warm"}, ref)

		cands := []clusterrouting.ReplicaCandidate{{NodeID: "warm"}, {NodeID: "cold"}}
		Expect(affinityPreferred(idx, "m1", chain, cands, cfg, ref)).To(Equal("warm"))
	})

	It("returns empty when no chain has been observed", func() {
		cfg := prefixcache.DefaultConfig()
		idx := prefixcache.NewIndex(cfg)
		chain := prefixcache.ExtractChain("m1", `{"model":"m1","messages":[{"role":"system","content":"hello world this is a fairly long shared system prompt"}]}`, cfg)
		cands := []clusterrouting.ReplicaCandidate{{NodeID: "warm"}, {NodeID: "cold"}}
		Expect(affinityPreferred(idx, "m1", chain, cands, cfg, ref)).To(Equal(""))
	})

	It("returns empty for a nil index or empty chain", func() {
		cfg := prefixcache.DefaultConfig()
		Expect(affinityPreferred(nil, "m1", []uint64{1}, nil, cfg, ref)).To(Equal(""))
		idx := prefixcache.NewIndex(cfg)
		Expect(affinityPreferred(idx, "m1", nil, nil, cfg, ref)).To(Equal(""))
	})
})

var _ = Describe("L7 request handling", func() {
	It("reads a buffered request and its body under the cap", func() {
		raw := "POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nContent-Length: 28\r\n\r\n" +
			`{"model":"m1","messages":[]}`
		req, body, err := readRequest(bufio.NewReader(strings.NewReader(raw)), 1024)
		Expect(err).ToNot(HaveOccurred())
		Expect(req.URL.Path).To(Equal("/v1/chat/completions"))
		Expect(string(body)).To(ContainSubstring(`"model":"m1"`))
	})

	It("rejects a body over the cap with ErrBodyTooLarge", func() {
		big := strings.Repeat("a", 200)
		raw := "POST /x HTTP/1.1\r\nHost: x\r\nContent-Length: 200\r\n\r\n" + big
		_, _, err := readRequest(bufio.NewReader(strings.NewReader(raw)), 64)
		Expect(err).To(MatchError(ErrBodyTooLarge))
	})

	It("detects a websocket upgrade request", func() {
		raw := "GET /v1/realtime HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"
		req, _, err := readRequest(bufio.NewReader(strings.NewReader(raw)), 1024)
		Expect(err).ToNot(HaveOccurred())
		Expect(isWebsocketUpgrade(req)).To(BeTrue())
	})

	It("does not flag a normal POST as a websocket upgrade", func() {
		raw := "POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nContent-Length: 2\r\n\r\n{}"
		req, _, err := readRequest(bufio.NewReader(strings.NewReader(raw)), 1024)
		Expect(err).ToNot(HaveOccurred())
		Expect(isWebsocketUpgrade(req)).To(BeFalse())
	})
})

var _ = Describe("affinityPreferred with a sync provider", func() {
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	It("returns the warm peer when the provider is a Sync wrapping the index", func() {
		cfg := prefixcache.DefaultConfig()
		idx := prefixcache.NewIndex(cfg)
		sync := prefixcache.NewSync(idx, nil)
		chain := prefixcache.ExtractChain("m1", `{"model":"m1","messages":[{"role":"system","content":"a long shared system prompt for affinity"}]}`, cfg)
		sync.Observe("m1", chain, prefixcache.ReplicaKey{NodeID: "warm"}, ref)

		cands := []clusterrouting.ReplicaCandidate{{NodeID: "warm"}, {NodeID: "cold"}}
		Expect(affinityPreferred(sync, "m1", chain, cands, cfg, ref)).To(Equal("warm"))
	})
})
