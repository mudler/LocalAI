package backendupdates

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/xlog"
)

// UpgradeInfoSource is the part of the upgrade checker the guard reads: the
// backends with a pending upgrade. The checker of the application also says
// when a check last succeeded and can run one (see the optional interfaces
// below), which the guard uses when it has them.
type UpgradeInfoSource interface {
	GetAvailableUpgrades() map[string]gallery.UpgradeInfo
}

// UpgradeGuard refuses a backend upgrade request that cannot do anything,
// before an operation is queued. Without it the request is accepted and the
// job fails later with the same answer, so a client learns about a typo, a
// removed backend or a build that moved on only after it has waited for the
// job. The REST routes and the in-process assistant use the same guard.
//
// A nil guard refuses nothing.
type UpgradeGuard struct {
	installed func() (gallery.SystemBackends, error)
	upgrades  UpgradeInfoSource
}

// NewUpgradeGuard reads the installed backends from installed (the backend
// manager, the cluster view in distributed mode) and the pending upgrades
// from upgrades. A nil installed returns nil, a guard that refuses nothing. A
// nil upgrades disables the refusal for a backend with no pending upgrade.
func NewUpgradeGuard(installed func() (gallery.SystemBackends, error), upgrades UpgradeInfoSource) *UpgradeGuard {
	if installed == nil {
		return nil
	}
	if v := reflect.ValueOf(upgrades); v.Kind() == reflect.Pointer && v.IsNil() {
		// A nil *UpgradeChecker boxed in the interface is not a nil interface.
		upgrades = nil
	}
	return &UpgradeGuard{installed: installed, upgrades: upgrades}
}

// UpgradeRefusal says why an upgrade request was refused: the code is
// not_installed, no_update or stale.
type UpgradeRefusal struct {
	Code    gallery.UpgradeFailureCode
	Message string
}

func (r *UpgradeRefusal) Error() string { return string(r.Code) + ": " + r.Message }

// ErrNoBackendManager is returned by an installed reader that has no backend
// manager to ask.
var ErrNoBackendManager = errors.New("no backend manager")

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
	CheckNow(ctx context.Context) Snapshot
}

const (
	// upgradeListMaxAge is how old the last successful check may be before a
	// "nothing to upgrade" answer is not trusted. The list is refreshed about
	// every six hours, so a build published since is not in it.
	upgradeListMaxAge = 5 * time.Minute
	// upgradeFreshCheckTimeout bounds the check that a request waits for.
	upgradeFreshCheckTimeout = 15 * time.Second
)

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
func (g *UpgradeGuard) Check(ctx context.Context, name, nodeID, expectedTo string) *UpgradeRefusal {
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
		return &UpgradeRefusal{gallery.UpgradeFailureNotInstalled,
			fmt.Sprintf("backend %q is not installed", name)}
	}
	if nodeID != "" && len(entry.Nodes) > 0 {
		onNode := slices.ContainsFunc(entry.Nodes, func(n gallery.NodeBackendRef) bool { return n.NodeID == nodeID })
		if !onNode {
			return &UpgradeRefusal{gallery.UpgradeFailureNotInstalled,
				fmt.Sprintf("backend %q is not installed on node %s", name, nodeID)}
		}
	}
	if entry.IsSystem {
		return &UpgradeRefusal{gallery.UpgradeFailureNoUpdate,
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
		return &UpgradeRefusal{gallery.UpgradeFailureNoUpdate,
			fmt.Sprintf("backend %q has no pending upgrade", name)}
	}
	if expectedTo != "" && expectedTo != info.AvailableVersion && expectedTo != info.AvailableDigest {
		available := info.AvailableVersion
		if available == "" {
			available = info.AvailableDigest
		}
		return &UpgradeRefusal{gallery.UpgradeFailureStale,
			fmt.Sprintf("backend %q: the available build is %q, not the requested %q", name, available, expectedTo)}
	}
	return nil
}

// freshen settles a "nothing pending" answer. When the list is recent it is
// the answer. When it is old or was never computed, and the checker can run a
// check, the check runs and the fresh list decides. decided is false when no
// answer can be given: the check failed or timed out, or nothing says that the
// list was ever computed.
func (g *UpgradeGuard) freshen(ctx context.Context, target string) (info gallery.UpgradeInfo, pending, decided bool) {
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
