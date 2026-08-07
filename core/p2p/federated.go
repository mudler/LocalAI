package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
	"github.com/mudler/LocalAI/pkg/clusterrouting"
	"github.com/mudler/xlog"
)

const FederatedID = "federated"

func NetworkID(networkID, serviceID string) string {
	if networkID != "" {
		return fmt.Sprintf("%s_%s", networkID, serviceID)
	}
	return serviceID
}

type FederatedServer struct {
	sync.Mutex
	listenAddr, service, p2ptoken string
	requestTable                  map[string]int
	loadBalanced                  bool
	workerTarget                  string
	bodyLimit                     int64 // max request body bytes (0 = unlimited)
	prefixCfg                     prefixcache.Config
	prefixIndex                   *prefixcache.Index
	prefixSync                    *prefixcache.Sync
	prefixProvider                prefixcache.Provider // Index (sync off) or Sync (sync on)
	syncAffinity                  bool
}

func NewFederatedServer(listenAddr, service, p2pToken string, loadBalanced bool, workerTarget string, bodyLimit int64, syncAffinity bool) *FederatedServer {
	cfg := prefixcache.DefaultConfig()
	idx := prefixcache.NewIndex(cfg)
	return &FederatedServer{
		listenAddr:     listenAddr,
		service:        service,
		p2ptoken:       p2pToken,
		requestTable:   map[string]int{},
		loadBalanced:   loadBalanced,
		workerTarget:   workerTarget,
		bodyLimit:      bodyLimit,
		prefixCfg:      cfg,
		prefixIndex:    idx,
		prefixProvider: idx,
		syncAffinity:   syncAffinity,
	}
}

func (fs *FederatedServer) RandomServer() string {
	var tunnelAddresses []string
	for _, v := range GetAvailableNodes(fs.service) {
		if v.IsOnline() {
			tunnelAddresses = append(tunnelAddresses, v.ID)
		} else {
			delete(fs.requestTable, v.ID) // make sure it's not tracked
			xlog.Info("Node is offline", "node", v.ID)
		}
	}

	if len(tunnelAddresses) == 0 {
		return ""
	}

	return tunnelAddresses[rand.IntN(len(tunnelAddresses))]
}

func (fs *FederatedServer) syncTableStatus() {
	fs.Lock()
	defer fs.Unlock()
	currentTunnels := make(map[string]struct{})

	for _, v := range GetAvailableNodes(fs.service) {
		if v.IsOnline() {
			fs.ensureRecordExist(v.ID)
			currentTunnels[v.ID] = struct{}{}
		}
	}

	// delete tunnels that don't exist anymore
	for t := range fs.requestTable {
		if _, ok := currentTunnels[t]; !ok {
			delete(fs.requestTable, t)
		}
	}
}

// buildFederatedCandidates maps the currently-online federated peers into the
// shared routing policy's candidate form, optionally filtered to peers that can
// serve model. A peer with a non-empty advertised model set that lacks model is
// excluded; a peer with an empty set is treated as "unknown" and stays eligible
// (so older peers and mid-convergence peers are not starved). When model is "",
// no model filtering is applied. InFlight comes from the per-peer request
// counter; AvailableVRAM from the gossiped NodeData; LastUsed is left zero.
func buildFederatedCandidates(nodes []schema.NodeData, requestTable map[string]int, now time.Time, model string) []clusterrouting.ReplicaCandidate {
	candidates := make([]clusterrouting.ReplicaCandidate, 0, len(nodes))
	for _, nd := range nodes {
		if !nd.IsOnlineAt(now) {
			continue
		}
		if !servesModel(nd, model) {
			continue
		}
		candidates = append(candidates, clusterrouting.ReplicaCandidate{
			NodeID:        nd.ID,
			InFlight:      requestTable[nd.ID],
			AvailableVRAM: nd.AvailableVRAM,
		})
	}
	return candidates
}

// servesModel reports whether nd is eligible to serve model. An empty model
// means "no filter". An empty advertised set means "unknown" and is eligible.
func servesModel(nd schema.NodeData, model string) bool {
	if model == "" || len(nd.Models) == 0 {
		return true
	}
	for _, m := range nd.Models {
		if m == model {
			return true
		}
	}
	return false
}

// extractModel best-effort resolves the target model of a buffered request,
// cheapest source first: an explicit query value, then the JSON body "model"
// field. Returns "" when it cannot be determined (for example a multipart or
// websocket request), in which case the caller routes by load/affinity only.
func extractModel(queryModel string, body []byte) string {
	if strings.TrimSpace(queryModel) != "" {
		return queryModel
	}
	if len(body) == 0 {
		return ""
	}
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	return probe.Model
}

// affinityPreferred returns the peer the prefix index considers warm for this
// chain, or "" when there is no match strong enough among the candidates. It
// reuses prefixcache's per-model radix-tree Decide; the final load-guarded pick
// is done by clusterrouting.PickWithAffinity so the VRAM tier is preserved.
func affinityPreferred(idx prefixcache.Provider, model string, chain []uint64, candidates []clusterrouting.ReplicaCandidate, cfg prefixcache.Config, now time.Time) string {
	if idx == nil || len(chain) == 0 || len(candidates) == 0 {
		return ""
	}
	keys := make([]prefixcache.ReplicaKey, 0, len(candidates))
	for _, c := range candidates {
		keys = append(keys, prefixcache.ReplicaKey{NodeID: c.NodeID})
	}
	d := idx.Decide(model, chain, keys, now)
	if d.HasHot && d.MatchRatio >= cfg.MinPrefixMatch {
		return d.Hot.NodeID
	}
	return ""
}

// selectPeer chooses the federated peer to serve a request for model with the
// given raw body. It filters candidates by model, computes the prefix chain,
// consults the affinity index, and makes the final load+VRAM-aware pick. It
// returns the chosen peer ID and the chain (so the caller can Observe after a
// successful forward). An empty model and nil body degrade to load+VRAM only.
// Returns "" when no eligible peer is online.
func (fs *FederatedServer) selectPeer(model string, body []byte, now time.Time) (string, []uint64) {
	fs.syncTableStatus()
	nodes := GetAvailableNodes(fs.service)
	// Snapshot candidates under the lock (it only guards requestTable), then
	// release before the prefix hashing and tree walk, which are lock-free
	// (candidates is a copy; prefixIndex/prefixCfg are set once at construction).
	fs.Lock()
	candidates := buildFederatedCandidates(nodes, fs.requestTable, now, model)
	fs.Unlock()
	if len(candidates) == 0 {
		return "", nil
	}
	var chain []uint64
	preferred := ""
	if fs.prefixProvider != nil && model != "" && len(body) > 0 {
		chain = prefixcache.ExtractChain(model, string(body), fs.prefixCfg)
		preferred = affinityPreferred(fs.prefixProvider, model, chain, candidates, fs.prefixCfg, now)
	}
	best := clusterrouting.PickWithAffinity(candidates, preferred, fs.prefixCfg.BalanceAbsThreshold)
	if best == nil {
		return "", chain
	}
	return best.NodeID, chain
}

// observeServed records that peerID served the given chain for model, so the
// next request sharing that prefix is routed back to the same warm peer.
func (fs *FederatedServer) observeServed(model string, chain []uint64, peerID string, now time.Time) {
	if fs.prefixProvider == nil || len(chain) == 0 || peerID == "" || model == "" {
		return
	}
	fs.prefixProvider.Observe(model, chain, prefixcache.ReplicaKey{NodeID: peerID}, now)
}

// evictLoop periodically sweeps expired affinity entries so the in-memory tree
// does not grow unbounded. Runs for the lifetime of the proxy.
func (fs *FederatedServer) evictLoop(ctx context.Context) {
	interval := fs.prefixCfg.TTL / 2
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			fs.prefixProvider.Evict(now)
		}
	}
}

func (fs *FederatedServer) RecordRequest(nodeID string) {
	fs.Lock()
	defer fs.Unlock()
	// increment the counter for the nodeID in the requestTable
	fs.requestTable[nodeID]++

	xlog.Debug("Recording request", "request_table", fs.requestTable, "request", nodeID)
}

func (fs *FederatedServer) ensureRecordExist(nodeID string) {
	// if the nodeID is not in the requestTable, add it with a counter of 0
	_, ok := fs.requestTable[nodeID]
	if !ok {
		fs.requestTable[nodeID] = 0
	}

	xlog.Debug("Ensure record exists", "request_table", fs.requestTable, "request", nodeID)
}
