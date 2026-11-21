package nodes

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CarrierWeightSource reads the carrier weights of the cluster. It runs on every
// frontend against the shared settings, so all of them rank alike.
type CarrierWeightSource func(ctx context.Context) (cluster.CarrierWeights, error)

// carrierRankTTL is how long a frontend keeps the weights and the ranks of the
// nodes. A change of a weight, or a worker that changes its route, is seen after
// at most this time, with no restart.
const carrierRankTTL = 2 * time.Second

// nodeRank is what the router knows about a node's way in.
type nodeRank struct {
	// weight is the highest weight among the routes of the node.
	weight int
	// limitedOnly is true when every route of the node is a tunnel over a
	// data-limited public relay.
	limitedOnly bool
}

// WeightOfRoute is the weight of one route: NATS for a direct route, and for a
// tunnel the weight of its transport.
func WeightOfRoute(rt Route, w cluster.CarrierWeights) int {
	if rt.Kind == RouteDirect {
		return w.NATS
	}
	if rt.Transport == TunnelP2P {
		return w.P2P
	}
	return w.WebSocket
}

// rankOf ranks a node by its routes: its weight is the highest weight of a
// route, and it is limited-only when it has routes and all of them are
// data-limited tunnels. A node with no route has weight 0 and is not
// limited-only; the router drops it earlier (narrowByRoutable).
func rankOf(n *BackendNode, w cluster.CarrierWeights) nodeRank {
	routes := RoutesOf(n)
	r := nodeRank{limitedOnly: len(routes) > 0}
	for _, rt := range routes {
		r.weight = max(r.weight, WeightOfRoute(rt, w))
		if rt.Kind != RouteTunnel || !rt.Limited {
			r.limitedOnly = false
		}
	}
	return r
}

type rankCache struct {
	mu    sync.Mutex
	at    time.Time
	ranks map[string]nodeRank
}

// SetCarrierWeights turns on the tie-break by carrier. With no source the
// registry orders nodes as it always did. NewSmartRouter calls it once, before
// any request.
func (r *NodeRegistry) SetCarrierWeights(src CarrierWeightSource) {
	r.weightSource.Store(&src)
	r.ranks.mu.Lock()
	r.ranks.ranks = nil
	r.ranks.mu.Unlock()
}

// nodeRanks returns the rank of every backend node, or nil when no source is
// set or the nodes cannot be read (the order is then the plain one).
func (r *NodeRegistry) nodeRanks(ctx context.Context) map[string]nodeRank {
	srcp := r.weightSource.Load()
	if srcp == nil || *srcp == nil {
		return nil
	}
	c := &r.ranks
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ranks != nil && time.Since(c.at) < carrierRankTTL {
		return c.ranks
	}
	weights, err := (*srcp)(ctx)
	if err != nil {
		xlog.Warn("Could not read the carrier weights; ranking with the defaults", "error", err)
		weights = cluster.DefaultCarrierWeights
	}
	var nodes []BackendNode
	if err := r.db.WithContext(ctx).Where("node_type = ?", NodeTypeBackend).Find(&nodes).Error; err != nil {
		xlog.Warn("Could not read the nodes to rank them by carrier; ordering without it", "error", err)
		return nil
	}
	c.ranks = make(map[string]nodeRank, len(nodes))
	for i := range nodes {
		c.ranks[nodes[i].ID] = rankOf(&nodes[i], weights)
	}
	c.at = time.Now()
	return c.ranks
}

// rankTerms are the ORDER BY terms that the ranks add, with their values. A term
// is empty when it would change nothing: every node has the same weight, or no
// node is limited-only. Then the query stays as it was.
type rankTerms struct {
	limited     string
	limitedVars []any
	weight      string
	weightVars  []any
}

// rankTermsFor builds the terms on the column idCol that holds the node id.
// Nodes are listed in id order so that the SQL is the same for the same ranks.
func (r *NodeRegistry) rankTermsFor(ctx context.Context, idCol string) rankTerms {
	ranks := r.nodeRanks(ctx)
	if len(ranks) == 0 {
		return rankTerms{}
	}
	ids := make([]string, 0, len(ranks))
	for id := range ranks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var t rankTerms
	var lim, wt strings.Builder
	differs := false
	for _, id := range ids {
		if ranks[id].weight != ranks[ids[0]].weight {
			differs = true
		}
	}
	for _, id := range ids {
		rk := ranks[id]
		if rk.limitedOnly {
			lim.WriteString(" WHEN ? THEN 1")
			t.limitedVars = append(t.limitedVars, id)
		}
		if differs {
			wt.WriteString(" WHEN ? THEN ?")
			t.weightVars = append(t.weightVars, id, rk.weight)
		}
	}
	if lim.Len() > 0 {
		t.limited = "CASE " + idCol + lim.String() + " ELSE 0 END ASC"
	}
	if differs {
		t.weight = "CASE " + idCol + wt.String() + " ELSE 0 END DESC"
	}
	return t
}

// weightTail is a scope that orders by base, then by carrier weight, then by id.
// The weight only separates nodes that base leaves equal. With no weight to
// apply it is Order(base) and nothing else.
func (r *NodeRegistry) weightTail(ctx context.Context, base string) func(*gorm.DB) *gorm.DB {
	return func(q *gorm.DB) *gorm.DB {
		// The scope runs after First has added its own ORDER BY on the primary key,
		// and a column added now would come after that one. An expression replaces
		// it, so the id is named here as the last key, as First would have put it.
		t := r.rankTermsFor(ctx, "backend_nodes.id")
		sql := base + ", backend_nodes.id ASC"
		if t.weight != "" {
			sql = base + ", " + t.weight + ", backend_nodes.id ASC"
		}
		return q.Clauses(clause.OrderBy{Expression: clause.Expr{SQL: sql, Vars: t.weightVars}})
	}
}

// warmOrder orders the loaded replicas of a model, for the warm path.
//
//  1. A replica on a limited-only node (a data-limited public relay) comes last:
//     it is a last resort, used only when no other replica is loaded.
//  2. in_flight ASC: the least busy replica. This is fit.
//  3. The carrier weight, highest first, among replicas that are equally busy.
//  4. last_used ASC: round-robin among replicas that are still equal, as before.
//  5. available_vram DESC, then id.
//
// With no ranks to apply it is the plain order of before the weights. It must be
// applied through Scopes: First adds its own ORDER BY on the primary key, and an
// expression added before it would be dropped.
func (r *NodeRegistry) warmOrder(ctx context.Context) clause.OrderBy {
	const plain = "node_models.in_flight ASC, node_models.last_used ASC, backend_nodes.available_vram DESC"
	t := r.rankTermsFor(ctx, "node_models.node_id")
	if t.limited == "" && t.weight == "" {
		return clause.OrderBy{Expression: clause.Expr{SQL: plain + ", node_models.id ASC"}}
	}
	var sql []string
	var vars []any
	if t.limited != "" {
		sql, vars = append(sql, t.limited), append(vars, t.limitedVars...)
	}
	sql = append(sql, "node_models.in_flight ASC")
	if t.weight != "" {
		sql, vars = append(sql, t.weight), append(vars, t.weightVars...)
	}
	sql = append(sql, "node_models.last_used ASC", "backend_nodes.available_vram DESC", "node_models.id ASC")
	return clause.OrderBy{Expression: clause.Expr{SQL: strings.Join(sql, ", "), Vars: vars}}
}

// nodeRanker is what the router needs of the registry to rank nodes.
type nodeRanker interface {
	nodeRanks(ctx context.Context) map[string]nodeRank
}

func (r *SmartRouter) nodeRanks(ctx context.Context) map[string]nodeRank {
	if r.ranker == nil {
		return nil
	}
	return r.ranker.nodeRanks(ctx)
}

// limitedOnlyNodes are the nodes whose every route is a tunnel over a
// data-limited public relay (Route.Limited). The scheduler uses them as a last
// resort: only when no other candidate fits, and never for a placement that
// pushes model files to the node. nil when no weight source is set.
func (r *SmartRouter) limitedOnlyNodes(ctx context.Context) map[string]struct{} {
	var out map[string]struct{}
	for id, rk := range r.nodeRanks(ctx) {
		if rk.limitedOnly {
			if out == nil {
				out = map[string]struct{}{}
			}
			out[id] = struct{}{}
		}
	}
	return out
}

// withoutNodes returns candidates without the nodes in drop. A nil candidates
// means every node, so the result is every ranked node that is not in drop. The
// result is empty, not nil, when nothing is left.
func (r *SmartRouter) withoutNodes(ctx context.Context, candidates []string, drop map[string]struct{}) []string {
	base := candidates
	if base == nil {
		for id := range r.nodeRanks(ctx) {
			base = append(base, id)
		}
		slices.Sort(base)
	}
	keep := make([]string, 0, len(base))
	for _, id := range base {
		if _, no := drop[id]; !no {
			keep = append(keep, id)
		}
	}
	return keep
}

// stagesOverRelay says whether loading this model on a node sends its files
// through the route to that node. It does not when every node mounts the same
// models directory (shared models), when the files go through an object store
// that the worker reads itself (S3), when no stager is set, or when the model
// has no local file to push.
func (r *SmartRouter) stagesOverRelay(modelID string, opts *pb.ModelOptions) bool {
	if r.fileStager == nil || r.sharedModels || r.stagingThroughStore {
		return false
	}
	return r.stagingPayloadBytes(modelID, opts) > 0
}
