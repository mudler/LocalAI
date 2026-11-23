package localai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/backendupdates"
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

// freshChecker is the optional part of the upgrade checker that runs a check
// and waits for it, the one that "Check now" on the Updates page uses. It
// shares a check that already runs.
type freshChecker interface {
	CheckNow(ctx context.Context) backendupdates.Snapshot
}

const (
	// upgradeListMaxAge is how old the last successful check may be before a
	// "nothing to upgrade" answer is not trusted. The list is refreshed about
	// every six hours, so a build published since is not in it.
	upgradeListMaxAge = 5 * time.Minute
	// upgradeFreshCheckTimeout bounds the check that a request waits for.
	upgradeFreshCheckTimeout = 15 * time.Second
)

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
// Check never refuses on a failure to look: an unreadable install state, or a
// fresh check that fails or runs out of time, leave the decision to the
// upgrade job. A "nothing to upgrade" answer is only given from a list that was
// computed in the last five minutes; an older or missing list is refreshed
// first, within fifteen seconds.
func (g *BackendUpgradeGuard) Check(ctx context.Context, name, nodeID, expectedTo string) *UpgradeRefusal {
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

	// The checker lists the concrete backend; a meta backend upgrades the
	// concrete one it points to.
	target := name
	if entry.Metadata != nil && entry.Metadata.MetaBackendFor != "" {
		target = entry.Metadata.MetaBackendFor
	}
	info, pending := g.upgrades.GetAvailableUpgrades()[target]
	if !pending {
		var decided bool
		info, pending, decided = g.freshen(ctx, target)
		if !decided {
			return nil
		}
	}
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

// freshen settles a "nothing pending" answer. When the list is recent it is
// the answer. When it is old or was never computed, and the checker can run a
// check, the check runs and the fresh list decides. decided is false when no
// answer can be given: the check failed or timed out, or nothing says that the
// list was ever computed.
func (g *BackendUpgradeGuard) freshen(ctx context.Context, target string) (info gallery.UpgradeInfo, pending, decided bool) {
	obs, observed := g.upgrades.(upgradeCheckObserver)
	checker, canCheck := g.upgrades.(freshChecker)
	var last time.Time
	if observed {
		last = obs.LastSuccessTime()
	}
	if observed && !last.IsZero() && time.Since(last) <= upgradeListMaxAge {
		return gallery.UpgradeInfo{}, false, true
	}
	if !canCheck || !observed {
		// Nothing can refresh the list. A list that was computed is the answer;
		// one that never was says nothing.
		if observed && !last.IsZero() {
			return gallery.UpgradeInfo{}, false, true
		}
		return gallery.UpgradeInfo{}, false, !observed
	}
	started := time.Now()
	cctx, cancel := context.WithTimeout(ctx, upgradeFreshCheckTimeout)
	defer cancel()
	checker.CheckNow(cctx)
	if !obs.LastSuccessTime().After(started) {
		xlog.Warn("The fresh update check did not succeed, not refusing the upgrade", "backend", target)
		return gallery.UpgradeInfo{}, false, false
	}
	info, pending = g.upgrades.GetAvailableUpgrades()[target]
	return info, pending, true
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
