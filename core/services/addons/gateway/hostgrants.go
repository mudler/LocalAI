// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The operations of a host grants call. Each one is a carrier verb with the
// action "hostgrants-<op>". Read, preview and deny-preview change nothing
// durable, so a reply lost on the way is plainly unavailable. Every other
// verb is a mutation: a lost reply is an unknown outcome with its operation ID.
const (
	HostGrantsRead        = "read"
	HostGrantsPreview     = "preview"
	HostGrantsInstall     = "install"
	HostGrantsEdit        = "edit"
	HostGrantsRevoke      = "revoke"
	HostGrantsBlanket     = "blanket"
	HostGrantsDeny        = "deny"
	HostGrantsDenyPreview = "deny-preview"
)

// CallHostGrants is the capability of an owner that serves the host grants
// calls. A frontend asks for it before it forwards, so an older owner is
// reported as unsupported and not as an unknown outcome.
const CallHostGrants = "host-grants"

// ReasonHostGrantsUnsupported is the 503 text for an owner without the calls.
const ReasonHostGrantsUnsupported = "the add-on owner does not support host access grants; upgrade the owner"

// HostGrantsCall is one host grants request to the owner. Body is the typed
// request of the operation; the owner decodes it again, strictly.
type HostGrantsCall struct {
	Op string `json:"op"`
	// OperationID is the idempotency key of a write. A read has none.
	OperationID string          `json:"operationId,omitempty"`
	AddonID     string          `json:"addonId,omitempty"`
	Body        json.RawMessage `json:"body,omitempty"`
}

func (c HostGrantsCall) wireAction() string { return "hostgrants-" + c.Op }

func (c HostGrantsCall) write() bool {
	switch c.Op {
	case HostGrantsInstall, HostGrantsEdit, HostGrantsRevoke, HostGrantsBlanket, HostGrantsDeny:
		return true
	}
	return false
}

func (c HostGrantsCall) valid() bool {
	if isFileOp(c.Op) {
		return c.OperationID == ""
	}
	switch c.Op {
	case HostGrantsRead, HostGrantsPreview, HostGrantsDenyPreview:
		return c.OperationID == ""
	case HostGrantsInstall, HostGrantsEdit, HostGrantsRevoke, HostGrantsBlanket, HostGrantsDeny:
		return c.OperationID != "" && len(c.OperationID) <= 128
	}
	return false
}

// hostGrantsRead are the host grants actions that change nothing durable.
var hostGrantsRead = map[string]bool{
	"hostgrants-" + HostGrantsRead: true, "hostgrants-" + HostGrantsPreview: true, "hostgrants-" + HostGrantsDenyPreview: true,
}

// The error types of a HostGrantsError. They are the wire types, so every
// frontend gives the answer of the owner.
const (
	HostGrantsNotHumanSurface   = "not_human_surface"
	HostGrantsLocked            = "locked_by_deployment"
	HostGrantsBlockedByPolicy   = "blocked_by_policy"
	HostGrantsApprovalRequired  = "approval_required"
	HostGrantsApprovalStale     = "approval_stale"
	HostGrantsRevisionConflict  = "revision_conflict"
	HostGrantsOperationConflict = "operation_conflict"
	HostGrantsInvalidRequest    = "invalid_request"
	HostGrantsNotFound          = "not_found"
	HostGrantsConfirmation      = "confirmation_mismatch"
)

// HostGrantsError is a refusal the owner decided. It travels in the reply
// envelope with status 200, because typedError would map several of these
// to other statuses than the routes answer.
type HostGrantsError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

func (e *HostGrantsError) Error() string { return e.Message }

// Errors a HostAccessManager returns for a decision of the lifecycle. The
// owner service types them for the routes.
var (
	// ErrApprovalStale means the package staged for the install is not the
	// one that was approved.
	ErrApprovalStale = errors.New("host access approval is stale")
	// ErrBlockedByPolicy means a required operation is denied by a policy.
	ErrBlockedByPolicy = errors.New("host access blocked by policy")
	// ErrApprovalRequired means an operation that is required is neither
	// held nor approved.
	ErrApprovalRequired = errors.New("host access approval required")
)

func hgErr(status int, kind, msg string) *HostGrantsError {
	return &HostGrantsError{Type: kind, Message: msg, Status: status}
}

type hostGrantsReply struct {
	Data  json.RawMessage  `json:"data,omitempty"`
	Error *HostGrantsError `json:"error,omitempty"`
}

type humanSurfaceKey struct{}

// WithHumanSurface marks a context as that of a request the HTTP layer found
// to come from the administrator's own browser tab. Only the HTTP layer of
// the process that received the request may set it. A frontend carries it to
// the owner as a signed field of the forwarded request; the owner sets it
// from the verified field. Without it every host grants write is refused.
func WithHumanSurface(ctx context.Context) context.Context {
	return context.WithValue(ctx, humanSurfaceKey{}, true)
}

// HumanSurfaceOf reports whether the context carries the mark.
func HumanSurfaceOf(ctx context.Context) bool {
	v, _ := ctx.Value(humanSurfaceKey{}).(bool)
	return v
}

// HostGrants runs one host grants call: on the owner itself, or through the
// carrier. A frontend never answers from its own flags or files.
func (g *Gateway) HostGrants(ctx context.Context, a contract.Actor, call HostGrantsCall) (json.RawMessage, error) {
	if !call.valid() {
		return nil, ErrForbidden
	}
	if e := g.revalidate(ctx, a); e != nil {
		return nil, e
	}
	if g.Forward != nil {
		return g.Forward.HostGrants(ctx, a, call)
	}
	return g.hostGrantsLocal(ctx, a, call)
}

func (g *Gateway) hostGrantsLocal(ctx context.Context, a contract.Actor, call HostGrantsCall) (json.RawMessage, error) {
	if g.HostGrantsOwner == nil {
		return nil, ErrUnavailable
	}
	return g.HostGrantsOwner.Serve(ctx, a, call)
}

// strictBody decodes a body that must hold exactly one JSON object of the
// type of dst, with no unknown field.
func strictBody(raw json.RawMessage, dst any) bool {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HostApproval is what the administrator approved in the install dialog. The
// owner recomputes everything it names; the manager grants only
// approved, declared by the verified manifest and registered operations.
type HostApproval struct {
	Digest   string
	Ops      []string
	Revision uint64
	Nonce    string
}

// ReleaseAccess is what the verified, staged package of a release asks for.
type ReleaseAccess struct {
	AddonID, Title, Version string
	// Digest is the digest of the staged package.
	Digest string
	// Official is true only when the package comes from the built-in official
	// library and its receipt shows the official signer.
	Official           bool
	Required, Optional []string
	// Installed is true when the add-on is installed already, so the release
	// would be an upgrade.
	Installed bool
}

// PendingUpgrade is a candidate release that waits for approval.
type PendingUpgrade struct {
	Version string   `json:"version"`
	Delta   []string `json:"delta"`
}

// InstalledAccess is an installed add-on as the host access page needs it.
type InstalledAccess struct {
	AddonID, Title, Version string
	// State is the state the Apps list shows.
	State string
	// Identity is the trusted identity of the installed package.
	Identity string
	// Generation and OperationID name the installed generation and the
	// lifecycle operation that completed it. A new UI entry of an add-on
	// that has none takes them, so the entry counts at once.
	Generation         uint64
	OperationID        string
	Official           bool
	Required, Optional []string
	PendingUpgrade     *PendingUpgrade
	// SourceID is the library the add-on came from.
	SourceID string
	// NewOptional are optional operations the installed version asks for
	// that an upgrade added and that nothing grants yet.
	NewOptional []string
}

// HostAccessManager is what the owner service needs from the add-on manager.
// The manager implements it; the install and upgrade approval runs inside the
// manager, between the real Stage and the admission, so the package it checks
// is the package it installs.
type HostAccessManager interface {
	// HostAccess lists the installed add-ons with their verified requests.
	// It answers ErrRecoveryRequired while the owner recovers.
	HostAccess(ctx context.Context) ([]InstalledAccess, error)
	// PreviewHostAccess resolves and stages a release, reads its verified
	// manifest and discards the stage. It changes no state.
	PreviewHostAccess(ctx context.Context, sourceID, addonID, version string) (ReleaseAccess, error)
	// SubmitWithApproval runs the install (or the pinned upgrade, for
	// cmd.Action upgrade) with the approval attached.
	SubmitWithApproval(ctx context.Context, actor contract.Actor, cmd addons.LifecycleCommand, approval HostApproval) (addons.OperationStatus, error)
}

// Wire shapes. Field names are the API of the routes.

// HostGrantsPreviewRequest is the body of POST /api/addons/host-grants/preview.
type HostGrantsPreviewRequest struct {
	SourceID string `json:"sourceId"`
	AddonID  string `json:"addonId"`
	Version  string `json:"version"`
}

// HostGrantsApprove is what the administrator saw and approved.
type HostGrantsApprove struct {
	Digest   string   `json:"digest"`
	Ops      []string `json:"ops"`
	Revision uint64   `json:"revision"`
	Nonce    string   `json:"nonce"`
}

// HostGrantsInstallRequest is the body of POST /api/addons/host-grants/install.
type HostGrantsInstallRequest struct {
	OperationID string `json:"operationId"`
	// Action is install or upgrade.
	Action   string `json:"action"`
	SourceID string `json:"sourceId"`
	AddonID  string `json:"addonId"`
	Version  string `json:"version"`
	// ExpectedDigest and CandidateDigest pin an upgrade; both or neither.
	ExpectedDigest  string `json:"expectedDigest,omitempty"`
	CandidateDigest string `json:"candidateDigest,omitempty"`
	// Downgrade confirms that a local file may install an older version than
	// the installed one. Only a sourceId of local-file reads it.
	Downgrade bool              `json:"downgrade,omitempty"`
	Approve   HostGrantsApprove `json:"approve"`
}

// HostGrantsEditRequest is the body of PUT /api/addons/host-grants/{addonId}.
type HostGrantsEditRequest struct {
	OperationID string   `json:"operationId"`
	Revision    uint64   `json:"revision"`
	Ops         []string `json:"ops"`
	// Keep names held blanket grants to relabel as the administrator's own,
	// so revoking the blanket grants leaves them.
	Keep []string `json:"keep,omitempty"`
}

// HostGrantsRevokeRequest is the body the route builds from the query of
// DELETE /api/addons/host-grants/{addonId}.
type HostGrantsRevokeRequest struct {
	OperationID string `json:"operationId"`
	Revision    uint64 `json:"revision"`
}

// HostGrantsBlanketRequest is the body of PUT /api/addons/host-grants/blanket.
type HostGrantsBlanketRequest struct {
	OperationID      string `json:"operationId"`
	Revision         uint64 `json:"revision"`
	Mode             string `json:"mode"`
	IncludeOptional  bool   `json:"includeOptional"`
	ApplyToInstalled bool   `json:"applyToInstalled"`
	// RevokeOnOff, with mode off, also revokes the grants the blanket made.
	RevokeOnOff bool `json:"revokeOnOff"`
	// Confirm is the typed confirmation text, required to turn the switch
	// on or widen it.
	Confirm string `json:"confirm"`
}

// HostGrantsDenyRequest is the body of PUT /api/addons/host-grants/deny.
type HostGrantsDenyRequest struct {
	OperationID string              `json:"operationId"`
	Revision    uint64              `json:"revision"`
	Deny        map[string][]string `json:"deny"`
	// DryRun answers what the rules would do and changes nothing.
	DryRun bool `json:"dryRun,omitempty"`
}

// BlanketConfirmOfficial and BlanketConfirmAny are the texts the administrator
// types to turn the blanket approval on.
const (
	BlanketConfirmOfficial = "grant official add-ons what they declare"
	BlanketConfirmAny      = "grant any library add-on what it declares"
)

// HostOpView is one operation of an add-on as the pages show it.
type HostOpView struct {
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
	Text  string `json:"text,omitempty"`
	Risk  string `json:"risk,omitempty"`
	// Status is allowed, not_allowed, blocked or unsupported, in the page and
	// in a preview alike. In a preview not_allowed means the dialog may grant it.
	Status   string `json:"status"`
	Source   string `json:"source,omitempty"`
	Reason   string `json:"reason,omitempty"`
	At       string `json:"at,omitempty"`
	By       string `json:"by,omitempty"`
	Required bool   `json:"required,omitempty"`
	// Hint is true in a preview when the operation was granted before an
	// earlier uninstall, so the dialog may tick it. It grants nothing.
	Hint bool `json:"hint,omitempty"`
}

// HostRequested lists what a manifest asks for.
type HostRequested struct {
	Required []string `json:"required"`
	Optional []string `json:"optional"`
}

// HostGrantsAddon is the entry of one installed add-on.
type HostGrantsAddon struct {
	ID                string          `json:"id"`
	Title             string          `json:"title"`
	Version           string          `json:"version"`
	State             string          `json:"state"`
	Requested         HostRequested   `json:"requested"`
	Ops               []HostOpView    `json:"ops"`
	NoLongerRequested []HostOpView    `json:"noLongerRequested"`
	IdentityMismatch  bool            `json:"identityMismatch"`
	PendingUpgrade    *PendingUpgrade `json:"pendingUpgrade,omitempty"`
	SourceID          string          `json:"sourceId,omitempty"`
	NewOptional       []string        `json:"newOptional"`
	// Stopped lists the required operations the add-on lacks, which keep it
	// from running ("stopped: needs X").
	Stopped            string `json:"stopped,omitempty"`
	BlanketGrantsCount int    `json:"blanketGrants"`
}

// HostBlanketView is the blanket switch as the page shows it.
type HostBlanketView struct {
	Mode            string `json:"mode"`
	IncludeOptional bool   `json:"includeOptional"`
	// SetBy is ui, or deployment when a flag fixes the mode.
	SetBy    string `json:"setBy"`
	Revision uint64 `json:"revision"`
}

// HostFloorAddon is the deployment grant of one add-on.
type HostFloorAddon struct {
	ID       string   `json:"id"`
	Prefixes []string `json:"prefixes"`
}

// HostGrantsFloor is the read-only policy of the file and flags.
type HostGrantsFloor struct {
	Source string           `json:"source"`
	Addons []HostFloorAddon `json:"addons"`
}

// HostGrantsDenyView holds the deny rules by where they come from.
type HostGrantsDenyView struct {
	File map[string][]string `json:"file"`
	UI   map[string][]string `json:"ui"`
}

// HostGrantsView is the response of GET /api/addons/host-grants.
type HostGrantsView struct {
	Revision uint64 `json:"revision"`
	Locked   bool   `json:"locked"`
	// LockedReason is LockedByDeployment or LockedNoAuth when Locked.
	LockedReason string             `json:"lockedReason,omitempty"`
	Blanket      HostBlanketView    `json:"blanket"`
	Floor        HostGrantsFloor    `json:"floor"`
	Deny         HostGrantsDenyView `json:"deny"`
	Addons       []HostGrantsAddon  `json:"addons"`
}

// HostBlanketPreview says whether the blanket switch would grant on install.
type HostBlanketPreview struct {
	Applies  bool   `json:"applies"`
	Revision uint64 `json:"revision,omitempty"`
}

// HostGrantsPreviewView is the response of the preview.
type HostGrantsPreviewView struct {
	AddonID   string `json:"addonId"`
	Title     string `json:"title"`
	Digest    string `json:"digest"`
	Version   string `json:"version"`
	Installed bool   `json:"installed"`
	Official  bool   `json:"official"`
	// Added are the operations this version asks for that the installed
	// version did not. Empty on an install.
	Added        []string           `json:"added"`
	Requested    HostRequested      `json:"requested"`
	Ops          []HostOpView       `json:"ops"`
	CanInstall   bool               `json:"canInstall"`
	BlockedReq   []string           `json:"blockedRequired"`
	Revision     uint64             `json:"revision"`
	Nonce        string             `json:"nonce"`
	Locked       bool               `json:"locked"`
	LockedReason string             `json:"lockedReason,omitempty"`
	Blanket      HostBlanketPreview `json:"blanket"`
	NonceExpiry  string             `json:"nonceExpiresAt"`
}

// HostGrantsAddonResult is the response of an edit.
type HostGrantsAddonResult struct {
	Revision uint64           `json:"revision"`
	Addon    *HostGrantsAddon `json:"addon,omitempty"`
}

// HostBlanketChange is one add-on a blanket change granted operations to.
type HostBlanketChange struct {
	ID  string   `json:"id"`
	Ops []string `json:"ops"`
}

// HostBlanketSkip is one add-on a blanket change could not grant.
type HostBlanketSkip struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// HostGrantsBlanketResult is the response of the blanket change.
type HostGrantsBlanketResult struct {
	Revision uint64              `json:"revision"`
	Blanket  HostBlanketView     `json:"blanket"`
	Changed  []HostBlanketChange `json:"changed"`
	Skipped  []HostBlanketSkip   `json:"skipped"`
	Revoked  []string            `json:"revoked"`
}

// HostDenyAffect says what a deny rule set takes from one add-on.
type HostDenyAffect struct {
	ID       string   `json:"id"`
	Loses    []string `json:"loses"`
	WillStop bool     `json:"willStop"`
}

// HostGrantsDenyResult is the response of the deny change and its dry run.
type HostGrantsDenyResult struct {
	Revision uint64              `json:"revision"`
	Deny     map[string][]string `json:"deny"`
	Affects  []HostDenyAffect    `json:"affects"`
	DryRun   bool                `json:"dryRun,omitempty"`
}

// HostGrantsRevision is the response of a revoke.
type HostGrantsRevision struct {
	Revision uint64 `json:"revision"`
	Changed  bool   `json:"changed"`
}

// hostGrantsLocalReply runs a call on the owner and writes the reply envelope.
func (g *Gateway) hostGrantsLocalReply(ctx context.Context, w http.ResponseWriter, a contract.Actor, call HostGrantsCall) {
	raw, e := g.hostGrantsLocal(ctx, a, call)
	var he *HostGrantsError
	switch {
	case e == nil:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hostGrantsReply{Data: raw})
	case errors.As(e, &he):
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hostGrantsReply{Error: he})
	default:
		typedError(w, e)
	}
}

// HostGrants forwards one host access call to the owner and returns the data
// of its reply. A refusal the owner decided is a *HostGrantsError. An owner
// without the calls is reported as unavailable with its reason, never as an
// unknown outcome.
func (c *Carrier) HostGrants(ctx context.Context, a contract.Actor, call HostGrantsCall) (json.RawMessage, error) {
	if !call.valid() {
		return nil, ErrForbidden
	}
	caps, e := c.Capabilities(ctx)
	if e != nil {
		return nil, e
	}
	if !caps.Supports(CallHostGrants) {
		return nil, &UnavailableError{Reason: ReasonHostGrantsUnsupported}
	}
	id := call.OperationID
	if id == "" {
		if id, e = newResourceOperationID(); e != nil {
			return nil, ErrUnavailable
		}
	}
	resp, e := c.send(ctx, a, wireRequest{Request: contract.Request{OperationID: id, Action: call.wireAction()}, HostGrants: &call})
	if e != nil {
		return nil, e
	}
	defer func() { _ = resp.Body.Close() }()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	var reply hostGrantsReply
	if e != nil || len(raw) > 2<<20 || decode(bytes.NewReader(raw), &reply) != nil {
		if mutation(call.wireAction()) {
			return nil, &OutcomeUnknown{OperationID: id}
		}
		return nil, ErrUnavailable
	}
	if reply.Error != nil {
		return nil, reply.Error
	}
	return reply.Data, nil
}

// Reasons the UI grants are locked.
const (
	// LockedByDeployment: the policy file and flags are the only source.
	LockedByDeployment = "deployment"
	// LockedNoAuth: sign-in is off, so anyone who can reach the server could
	// change the grants.
	LockedNoAuth = "no_auth"
	// LockedNoAuthText is the sentence shown with the refusal.
	LockedNoAuthText = "Sign-in is off, so anyone who can reach this server could change these; they are set by the administrator in the policy file"
)
