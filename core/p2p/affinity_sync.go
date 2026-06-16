package p2p

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
	"github.com/mudler/edgevpn/pkg/blockchain"
	"github.com/mudler/edgevpn/pkg/hub"
	"github.com/mudler/edgevpn/pkg/node"
	"github.com/mudler/xlog"
)

// affinitySubjectKey is the hub.Message annotation carrying the logical subject
// (observe vs invalidate) so the receiver can dispatch the way a NATS subject
// would. The generic channel has no subject routing, so we carry it ourselves.
const affinitySubjectKey = "subject"

// genericChannelPublisher adapts an edgevpn node's generic broadcast channel to
// the prefixcache publisher interface (Publish(subject, v)). It lets a
// federation server reuse prefixcache.Sync for cross-server affinity coherence
// without NATS: each event is JSON-encoded into a hub.Message and broadcast over
// the generic channel (not the slow blockchain ledger).
type genericChannelPublisher struct {
	node *node.Node
}

// Publish satisfies prefixcache's (unexported) publisher interface structurally.
func (p *genericChannelPublisher) Publish(subject string, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshalling affinity event: %w", err)
	}
	return p.node.PublishMessage(&hub.Message{
		Message:     string(payload),
		Annotations: map[string]interface{}{affinitySubjectKey: subject},
	})
}

// applyAffinityMessage decodes a generic-channel affinity message and applies it
// to sync WITHOUT re-broadcasting (ApplyObserve/ApplyInvalidate). now is the
// local clock so TTL is measured per server. Unknown subjects, malformed
// payloads, and nil inputs are ignored (debug-logged), never fatal.
func applyAffinityMessage(sync *prefixcache.Sync, m *hub.Message, now time.Time) {
	if sync == nil || m == nil {
		return
	}
	subject, _ := m.Annotations[affinitySubjectKey].(string)
	switch subject {
	case messaging.SubjectPrefixCacheObserve:
		var ev messaging.PrefixCacheObserveEvent
		if err := json.Unmarshal([]byte(m.Message), &ev); err != nil {
			xlog.Debug("affinity: bad observe payload", "error", err)
			return
		}
		sync.ApplyObserve(ev, now)
	case messaging.SubjectPrefixCacheInvalidate:
		var ev messaging.PrefixCacheInvalidateEvent
		if err := json.Unmarshal([]byte(m.Message), &ev); err != nil {
			xlog.Debug("affinity: bad invalidate payload", "error", err)
			return
		}
		sync.ApplyInvalidate(ev)
	default:
		// Other generic-channel traffic; not ours.
	}
}

// affinityHandler returns the edgevpn generic-channel handler that applies remote
// affinity events to this server's index. It is registered at node construction
// (handlers cannot be added after Start) and reads fs.prefixSync lazily, which is
// safe because messages only arrive after Start, by which point Start has wired
// fs.prefixSync.
func (fs *FederatedServer) affinityHandler() node.Handler {
	return func(_ *blockchain.Ledger, m *hub.Message, _ chan *hub.Message) error {
		applyAffinityMessage(fs.prefixSync, m, time.Now())
		return nil
	}
}
