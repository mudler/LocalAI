// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Typed denials of the UI-only resource management calls. Each one wraps
// ErrForbidden, so every consumer that only knows denials still reads a
// denial; the HTTP layer tells them apart to give the person a useful answer.
var (
	// ErrSharingDisabled means the deployment has no named users to share
	// with (local-owner mode). It is a conflict with the deployment, not a
	// missing right.
	ErrSharingDisabled = fmt.Errorf("addon sharing is disabled: %w", ErrForbidden)
	// ErrStale means the view the person acted on is out of date: the addon
	// generation or the resource revision changed since it was listed.
	ErrStale = fmt.Errorf("addon resource view is stale: %w", ErrForbidden)
	// ErrOperationConflict means the operation ID was already used for a
	// different request. Nothing was applied: the answer is final and the
	// person repeats the change under a new operation ID.
	ErrOperationConflict = fmt.Errorf("addon operation id was used for another request: %w", ErrForbidden)
	// ErrInvalidRequest means a well-formed call that names nothing the
	// owner can act on: a missing confirmation, an unsupported operation, a
	// bad name or role.
	ErrInvalidRequest = fmt.Errorf("addon resource request is invalid: %w", ErrForbidden)
	// ErrNotSelected means no resource is selected for the conversation.
	ErrNotSelected = fmt.Errorf("no resource is selected for the conversation: %w", ErrForbidden)
	// ErrQuotaReached means the person reached the resource limits.
	ErrQuotaReached = fmt.Errorf("addon resource quota reached: %w", ErrForbidden)
)

// Refused types the add-on's own invalid-request refusal as ErrInvalidRequest,
// so every path that reaches the runtime answers it the same way: a
// deterministic 400 with nothing applied, never an outage and never an
// unknown outcome. The runtime's cause stays in the chain. Every other error
// passes through unchanged.
func Refused(err error) error {
	if err == nil || errors.Is(err, ErrInvalidRequest) || !errors.Is(err, contract.ErrRefused) || errors.Is(err, ErrUnavailable) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
}

// ResourceInfo is one resource as the caller sees it. It carries nothing of
// any other user: not their role, not their identity, not their grants.
type ResourceInfo struct {
	// ID is the opaque resource identifier.
	ID      string `json:"id" example:"r0123456789abcdef0123456789abcde"`
	AddonID string `json:"addonId" example:"tables"`
	// Name is the display name the add-on reported. It is empty when the
	// add-on could not answer in time.
	Name string `json:"name,omitempty" example:"Budget"`
	// Role is the caller's own role: viewer, editor or owner.
	Role string `json:"role" enums:"viewer,editor,owner" example:"owner"`
	// Owner is the display name of the owner, shown to people the resource
	// is shared with. It is empty in local-owner mode.
	Owner string `json:"owner,omitempty" example:"Alice"`
	// SharedWith is the number of people the resource is shared with. It is
	// present for the owner role only.
	SharedWith *int `json:"sharedWith,omitempty" example:"2"`
	// Revision is the schema and data revision the listing saw. It is
	// present for the owner role and goes back with a rename or a delete.
	Revision *contract.Revision `json:"revision,omitempty"`
}

// ResourceList is the answer of the resource listing.
type ResourceList struct {
	AddonID string `json:"addonId" example:"tables"`
	// Generation is the installed generation the listing saw. Rename, delete
	// and create carry it; a stale tab is denied.
	Generation uint64 `json:"generation" example:"3"`
	// Sharing is false in local-owner mode, where nothing can be shared.
	Sharing bool `json:"sharing"`
	// Noun is the singular word the add-on uses for its resources.
	Noun string `json:"noun,omitempty" example:"database"`
	// Operations lists the management operations the add-on supports.
	Operations []string       `json:"operations" enums:"create,rename,delete"`
	Resources  []ResourceInfo `json:"resources"`
	// Truncated says more resources exist than the bound lists.
	Truncated bool `json:"truncated,omitempty"`
}

// Grant is one grant on a whole resource.
type Grant struct {
	// UserID is the immutable account identifier of the grantee.
	UserID      string `json:"userId" example:"4f1c"`
	DisplayName string `json:"displayName,omitempty" example:"Bob"`
	Role        string `json:"role" enums:"viewer,editor" example:"viewer"`
}

// GrantList is the answer of the grant listing and of a grant change.
type GrantList struct {
	ResourceID string  `json:"resourceId"`
	Grants     []Grant `json:"grants"`
}

// UserRef is the minimal record of a person the owner may share with.
type UserRef struct {
	ID          string `json:"id" example:"4f1c"`
	DisplayName string `json:"displayName" example:"Bob"`
}

// UserList is the answer of the user picker search.
type UserList struct {
	Users []UserRef `json:"users"`
}

// CreateResourceRequest is the body of a resource creation.
type CreateResourceRequest struct {
	// AddonID names the add-on whose resource is created.
	AddonID string `json:"addonId" example:"tables"`
	// OperationID names the creation. Repeating it never creates a second
	// resource and, after an outcome_unknown answer, it is what to repeat.
	OperationID string `json:"operationId" example:"create-budget-1"`
	// Generation is the installed generation the caller listed.
	Generation uint64 `json:"generation" example:"3"`
	// Arguments are the create arguments the add-on's create tool takes, for
	// example the name. They are validated against its tool definition.
	Arguments json.RawMessage `json:"arguments" swaggertype:"object"`
}

// RenameResourceRequest is the body of a rename.
type RenameResourceRequest struct {
	OperationID string `json:"operationId" example:"rename-1"`
	Generation  uint64 `json:"generation" example:"3"`
	// Revision is the revision the caller listed. A moved revision is a
	// stale denial and nothing is changed.
	Revision contract.Revision `json:"revision"`
	Name     string            `json:"name" example:"Budget 2027"`
}

// DeleteResourceRequest is the body of a deletion.
type DeleteResourceRequest struct {
	OperationID string            `json:"operationId" example:"delete-1"`
	Generation  uint64            `json:"generation" example:"3"`
	Revision    contract.Revision `json:"revision"`
	// Confirm must equal the display name of the resource exactly: the person
	// types it to confirm. Deletion removes the data and every grant, cannot
	// be undone and is never a chat operation.
	Confirm string `json:"confirm" example:"Budget"`
}

// DeleteResult is the answer of a deletion.
type DeleteResult struct {
	ResourceID string `json:"resourceId"`
	// Deleted is true once the add-on confirmed that the data is gone and
	// the grants were removed.
	Deleted bool `json:"deleted"`
}

// ApplyResourceRequest is the body of a UI change of a resource's data or
// schema, made as the person's own explicit action.
type ApplyResourceRequest struct {
	// OperationID names the change. Repeating it with the same request
	// returns the same receipt and never applies twice; after an
	// outcome_unknown answer it is what to repeat.
	OperationID string `json:"operationId" example:"apply-1"`
	// Generation is the installed generation the caller listed.
	Generation uint64 `json:"generation" example:"3"`
	// Revision is the revision the caller read. A moved revision is a stale
	// denial and nothing is changed.
	Revision contract.Revision `json:"revision"`
	// Tool is a write or schema tool the add-on declares.
	Tool string `json:"tool" example:"rows-insert"`
	// Arguments are the tool's own arguments without the resource, which the
	// path names, and without the tool, which Tool names.
	Arguments json.RawMessage `json:"arguments" swaggertype:"object"`
}

// ApplyResult is the answer of a committed UI change.
type ApplyResult struct {
	ResourceID  string `json:"resourceId"`
	OperationID string `json:"operationId"`
	// Revision is the revision after the change.
	Revision contract.Revision `json:"revision"`
	// Result is what the add-on's receipt reported, for example the count
	// and the identifiers of the changed rows.
	Result json.RawMessage `json:"result,omitempty" swaggertype:"object"`
	// RequestHash binds the operation ID to the request: the SHA-256 of the
	// resource, the tool and the canonical arguments. A lookup of the
	// operation reports it, so a client can tell its own request from
	// another one that used the same ID.
	RequestHash string `json:"requestHash,omitempty" example:"3f786850e387550fdab836ed7e6dc881de23001b3f786850e387550fdab836ed"`
}

// SetGrantRequest is the body of a grant change.
type SetGrantRequest struct {
	// Role is viewer or editor. The owner role cannot be granted.
	Role string `json:"role" enums:"viewer,editor" example:"viewer"`
	// OperationID is optional. It is returned with outcome_unknown, and the
	// change is safe to repeat.
	OperationID string `json:"operationId,omitempty" example:"grant-1"`
}

// OperationStatus is the read-only receipt lookup of a UI mutation.
type OperationStatus struct {
	OperationID string `json:"operationId"`
	// State is pending, committed, rejected or unknown.
	State string `json:"state" enums:"pending,committed,rejected,unknown"`
	// Tool and RequestHash name the request the operation ID was first
	// used for, as the host recorded it when it dispatched the change (kind
	// apply only). They are absent when the host no longer holds the
	// request, for example after a restart. Compare RequestHash with the
	// hash of your own request before you believe a committed state.
	Tool        string `json:"tool,omitempty" example:"rows-insert"`
	RequestHash string `json:"requestHash,omitempty" example:"3f786850e387550fdab836ed7e6dc881de23001b3f786850e387550fdab836ed"`
}

// SelectionRequest is the body of a conversation selection.
type SelectionRequest struct {
	// ResourceID is the resource to use in the conversation. The caller must
	// hold at least the viewer role on it now.
	ResourceID string `json:"resourceId" example:"r0123456789abcdef0123456789abcde"`
}

// Selection is the resource selected for one conversation and addon. The
// stored reference is never a credential: every use is authorized again.
type Selection struct {
	AddonID        string `json:"addonId" example:"tables"`
	ConversationID string `json:"conversationId" example:"chat-1"`
	ResourceID     string `json:"resourceId" example:"r0123456789abcdef0123456789abcde"`
	Role           string `json:"role" enums:"viewer,editor,owner"`
	Name           string `json:"name,omitempty" example:"Budget"`
}

// ResourceManager is the owner-held UI-only resource management. Only the
// designated owner implements it; other frontends reach it through the
// carrier. It deliberately has no relation to the tool adapter or to the
// proposal broker: sharing and deletion are never tools.
type ResourceManager interface {
	List(ctx context.Context, actor contract.Actor, addonID string) (ResourceList, error)
	Create(ctx context.Context, actor contract.Actor, r CreateResourceRequest) (ResourceInfo, error)
	Grants(ctx context.Context, actor contract.Actor, resourceID string) (GrantList, error)
	SetGrant(ctx context.Context, actor contract.Actor, resourceID, userID, role string) (GrantList, error)
	RevokeGrant(ctx context.Context, actor contract.Actor, resourceID, userID string) (GrantList, error)
	SearchUsers(ctx context.Context, actor contract.Actor, resourceID, query string, limit int) (UserList, error)
	Rename(ctx context.Context, actor contract.Actor, resourceID string, r RenameResourceRequest) (ResourceInfo, error)
	Delete(ctx context.Context, actor contract.Actor, resourceID string, r DeleteResourceRequest) (DeleteResult, error)
	Apply(ctx context.Context, actor contract.Actor, resourceID string, r ApplyResourceRequest) (ApplyResult, error)
	Operation(ctx context.Context, actor contract.Actor, resourceID, kind, operationID, requestHash string) (OperationStatus, error)
	Selection(ctx context.Context, actor contract.Actor, conversationID, addonID string) (Selection, error)
	Select(ctx context.Context, actor contract.Actor, conversationID, addonID, resourceID string) (Selection, error)
	ClearSelection(ctx context.Context, actor contract.Actor, conversationID, addonID string) error
	// ResolveSelection returns the resource ID to use for a tool call of
	// the conversation, after authorizing the reference at the owner.
	ResolveSelection(ctx context.Context, actor contract.Actor, conversationID, addonID string) (string, error)
}

// Resource call operations on the carrier. They travel in ResourceCall, never
// as a typed dispatch action.
const (
	resourceList       = "list"
	resourceCreate     = "create"
	resourceGrants     = "grants"
	resourceGrantSet   = "grant-set"
	resourceGrantClear = "grant-revoke"
	resourceUsers      = "users"
	resourceRename     = "rename"
	resourceDelete     = "delete"
	resourceApply      = "apply"
	resourceStatus     = "status"
	resourceSelGet     = "selection-get"
	resourceSelPut     = "selection-put"
	resourceSelClear   = "selection-clear"
	resourceSelResolve = "selection-resolve"
)

// ResourceCall is the body a frontend forwards to the owner.
type ResourceCall struct {
	Op             string            `json:"op"`
	AddonID        string            `json:"addonId,omitempty"`
	ResourceID     string            `json:"resourceId,omitempty"`
	UserID         string            `json:"userId,omitempty"`
	Role           string            `json:"role,omitempty"`
	Name           string            `json:"name,omitempty"`
	Query          string            `json:"query,omitempty"`
	Limit          int               `json:"limit,omitempty"`
	ConversationID string            `json:"conversationId,omitempty"`
	Kind           string            `json:"kind,omitempty"`
	Confirm        string            `json:"confirm,omitempty"`
	Generation     uint64            `json:"generation,omitempty"`
	Revision       contract.Revision `json:"revision"`
	Arguments      json.RawMessage   `json:"arguments,omitempty"`
	Tool           string            `json:"tool,omitempty"`
	RequestHash    string            `json:"requestHash,omitempty"`
}

// mutating says whether the call can change state that has an operation
// identifier and an unknown outcome. Everything but the known reads and the
// two idempotent selection calls is a mutation, so an operation added later
// is classified as one. Putting or clearing a selection stores a convenience
// that is no authority and is safe to repeat, so a lost answer is plain
// unavailability.
func (c ResourceCall) mutating() bool {
	switch c.Op {
	case resourceList, resourceGrants, resourceUsers, resourceStatus, resourceSelGet, resourceSelResolve, resourceSelPut, resourceSelClear:
		return false
	}
	return true
}

func (c ResourceCall) wireAction() string { return "resource-" + c.Op }

// resourceOutcome reports a mutation that failed as unavailable as an unknown
// outcome with its original operation ID, from the owner and from every
// frontend alike. Reads, denials and unknown outcomes pass through.
func resourceOutcome(call ResourceCall, id string, e error) error {
	var unknown *OutcomeUnknown
	if e == nil || errors.As(e, &unknown) || !call.mutating() || StatusFor(e) != http.StatusServiceUnavailable {
		return e
	}
	return &OutcomeUnknown{OperationID: id}
}

func newResourceOperationID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "q" + hex.EncodeToString(raw[:]), nil
}

// resourceCall runs one call on the owner, here or through the carrier. id is
// the operation identifier of a mutation, or empty for a read.
func (g *Gateway) resourceCall(ctx context.Context, a contract.Actor, id string, call ResourceCall) (json.RawMessage, error) {
	if e := g.revalidate(ctx, a); e != nil {
		return nil, e
	}
	if id == "" {
		var err error
		if id, err = newResourceOperationID(); err != nil {
			return nil, ErrUnavailable
		}
	}
	request := contract.Request{OperationID: id, AddonID: call.AddonID, ResourceID: call.ResourceID, Action: call.wireAction()}
	if g.Forward != nil {
		raw, e := g.Forward.resource(ctx, a, request, call)
		return raw, resourceOutcome(call, id, e)
	}
	raw, e := g.resourceLocal(ctx, a, request, call)
	return raw, resourceOutcome(call, id, e)
}

// resourceLocal serves a resource call on the owner itself.
func (g *Gateway) resourceLocal(ctx context.Context, a contract.Actor, request contract.Request, call ResourceCall) (json.RawMessage, error) {
	if g.Resources == nil {
		return nil, ErrUnavailable
	}
	if request.Action != call.wireAction() {
		return nil, ErrForbidden
	}
	var out any
	var err error
	switch call.Op {
	case resourceList:
		out, err = g.Resources.List(ctx, a, call.AddonID)
	case resourceCreate:
		out, err = g.Resources.Create(ctx, a, CreateResourceRequest{AddonID: call.AddonID, OperationID: request.OperationID, Generation: call.Generation, Arguments: call.Arguments})
	case resourceGrants:
		out, err = g.Resources.Grants(ctx, a, call.ResourceID)
	case resourceGrantSet:
		out, err = g.Resources.SetGrant(ctx, a, call.ResourceID, call.UserID, call.Role)
	case resourceGrantClear:
		out, err = g.Resources.RevokeGrant(ctx, a, call.ResourceID, call.UserID)
	case resourceUsers:
		out, err = g.Resources.SearchUsers(ctx, a, call.ResourceID, call.Query, call.Limit)
	case resourceRename:
		out, err = g.Resources.Rename(ctx, a, call.ResourceID, RenameResourceRequest{OperationID: request.OperationID, Generation: call.Generation, Revision: call.Revision, Name: call.Name})
	case resourceDelete:
		out, err = g.Resources.Delete(ctx, a, call.ResourceID, DeleteResourceRequest{OperationID: request.OperationID, Generation: call.Generation, Revision: call.Revision, Confirm: call.Confirm})
	case resourceApply:
		out, err = g.Resources.Apply(ctx, a, call.ResourceID, ApplyResourceRequest{OperationID: request.OperationID, Generation: call.Generation, Revision: call.Revision, Tool: call.Tool, Arguments: call.Arguments})
	case resourceStatus:
		out, err = g.Resources.Operation(ctx, a, call.ResourceID, call.Kind, call.UserID, call.RequestHash)
	case resourceSelGet:
		out, err = g.Resources.Selection(ctx, a, call.ConversationID, call.AddonID)
	case resourceSelPut:
		out, err = g.Resources.Select(ctx, a, call.ConversationID, call.AddonID, call.ResourceID)
	case resourceSelClear:
		err = g.Resources.ClearSelection(ctx, a, call.ConversationID, call.AddonID)
		out = struct{}{}
	case resourceSelResolve:
		var id string
		id, err = g.Resources.ResolveSelection(ctx, a, call.ConversationID, call.AddonID)
		out = struct {
			ResourceID string `json:"resourceId"`
		}{id}
	default:
		return nil, ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	raw, e := json.Marshal(out)
	if e != nil {
		return nil, ErrUnavailable
	}
	return raw, nil
}

func decodeResource[T any](raw json.RawMessage, err error) (T, error) {
	var out T
	if err != nil {
		return out, err
	}
	if e := decode(bytes.NewReader(raw), &out); e != nil {
		return out, ErrUnavailable
	}
	return out, nil
}

// ListResources lists the resources of an addon the caller may use.
func (g *Gateway) ListResources(ctx context.Context, a contract.Actor, addonID string) (ResourceList, error) {
	return decodeResource[ResourceList](g.resourceCall(ctx, a, "", ResourceCall{Op: resourceList, AddonID: addonID}))
}

// CreateResource creates a resource through the add-on's create tool, as the
// person's explicit UI action.
func (g *Gateway) CreateResource(ctx context.Context, a contract.Actor, r CreateResourceRequest) (ResourceInfo, error) {
	return decodeResource[ResourceInfo](g.resourceCall(ctx, a, r.OperationID, ResourceCall{Op: resourceCreate, AddonID: r.AddonID, Generation: r.Generation, Arguments: r.Arguments}))
}

// ResourceGrants lists the grants of a resource for its owner role.
func (g *Gateway) ResourceGrants(ctx context.Context, a contract.Actor, resourceID string) (GrantList, error) {
	return decodeResource[GrantList](g.resourceCall(ctx, a, "", ResourceCall{Op: resourceGrants, ResourceID: resourceID}))
}

// SetResourceGrant gives a user a role on the whole resource.
func (g *Gateway) SetResourceGrant(ctx context.Context, a contract.Actor, operationID, resourceID, userID, role string) (GrantList, error) {
	return decodeResource[GrantList](g.resourceCall(ctx, a, operationID, ResourceCall{Op: resourceGrantSet, ResourceID: resourceID, UserID: userID, Role: role}))
}

// RevokeResourceGrant removes a user's grant.
func (g *Gateway) RevokeResourceGrant(ctx context.Context, a contract.Actor, operationID, resourceID, userID string) (GrantList, error) {
	return decodeResource[GrantList](g.resourceCall(ctx, a, operationID, ResourceCall{Op: resourceGrantClear, ResourceID: resourceID, UserID: userID}))
}

// SearchResourceUsers is the user picker of a resource's owner.
func (g *Gateway) SearchResourceUsers(ctx context.Context, a contract.Actor, resourceID, query string, limit int) (UserList, error) {
	return decodeResource[UserList](g.resourceCall(ctx, a, "", ResourceCall{Op: resourceUsers, ResourceID: resourceID, Query: query, Limit: limit}))
}

// RenameResource renames a resource through the add-on, as the owner's
// explicit UI action.
func (g *Gateway) RenameResource(ctx context.Context, a contract.Actor, resourceID string, r RenameResourceRequest) (ResourceInfo, error) {
	return decodeResource[ResourceInfo](g.resourceCall(ctx, a, r.OperationID, ResourceCall{Op: resourceRename, ResourceID: resourceID, Generation: r.Generation, Revision: r.Revision, Name: r.Name}))
}

// DeleteResource deletes a resource and its data, as the owner's explicit UI
// action. It is crash-reconciled by the owner.
func (g *Gateway) DeleteResource(ctx context.Context, a contract.Actor, resourceID string, r DeleteResourceRequest) (DeleteResult, error) {
	return decodeResource[DeleteResult](g.resourceCall(ctx, a, r.OperationID, ResourceCall{Op: resourceDelete, ResourceID: resourceID, Generation: r.Generation, Revision: r.Revision, Confirm: r.Confirm}))
}

// ApplyResource applies one data or schema change of a resource as the
// person's explicit UI action. It is not a proposal: the request is the
// intent, and the same admission, revision check and receipt rules apply.
func (g *Gateway) ApplyResource(ctx context.Context, a contract.Actor, resourceID string, r ApplyResourceRequest) (ApplyResult, error) {
	return decodeResource[ApplyResult](g.resourceCall(ctx, a, r.OperationID, ResourceCall{Op: resourceApply, ResourceID: resourceID, Generation: r.Generation, Revision: r.Revision, Tool: r.Tool, Arguments: r.Arguments}))
}

// ResourceOperation looks the receipt of a UI mutation up. It is read-only.
func (g *Gateway) ResourceOperation(ctx context.Context, a contract.Actor, resourceID, kind, operationID, requestHash string) (OperationStatus, error) {
	return decodeResource[OperationStatus](g.resourceCall(ctx, a, "", ResourceCall{Op: resourceStatus, ResourceID: resourceID, Kind: kind, UserID: operationID, RequestHash: requestHash}))
}

// ConversationSelection reads the selection of a conversation.
func (g *Gateway) ConversationSelection(ctx context.Context, a contract.Actor, conversationID, addonID string) (Selection, error) {
	return decodeResource[Selection](g.resourceCall(ctx, a, "", ResourceCall{Op: resourceSelGet, ConversationID: conversationID, AddonID: addonID}))
}

// SelectResource stores the selection of a conversation.
func (g *Gateway) SelectResource(ctx context.Context, a contract.Actor, conversationID, addonID, resourceID string) (Selection, error) {
	return decodeResource[Selection](g.resourceCall(ctx, a, "", ResourceCall{Op: resourceSelPut, ConversationID: conversationID, AddonID: addonID, ResourceID: resourceID}))
}

// ClearConversationSelection removes the selection of a conversation.
func (g *Gateway) ClearConversationSelection(ctx context.Context, a contract.Actor, conversationID, addonID string) error {
	_, e := g.resourceCall(ctx, a, "", ResourceCall{Op: resourceSelClear, ConversationID: conversationID, AddonID: addonID})
	return e
}

// ResolveSelection is what a chat tool call uses when the model named no
// resource: the owner authorizes the stored reference for the actor and
// answers its resource ID, or a denial that is the same as for an unknown
// resource. ErrNotSelected says nothing is selected.
func (g *Gateway) ResolveSelection(ctx context.Context, a contract.Actor, conversationID, addonID string) (string, error) {
	out, e := decodeResource[struct {
		ResourceID string `json:"resourceId"`
	}](g.resourceCall(ctx, a, "", ResourceCall{Op: resourceSelResolve, ConversationID: conversationID, AddonID: addonID}))
	return out.ResourceID, e
}

// resourceWire is the typed body of an owner's failed resource call.
type resourceWire struct {
	Type        string `json:"type"`
	OperationID string `json:"operationId,omitempty"`
	BlockedBy   string `json:"blockedBy,omitempty"`
}

const (
	wireSharingDisabled = "sharing_disabled"
	wireStale           = "stale"
	wireInvalid         = "invalid_request"
	wireQuota           = "quota_reached"
	wireNotSelected     = "not_selected"
	wireOpConflict      = "operation_conflict"
)

// resourceFailure writes the owner's typed failure of a resource call, so a
// forwarding frontend reports exactly what the owner decided.
func resourceFailure(w http.ResponseWriter, call ResourceCall, id string, e error) {
	e = resourceOutcome(call, id, e)
	body := resourceWire{}
	status := http.StatusServiceUnavailable
	var unknown *OutcomeUnknown
	var blocked *OutcomeBlocked
	switch {
	case errors.As(e, &unknown):
		body.Type, body.OperationID = wireOutcomeUnknown, unknown.OperationID
	case errors.As(e, &blocked) && blocked.BlockedBy != "" && StatusFor(e) == http.StatusForbidden:
		status, body.Type, body.BlockedBy = http.StatusForbidden, wireOutcomeBlocked, blocked.BlockedBy
	case errors.Is(e, ErrSharingDisabled):
		status, body.Type = http.StatusConflict, wireSharingDisabled
	case errors.Is(e, ErrStale):
		status, body.Type = http.StatusConflict, wireStale
	case errors.Is(e, ErrOperationConflict):
		status, body.Type = http.StatusConflict, wireOpConflict
	case errors.Is(e, ErrInvalidRequest):
		status, body.Type = http.StatusBadRequest, wireInvalid
	case errors.Is(e, ErrQuotaReached):
		status, body.Type = http.StatusForbidden, wireQuota
	case errors.Is(e, ErrNotSelected):
		status, body.Type = http.StatusNotFound, wireNotSelected
	default:
		typedError(w, e)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// resourceReplyErrorFor is resourceReplyError for a call whose untyped,
// non-denial failure leaves a mutation uncertain: the owner may have applied
// it before it failed.
func resourceReplyErrorFor(r io.Reader, status int, request contract.Request) error {
	e := resourceReplyError(r, status, request.OperationID)
	if errors.Is(e, ErrUnavailable) && mutation(request.Action) {
		var unknown *OutcomeUnknown
		if !errors.As(e, &unknown) {
			return &OutcomeUnknown{OperationID: request.OperationID}
		}
	}
	return e
}

// resourceReplyError reads a failed answer of the owner. A typed body gives
// back the owner's own decision; anything else is a plain denial for 403 and
// otherwise unavailable, which a mutation turns into an unknown outcome.
func resourceReplyError(r io.Reader, status int, id string) error {
	var b resourceWire
	if decode(io.LimitReader(r, 4096), &b) == nil {
		switch b.Type {
		case wireOutcomeUnknown:
			return &OutcomeUnknown{OperationID: id}
		case wireOutcomeBlocked:
			if b.BlockedBy != "" && len(b.BlockedBy) <= 256 && status == http.StatusForbidden {
				return &OutcomeBlocked{BlockedBy: b.BlockedBy, Cause: ErrForbidden}
			}
		case wireSharingDisabled:
			return ErrSharingDisabled
		case wireStale:
			return ErrStale
		case wireOpConflict:
			return ErrOperationConflict
		case wireInvalid:
			return ErrInvalidRequest
		case wireQuota:
			return ErrQuotaReached
		case wireNotSelected:
			return ErrNotSelected
		}
	}
	if status == http.StatusForbidden {
		return ErrForbidden
	}
	return ErrUnavailable
}

// resource forwards a resource call to the owner.
func (c *Carrier) resource(ctx context.Context, a contract.Actor, r contract.Request, call ResourceCall) (json.RawMessage, error) {
	resp, e := c.send(ctx, a, wireRequest{Request: r, Resource: &call})
	if e != nil {
		return nil, e
	}
	defer func() { _ = resp.Body.Close() }()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if e != nil || len(raw) > 2<<20 {
		return nil, ErrUnavailable
	}
	return raw, nil
}
