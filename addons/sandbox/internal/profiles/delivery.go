package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/addons/internal/atomicfile"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The phases of the image of a profile.
const (
	PhaseIdle    = "idle"    // not on this host yet; the first use pulls it
	PhasePulling = "pulling" // being pulled and converted
	PhaseReady   = "ready"   // in the cache
	PhaseFailed  = "failed"  // the last pull failed
	PhaseAbsent  = "absent"  // this release has no image for the profile
)

// Defaults of Options.
const (
	DefaultWaitBudget  = 20 * time.Minute
	DefaultFailBackoff = 5 * time.Minute
	pullTimeout        = 45 * time.Minute
)

// Choice is what one call runs in: the profile, its image and the limits of
// its sandbox. Fallback says the profile the call asked for was not used, and
// Notice says why, in words for the model and the person.
type Choice struct {
	ID       string
	Image    string
	CPUs     int
	Memory   int64 // bytes; 0 means the backend default
	Disk     int64 // bytes; 0 means the backend default
	Pinned   bool  // the image is the previous one, by the administrator's pin
	Fallback bool
	Notice   string
}

// Caps are limits the host sets for every sandbox, in MiB. Zero is no cap.
type Caps struct{ MemoryMiB, DiskMiB int }

// State is what the pages show about the image of one profile.
type State struct {
	ID          string  `json:"id"`
	Phase       string  `json:"phase"`
	Percent     int     `json:"percent,omitempty"`
	Error       string  `json:"error,omitempty"`
	HasPrevious bool    `json:"hasPrevious,omitempty"`
	Image       string  `json:"-"`
	Fraction    float64 `json:"-"`
}

// Options configures a Delivery.
type Options struct {
	// DataDir holds the record of the digests (profiles-cache.json).
	DataDir  string
	Declared []contract.ExecutionProfile
	// Images are the image references of this release, by profile ID. A
	// profile without one is absent.
	Images Images
	// Cache returns the image cache of the active backend, or an error when
	// the backend has none (it then runs no profiles).
	Cache func() (sandbox.ImageCache, error)
	// ForgetOld is called for each old image the cache must drop, so it can
	// clear every backend's cache folder. Nil: the active cache only.
	ForgetOld func(ctx context.Context, ref string)
	// InUse returns the image references that running sandboxes use. They
	// are not deleted while they run.
	InUse                   func() map[string]bool
	WaitBudget, FailBackoff time.Duration
	Now                     func() time.Time
}

// record is what the add-on remembers of the digests of one profile.
type record struct {
	Current  string   `json:"current,omitempty"`
	Previous string   `json:"previous,omitempty"`
	Pending  []string `json:"pending,omitempty"` // old images still in use, to delete later
}

type pull struct {
	progress *ociimage.Progress
	done     chan struct{}
	err      error
	failedAt time.Time
}

// Delivery gets the image of a profile onto the host: it pulls on first use
// with progress, pulls new digests ahead of use after an upgrade, keeps the
// current and the previous digest of each profile and deletes the older ones,
// and falls back to base when a profile cannot be had.
type Delivery struct {
	o    Options
	path string

	mu    sync.Mutex
	rec   map[string]record
	pulls map[string]*pull // by image reference
}

// New reads the record in DataDir.
func New(o Options) (*Delivery, error) {
	if o.WaitBudget <= 0 {
		o.WaitBudget = DefaultWaitBudget
	}
	if o.FailBackoff <= 0 {
		o.FailBackoff = DefaultFailBackoff
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Cache == nil {
		return nil, errors.New("profiles: a cache is required")
	}
	d := &Delivery{o: o, path: filepath.Join(o.DataDir, "profiles-cache.json"), rec: map[string]record{}, pulls: map[string]*pull{}}
	raw, err := os.ReadFile(d.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		// A record that cannot be read is dropped: it only says what to delete.
		if json.Unmarshal(raw, &d.rec) != nil || d.rec == nil {
			d.rec = map[string]record{}
		}
	}
	return d, nil
}

func (d *Delivery) saveLocked() {
	raw, _ := json.Marshal(d.rec)
	if err := os.MkdirAll(d.o.DataDir, 0o700); err == nil {
		if err := atomicfile.Write(d.path, raw, 0o600); err != nil {
			xlog.Warn("sandbox: cannot save the image record", "error", err)
		}
	}
}

func (d *Delivery) profile(id string) (contract.ExecutionProfile, bool) {
	for _, p := range d.o.Declared {
		if p.ID == id {
			return p, true
		}
	}
	return contract.ExecutionProfile{}, false
}

// Known returns the profiles of this release.
func (d *Delivery) Known() []contract.ExecutionProfile { return d.o.Declared }

// Current is the image reference of this release for a profile, "" when the
// release has none.
func (d *Delivery) Current(id string) string { return d.o.Images[id] }

// limits turns a profile and the host caps into the numbers of a Choice.
func limits(p contract.ExecutionProfile, caps Caps) (cpus int, mem, disk int64) {
	capped := func(v, c int) int {
		if c > 0 && (v == 0 || v > c) {
			return c
		}
		return v
	}
	return p.CPUs, int64(capped(p.MemoryMiB, caps.MemoryMiB)) << 20, int64(capped(p.DiskMiB, caps.DiskMiB)) << 20
}

// Resolve returns what a call that asked for profile id runs in. pin asks for
// the previous image of the profile when the add-on has one (the
// administrator's pin). It waits while the image is pulled, for at most the
// wait budget; the pages show the progress meanwhile (State). A profile that is
// missing, or whose image cannot be had, falls back to base with a notice. It
// fails only when base cannot be had either.
func (d *Delivery) Resolve(ctx context.Context, id string, pin bool, caps Caps) (Choice, error) {
	if id == "" {
		id = Base
	}
	c, err := d.resolve(ctx, id, pin, caps)
	if err == nil || id == Base {
		return c, err
	}
	reason := err.Error()
	xlog.Warn("sandbox: profile not usable, running base", "profile", id, "reason", reason)
	b, berr := d.resolve(ctx, Base, false, caps)
	if berr != nil {
		return Choice{}, fmt.Errorf("the %s profile could not be prepared (%s) and neither could base: %w", id, reason, berr)
	}
	b.Fallback = true
	b.Notice = fmt.Sprintf("[The %s sandbox profile is not available (%s), so this command ran in the base profile. Programs that only %s has are missing. Tell the user.]", id, reason, id)
	return b, nil
}

// errNoCache says the active backend keeps no images.
var errNoCache = errors.New("this backend runs no profiles")

func (d *Delivery) resolve(ctx context.Context, id string, pin bool, caps Caps) (Choice, error) {
	p, ok := d.profile(id)
	if !ok {
		return Choice{}, errors.New("no such profile")
	}
	cpus, mem, disk := limits(p, caps)
	cache, err := d.o.Cache()
	if err != nil || cache == nil {
		// The host backend has no images: the call runs on the host as it
		// always did, and the profile only names the limits it would have.
		return Choice{ID: id, CPUs: cpus, Memory: mem, Disk: disk}, nil
	}
	image := d.o.Images[id]
	if image == "" {
		return Choice{}, errors.New("this release has no image for it")
	}
	ref, pinned := image, false
	d.mu.Lock()
	if prev := d.rec[id].Previous; pin && prev != "" {
		ref, pinned = prev, true
	}
	d.mu.Unlock()
	if err := d.ensure(ctx, cache, id, ref); err != nil {
		return Choice{}, err
	}
	if !pinned {
		d.noteCurrent(ctx, cache, id, ref)
	}
	return Choice{ID: id, Image: ref, CPUs: cpus, Memory: mem, Disk: disk, Pinned: pinned}, nil
}

// ensure makes ref ready in the cache, pulling it when needed. Callers of the
// same reference share one pull, which keeps going when a caller stops waiting.
func (d *Delivery) ensure(ctx context.Context, cache sandbox.ImageCache, id, ref string) error {
	if cache.ImageCached(ctx, ref) {
		return nil
	}
	pl, err := d.startPull(cache, ref)
	if err != nil {
		return err
	}
	budget := time.NewTimer(d.o.WaitBudget)
	defer budget.Stop()
	select {
	case <-pl.done:
		return pl.err
	case <-ctx.Done():
		return ctx.Err()
	case <-budget.C:
		return fmt.Errorf("it is still being prepared after %d minutes", int(d.o.WaitBudget.Minutes()))
	}
}

// startPull returns the running pull of ref, or starts it. A pull that failed
// lately is not tried again until the backoff has passed: its error comes back
// at once, so a broken registry does not stall every call.
func (d *Delivery) startPull(cache sandbox.ImageCache, ref string) (*pull, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if pl, ok := d.pulls[ref]; ok {
		select {
		case <-pl.done:
			if pl.err == nil {
				break // it finished, yet the image is not cached any more: pull again
			}
			if d.o.Now().Sub(pl.failedAt) < d.o.FailBackoff {
				return nil, pl.err
			}
		default:
			return pl, nil
		}
	}
	pl := &pull{progress: &ociimage.Progress{}, done: make(chan struct{})}
	d.pulls[ref] = pl
	go func() {
		defer close(pl.done)
		ctx, cancel := context.WithTimeout(ociimage.WithProgress(context.Background(), pl.progress), pullTimeout)
		defer cancel()
		if err := cache.PrepareImage(ctx, ref); err != nil {
			xlog.Warn("sandbox: image pull failed", "image", ref, "error", err)
			pl.err = fmt.Errorf("the image pull failed: %w", err)
			pl.failedAt = d.o.Now()
		}
	}()
	return pl, nil
}

// noteCurrent records that ref is the current image of a profile. When the
// current digest changed, the old current becomes the previous one, and the
// image that was previous before is deleted unless a sandbox still runs it.
func (d *Delivery) noteCurrent(ctx context.Context, cache sandbox.ImageCache, id, ref string) {
	d.mu.Lock()
	r := d.rec[id]
	if r.Current != ref {
		if r.Previous != "" && r.Previous != r.Current {
			r.Pending = append(r.Pending, r.Previous)
		}
		if r.Current != "" {
			r.Previous = r.Current
		}
		r.Current = ref
	}
	var keep []string
	pending := r.Pending
	r.Pending = nil
	inUse := map[string]bool{}
	if d.o.InUse != nil {
		inUse = d.o.InUse()
	}
	var drop []string
	for _, old := range pending {
		switch {
		case old == r.Current || old == r.Previous:
		case inUse[old]:
			keep = append(keep, old)
		default:
			drop = append(drop, old)
		}
	}
	r.Pending = keep
	d.rec[id] = r
	d.saveLocked()
	d.mu.Unlock()
	for _, old := range drop {
		if d.o.ForgetOld != nil {
			d.o.ForgetOld(ctx, old)
		} else if err := cache.ForgetImage(ctx, old); err != nil {
			xlog.Warn("sandbox: cannot delete an old image", "image", old, "error", err)
		}
	}
}

// Prepull gets the new image of each profile this host used before, in the
// background, so the first call after an upgrade does not wait for it. A
// profile that was never used is not pulled: it would cost bandwidth and disk
// for nothing. The pulls run one after the other.
func (d *Delivery) Prepull(ctx context.Context) {
	cache, err := d.o.Cache()
	if err != nil || cache == nil {
		return
	}
	d.mu.Lock()
	var todo []string
	for _, p := range d.o.Declared {
		if ref := d.o.Images[p.ID]; ref != "" && d.rec[p.ID].Current != "" && d.rec[p.ID].Current != ref {
			todo = append(todo, p.ID)
		}
	}
	d.mu.Unlock()
	for _, id := range todo {
		ref := d.o.Images[id]
		if err := d.ensure(ctx, cache, id, ref); err != nil {
			xlog.Warn("sandbox: could not pull the new image ahead of use", "profile", id, "error", err)
			continue
		}
		d.noteCurrent(ctx, cache, id, ref)
	}
}

// Status returns the state of the image of each profile of this release.
func (d *Delivery) Status(ctx context.Context) []State {
	cache, cerr := d.o.Cache()
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]State, 0, len(d.o.Declared))
	for _, p := range d.o.Declared {
		ref := d.o.Images[p.ID]
		st := State{ID: p.ID, Phase: PhaseIdle, Image: ref, HasPrevious: d.rec[p.ID].Previous != ""}
		switch pl, pulling := d.pulls[ref]; {
		case ref == "":
			st.Phase = PhaseAbsent
		case cerr != nil || cache == nil:
			st.Phase = PhaseReady // nothing to pull: the host backend runs commands as they are
		case pulling && !isDone(pl):
			st.Phase, st.Fraction = PhasePulling, pl.progress.Fraction()
			st.Percent = min(int(st.Fraction*100), 99)
		case cache.ImageCached(ctx, ref):
			st.Phase = PhaseReady
		case pulling && pl.err != nil:
			st.Phase, st.Error = PhaseFailed, pl.err.Error()
		}
		out = append(out, st)
	}
	return out
}

func isDone(p *pull) bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}
