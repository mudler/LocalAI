package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
)

// prefetchNext is the suffix of a staged file, beside its place.
const prefetchNext = ".next"

// modelPrefetcher serves model.prefetch: the worker downloads and verifies its
// own copy of the files of a model upgrade, then puts them where a load of the
// model looks for them, so the frontend does not have to send them.
//
// A load stages a model file under the key models/<model>/<file>. Over HTTP
// the worker keeps it at <models>/<model>/<file>; with object storage, the
// worker's cache holds it at <cache>/models/<model>/<file> and a cached copy
// is used as it is. Both places are written so the next load finds the new
// file in either mode.
type modelPrefetcher struct {
	modelsPath string
	// cacheDir is the object storage cache, or "" when the worker has none.
	cacheDir string
	download func(ctx context.Context, uri, dest, sha256 string) error
	// validateURL refuses a URI the worker must not fetch. Nil allows every
	// URI that passes the scheme check of stage.
	validateURL func(uri string) error
}

func newModelPrefetcher(cfg *Config) *modelPrefetcher {
	p := &modelPrefetcher{modelsPath: cfg.ModelsPath, download: downloadVerified, validateURL: externalOnlyURL}
	if cfg.StorageURL != "" {
		p.cacheDir = filepath.Join(cfg.ModelsPath, "..", "cache")
	}
	return p
}

// externalOnlyURL applies to a plain http(s) URI the external-URL policy of
// pkg/httpclient (httpclient.WithExternalOnly): the host must resolve to
// public addresses only, so a gallery entry cannot make a worker fetch from
// its own network or from a metadata service. Other schemes (huggingface://,
// oci://) name fixed public services. The downloader follows redirects with
// its own client, so only the first URL is checked here.
func externalOnlyURL(uri string) error {
	lower := strings.ToLower(uri)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return httpclient.ValidateExternalURL(uri)
	}
	return nil
}

// cacheKey is the object storage key under which a load stages the file of a
// model, which is also the place of the file in the worker's cache.
func cacheKey(model, filename string) string {
	return storage.ModelKey(filepath.ToSlash(filepath.Join(model, filepath.FromSlash(filename))))
}

func downloadVerified(ctx context.Context, uri, dest, sha256 string) error {
	return downloader.URI(uri).DownloadFileWithContext(ctx, dest, sha256, 1, 1, func(string, string, string, float64) {})
}

func (p *modelPrefetcher) register(srv controlServer) error {
	// One request at a time, like files.ensure: the requests of one upgrade
	// come in order (stage, then commit or discard), and a request queued
	// behind a long download times out on the frontend, which then sends the
	// files itself at the next load.
	return srv.handle(verbModelPrefetch, unary(decodeJSON[workerctl.ModelPrefetchRequest], refuseModelPrefetch, p.serve))
}

func refuseModelPrefetch(err error) workerctl.ModelPrefetchReply {
	xlog.Warn("Ignoring malformed control request", "verb", verbModelPrefetch, "error", err)
	return workerctl.ModelPrefetchReply{Error: fmt.Sprintf("invalid request: %v", err)}
}

// places returns where a load looks for the file: the primary place first
// (the one this worker stages to), then the other.
func (p *modelPrefetcher) places(model, filename string) ([]string, error) {
	rel := filepath.FromSlash(filename)
	if model == "" || !filepath.IsLocal(model) || filename == "" || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("invalid model %q or file %q", model, filename)
	}
	httpPlace := filepath.Join(p.modelsPath, model, rel)
	if p.cacheDir == "" {
		return []string{httpPlace}, nil
	}
	key := cacheKey(model, filename)
	cachePlace := filepath.Join(p.cacheDir, filepath.FromSlash(key))
	return []string{cachePlace, httpPlace}, nil
}

func (p *modelPrefetcher) serve(ctx context.Context, req workerctl.ModelPrefetchRequest) workerctl.ModelPrefetchReply {
	xlog.Info("Received model.prefetch", "action", req.Action, "model", req.ModelName, "files", len(req.Files))
	switch req.Action {
	case workerctl.PrefetchStage, workerctl.PrefetchCommit, workerctl.PrefetchDiscard:
	default:
		return workerctl.ModelPrefetchReply{Error: fmt.Sprintf("unknown action %q", req.Action)}
	}
	var (
		placed []string
		errs   []error
	)
	for _, f := range req.Files {
		places, err := p.places(req.ModelName, f.Filename)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		switch req.Action {
		case workerctl.PrefetchStage:
			err = p.stage(ctx, f, places[0])
		case workerctl.PrefetchCommit:
			var moved bool
			moved, err = p.commit(places)
			if moved {
				placed = append(placed, f.Filename)
				p.recordVerified(req.ModelName, f)
			}
		case workerctl.PrefetchDiscard:
			err = p.discard(places, req.Evict)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.Filename, err))
			continue
		}
		if req.Action == workerctl.PrefetchStage {
			placed = append(placed, f.Filename)
		}
	}
	if err := errors.Join(errs...); err != nil {
		xlog.Warn("model.prefetch did not complete", "action", req.Action, "model", req.ModelName, "error", err)
		return workerctl.ModelPrefetchReply{Error: err.Error(), Placed: placed}
	}
	return workerctl.ModelPrefetchReply{Success: true, Placed: placed}
}

// stage downloads the file beside its place and verifies it. A file that
// fails its checksum is removed, so a commit cannot put it in place.
func (p *modelPrefetcher) stage(ctx context.Context, f workerctl.ModelPrefetchFile, place string) error {
	if f.SHA256 == "" {
		return errors.New("no sha256: a worker downloads only files it can verify")
	}
	if f.URI == "" || strings.HasPrefix(f.URI, "file://") || !strings.Contains(f.URI, "://") {
		return fmt.Errorf("the worker does not download from %q", f.URI)
	}
	if p.validateURL != nil {
		if err := p.validateURL(f.URI); err != nil {
			return fmt.Errorf("the worker does not download from %q: %w", f.URI, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(place), 0750); err != nil {
		return err
	}
	next := place + prefetchNext
	if err := p.download(ctx, f.URI, next, f.SHA256); err != nil {
		removeQuiet(next)
		removeQuiet(next + ".partial")
		return err
	}
	return nil
}

// commit moves the staged file into the primary place and removes the copy in
// the other place. With nothing staged it removes the copies, so the next
// load sends the frontend's file. moved reports whether a staged file went in.
func (p *modelPrefetcher) commit(places []string) (moved bool, err error) {
	next := places[0] + prefetchNext
	if _, statErr := os.Stat(next); statErr == nil {
		if err := os.Rename(next, places[0]); err != nil {
			return false, err
		}
		moved = true
	} else if err := utils.RemoveIfPresent(places[0]); err != nil {
		return false, err
	}
	for _, other := range places[1:] {
		if err := utils.RemoveIfPresent(other); err != nil {
			return moved, err
		}
	}
	return moved, nil
}

// recordVerified tells the object storage cache that the file placed in it
// was verified against its sha256 at its download, so the next files.ensure
// with that sha256 does not hash it again.
func (p *modelPrefetcher) recordVerified(model string, f workerctl.ModelPrefetchFile) {
	if p.cacheDir == "" || f.SHA256 == "" {
		return
	}
	if err := storage.RecordCacheSHA256(p.cacheDir, cacheKey(model, f.Filename), f.SHA256); err != nil {
		xlog.Warn("Could not record the sha256 of a prefetched file", "model", model, "file", f.Filename, "error", err)
	}
}

func (p *modelPrefetcher) discard(places []string, evict bool) error {
	var errs []error
	for _, place := range places {
		for _, path := range []string{place + prefetchNext, place + prefetchNext + ".partial"} {
			errs = append(errs, utils.RemoveIfPresent(path))
		}
		if evict {
			errs = append(errs, utils.RemoveIfPresent(place))
		}
	}
	return errors.Join(errs...)
}

func removeQuiet(path string) {
	if err := utils.RemoveIfPresent(path); err != nil {
		xlog.Warn("Could not remove a staged file", "file", path, "error", err)
	}
}
