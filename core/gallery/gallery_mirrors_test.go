package gallery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/config"
)

// countingServer serves body with status, counting the requests it actually
// received. The counter is atomic because the handler runs on the server's
// goroutine while the assertions run on the test's.
func countingServer(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if status >= 400 {
			http.Error(w, body, status)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestGalleryCandidatesOrdersPrimaryFirst(t *testing.T) {
	got := galleryCandidates(config.Gallery{
		URL:     "https://primary/index.yaml",
		Mirrors: []string{"https://a/index.yaml", "https://b/index.yaml"},
	})
	want := []string{"https://primary/index.yaml", "https://a/index.yaml", "https://b/index.yaml"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestGalleryCandidatesDropsEmptiesAndDuplicates(t *testing.T) {
	got := galleryCandidates(config.Gallery{
		URL:     "https://primary/index.yaml",
		Mirrors: []string{"", "https://primary/index.yaml", "https://a/index.yaml", "https://a/index.yaml"},
	})
	if len(got) != 2 {
		t.Fatalf("got %v, want the primary and one mirror", got)
	}
	if got[0] != "https://primary/index.yaml" || got[1] != "https://a/index.yaml" {
		t.Fatalf("got %v, want the primary then the single distinct mirror", got)
	}
}

// A gallery whose primary URL is empty still has usable mirrors; dropping the
// empty must not drop the rest with it.
func TestGalleryCandidatesKeepsMirrorsWhenPrimaryIsEmpty(t *testing.T) {
	got := galleryCandidates(config.Gallery{Mirrors: []string{"https://a/index.yaml"}})
	if len(got) != 1 || got[0] != "https://a/index.yaml" {
		t.Fatalf("got %v, want just the mirror", got)
	}
}

func TestFetchFallsBackToMirrorWhenPrimaryFails(t *testing.T) {
	resetGalleryFailures()

	primary, _ := countingServer(t, http.StatusInternalServerError, "down")
	mirror, _ := countingServer(t, http.StatusOK, "- name: from-mirror\n")

	body, served, err := fetchGalleryIndex(context.Background(), config.Gallery{
		URL:     primary.URL,
		Mirrors: []string{mirror.URL},
	}, t.TempDir())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if served != mirror.URL {
		t.Errorf("served by %q, want the mirror %q", served, mirror.URL)
	}
	if string(body) != "- name: from-mirror\n" {
		t.Errorf("body = %q", body)
	}
}

func TestFetchPrefersPrimaryWhenItWorks(t *testing.T) {
	resetGalleryFailures()

	mirror, mirrorHits := countingServer(t, http.StatusOK, "- name: from-mirror\n")
	primary, _ := countingServer(t, http.StatusOK, "- name: from-primary\n")

	body, served, err := fetchGalleryIndex(context.Background(), config.Gallery{
		URL:     primary.URL,
		Mirrors: []string{mirror.URL},
	}, t.TempDir())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if served != primary.URL {
		t.Errorf("served by %q, want the primary", served)
	}
	if string(body) != "- name: from-primary\n" {
		t.Errorf("body = %q, want the primary's index", body)
	}
	if mirrorHits.Load() != 0 {
		t.Error("mirror was contacted even though the primary answered")
	}
}

func TestFetchReturnsErrorWhenEveryCandidateFails(t *testing.T) {
	resetGalleryFailures()

	down, hits := countingServer(t, http.StatusInternalServerError, "down")

	if _, _, err := fetchGalleryIndex(context.Background(), config.Gallery{
		URL:     down.URL,
		Mirrors: []string{down.URL + "/other"},
	}, t.TempDir()); err == nil {
		t.Fatal("want an error when nothing can serve the index")
	}
	if hits.Load() != 2 {
		t.Errorf("dialled %d times, want both candidates tried", hits.Load())
	}
}

func TestFetchWithNoSourcesErrors(t *testing.T) {
	resetGalleryFailures()

	if _, _, err := fetchGalleryIndex(context.Background(), config.Gallery{Name: "empty"}, t.TempDir()); err == nil {
		t.Fatal("want an error for a gallery with neither a URL nor mirrors")
	}
}

// An HTTP error page is not an index. Without this the downloader hands back a
// 404 body as if it were content, the fallback never triggers, and the junk
// gets cached for an hour.
func TestFetchTreatsHTTPErrorStatusAsFailure(t *testing.T) {
	resetGalleryFailures()

	primary, _ := countingServer(t, http.StatusNotFound, "no such index")
	mirror, _ := countingServer(t, http.StatusOK, "- name: from-mirror\n")

	body, served, err := fetchGalleryIndex(context.Background(), config.Gallery{
		URL:     primary.URL,
		Mirrors: []string{mirror.URL},
	}, t.TempDir())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if served != mirror.URL {
		t.Errorf("served by %q, want the mirror — a 404 body was taken for an index", served)
	}
	if string(body) != "- name: from-mirror\n" {
		t.Errorf("body = %q", body)
	}
}

// A caller that has already given up must not be dragged through the whole
// candidate list.
func TestFetchHonoursCallerContext(t *testing.T) {
	resetGalleryFailures()

	srv, hits := countingServer(t, http.StatusOK, "- name: from-primary\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := fetchGalleryIndex(ctx, config.Gallery{URL: srv.URL}, t.TempDir()); err == nil {
		t.Fatal("want an error when the caller's context is already cancelled")
	}
	if hits.Load() != 0 {
		t.Errorf("server dialled %d times despite a cancelled context", hits.Load())
	}
	// The source did nothing wrong. Blaming it would blackhole a healthy
	// candidate for ten minutes because a browser tab closed.
	if inCooldown(srv.URL) {
		t.Error("caller cancellation was recorded as a failure of the source")
	}
}

// A candidate that accepts the connection and then never answers is the failure
// mode the per-attempt timeout exists for: without it the whole listing hangs on
// one bad host and the mirrors are never reached.
func TestFetchGivesUpOnAHangingCandidate(t *testing.T) {
	resetGalleryFailures()

	release := make(chan struct{})
	var hangHits atomic.Int64
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hangHits.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		hang.Close()
	})

	mirror, mirrorHits := countingServer(t, http.StatusOK, "- name: from-mirror\n")

	restore := galleryFetchTimeout
	galleryFetchTimeout = 100 * time.Millisecond
	t.Cleanup(func() { galleryFetchTimeout = restore })

	g := config.Gallery{URL: hang.URL, Mirrors: []string{mirror.URL}}
	basePath := t.TempDir()

	type outcome struct {
		served string
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		_, served, err := fetchGalleryIndex(context.Background(), g, basePath)
		done <- outcome{served, err}
	}()

	// The assertion has to be bounded: an unbounded attempt does not fail, it
	// hangs, and a hung test is a useless signal.
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("fetch: %v", got.err)
		}
		if got.served != mirror.URL {
			t.Errorf("served by %q, want the mirror", got.served)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("fetch never returned — a hanging candidate is not bounded by a per-attempt timeout")
	}

	if hangHits.Load() != 1 {
		t.Errorf("hanging candidate dialled %d times, want 1", hangHits.Load())
	}
	if mirrorHits.Load() != 1 {
		t.Errorf("mirror dialled %d times, want 1 — the timed-out attempt did not fall through", mirrorHits.Load())
	}
	// A timeout is the source's own failure, unlike caller cancellation.
	if !inCooldown(hang.URL) {
		t.Error("a candidate that timed out was not put in cooldown")
	}
}

// A dead primary must not be re-dialled on every call. Without this, a gallery
// listing in the UI pays the full timeout against a dead host every refresh.
func TestFailedCandidateIsSkippedDuringCooldown(t *testing.T) {
	resetGalleryFailures()

	primary, hits := countingServer(t, http.StatusInternalServerError, "down")
	mirror, _ := countingServer(t, http.StatusOK, "- name: from-mirror\n")

	g := config.Gallery{URL: primary.URL, Mirrors: []string{mirror.URL}}
	for i := 0; i < 3; i++ {
		if _, _, err := fetchGalleryIndex(context.Background(), g, t.TempDir()); err != nil {
			t.Fatalf("fetch %d: %v", i, err)
		}
	}
	if hits.Load() != 1 {
		t.Errorf("primary dialled %d times, want 1 — cooldown is not holding", hits.Load())
	}
}

func TestCooldownExpires(t *testing.T) {
	resetGalleryFailures()

	primary, hits := countingServer(t, http.StatusInternalServerError, "down")
	mirror, _ := countingServer(t, http.StatusOK, "- name: from-mirror\n")

	g := config.Gallery{URL: primary.URL, Mirrors: []string{mirror.URL}}
	if _, _, err := fetchGalleryIndex(context.Background(), g, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// Age the recorded failure past the cooldown rather than sleeping.
	expireGalleryFailure(primary.URL, time.Now().Add(-2*galleryFailureCooldown))
	if _, _, err := fetchGalleryIndex(context.Background(), g, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Errorf("primary dialled %d times, want 2 — cooldown never expired", hits.Load())
	}
}

// Refusing to serve a gallery because every source is in cooldown is worse than
// paying for one slow request, so the cooldown is ignored when it would leave
// nothing to try.
func TestAllCandidatesInCooldownAreStillAttempted(t *testing.T) {
	resetGalleryFailures()

	primary, primaryHits := countingServer(t, http.StatusInternalServerError, "down")
	mirror, mirrorHits := countingServer(t, http.StatusOK, "- name: from-mirror\n")

	// Put both candidates in cooldown without dialling them.
	expireGalleryFailure(primary.URL, time.Now())
	expireGalleryFailure(mirror.URL, time.Now())

	_, served, err := fetchGalleryIndex(context.Background(), config.Gallery{
		URL:     primary.URL,
		Mirrors: []string{mirror.URL},
	}, t.TempDir())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if served != mirror.URL {
		t.Errorf("served by %q, want the mirror", served)
	}
	if primaryHits.Load() != 1 || mirrorHits.Load() != 1 {
		t.Errorf("primary %d hits, mirror %d hits — cooldown should have been ignored, not obeyed",
			primaryHits.Load(), mirrorHits.Load())
	}
}

// A source that answers is out of cooldown immediately, otherwise a host that
// blipped once stays skipped for ten minutes after it has recovered.
func TestSuccessClearsTheCooldown(t *testing.T) {
	resetGalleryFailures()

	srv, hits := countingServer(t, http.StatusOK, "- name: ok\n")

	expireGalleryFailure(srv.URL, time.Now())
	g := config.Gallery{URL: srv.URL}
	for i := 0; i < 2; i++ {
		if _, _, err := fetchGalleryIndex(context.Background(), g, t.TempDir()); err != nil {
			t.Fatalf("fetch %d: %v", i, err)
		}
	}
	if hits.Load() != 2 {
		t.Errorf("server dialled %d times, want 2 — a successful fetch must clear the cooldown", hits.Load())
	}
	if inCooldown(srv.URL) {
		t.Error("candidate still in cooldown after answering")
	}
}

// getGalleryElements is the choke point every gallery listing goes through, so
// the fallback has to be reachable from there and not just from the helper.
func TestGetGalleryElementsFallsBackToMirror(t *testing.T) {
	resetGalleryFailures()

	primary, _ := countingServer(t, http.StatusInternalServerError, "down")
	mirror, _ := countingServer(t, http.StatusOK, "- name: mirror-model\n  description: served by a mirror\n")

	g := config.Gallery{Name: t.Name(), URL: primary.URL, Mirrors: []string{mirror.URL}}
	t.Cleanup(func() { galleryCache.Delete(g.Name + "-" + g.URL) })

	models, err := getGalleryElements(g, t.TempDir(), func(*GalleryModel) bool { return false })
	if err != nil {
		t.Fatalf("getGalleryElements: %v", err)
	}
	if len(models) != 1 || models[0].Name != "mirror-model" {
		t.Fatalf("got %+v, want the mirror's single entry", models)
	}

	// The cache identifies the gallery, not whichever source answered, so a
	// mirror-served fetch must populate the entry the primary URL would hit.
	if !galleryCache.Exists(g.Name + "-" + g.URL) {
		t.Error("mirror-served index was not cached under the gallery's own key")
	}
}

func TestSuccessfulFetchIsPersisted(t *testing.T) {
	resetGalleryFailures()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("- name: cached\n"))
	}))
	defer srv.Close()

	base := t.TempDir()
	g := config.Gallery{URL: srv.URL, Name: "localai"}
	if _, _, err := fetchGalleryIndex(context.Background(), g, base); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(galleryCachePath(base, srv.URL))
	if err != nil {
		t.Fatalf("no cached copy written: %v", err)
	}
	if string(body) != "- name: cached\n" {
		t.Errorf("cached %q", body)
	}
}

// The offline case: nothing is reachable, but a previous run left a copy.
func TestFallsBackToDiskWhenEverySourceFails(t *testing.T) {
	resetGalleryFailures()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("- name: cached\n"))
	}))
	base := t.TempDir()
	g := config.Gallery{URL: srv.URL, Name: "localai"}
	if _, _, err := fetchGalleryIndex(context.Background(), g, base); err != nil {
		t.Fatal(err)
	}
	srv.Close() // now nothing is reachable
	resetGalleryFailures()

	body, served, err := fetchGalleryIndex(context.Background(), g, base)
	if err != nil {
		t.Fatalf("want the cached copy, got error: %v", err)
	}
	if string(body) != "- name: cached\n" {
		t.Errorf("body = %q", body)
	}
	if served != galleryCachePath(base, srv.URL) {
		t.Errorf("served = %q, want the cache path", served)
	}
}

func TestNoCacheAndNoNetworkStillErrors(t *testing.T) {
	resetGalleryFailures()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	if _, _, err := fetchGalleryIndex(context.Background(),
		config.Gallery{URL: url, Name: "localai"}, t.TempDir()); err == nil {
		t.Fatal("want an error when there is neither a source nor a cached copy")
	}
}

// The cache must never land in the models directory, where a *.yaml file is
// interpreted as an installed model config.
func TestCachePathIsOutsideTheModelsDirectory(t *testing.T) {
	base := t.TempDir()
	got := galleryCachePath(base, "https://example/index.yaml")
	if filepath.Dir(got) == base {
		t.Errorf("cache path %q is inside the models directory", got)
	}
	// Nor anywhere below it: the models directory is walked and listed, and a
	// cache subdirectory in there is LocalAI's own litter in the user's models.
	rel, err := filepath.Rel(base, got)
	if err == nil && !strings.HasPrefix(rel, "..") {
		t.Errorf("cache path %q is under the models directory", got)
	}
}

// The model gallery and the backend gallery are both fetched, often under the
// same parent directory. Keying the file on the URL is what stops one from
// being served as the other.
func TestCachePathDistinguishesGalleries(t *testing.T) {
	base := t.TempDir()
	models := galleryCachePath(base, "https://example/index.yaml")
	backends := galleryCachePath(base, "https://example/backends.yaml")
	if models == backends {
		t.Errorf("both galleries cache to %q — one would overwrite the other", models)
	}
	if galleryCachePath(base, "https://example/index.yaml") != models {
		t.Error("the same gallery URL produced two different cache paths")
	}
}

// Without a models directory there is no sensible place for the cache, and a
// relative path would write next to the process' working directory.
func TestCachePathIsEmptyWithoutAModelsDirectory(t *testing.T) {
	if got := galleryCachePath("", "https://example/index.yaml"); got != "" {
		t.Errorf("galleryCachePath with no base = %q, want no cache path at all", got)
	}
	// Must not panic or write anywhere either.
	persistGalleryIndex("", "https://example/index.yaml", []byte("- name: x\n"))
}

// The cache is an optimisation. A read-only or full disk must not turn a
// gallery that was fetched perfectly well into a failure.
func TestCacheWriteFailureDoesNotFailTheFetch(t *testing.T) {
	resetGalleryFailures()

	srv, _ := countingServer(t, http.StatusOK, "- name: live\n")

	base := t.TempDir()
	// A regular file where the cache directory needs to be: every write below
	// it fails, and nothing can repair it at runtime.
	if err := os.WriteFile(filepath.Join(base, "..", "cache"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	body, served, err := fetchGalleryIndex(context.Background(),
		config.Gallery{URL: srv.URL, Name: "localai"}, base)
	if err != nil {
		t.Fatalf("a cache write failure failed the whole fetch: %v", err)
	}
	if served != srv.URL || string(body) != "- name: live\n" {
		t.Errorf("served = %q body = %q, want the live index", served, body)
	}
}

// The cache is keyed on the gallery, not on whichever source answered, so a
// mirror-served fetch refreshes the copy an offline run will look for.
func TestMirrorServedFetchIsPersistedUnderTheGalleryURL(t *testing.T) {
	resetGalleryFailures()

	primary, _ := countingServer(t, http.StatusInternalServerError, "down")
	mirror, _ := countingServer(t, http.StatusOK, "- name: from-mirror\n")

	base := t.TempDir()
	g := config.Gallery{URL: primary.URL, Mirrors: []string{mirror.URL}, Name: "localai"}
	if _, _, err := fetchGalleryIndex(context.Background(), g, base); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(galleryCachePath(base, g.URL))
	if err != nil {
		t.Fatalf("no copy cached under the gallery's own URL: %v", err)
	}
	if string(body) != "- name: from-mirror\n" {
		t.Errorf("cached %q, want the mirror's index", body)
	}
	if _, err := os.ReadFile(galleryCachePath(base, mirror.URL)); err == nil {
		t.Error("the copy was cached under the mirror's URL, where an offline run will not look for it")
	}
}

// A reachable source always wins over the copy on disk, and the copy is
// refreshed with what it served — otherwise the first fetch a machine ever
// makes would be the only one it remembers.
func TestLiveFetchWinsOverTheCachedCopyAndRefreshesIt(t *testing.T) {
	resetGalleryFailures()

	served := "- name: old\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(served))
	}))
	defer srv.Close()

	base := t.TempDir()
	g := config.Gallery{URL: srv.URL, Name: "localai"}
	if _, _, err := fetchGalleryIndex(context.Background(), g, base); err != nil {
		t.Fatal(err)
	}

	served = "- name: new\n"
	body, from, err := fetchGalleryIndex(context.Background(), g, base)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "- name: new\n" || from != srv.URL {
		t.Errorf("body = %q served by %q, want the live index from the source", body, from)
	}
	onDisk, err := os.ReadFile(galleryCachePath(base, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != "- name: new\n" {
		t.Errorf("cached copy = %q, want it refreshed with what the source served", onDisk)
	}
}

// A failed fetch must leave the copy alone: writing a failure's empty body over
// it would destroy the only gallery an offline machine has. The staged write
// must not litter the cache directory either.
func TestFailedFetchLeavesTheCachedCopyIntact(t *testing.T) {
	resetGalleryFailures()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("- name: cached\n"))
	}))
	defer srv.Close()

	base := t.TempDir()
	g := config.Gallery{URL: srv.URL, Name: "localai"}
	if _, _, err := fetchGalleryIndex(context.Background(), g, base); err != nil {
		t.Fatal(err)
	}

	cacheDir := filepath.Dir(galleryCachePath(base, g.URL))
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("cache directory holds %d files, want just the index — a staging file was left behind", len(entries))
	}

	srv.Close()
	resetGalleryFailures()
	if _, _, err := fetchGalleryIndex(context.Background(), g, base); err != nil {
		t.Fatalf("fallback: %v", err)
	}

	body, err := os.ReadFile(galleryCachePath(base, g.URL))
	if err != nil {
		t.Fatalf("the cached copy is gone after a failed fetch: %v", err)
	}
	if string(body) != "- name: cached\n" {
		t.Errorf("cached copy = %q, want it untouched by a failed fetch", body)
	}
	if entries, err = os.ReadDir(cacheDir); err != nil || len(entries) != 1 {
		t.Errorf("cache directory holds %d files after a failed fetch (err %v), want just the index", len(entries), err)
	}
}
