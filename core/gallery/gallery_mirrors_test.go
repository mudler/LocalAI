package gallery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// TestMain gives this package its own temporary root so the gallery index cache
// cannot escape it.
//
// The cache is a sibling of the models directory (<models>/../cache/gallery),
// which is right in production but leaks under test: a models directory made
// with os.MkdirTemp("", …) gets one directly under the system temp directory,
// so the sibling resolves to /tmp/cache — a path no test framework cleans up,
// left behind after every run. Pointing TMPDIR at a directory we remove
// ourselves contains the sibling without having to rewrite every call site,
// and covers any added later.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "localai-gallery-tests-*")
	if err != nil {
		panic(err)
	}
	// os.TempDir consults TMPDIR on every call, so this applies to temp
	// directories created from here on.
	if err := os.Setenv("TMPDIR", root); err != nil {
		panic(err)
	}

	code := m.Run()

	// Not deferred: os.Exit does not run deferred functions. Nothing useful
	// can be done about a failure to clean up a temporary directory at this
	// point, and the exit code must stay the suite's.
	_ = os.RemoveAll(root)
	os.Exit(code)
}

// resetGalleryFailures and expireGalleryFailure exist so specs can drive the
// cooldown without sleeping. They live here because nothing in the production
// path ever needs to reach into the failure map.
func resetGalleryFailures() {
	for _, k := range galleryFailures.Keys() {
		galleryFailures.Delete(k)
	}
}

func expireGalleryFailure(url string, at time.Time) {
	galleryFailures.Set(url, at)
}

// tempModelsDir returns an absolute models directory whose parent is private to
// the calling spec, so the sibling cache (<models>/../cache/gallery) is
// isolated too. A bare temp directory would put every spec's cache in one
// shared place, where the specs that count files in it see each other's.
func tempModelsDir() string {
	GinkgoHelper()
	root, err := os.MkdirTemp("", "gallery-mirrors-spec-*")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(root) })

	models := filepath.Join(root, "models")
	Expect(os.MkdirAll(models, 0o750)).To(Succeed())
	return models
}

// countingServer serves body with status, counting the requests it actually
// received. The counter is atomic because the handler runs on the server's
// goroutine while the assertions run on the spec's.
func countingServer(status int, body string) (*httptest.Server, *atomic.Int64) {
	GinkgoHelper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if status >= 400 {
			http.Error(w, body, status)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	DeferCleanup(srv.Close)
	return srv, &hits
}

var _ = Describe("galleryCandidates", func() {
	It("orders the primary first", func() {
		Expect(galleryCandidates(config.Gallery{
			URL:     "https://primary/index.yaml",
			Mirrors: []string{"https://a/index.yaml", "https://b/index.yaml"},
		})).To(Equal([]string{"https://primary/index.yaml", "https://a/index.yaml", "https://b/index.yaml"}))
	})

	It("drops empty and duplicate entries", func() {
		Expect(galleryCandidates(config.Gallery{
			URL:     "https://primary/index.yaml",
			Mirrors: []string{"", "https://primary/index.yaml", "https://a/index.yaml", "https://a/index.yaml"},
		})).To(Equal([]string{"https://primary/index.yaml", "https://a/index.yaml"}),
			"want the primary then the single distinct mirror")
	})

	// A gallery whose primary URL is empty still has usable mirrors; dropping
	// the empty must not drop the rest with it.
	It("keeps the mirrors when the primary is empty", func() {
		Expect(galleryCandidates(config.Gallery{Mirrors: []string{"https://a/index.yaml"}})).
			To(Equal([]string{"https://a/index.yaml"}))
	})
})

var _ = Describe("fetchGalleryIndex", func() {
	BeforeEach(resetGalleryFailures)

	It("falls back to a mirror when the primary fails", func() {
		primary, _ := countingServer(http.StatusInternalServerError, "down")
		mirror, _ := countingServer(http.StatusOK, "- name: from-mirror\n")

		body, served, err := fetchGalleryIndex(context.Background(), config.Gallery{
			URL:     primary.URL,
			Mirrors: []string{mirror.URL},
		}, tempModelsDir())
		Expect(err).ToNot(HaveOccurred())
		Expect(served).To(Equal(mirror.URL))
		Expect(string(body)).To(Equal("- name: from-mirror\n"))
	})

	It("prefers the primary when it works", func() {
		mirror, mirrorHits := countingServer(http.StatusOK, "- name: from-mirror\n")
		primary, _ := countingServer(http.StatusOK, "- name: from-primary\n")

		body, served, err := fetchGalleryIndex(context.Background(), config.Gallery{
			URL:     primary.URL,
			Mirrors: []string{mirror.URL},
		}, tempModelsDir())
		Expect(err).ToNot(HaveOccurred())
		Expect(served).To(Equal(primary.URL))
		Expect(string(body)).To(Equal("- name: from-primary\n"))
		Expect(mirrorHits.Load()).To(BeZero(), "mirror was contacted even though the primary answered")
	})

	It("errors when every candidate fails", func() {
		down, hits := countingServer(http.StatusInternalServerError, "down")

		_, _, err := fetchGalleryIndex(context.Background(), config.Gallery{
			URL:     down.URL,
			Mirrors: []string{down.URL + "/other"},
		}, tempModelsDir())
		Expect(err).To(HaveOccurred(), "want an error when nothing can serve the index")
		Expect(hits.Load()).To(BeEquivalentTo(2), "want both candidates tried")
	})

	It("errors for a gallery with neither a URL nor mirrors", func() {
		_, _, err := fetchGalleryIndex(context.Background(), config.Gallery{Name: "empty"}, tempModelsDir())
		Expect(err).To(HaveOccurred())
	})

	// An HTTP error page is not an index. Without this the downloader hands
	// back a 404 body as if it were content, the fallback never triggers, and
	// the junk gets cached for an hour.
	It("treats an HTTP error status as a failure", func() {
		primary, _ := countingServer(http.StatusNotFound, "no such index")
		mirror, _ := countingServer(http.StatusOK, "- name: from-mirror\n")

		body, served, err := fetchGalleryIndex(context.Background(), config.Gallery{
			URL:     primary.URL,
			Mirrors: []string{mirror.URL},
		}, tempModelsDir())
		Expect(err).ToNot(HaveOccurred())
		Expect(served).To(Equal(mirror.URL), "a 404 body was taken for an index")
		Expect(string(body)).To(Equal("- name: from-mirror\n"))
	})

	// A caller that has already given up must not be dragged through the whole
	// candidate list.
	It("honours the caller's context", func() {
		srv, hits := countingServer(http.StatusOK, "- name: from-primary\n")

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, _, err := fetchGalleryIndex(ctx, config.Gallery{URL: srv.URL}, tempModelsDir())
		Expect(err).To(HaveOccurred(), "want an error when the caller's context is already cancelled")
		Expect(hits.Load()).To(BeZero(), "server dialled despite a cancelled context")
		// The source did nothing wrong. Blaming it would blackhole a healthy
		// candidate for ten minutes because a browser tab closed.
		Expect(inCooldown(srv.URL)).To(BeFalse(),
			"caller cancellation was recorded as a failure of the source")
	})

	// A candidate that accepts the connection and then never answers is the
	// failure mode the per-attempt timeout exists for: without it the whole
	// listing hangs on one bad host and the mirrors are never reached.
	It("gives up on a hanging candidate", func() {
		release := make(chan struct{})
		var hangHits atomic.Int64
		hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hangHits.Add(1)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		DeferCleanup(func() {
			close(release)
			hang.Close()
		})

		mirror, mirrorHits := countingServer(http.StatusOK, "- name: from-mirror\n")

		restore := galleryFetchTimeout
		galleryFetchTimeout = 100 * time.Millisecond
		DeferCleanup(func() { galleryFetchTimeout = restore })

		g := config.Gallery{URL: hang.URL, Mirrors: []string{mirror.URL}}
		basePath := tempModelsDir()

		type outcome struct {
			served string
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			defer GinkgoRecover()
			_, served, err := fetchGalleryIndex(context.Background(), g, basePath)
			done <- outcome{served, err}
		}()

		// The assertion has to be bounded: an unbounded attempt does not fail,
		// it hangs, and a hung spec is a useless signal.
		var got outcome
		Eventually(done, 30*time.Second).Should(Receive(&got),
			"fetch never returned — a hanging candidate is not bounded by a per-attempt timeout")
		Expect(got.err).ToNot(HaveOccurred())
		Expect(got.served).To(Equal(mirror.URL))

		Expect(hangHits.Load()).To(BeEquivalentTo(1), "hanging candidate should be dialled once")
		Expect(mirrorHits.Load()).To(BeEquivalentTo(1), "the timed-out attempt did not fall through")
		// A timeout is the source's own failure, unlike caller cancellation.
		Expect(inCooldown(hang.URL)).To(BeTrue(), "a candidate that timed out was not put in cooldown")
	})

	// "all 1 source(s) failed" on a three-mirror gallery reads as a
	// misconfiguration; the operator needs to see that the rest were skipped.
	It("reports how many sources were configured and skipped when all fail", func() {
		down, _ := countingServer(http.StatusInternalServerError, "down")
		g := config.Gallery{
			URL:     down.URL,
			Name:    "localai",
			Mirrors: []string{down.URL + "/a", down.URL + "/b"},
		}

		// Two of the three are already in cooldown, so only one is dialled.
		expireGalleryFailure(down.URL+"/a", time.Now())
		expireGalleryFailure(down.URL+"/b", time.Now())

		_, _, err := fetchGalleryIndex(context.Background(), g, tempModelsDir())
		Expect(err).To(HaveOccurred(), "want an error when nothing can serve the index")
		Expect(err.Error()).To(And(
			ContainSubstring("3 configured"),
			ContainSubstring("2 skipped"),
		), "the error does not say how many sources were configured and skipped")
	})
})

var _ = Describe("the gallery source cooldown", func() {
	BeforeEach(resetGalleryFailures)

	// A dead primary must not be re-dialled on every call. Without this, a
	// gallery listing in the UI pays the full timeout against a dead host every
	// refresh.
	It("skips a failed candidate while it is cooling down", func() {
		primary, hits := countingServer(http.StatusInternalServerError, "down")
		mirror, _ := countingServer(http.StatusOK, "- name: from-mirror\n")

		g := config.Gallery{URL: primary.URL, Mirrors: []string{mirror.URL}}
		for i := 0; i < 3; i++ {
			_, _, err := fetchGalleryIndex(context.Background(), g, tempModelsDir())
			Expect(err).ToNot(HaveOccurred(), "fetch %d", i)
		}
		Expect(hits.Load()).To(BeEquivalentTo(1), "primary re-dialled — cooldown is not holding")
	})

	It("expires", func() {
		primary, hits := countingServer(http.StatusInternalServerError, "down")
		mirror, _ := countingServer(http.StatusOK, "- name: from-mirror\n")

		g := config.Gallery{URL: primary.URL, Mirrors: []string{mirror.URL}}
		_, _, err := fetchGalleryIndex(context.Background(), g, tempModelsDir())
		Expect(err).ToNot(HaveOccurred())

		// Age the recorded failure past the cooldown rather than sleeping.
		expireGalleryFailure(primary.URL, time.Now().Add(-2*galleryFailureCooldown))
		_, _, err = fetchGalleryIndex(context.Background(), g, tempModelsDir())
		Expect(err).ToNot(HaveOccurred())
		Expect(hits.Load()).To(BeEquivalentTo(2), "primary was not re-dialled — cooldown never expired")
	})

	// Refusing to serve a gallery because every source is in cooldown is worse
	// than paying for one slow request, so the cooldown is ignored when it
	// would leave nothing to try.
	It("is ignored when every candidate is cooling down", func() {
		primary, primaryHits := countingServer(http.StatusInternalServerError, "down")
		mirror, mirrorHits := countingServer(http.StatusOK, "- name: from-mirror\n")

		// Put both candidates in cooldown without dialling them.
		expireGalleryFailure(primary.URL, time.Now())
		expireGalleryFailure(mirror.URL, time.Now())

		_, served, err := fetchGalleryIndex(context.Background(), config.Gallery{
			URL:     primary.URL,
			Mirrors: []string{mirror.URL},
		}, tempModelsDir())
		Expect(err).ToNot(HaveOccurred())
		Expect(served).To(Equal(mirror.URL))
		Expect(primaryHits.Load()).To(BeEquivalentTo(1), "cooldown should have been ignored, not obeyed")
		Expect(mirrorHits.Load()).To(BeEquivalentTo(1), "cooldown should have been ignored, not obeyed")
	})

	// A source that answers is out of cooldown immediately, otherwise a host
	// that blipped once stays skipped for ten minutes after it has recovered.
	It("is cleared by a successful fetch", func() {
		srv, hits := countingServer(http.StatusOK, "- name: ok\n")

		expireGalleryFailure(srv.URL, time.Now())
		g := config.Gallery{URL: srv.URL}
		for i := 0; i < 2; i++ {
			_, _, err := fetchGalleryIndex(context.Background(), g, tempModelsDir())
			Expect(err).ToNot(HaveOccurred(), "fetch %d", i)
		}
		Expect(hits.Load()).To(BeEquivalentTo(2), "a successful fetch must clear the cooldown")
		Expect(inCooldown(srv.URL)).To(BeFalse(), "candidate still in cooldown after answering")
	})
})

// getGalleryElements is the choke point every gallery listing goes through, so
// the fallback has to be reachable from there and not just from the helper.
var _ = Describe("getGalleryElements", func() {
	BeforeEach(resetGalleryFailures)

	It("falls back to a mirror", func() {
		primary, _ := countingServer(http.StatusInternalServerError, "down")
		mirror, _ := countingServer(http.StatusOK, "- name: mirror-model\n  description: served by a mirror\n")

		g := config.Gallery{Name: "mirror-fallback-spec", URL: primary.URL, Mirrors: []string{mirror.URL}}
		DeferCleanup(func() { galleryCache.Delete(g.Name + "-" + g.URL) })

		models, err := getGalleryElements(g, tempModelsDir(), func(*GalleryModel) bool { return false })
		Expect(err).ToNot(HaveOccurred())
		Expect(models).To(HaveLen(1))
		Expect(models[0].Name).To(Equal("mirror-model"))

		// The cache identifies the gallery, not whichever source answered, so a
		// mirror-served fetch must populate the entry the primary URL would hit.
		Expect(galleryCache.Exists(g.Name + "-" + g.URL)).To(BeTrue(),
			"mirror-served index was not cached under the gallery's own key")
	})
})

var _ = Describe("galleryCachePath", func() {
	// The cache must never land in the models directory, where a *.yaml file is
	// interpreted as an installed model config.
	It("is outside the models directory", func() {
		base := tempModelsDir()
		got := galleryCachePath(base, "https://example/index.yaml")
		Expect(filepath.Dir(got)).ToNot(Equal(base), "cache path is inside the models directory")

		// Nor anywhere below it: the models directory is walked and listed, and
		// a cache subdirectory in there is LocalAI's own litter in the user's
		// models.
		rel, err := filepath.Rel(base, got)
		Expect(err).ToNot(HaveOccurred())
		Expect(rel).To(HavePrefix(".."), "cache path %q is under the models directory", got)
	})

	// The model gallery and the backend gallery are both fetched, often under
	// the same parent directory. Keying the file on the URL is what stops one
	// from being served as the other.
	It("distinguishes galleries", func() {
		base := tempModelsDir()
		models := galleryCachePath(base, "https://example/index.yaml")
		backends := galleryCachePath(base, "https://example/backends.yaml")
		Expect(models).ToNot(Equal(backends), "one gallery would overwrite the other")
		Expect(galleryCachePath(base, "https://example/index.yaml")).To(Equal(models),
			"the same gallery URL produced two different cache paths")
	})

	// Without a models directory there is no sensible place for the cache, and
	// a relative path would write next to the process' working directory.
	It("yields nothing without a models directory", func() {
		Expect(galleryCachePath("", "https://example/index.yaml")).To(BeEmpty())
		// Must not panic or write anywhere either.
		persistGalleryIndex("", "https://example/index.yaml", []byte("- name: x\n"))
	})

	// A relative models directory is the same failure as an empty one: "." and
	// "models" both resolve against whatever directory the process happens to
	// be running in, which is exactly what the guard exists to prevent.
	DescribeTable("rejects a relative models directory",
		func(base string) {
			Expect(galleryCachePath(base, "https://example/index.yaml")).To(BeEmpty(),
				"it resolves against the working directory")
			// And nothing may be written next to the working directory either.
			persistGalleryIndex(base, "https://example/index.yaml", []byte("- name: x\n"))
		},
		Entry("the working directory itself", "."),
		Entry("a bare relative name", "models"),
		Entry("an explicitly relative path", "./models"),
		Entry("a parent-relative path", "../models"),
	)

	// Sanity: the guard must still let a real absolute models directory through.
	It("accepts an absolute models directory", func() {
		Expect(galleryCachePath(tempModelsDir(), "https://example/index.yaml")).ToNot(BeEmpty())
	})
})

var _ = Describe("the last known good gallery index", func() {
	BeforeEach(resetGalleryFailures)

	It("is written after a successful fetch", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("- name: cached\n"))
		}))
		DeferCleanup(srv.Close)

		base := tempModelsDir()
		g := config.Gallery{URL: srv.URL, Name: "localai"}
		_, _, err := fetchGalleryIndex(context.Background(), g, base)
		Expect(err).ToNot(HaveOccurred())

		body, err := os.ReadFile(galleryCachePath(base, srv.URL))
		Expect(err).ToNot(HaveOccurred(), "no cached copy written")
		Expect(string(body)).To(Equal("- name: cached\n"))
	})

	// The offline case: nothing is reachable, but a previous run left a copy.
	It("is served when every source fails", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("- name: cached\n"))
		}))
		base := tempModelsDir()
		g := config.Gallery{URL: srv.URL, Name: "localai"}
		_, _, err := fetchGalleryIndex(context.Background(), g, base)
		Expect(err).ToNot(HaveOccurred())

		srv.Close() // now nothing is reachable
		resetGalleryFailures()

		body, served, err := fetchGalleryIndex(context.Background(), g, base)
		Expect(err).ToNot(HaveOccurred(), "want the cached copy")
		Expect(string(body)).To(Equal("- name: cached\n"))
		Expect(served).To(Equal(galleryCachePath(base, srv.URL)))
	})

	It("cannot rescue a fetch when there is no copy and no network", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := srv.URL
		srv.Close()

		_, _, err := fetchGalleryIndex(context.Background(),
			config.Gallery{URL: url, Name: "localai"}, tempModelsDir())
		Expect(err).To(HaveOccurred(), "want an error when there is neither a source nor a cached copy")
	})

	// The cache is an optimisation. A read-only or full disk must not turn a
	// gallery that was fetched perfectly well into a failure.
	It("does not fail the fetch when it cannot be written", func() {
		srv, _ := countingServer(http.StatusOK, "- name: live\n")

		base := tempModelsDir()
		// A regular file where the cache directory needs to be: every write
		// below it fails, and nothing can repair it at runtime.
		Expect(os.WriteFile(filepath.Join(base, "..", "cache"), []byte("not a directory"), 0o600)).To(Succeed())

		body, served, err := fetchGalleryIndex(context.Background(),
			config.Gallery{URL: srv.URL, Name: "localai"}, base)
		Expect(err).ToNot(HaveOccurred(), "a cache write failure failed the whole fetch")
		Expect(served).To(Equal(srv.URL))
		Expect(string(body)).To(Equal("- name: live\n"))
	})

	// The cache is keyed on the gallery, not on whichever source answered, so a
	// mirror-served fetch refreshes the copy an offline run will look for.
	It("is keyed on the gallery URL even when a mirror served it", func() {
		primary, _ := countingServer(http.StatusInternalServerError, "down")
		mirror, _ := countingServer(http.StatusOK, "- name: from-mirror\n")

		base := tempModelsDir()
		g := config.Gallery{URL: primary.URL, Mirrors: []string{mirror.URL}, Name: "localai"}
		_, _, err := fetchGalleryIndex(context.Background(), g, base)
		Expect(err).ToNot(HaveOccurred())

		body, err := os.ReadFile(galleryCachePath(base, g.URL))
		Expect(err).ToNot(HaveOccurred(), "no copy cached under the gallery's own URL")
		Expect(string(body)).To(Equal("- name: from-mirror\n"))

		_, err = os.ReadFile(galleryCachePath(base, mirror.URL))
		Expect(err).To(HaveOccurred(),
			"the copy was cached under the mirror's URL, where an offline run will not look for it")
	})

	// A reachable source always wins over the copy on disk, and the copy is
	// refreshed with what it served — otherwise the first fetch a machine ever
	// makes would be the only one it remembers.
	It("loses to a live fetch, and is refreshed by it", func() {
		served := "- name: old\n"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(served))
		}))
		DeferCleanup(srv.Close)

		base := tempModelsDir()
		g := config.Gallery{URL: srv.URL, Name: "localai"}
		_, _, err := fetchGalleryIndex(context.Background(), g, base)
		Expect(err).ToNot(HaveOccurred())

		served = "- name: new\n"
		body, from, err := fetchGalleryIndex(context.Background(), g, base)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal("- name: new\n"), "want the live index from the source")
		Expect(from).To(Equal(srv.URL))

		onDisk, err := os.ReadFile(galleryCachePath(base, srv.URL))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(onDisk)).To(Equal("- name: new\n"),
			"the cached copy was not refreshed with what the source served")
	})

	// A failed fetch must leave the copy alone: writing a failure's empty body
	// over it would destroy the only gallery an offline machine has. The staged
	// write must not litter the cache directory either.
	It("survives a failed fetch, and leaves no staging file behind", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("- name: cached\n"))
		}))
		DeferCleanup(srv.Close)

		base := tempModelsDir()
		g := config.Gallery{URL: srv.URL, Name: "localai"}
		_, _, err := fetchGalleryIndex(context.Background(), g, base)
		Expect(err).ToNot(HaveOccurred())

		cacheDir := filepath.Dir(galleryCachePath(base, g.URL))
		entries, err := os.ReadDir(cacheDir)
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).To(HaveLen(1), "want just the index — a staging file was left behind")

		srv.Close()
		resetGalleryFailures()
		_, _, err = fetchGalleryIndex(context.Background(), g, base)
		Expect(err).ToNot(HaveOccurred(), "fallback")

		body, err := os.ReadFile(galleryCachePath(base, g.URL))
		Expect(err).ToNot(HaveOccurred(), "the cached copy is gone after a failed fetch")
		Expect(string(body)).To(Equal("- name: cached\n"), "want it untouched by a failed fetch")

		entries, err = os.ReadDir(cacheDir)
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).To(HaveLen(1), "want just the index after a failed fetch")
	})

	// A captive portal, a corporate proxy or a CDN error page all answer HTTP
	// 200 with HTML. Persisting on status alone lets one of those overwrite the
	// copy an offline start depends on, which is the worst possible time to
	// discover it.
	It("is not overwritten by an HTML page served with status 200", func() {
		served := "- name: cached\n"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(served))
		}))
		DeferCleanup(srv.Close)

		base := tempModelsDir()
		g := config.Gallery{URL: srv.URL, Name: "localai"}
		_, _, err := fetchGalleryIndex(context.Background(), g, base)
		Expect(err).ToNot(HaveOccurred())

		// Now the same URL answers 200 with an interception page.
		served = "<html><head><title>Sign in to the network</title></head>\n<body>Please authenticate</body></html>\n"
		body, from, err := fetchGalleryIndex(context.Background(), g, base)
		Expect(err).ToNot(HaveOccurred())
		// The live body is still handed back — rejecting it here would hide the
		// failure from the caller that actually parses it.
		Expect(from).To(Equal(srv.URL))
		Expect(string(body)).To(Equal(served), "want the live response")

		onDisk, err := os.ReadFile(galleryCachePath(base, g.URL))
		Expect(err).ToNot(HaveOccurred(), "the cached copy is gone")
		Expect(string(onDisk)).To(Equal("- name: cached\n"), "a 200 HTML page overwrote the good index")
	})

	// The point of the probe is what happens next: once the network is gone,
	// the offline path must still find a copy it can parse.
	It("still parses as a gallery index after an unparseable body was served", func() {
		served := "- name: cached\n  description: the good index\n"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(served))
		}))

		base := tempModelsDir()
		g := config.Gallery{URL: srv.URL, Name: "localai"}
		_, _, err := fetchGalleryIndex(context.Background(), g, base)
		Expect(err).ToNot(HaveOccurred())

		// A proxy starts answering 200 with something that is not YAML at all.
		served = "\t<html>\n\t  <body>502 Bad Gateway</body>\n</html>\n"
		_, _, err = fetchGalleryIndex(context.Background(), g, base)
		Expect(err).ToNot(HaveOccurred())

		srv.Close() // and now the machine is offline
		resetGalleryFailures()

		body, from, err := fetchGalleryIndex(context.Background(), g, base)
		Expect(err).ToNot(HaveOccurred(), "offline fallback")
		Expect(from).To(Equal(galleryCachePath(base, g.URL)), "want the cached copy")

		// Readable by the offline path means parseable, not merely present.
		var models []GalleryModel
		Expect(yaml.Unmarshal(body, &models)).To(Succeed(),
			"the offline copy no longer parses as a gallery index")
		Expect(models).To(HaveLen(1))
		Expect(models[0].Name).To(Equal("cached"))
	})

	// An empty document parses fine but is not an index. Replacing a populated
	// copy with one that lists nothing is the same outage as replacing it with
	// garbage, and an empty index is worth nothing offline, so the older copy
	// wins.
	DescribeTable("is not overwritten by an empty index",
		func(empty string) {
			served := "- name: cached\n"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(served))
			}))
			DeferCleanup(srv.Close)

			base := tempModelsDir()
			g := config.Gallery{URL: srv.URL, Name: "localai"}
			_, _, err := fetchGalleryIndex(context.Background(), g, base)
			Expect(err).ToNot(HaveOccurred())

			served = empty
			_, _, err = fetchGalleryIndex(context.Background(), g, base)
			Expect(err).ToNot(HaveOccurred())

			onDisk, err := os.ReadFile(galleryCachePath(base, g.URL))
			Expect(err).ToNot(HaveOccurred(), "the cached copy is gone after an empty body %q", empty)
			Expect(string(onDisk)).To(Equal("- name: cached\n"), "want the populated index kept")
		},
		Entry("no body at all", ""),
		Entry("an empty sequence", "[]\n"),
		Entry("a bare document marker", "---\n"),
	)

	// A machine with nothing cached and an interception page in front of it has
	// no gallery: the junk must not be written, so the next offline start still
	// has nothing rather than something unparseable.
	It("is not created at all when the first fetch is unparseable", func() {
		srv, _ := countingServer(http.StatusOK, "<html><body>hello</body></html>")

		base := tempModelsDir()
		g := config.Gallery{URL: srv.URL, Name: "localai"}
		_, _, err := fetchGalleryIndex(context.Background(), g, base)
		Expect(err).ToNot(HaveOccurred())

		_, err = os.Stat(galleryCachePath(base, g.URL))
		Expect(err).To(HaveOccurred(), "an HTML page was written as the last known good gallery index")
	})
})
