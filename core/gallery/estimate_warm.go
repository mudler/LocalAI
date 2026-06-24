package gallery

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/concurrency"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/vram"
	"github.com/mudler/xlog"
)

// EstimateInput builds the VRAM estimator's input from a gallery entry.
//
// It lives here rather than beside the HTTP handler because two callers need
// it: the handler answering one model, and the warmer below answering all of
// them ahead of time.
func EstimateInput(m *GalleryModel) vram.ModelEstimateInput {
	var input vram.ModelEstimateInput
	input.Size = m.Size
	if repoID := extractHFRepo(m.Overrides, m.URLs); repoID != "" {
		input.HFRepo = repoID
	}
	for _, f := range m.AdditionalFiles {
		if vram.IsWeightFile(f.URI) {
			input.Files = append(input.Files, vram.FileInput{URI: f.URI, Size: 0})
		}
	}
	return input
}

// extractHFRepo finds a HuggingFace repo ID in a model's overrides or URLs.
func extractHFRepo(overrides map[string]any, urls []string) string {
	if overrides != nil {
		if params, ok := overrides["parameters"].(map[string]any); ok {
			if modelRef, ok := params["model"].(string); ok {
				if repoID, ok := vram.ExtractHFRepoID(modelRef); ok {
					return repoID
				}
			}
		}
	}
	for _, u := range urls {
		if repoID, ok := vram.ExtractHFRepoID(u); ok {
			return repoID
		}
	}
	return ""
}

// EstimateWarmConfig bounds the background warm-up.
type EstimateWarmConfig struct {
	// Limit is how many gallery entries to warm, in gallery order. Zero
	// disables warming entirely. The order matters: it is the order the UI
	// lists them in, so the entries a user sees first are warmed first.
	Limit int
	// Concurrency is how many estimates run at once. Each one can be a remote
	// probe, so this is deliberately small: the point is to be finished before
	// anybody looks, not to saturate the link or the upstream.
	Concurrency int
	// Contexts are the context lengths to estimate at. These want to match what
	// the UI asks for, or the warmed entry is not the one it reads.
	Contexts []uint32
}

// DefaultEstimateWarmConfig is what the server uses unless told otherwise.
//
// The limit is a deliberate compromise. Warming the whole gallery would be
// thousands of remote probes on every boot, which is rude to the upstream and
// slow to finish; warming nothing leaves the first page of the model gallery
// paying two seconds per row. A few hundred covers what anyone browses in a
// sitting, and everything past it still warms itself on first view.
var DefaultEstimateWarmConfig = EstimateWarmConfig{
	Limit:       300,
	Concurrency: 4,
	Contexts:    []uint32{8192, 16384, 32768, 65536, 131072, 262144},
}

// WarmEstimateCache fills the gallery's derived caches in the background.
//
// Two things are warmed, and they are the same cost wearing different hats.
// An estimate for an entry the server has never seen costs a network probe of
// its weight files, and describing an entry's variants costs one probe per
// build it offers. The UI asks for an estimate per row and a variant
// description per model opened, so without this the first visitor pays for
// both: ten seconds of a page filling in its own sizes, then another second
// and a half the first time they click anything.
//
// Both land in the same caches underneath, which is why one pass covers them.
//
// It returns immediately; the work happens on its own goroutine and stops when
// ctx is done. Failures are logged at debug and otherwise ignored: a warm-up
// that cannot reach an upstream must never stop the server from starting, and
// the entry it failed on simply stays cold.
func WarmEstimateCache(ctx context.Context, galleries []config.Gallery, systemState *system.SystemState, cfg EstimateWarmConfig) {
	if cfg.Limit <= 0 || cfg.Concurrency <= 0 {
		return
	}

	concurrency.SafeGo(func() {
		started := time.Now()

		models, err := AvailableGalleryModelsCached(galleries, systemState)
		if err != nil {
			xlog.Debug("VRAM estimate warm-up skipped, gallery unavailable", "error", err)
			return
		}
		if len(models) > cfg.Limit {
			models = models[:cfg.Limit]
		}
		if len(models) == 0 {
			return
		}

		// The host gate the variant picker resolves against. Derived once: it
		// describes this machine, not this entry, and HostResolveEnv reads the
		// system state to build it.
		env := HostResolveEnv(ctx, systemState)

		var (
			wg             sync.WaitGroup
			cursor         = make(chan *GalleryModel)
			warmed         int
			warmedVariants int
			mu             sync.Mutex
		)

		for i := 0; i < cfg.Concurrency; i++ {
			wg.Add(1)
			concurrency.SafeGo(func() {
				defer wg.Done()
				for m := range cursor {
					// Per entry, not for the run: one unreachable weight file
					// must not hold a worker for the whole warm-up.
					entryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)

					input := EstimateInput(m)
					if len(input.Files) > 0 || input.HFRepo != "" || input.Size != "" {
						if _, err := vram.EstimateModelMultiContext(entryCtx, input, cfg.Contexts); err != nil {
							xlog.Debug("VRAM estimate warm-up failed for entry", "model", m.GetName(), "error", err)
						} else {
							mu.Lock()
							warmed++
							mu.Unlock()
						}
					}

					// Describing variants probes each build the entry offers.
					// An entry that declares none costs nothing here, so this is
					// gated rather than attempted and discarded.
					if m.HasVariants() {
						if _, err := DescribeVariants(models, m, env); err != nil {
							xlog.Debug("variant warm-up failed for entry", "model", m.GetName(), "error", err)
						} else {
							mu.Lock()
							warmedVariants++
							mu.Unlock()
						}
					}

					cancel()
				}
			})
		}

	feed:
		for _, m := range models {
			select {
			case <-ctx.Done():
				break feed
			case cursor <- m:
			}
		}
		close(cursor)
		wg.Wait()

		if ctx.Err() != nil {
			xlog.Debug("gallery warm-up stopped", "estimates", warmed, "variants", warmedVariants)
			return
		}
		xlog.Info("gallery caches warmed", "estimates", warmed, "variants", warmedVariants, "of", len(models), "took", time.Since(started).Round(time.Second))
	})
}

// EstimateWarmConfigFromEnv reads the warm-up bounds from the environment,
// falling back to the defaults.
//
//	LOCALAI_VRAM_WARM_LIMIT        entries to warm; 0 disables the warm-up
//	LOCALAI_VRAM_WARM_CONCURRENCY  estimates in flight at once
//
// Env rather than a flag because it is an operational tuning knob, not part of
// what the server does: an air-gapped host wants it off, and a host behind a
// slow link wants it slower, and neither is a decision the CLI should carry.
func EstimateWarmConfigFromEnv() EstimateWarmConfig {
	cfg := DefaultEstimateWarmConfig
	if v, ok := os.LookupEnv("LOCALAI_VRAM_WARM_LIMIT"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			cfg.Limit = n
		}
	}
	if v, ok := os.LookupEnv("LOCALAI_VRAM_WARM_CONCURRENCY"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			cfg.Concurrency = n
		}
	}
	return cfg
}
