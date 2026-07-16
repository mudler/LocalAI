package config

import (
	"encoding/json"

	"github.com/mudler/LocalAI/pkg/vrambudget"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
)

// DefaultGalleriesJSON / DefaultBackendGalleriesJSON are the gallery lists
// an option-less `local-ai run` gets. cmd/local-ai feeds them to kong as
// the "${galleries}" / "${backends}" vars, and DefaultRuntimeBaseline uses
// them to tell "kong default" apart from "user-configured" at settings-load
// time - they must stay the single source for both.
const DefaultGalleriesJSON = `[{"name":"localai", "url":"github:mudler/LocalAI/gallery/index.yaml@master"}]`
const DefaultBackendGalleriesJSON = `[{"name":"localai", "url":"github:mudler/LocalAI/backend/index.yaml@master"}]`

func mustGalleries(jsonList string) []Gallery {
	var g []Gallery
	if err := json.Unmarshal([]byte(jsonList), &g); err != nil {
		// Both inputs are compile-time constants above; failing loudly at
		// init beats silently mis-detecting every gallery as env-set.
		panic("invalid built-in gallery JSON: " + err.Error())
	}
	return g
}

// DefaultRuntimeBaseline is the ApplicationConfig an option-less `local-ai
// run` produces: NewApplicationConfig plus the flag defaults kong injects
// even when the user passes nothing (see the default:"..." tags in
// core/cli/run.go). ApplyRuntimeSettingsAtStartup compares the live config
// against it to decide whether env/CLI claimed a field (env > file).
func DefaultRuntimeBaseline() *ApplicationConfig {
	o := NewApplicationConfig()
	// WithDebug(log level) is always applied by run.go; the default level
	// is not debug, so an option-less run boots with Debug=false even
	// though the NewApplicationConfig literal says true.
	o.Debug = false
	o.Galleries = mustGalleries(DefaultGalleriesJSON)
	o.BackendGalleries = mustGalleries(DefaultBackendGalleriesJSON)
	o.AutoloadGalleries = true
	o.AutoloadBackendGalleries = true
	// core/cli/run.go injects WithMemoryReclaimer(enabled, threshold)
	// unconditionally, so the kong threshold default (0.95) reaches the
	// config even when the reclaimer flag is off - this overlay must match
	// that contract or a UI-persisted threshold looks env-set and is
	// skipped at boot.
	o.MemoryReclaimerThreshold = 0.95
	return o
}

// ApplyRuntimeSettingsAtStartup merges persisted settings into o with
// env-over-file precedence: a field is taken from the file only when the
// live value still equals the option-less baseline (env/CLI did not touch
// it) or when the file is that field's only source (fileAuthoritative).
// Used at boot (application.New) and by the runtime_settings.json file
// watcher, so a manual file edit behaves exactly like a boot-time load.
//
// Known limitation (accepted): an env var explicitly set to its default
// value is indistinguishable from "not set", so the file wins there; and a
// field previously changed via the API looks env-set to the watcher, so a
// manual file edit of that field lands on the next restart.
func (o *ApplicationConfig) ApplyRuntimeSettingsAtStartup(settings *RuntimeSettings) {
	if settings == nil {
		return
	}
	baseline := DefaultRuntimeBaseline()
	for _, f := range runtimeSettingsFields {
		if f.snapshotOnly || !f.isSet(settings) {
			continue
		}
		if !f.fileAuthoritative && f.envSet(o, baseline) {
			continue
		}
		f.apply(o, settings)
	}
	// Startup invariant, mirroring ApplyRuntimeSettings and the gating in
	// startWatchdog: enabled idle/busy checks or the memory reclaimer imply
	// the watchdog master flag. Never forced off here - an explicit
	// watchdog_enabled=false row already applied above when unclaimed.
	if o.WatchDogIdle || o.WatchDogBusy || o.MemoryReclaimerEnabled {
		o.WatchDog = true
	}
	// VRAM budget post-processing, mirroring ApplyRuntimeSettings: at boot
	// only run.go's env path installs the process-wide cap, so a
	// file-persisted budget applied by the loop above would set
	// o.VRAMBudget without ever capping allocations. When env set the
	// budget the loop skipped the file value and this re-installs the env
	// value - idempotent. Fail-open on a malformed persisted value so it
	// cannot wedge startup.
	if settings.VRAMBudget != nil {
		if b, err := vrambudget.Parse(o.VRAMBudget); err == nil {
			xsysinfo.SetDefaultVRAMBudget(b)
		}
	}
}
