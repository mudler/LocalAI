package nodes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// ErrInsufficientDisk reports that no candidate node has enough free space on
// its models filesystem to store the model being scheduled.
//
// This is a scheduling-time verdict on purpose. Before it existed, a worker
// whose models filesystem was 100% full still advertised `status: healthy`,
// was picked to host a 70GB model, accepted the staging request, streamed
// ~17GB and only then failed with "no space left on device" — sixteen minutes
// after a decision that could never have succeeded.
var ErrInsufficientDisk = errors.New("no node has enough free disk for the model")

const (
	// diskHeadroomMarginRatio is the fraction of the payload kept free on top
	// of the payload itself. Staging is not the only writer on that
	// filesystem (backend installs, logs, the backend's own scratch files),
	// and the payload figure is a floor rather than an exact prediction.
	diskHeadroomMarginRatio = 0.05

	// diskHeadroomMinMarginBytes floors the proportional margin so small
	// models still leave usable space behind them.
	diskHeadroomMinMarginBytes = uint64(1) << 30 // 1 GiB

	// diskHeadroomUnknownSizeBytes is what we demand when the model's payload
	// cannot be sized locally (a bare HuggingFace repo id the worker will
	// fetch itself). Deliberately small: it is a "this filesystem is not
	// wedged" floor, not a capacity estimate. Demanding more would strand
	// small-but-healthy nodes on every model whose size we cannot see.
	diskHeadroomUnknownSizeBytes = uint64(2) << 30 // 2 GiB
)

// DiskRequirementFor returns the free bytes a node must have on its models
// filesystem before it may be handed a model whose staged payload is
// payloadBytes (as computed by modelPayloadBytes).
//
// The requirement is derived from the ACTUAL model size rather than from a
// fixed percentage of the node's disk. A percentage threshold would take a
// small node out of rotation for models it could comfortably hold, which on a
// homogeneous cluster strands every node at once.
func DiskRequirementFor(payloadBytes int64) uint64 {
	if payloadBytes <= 0 {
		return diskHeadroomUnknownSizeBytes
	}
	payload := uint64(payloadBytes)
	margin := uint64(float64(payload) * diskHeadroomMarginRatio)
	if margin < diskHeadroomMinMarginBytes {
		margin = diskHeadroomMinMarginBytes
	}
	return payload + margin
}

// reportsDisk says whether a node's disk figures are usable.
//
// TotalDisk is the "does this worker report disk at all" bit, NOT
// AvailableDisk: a 100%-full node legitimately reports available == 0, and
// treating that as unknown would reinstate exactly the bug this guards
// against. A worker predating the field (or one whose stat failed) reports
// total == 0 and is passed through untouched, so a rolling upgrade never
// empties the candidate pool.
func (n BackendNode) reportsDisk() bool { return n.TotalDisk > 0 }

// nodesWithDiskHeadroom filters candidates down to those that can actually
// store a model needing `required` free bytes on their models filesystem.
func nodesWithDiskHeadroom(candidates []BackendNode, required uint64) []BackendNode {
	fit := make([]BackendNode, 0, len(candidates))
	for _, n := range candidates {
		if !n.reportsDisk() || n.AvailableDisk >= required {
			fit = append(fit, n)
		}
	}
	return fit
}

// describeDiskShortfall renders the per-node free-space readings that produced
// a rejection, so the operator sees the numbers behind the decision instead of
// a bare "no nodes available".
func describeDiskShortfall(candidates []BackendNode) string {
	parts := make([]string, 0, len(candidates))
	for _, n := range candidates {
		parts = append(parts, fmt.Sprintf("%s has %s free of %s",
			n.Name, humanFileSize(int64(n.AvailableDisk)), humanFileSize(int64(n.TotalDisk))))
	}
	return strings.Join(parts, ", ")
}

// NarrowByDiskHeadroom restricts candidateNodeIDs to nodes whose models
// filesystem can hold `required` more bytes.
//
// A nil candidateNodeIDs means "any healthy backend node" (the caller applied
// no selector); the returned slice is then the narrowed set, never nil, so the
// caller keeps the constraint. When nothing fits, the error wraps
// ErrInsufficientDisk and names every candidate's free space.
//
// Errors reading the registry are NOT fatal: the caller gets its original
// candidate set back alongside the error and can carry on. A database hiccup
// must not stop a cluster from scheduling.
func (r *NodeRegistry) NarrowByDiskHeadroom(ctx context.Context, candidateNodeIDs []string, required uint64) ([]string, error) {
	candidates, err := r.healthyBackendNodes(ctx, candidateNodeIDs)
	if err != nil {
		return candidateNodeIDs, err
	}
	// No rows at all is not a disk verdict — the pool is empty for some other
	// reason (nothing registered, everything unhealthy). Let the existing
	// "no healthy nodes" paths report that; claiming a disk shortage here
	// would be a misleading diagnosis.
	if len(candidates) == 0 {
		return candidateNodeIDs, nil
	}

	fit := nodesWithDiskHeadroom(candidates, required)
	if len(fit) == 0 {
		return nil, fmt.Errorf("%w: need %s free on the models filesystem, but %s",
			ErrInsufficientDisk, humanFileSize(int64(required)), describeDiskShortfall(candidates))
	}
	return extractNodeIDs(fit), nil
}

// healthyBackendNodes loads the healthy backend nodes, optionally restricted to
// an explicit candidate set.
func (r *NodeRegistry) healthyBackendNodes(ctx context.Context, candidateNodeIDs []string) ([]BackendNode, error) {
	q := r.db.WithContext(ctx).Model(&BackendNode{}).
		Where("status = ? AND node_type = ?", StatusHealthy, NodeTypeBackend)
	if candidateNodeIDs != nil {
		q = q.Where("id IN ?", candidateNodeIDs)
	}
	var out []BackendNode
	if err := q.Find(&out).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("listing healthy backend nodes: %w", err)
	}
	return out, nil
}
