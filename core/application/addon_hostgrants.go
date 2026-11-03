// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostgrants"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/xlog"
)

// File names of the owner's host grants, beside the host settings.
const (
	addonHostGrantsFile  = "host-grants.json"
	addonHostGrantsAudit = "host-grants-audit.jsonl"
)

// errHostGrantsLocked is what a write answers when the deployment keeps the
// policy file and flags the only source of grants.
var errHostGrantsLocked = errors.New("host grants are locked by the deployment")

// addonHostGrants is the owner's side of the UI host grants: the document, its
// audit file, the deployment floor and the glue to the add-on manager. Only
// the owner has one. A frontend never stores or enforces grants.
type addonHostGrants struct {
	floor  hostgrants.Floor
	locked bool
	// lockedReason is gateway.LockedByDeployment or gateway.LockedNoAuth.
	lockedReason string
	override     string

	store    *hostgrants.Store
	storeErr error
	audit    *hostgrants.Audit

	mu      sync.RWMutex
	manager *addons.Manager

	// refresh coalesces the calls that follow a grant write.
	refreshMu      sync.Mutex
	refreshQueued  bool
	refreshRunning bool
	warned         atomic.Bool
}

var supportedOps = func() map[string]bool {
	m := map[string]bool{}
	for _, op := range hostops.SupportedOperations() {
		m[op] = true
	}
	return m
}()

// hostGrantsFloor builds the floor of the deployment: the grants and deny
// rules of the policy file and flags, read once at startup. Empty prefixes are
// dropped, since an empty prefix would match every operation.
func hostGrantsFloor(grants, deny map[string][]string) hostgrants.Floor {
	clean := func(in map[string][]string) map[string][]string {
		out := map[string][]string{}
		for id, list := range in {
			for _, e := range list {
				if e != "" {
					out[id] = append(out[id], e)
				}
			}
		}
		return out
	}
	return hostgrants.Floor{Grants: clean(grants), Deny: clean(deny)}
}

func (a *Application) hostGrantsFloorFromConfig() hostgrants.Floor {
	if a.applicationConfig == nil {
		return hostgrants.Floor{}
	}
	return hostGrantsFloor(a.applicationConfig.AddonHostOps, a.applicationConfig.AddonHostDeny)
}

// openAddonHostGrants opens the document and the audit file under the owner's
// state directory. Call it on the owner only, after NewManager took the
// storage guard and before the Authority is first used. A missing document is
// the first start: it is not created until the first write. An unreadable one
// does not stop the owner; the Authority then refuses every operation, because
// the deny rules in it cannot be read.
func (a *Application) openAddonHostGrants(root string) {
	cfg := a.applicationConfig
	g := &addonHostGrants{floor: a.hostGrantsFloorFromConfig(), override: cfg.AddonHostBlanket}
	switch {
	case cfg.AddonHostUIGrantsLocked:
		g.locked, g.lockedReason = true, gateway.LockedByDeployment
	case a.AddonOwnershipMode() == auth.AddonModeLocalOwner && !cfg.AddonHostUIGrantsWithoutAuth:
		// Without sign-in every caller that reaches the port is the same
		// administrator, so a UI write proves nothing about who made it.
		g.locked, g.lockedReason = true, gateway.LockedNoAuth
	}
	g.store, g.storeErr = hostgrants.Open(filepath.Join(root, addonHostGrantsFile))
	if g.storeErr == nil {
		var err error
		if g.audit, err = hostgrants.OpenAudit(filepath.Join(root, addonHostGrantsAudit)); err != nil {
			// Without the audit file nothing may be granted: the store is shut
			// so the Authority fails closed, like for an unreadable document.
			_ = g.store.Close()
			g.store, g.storeErr = nil, fmt.Errorf("%w: audit file", hostgrants.ErrUnavailable)
		}
	}
	a.addonGrants = g
	g.logStartup(cfg.AddonHostOpsSource)
	switch {
	case g.lockedReason == gateway.LockedNoAuth:
		xlog.Warn("Addon host grants are locked because sign-in is off: anyone who can reach this port could change them. Set them in the policy file, turn sign-in on, or pass --addon-host-ui-grants-without-auth to allow UI changes knowingly")
	case a.AddonOwnershipMode() == auth.AddonModeLocalOwner && !g.locked:
		xlog.Warn("Addon host grants can be changed from the UI although sign-in is off: anyone who can reach this port can grant host operations, set deny rules and turn the blanket approval on")
	}
	if g.storeErr == nil {
		a.addonGrantStore = g
	}
}

func (g *addonHostGrants) logStartup(source string) {
	if source == "" {
		source = "empty"
	}
	prefixes, denyRules := 0, 0
	for _, l := range g.floor.Grants {
		prefixes += len(l)
	}
	for _, l := range g.floor.Deny {
		denyRules += len(l)
	}
	mode, byDeployment := hostgrants.BlanketOff, false
	if g.override != "" {
		mode, byDeployment = g.override, true
	}
	fields := []any{"floorSource", source, "floorAddons", len(g.floor.Grants), "floorPrefixes", prefixes, "floorDenyRules", denyRules, "uiGrants", map[bool]string{true: "locked", false: "enabled"}[g.locked]}
	if g.storeErr != nil {
		xlog.Warn("Addon host grants store is unreadable: no add-on can call a host operation until it is repaired", "reason", describeStoreError(g.storeErr))
		xlog.Info("Addon host grants", fields...)
		return
	}
	snap, _ := g.store.Snapshot(context.Background())
	ops := 0
	for _, e := range snap.Grants {
		ops += len(e.Ops)
	}
	if !byDeployment {
		mode = snap.Blanket.Mode
	}
	uiDeny := 0
	for _, l := range snap.Deny {
		uiDeny += len(l)
	}
	fields = append(fields, "revision", snap.Revision, "uiGrantAddons", len(snap.Grants), "uiGrantOperations", ops, "uiDenyRules", uiDeny, "blanket", mode, "blanketSetByDeployment", byDeployment)
	xlog.Info("Addon host grants", fields...)
	if g.locked && len(snap.Grants)+uiDeny > 0 {
		xlog.Warn("Addon host grants are locked: the document keeps applying as it is, and no UI write changes it")
	}
}

// describeStoreError gives a reason that names no path.
func describeStoreError(err error) string {
	switch {
	case errors.Is(err, hostgrants.ErrInvalid):
		return "the document is not valid"
	case errors.Is(err, hostgrants.ErrUncertain):
		return "the last write is in doubt"
	case errors.Is(err, hostgrants.ErrUnavailable):
		return "the document cannot be opened or locked"
	}
	return "unreadable"
}

func (a *Application) closeAddonHostGrants() {
	if g := a.addonGrants; g != nil {
		if g.store != nil {
			_ = g.store.Close()
		}
		_ = g.audit.Close()
	}
}

func (g *addonHostGrants) bind(m *addons.Manager) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.manager = m
}

func (g *addonHostGrants) mgr() *addons.Manager {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.manager
}

// context is what the evaluator needs for one add-on. Until the manager is
// bound no identity is known, so no UI entry counts.
func (g *addonHostGrants) context(id string) hostgrants.Context {
	c := hostgrants.Context{Registered: func(op string) bool { return supportedOps[op] }}
	if m := g.mgr(); m != nil {
		c.Identity, c.Completed = m.TrustedIdentity(id), m.OperationCompleted
	} else {
		c.Completed = func(string) bool { return false }
	}
	return c
}

// failClosed wraps the Authority so that a store that cannot be read refuses
// every operation: the UI deny rules in it are unknown, and an empty view would
// let a denied operation through.
type failClosed struct {
	inner hostops.Authority
	g     *addonHostGrants
}

func (f failClosed) broken() bool {
	if f.g.storeErr != nil || (f.g.store != nil && f.g.store.Usable() != nil) {
		if f.g.warned.CompareAndSwap(false, true) {
			xlog.Error("Addon host grants store is unreadable: every host operation is refused")
		}
		return true
	}
	return false
}

func (f failClosed) Allows(id, op string) (bool, hostops.Reason) {
	if id == "" || f.broken() {
		return false, hostops.ReasonNotGranted
	}
	return f.inner.Allows(id, op)
}

func (f failClosed) Snapshot(id string) hostops.EffectiveSet {
	if id == "" || f.broken() {
		return hostops.EffectiveSet{}
	}
	return f.inner.Snapshot(id)
}

func (f failClosed) Entitled(id string) bool {
	return id != "" && !f.broken() && f.inner.Entitled(id)
}

// buildAddonAuthority is the Authority every reader asks. On the owner it reads
// the store on every call; elsewhere it is the floor of the deployment alone.
func (a *Application) buildAddonAuthority() hostops.Authority {
	g := a.addonGrants
	if g == nil {
		return hostgrants.NewAuthority(hostgrants.AuthorityConfig{Floor: a.hostGrantsFloorFromConfig()})
	}
	inner := hostgrants.NewAuthority(hostgrants.AuthorityConfig{Floor: g.floor, Store: g.store, Context: g.context})
	return failClosed{inner: inner, g: g}
}

// AddonHostGrantsStore is the owner's document of UI grants, or nil in a process that has none.
func (a *Application) AddonHostGrantsStore() *hostgrants.Store {
	if a.addonGrants == nil {
		return nil
	}
	return a.addonGrants.store
}

// AddonHostGrantsAfterChange is the hook for every grant write. It asks the
// manager to look again at what each add-on holds, in the background, and never
// blocks. The Authority needs no call: it reads the store on every operation.
func (a *Application) AddonHostGrantsAfterChange(addonIDs []string) {
	if g := a.addonGrants; g != nil {
		g.refreshSoon(a.applicationConfig.Context, addonIDs)
	}
}

// ApplyAddonHostGrants runs the refresh now.
func (a *Application) ApplyAddonHostGrants(ctx context.Context) error {
	if a.addonGrants == nil || a.addonGrants.mgr() == nil {
		return addons.ErrConfiguration
	}
	return a.addonGrants.mgr().HostGrantsChanged(ctx)
}

func (g *addonHostGrants) refreshSoon(ctx context.Context, ids []string) {
	m := g.mgr()
	if m == nil {
		return
	}
	g.refreshMu.Lock()
	g.refreshQueued = true
	if g.refreshRunning {
		g.refreshMu.Unlock()
		return
	}
	g.refreshRunning = true
	g.refreshMu.Unlock()
	go func() {
		for {
			g.refreshMu.Lock()
			if !g.refreshQueued {
				g.refreshRunning = false
				g.refreshMu.Unlock()
				return
			}
			g.refreshQueued = false
			g.refreshMu.Unlock()
			if ctx == nil || ctx.Err() == nil {
				started, stopped, err := m.HostGrantsChangedReport(context.Background())
				if err != nil && !errors.Is(err, addons.ErrRecoveryRequired) {
					xlog.Warn("Could not apply a change of host grants to the add-ons", "addons", ids, "error", err)
				}
				for _, id := range started {
					g.auditAppend(hostgrants.AuditEntry{Event: hostgrants.AuditRestartForGrant, AddonID: id, Reason: "started_after_grant"})
				}
				for _, id := range stopped {
					g.auditAppend(hostgrants.AuditEntry{Event: hostgrants.AuditRestartForGrant, AddonID: id, Reason: "stopped_after_access_removed"})
				}
			}
		}
	}()
}

// --- addons.GrantStore ---

var blanketName = regexp.MustCompile(`^blanket:(official|any)@rev([0-9]+)$`)

func (g *addonHostGrants) now() string { return time.Now().UTC().Format(time.RFC3339) }

// auditAppend records a refusal or a housekeeping removal. A failure is logged
// by the caller's error path only: nothing was granted, so there is nothing to
// fail closed on. Writes that grant go through Store.UpdateAudited instead.
func (g *addonHostGrants) auditAppend(e hostgrants.AuditEntry) {
	if err := g.audit.Append(e); err != nil {
		xlog.Warn("Could not write an addon host grants audit line", "event", e.Event, "error", err)
	}
}

// Stage records pending grants for one lifecycle operation. They are part of
// the entry of the add-on but take effect only when the operation completed.
// An entry that exists keeps working: the new operations carry the ID of the
// operation, the old ones their own.
func (g *addonHostGrants) Stage(ctx context.Context, s addons.GrantStage) error {
	if g.locked {
		g.auditAppend(hostgrants.AuditEntry{Event: hostgrants.AuditRefused, AddonID: s.AddonID, OperationID: s.OperationID, Reason: "locked_by_deployment", By: s.Actor.UserID})
		return errHostGrantsLocked
	}
	source := hostgrants.SourceInstall
	if s.Action == "upgrade" {
		source = hostgrants.SourceUpgrade
	}
	var blanketMode string
	var blanketRev uint64
	if s.Blanket != "" {
		m := blanketName.FindStringSubmatch(s.Blanket)
		if m == nil {
			return fmt.Errorf("record host grants: unknown blanket %q", s.Blanket)
		}
		blanketMode = m[1]
		blanketRev, _ = strconv.ParseUint(m[2], 10, 64)
		// Revision 0 means the switch was never set in the document: the
		// deployment fixed the mode. Those grants stay the install's own, since
		// a blanket grant must name a revision of the document.
		if blanketRev != 0 {
			source = hostgrants.SourceBlanket
		}
	}
	for range 3 {
		snap, err := g.store.Snapshot(ctx)
		if err != nil {
			return err
		}
		prev := snap.Grants[s.AddonID]
		if prev.Identity == s.Identity && stagedBy(prev, s.OperationID) {
			return nil
		}
		if s.ExpectedRevision != 0 && s.ExpectedRevision != snap.Revision {
			g.auditAppend(hostgrants.AuditEntry{Event: hostgrants.AuditRefused, AddonID: s.AddonID, OperationID: s.OperationID, Reason: "revision_conflict", By: s.Actor.UserID})
			return addons.ErrGrantsConflict
		}
		patch := hostgrants.Patch{}
		if s.Blanket != "" {
			// The mode may come from the deployment, which is never written to the
			// document; only the revision the grants were planned against counts.
			if blanketRev != snap.Blanket.Revision {
				return addons.ErrGrantsConflict
			}
		}
		entry := hostgrants.AddonGrants{Identity: s.Identity, Generation: s.Generation, OperationID: s.OperationID}
		have := map[string]bool{}
		fresh := true
		if len(prev.Ops) > 0 && prev.Identity == s.Identity {
			entry.Ops, entry.Generation, entry.OperationID = slices.Clone(prev.Ops), s.Generation, prev.OperationID
			for _, op := range prev.Ops {
				have[op.Name] = true
			}
			fresh = false
		}
		var added []string
		for _, name := range s.Ops {
			if have[name] {
				continue
			}
			op := hostgrants.OpGrant{Name: name, Source: source, At: g.now(), By: s.Actor.UserID, Version: s.Version, BlanketRevision: blanketRev}
			if !fresh {
				op.OperationID = s.OperationID
			}
			entry.Ops = append(entry.Ops, op)
			added = append(added, name)
		}
		if len(added) == 0 {
			return nil
		}
		patch.Addons = map[string]*hostgrants.AddonGrants{s.AddonID: &entry}
		ev := hostgrants.AuditGrant
		tag := ""
		if s.Blanket != "" {
			ev, tag = hostgrants.AuditBlanketGrant, blanketMode+"@rev"+strconv.FormatUint(blanketRev, 10)
		}
		_, err = g.store.UpdateAudited(ctx, patch, snap.Revision, "stage-"+s.OperationID+"-r"+strconv.FormatUint(snap.Revision, 10), func(n hostgrants.Snapshot) error {
			return g.audit.Append(hostgrants.AuditEntry{Event: ev, AddonID: s.AddonID, Ops: added, Source: string(source), Version: s.Version, Digest: s.Digest,
				OperationID: s.OperationID, RevisionBefore: snap.Revision, RevisionAfter: n.Revision, By: s.Actor.UserID, Credential: s.Actor.Mode, Blanket: tag})
		})
		if errors.Is(err, hostgrants.ErrConflict) && s.ExpectedRevision == 0 {
			continue
		}
		if errors.Is(err, hostgrants.ErrConflict) {
			return addons.ErrGrantsConflict
		}
		if err != nil {
			return err
		}
		return nil
	}
	return addons.ErrGrantsConflict
}

// stagedBy reports whether the entry already holds the operations of the
// lifecycle operation, so a continued operation does not stage twice.
func stagedBy(e hostgrants.AddonGrants, opID string) bool {
	for _, op := range e.Ops {
		if hostgrants.EffectiveOperationID(e, op) == opID {
			return true
		}
	}
	return false
}

// Abort removes what an operation that did not complete staged, and nothing
// else: the operations the add-on held before keep working.
func (g *addonHostGrants) Abort(ctx context.Context, operationID string) error {
	for range 3 {
		snap, err := g.store.Snapshot(ctx)
		if err != nil {
			return err
		}
		patch := hostgrants.Patch{Addons: map[string]*hostgrants.AddonGrants{}}
		var audits []hostgrants.AuditEntry
		for id, e := range snap.Grants {
			var kept []hostgrants.OpGrant
			var dropped []string
			for _, op := range e.Ops {
				if hostgrants.EffectiveOperationID(e, op) == operationID {
					dropped = append(dropped, op.Name)
				} else {
					kept = append(kept, op)
				}
			}
			if len(dropped) == 0 {
				continue
			}
			if len(kept) == 0 {
				patch.Addons[id] = nil
			} else {
				e.Ops = kept
				patch.Addons[id] = &e
			}
			audits = append(audits, hostgrants.AuditEntry{Event: hostgrants.AuditRefused, AddonID: id, Ops: dropped, OperationID: operationID, Reason: "operation_not_completed", RevisionBefore: snap.Revision})
		}
		if len(patch.Addons) == 0 {
			return nil
		}
		// Removing pending grants is not a grant: a failed audit line must not
		// keep a half-staged entry, so this write is not gated on the audit.
		res, err := g.store.Update(ctx, patch, snap.Revision, "abort-"+operationID+"-r"+strconv.FormatUint(snap.Revision, 10))
		if errors.Is(err, hostgrants.ErrConflict) {
			continue
		}
		if err != nil {
			return err
		}
		for _, e := range audits {
			e.RevisionAfter = res.Snapshot.Revision
			g.auditAppend(e)
		}
		return nil
	}
	return hostgrants.ErrConflict
}

// Remove deletes the grants of an uninstalled add-on and, when asked, keeps a
// hint that pre-ticks a later install. A hint grants nothing.
func (g *addonHostGrants) Remove(ctx context.Context, addonID string, keepHint bool) error {
	for range 3 {
		snap, err := g.store.Snapshot(ctx)
		if err != nil {
			return err
		}
		entry, had := snap.Grants[addonID]
		_, hinted := snap.Hints[addonID]
		if !had && !(hinted && !keepHint) {
			return nil
		}
		patch := hostgrants.Patch{Addons: map[string]*hostgrants.AddonGrants{addonID: nil}}
		var names []string
		for _, op := range entry.Ops {
			names = append(names, op.Name)
		}
		if keepHint && len(names) > 0 {
			patch.Hints = map[string][]string{addonID: names}
		} else if hinted {
			patch.Hints = map[string][]string{addonID: nil}
		}
		res, err := g.store.Update(ctx, patch, snap.Revision, "remove-"+addonID+"-r"+strconv.FormatUint(snap.Revision, 10))
		if errors.Is(err, hostgrants.ErrConflict) {
			continue
		}
		if err != nil {
			return err
		}
		if had {
			g.auditAppend(hostgrants.AuditEntry{Event: hostgrants.AuditRevoke, AddonID: addonID, Ops: names, Reason: "uninstalled", RevisionBefore: snap.Revision, RevisionAfter: res.Snapshot.Revision})
		}
		return nil
	}
	return hostgrants.ErrConflict
}

// Reconcile drops the entries that no longer match the journal.
func (g *addonHostGrants) Reconcile(ctx context.Context, installed map[string]string, completed func(string) bool) error {
	removed, err := g.store.Reconcile(ctx, hostgrants.ReconcileInput{
		Installed:          func(id string) (string, bool) { identity, ok := installed[id]; return identity, ok },
		OperationCompleted: completed,
	})
	for _, r := range removed {
		g.auditAppend(hostgrants.AuditEntry{Event: hostgrants.AuditReconcileRemove, AddonID: r.AddonID, Ops: r.Ops, Reason: r.Reason})
	}
	return err
}

// Blanket says what the blanket switch grants to a package, when it is on and
// the package is in its scope.
func (g *addonHostGrants) Blanket(ctx context.Context, q addons.BlanketQuery) ([]string, string) {
	if g.locked || g.store.Usable() != nil {
		return nil, ""
	}
	snap := g.store.View()
	id := q.Receipt.Manifest.ID
	res := g.floor.EvaluateBlanket(snap, g.context(id), g.override, hostgrants.BlanketInput{
		AddonID: id, Official: addons.OfficialReceipt(q.Receipt), Required: q.Required, Optional: q.Optional,
	})
	if !res.Applies || len(res.Plan.Grant) == 0 {
		return nil, ""
	}
	mode, rev := snap.Blanket.Mode, res.Revision
	// A mode the deployment fixes is applied here, at read time, and is never
	// copied into the document: removing the flag must hand the switch back to
	// what a person set.
	if g.override != "" {
		mode = g.override
	}
	return res.Plan.Grant, fmt.Sprintf("blanket:%s@rev%d", mode, rev)
}

// --- gateway.HostAccessManager ---

type hostAccessManager struct{ m *addons.Manager }

func (h hostAccessManager) HostAccess(ctx context.Context) ([]gateway.InstalledAccess, error) {
	list, err := h.m.HostAccessInstalled(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gateway.InstalledAccess, 0, len(list))
	for _, in := range list {
		a := gateway.InstalledAccess{AddonID: in.AddonID, Title: in.Title, Version: in.Version, State: in.State, Identity: in.Identity,
			Generation: in.Generation, OperationID: in.OperationID, Official: in.Official, Required: in.Required, Optional: in.Optional,
			SourceID: in.SourceID, NewOptional: in.NewOptional}
		if in.HeldVersion != "" {
			a.PendingUpgrade = &gateway.PendingUpgrade{Version: in.HeldVersion, Delta: in.HeldDelta}
		}
		out = append(out, a)
	}
	return out, nil
}

func (h hostAccessManager) PreviewHostAccess(ctx context.Context, sourceID, addonID, version string) (gateway.ReleaseAccess, error) {
	r, err := h.m.PreviewRelease(ctx, sourceID, addonID, version)
	if err != nil {
		return gateway.ReleaseAccess{}, err
	}
	return gateway.ReleaseAccess{AddonID: r.AddonID, Title: r.Title, Version: r.Version, Digest: r.Digest, Official: r.Official,
		Required: r.Required, Optional: r.Optional, Installed: r.Installed}, nil
}

func (h hostAccessManager) SubmitWithApproval(ctx context.Context, actor contract.Actor, cmd addons.LifecycleCommand, ap gateway.HostApproval) (addons.OperationStatus, error) {
	cmd.Approval = &addons.HostApproval{Digest: ap.Digest, Ops: ap.Ops, Revision: ap.Revision, Nonce: ap.Nonce}
	st, err := h.m.Submit(ctx, actor, cmd)
	// The owner service types its refusals by these errors.
	var stale *addons.ApprovalStaleError
	var refusal *addons.HostRequirementsRefusal
	switch {
	case errors.As(err, &stale):
		return st, gateway.ErrApprovalStale
	case errors.As(err, &refusal) && refusal.Blocked():
		return st, gateway.ErrBlockedByPolicy
	case errors.As(err, &refusal):
		return st, gateway.ErrApprovalRequired
	}
	return st, err
}

// wireHostGrantsOwner gives the gateway of the owner the host access routes.
// A frontend has no grants and forwards.
func (a *Application) wireHostGrantsOwner(g *gateway.Gateway) error {
	hg := a.addonGrants
	if hg == nil || hg.store == nil || a.addonManager == nil {
		return nil
	}
	owner, err := gateway.NewHostGrantsOwner(gateway.HostGrantsConfig{
		Store: hg.store, Floor: hg.floor, Audit: hg.audit, Manager: hostAccessManager{a.addonManager},
		Supported: hostops.SupportedOperations(),
		Describe: func(op string) (gateway.HostOpDescription, bool) {
			title, text, risk, ok := hostops.DescribeOperation(op)
			return gateway.HostOpDescription{Title: title, Text: text, Risk: risk}, ok
		},
		BlanketOverride: hg.override, Locked: hg.locked, LockedReason: hg.lockedReason,
		Completed:   a.addonManager.OperationCompleted,
		Admin:       g.HostSettingsAdmin,
		AfterChange: a.AddonHostGrantsAfterChange,
	})
	if err != nil {
		return err
	}
	// The same service takes the zip files of an install from a file.
	g.HostGrantsOwner = owner.WithFiles(a.addonManager)
	return nil
}
