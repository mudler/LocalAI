package application

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/cloudproxy/mitm"
	"github.com/mudler/xlog"
)

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

	// Per-host PII gate inherits from the owning model's pii.enabled.
	// A non-cloud-proxy backend with no explicit pii.enabled resolves
	// to false → host is intercepted but the regex pass is skipped
	// (audit events still record).
	var piiDisabled []string
	for host, modelName := range ownership.Owners {
		cfg, exists := app.backendLoader.GetModelConfig(modelName)
		if !exists {
			continue
		}
		if !cfg.PIIIsEnabled() {
			piiDisabled = append(piiDisabled, host)
		}
	}

	handler := mitm.NewPIIHandler(mitm.PIIHandlerOptions{
		Redactor:             app.piiRedactor,
		EventStore:           app.piiEvents,
		HostsWithPIIDisabled: piiDisabled,
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
		"pii_disabled_hosts", len(piiDisabled),
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
