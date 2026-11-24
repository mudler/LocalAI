// SPDX-License-Identifier: MIT
package artifacts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Authority is the owner policy the artifact store consults. *policy.Policy is
// the implementation. Authorize is the decision and Admit serializes work with
// revocation: a revocation cancels the context given to run and waits for it.
type Authority interface {
	Authorize(ctx context.Context, actor contract.Actor, resource policy.ResourceKey, action policy.Action) error
	Admit(ctx context.Context, actor contract.Actor, resource policy.ResourceKey, action policy.Action, run func(context.Context) error) error
	Resolve(ctx context.Context, actor contract.Actor, request contract.Request) (policy.ResourceKey, error)
}

// AddonState is what the lifecycle manager says about an addon now.
type AddonState struct {
	Installed  bool
	Enabled    bool
	Generation uint64
}

// Limits are finite. A zero Limits selects the defaults as a whole; a partly
// filled one is refused, so no bound can be dropped by leaving it out.
type Limits struct {
	// MaxBytes is the largest single artifact. It never exceeds the relay
	// byte bound, because a larger file could be staged but not downloaded.
	MaxBytes int64
	// UserBytes and TotalBytes bound the declared bytes of live artifacts of
	// one principal and of all principals.
	UserBytes, TotalBytes int64
	// UserCount and TotalCount bound the live artifact records.
	UserCount, TotalCount int
	// Transfers bounds concurrent ingestions, UserTransfers those of one
	// principal and Downloads concurrent streams.
	Transfers, UserTransfers, Downloads int
	// Expiry is how long an artifact lives from its creation.
	Expiry time.Duration
	// TransferTimeout bounds one ingestion. It is shorter than Expiry.
	TransferTimeout time.Duration
	// SweepInterval is how often the expiry sweep runs.
	SweepInterval time.Duration
	// RecheckInterval is how often a running stream looks at the account and
	// source permissions again, in addition to revocation cancelling it.
	RecheckInterval time.Duration
}

// MaxArtifactBytes is the hard upper bound of MaxBytes.
const MaxArtifactBytes = 64 << 20

// DefaultLimits derive from the contract defaults and the relay bound.
func DefaultLimits() Limits {
	c := contract.DefaultLimits()
	return Limits{
		MaxBytes: min(c.ArtifactBytes, MaxArtifactBytes), UserBytes: c.ArtifactQuotaBytes, TotalBytes: 4 * c.ArtifactQuotaBytes,
		UserCount: int(c.ArtifactCount), TotalCount: 10 * int(c.ArtifactCount),
		Transfers: int(c.Concurrency), UserTransfers: max(1, int(c.Concurrency)/2), Downloads: 16,
		Expiry: c.ArtifactExpiry, TransferTimeout: 5 * time.Minute, SweepInterval: time.Minute, RecheckInterval: time.Second,
	}
}

// WithExpiry returns the limits with another artifact lifetime. The transfer
// bound and the sweep follow it, so the result stays consistent. Zero keeps
// the limits as they are. Only a fixture that needs a short lifetime sets it.
func (l Limits) WithExpiry(d time.Duration) Limits {
	if d <= 0 {
		return l
	}
	l.Expiry = d
	l.TransferTimeout = min(l.TransferTimeout, d/2)
	l.SweepInterval = min(l.SweepInterval, max(d/4, time.Millisecond))
	return l
}

// Validate refuses an unbounded or inconsistent configuration.
func (l Limits) Validate() error {
	if l.MaxBytes < 1 || l.MaxBytes > MaxArtifactBytes || l.UserBytes < l.MaxBytes || l.TotalBytes < l.UserBytes ||
		l.UserCount < 1 || l.TotalCount < l.UserCount || l.Transfers < 1 || l.UserTransfers < 1 || l.UserTransfers > l.Transfers ||
		l.Downloads < 1 || l.Expiry < time.Second || l.TransferTimeout < time.Millisecond || l.TransferTimeout >= l.Expiry ||
		l.SweepInterval < time.Millisecond || l.RecheckInterval < time.Millisecond {
		return fmt.Errorf("invalid addon artifact limits: %w", gateway.ErrForbidden)
	}
	return nil
}

// Options are the host-supplied dependencies of Artifacts. Missing ones deny.
type Options struct {
	// Dir is the private root of the store. It is created owner-only.
	Dir string
	// Policy decides and serializes access.
	Policy Authority
	// Runtime resolves the registered runtime endpoint of an addon. Bytes are
	// read only from it.
	Runtime gateway.CurrentRuntime
	// Addon reports the lifecycle state of an addon. Without it nothing is
	// served.
	Addon func(ctx context.Context, addonID string) (AddonState, error)
	// Limits bound sizes, quotas and concurrency. The zero value selects the
	// defaults.
	Limits Limits
	// Now is the clock. The zero value is time.Now.
	Now func() time.Time
	// Tick drives the expiry sweep of Run. The zero value is a ticker at
	// Limits.SweepInterval.
	Tick <-chan time.Time
}

// Artifacts is the owner's private artifact store.
type Artifacts struct {
	opts   Options
	limits Limits
	meta   *metaStore
	blobs  *stagedStore
	// objects is the byte store behind the ObjectStore boundary. It is the
	// staged store; the field lets a test put a faulty store in its place.
	objects storage.ObjectStore
	now     func() time.Time

	// ctx ends every transfer, stream and the sweep loop when the store closes.
	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	closed    bool
	wg        sync.WaitGroup
	jobs      map[string]*job
	downloads int
}

// job is a live transfer. cancelled is set under Artifacts.mu by Cancel.
type job struct {
	ctx       context.Context
	principal string
	cancel    context.CancelFunc
	cancelled bool
	done      chan struct{}
}

var _ gateway.ArtifactProvider = (*gatewayAdapter)(nil)

// Open opens the store in opts.Dir. Transfers that a crash interrupted are
// recorded as failed and their partial bytes removed; bytes with no record are
// removed. A corrupt metadata file is refused and left as it is.
func Open(opts Options) (*Artifacts, error) {
	if opts.Dir == "" || opts.Policy == nil || opts.Runtime == nil {
		return nil, gateway.ErrForbidden
	}
	limits := opts.Limits
	if limits == (Limits{}) {
		limits = DefaultLimits()
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	meta, err := openMeta(filepath.Join(opts.Dir, metaDir))
	if err != nil {
		return nil, err
	}
	blobs, err := openStaged(opts.Dir)
	if err != nil {
		_ = meta.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &Artifacts{opts: opts, limits: limits, meta: meta, blobs: blobs, objects: blobs, now: now, ctx: ctx, cancel: cancel, jobs: map[string]*job{}}
	if err = a.recover(); err != nil {
		cancel()
		_ = meta.Close()
		return nil, err
	}
	return a, nil
}

// Limits reports the limits in force.
func (a *Artifacts) Limits() Limits { return a.limits }

func newID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", gateway.ErrUnavailable
	}
	return "a" + hex.EncodeToString(raw[:]), nil
}

// binding is the digest that finds the artifact of a repeated request. The
// output handle is operation-bound and private, so only its digest is kept.
func binding(principal, operation, handle string) string {
	h := sha256.New()
	for _, part := range []string{principal, operation, handle} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// sourcesOf orders the source resources so every admission takes them in the
// same order.
func sourcesOf(keys []policy.ResourceKey) []gateway.ArtifactResource {
	out := make([]gateway.ArtifactResource, 0, len(keys))
	seen := map[policy.ResourceKey]bool{}
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, gateway.ArtifactResource{AddonID: k.AddonID, ResourceID: k.ResourceID})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AddonID != out[j].AddonID {
			return out[i].AddonID < out[j].AddonID
		}
		return out[i].ResourceID < out[j].ResourceID
	})
	return out
}

func keysOf(sources []gateway.ArtifactResource) []policy.ResourceKey {
	keys := make([]policy.ResourceKey, len(sources))
	for i, s := range sources {
		keys[i] = policy.ResourceKey{AddonID: s.AddonID, ResourceID: s.ResourceID}
	}
	return keys
}

// admit holds an output admission on every source resource for the whole of
// run, so a revocation of any of them cancels run's context and waits for it.
func (a *Artifacts) admit(ctx context.Context, actor contract.Actor, keys []policy.ResourceKey, run func(context.Context) error) error {
	switch len(keys) {
	case 0:
		return gateway.ErrForbidden
	case 1:
		return a.opts.Policy.Admit(ctx, actor, keys[0], policy.ActionOutput, run)
	}
	return a.opts.Policy.Admit(ctx, actor, keys[0], policy.ActionOutput, func(ctx context.Context) error {
		return a.admit(ctx, actor, keys[1:], run)
	})
}

// authorizeAll is the decision on every source resource and on the account, now.
func (a *Artifacts) authorizeAll(ctx context.Context, actor contract.Actor, keys []policy.ResourceKey) error {
	for _, k := range keys {
		if err := a.opts.Policy.Authorize(ctx, actor, k, policy.ActionRead); err != nil {
			return err
		}
	}
	return nil
}

// addonReady denies when the addon is not installed and enabled at the
// generation that produced the artifact. A lookup that fails is unavailable.
func (a *Artifacts) addonReady(ctx context.Context, addonID string, generation uint64) error {
	if a.opts.Addon == nil {
		return gateway.ErrForbidden
	}
	st, err := a.opts.Addon(ctx, addonID)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, gateway.ErrForbidden) {
			return gateway.ErrForbidden
		}
		return fmt.Errorf("addon state: %w", gateway.ErrUnavailable)
	}
	if !st.Installed || !st.Enabled || st.Generation != generation {
		return gateway.ErrForbidden
	}
	return nil
}

// owns reports whether the actor is the principal that requested the artifact.
func owns(actor contract.Actor, r record) bool {
	return actor.UserID != "" && actor.UserID == r.Principal && actor.Mode == r.Mode
}

// lookup returns the record of an unexpired artifact of the actor. An unknown
// identifier, another principal's artifact and an expired artifact are the
// same answer.
func (a *Artifacts) lookup(actor contract.Actor, id string) (record, error) {
	if !idPattern.MatchString(id) {
		return record{}, ErrNotFound
	}
	r, ok := a.meta.get(id)
	if !ok || !owns(actor, r) || !a.now().Before(r.ExpiresAt) {
		return record{}, ErrNotFound
	}
	return r, nil
}

// Status returns the artifact of the actor, including a transfer that is still
// running. It is read-only.
func (a *Artifacts) Status(_ context.Context, actor contract.Actor, id string) (Artifact, error) {
	r, err := a.lookup(actor, id)
	if err != nil {
		return Artifact{}, err
	}
	return r.view(), nil
}

// Output is the host record of an output the actor already turned into an
// artifact: the policy's gateway adapter uses it to describe an output without
// the addon being asked. It returns the artifact identifier as the handle.
func (a *Artifacts) Output(_ context.Context, actor contract.Actor, request contract.Request, handle string) (contract.OutputDescription, error) {
	bind := binding(actor.UserID, request.OperationID, handle)
	for _, r := range a.meta.all() {
		if r.Binding == bind && owns(actor, r) && r.State == gateway.ArtifactComplete && a.now().Before(r.ExpiresAt) {
			return contract.OutputDescription{Handle: r.ID, Name: r.Name, MediaType: r.MediaType, Size: r.Size}, nil
		}
	}
	return contract.OutputDescription{}, ErrNotFound
}

// Cancel withdraws a transfer of the actor that is still running. The artifact
// is recorded as cancelled and its partial bytes are removed. A complete
// artifact is unchanged.
func (a *Artifacts) Cancel(ctx context.Context, actor contract.Actor, id string) (Artifact, error) {
	if _, err := a.lookup(actor, id); err != nil {
		return Artifact{}, err
	}
	a.mu.Lock()
	j := a.jobs[id]
	if j != nil {
		j.cancelled = true
		j.cancel()
	}
	a.mu.Unlock()
	if j != nil {
		select {
		case <-j.done:
		case <-ctx.Done():
			return Artifact{}, ctx.Err()
		}
	}
	cur, ok := a.meta.get(id)
	if !ok {
		return Artifact{}, ErrNotFound
	}
	if cur.State == gateway.ArtifactTransferring && j == nil {
		// No live job: an abandoned record. Settle it as cancelled.
		if err := a.settle(ctx, id, gateway.ArtifactCancelled, "cancelled"); err != nil {
			return Artifact{}, err
		}
		cur, _ = a.meta.get(id)
	}
	return cur.view(), nil
}

// settle ends a transfer without completing it: the record keeps the state and
// reason until expiry, and no byte stays on disk.
func (a *Artifacts) settle(ctx context.Context, id string, state gateway.ArtifactState, reason string) error {
	err := a.meta.mutate(func(d *document) error {
		r := d.Artifacts[id]
		if r == nil {
			return nil
		}
		if r.State == gateway.ArtifactTransferring {
			r.State, r.Reason = state, reason
		}
		return nil
	})
	// The bytes go whatever the metadata write did: a failed record with bytes
	// would be worse than bytes that are gone. The one exception is a record
	// that is complete: its bytes are what it names.
	if cur, ok := a.meta.get(id); ok && cur.State == gateway.ArtifactComplete {
		return err
	}
	derr := a.objects.Delete(ctx, id)
	return errors.Join(err, derr)
}

// dropUnlessComplete removes the bytes of a transfer that did not complete. A
// record that is complete, as it can be when only the durability of its write
// failed, keeps them.
func (a *Artifacts) dropUnlessComplete(id string) {
	if cur, ok := a.meta.get(id); ok && cur.State == gateway.ArtifactComplete {
		return
	}
	_ = a.objects.Delete(context.Background(), id)
}
