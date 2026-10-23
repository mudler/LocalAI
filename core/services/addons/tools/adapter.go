// SPDX-License-Identifier: MIT

// Package tools adapts the tools of installed add-ons to model tool calls.
//
// The adapter is the one place where a model-facing consumer (chat, an
// agent, a scheduled job) meets add-on tools. It never decides authority:
//
//   - discovery shows only what the owner lists for the authenticated actor:
//     enabled, running add-ons with a tool definition in their verified
//     manifest;
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
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
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
}

// ResultKind says what a ToolResult carries.
type ResultKind string

const (
	// ResultData is a read result.
	ResultData ResultKind = "result"
	// ResultProposal is a reference to a stored proposal. Nothing is applied.
	ResultProposal ResultKind = "proposal"
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
	Artifacts []ArtifactRef   `json:"artifacts,omitempty"`
	// Code says what the person is asked to do, for a ResultError.
	Code string `json:"code,omitempty"`
	// Note is host text for the model: a proposal is not applied.
	Note string `json:"note,omitempty"`
}

// ProposalNote is what the model is told after a proposal.
const ProposalNote = "Nothing has been changed. A person must review and confirm this proposal in the approval card. Do not say the change was made."

// ToolAdapter exposes add-on tools to model consumers.
type ToolAdapter struct {
	backend Backend
	newID   func() (string, error)
}

// NewToolAdapter returns an adapter over the owner.
func NewToolAdapter(b Backend) *ToolAdapter {
	return &ToolAdapter{backend: b, newID: randomOperationID}
}

func randomOperationID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "t" + hex.EncodeToString(raw[:]), nil
}

var toolName = regexp.MustCompile(`^addon__([a-z][a-z0-9-]{0,63})__([a-z][a-z0-9-]{0,63})$`)

// ToolName is the function name of an add-on tool.
func ToolName(addonID, tool string) string { return Prefix + addonID + "__" + tool }

// ParseToolName splits a namespaced name. It accepts nothing else.
func ParseToolName(name string) (addonID, tool string, ok bool) {
	m := toolName.FindStringSubmatch(name)
	if m == nil || len(name) > MaxNameLength {
		return "", "", false
	}
	return m[1], m[2], true
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
		if a.Enabled && a.State == "running" && a.Generation > 0 && len(a.ToolDefinitions) > 0 {
			out = append(out, a)
		}
	}
	return out, nil
}

func definition(a apps.App, d contract.ToolDefinition) (ToolDefinition, bool) {
	name := ToolName(a.ID, d.Name)
	if _, _, ok := ParseToolName(name); !ok || !slices.Contains(a.Tools, d.Name) {
		return ToolDefinition{}, false
	}
	// The owner validated the manifest at install; it is checked again here
	// because a schema is what a model is told to obey.
	if contract.ValidateToolSchema(d.InputSchema, d.Kind) != nil {
		return ToolDefinition{}, false
	}
	var params map[string]any
	if json.Unmarshal(d.InputSchema, &params) != nil {
		return ToolDefinition{}, false
	}
	return ToolDefinition{Name: name, AddonID: a.ID, Tool: d.Name, Kind: d.Kind, Description: d.Description, Parameters: params}, true
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
		for _, d := range a.ToolDefinitions {
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

// ExecuteIn runs one tool call of a conversation. The conversation ID comes
// from the consumer's authenticated request, never from the arguments: it is
// used only when the call names no resource, to resolve the conversation's
// selection at the owner. An empty ID means the consumer has no conversation.
func (t *ToolAdapter) ExecuteIn(ctx context.Context, actor contract.Actor, conversationID, name string, arguments json.RawMessage) (ToolResult, error) {
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
		for _, d := range a.ToolDefinitions {
			if d.Name != tool {
				continue
			}
			if x, ok := definition(a, d); ok {
				app, def, found = a, x, true
			}
		}
	}
	if !found {
		return ToolResult{}, ErrUnknownTool
	}
	resource, body, err := toolargs.Bind(def.Tool, def.Parameters, arguments, conversationID != "")
	if err != nil {
		return ToolResult{}, err
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
			// unknown resource, and an outage stays an outage.
			return ToolResult{}, err
		case !toolargs.ValidResourceID(id):
			return ToolResult{}, gateway.ErrForbidden
		}
		resource = id
	}
	op, err := t.newID()
	if err != nil {
		return ToolResult{}, gateway.ErrUnavailable
	}
	request := contract.Request{AddonID: app.ID, ResourceID: resource, OperationID: op, Generation: app.Generation, Arguments: body}
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
	ref := ProposalRef{ID: p.ID, AddonID: p.AddonID, ResourceID: p.ResourceID, Action: p.Action, Digest: p.Digest,
		State: string(p.State), Count: p.Effects.Count, ExpiresAt: p.ExpiresAt}
	if len(p.Effects.Sample) <= MaxSummaryBytes {
		ref.Summary = p.Effects.Sample
	}
	return ToolResult{Kind: ResultProposal, Tool: name, Proposal: &ref, Note: ProposalNote}, nil
}

// refusedResult turns the add-on's own refusal into a result the model can act
// on. Every other error, an outage and an unknown outcome included, passes
// through unchanged.
func refusedResult(name string, err error) (ToolResult, error) {
	if errors.Is(err, gateway.ErrInvalidRequest) || errors.Is(err, contract.ErrRefused) {
		return ToolResult{Kind: ResultError, Tool: name, Code: CodeInvalidRequest, Note: InvalidNote}, nil
	}
	return ToolResult{}, err
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
