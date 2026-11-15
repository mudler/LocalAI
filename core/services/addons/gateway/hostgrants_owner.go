// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/hostgrants"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/zipformat"
	"github.com/mudler/LocalAI/pkg/randtoken"
	"github.com/mudler/LocalAI/pkg/xslices"
)

// nonceLife is how long a preview nonce stays valid.
const nonceLife = 5 * time.Minute

const maxNonces = 1024

// HostOpDescription is the plain text of one operation.
type HostOpDescription struct{ Title, Text, Risk string }

// HostGrantsConfig builds the owner service.
type HostGrantsConfig struct {
	Store *hostgrants.Store
	// Floor is the startup policy of the file and flags.
	Floor   hostgrants.Floor
	Audit   *hostgrants.Audit
	Manager HostAccessManager
	// Supported is the host registry vocabulary. Nil means
	// hostops.SupportedOperations().
	Supported []string
	// Describe gives the text of an operation. Nil, or a miss, shows the name.
	Describe func(op string) (HostOpDescription, bool)
	// BlanketOverride is the mode a deployment fixes ("" lets the UI decide).
	BlanketOverride string
	// Locked refuses every write: the deployment keeps the file the only source.
	Locked bool
	// LockedReason says why: LockedByDeployment or LockedNoAuth. It selects the
	// text of the refusal and is shown by the page.
	LockedReason string
	// Completed reports whether a lifecycle operation completed. Nil counts
	// every entry as completed.
	Completed func(operationID string) bool
	// Admin checks the current host authority of the actor. Nil refuses.
	Admin func(context.Context, contract.Actor) error
	// AfterChange runs after a write changed what add-ons may call, so the
	// application can start an add-on that now holds an operation but has no
	// host endpoint. Optional; it must not block.
	AfterChange func(addonIDs []string)
	Now         func() time.Time
}

// HostGrantsOwner is the owner's service for the host access routes. It holds
// the nonces of the previews, applies the rules of the spec and writes the
// grants document. It exists on the owner only.
type HostGrantsOwner struct {
	cfg    HostGrantsConfig
	nonces nonceStore
	files  FileService
}

// NewHostGrantsOwner returns the owner service. A store and a manager are required.
func NewHostGrantsOwner(cfg HostGrantsConfig) (*HostGrantsOwner, error) {
	if cfg.Store == nil || cfg.Manager == nil {
		return nil, ErrUnavailable
	}
	if cfg.Supported == nil {
		cfg.Supported = hostops.SupportedOperations()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &HostGrantsOwner{cfg: cfg, nonces: nonceStore{now: cfg.Now, m: map[string]*nonceRecord{}}}, nil
}

type nonceRecord struct {
	bind    string
	expires time.Time
	usedBy  string
}

type nonceStore struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]*nonceRecord
}

func (s *nonceStore) purge() {
	now := s.now()
	for k, r := range s.m {
		if !r.expires.After(now) {
			delete(s.m, k)
		}
	}
}

func (s *nonceStore) issue(bind string) (string, time.Time, error) {
	n, err := randtoken.Hex(16)
	if err != nil {
		return "", time.Time{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge()
	for len(s.m) >= maxNonces {
		var oldest string
		for k, r := range s.m {
			if oldest == "" || r.expires.Before(s.m[oldest].expires) {
				oldest = k
			}
		}
		delete(s.m, oldest)
	}
	exp := s.now().Add(nonceLife)
	s.m[n] = &nonceRecord{bind: bind, expires: exp}
	return n, exp, nil
}

// consume uses the nonce once for the operation. The same operation may
// present it again (a replay of the same request).
func (s *nonceStore) consume(nonce, bind, opID string) (replay, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge()
	r, found := s.m[nonce]
	if !found || r.bind != bind {
		return false, false
	}
	if r.usedBy != "" {
		return true, r.usedBy == opID
	}
	r.usedBy = opID
	return false, true
}

func nonceBind(a contract.Actor, addonID, digest string, revision uint64) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d", a.UserID, a.CredentialID, addonID, digest, revision)
}

// Serve runs one call for an actor the verifier or the HTTP layer
// authenticated. Typed refusals are *HostGrantsError.
func (o *HostGrantsOwner) Serve(ctx context.Context, a contract.Actor, call HostGrantsCall) (json.RawMessage, error) {
	if !call.valid() {
		return nil, ErrForbidden
	}
	if o.cfg.Admin == nil {
		return nil, ErrForbidden
	}
	// A staged local file is read for the user that uploaded it only.
	ctx = addons.WithFileOwner(ctx, a.UserID)
	if e := ClassifyCheck(o.cfg.Admin(ctx, a)); e != nil {
		return nil, e
	}
	if call.write() {
		if !HumanSurfaceOf(ctx) {
			o.refused(a, call, "not_human_surface")
			return nil, hgErr(403, HostGrantsNotHumanSurface, "This action requires your own browser session")
		}
		if o.cfg.Locked {
			o.refused(a, call, "locked_by_deployment")
			return nil, hgErr(403, HostGrantsLocked, o.lockedText())
		}
	}
	var out any
	var err error
	switch call.Op {
	case HostGrantsRead:
		out, err = o.read(ctx)
	case HostGrantsPreview:
		var r HostGrantsPreviewRequest
		if !strictBody(call.Body, &r) {
			return nil, hgErr(400, HostGrantsInvalidRequest, "The request cannot be carried out as given")
		}
		out, err = o.preview(ctx, a, r)
	case HostGrantsInstall:
		var r HostGrantsInstallRequest
		if !strictBody(call.Body, &r) || r.OperationID != call.OperationID {
			return nil, hgErr(400, HostGrantsInvalidRequest, "The request cannot be carried out as given")
		}
		out, err = o.install(ctx, a, r)
	case HostGrantsEdit:
		var r HostGrantsEditRequest
		if !strictBody(call.Body, &r) || r.OperationID != call.OperationID {
			return nil, hgErr(400, HostGrantsInvalidRequest, "The request cannot be carried out as given")
		}
		out, err = o.edit(ctx, a, call.AddonID, r)
	case HostGrantsRevoke:
		var r HostGrantsRevokeRequest
		if !strictBody(call.Body, &r) || r.OperationID != call.OperationID {
			return nil, hgErr(400, HostGrantsInvalidRequest, "The request cannot be carried out as given")
		}
		out, err = o.revoke(ctx, a, call.AddonID, r)
	case HostGrantsBlanket:
		var r HostGrantsBlanketRequest
		if !strictBody(call.Body, &r) || r.OperationID != call.OperationID {
			return nil, hgErr(400, HostGrantsInvalidRequest, "The request cannot be carried out as given")
		}
		out, err = o.blanket(ctx, a, r)
	case HostGrantsDeny, HostGrantsDenyPreview:
		var r HostGrantsDenyRequest
		if !strictBody(call.Body, &r) || r.OperationID != call.OperationID {
			return nil, hgErr(400, HostGrantsInvalidRequest, "The request cannot be carried out as given")
		}
		r.DryRun = r.DryRun || call.Op == HostGrantsDenyPreview
		if r.DryRun != (call.Op == HostGrantsDenyPreview) {
			return nil, hgErr(400, HostGrantsInvalidRequest, "The request cannot be carried out as given")
		}
		out, err = o.deny(ctx, a, r)
	case HostGrantsFileBegin, HostGrantsFileChunk, HostGrantsFileFinish, HostGrantsFileAbort, HostGrantsFileDrop, HostGrantsFileDescribe:
		out, err = o.fileCall(ctx, a, call)
	default:
		return nil, ErrForbidden
	}
	if err != nil {
		if call.write() {
			var he *HostGrantsError
			if errors.As(err, &he) {
				o.refused(a, call, he.Type)
			}
		}
		return nil, o.mapError(err)
	}
	raw, e := json.Marshal(out)
	if e != nil {
		return nil, ErrUnavailable
	}
	return raw, nil
}

func (o *HostGrantsOwner) refused(a contract.Actor, call HostGrantsCall, reason string) {
	_ = o.cfg.Audit.Append(hostgrants.AuditEntry{Event: hostgrants.AuditRefused, AddonID: call.AddonID, OperationID: call.OperationID, By: a.UserID, Credential: credentialKind(a), Reason: reason})
}

func credentialKind(a contract.Actor) string {
	switch {
	case strings.HasPrefix(a.CredentialID, "session:"):
		return "session"
	case strings.HasPrefix(a.CredentialID, "key:"):
		return "key"
	}
	return "other"
}

// mapError turns the errors of the store and the manager into typed answers.
func (o *HostGrantsOwner) mapError(err error) error {
	var he *HostGrantsError
	switch {
	case errors.As(err, &he):
		return he
	case errors.Is(err, hostgrants.ErrConflict):
		return hgErr(409, HostGrantsRevisionConflict, "Host access changed since you loaded it; reload and repeat the action")
	case errors.Is(err, hostgrants.ErrOperationMismatch):
		return hgErr(409, HostGrantsOperationConflict, "The operation ID was used for another request; nothing was applied. Repeat the change under a new operation ID")
	case errors.Is(err, hostgrants.ErrInvalid):
		return hgErr(400, HostGrantsInvalidRequest, "The request cannot be carried out as given")
	case errors.Is(err, ErrApprovalStale):
		return hgErr(409, HostGrantsApprovalStale, "The package changed since you reviewed it; review it again")
	case errors.Is(err, ErrBlockedByPolicy):
		return hgErr(403, HostGrantsBlockedByPolicy, "An operation this add-on needs is blocked by the administrator policy")
	case errors.Is(err, ErrApprovalRequired):
		return hgErr(403, HostGrantsApprovalRequired, "Approve the host access this add-on needs")
	case errors.Is(err, hostgrants.ErrUnavailable), errors.Is(err, hostgrants.ErrUncertain):
		return ErrUnavailable
	}
	return err
}

func (o *HostGrantsOwner) notFound() error {
	return hgErr(404, HostGrantsNotFound, "No such add-on")
}

func (o *HostGrantsOwner) invalid() error {
	return hgErr(400, HostGrantsInvalidRequest, "The request cannot be carried out as given")
}

func (o *HostGrantsOwner) registered(op string) bool { return slices.Contains(o.cfg.Supported, op) }

func (o *HostGrantsOwner) context(identity string) hostgrants.Context {
	return hostgrants.Context{Identity: identity, Completed: o.cfg.Completed, Registered: o.registered}
}

func (o *HostGrantsOwner) describe(v *HostOpView) {
	if o.cfg.Describe != nil {
		if d, ok := o.cfg.Describe(v.Name); ok {
			v.Title, v.Text, v.Risk = d.Title, d.Text, d.Risk
			return
		}
	}
	v.Title = v.Name
}

func (o *HostGrantsOwner) blanketMode(doc hostgrants.Snapshot) string {
	if o.cfg.BlanketOverride != "" {
		return o.cfg.BlanketOverride
	}
	return doc.Blanket.Mode
}

func unique(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, s := range l {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}

func status(d hostgrants.Decision, registered bool) string {
	switch {
	case !registered:
		return "unsupported"
	case d.Allowed:
		return "allowed"
	case d.Reason == hostgrants.ReasonDeniedByFile || d.Reason == hostgrants.ReasonDeniedByAdmin:
		return "blocked"
	}
	return "not_allowed"
}

func (o *HostGrantsOwner) addonView(doc hostgrants.Snapshot, in InstalledAccess) HostGrantsAddon {
	c := o.context(in.Identity)
	declared := unique(in.Required, in.Optional)
	req := map[string]bool{}
	for _, r := range in.Required {
		req[r] = true
	}
	entry := doc.Grants[in.AddonID]
	grant := map[string]hostgrants.OpGrant{}
	for _, g := range entry.Ops {
		grant[g.Name] = g
	}
	v := HostGrantsAddon{ID: in.AddonID, Title: in.Title, Version: in.Version, State: in.State, PendingUpgrade: in.PendingUpgrade, SourceID: in.SourceID, NewOptional: xslices.NonNil(sorted(in.NewOptional)),
		Requested: HostRequested{Required: xslices.NonNil(sorted(in.Required)), Optional: xslices.NonNil(sorted(in.Optional))},
		Ops:       []HostOpView{}, NoLongerRequested: []HostOpView{}}
	view := func(op string) HostOpView {
		d := o.cfg.Floor.Decide(doc, c, in.AddonID, op)
		ov := HostOpView{Name: op, Status: status(d, o.registered(op)), Reason: string(d.Reason), Required: req[op]}
		if d.Allowed {
			ov.Source = string(d.Source)
		}
		if g, ok := grant[op]; ok && (d.Allowed || d.Reason == hostgrants.ReasonIdentityMismatch) {
			ov.At, ov.By = g.At, g.By
			if d.Reason == hostgrants.ReasonIdentityMismatch {
				v.IdentityMismatch = true
			}
		}
		o.describe(&ov)
		return ov
	}
	var lacking []string
	for _, op := range declared {
		ov := view(op)
		v.Ops = append(v.Ops, ov)
		if ov.Required && ov.Status != "allowed" {
			lacking = append(lacking, op)
		}
	}
	v.Stopped = strings.Join(lacking, ", ")
	for _, g := range entry.Ops {
		if g.Source == hostgrants.SourceBlanket {
			v.BlanketGrantsCount++
		}
		if !slices.Contains(declared, g.Name) {
			v.NoLongerRequested = append(v.NoLongerRequested, view(g.Name))
		}
	}
	sort.Slice(v.NoLongerRequested, func(i, j int) bool { return v.NoLongerRequested[i].Name < v.NoLongerRequested[j].Name })
	return v
}

func sorted(in []string) []string {
	out := slices.Clone(in)
	sort.Strings(out)
	return out
}

func (o *HostGrantsOwner) read(ctx context.Context) (HostGrantsView, error) {
	doc, err := o.cfg.Store.Snapshot(ctx)
	if err != nil {
		return HostGrantsView{}, err
	}
	installed, err := o.cfg.Manager.HostAccess(ctx)
	if err != nil {
		return HostGrantsView{}, err
	}
	v := HostGrantsView{Revision: doc.Revision, Locked: o.cfg.Locked, LockedReason: o.lockedReason(),
		Blanket: HostBlanketView{Mode: o.blanketMode(doc), IncludeOptional: doc.Blanket.IncludeOptional, SetBy: "ui", Revision: doc.Blanket.Revision},
		Floor:   HostGrantsFloor{Source: string(hostgrants.SourceDeployment), Addons: []HostFloorAddon{}},
		Deny:    HostGrantsDenyView{File: nonNilMap(o.cfg.Floor.Deny), UI: nonNilMap(doc.Deny)},
		Addons:  []HostGrantsAddon{}}
	if o.cfg.BlanketOverride != "" {
		v.Blanket.SetBy = "deployment"
	}
	ids := make([]string, 0, len(o.cfg.Floor.Grants))
	for id := range o.cfg.Floor.Grants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		v.Floor.Addons = append(v.Floor.Addons, HostFloorAddon{ID: id, Prefixes: sorted(o.cfg.Floor.Grants[id])})
	}
	sort.Slice(installed, func(i, j int) bool { return installed[i].AddonID < installed[j].AddonID })
	for _, in := range installed {
		v.Addons = append(v.Addons, o.addonView(doc, in))
	}
	return v, nil
}

func nonNilMap(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = sorted(v)
	}
	return out
}

func (o *HostGrantsOwner) preview(ctx context.Context, a contract.Actor, r HostGrantsPreviewRequest) (HostGrantsPreviewView, error) {
	if !contract.ValidAddonID(r.AddonID) || r.SourceID == "" || len(r.SourceID) > 64 || len(r.Version) > 128 {
		return HostGrantsPreviewView{}, o.invalid()
	}
	doc, err := o.cfg.Store.Snapshot(ctx)
	if err != nil {
		return HostGrantsPreviewView{}, err
	}
	rel, err := o.cfg.Manager.PreviewHostAccess(ctx, r.SourceID, r.AddonID, r.Version)
	if err != nil {
		return HostGrantsPreviewView{}, err
	}
	// The verified manifest of the staged package is the only truth. For an
	// upgrade the identity of the installed package says what is held already;
	// a package that is not installed yet holds nothing from the UI.
	var installed InstalledAccess
	identity := ""
	if rel.Installed {
		list, lerr := o.cfg.Manager.HostAccess(ctx)
		if lerr != nil {
			return HostGrantsPreviewView{}, lerr
		}
		for _, in := range list {
			if in.AddonID == rel.AddonID {
				installed, identity = in, in.Identity
			}
		}
	}
	c := o.context(identity)
	declared := unique(rel.Required, rel.Optional)
	plan := o.cfg.Floor.Plan(doc, c, hostgrants.PlanInput{AddonID: rel.AddonID, Required: rel.Required, Optional: rel.Optional, Wanted: declared})
	req := map[string]bool{}
	for _, x := range rel.Required {
		req[x] = true
	}
	hint := doc.Hints[rel.AddonID]
	v := HostGrantsPreviewView{AddonID: rel.AddonID, Title: rel.Title, Digest: rel.Digest, Version: rel.Version, Installed: rel.Installed,
		Official: rel.Official, Revision: doc.Revision, Ops: []HostOpView{}, BlockedReq: xslices.NonNil(plan.BlockedRequired), Added: []string{},
		Requested: HostRequested{Required: xslices.NonNil(sorted(rel.Required)), Optional: xslices.NonNil(sorted(rel.Optional))}}
	v.Locked, v.LockedReason = o.cfg.Locked, o.lockedReason()
	before := unique(installed.Required, installed.Optional)
	for _, op := range declared {
		ov := HostOpView{Name: op, Required: req[op], Hint: slices.Contains(hint, op)}
		switch {
		case slices.Contains(plan.Denied, op):
			_, why := o.cfg.Floor.Denied(doc, rel.AddonID, op)
			ov.Status, ov.Reason = "blocked", string(why)
		case slices.Contains(plan.Unsupported, op):
			ov.Status = "unsupported"
		case slices.Contains(plan.Held, op):
			ov.Status = "allowed"
			ov.Source = string(o.cfg.Floor.Decide(doc, c, rel.AddonID, op).Source)
		default:
			ov.Status = "not_allowed"
		}
		o.describe(&ov)
		v.Ops = append(v.Ops, ov)
		if rel.Installed && !slices.Contains(before, op) {
			v.Added = append(v.Added, op)
		}
	}
	v.CanInstall = onlyGrantableMissing(plan, rel.Required)
	b := o.cfg.Floor.EvaluateBlanket(doc, c, o.cfg.BlanketOverride, hostgrants.BlanketInput{AddonID: rel.AddonID, Official: rel.Official, Required: rel.Required, Optional: rel.Optional})
	v.Blanket = HostBlanketPreview{Applies: b.Applies && len(b.Plan.BlockedRequired) == 0, Revision: b.Revision}
	nonce, exp, err := o.nonces.issue(nonceBind(a, rel.AddonID, rel.Digest, doc.Revision))
	if err != nil {
		return HostGrantsPreviewView{}, err
	}
	v.Nonce, v.NonceExpiry = nonce, exp.UTC().Format(time.RFC3339)
	return v, nil
}

// onlyGrantableMissing is true when every required operation that is not held
// is one the administrator can grant: nothing blocked and nothing unsupported.
func onlyGrantableMissing(p hostgrants.Plan, required []string) bool {
	if len(p.BlockedRequired) != 0 {
		return false
	}
	for _, r := range required {
		if slices.Contains(p.Unsupported, r) {
			return false
		}
	}
	return true
}

func validDigest(s string) bool {
	return s != "" && len(s) <= 128 && !strings.ContainsAny(s, " \t\r\n")
}

func (o *HostGrantsOwner) install(ctx context.Context, a contract.Actor, r HostGrantsInstallRequest) (any, error) {
	if !contract.ValidAddonID(r.AddonID) || r.SourceID == "" || len(r.SourceID) > 64 || len(r.Version) > 128 ||
		(r.Action != "install" && r.Action != "upgrade") || !validDigest(r.Approve.Digest) || r.Approve.Nonce == "" || len(r.Approve.Nonce) > 128 ||
		len(r.Approve.Ops) > hostgrants.MaxOpsPerAddon || (r.ExpectedDigest == "") != (r.CandidateDigest == "") ||
		(r.Action == "install" && r.ExpectedDigest != "") {
		return nil, o.invalid()
	}
	for _, op := range r.Approve.Ops {
		if !hostgrants.ValidDenyEntry(op) || strings.HasSuffix(op, ".") || op == "*" {
			return nil, o.invalid()
		}
	}
	doc, err := o.cfg.Store.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	bind := nonceBind(a, r.AddonID, r.Approve.Digest, r.Approve.Revision)
	replay, ok := o.nonces.consume(r.Approve.Nonce, bind, r.OperationID)
	switch {
	case replay && ok:
		// The same operation again: the manager answers from its record.
	case replay:
		return nil, hgErr(409, HostGrantsApprovalStale, "The approval was used for another operation; review the package again")
	case r.Approve.Revision != doc.Revision:
		// The nonce is used up only when it matched; a stale revision leaves
		// the dialog to reload.
		return nil, hgErr(409, HostGrantsRevisionConflict, "Host access changed since you loaded it; reload and repeat the action")
	case !ok:
		return nil, hgErr(409, HostGrantsApprovalStale, "The approval expired or does not match; review the package again")
	}
	cmd := addons.LifecycleCommand{OperationID: r.OperationID, AddonID: r.AddonID, Action: r.Action, SourceID: r.SourceID, Version: r.Version,
		ExpectedDigest: r.ExpectedDigest, CandidateDigest: r.CandidateDigest, Downgrade: r.Downgrade && r.SourceID == zipformat.SourceID}
	st, err := o.cfg.Manager.SubmitWithApproval(ctx, a, cmd, HostApproval{Digest: r.Approve.Digest, Ops: slices.Clone(r.Approve.Ops), Revision: r.Approve.Revision, Nonce: r.Approve.Nonce})
	if err != nil {
		return nil, err
	}
	if o.cfg.AfterChange != nil {
		o.cfg.AfterChange([]string{r.AddonID})
	}
	return st, nil
}

func (o *HostGrantsOwner) find(ctx context.Context, id string) (InstalledAccess, error) {
	if !contract.ValidAddonID(id) {
		return InstalledAccess{}, o.notFound()
	}
	list, err := o.cfg.Manager.HostAccess(ctx)
	if err != nil {
		return InstalledAccess{}, err
	}
	for _, in := range list {
		if in.AddonID == id {
			return in, nil
		}
	}
	return InstalledAccess{}, o.notFound()
}

// lockedReason is the reason the page shows, or "" when writes are open.
func (o *HostGrantsOwner) lockedReason() string {
	if !o.cfg.Locked {
		return ""
	}
	if o.cfg.LockedReason == "" {
		return LockedByDeployment
	}
	return o.cfg.LockedReason
}

func (o *HostGrantsOwner) lockedText() string {
	if o.lockedReason() == LockedNoAuth {
		return LockedNoAuthText
	}
	return "The deployment keeps host access in its policy file; it cannot be changed here"
}

func (o *HostGrantsOwner) now() string { return o.cfg.Now().UTC().Format(time.RFC3339) }

func (o *HostGrantsOwner) edit(ctx context.Context, a contract.Actor, id string, r HostGrantsEditRequest) (any, error) {
	if len(r.Ops) > hostgrants.MaxOpsPerAddon {
		return nil, o.invalid()
	}
	in, err := o.find(ctx, id)
	if err != nil {
		return nil, err
	}
	doc, err := o.cfg.Store.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	entry := doc.Grants[id]
	if entry.Identity != in.Identity {
		entry = hostgrants.AddonGrants{}
	}
	have := map[string]hostgrants.OpGrant{}
	for _, g := range entry.Ops {
		have[g.Name] = g
	}
	declared := unique(in.Required, in.Optional)
	want := map[string]bool{}
	next := hostgrants.AddonGrants{Identity: in.Identity, Generation: in.Generation, OperationID: in.OperationID}
	if len(entry.Ops) != 0 {
		next.Generation, next.OperationID = entry.Generation, entry.OperationID
	}
	var added, removed []string
	for _, op := range r.Ops {
		if want[op] || !hostgrants.ValidDenyEntry(op) || strings.HasSuffix(op, ".") || op == "*" {
			return nil, o.invalid()
		}
		want[op] = true
		if g, ok := have[op]; ok {
			next.Ops = append(next.Ops, g)
			continue
		}
		if !slices.Contains(declared, op) || !o.registered(op) {
			return nil, o.invalid()
		}
		if denied, _ := o.cfg.Floor.Denied(doc, id, op); denied {
			return nil, hgErr(403, HostGrantsBlockedByPolicy, "The administrator policy blocks "+op)
		}
		next.Ops = append(next.Ops, hostgrants.OpGrant{Name: op, Source: hostgrants.SourceEdit, At: o.now(), By: a.UserID, Version: in.Version})
		added = append(added, op)
	}
	for _, g := range entry.Ops {
		if !want[g.Name] {
			removed = append(removed, g.Name)
		}
	}
	if len(r.Keep) != 0 {
		// Relabel only the blanket grants the caller named and still keeps.
		keep := map[string]bool{}
		for _, k := range r.Keep {
			keep[k] = true
		}
		for i, g := range next.Ops {
			if keep[g.Name] && g.Source == hostgrants.SourceBlanket {
				next.Ops[i] = hostgrants.OpGrant{Name: g.Name, Source: hostgrants.SourceEdit, At: o.now(), By: a.UserID, Version: g.Version}
			}
		}
	}
	var patch hostgrants.Patch
	if len(next.Ops) == 0 {
		patch.Addons = map[string]*hostgrants.AddonGrants{id: nil}
	} else {
		patch.Addons = map[string]*hostgrants.AddonGrants{id: &next}
	}
	res, err := o.cfg.Store.UpdateAudited(ctx, patch, r.Revision, r.OperationID, func(n hostgrants.Snapshot) error {
		return o.auditEdit(a, id, in.Version, r.OperationID, r.Revision, n.Revision, added, removed)
	})
	if err != nil {
		return nil, err
	}
	// A removal can stop an add-on that now lacks a required operation, and an
	// addition can start one that was refused, so both ask the manager to look.
	if !res.Replayed {
		o.changed(id)
	}
	view := o.addonView(res.Snapshot, in)
	return HostGrantsAddonResult{Revision: res.Snapshot.Revision, Addon: &view}, nil
}

// changed asks the manager to look again at what the add-ons hold. It does not
// block.
func (o *HostGrantsOwner) changed(ids ...string) {
	if o.cfg.AfterChange != nil && len(ids) != 0 {
		o.cfg.AfterChange(ids)
	}
}

func (o *HostGrantsOwner) auditEdit(a contract.Actor, id, version, opID string, before, after uint64, added, removed []string) error {
	var errs []error
	base := hostgrants.AuditEntry{AddonID: id, Version: version, OperationID: opID, RevisionBefore: before, RevisionAfter: after, By: a.UserID, Credential: credentialKind(a)}
	if len(added) != 0 {
		e := base
		e.Event, e.Ops, e.Source = hostgrants.AuditGrant, added, string(hostgrants.SourceEdit)
		errs = append(errs, o.cfg.Audit.Append(e))
	}
	if len(removed) != 0 {
		e := base
		e.Event, e.Ops = hostgrants.AuditRevoke, removed
		errs = append(errs, o.cfg.Audit.Append(e))
	}
	return errors.Join(errs...)
}

func (o *HostGrantsOwner) revoke(ctx context.Context, a contract.Actor, id string, r HostGrantsRevokeRequest) (any, error) {
	if !contract.ValidAddonID(id) {
		return nil, o.invalid()
	}
	doc, err := o.cfg.Store.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	entry, ok := doc.Grants[id]
	if !ok {
		// Nothing to remove. An unknown add-on is a 404; a known one without
		// an entry is already as the caller wants it.
		if _, err = o.find(ctx, id); err != nil {
			return nil, err
		}
		if r.Revision != doc.Revision {
			return nil, hostgrants.ErrConflict
		}
		return HostGrantsRevision{Revision: doc.Revision}, nil
	}
	names := make([]string, 0, len(entry.Ops))
	for _, g := range entry.Ops {
		names = append(names, g.Name)
	}
	res, err := o.cfg.Store.UpdateAudited(ctx, hostgrants.Patch{Addons: map[string]*hostgrants.AddonGrants{id: nil}}, r.Revision, r.OperationID, func(n hostgrants.Snapshot) error {
		return o.auditEdit(a, id, "", r.OperationID, r.Revision, n.Revision, nil, names)
	})
	if err != nil {
		return nil, err
	}
	if !res.Replayed {
		o.changed(id)
	}
	return HostGrantsRevision{Revision: res.Snapshot.Revision, Changed: true}, nil
}

func (o *HostGrantsOwner) blanket(ctx context.Context, a contract.Actor, r HostGrantsBlanketRequest) (any, error) {
	if o.cfg.BlanketOverride != "" {
		return nil, hgErr(403, HostGrantsLocked, "The blanket approval is set by the deployment")
	}
	switch r.Mode {
	case hostgrants.BlanketOff, hostgrants.BlanketOfficial, hostgrants.BlanketAny:
	default:
		return nil, o.invalid()
	}
	if r.Mode == hostgrants.BlanketOff {
		// Off has no scope to widen: the options are ignored, not refused.
		r.IncludeOptional, r.ApplyToInstalled = false, false
	}
	if r.Mode != hostgrants.BlanketOff && r.RevokeOnOff {
		return nil, o.invalid()
	}
	doc, err := o.cfg.Store.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	cur := doc.Blanket
	changes := r.Mode != cur.Mode || r.IncludeOptional != cur.IncludeOptional
	if r.Mode != hostgrants.BlanketOff && (changes || r.ApplyToInstalled) {
		want := BlanketConfirmOfficial
		if r.Mode == hostgrants.BlanketAny {
			want = BlanketConfirmAny
		}
		if strings.TrimSpace(r.Confirm) != want {
			return nil, hgErr(400, HostGrantsConfirmation, "The typed confirmation does not match")
		}
	}
	rev := cur.Revision
	if changes {
		rev++
	}
	next := hostgrants.Blanket{Mode: r.Mode, IncludeOptional: r.IncludeOptional, Revision: rev, ChangedAt: o.now(), ChangedBy: a.UserID}
	patch := hostgrants.Patch{Blanket: &next, RevokeBlanket: r.Mode == hostgrants.BlanketOff && r.RevokeOnOff}
	out := HostGrantsBlanketResult{Changed: []HostBlanketChange{}, Skipped: []HostBlanketSkip{}, Revoked: []string{}}
	if patch.RevokeBlanket {
		for id, g := range doc.Grants {
			if slices.ContainsFunc(g.Ops, func(x hostgrants.OpGrant) bool { return x.Source == hostgrants.SourceBlanket }) {
				out.Revoked = append(out.Revoked, id)
			}
		}
		sort.Strings(out.Revoked)
	}
	var grantedFor []string
	if r.ApplyToInstalled && r.Mode != hostgrants.BlanketOff {
		installed, err := o.cfg.Manager.HostAccess(ctx)
		if err != nil {
			return nil, err
		}
		sim := doc.Clone()
		sim.Blanket = next
		patch.Addons = map[string]*hostgrants.AddonGrants{}
		for _, in := range installed {
			res := o.cfg.Floor.EvaluateBlanket(sim, o.context(in.Identity), "", hostgrants.BlanketInput{AddonID: in.AddonID, Official: in.Official, Required: in.Required, Optional: in.Optional})
			switch {
			case !res.Applies:
				out.Skipped = append(out.Skipped, HostBlanketSkip{ID: in.AddonID, Reason: "out_of_scope"})
			case len(res.Plan.BlockedRequired) != 0:
				out.Skipped = append(out.Skipped, HostBlanketSkip{ID: in.AddonID, Reason: "blocked_by_policy"})
			case len(res.Plan.Grant) != 0:
				add := make([]hostgrants.OpGrant, 0, len(res.Plan.Grant))
				for _, op := range res.Plan.Grant {
					add = append(add, hostgrants.OpGrant{Name: op, Source: hostgrants.SourceBlanket, BlanketRevision: rev, At: o.now(), By: a.UserID, Version: in.Version})
				}
				merged := hostgrants.MergeGrants(doc.Grants[in.AddonID], in.Identity, in.Generation, in.OperationID, add)
				if e := doc.Grants[in.AddonID]; e.Identity == in.Identity && len(e.Ops) != 0 {
					merged.Generation, merged.OperationID = e.Generation, e.OperationID
				}
				patch.Addons[in.AddonID] = &merged
				out.Changed = append(out.Changed, HostBlanketChange{ID: in.AddonID, Ops: sorted(res.Plan.Grant)})
				grantedFor = append(grantedFor, in.AddonID)
			}
		}
	}
	tag := fmt.Sprintf("%s@rev%d", next.Mode, next.Revision)
	res, err := o.cfg.Store.UpdateAudited(ctx, patch, r.Revision, r.OperationID, func(n hostgrants.Snapshot) error {
		ev := hostgrants.AuditBlanketOn
		reason := ""
		if next.Mode == hostgrants.BlanketOff {
			ev = hostgrants.AuditBlanketOff
			if patch.RevokeBlanket {
				reason = "revoked_blanket_grants"
			} else {
				reason = "kept_blanket_grants"
			}
		}
		errs := []error{o.cfg.Audit.Append(hostgrants.AuditEntry{Event: ev, Blanket: tag, OperationID: r.OperationID, RevisionBefore: r.Revision, RevisionAfter: n.Revision, By: a.UserID, Credential: credentialKind(a), Reason: reason})}
		for _, c := range out.Changed {
			errs = append(errs, o.cfg.Audit.Append(hostgrants.AuditEntry{Event: hostgrants.AuditBlanketGrant, AddonID: c.ID, Ops: c.Ops, Source: string(hostgrants.SourceBlanket), Blanket: tag, OperationID: r.OperationID, RevisionAfter: n.Revision, By: a.UserID}))
		}
		return errors.Join(errs...)
	})
	if err != nil {
		return nil, err
	}
	out.Revision = res.Snapshot.Revision
	out.Blanket = HostBlanketView{Mode: res.Snapshot.Blanket.Mode, IncludeOptional: res.Snapshot.Blanket.IncludeOptional, SetBy: "ui", Revision: res.Snapshot.Blanket.Revision}
	if !res.Replayed {
		// Grants given to installed add-ons can start them; revoked blanket
		// grants can stop them.
		o.changed(append(slices.Clone(grantedFor), out.Revoked...)...)
	}
	return out, nil
}

func (o *HostGrantsOwner) deny(ctx context.Context, a contract.Actor, r HostGrantsDenyRequest) (any, error) {
	if r.Deny == nil {
		r.Deny = map[string][]string{}
	}
	if hostgrants.ValidateDeny(r.Deny) != nil {
		return nil, o.invalid()
	}
	doc, err := o.cfg.Store.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	installed, err := o.cfg.Manager.HostAccess(ctx)
	if err != nil {
		return nil, err
	}
	sim := doc.Clone()
	sim.Deny = r.Deny
	affects := []HostDenyAffect{}
	for _, in := range installed {
		c := o.context(in.Identity)
		var loses []string
		stop := false
		for _, op := range unique(in.Required, in.Optional, opsOf(doc.Grants[in.AddonID])) {
			before := o.cfg.Floor.Decide(doc, c, in.AddonID, op)
			after := o.cfg.Floor.Decide(sim, c, in.AddonID, op)
			if before.Allowed && !after.Allowed {
				loses = append(loses, op)
				stop = stop || slices.Contains(in.Required, op)
			}
		}
		if len(loses) != 0 {
			affects = append(affects, HostDenyAffect{ID: in.AddonID, Loses: loses, WillStop: stop})
		}
	}
	sort.Slice(affects, func(i, j int) bool { return affects[i].ID < affects[j].ID })
	if r.DryRun {
		if r.Revision != doc.Revision {
			return nil, hostgrants.ErrConflict
		}
		return HostGrantsDenyResult{Revision: doc.Revision, Deny: nonNilMap(r.Deny), Affects: affects, DryRun: true}, nil
	}
	deny := r.Deny
	res, err := o.cfg.Store.UpdateAudited(ctx, hostgrants.Patch{Deny: &deny}, r.Revision, r.OperationID, func(n hostgrants.Snapshot) error {
		return o.auditDeny(a, r, doc.Deny, n.Revision)
	})
	if err != nil {
		return nil, err
	}
	if !res.Replayed {
		// A new deny stops an add-on that loses a required operation; a removed
		// one lets an add-on that was refused start.
		o.changed(denyKeys(doc.Deny, r.Deny)...)
	}
	return HostGrantsDenyResult{Revision: res.Snapshot.Revision, Deny: nonNilMap(res.Snapshot.Deny), Affects: affects}, nil
}

func opsOf(g hostgrants.AddonGrants) []string {
	out := make([]string, 0, len(g.Ops))
	for _, x := range g.Ops {
		out = append(out, x.Name)
	}
	return out
}

func denyKeys(lists ...map[string][]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for k := range l {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (o *HostGrantsOwner) auditDeny(a contract.Actor, r HostGrantsDenyRequest, old map[string][]string, after uint64) error {
	var errs []error
	keys := map[string]bool{}
	for k := range old {
		keys[k] = true
	}
	for k := range r.Deny {
		keys[k] = true
	}
	for k := range keys {
		var added, removed []string
		for _, e := range r.Deny[k] {
			if !slices.Contains(old[k], e) {
				added = append(added, e)
			}
		}
		for _, e := range old[k] {
			if !slices.Contains(r.Deny[k], e) {
				removed = append(removed, e)
			}
		}
		base := hostgrants.AuditEntry{AddonID: k, OperationID: r.OperationID, RevisionBefore: r.Revision, RevisionAfter: after, By: a.UserID, Credential: credentialKind(a)}
		if len(added) != 0 {
			e := base
			e.Event, e.Ops = hostgrants.AuditDenyAdd, sorted(added)
			errs = append(errs, o.cfg.Audit.Append(e))
		}
		if len(removed) != 0 {
			e := base
			e.Event, e.Ops = hostgrants.AuditDenyRemove, sorted(removed)
			errs = append(errs, o.cfg.Audit.Append(e))
		}
	}
	return errors.Join(errs...)
}
