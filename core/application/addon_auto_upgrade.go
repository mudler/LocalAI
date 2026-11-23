package application

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/updatenotify"
	"github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/xlog"
)

// runAddonAutoUpgrade runs the add-on upgrade pass on the schedule of the
// backend update check: once after a short delay, then on every interval. The
// setting is read at each pass, so a change in the Settings page takes effect
// without a restart.
func (a *Application) runAddonAutoUpgrade(ctx context.Context, initialDelay, interval time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}
	a.autoUpgradeAddons(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.autoUpgradeAddons(ctx)
		}
	}
}

// autoUpgradeAddons is one pass. With the setting off it does nothing at all:
// no library fetch and no upgrade. With it on, the owner runs the same pinned
// upgrade an administrator starts by hand.
//
// When the upgrade runs are available the pass starts one run with the source
// "auto" for the add-ons it would upgrade now; the run's own rows in the inbox
// tell the outcome, so the pass writes no summary of its own. Without the run
// service the pass upgrades inline, as it always did.
func (a *Application) autoUpgradeAddons(ctx context.Context) {
	m := a.addonManager
	if m == nil {
		return
	}
	enabled, err := a.addonUpgradePolicy(ctx)
	if err != nil {
		xlog.Error("Skipping add-on auto-upgrade pass: owner policy unavailable", "error", err)
		return
	}
	if !enabled {
		return
	}
	a.autoUpgradeAddonsWith(ctx, m)
}

// autoUpgradeAddonsWith is the pass once the setting is known to be on.
func (a *Application) autoUpgradeAddonsWith(ctx context.Context, m addonAutoUpgrader) {
	if runs := a.autoRunner(); runs.available() {
		var items []upgraderun.StartItem
		for _, c := range m.AutoUpgradePending(ctx) {
			items = append(items, upgraderun.StartItem{Kind: upgraderun.KindAddon, Name: c.AddonID, ExpectedTo: c.CandidateDigest})
		}
		if len(items) == 0 {
			return
		}
		if runs.begin(ctx, "addons", items) != autoRunUnavailable {
			return
		}
	}
	results := m.AutoUpgrade(ctx)
	upgraded := 0
	for _, r := range results {
		if r.Upgraded {
			upgraded++
		}
	}
	xlog.Debug("Add-on auto-upgrade pass finished", "candidates", len(results), "upgraded", upgraded)
	// This pass runs on the add-on owner only, so it is the single runner of the
	// add-on family. A pass that did nothing posts nothing.
	if outcomes := addonOutcomes(results); len(outcomes) > 0 {
		if err := a.updateEmitter().Result(ctx, updatenotify.Result{Family: updatenotify.FamilyAddons, Outcomes: outcomes}); err != nil {
			xlog.Warn("Could not write the result of the add-on update pass to the inbox", "error", err)
		}
	}
}

// addonOutcomes is what the inbox says about the results of a pass: what was
// updated, what failed and what waits for an administrator to approve it. A
// candidate that is skipped because it failed in an earlier pass is not
// reported again. The id of an outcome names the add-on and the candidate, so
// the same candidate that fails or waits again is the same report.
func addonOutcomes(results []addons.AutoUpgradeResult) []updatenotify.Outcome {
	var out []updatenotify.Outcome
	for _, r := range results {
		if r.SkippedAsFailure {
			continue
		}
		target := r.CandidateDigest
		if target == "" {
			target = r.To
		}
		o := updatenotify.Outcome{ID: r.AddonID + "@" + target, Name: r.AddonID, From: r.From, To: r.To}
		switch {
		case r.Upgraded:
			o.Kind = updatenotify.OutcomeUpdated
		case r.HeldForApproval:
			o.Kind = updatenotify.OutcomeHeld
		case r.Err != nil:
			o.Kind, o.Err = updatenotify.OutcomeFailed, r.Err.Error()
		default:
			continue
		}
		out = append(out, o)
	}
	return out
}

func (a *Application) addonUpgradePolicy(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !a.applicationConfig.Addons.Enabled {
		return a.applicationConfig.AutoUpgradeAddons, nil
	}
	snapshot, err := a.readAddonHostPolicy(ctx)
	if err != nil {
		return false, err
	}
	return snapshot.Policy.AutoUpgradeAddons, nil
}
