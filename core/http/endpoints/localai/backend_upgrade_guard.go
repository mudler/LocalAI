package localai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

// BackendUpgradeGuard is backendupdates.UpgradeGuard for the HTTP routes: it
// answers a refusal with its status. A nil guard refuses nothing.
type BackendUpgradeGuard struct {
	core *backendupdates.UpgradeGuard
}

// NewBackendUpgradeGuard reads the installed backends from the backend manager
// of the gallery service (the cluster view in distributed mode) and the
// pending upgrades from the checker. It returns nil, a guard that refuses
// nothing, when there is no gallery service. A nil checker disables the
// refusal for a backend with no pending upgrade.
func NewBackendUpgradeGuard(svc *galleryop.GalleryService, upgrades UpgradeInfoProvider) *BackendUpgradeGuard {
	if svc == nil {
		return nil
	}
	return &BackendUpgradeGuard{core: backendupdates.NewUpgradeGuard(installedBackendsOf(svc), upgrades)}
}

// installedBackendsOf reads the installed backends from the backend manager
// of svc, for backendupdates.NewUpgradeGuard.
func installedBackendsOf(svc *galleryop.GalleryService) func() (gallery.SystemBackends, error) {
	return func() (gallery.SystemBackends, error) {
		manager := svc.BackendManager()
		if manager == nil {
			return nil, backendupdates.ErrNoBackendManager
		}
		return manager.ListBackends()
	}
}

// UpgradeRefusal is the answer to an upgrade request that was refused.
type UpgradeRefusal struct {
	Status  int
	Code    gallery.UpgradeFailureCode
	Message string
}

// Write sends the refusal. The body has the shape of the other refusals of the
// backend routes: an error text and a stable code.
func (r *UpgradeRefusal) Write(c echo.Context) error {
	return c.JSON(r.Status, map[string]any{"error": r.Message, "code": string(r.Code)})
}

// Check returns nil when the upgrade may be queued, and the refusal when not:
// 404 not_installed, 409 no_update or stale (see backendupdates.UpgradeGuard).
func (g *BackendUpgradeGuard) Check(ctx context.Context, name, nodeID, expectedTo string) *UpgradeRefusal {
	if g == nil {
		return nil
	}
	r := g.core.Check(ctx, name, nodeID, expectedTo)
	if r == nil {
		return nil
	}
	status := http.StatusConflict
	if r.Code == gallery.UpgradeFailureNotInstalled {
		status = http.StatusNotFound
	}
	return &UpgradeRefusal{Status: status, Code: r.Code, Message: r.Message}
}

// ExpectedToOf reads the optional expected_to of an upgrade request: the query
// parameter, or the field of a JSON body. A request with no body, which is
// what every existing client sends, has none. The body stays readable for the
// handler after it: the single upgrade routes read it once to refuse early and
// again in the handler that the run of one falls back to.
func ExpectedToOf(c echo.Context) (string, error) {
	if v := c.QueryParam("expected_to"); v != "" {
		return v, nil
	}
	req := c.Request()
	if req.Body == nil || req.ContentLength == 0 {
		return "", nil
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("invalid request body: %w", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	if len(bytes.TrimSpace(body)) == 0 {
		return "", nil
	}
	var v struct {
		ExpectedTo string `json:"expected_to"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", fmt.Errorf("invalid request body: %w", err)
	}
	return v.ExpectedTo, nil
}
