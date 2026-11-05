// SPDX-License-Identifier: MIT

// Package tools adapts the tools of installed add-ons to model tool calls.
//
// The adapter is the one place where a model-facing consumer (chat, an
// agent, a scheduled job) meets add-on tools. It never decides authority:
//
//   - discovery shows only what the owner lists for the authenticated actor:
//     enabled, running add-ons with verified manifest tools, plus the narrow
//     host-owned Tables database listing;
//   - a read goes to the gateway read path with that actor;
//   - a change, a schema change or a creation goes to the proposal broker and
//     returns a reference to the stored proposal. The adapter has no way to
//     confirm, cancel or resolve a proposal: Backend does not name those
//     operations, so a tool call can never apply a change;
//   - the actor is a value the consumer took from its authenticated request
//     or job principal. Nothing in the arguments can change it, and any
//     argument that names identity, role or approval is refused.
package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/core/services/addons/toolargs"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const (
	// Prefix starts the name of every add-on tool. Names have the form
	// addon__<addonId>__<tool>: letters, digits, hyphen and underscore, so
	// they are valid function names for the OpenAI tool format. An add-on ID
	// and a tool name never contain an underscore.
	Prefix = "addon__"
	// MaxNameLength is the longest function name providers accept.
	MaxNameLength = 64
	// MaxArgumentBytes bounds the arguments of one call.
	MaxArgumentBytes = toolargs.MaxArgumentBytes
	// MaxResultBytes bounds a read result handed to a model. A larger result
	// is refused: a truncated JSON document would read as a complete one.
	MaxResultBytes = 128 << 10
	// MaxAppliedBytes bounds each excerpt copied into the result of a change
	// made at once. The chat keeps a tool result capped at 2 KiB, so the whole
	// document must stay well under it to remain valid JSON there.
	MaxAppliedBytes = 512
	// MaxTools bounds the tools one discovery returns.
	MaxTools = 64
	// MaxSummaryBytes bounds the effect sample copied into a proposal
	// reference. The approval card loads the full preview from the host.
	MaxSummaryBytes = 4 << 10
)

var (
	// ErrUnknownTool means the name is not a tool this actor may call now:
	// not in the namespace, not defined by the manifest, or its add-on is
	// disabled, stopped or not visible to the actor. Approval, sharing,
	// deletion and purge names land here: no such tool exists.
	ErrUnknownTool = errors.New("addon tool not available")
	// ErrForbiddenArgument means the arguments carry a field that stands for
	// identity, authority or approval.
	ErrForbiddenArgument = toolargs.ErrForbiddenArgument
	// ErrInvalidArguments means the arguments are not what the tool schema
	// declares.
	ErrInvalidArguments = toolargs.ErrInvalid
	// ErrResultTooLarge means a read result is too large for a model.
	ErrResultTooLarge = errors.New("addon tool result is too large; narrow the request")
)

// Backend is what the adapter needs from the owner. It deliberately names no
// way to confirm, cancel, reconcile or resolve a proposal.
type Backend interface {
	// Apps lists the add-ons the actor may see.
	Apps(ctx context.Context, actor contract.Actor) (apps.List, error)
	// Dispatch runs a typed read.
	Dispatch(ctx context.Context, actor contract.Actor, request contract.Request) (contract.Result, error)
	// Propose stores a change for a person to confirm. It never applies.
	Propose(ctx context.Context, actor contract.Actor, request contract.Request) (gateway.Proposal, error)
}

var _ Backend = (*gateway.Gateway)(nil)

// Selector resolves the resource a conversation selected. The owner
// authorizes the stored reference for the actor on every call, so the answer
// is never a credential. A Backend that does not implement it has no
// conversation selection: a call that omits the resource is invalid.
type Selector interface {
	ResolveSelection(ctx context.Context, actor contract.Actor, conversationID, addonID string) (string, error)
}

var _ Selector = (*gateway.Gateway)(nil)

// ToolDefinition is one tool as a model sees it.
type ToolDefinition struct {
	// Name is the namespaced function name.
	Name        string         `json:"name"`
	AddonID     string         `json:"addonId"`
	Tool        string         `json:"tool"`
	Kind        string         `json:"kind"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	// Shadows is the built-in chat tool this tool replaces in a chat (see
	// contract.ToolDefinition.Shadows), or empty. Only the chat consumer acts
	// on it; every other consumer keeps the namespaced Name.
	Shadows string `json:"shadows,omitempty"`
	// Execution is what the add-on declares about a tool that shadows bash. It
	// is never nil on such a tool: a manifest without a descriptor gets one
	// with the add-on's title.
	Execution *contract.ExecutionDescriptor `json:"execution,omitempty"`
	// InstalledAt is when the add-on was first installed (RFC 3339, UTC), or
	// empty. The chat consumer ranks the add-ons that shadow one name by it.
	InstalledAt string `json:"installedAt,omitempty"`
	// Permission is the capability (see contract.ToolPermission) that a
	// sender needs to use the tool in a connector chat, or empty. The chat
	// consumer holds the tool to it; another consumer has no sender.
	Permission string `json:"permission,omitempty"`
	// Credentialed is true when the add-on declares a credential: its tools
	// run with the token of a connection that the person selects. A chat
	// offers them only after a connection is selected.
	Credentialed bool `json:"credentialed,omitempty"`
	// ApprovalOverridable is true for a write tool that declares approval
	// "always" and lets the person relax it with their own setting (see
	// contract.ToolDefinition). The chat consumer shows it; the broker decides.
	ApprovalOverridable bool `json:"approvalOverridable,omitempty"`
	// ResourceArgument and ResourceLabel come from the credential slot of the
	// add-on. They only shape the line that says which connection acted.
	ResourceArgument string `json:"-"`
	ResourceLabel    string `json:"-"`
	// ResourceScoped is true when the manifest opts in to per-agent resources
	// (capabilities.resources.agentScoped). A chat of a saved agent then
	// reaches only the resources on the list of the agent.
	ResourceScoped bool `json:"-"`
}

// ResultKind says what a ToolResult carries.
type ResultKind string

const (
	// ResultData is a read result.
	ResultData ResultKind = "result"
	// ResultProposal is a reference to a stored proposal. Nothing is applied.
	ResultProposal ResultKind = "proposal"
	// ResultApplied says a change tool ran for a chat and the change is made.
	ResultApplied ResultKind = "applied"
	// ResultArtifact is a reference to a private file the add-on produced.
	ResultArtifact ResultKind = "artifact"
	// ResultError is a typed answer that asks the person to act: no resource
	// is selected for the conversation. It is a result, not a transport
	// failure, so the model can tell the person what to do.
	ResultError ResultKind = "error"
)

// CodeSelectResource is the Code of a ResultError for a tool call that named
// no resource in a conversation that has none selected.
const CodeSelectResource = "select_resource"

// CodeInvalidRequest is the Code of a ResultError for a call the add-on
// refused as invalid. Nothing was changed, so the model corrects the call
// instead of reporting an outage or retrying it unchanged.
const CodeInvalidRequest = "invalid_request"

// InvalidNote is what the model is told when the add-on refused a call.
const InvalidNote = "The add-on refused this request as invalid. Nothing was changed. Check the arguments against the tool description and correct them; do not repeat the same call and do not report an outage."

// ReasonedNote is what the model is told when the add-on refused and said why.
const ReasonedNote = "The add-on cannot do this now, and says why in message. Nothing was changed. Tell the person what it said in plain words; do not repeat the same call and do not report an outage."

// CodeUpstreamRefused is the Code of a ResultError for a call that the service
// behind the add-on refused (a permission its token lacks, a name that does
// not exist, a value it does not accept). Upstream holds the status and Message
// the sentence the add-on wrote for the person. Nothing was changed.
const CodeUpstreamRefused = "upstream_refused"

// UpstreamNote is what the model is told when the service behind the add-on refused.
const UpstreamNote = "The service behind the add-on refused this call, and message says what to change. Nothing was changed. Tell the person what message says in plain words, with the status in upstream; do not repeat the same call and do not report an outage."

// CodeNotRunning is the Code of a ResultError for a call that found the add-on
// not running. Nothing was sent.
const CodeNotRunning = "not_running"

// NotRunningNote is what the model is told when the add-on is not running.
const NotRunningNote = "The add-on is starting or stopped, so nothing was sent and nothing was changed. Tell the person to try again in a moment; you may try the same call once more later."

// CodeNotPermitted is the Code of a ResultError for a call the person is not
// allowed to make, for example without the fine-tuning permission.
const CodeNotPermitted = "not_permitted"

// NotPermittedNote is what the model is told when the person lacks a permission.
const NotPermittedNote = "The person does not have the permission this tool needs. Nothing was changed. Tell them an administrator must grant it; do not repeat the call and do not report an outage."

// CodeRateLimited is the Code of a ResultError for a call that the service
// behind the add-on turned away because it was called too often. RetryAfter says
// how long to wait.
const CodeRateLimited = "rate_limited"

// RateLimitedNote is what the model is told when the service asks it to wait.
const RateLimitedNote = "The service is limiting how often it is called. Nothing was changed. Do not repeat this call, or any call to the same add-on, before retryAfter seconds have passed; tell the person if the wait is long."

// CodeResourceNotAllowed is the Code of a ResultError for a call that names a
// resource the person did not allow for the add-on. The add-on was not called.
const CodeResourceNotAllowed = gateway.CodeResourceNotAllowed

// ResourceNotAllowedNote is what the model is told for a resource that is not on the list.
const ResourceNotAllowedNote = "That resource is not on the list the person allowed for this add-on, so nothing was sent to it. Use one of the resources in allowed, or tell the person to add it to the list on the add-on page. Do not repeat the call and do not report an outage."

// CodeNoCredential is the Code of a ResultError for an add-on that needs a
// credential the person has not given.
const CodeNoCredential = gateway.CodeNoCredential

// CodeSelectConnection is the Code of a ResultError for a call of an add-on
// that runs with a credential when the person has not selected which
// connection to use.
const CodeSelectConnection = gateway.CodeSelectConnection

// SelectConnectionNote is what the model is told when no connection is enabled.
const SelectConnectionNote = "No connection is enabled for this add-on in this chat, so nothing was sent. Tell the person to enable one for it in the Tools menu of this chat. Do not repeat the call, do not pick one yourself and do not report an outage."

// WithConnection returns a context that names the binding a credentialed call
// of a chat must use. The chats service sets it after it checked the name the
// model gave against the connections the person enabled for the chat. The
// owner refuses a binding that is not in that set.
func WithConnection(ctx context.Context, bindingID string) context.Context {
	return gateway.WithConnection(ctx, bindingID)
}

type holdKey struct{}

// HoldForPerson returns a context for a call whose write must wait for a
// person: the adapter stores the proposal and returns it, and does not apply
// it, even for a chat that applies its writes at once. The host sets it when a
// rule of its own, such as the save level of a person, says the write needs a
// confirmation. A read tool is not affected.
func HoldForPerson(ctx context.Context) context.Context {
	return context.WithValue(ctx, holdKey{}, true)
}

// HeldForPerson reports whether HoldForPerson marked the context.
func HeldForPerson(ctx context.Context) bool { return heldForPerson(ctx) }

func heldForPerson(ctx context.Context) bool {
	v, _ := ctx.Value(holdKey{}).(bool)
	return v
}

// NoCredentialNote is what the model is told when the person has no credential for the add-on.
const NoCredentialNote = "The person has not connected this add-on yet, so nothing was sent. Tell them to add a connection on the add-on page. Do not repeat the call and do not report an outage."

// CodeWriteNotAllowed is the Code of a ResultError for a write tool of an add-on
// whose binding has its write mode off. The add-on was not called.
const CodeWriteNotAllowed = gateway.CodeWriteNotAllowed

// WriteNotAllowedNote is what the model is told when writes are off.
const WriteNotAllowedNote = "The person has not allowed this add-on to change anything, so nothing was sent. Tell them they can turn on writes on the add-on page, choosing to confirm each change or to allow them. Do not repeat the call and do not report an outage."

// CodeResourceUnreachable is the Code of a ResultError for a resource the person cannot reach.
const CodeResourceUnreachable = "resource_unreachable"

// ResourceUnreachableNote is what the model is told when a resource cannot be used.
const ResourceUnreachableNote = "That resource does not exist or the person has no access to it. Nothing was changed. Ask which resource to use, or ask them to choose one; do not guess an ID and do not repeat the call."

// SelectNote is what the model is told when no resource is selected.
const SelectNote = "No resource is selected for this conversation. Ask the person to choose one with the Select action of the add-on in the chat, or to give its ID. Do not guess an ID."

// ProposalRef is what a model and a chat learn of a stored proposal. The
// approval card reads the authoritative record from the host by ID; model
// text is never the preview.
type ProposalRef struct {
	ID         string          `json:"id"`
	AddonID    string          `json:"addonId"`
	ResourceID string          `json:"resourceId"`
	Action     string          `json:"action"`
	Digest     string          `json:"digest"`
	State      string          `json:"state"`
	Count      uint64          `json:"count"`
	Summary    json.RawMessage `json:"summary,omitempty"`
	ExpiresAt  time.Time       `json:"expiresAt"`
	// Hold says why the proposal waits for a person: write_mode_propose (the
	// connection only proposes), approval_always (the tool always asks) or
	// held_by_host (a rule of the host).
	Hold string `json:"hold,omitempty"`
	// Connection names the connection the change will be made with, as
	// "as Work, repository acme/widgets"; empty for an add-on with no credential.
	Connection string `json:"connection,omitempty"`
}

// AppliedRef is what a chat learns of a change it made. The person enabled the
// add-on for the chat or the agent, which is the consent, so the change is made
// at once. The safety net is undo: when the add-on kept a change set for the
// change, Undoable is true and ChangeID names it; the person undoes it from the
// chat with a button, and the model cannot, because the undo route accepts the
// person's own browser session only. When it kept none, Reason says why.
type AppliedRef struct {
	// ID is the operation ID of the change.
	ID         string          `json:"id"`
	AddonID    string          `json:"addonId"`
	ResourceID string          `json:"resourceId,omitempty"`
	Action     string          `json:"action"`
	Count      uint64          `json:"count"`
	Summary    json.RawMessage `json:"summary,omitempty"`
	// Result is what the add-on's receipt reported.
	Result json.RawMessage `json:"result,omitempty"`
	// Undoable says whether the person can roll the change back.
	Undoable bool `json:"undoable"`
	// ChangeID is the change set to undo, when Undoable.
	ChangeID string `json:"changeId,omitempty"`
	// Undo says in one line what the undo will do, when Undoable.
	Undo string `json:"undo,omitempty"`
	// Reason says why the change cannot be undone, when it cannot.
	Reason string `json:"reason,omitempty"`
}

// ArtifactRef points at a private artifact. The bytes are fetched by the
// person's authenticated session, never through a tool result.
type ArtifactRef struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
}

// ToolResult discriminates a read result, a host proposal reference and
// private artifact references.
type ToolResult struct {
	Kind      ResultKind      `json:"kind"`
	Tool      string          `json:"tool"`
	Result    json.RawMessage `json:"result,omitempty"`
	Proposal  *ProposalRef    `json:"proposal,omitempty"`
	Applied   *AppliedRef     `json:"applied,omitempty"`
	Artifacts []ArtifactRef   `json:"artifacts,omitempty"`
	// Code says what the person is asked to do, for a ResultError.
	Code string `json:"code,omitempty"`
	// Message is the reason the add-on gave for refusing, in a short fixed
	// sentence such as a missing engine. It is empty when the add-on gave none.
	Message string `json:"message,omitempty"`
	// Note is host text for the model: a proposal is not applied.
	Note string `json:"note,omitempty"`
	// Allowed lists the resources the person's bindings allow, for the code
	// resource_not_allowed.
	Allowed []string `json:"allowed,omitempty"`
	// Connection says which connection the call used, as "as Work, repository
	// acme/widgets", for a tool of an add-on that runs with a credential. The
	// host writes it; ConnectionID is the binding. Tell the person which
	// connection acted.
	Connection   string `json:"connection,omitempty"`
	ConnectionID string `json:"connectionId,omitempty"`
	// RetryAfter is how many seconds to wait, for the code rate_limited.
	RetryAfter int `json:"retryAfter,omitempty"`
	// Upstream is the status that the service behind the add-on answered, for
	// the code upstream_refused. Message says in a sentence what to change.
	Upstream int `json:"upstream,omitempty"`
}

// ProposalNote is what the model is told after a proposal.
const ProposalNote = "Nothing has been changed. A person must review and confirm this proposal in the approval card. Do not say the change was made."

// AppliedNote is what the model is told after a change it made in a chat that
// cannot be undone. The reason follows it in the result.
const AppliedNote = "The change was made. It cannot be undone: see the reason. Tell the person plainly what changed and that it cannot be undone."

// UndoableNote is what the model is told after a change it made in a chat that
// the person can undo. Only the person can: the button is theirs.
const UndoableNote = "The change was made. The person can undo it with the Undo button on this change in the chat; you cannot undo it. Tell the person plainly what changed and that they can undo it."

// noUndoReason is the reason when the add-on kept no change set and said
// nothing more.
const noUndoReason = "the add-on keeps no change set for it"

// Applier is the owner side of a change the chats consumer makes at once. It is
// separate from Backend so that no consumer that holds only a Backend can apply
// a change, and the adapter has no method that confirms anything: the chats
// adapter confirms only the proposal it has just stored for the same actor.
type Applier interface {
	// ConfirmProposal applies the stored proposal once.
	ConfirmProposal(ctx context.Context, actor contract.Actor, id, digest string) (gateway.Proposal, error)
	// CancelProposal withdraws a pending proposal.
	CancelProposal(ctx context.Context, actor contract.Actor, id string) (gateway.Proposal, error)
}

var _ Applier = (*gateway.Gateway)(nil)

// ToolAdapter exposes add-on tools to model consumers.
type ToolAdapter struct {
	// connection names the connection a call used, or "". The chat consumer
	// sets it; another consumer has no chat.
	connection ConnectionNamer
	// actions says that this adapter serves action tools. Only the adapter of
	// the chats consumer is built with it; every other consumer neither lists
	// nor runs an action tool.
	actions bool
	apply   Applier
	backend Backend
	newID   func() (string, error)
}

// ConnectionNamer returns the display name of the binding that a call of the
// chat conversation uses, or "" when the binding is not known.
type ConnectionNamer func(ctx context.Context, conversationID, bindingID string) string

// WithConnectionNames makes the adapter state, in the result of a tool of an
// add-on that runs with a credential, which connection acted. It returns t.
func (t *ToolAdapter) WithConnectionNames(f ConnectionNamer) *ToolAdapter {
	t.connection = f
	return t
}

// connectionLine is the sentence that says which connection acted.
func connectionLine(name, label, key string) string {
	if key == "" {
		return "as " + name
	}
	if label == "" {
		label = "resource"
	}
	return "as " + name + ", " + label + " " + key
}

// NewToolAdapter returns an adapter over the owner.
func NewToolAdapter(b Backend) *ToolAdapter {
	return &ToolAdapter{backend: b, newID: randomOperationID}
}

// ActBackend is the owner side of action tools. It is separate from Backend so
// that no consumer that holds only a Backend can run one.
type ActBackend interface {
	// Act runs an action tool for the actor and returns its answer. It never
	// proposes and never repeats a call.
	Act(ctx context.Context, actor contract.Actor, request contract.Request) (contract.Result, error)
}

var _ ActBackend = (*gateway.Gateway)(nil)

// NewChatToolAdapter returns an adapter for the chats consumer: besides what
// NewToolAdapter serves, it lists and runs action tools, as the chat's user,
// with no confirmation. The owner it is given must be an ActBackend. A write,
// schema or create tool is made at once, not left as a proposal, when the owner
// is also an Applier: enabling the add-on for the chat is the consent, and the
// same preview, revision and receipt rules apply.
func NewChatToolAdapter(b Backend) *ToolAdapter {
	t := &ToolAdapter{backend: b, newID: randomOperationID, actions: true}
	t.apply, _ = b.(Applier)
	return t
}

func randomOperationID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "t" + hex.EncodeToString(raw[:]), nil
}

// ToolName formats a qualified identity. Callers validate components and, for
// model-callable tools, the total length with ParseToolName.
func ToolName(addonID, tool string) string { return Prefix + addonID + "__" + tool }

// ParseQualifiedToolName splits an add-on tool identity with valid contract
// components. It does not apply the model's total name limit: Studio stand-ins
// identify unavailable add-ons, including long IDs, rather than dispatch tools.
func ParseQualifiedToolName(name string) (addonID, tool string, ok bool) {
	qualified, ok := strings.CutPrefix(name, Prefix)
	if !ok {
		return "", "", false
	}
	addonID, tool, ok = strings.Cut(qualified, "__")
	if !ok || !contract.ValidName(addonID) || !contract.ValidName(tool) {
		return "", "", false
	}
	return addonID, tool, true
}

// ParseToolName splits a model-callable name, enforcing its total length limit.
func ParseToolName(name string) (addonID, tool string, ok bool) {
	if len(name) > MaxNameLength {
		return "", "", false
	}
	return ParseQualifiedToolName(name)
}

// IsToolName reports whether name is in the namespace reserved for add-on
// tools. Other tool sources never use it.
func IsToolName(name string) bool { return strings.HasPrefix(name, Prefix) }

func checkActor(a contract.Actor) error {
	if strings.TrimSpace(a.UserID) == "" || strings.TrimSpace(a.CredentialID) == "" || (a.Mode != "named-user" && a.Mode != "local-owner") {
		return gateway.ErrForbidden
	}
	return nil
}

// visible lists the apps whose tools the actor can use now.
func (t *ToolAdapter) visible(ctx context.Context, actor contract.Actor) ([]apps.App, error) {
	if err := checkActor(actor); err != nil {
		return nil, err
	}
	list, err := t.backend.Apps(ctx, actor)
	if err != nil {
		return nil, gateway.ClassifyCheck(err)
	}
	var out []apps.App
	for _, a := range list.Apps {
		if a.Enabled && a.State == "running" && a.Generation > 0 && (len(a.ToolDefinitions) > 0 || t.hasListing(a)) {
			if err := validateListing(a); err != nil {
				return nil, err
			}
			out = append(out, a)
		}
	}
	return out, nil
}

// Controls returns the host controls the apps list draws for one add-on the
// actor can see (see contract.EntitledControls), or nil when it is not
// listed, has no UI or is not entitled to any.
func (t *ToolAdapter) Controls(ctx context.Context, actor contract.Actor, addonID string) ([]string, error) {
	if err := checkActor(actor); err != nil {
		return nil, err
	}
	list, err := t.backend.Apps(ctx, actor)
	if err != nil {
		return nil, gateway.ClassifyCheck(err)
	}
	for _, a := range list.Apps {
		if a.ID == addonID && a.UI != nil {
			return slices.Clone(a.UI.Controls), nil
		}
	}
	return nil, nil
}

// Network returns the network an add-on declares, when the actor can see the
// add-on and the apps list draws its network-policy control.
func (t *ToolAdapter) Network(ctx context.Context, actor contract.Actor, addonID string) (apps.NetworkView, bool, error) {
	if err := checkActor(actor); err != nil {
		return apps.NetworkView{}, false, err
	}
	list, err := t.backend.Apps(ctx, actor)
	if err != nil {
		return apps.NetworkView{}, false, gateway.ClassifyCheck(err)
	}
	for _, a := range list.Apps {
		if a.ID == addonID && a.Network != nil {
			return *a.Network, true, nil
		}
	}
	return apps.NetworkView{}, false, nil
}

// AddonSkills is the skills of one add-on the actor can see.
type AddonSkills struct {
	AddonID, Title string
	// InstalledAt is when the add-on was first installed, or empty.
	InstalledAt string
	Skills      []contract.SkillDefinition
	// Isolated are the skills that need the add-on's own commands tool to
	// report isolated execution (see contract.IsolatedSkills).
	Isolated []string
	// Slots are the slots the add-on's skills serve (see contract.SlotsOf).
	Slots []contract.Slot
	// Running is true when the add-on may serve now: enabled and running, as
	// Discover requires for tools.
	Running bool
}

// Skills lists the installed add-ons that ship skills and that the actor can
// see, running or not. A chat offers the skills of a running one from the copy
// in its skill layer, and says when a built-in skill serves because the
// add-on that replaces it is not running.
func (t *ToolAdapter) Skills(ctx context.Context, actor contract.Actor) ([]AddonSkills, error) {
	if err := checkActor(actor); err != nil {
		return nil, err
	}
	list, err := t.backend.Apps(ctx, actor)
	if err != nil {
		return nil, gateway.ClassifyCheck(err)
	}
	var out []AddonSkills
	for _, a := range list.Apps {
		if len(a.Skills) == 0 {
			continue
		}
		running := a.Enabled && a.State == "running" && a.Generation > 0
		out = append(out, AddonSkills{AddonID: a.ID, Title: a.Title, InstalledAt: a.InstalledAt, Skills: slices.Clone(a.Skills),
			Isolated: contract.IsolatedSkills(a.ToolDefinitions, a.Skills), Slots: contract.SlotsOf(a.Skills), Running: running})
	}
	slices.SortFunc(out, func(x, y AddonSkills) int { return strings.Compare(x.AddonID, y.AddonID) })
	return out, nil
}

// AddonContextFiles is the context files one add-on declares.
type AddonContextFiles struct {
	AddonID string
	Files   []contract.ContextFile
	// Running is true when the add-on may serve now: enabled and running, as
	// Discover requires for tools.
	Running bool
}

// ContextFiles lists the installed add-ons that declare context files and that
// the actor can see, running or not. The host reads the files itself; a chat
// loads those of a running add-on, and the Memory page lists them for any.
func (t *ToolAdapter) ContextFiles(ctx context.Context, actor contract.Actor) ([]AddonContextFiles, error) {
	if err := checkActor(actor); err != nil {
		return nil, err
	}
	list, err := t.backend.Apps(ctx, actor)
	if err != nil {
		return nil, gateway.ClassifyCheck(err)
	}
	var out []AddonContextFiles
	for _, a := range list.Apps {
		if len(a.ContextFiles) == 0 {
			continue
		}
		running := a.Enabled && a.State == "running" && a.Generation > 0
		out = append(out, AddonContextFiles{AddonID: a.ID, Files: slices.Clone(a.ContextFiles), Running: running})
	}
	slices.SortFunc(out, func(x, y AddonContextFiles) int { return strings.Compare(x.AddonID, y.AddonID) })
	return out, nil
}

// AddonProviders is the provider slots one add-on declares, with the title and
// description it shows to people.
type AddonProviders struct {
	AddonID, Title, Description string
	Provides                    []contract.ProviderDeclaration
	// Running is true when the add-on may serve now: enabled and running.
	Running bool
}

// Providers lists the installed add-ons that declare provider slots and that
// the actor can see, running or not. The host decides which one serves a slot;
// the add-on is not called.
func (t *ToolAdapter) Providers(ctx context.Context, actor contract.Actor) ([]AddonProviders, error) {
	if err := checkActor(actor); err != nil {
		return nil, err
	}
	list, err := t.backend.Apps(ctx, actor)
	if err != nil {
		return nil, gateway.ClassifyCheck(err)
	}
	var out []AddonProviders
	for _, a := range list.Apps {
		if len(a.Provides) == 0 {
			continue
		}
		running := a.Enabled && a.State == "running" && a.Generation > 0
		out = append(out, AddonProviders{AddonID: a.ID, Title: a.Title, Description: a.Description, Provides: slices.Clone(a.Provides), Running: running})
	}
	slices.SortFunc(out, func(x, y AddonProviders) int { return strings.Compare(x.AddonID, y.AddonID) })
	return out, nil
}

// AddonUserSettings is the per-person settings one add-on declares.
type AddonUserSettings struct {
	AddonID  string
	Settings []contract.UserSetting
	// Running is true when the add-on may serve now: enabled and running.
	Running bool
}

// UserSettings lists the installed add-ons that declare per-person settings and
// that the actor can see, running or not. The host reads the answers itself;
// the add-on is not called.
func (t *ToolAdapter) UserSettings(ctx context.Context, actor contract.Actor) ([]AddonUserSettings, error) {
	if err := checkActor(actor); err != nil {
		return nil, err
	}
	list, err := t.backend.Apps(ctx, actor)
	if err != nil {
		return nil, gateway.ClassifyCheck(err)
	}
	var out []AddonUserSettings
	for _, a := range list.Apps {
		if len(a.UserSettings) == 0 {
			continue
		}
		running := a.Enabled && a.State == "running" && a.Generation > 0
		out = append(out, AddonUserSettings{AddonID: a.ID, Settings: slices.Clone(a.UserSettings), Running: running})
	}
	slices.SortFunc(out, func(x, y AddonUserSettings) int { return strings.Compare(x.AddonID, y.AddonID) })
	return out, nil
}

// AddonListing is the title and description an add-on declares for people.
type AddonListing struct {
	AddonID, Title, Description string
	// Resources is set for an add-on that scopes its resources per agent.
	Resources *ResourceLabel
}

// ResourceLabel is the word an add-on uses for one of its resources, as its
// manifest declares it.
type ResourceLabel struct {
	Noun, NounPlural string
	// Translations are the translated nouns by language tag.
	Translations map[string]contract.ResourceText
}

// Listings returns the title and description of every add-on the actor can
// see. An add-on that declares neither has its ID as title.
func (t *ToolAdapter) Listings(ctx context.Context, actor contract.Actor) ([]AddonListing, error) {
	if err := checkActor(actor); err != nil {
		return nil, err
	}
	list, err := t.backend.Apps(ctx, actor)
	if err != nil {
		return nil, gateway.ClassifyCheck(err)
	}
	out := make([]AddonListing, 0, len(list.Apps))
	for _, a := range list.Apps {
		l := AddonListing{AddonID: a.ID, Title: a.Title, Description: a.Description}
		if r := a.Resources; r != nil && r.AgentScoped {
			l.Resources = &ResourceLabel{Noun: r.Noun, NounPlural: r.NounPlural, Translations: maps.Clone(a.ResourceTexts)}
		}
		out = append(out, l)
	}
	return out, nil
}

// AddonShadows is one installed add-on that replaces built-in chat tools, as
// the actor sees it, running or not.
type AddonShadows struct {
	AddonID, Title string
	// InstalledAt is when the add-on was first installed, or empty.
	InstalledAt string
	// Tools are the built-in chat tools it replaces (bash, web_fetch).
	Tools []string
	// Running is true when the add-on may serve now: enabled and running, as
	// Discover requires.
	Running bool
}

// Shadowers lists the installed add-ons that replace a built-in chat tool and
// that the actor can see, in any state. A chat uses it to tell a pinned add-on
// that is stopped (the built-in serves, with a note) from a pin to an add-on
// that is not installed (the pin is ignored), and Settings lists every add-on
// an administrator can choose.
func (t *ToolAdapter) Shadowers(ctx context.Context, actor contract.Actor) ([]AddonShadows, error) {
	if err := checkActor(actor); err != nil {
		return nil, err
	}
	list, err := t.backend.Apps(ctx, actor)
	if err != nil {
		return nil, gateway.ClassifyCheck(err)
	}
	var out []AddonShadows
	for _, a := range list.Apps {
		var names []string
		for _, d := range a.ToolDefinitions {
			if d.Shadows != "" && slices.Contains(a.Tools, d.Name) && contract.ValidateShadow(d) == nil && !slices.Contains(names, d.Shadows) {
				names = append(names, d.Shadows)
			}
		}
		if len(names) == 0 {
			continue
		}
		running := a.Enabled && a.State == "running" && a.Generation > 0
		out = append(out, AddonShadows{AddonID: a.ID, Title: a.Title, InstalledAt: a.InstalledAt, Tools: names, Running: running})
	}
	slices.SortFunc(out, func(x, y AddonShadows) int { return strings.Compare(x.AddonID, y.AddonID) })
	return out, nil
}

func definition(a apps.App, d contract.ToolDefinition) (ToolDefinition, bool) {
	if d.UIOnly {
		return ToolDefinition{}, false // for the add-on's own page, never for a model
	}
	name := ToolName(a.ID, d.Name)
	if _, _, ok := ParseToolName(name); !ok || !slices.Contains(a.Tools, d.Name) {
		return ToolDefinition{}, false
	}
	// The owner validated the manifest at install; it is checked again here
	// because a schema is what a model is told to obey.
	if contract.ValidateDefinitionSchema(d) != nil {
		return ToolDefinition{}, false
	}
	var params map[string]any
	if json.Unmarshal(d.InputSchema, &params) != nil {
		return ToolDefinition{}, false
	}
	def := ToolDefinition{Name: name, AddonID: a.ID, Tool: d.Name, Kind: d.Kind, Description: d.Description, Parameters: params, InstalledAt: a.InstalledAt, Permission: contract.ToolPermission(a.Credentialed || (a.Resources != nil && a.Resources.AgentScoped), d), Credentialed: a.Credentialed, ApprovalOverridable: d.ApprovalOverridable && d.Approval == contract.ApprovalAlways,
		ResourceArgument: a.ResourceArgument, ResourceLabel: a.ResourceLabel, ResourceScoped: a.Resources != nil && a.Resources.AgentScoped}
	// Checked again for the same reason as the schema: a manifest that names a
	// tool outside the list, or a shadow of the wrong kind or schema, never
	// shadows it.
	if d.Shadows != "" && contract.ValidateShadow(d) == nil {
		def.Shadows = d.Shadows
		if d.Shadows == "bash" {
			def.Execution = executionOf(a, d)
		}
	}
	return def, true
}

// executionOf is the descriptor of a commands tool as the chat consumer sees
// it. A manifest that declares none behaves as a provider of commands under the
// add-on's title.
func executionOf(a apps.App, d contract.ToolDefinition) *contract.ExecutionDescriptor {
	e := contract.ExecutionDescriptor{Class: contract.ExecutionClassCommands}
	if d.Execution != nil {
		e = *d.Execution
	}
	if e.Title == "" {
		e.Title = a.Title
	}
	return &e
}

// Discover returns the tools the actor may offer a model now, in a stable
// order. A model is never told about a tool it could not call.
func (t *ToolAdapter) Discover(ctx context.Context, actor contract.Actor) ([]ToolDefinition, error) {
	list, err := t.visible(ctx, actor)
	if err != nil {
		return nil, err
	}
	var out []ToolDefinition
	for _, a := range list {
		if l, ok := t.listingOf(a); ok && len(out) < MaxTools {
			out = append(out, resourceListingDefinition(a, l))
		}
		for _, d := range a.ToolDefinitions {
			if d.Kind == contract.ToolKindAction && !t.actions {
				continue
			}
			if def, ok := definition(a, d); ok && len(out) < MaxTools {
				out = append(out, def)
			}
		}
	}
	slices.SortFunc(out, func(x, y ToolDefinition) int { return strings.Compare(x.Name, y.Name) })
	return out, nil
}

// Execute runs one tool call as actor. The actor is the authenticated
// principal of the consumer's request or job; arguments cannot change it.
func (t *ToolAdapter) Execute(ctx context.Context, actor contract.Actor, name string, arguments json.RawMessage) (ToolResult, error) {
	return t.ExecuteIn(ctx, actor, "", name, arguments)
}

// ChatCall is the chat a tool call comes from: the user whose files the chat
// uses, as the chats service names them, and the chat's ID. The chat turn
// builds it from its own record of the chat, never from the model's arguments.
type ChatCall struct {
	principal, conversation string
}

// NewChatCall returns the chat of a tool call.
func NewChatCall(principal, conversation string) ChatCall {
	return ChatCall{principal: principal, conversation: conversation}
}

// ExecuteChat runs one tool call of a chat, of any kind. The call is bound to
// the chat on the host side (hostops.WithBinding): the owner records the chat
// next to the envelope, so the add-on's workspace operations act with the
// chat's access, also for a read tool that names no resource. The envelope the
// add-on receives does not change. The conversation is the chat's ID, used as
// ExecuteIn uses it.
func (t *ToolAdapter) ExecuteChat(ctx context.Context, actor contract.Actor, call ChatCall, name string, arguments json.RawMessage) (ToolResult, error) {
	b := hostops.Binding{Principal: call.principal, ChatID: call.conversation}
	if !b.Valid() || !ValidConversationID(call.conversation) {
		return ToolResult{}, ErrInvalidArguments
	}
	return t.execute(hostops.WithBinding(ctx, b), actor, call.conversation, name, arguments)
}

// ExecuteIn runs one tool call of a conversation. The conversation ID comes
// from the consumer's authenticated request, never from the arguments: it is
// used only when the call names no resource, to resolve the conversation's
// selection at the owner. An empty ID means the consumer has no conversation.
// The chats adapter refuses a conversation here: a chat's call goes through
// ExecuteChat, so it is never made without its binding.
func (t *ToolAdapter) ExecuteIn(ctx context.Context, actor contract.Actor, conversationID, name string, arguments json.RawMessage) (ToolResult, error) {
	if t.actions && conversationID != "" {
		return ToolResult{}, ErrInvalidArguments
	}
	return t.execute(ctx, actor, conversationID, name, arguments)
}

func (t *ToolAdapter) execute(ctx context.Context, actor contract.Actor, conversationID, name string, arguments json.RawMessage) (ToolResult, error) {
	var acted connectionUse
	res, err := t.executeTool(ctx, actor, conversationID, name, arguments, &acted)
	if o := OutcomeFrom(ctx); o != nil {
		result, reason, status := Classify(res, err)
		o.Finish(result, reason, status)
	}
	if err == nil && acted.line != "" && res.Connection == "" {
		res.Connection, res.ConnectionID = acted.line, acted.id
		if res.Proposal != nil {
			res.Proposal.Connection = acted.line
		}
	}
	return res, err
}

// connectionUse is the connection that a credentialed call of a chat names.
type connectionUse struct{ id, line string }

func (t *ToolAdapter) executeTool(ctx context.Context, actor contract.Actor, conversationID, name string, arguments json.RawMessage, acted *connectionUse) (ToolResult, error) {
	if conversationID != "" && !ValidConversationID(conversationID) {
		return ToolResult{}, ErrInvalidArguments
	}
	addonID, tool, ok := ParseToolName(name)
	if !ok {
		return ToolResult{}, ErrUnknownTool
	}
	list, err := t.visible(ctx, actor)
	if err != nil {
		return ToolResult{}, err
	}
	var app apps.App
	var def ToolDefinition
	var found bool
	for _, a := range list {
		if a.ID != addonID {
			continue
		}
		if l, ok := t.listingOf(a); ok && l.Tool == tool {
			return t.listResources(ctx, actor, a, l, name, arguments)
		}
		for _, d := range a.ToolDefinitions {
			if d.Name != tool {
				continue
			}
			if d.Kind == contract.ToolKindAction && !t.actions {
				continue // not offered to this consumer, so not there
			}
			if x, ok := definition(a, d); ok {
				app, def, found = a, x, true
			}
		}
	}
	if !found {
		return ToolResult{}, ErrUnknownTool
	}
	out := OutcomeFrom(ctx)
	out.Call(def.Kind, "")
	resource, body, err := toolargs.Bind(def.Tool, def.Parameters, arguments, conversationID != "")
	if err != nil {
		return ToolResult{}, err
	}
	out.Call("", resource)
	if def.Credentialed {
		id := gateway.ConnectionOverride(ctx)
		key := ""
		if id != "" && def.ResourceArgument != "" {
			var in map[string]json.RawMessage
			if json.Unmarshal(body, &in) == nil {
				_ = json.Unmarshal(in[def.ResourceArgument], &key)
			}
		}
		out.Connection(id)
		// The name the person gave the resource of the connection (a
		// repository), never the text of the call.
		out.Call("", key)
		if id != "" && conversationID != "" && t.connection != nil {
			cname := t.connection(ctx, conversationID, id)
			*acted = connectionUse{id: id, line: connectionLine(cname, def.ResourceLabel, key)}
		}
	}
	if resource == "" && conversationID != "" && requiresResource(def) {
		sel, ok := t.backend.(Selector)
		if !ok {
			return ToolResult{}, ErrInvalidArguments
		}
		id, err := sel.ResolveSelection(ctx, actor, conversationID, app.ID)
		switch {
		case errors.Is(err, gateway.ErrNotSelected):
			return ToolResult{Kind: ResultError, Tool: name, Code: CodeSelectResource, Note: SelectNote}, nil
		case err != nil:
			// A revoked, deleted or foreign reference is the denial of an
			// unknown resource, so the model reads the same words as for a
			// resource it named that does not exist. An outage stays an
			// outage.
			return refusedResult(name, err)
		case !toolargs.ValidResourceID(id):
			return ToolResult{}, gateway.ErrForbidden
		}
		resource = id
	}
	// The list of the agent of the chat, on top of what the person may use.
	// It binds the resource the model named and the one the chat selected.
	if res, refused := limitedResult(ctx, def, name, resource); refused {
		return res, nil
	}
	op, err := t.newID()
	if err != nil {
		return ToolResult{}, gateway.ErrUnavailable
	}
	request := contract.Request{AddonID: app.ID, ResourceID: resource, OperationID: op, Generation: app.Generation, Arguments: body}
	if def.Kind == contract.ToolKindAction {
		ab, ok := t.backend.(ActBackend)
		if !ok {
			return ToolResult{}, gateway.ErrUnavailable
		}
		request.Action = "act"
		// An action works for one chat. The conversation comes from the chat's
		// own authenticated turn and rides in ResourceID, mapped to a valid resource ID; the model cannot name
		// it, because no action schema has that property. It scopes the
		// add-on's state and is never authority.
		if conversationID == "" {
			return ToolResult{}, ErrInvalidArguments
		}
		request.ResourceID = toolargs.ActionResourceID(conversationID)
		out, err := ab.Act(ctx, actor, request)
		if err != nil {
			return refusedResult(name, err)
		}
		return readResult(name, out)
	}
	if def.Kind == contract.ToolKindRead {
		request.Action = "read"
		out, err := t.backend.Dispatch(ctx, actor, request)
		if err != nil {
			return refusedResult(name, err)
		}
		return readResult(name, out)
	}
	request.Action = map[string]string{contract.ToolKindWrite: "write", contract.ToolKindSchema: "schema", contract.ToolKindCreate: "create"}[def.Kind]
	p, err := t.backend.Propose(ctx, actor, request)
	if err != nil {
		return refusedResult(name, err)
	}
	if t.apply != nil && !p.AwaitsPerson && !heldForPerson(ctx) {
		return t.applyNow(ctx, actor, name, p)
	}
	ref := ProposalRef{ID: p.ID, AddonID: p.AddonID, ResourceID: p.ResourceID, Action: p.Action, Digest: p.Digest,
		State: string(p.State), Count: p.Effects.Count, ExpiresAt: p.ExpiresAt, Hold: p.Hold}
	if ref.Hold == "" {
		ref.Hold = HoldByHost
	}
	if p.ConnectionName != "" {
		// What the owner stored, so the card says it on every frontend.
		ref.Connection = "as " + p.ConnectionName
	}
	if len(p.Effects.Sample) <= MaxSummaryBytes {
		ref.Summary = p.Effects.Sample
	}
	return ToolResult{Kind: ResultProposal, Tool: name, Proposal: &ref, Note: ProposalNote}, nil
}

// applyNow confirms the proposal the adapter has just stored for the actor. A
// failure that is certain withdraws the proposal, so nothing waits for a
// browser; an unknown outcome is left alone and reported, never repeated.
func (t *ToolAdapter) applyNow(ctx context.Context, actor contract.Actor, name string, p gateway.Proposal) (ToolResult, error) {
	// The mark keeps the owner from treating this confirmation as a person's
	// when the chat and the browser share one credential (local-owner mode).
	done, err := t.apply.ConfirmProposal(gateway.WithChatTurn(ctx), actor, p.ID, p.Digest)
	if err != nil {
		var unknown *gateway.OutcomeUnknown
		var unavailable *gateway.ProposalUnavailable
		if !errors.As(err, &unknown) && !errors.As(err, &unavailable) {
			// The caller's context may be over; the withdrawal must still run.
			_, _ = t.apply.CancelProposal(context.WithoutCancel(ctx), actor, p.ID)
		}
		return refusedResult(name, err)
	}
	if done.State != gateway.ProposalApplied {
		// The add-on rejected it at the apply: the data moved or it cannot do
		// this. Nothing changed, so the model corrects or asks again.
		return ToolResult{}, gateway.ErrStale
	}
	ref := AppliedRef{ID: p.ID, AddonID: p.AddonID, ResourceID: p.ResourceID, Action: p.Action, Count: p.Effects.Count}
	if len(p.Effects.Sample) <= MaxAppliedBytes {
		ref.Summary = p.Effects.Sample
	}
	if done.Receipt != nil && len(done.Receipt.Result.Payload) <= MaxAppliedBytes {
		ref.Result = done.Receipt.Result.Payload
	}
	note := AppliedNote
	n, _ := contract.ParseUndoNote(receiptPayload(done))
	switch {
	case n.Available:
		ref.Undoable, ref.ChangeID, ref.Undo = true, n.ID, n.Summary
		note = UndoableNote
	case n.Reason != "":
		ref.Reason = n.Reason
	default:
		ref.Reason = noUndoReason
	}
	return ToolResult{Kind: ResultApplied, Tool: name, Applied: &ref, Note: note}, nil
}

func receiptPayload(p gateway.Proposal) json.RawMessage {
	if p.Receipt == nil {
		return nil
	}
	return p.Receipt.Result.Payload
}

// refusedResult turns the add-on's own refusal, a missing permission and an
// unreachable resource into a result the model can act
// on. Every other error, an outage and an unknown outcome included, passes
// through unchanged.
func refusedResult(name string, err error) (ToolResult, error) {
	var limited *contract.ErrRateLimited
	var refusal *gateway.CredentialRefusal
	switch {
	case errors.As(err, &limited):
		return ToolResult{Kind: ResultError, Tool: name, Code: CodeRateLimited, Note: RateLimitedNote, RetryAfter: limited.Seconds()}, nil
	case errors.As(err, &refusal) && refusal.Code == gateway.CodeResourceNotAllowed:
		return ToolResult{Kind: ResultError, Tool: name, Code: CodeResourceNotAllowed, Note: ResourceNotAllowedNote, Allowed: refusal.Allowed}, nil
	case errors.As(err, &refusal) && refusal.Code == gateway.CodeWriteNotAllowed:
		return ToolResult{Kind: ResultError, Tool: name, Code: CodeWriteNotAllowed, Note: WriteNotAllowedNote}, nil
	case errors.As(err, &refusal) && refusal.Code == gateway.CodeSelectConnection:
		return ToolResult{Kind: ResultError, Tool: name, Code: CodeSelectConnection, Note: SelectConnectionNote}, nil
	case errors.As(err, &refusal) && refusal.Code == gateway.CodeNoCredential:
		return ToolResult{Kind: ResultError, Tool: name, Code: CodeNoCredential, Note: NoCredentialNote}, nil
	case errors.Is(err, gateway.ErrNotPermitted):
		return ToolResult{Kind: ResultError, Tool: name, Code: CodeNotPermitted, Note: NotPermittedNote}, nil
	case errors.Is(err, gateway.ErrNotSent):
		return ToolResult{Kind: ResultError, Tool: name, Code: CodeNotRunning, Note: NotRunningNote}, nil
	case errors.Is(err, gateway.ErrResourceUnreachable):
		return ToolResult{Kind: ResultError, Tool: name, Code: CodeResourceUnreachable, Note: ResourceUnreachableNote}, nil
	}
	var upstream *addonruntime.Refusal
	if errors.As(err, &upstream) && upstream.Upstream != 0 {
		msg := upstream.Hint
		if msg == "" {
			msg = refusalMessage(err)
		}
		return ToolResult{Kind: ResultError, Tool: name, Code: CodeUpstreamRefused, Note: UpstreamNote, Message: msg, Upstream: upstream.Upstream}, nil
	}
	if errors.Is(err, gateway.ErrInvalidRequest) || errors.Is(err, contract.ErrRefused) {
		res := ToolResult{Kind: ResultError, Tool: name, Code: CodeInvalidRequest, Note: InvalidNote, Message: refusalMessage(err)}
		if res.Message != "" {
			res.Note = ReasonedNote
		}
		return res, nil
	}
	return ToolResult{}, err
}

// refusalMessage is the reason in the add-on's refusal, unless it is only the
// class every refusal has.
func refusalMessage(err error) string {
	var r *addonruntime.Refusal
	if errors.As(err, &r) && r.Reason != "" && r.Reason != "invalid request" {
		return r.Reason
	}
	return ""
}

// requiresResource reports a tool whose schema requires the resource.
func requiresResource(def ToolDefinition) bool {
	props, _ := def.Parameters["properties"].(map[string]any)
	if _, ok := props[contract.ResourceArgument]; !ok {
		return false
	}
	req, _ := def.Parameters["required"].([]any)
	for _, r := range req {
		if s, _ := r.(string); s == contract.ResourceArgument {
			return true
		}
	}
	return false
}

// ValidConversationID reports whether id has the shape of a conversation
// identifier: the namespace of one user's selections.
func ValidConversationID(id string) bool { return toolargs.ValidConversationID(id) }

func readResult(name string, out contract.Result) (ToolResult, error) {
	if len(out.Outputs) > 0 {
		refs := make([]ArtifactRef, 0, len(out.Outputs))
		for _, o := range out.Outputs {
			refs = append(refs, ArtifactRef{ID: o.Handle, Name: o.Name, MediaType: o.MediaType, Size: o.Size})
		}
		return ToolResult{Kind: ResultArtifact, Tool: name, Artifacts: refs}, nil
	}
	if len(out.Payload) > MaxResultBytes {
		return ToolResult{}, ErrResultTooLarge
	}
	return ToolResult{Kind: ResultData, Tool: name, Result: out.Payload}, nil
}

// JSON is the form of a result in a tool message and in chat.
func (r ToolResult) JSON() string {
	b, err := json.Marshal(r)
	if err != nil {
		return `{"kind":"error"}`
	}
	return string(b)
}

// String describes a definition for logs.
func (d ToolDefinition) String() string { return fmt.Sprintf("%s (%s)", d.Name, d.Kind) }
