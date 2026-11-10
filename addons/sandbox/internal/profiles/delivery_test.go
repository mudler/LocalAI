package profiles_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/profiles"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// fakeCache is an image cache whose pulls the test controls.
type fakeCache struct {
	mu      sync.Mutex
	cached  map[string]bool
	pulls   map[string]int
	forgot  []string
	failing map[string]error
	gate    map[string]chan struct{} // a pull waits for its gate to close
}

func newCache() *fakeCache {
	return &fakeCache{cached: map[string]bool{}, pulls: map[string]int{}, failing: map[string]error{}, gate: map[string]chan struct{}{}}
}

func (f *fakeCache) ImageCached(_ context.Context, ref string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cached[ref]
}

func (f *fakeCache) PrepareImage(ctx context.Context, ref string) error {
	f.mu.Lock()
	f.pulls[ref]++
	g := f.gate[ref]
	err := f.failing[ref]
	f.mu.Unlock()
	if g != nil {
		<-g
	}
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.cached[ref] = true
	f.mu.Unlock()
	return nil
}

func (f *fakeCache) ForgetImage(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.cached, ref)
	f.forgot = append(f.forgot, ref)
	return nil
}

func ref(n string) string { return "reg.example/" + n + "@sha256:" + strings.Repeat(n[:1], 64) }

func newDelivery(t *testing.T, c *fakeCache, images profiles.Images, mod func(*profiles.Options)) *profiles.Delivery {
	t.Helper()
	o := profiles.Options{DataDir: t.TempDir(), Declared: profiles.Declared(), Images: images,
		Cache: func() (sandbox.ImageCache, error) { return c, nil }, WaitBudget: 2 * time.Second, FailBackoff: time.Hour}
	if mod != nil {
		mod(&o)
	}
	d, err := profiles.New(o)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestFirstUsePullsAndReportsProgress(t *testing.T) {
	c := newCache()
	c.gate[ref("media1")] = make(chan struct{})
	d := newDelivery(t, c, profiles.Images{"base": ref("base1"), "media": ref("media1")}, nil)
	c.cached[ref("base1")] = true
	var got profiles.Choice
	var err error
	done := make(chan struct{})
	go func() { got, err = d.Resolve(context.Background(), "media", false, profiles.Caps{}); close(done) }()
	deadline := time.After(2 * time.Second)
	for {
		var media profiles.State
		for _, s := range d.Status(context.Background()) {
			if s.ID == "media" {
				media = s
			}
		}
		if media.Phase == profiles.PhasePulling {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("the pull never showed as pulling: %+v", media)
		case <-time.After(5 * time.Millisecond):
		}
	}
	close(c.gate[ref("media1")])
	<-done
	if err != nil || got.ID != "media" || got.Fallback || got.Image != ref("media1") {
		t.Fatalf("choice = %+v, %v", got, err)
	}
	if got.Memory != 4096<<20 || got.CPUs != 2 {
		t.Fatalf("the profile limits did not reach the choice: %+v", got)
	}
	// A second call finds it cached and pulls nothing.
	if _, err := d.Resolve(context.Background(), "media", false, profiles.Caps{}); err != nil || c.pulls[ref("media1")] != 1 {
		t.Fatalf("pulls = %d, err = %v", c.pulls[ref("media1")], err)
	}
}

func TestHostCapsLowerTheLimits(t *testing.T) {
	c := newCache()
	c.cached[ref("media1")] = true
	d := newDelivery(t, c, profiles.Images{"base": ref("base1"), "media": ref("media1")}, nil)
	got, err := d.Resolve(context.Background(), "media", false, profiles.Caps{MemoryMiB: 1024, DiskMiB: 999999})
	if err != nil {
		t.Fatal(err)
	}
	if got.Memory != 1024<<20 || got.Disk != 20480<<20 {
		t.Fatalf("memory %d disk %d: a cap lowers a limit and never raises it", got.Memory>>20, got.Disk>>20)
	}
}

func TestMissingOrFailedProfileFallsBackToBaseWithANotice(t *testing.T) {
	c := newCache()
	c.cached[ref("base1")] = true
	c.failing[ref("media1")] = errors.New("registry unreachable")
	d := newDelivery(t, c, profiles.Images{"base": ref("base1"), "media": ref("media1")}, nil)
	for _, id := range []string{"media", "video", "nope"} {
		got, err := d.Resolve(context.Background(), id, false, profiles.Caps{})
		if err != nil || got.ID != "base" || !got.Fallback || !strings.Contains(got.Notice, id) {
			t.Fatalf("%s: choice = %+v, %v", id, got, err)
		}
	}
	if !strings.Contains(mustResolve(t, d, "media").Notice, "registry unreachable") {
		t.Fatal("the notice must say why")
	}
	// The failed pull is not retried inside the backoff.
	if n := c.pulls[ref("media1")]; n != 1 {
		t.Fatalf("media was pulled %d times; a failed pull waits for the backoff", n)
	}
	st := map[string]profiles.State{}
	for _, s := range d.Status(context.Background()) {
		st[s.ID] = s
	}
	if st["media"].Phase != profiles.PhaseFailed || st["video"].Phase != profiles.PhaseAbsent || st["base"].Phase != profiles.PhaseReady {
		t.Fatalf("states = %+v", st)
	}
}

func mustResolve(t *testing.T, d *profiles.Delivery, id string) profiles.Choice {
	t.Helper()
	got, err := d.Resolve(context.Background(), id, false, profiles.Caps{})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestFailsWhenBaseCannotBeHadEither(t *testing.T) {
	c := newCache()
	c.failing[ref("base1")] = errors.New("disk full")
	d := newDelivery(t, c, profiles.Images{"base": ref("base1")}, nil)
	if _, err := d.Resolve(context.Background(), "base", false, profiles.Caps{}); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
}

func TestAWaitThatRunsOutFallsBack(t *testing.T) {
	c := newCache()
	c.cached[ref("base1")] = true
	c.gate[ref("media1")] = make(chan struct{})
	defer close(c.gate[ref("media1")])
	d := newDelivery(t, c, profiles.Images{"base": ref("base1"), "media": ref("media1")}, func(o *profiles.Options) { o.WaitBudget = 30 * time.Millisecond })
	got := mustResolve(t, d, "media")
	if !got.Fallback || !strings.Contains(got.Notice, "still being prepared") {
		t.Fatalf("choice = %+v", got)
	}
}

func TestKeepsCurrentAndPreviousDigestAndDeletesOlderOnes(t *testing.T) {
	dir := t.TempDir()
	c := newCache()
	inUse := map[string]bool{}
	mk := func(base string) *profiles.Delivery {
		return newDelivery(t, c, profiles.Images{"base": ref(base)}, func(o *profiles.Options) {
			o.DataDir = dir
			o.InUse = func() map[string]bool { return inUse }
		})
	}
	for _, v := range []string{"base1", "base2", "base3"} {
		mustResolve(t, mk(v), "base")
	}
	if len(c.forgot) != 1 || c.forgot[0] != ref("base1") {
		t.Fatalf("after three releases only the oldest goes, got %v", c.forgot)
	}
	if c.cached[ref("base1")] || !c.cached[ref("base2")] || !c.cached[ref("base3")] {
		t.Fatalf("cache = %v: current and previous stay", c.cached)
	}
	// An old image that a sandbox still runs is kept until it stops.
	inUse[ref("base2")] = true
	mustResolve(t, mk("base4"), "base") // base3 current, base2 -> pending
	if c.cached[ref("base2")] == false {
		t.Fatal("an image in use was deleted")
	}
	delete(inUse, ref("base2"))
	mustResolve(t, mk("base4"), "base")
	if c.cached[ref("base2")] {
		t.Fatal("the image was not deleted once it stopped being used")
	}
}

func TestAdminPinRunsThePreviousImage(t *testing.T) {
	dir := t.TempDir()
	c := newCache()
	mk := func(v string) *profiles.Delivery {
		return newDelivery(t, c, profiles.Images{"base": ref(v)}, func(o *profiles.Options) { o.DataDir = dir })
	}
	mustResolve(t, mk("base1"), "base")
	d := mk("base2")
	if got := mustResolve(t, d, "base"); got.Image != ref("base2") || got.Pinned {
		t.Fatalf("unpinned = %+v", got)
	}
	got, err := d.Resolve(context.Background(), "base", true, profiles.Caps{})
	if err != nil || got.Image != ref("base1") || !got.Pinned {
		t.Fatalf("pinned = %+v, %v", got, err)
	}
	// Nothing previous: the pin has nothing to hold, the current image runs.
	got, _ = mk("base9").Resolve(context.Background(), "video", true, profiles.Caps{})
	if got.ID != "base" {
		t.Fatalf("got %+v", got)
	}
}

func TestPrepullPullsOnlyProfilesUsedBefore(t *testing.T) {
	dir := t.TempDir()
	c := newCache()
	old := newDelivery(t, c, profiles.Images{"base": ref("base1"), "media": ref("media1")}, func(o *profiles.Options) { o.DataDir = dir })
	mustResolve(t, old, "base") // media was never used, video has no image
	d := newDelivery(t, c, profiles.Images{"base": ref("base2"), "media": ref("media2")}, func(o *profiles.Options) { o.DataDir = dir })
	d.Prepull(context.Background())
	if !c.cached[ref("base2")] {
		t.Fatal("base was used before, so its new image must be pulled ahead")
	}
	if c.pulls[ref("media2")] != 0 {
		t.Fatal("media was never used, so it must not be pulled ahead")
	}
}

func TestNoCacheMeansTheHostBackendAndNoPull(t *testing.T) {
	d, err := profiles.New(profiles.Options{DataDir: t.TempDir(), Declared: profiles.Declared(), Images: profiles.Images{"base": ref("base1")},
		Cache: func() (sandbox.ImageCache, error) { return nil, errors.New("host backend") }})
	if err != nil {
		t.Fatal(err)
	}
	got := mustResolve(t, d, "base")
	if got.ID != "base" || got.Image != "" {
		t.Fatalf("got %+v", got)
	}
}
