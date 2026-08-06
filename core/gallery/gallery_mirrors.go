package gallery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/xsync"
	"github.com/mudler/xlog"
	"gopkg.in/yaml.v3"
)

// galleryFetchTimeout bounds a single candidate attempt. GitHub's raw endpoint
// degrades by getting slow far more often than by returning an error, so the
// timeout — not the mirror list — is what actually gets a user to a working
// gallery on a bad day.
//
// It is deliberately far longer than a healthy fetch needs. The downloader only
// ever bounded the response headers, never the body, so this is the first
// whole-transfer deadline this path has had: too tight a value would fail slow
// links that work today and then park a perfectly healthy source in cooldown
// for ten minutes. The default index is ~2.2 MB, so 120s tolerates a sustained
// ~19 KB/s — below any link that could go on to install a model.
//
// A var rather than a const so tests can shorten it.
var galleryFetchTimeout = 120 * time.Second

// galleryFailureCooldown keeps a candidate that just failed out of the rotation
// for a while. Without it, every gallery listing pays the full timeout against
// a dead host before reaching a mirror that works.
const galleryFailureCooldown = 10 * time.Minute

// galleryFailures records when each candidate URL last failed. It is
// package-level and shared by every gallery: the point is that a host which is
// down stays skipped across listings, and the URL is what identifies it.
var galleryFailures = xsync.NewSyncedMap[string, time.Time]()

// galleryCandidates returns the URLs to try, primary first. Empty and repeated
// entries are dropped so a copy-pasted config cannot make us dial the same
// dead host three times.
//
// Deliberately no SSRF validation here. validateGalleryConfigURL guards
// GetGalleryConfigFromURL because that URL arrives in a request body; these
// come from the operator's own gallery configuration (LOCALAI_GALLERIES or the
// admin-gated POST /api/settings), the same place the primary URL has always
// come from, and the index fetch has never validated the primary. A mirror is
// no more privileged than the URL it backs up, so validating mirrors while the
// primary goes unchecked would buy nothing and would break the deployment
// mirrors exist for: an index served from a host on the LAN. file:// mirrors
// remain confined to the models directory by the downloader's basePath check.
func galleryCandidates(g config.Gallery) []string {
	seen := make(map[string]struct{}, len(g.Mirrors)+1)
	out := make([]string, 0, len(g.Mirrors)+1)

	for _, candidate := range append([]string{g.URL}, g.Mirrors...) {
		if candidate == "" {
			continue
		}
		if _, dup := seen[candidate]; dup {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

// inCooldown reports whether a candidate failed recently enough to skip.
//
// Exists and Get take the lock separately, so a concurrent Delete between them
// yields the zero time and reads as "not in cooldown". That is the harmless
// direction: the cost is one extra dial, never a skipped source.
func inCooldown(url string) bool {
	if !galleryFailures.Exists(url) {
		return false
	}
	failedAt := galleryFailures.Get(url)
	if failedAt.IsZero() || time.Since(failedAt) >= galleryFailureCooldown {
		galleryFailures.Delete(url)
		return false
	}
	return true
}

// galleryCachePath is where the last known good copy of an index lives.
//
// Deliberately not inside basePath: getGalleryElements' caller treats every
// <name>.yaml in the models directory as an installed model config, so a cached
// index there would be misread as a model. The sibling cache directory follows
// the precedent in core/services/worker/file_staging.go. The name is a digest
// of the gallery URL so the model and the backend gallery — often fetched with
// sibling base paths — cannot overwrite each other.
//
// A non-absolute basePath yields no path at all: "", "." and "models" all
// resolve the sibling against the process' working directory, which is not
// somewhere LocalAI should be dropping files. Only an absolute models
// directory names a location we can reason about.
func galleryCachePath(basePath, url string) string {
	if !filepath.IsAbs(basePath) {
		return ""
	}
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(basePath, "..", "cache", "gallery", hex.EncodeToString(sum[:])+".yaml")
}

// isUsableGalleryIndex reports whether body is worth keeping as the last known
// good copy.
//
// HTTP 200 does not mean "index": a captive portal, a corporate proxy or a CDN
// error page all answer 200 with HTML, and the fetch path has no other reason
// to look at the bytes — the parse only happens later, in getGalleryElements.
// Persisting on status alone therefore lets an interception page overwrite a
// good copy, and the next offline start — the one case this cache exists for —
// would serve that page instead of the gallery it already had.
//
// An empty document is rejected for the same reason. It parses fine, so a
// probe that only checked the parse would let a source that answers with a
// blank body replace a populated index with one that lists nothing; from the
// user's side an empty gallery and an unparseable one are the same outage. A
// genuinely empty index is worth nothing offline anyway, so there is no case
// where keeping it beats keeping what came before.
//
// The shape check is deliberately shallow — a top-level YAML sequence — because
// this is a guard against "not an index at all", not a schema validator.
// getGalleryElements still does the real typed unmarshal.
func isUsableGalleryIndex(body []byte) bool {
	var probe []any
	if err := yaml.Unmarshal(body, &probe); err != nil {
		return false
	}
	return len(probe) > 0
}

// persistGalleryIndex stores a freshly fetched index for the next time nothing
// is reachable.
//
// Every failure here is logged at debug and otherwise ignored: the copy is an
// optimisation, and a read-only or full disk must not turn a gallery that was
// fetched perfectly well into a failed listing.
func persistGalleryIndex(basePath, url string, body []byte) {
	path := galleryCachePath(basePath, url)
	if path == "" {
		return
	}
	if !isUsableGalleryIndex(body) {
		xlog.Debug("refusing to cache a response that is not a gallery index",
			"url", url, "bytes", len(body))
		return
	}
	// 0o750: the cache is LocalAI's own bookkeeping, so nothing outside the
	// server's user and group has any reason to traverse it.
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		xlog.Debug("could not create gallery cache directory", "path", path, "error", err)
		return
	}
	// Write via a temporary file so an interrupted write cannot leave a
	// truncated index that the next offline start would try to parse.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gallery-*.tmp")
	if err != nil {
		xlog.Debug("could not stage gallery cache", "path", path, "error", err)
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		// The write already failed; a close or unlink error on the way out
		// changes nothing about the outcome and has nowhere useful to go.
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		xlog.Debug("could not write gallery cache", "path", path, "error", err)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		xlog.Debug("could not flush gallery cache", "path", path, "error", err)
		return
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		xlog.Debug("could not install gallery cache", "path", path, "error", err)
	}
}

// fetchGalleryIndex returns the raw index bytes and the URL that served them,
// trying each candidate in order.
//
// A candidate in cooldown is skipped, unless every candidate is in cooldown —
// in which case the cooldown is ignored rather than failing outright, because
// refusing to serve a gallery we might be able to reach is worse than one slow
// request.
//
// If no candidate answers, the last known good copy on disk is served and its
// path is returned as the source. Nothing else in the chain helps a machine
// that has no network at all.
func fetchGalleryIndex(ctx context.Context, g config.Gallery, basePath string) ([]byte, string, error) {
	candidates := galleryCandidates(g)
	if len(candidates) == 0 {
		return nil, "", fmt.Errorf("gallery %q has no URL", g.Name)
	}

	attempt := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if !inCooldown(c) {
			attempt = append(attempt, c)
		}
	}
	if len(attempt) == 0 {
		attempt = candidates
	}

	var lastErr error
	for _, candidate := range attempt {
		attemptCtx, cancel := context.WithTimeout(ctx, galleryFetchTimeout)

		var body []byte
		err := downloader.URI(candidate).ReadWithAuthorizationAndCallback(
			attemptCtx, basePath, "",
			func(_ string, d []byte) error {
				body = d
				return nil
			})
		cancel()

		if err == nil {
			// A source that answers is usable again immediately; leaving the
			// record behind would keep a recovered host skipped.
			galleryFailures.Delete(candidate)
			// Keyed on the gallery's own URL rather than the candidate that
			// answered: a mirror serves the same index, so a mirror-served
			// fetch must refresh the copy an offline run will look for.
			persistGalleryIndex(basePath, g.URL, body)
			return body, candidate, nil
		}

		lastErr = err
		// Only blame the source for its own failures. If the caller gave up —
		// a browser disconnecting mid-listing, once a request context is wired
		// through here — recording that would blackhole every candidate for ten
		// minutes over something the sources had no part in.
		if ctx.Err() == nil {
			galleryFailures.Set(candidate, time.Now())
		}
		xlog.Warn("gallery source unreachable, trying the next one",
			"gallery", g.Name, "url", candidate, "error", err)
	}

	// Every source failed. A copy from a previous run is much better than no
	// gallery at all — this is what lets an offline or airgapped machine still
	// list what it already knows about.
	cachePath := galleryCachePath(basePath, g.URL)
	if cachePath != "" {
		// #nosec G304 -- cachePath is galleryCachePath's own construction: a
		// hex sha256 of the URL under the fixed <basePath>/../cache/gallery
		// directory, with a non-absolute basePath already rejected. No part of
		// it is caller-supplied text, so there is nothing to traverse with.
		if body, readErr := os.ReadFile(cachePath); readErr == nil {
			xlog.Warn("all gallery sources failed, serving the last known good copy",
				"gallery", g.Name, "path", cachePath, "error", lastErr)
			return body, cachePath, nil
		}
	}

	// Report what was configured and what was skipped, not just what we dialled:
	// "all 1 source(s) failed" on a gallery with three mirrors reads as a
	// misconfiguration and sends the operator looking for the missing mirrors,
	// when the truth is that two of them are in cooldown.
	return nil, "", fmt.Errorf("all %d source(s) for gallery %q failed (%d configured, %d skipped as recently failed) and no cached copy exists, last error: %w",
		len(attempt), g.Name, len(candidates), len(candidates)-len(attempt), lastErr)
}
