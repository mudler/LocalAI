package httpapi

import (
	"fmt"
	"net/url"
)

// Route paths for the LocalAI admin REST surface that this client targets.
// Static paths are constants; dynamic paths are builders that handle
// url.PathEscape on segment values. Keep these aligned with the server-side
// registrations in core/http/routes/localai.go — the Tool↔REST drift detector
// in coverage_test.go documents the mapping.
const (
	routeWelcome       = "/"
	routeModelsApply   = "/models/apply"
	routeModelsAvail   = "/models/available"
	routeModelsGall    = "/models/galleries"
	routeModelsImport  = "/models/import-uri"
	routeModelsReload  = "/models/reload"
	routeBackends      = "/backends"
	routeBackendsKnown = "/backends/known"
	routeBackendsApply = "/backends/apply"
	routeNodes         = "/api/nodes"
	routeVRAMEstimate  = "/api/models/vram-estimate"
	routeBranding      = "/api/branding"
	routeSettings      = "/api/settings"
)

func routeJobStatus(jobID string) string {
	return "/models/jobs/" + url.PathEscape(jobID)
}

func routeModelDelete(name string) string {
	return "/models/delete/" + url.PathEscape(name)
}

func routeModelConfigJSON(name string) string {
	return "/api/models/config-json/" + url.PathEscape(name)
}

func routeBackendUpgrade(name string) string {
	return "/backends/upgrade/" + url.PathEscape(name)
}

func routeToggleModelState(name, action string) string {
	return fmt.Sprintf("/models/toggle-state/%s/%s", url.PathEscape(name), url.PathEscape(action))
}

func routeToggleModelPinned(name, action string) string {
	return fmt.Sprintf("/models/toggle-pinned/%s/%s", url.PathEscape(name), url.PathEscape(action))
}
