package gallery

import (
	"context"
	"net/http"
	"net/http/httptest"
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
