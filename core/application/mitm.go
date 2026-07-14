package application

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/cloudproxy/mitm"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/xlog"
)

// startMITMIfConfigured brings up the cloudproxy MITM listener when an
// address is configured, treating any startup failure as non-fatal.
//
// The listener is opt-in middleware whose address is persisted in runtime
// settings (/api/settings → runtime_settings.json) and replayed on every
// boot. A bad value — e.g. a host the process can't bind, like a LAN IP
// inside a container — must NOT abort the whole server: doing so crash-loops
// with no way out, because the Settings UI used to correct the address can't
// load if startup never completes. So on failure we log loudly and carry on;
// the admin fixes the address via /api/settings, which calls RestartMITM.
func startMITMIfConfigured(app *Application, options *config.ApplicationConfig) {
	if options.MITMListen == "" {
		return
	}
	if err := startMITMProxy(app, options); err != nil {
		xlog.Error("mitm: cloudproxy listener failed to start — continuing without it",
			"listen", options.MITMListen,
			"error", err,
			"hint", "fix the address via Settings (e.g. \":8082\" to bind all interfaces) and the listener will restart",
		)
	}
}

func startMITMProxy(app *Application, options *config.ApplicationConfig) error {
	app.mitmMutex.Lock()
	defer app.mitmMutex.Unlock()
	return startMITMLocked(app, options)
}

func startMITMLocked(app *Application, options *config.ApplicationConfig) error {
	// Validate the host↔model-config 1-to-1 invariant before binding
	// the listener. Two configs claiming the same host means the
	// dispatcher would have ambiguous PII settings; refuse to start
	// rather than silently picking one. The conflict map is published
	// for /api/middleware/status to surface in the UI.
	ownership := app.backendLoader.MITMHostOwners()
	if len(ownership.Conflicts) > 0 {
		conflicts := ownership.Conflicts
		app.mitmHostConflicts.Store(&conflicts)
		hosts := make([]string, 0, len(conflicts))
		for h := range conflicts {
			hosts = append(hosts, h)
		}
		sort.Strings(hosts)
		xlog.Error("mitm: refusing to start — duplicate host claims across model configs",
			"hosts", hosts,
			"conflicts", conflicts,
		)
		return errors.New("mitm: configuration error: duplicate host claims (see /api/middleware/status)")
	}
	app.mitmHostConflicts.Store(nil)

	caDir := options.MITMCADir
	if caDir == "" {
		base := options.DataPath
		if base == "" {
			base = "."
		}
		caDir = filepath.Join(base, "mitm-ca")
	}

	if app.mitmCA.Load() == nil {
		ca, err := mitm.LoadOrCreateCA(caDir)
		if err != nil {
			return fmt.Errorf("ca: %w", err)
		}
		app.mitmCA.Store(ca)
	}

	// Allowlist is exactly the set of hosts claimed by model configs.
	// No global list — admins add hosts by creating an MITM model
	// config (template available in the Add Model UI). When no config
	// claims any host, the listener still starts but every CONNECT
	// tunnels through unmodified.
	effectiveHosts := make([]string, 0, len(ownership.Owners))
	for h := range ownership.Owners {
		effectiveHosts = append(effectiveHosts, h)
	}
	sort.Strings(effectiveHosts)

	// Per-host NER detectors come from the owning model's pii.detectors
	// (resolved against each detector model's pii_detection policy). A
	// host whose model has pii.enabled=false, lists no detectors, or
	// whose detectors can't be resolved gets no entry → it is intercepted
	// and forwarded unredacted (audit events still record traffic). An
	// unresolvable detector is recorded as an error-detector so the
	// request fails closed at request time rather than leaking.
	resolver := app.PIINERResolver()
	detectorsByHost := map[string][]pii.NERConfig{}
	for host, modelName := range ownership.Owners {
		cfg, exists := app.backendLoader.GetModelConfig(modelName)
		if !exists {
			continue
		}
		// Resolve through the shared policy so cloud-proxy hosts inherit the
		// instance-wide default detector when they name none of their own.
		enabled, detectors := app.ResolvePIIPolicy(&cfg)
		if !enabled || len(detectors) == 0 {
			continue
		}
		cfgs := make([]pii.NERConfig, 0, len(detectors))
		for _, name := range detectors {
			nc, ok := resolver(name)
			if !ok {
				xlog.Error("mitm: detector model not resolvable; requests to host will fail closed", "host", host, "detector", name)
				nc = pii.NERConfig{Detector: pii.NewErrNERDetector("detector model '" + name + "' not resolvable")}
			}
			cfgs = append(cfgs, nc)
		}
		detectorsByHost[host] = cfgs
	}

	handler := mitm.NewPIIHandler(mitm.PIIHandlerOptions{
		EventStore:      app.piiEvents,
		DetectorsByHost: detectorsByHost,
	})

	srv, err := mitm.NewServer(mitm.Config{
		Addr:           options.MITMListen,
		CA:             app.mitmCA.Load(),
		InterceptHosts: effectiveHosts,
		Handler:        handler,
		EventStore:     app.piiEvents,
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	if err := srv.Start(); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	app.mitmServer.Store(srv)

	xlog.Info("mitm: cloudproxy listener started",
		"addr", srv.Addr(),
		"ca_dir", caDir,
		"intercept_hosts", effectiveHosts,
		"model_owned_hosts", len(ownership.Owners),
		"pii_detector_hosts", len(detectorsByHost),
	)
	return nil
}

// StopMITM is idempotent.
func (a *Application) StopMITM() error {
	a.mitmMutex.Lock()
	defer a.mitmMutex.Unlock()
	stopMITMLocked(a)
	return nil
}

// RestartMITM reuses the existing CA so trusted clients keep
// working across listener flips.
func (a *Application) RestartMITM() error {
	a.mitmMutex.Lock()
	defer a.mitmMutex.Unlock()
	stopMITMLocked(a)
	if a.applicationConfig.MITMListen == "" {
		xlog.Info("mitm: cloudproxy listener stays disabled (no listen address)")
		return nil
	}
	return startMITMLocked(a, a.applicationConfig)
}

func stopMITMLocked(a *Application) {
	srv := a.mitmServer.Load()
	if srv == nil {
		return
	}
	srv.Stop()
	a.mitmServer.Store(nil)
	xlog.Info("mitm: cloudproxy listener stopped")
}
