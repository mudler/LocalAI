package nodes

import (
	"context"
	"sync"
)

// loadHistorySamples bounds the per-node ring. At the usual heartbeat interval
// it covers a few minutes, enough for a trend line and nothing more.
const loadHistorySamples = 30

// loadHistory keeps the most recent load readings of each node in memory. It is
// a display aid for the node list, not a metrics store: it is empty after a
// restart of the controller and is never persisted. The zero value is ready.
type loadHistory struct {
	mu     sync.Mutex
	rings  map[string]*loadRing
	vramOf map[string]uint64 // total VRAM per node, to turn free bytes into a percent
}

type loadRing struct {
	samples [loadHistorySamples]float64
	next    int // slot the next sample is written to
	count   int
}

func (h *loadHistory) add(nodeID string, percent float64) {
	percent = clampCPUUsage(percent)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rings == nil {
		h.rings = make(map[string]*loadRing)
	}
	ring := h.rings[nodeID]
	if ring == nil {
		ring = &loadRing{}
		h.rings[nodeID] = ring
	}
	ring.samples[ring.next] = percent
	ring.next = (ring.next + 1) % loadHistorySamples
	if ring.count < loadHistorySamples {
		ring.count++
	}
}

// snapshot returns the samples oldest first. It returns nil for a node with
// none, so the JSON field is omitted for workers that never reported load.
func (h *loadHistory) snapshot(nodeID string) []float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	ring := h.rings[nodeID]
	if ring == nil || ring.count == 0 {
		return nil
	}
	out := make([]float64, ring.count)
	start := (ring.next - ring.count + loadHistorySamples) % loadHistorySamples
	for i := range out {
		out[i] = ring.samples[(start+i)%loadHistorySamples]
	}
	return out
}

func (h *loadHistory) forget(nodeID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.rings, nodeID)
	delete(h.vramOf, nodeID)
}

func (h *loadHistory) totalVRAM(nodeID string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.vramOf[nodeID]
}

func (h *loadHistory) setTotalVRAM(nodeID string, total uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.vramOf == nil {
		h.vramOf = make(map[string]uint64)
	}
	h.vramOf[nodeID] = total
}

// recordLoad samples one heartbeat: the used share of VRAM on a node that
// reports it, otherwise the CPU usage. A beat with neither adds nothing, so an
// older worker simply has no history.
func (r *NodeRegistry) recordLoad(ctx context.Context, nodeID string, update *HeartbeatUpdate) {
	if update == nil {
		return
	}
	if update.TotalVRAM != nil && *update.TotalVRAM > 0 {
		r.loadHist.setTotalVRAM(nodeID, *update.TotalVRAM)
	}
	if update.AvailableVRAM != nil {
		total := r.loadHist.totalVRAM(nodeID)
		if total == 0 {
			// Heartbeats carry only the free figure; the total is read once.
			var stored uint64
			r.db.WithContext(ctx).Model(&BackendNode{}).
				Select(ColTotalVRAM).Where("id = ?", nodeID).Scan(&stored)
			if stored > 0 {
				total = stored
				r.loadHist.setTotalVRAM(nodeID, total)
			}
		}
		if total > 0 {
			free := min(*update.AvailableVRAM, total)
			r.loadHist.add(nodeID, float64(total-free)/float64(total)*100)
		}
		return
	}
	if update.CPUUsagePercent != nil {
		r.loadHist.add(nodeID, *update.CPUUsagePercent)
	}
}
