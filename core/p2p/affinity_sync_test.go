package p2p

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
	"github.com/mudler/edgevpn/pkg/hub"
)

var _ = Describe("applyAffinityMessage", func() {
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	observeMsg := func(ev messaging.PrefixCacheObserveEvent) *hub.Message {
		payload, _ := json.Marshal(ev)
		return &hub.Message{
			Message:     string(payload),
			Annotations: map[string]interface{}{affinitySubjectKey: messaging.SubjectPrefixCacheObserve},
		}
	}

	It("applies a peer observe so the local index resolves the warm peer", func() {
		cfg := prefixcache.DefaultConfig()
		idx := prefixcache.NewIndex(cfg)
		sync := prefixcache.NewSync(idx, nil)
		chain := prefixcache.ExtractChain("m1", "a fairly long shared system prompt body for the prefix chain", cfg)

		applyAffinityMessage(sync, observeMsg(messaging.PrefixCacheObserveEvent{Model: "m1", Chain: chain, NodeID: "warm", Replica: 0}), ref)

		d := idx.Decide("m1", chain, []prefixcache.ReplicaKey{{NodeID: "warm"}, {NodeID: "cold"}}, ref)
		Expect(d.HasHot).To(BeTrue())
		Expect(d.Hot.NodeID).To(Equal("warm"))
	})

	It("ignores malformed, unknown-subject, and nil inputs without panicking", func() {
		sync := prefixcache.NewSync(prefixcache.NewIndex(prefixcache.DefaultConfig()), nil)
		applyAffinityMessage(sync, &hub.Message{Message: "not-json", Annotations: map[string]interface{}{affinitySubjectKey: messaging.SubjectPrefixCacheObserve}}, ref)
		applyAffinityMessage(sync, &hub.Message{Message: "{}", Annotations: map[string]interface{}{affinitySubjectKey: "some.other.subject"}}, ref)
		applyAffinityMessage(sync, &hub.Message{Message: "{}"}, ref)
		applyAffinityMessage(nil, observeMsg(messaging.PrefixCacheObserveEvent{Model: "m"}), ref)
		applyAffinityMessage(sync, nil, ref)
		Expect(true).To(BeTrue())
	})
})
