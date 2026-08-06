package gallery

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/xsync"
	"github.com/mudler/xlog"
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

// fetchGalleryIndex returns the raw index bytes and the URL that served them,
// trying each candidate in order.
//
// A candidate in cooldown is skipped, unless every candidate is in cooldown —
// in which case the cooldown is ignored rather than failing outright, because
// refusing to serve a gallery we might be able to reach is worse than one slow
// request.
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

	return nil, "", fmt.Errorf("all %d source(s) for gallery %q failed, last error: %w",
		len(attempt), g.Name, lastErr)
}

// resetGalleryFailures and expireGalleryFailure exist so tests can drive the
// cooldown without sleeping.
func resetGalleryFailures() {
	for _, k := range galleryFailures.Keys() {
		galleryFailures.Delete(k)
	}
}

func expireGalleryFailure(url string, at time.Time) {
	galleryFailures.Set(url, at)
}
