package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

// modelInstaller is the subset of gallery.InstallModelFromGallery the prefetch
// loop needs. Carved out as a function value so tests can substitute a fake
// installer without touching the real gallery — and so we don't duplicate the
// full install pipeline (URL resolution, SHA verification, idempotent skip,
// config-file write) which already lives in core/gallery/models.go.
type modelInstaller func(
	ctx context.Context,
	modelGalleries, backendGalleries []config.Gallery,
	systemState *system.SystemState,
	modelLoader *model.ModelLoader,
	name string,
) error

// realModelInstaller is the production binding to gallery.InstallModelFromGallery.
// Kept as a package-level var so tests can swap it; production code never
// reassigns it.
var realModelInstaller modelInstaller = func(
	ctx context.Context,
	modelGalleries, backendGalleries []config.Gallery,
	systemState *system.SystemState,
	modelLoader *model.ModelLoader,
	name string,
) error {
	// enforceScan=false: workers fetch from the same gallery the master already
	// trusts, and the master would have scanned at install time anyway.
	// autoloadBackendGalleries=false: the worker installs backends on demand via
	// backend.install NATS events; prefetching the backend here would race the
	// supervisor's own install path and double-trigger gallery work.
	// requireBackendIntegrity=false: same reason — we're not installing a backend.
	return gallery.InstallModelFromGallery(
		ctx,
		modelGalleries,
		backendGalleries,
		systemState,
		modelLoader,
		name,
		gallery.GalleryModel{},
		nil, /* downloadStatus: silent on the worker; master is the UX surface */
		false /* enforceScan */, false /* autoloadBackendGalleries */, false, /* requireBackendIntegrity */
	)
}

// prefetchModels resolves each configured gallery ID against the model gallery
// and downloads the artifact into the worker's /models. It is called once at
// worker startup, BEFORE the NATS lifecycle subscription, so that the steady
// state has the file already on disk and the master never needs to stream it.
//
// Errors are intentionally non-fatal: on a fresh worker with no outbound
// connectivity (or a misconfigured gallery JSON), we want the worker to still
// register and serve traffic — the master will fall back to pushing files
// on-demand over NATS/HTTP, which is the pre-existing behavior. Per-model
// failures are logged at warn level and the loop continues with the next ID.
//
// Idempotency comes for free from pkg/downloader.URI.DownloadFileWithContext:
// it stats the target path, hashes it if a SHA is configured, and short-circuits
// on a match. So restarts against a populated PVC are effectively no-ops.
func prefetchModels(
	ctx context.Context,
	cfg *Config,
	systemState *system.SystemState,
	ml *model.ModelLoader,
	backendGalleries []config.Gallery,
	installer modelInstaller,
) {
	models := normalizePrefetchList(cfg.PrefetchModels)
	if len(models) == 0 {
		return
	}

	modelGalleries, err := parseModelGalleries(cfg.Galleries)
	if err != nil {
		// Without a model-gallery config we cannot resolve gallery IDs. Warn
		// and let the worker proceed — the master can still push files later.
		xlog.Warn("Skipping model prefetch: invalid LOCALAI_GALLERIES", "error", err)
		return
	}
	if len(modelGalleries) == 0 {
		xlog.Warn("Skipping model prefetch: no model galleries configured (set LOCALAI_GALLERIES)", "models", models)
		return
	}

	if installer == nil {
		installer = realModelInstaller
	}

	xlog.Info("Prefetching models from gallery before entering NATS loop", "count", len(models), "models", models)
	for _, name := range models {
		xlog.Info("Prefetching model", "model", name)
		if err := installer(ctx, modelGalleries, backendGalleries, systemState, ml, name); err != nil {
			// Non-fatal: master can still push the file on demand. We log
			// loudly so an operator can spot a misconfigured gallery ID or
			// a missing outbound route without the worker crash-looping.
			xlog.Warn("Model prefetch failed; master will push on demand", "model", name, "error", err)
			continue
		}
		xlog.Info("Prefetched model", "model", name)
	}
}

// normalizePrefetchList trims whitespace and drops empty entries. kong already
// splits comma-separated env values into []string, but callers using the CLI
// flag repeatedly (or pasting whitespace) can produce stragglers we don't want
// to ship into the gallery resolver as "" or "  ".
func normalizePrefetchList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// parseModelGalleries parses the JSON-encoded LOCALAI_GALLERIES value the same
// way the master does. Returns an empty slice (not nil) and nil error when the
// input is empty, so callers can treat "" as "not configured" without a
// secondary check.
func parseModelGalleries(raw string) ([]config.Gallery, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []config.Gallery{}, nil
	}
	var galleries []config.Gallery
	if err := json.Unmarshal([]byte(raw), &galleries); err != nil {
		return nil, fmt.Errorf("parsing model galleries JSON: %w", err)
	}
	return galleries, nil
}
