package localai

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/xlog"
)

// BackendUpgradeGuard refuses a backend upgrade request that cannot do
// anything, before an operation is queued. Without it the request is accepted
// with 200 and the job fails later with the same answer, so a client that
// starts an upgrade run learns about a typo, a removed backend or a build that
// moved on only after it has waited for the job.
//
// A nil guard refuses nothing.
type BackendUpgradeGuard struct {
	installed func() (gallery.SystemBackends, error)
	upgrades  UpgradeInfoProvider
}

// upgradeCheckObserver is the optional part of the upgrade checker that says
// whether any check has finished. Before the first one the pending list is
// empty because nothing was looked at, not because nothing is pending.
type upgradeCheckObserver interface {
	LastSuccessTime() time.Time
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
	if v := reflect.ValueOf(upgrades); v.Kind() == reflect.Ptr && v.IsNil() {
		// A nil *UpgradeChecker boxed in the interface is not a nil interface.
		upgrades = nil
	}
	return &BackendUpgradeGuard{
		installed: func() (gallery.SystemBackends, error) {
			manager := svc.BackendManager()
			if manager == nil {
				return nil, errors.New("no backend manager")
			}
			return manager.ListBackends()
		},
		upgrades: upgrades,
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

// Check returns nil when the upgrade may be queued, and the refusal when not.
// A request that names a node (nodeID) also needs the backend on that node.
// expectedTo is the build the caller saw and wants: the available version or
// digest. When it no longer is the available one, the answer is stale.
//
// Check never refuses on a failure to look: an unreadable install state or an
// unfinished first check leave the decision to the upgrade job.
func (g *BackendUpgradeGuard) Check(name, nodeID, expectedTo string) *UpgradeRefusal {
	if g == nil {
		return nil
	}
	installed, err := g.installed()
	if err != nil {
		xlog.Warn("Could not read the installed backends, not checking the upgrade request", "backend", name, "error", err)
		return nil
	}
	entry, ok := installed.Get(name)
	if !ok {
		return &UpgradeRefusal{http.StatusNotFound, gallery.UpgradeFailureNotInstalled,
			fmt.Sprintf("backend %q is not installed", name)}
	}
	if nodeID != "" && len(entry.Nodes) > 0 {
		onNode := false
		for _, n := range entry.Nodes {
			if n.NodeID == nodeID {
				onNode = true
				break
			}
		}
		if !onNode {
			return &UpgradeRefusal{http.StatusNotFound, gallery.UpgradeFailureNotInstalled,
				fmt.Sprintf("backend %q is not installed on node %s", name, nodeID)}
		}
	}
	if entry.IsSystem {
		return &UpgradeRefusal{http.StatusConflict, gallery.UpgradeFailureNoUpdate,
			fmt.Sprintf("backend %q is provided by the system and has no gallery upgrade", name)}
	}
	if g.upgrades == nil {
		return nil
	}
	if obs, ok := g.upgrades.(upgradeCheckObserver); ok && obs.LastSuccessTime().IsZero() {
		return nil
	}

	// The checker lists the concrete backend; a meta backend upgrades the
	// concrete one it points to.
	target := name
	if entry.Metadata != nil && entry.Metadata.MetaBackendFor != "" {
		target = entry.Metadata.MetaBackendFor
	}
	info, pending := g.upgrades.GetAvailableUpgrades()[target]
	if !pending {
		return &UpgradeRefusal{http.StatusConflict, gallery.UpgradeFailureNoUpdate,
			fmt.Sprintf("backend %q has no pending upgrade", name)}
	}
	if expectedTo != "" && expectedTo != info.AvailableVersion && expectedTo != info.AvailableDigest {
		available := info.AvailableVersion
		if available == "" {
			available = info.AvailableDigest
		}
		return &UpgradeRefusal{http.StatusConflict, gallery.UpgradeFailureStale,
			fmt.Sprintf("backend %q: the available build is %q, not the requested %q", name, available, expectedTo)}
	}
	return nil
}

// ExpectedToOf reads the optional expected_to of an upgrade request: the query
// parameter, or the field of a JSON body. A request with no body, which is
// what every existing client sends, has none.
func ExpectedToOf(c echo.Context) (string, error) {
	if v := c.QueryParam("expected_to"); v != "" {
		return v, nil
	}
	if c.Request().Body == nil || c.Request().ContentLength == 0 {
		return "", nil
	}
	var body struct {
		ExpectedTo string `json:"expected_to"`
	}
	if err := json.NewDecoder(c.Request().Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("invalid request body: %w", err)
	}
	return body.ExpectedTo, nil
}
