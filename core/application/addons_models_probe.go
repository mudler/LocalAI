// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/modelsprobe"
	"github.com/mudler/xlog"
)

// Model export states. Only a mismatch turns exports off: an owner that does
// not answer, or that is older than this check, leaves them on.
const (
	ModelExportsShared   = "shared"
	ModelExportsMismatch = "mismatch"
	ModelExportsUnknown  = "unknown"
)

// modelsProbeInterval is how often a frontend looks at the probe again. It
// also covers delayed visibility on a network filesystem.
const modelsProbeInterval = 60 * time.Second

// modelsProbeState is per-instance state (mode 4 of
// .agents/distributed-state.md): each process checks its own view of the
// storage, and that view is what the state describes.
type modelsProbeState struct {
	// own is the probe this process wrote; only the owner sets it.
	own atomic.Pointer[modelsprobe.Probe]
	// state is the last frontend result; the zero value reads as unknown.
	state atomic.Pointer[string]
	// interval is set by tests only.
	interval time.Duration
}

func (a *Application) modelsPath() string {
	if a.applicationConfig == nil || a.applicationConfig.SystemState == nil {
		return ""
	}
	return a.applicationConfig.SystemState.Model.ModelsPath
}

// writeModelsProbe is called by the owner at start. A failure does not stop
// the owner: it keeps running and refuses exports (the owner guard).
func (a *Application) writeModelsProbe() {
	dir := a.modelsPath()
	if dir == "" {
		xlog.Error("addons: no models path is set, so the shared models path cannot be checked; model exports are refused")
		return
	}
	p, err := modelsprobe.Write(dir)
	if err != nil {
		xlog.Error("addons: the models path probe could not be written; model exports are refused", "path", dir, "error", err)
		return
	}
	a.modelsProbe.own.Store(&p)
}

// ownerProbeCurrent reports whether the probe the owner wrote is still in its
// models path with the same nonce.
func (a *Application) ownerProbeCurrent() bool {
	own := a.modelsProbe.own.Load()
	if own == nil {
		return false
	}
	got, err := modelsprobe.Read(a.modelsPath())
	return err == nil && modelsprobe.Same(*own, got)
}

// checkModelsProbe is one frontend check against the owner.
func (a *Application) checkModelsProbe(ctx context.Context) {
	g := a.addonGateway
	if g == nil || g.Forward == nil {
		return
	}
	next, reason := ModelExportsShared, ""
	remote, err := g.Forward.ModelsProbe(ctx)
	switch {
	case err != nil:
		next, reason = ModelExportsUnknown, "the add-on owner did not give its probe"
	default:
		local, rerr := modelsprobe.Read(a.modelsPath())
		if rerr != nil {
			next, reason = ModelExportsMismatch, "the probe file of the owner is not visible in this models path"
		} else if abs, _ := filepath.Abs(a.modelsPath()); !modelsprobe.Same(remote, local) || abs != remote.ModelsPath {
			// The file records the owner's path, so the path of this instance
			// is compared on its own: a symlink to the same storage fails.
			next, reason = ModelExportsMismatch, "the probe file in this models path or its path differs from the owner's"
		}
	}
	prev := ModelExportsUnknown
	if p := a.modelsProbe.state.Load(); p != nil {
		prev = *p
	}
	a.modelsProbe.state.Store(&next)
	if next == prev {
		return
	}
	switch next {
	case ModelExportsMismatch:
		xlog.Error("addons: this instance does not share the models path of the add-on owner; model exports are blocked here",
			"reason", reason, "models_path", a.modelsPath(), "owner_models_path", remote.ModelsPath)
	case ModelExportsUnknown:
		xlog.Warn("addons: the shared models path could not be checked; model exports stay on", "reason", reason)
	}
}

func (a *Application) runModelsProbe(ctx context.Context) {
	iv := a.modelsProbe.interval
	if iv <= 0 {
		iv = modelsProbeInterval
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		a.checkModelsProbe(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// AddonModelExports is the one reader of the shared models path state:
// "shared", "mismatch" or "unknown". An owner or combined process answers from
// its own probe. Without the add-on carrier there is nothing to check and the
// answer is "shared".
func (a *Application) AddonModelExports(_ context.Context) string {
	cfg := a.applicationConfig.Addons
	if !cfg.Enabled || cfg.Transport == nil {
		return ModelExportsShared
	}
	if cfg.InstanceID == cfg.OwnerID {
		if a.ownerProbeCurrent() {
			return ModelExportsShared
		}
		return ModelExportsMismatch
	}
	if p := a.modelsProbe.state.Load(); p != nil {
		return *p
	}
	return ModelExportsUnknown
}

// ModelExportsAllowed is what the export routes and /api/features ask.
func (a *Application) ModelExportsAllowed(ctx context.Context) bool {
	return a.AddonModelExports(ctx) != ModelExportsMismatch
}

// guardedExporter refuses an export when the probe of the owner's models path
// is missing. Reads stay open.
type guardedExporter struct {
	hostops.Exporter
	ok func() bool
}

func (g guardedExporter) ExportModel(ctx context.Context, userID, jobID string, req schema.ExportRequest) (string, error) {
	if !g.ok() {
		return "", modelsprobe.ErrNotShared
	}
	return g.Exporter.ExportModel(ctx, userID, jobID, req)
}

type guardedImporter struct {
	hostops.Importer
	ok func() bool
}

func (g guardedImporter) ImportModel(ctx context.Context, userID, jobID string, req schema.QuantizationImportRequest) (string, error) {
	if !g.ok() {
		return "", modelsprobe.ErrNotShared
	}
	return g.Importer.ImportModel(ctx, userID, jobID, req)
}
