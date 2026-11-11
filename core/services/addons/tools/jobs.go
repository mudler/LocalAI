// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/toolargs"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ErrNoJobs says the add-on declares no jobs tool: it is older than the job
// tool of this host, or it does not run background jobs. Nothing was sent.
var ErrNoJobs = errors.New("the add-on cannot run background jobs")

// ExecuteJobs runs one call of the jobs tool of an add-on (the tool that
// ExecutionProvider.Jobs binds) for a chat, as the actor. The host's own job
// tool calls it; no model names the tool, which is page-only. The call is
// bound to the chat as ExecuteChat binds it, carries the host's
// ExecutionCall from ctx, and is never repeated. The answer is checked
// against the arguments before it is returned: an add-on's answer is not
// trusted for sizes, offsets or job IDs.
func (t *ToolAdapter) ExecuteJobs(ctx context.Context, actor contract.Actor, call ChatCall, addonID string, args contract.JobArgs) (contract.JobReply, error) {
	b := hostops.Binding{Principal: call.principal, ChatID: call.conversation}
	if !t.actions || !b.Valid() || !ValidConversationID(call.conversation) || args.Validate() != nil {
		return contract.JobReply{}, ErrInvalidArguments
	}
	list, err := t.visible(ctx, actor)
	if err != nil {
		return contract.JobReply{}, err
	}
	i := slices.IndexFunc(list, func(a apps.App) bool { return a.ID == addonID })
	if i < 0 {
		return contract.JobReply{}, ErrUnknownTool
	}
	app := list[i]
	if app.JobsTool == "" {
		return contract.JobReply{}, ErrNoJobs
	}
	j := slices.IndexFunc(app.ToolDefinitions, func(d contract.ToolDefinition) bool { return d.Name == app.JobsTool })
	if j < 0 || !slices.Contains(app.Tools, app.JobsTool) {
		return contract.JobReply{}, ErrNoJobs
	}
	def := app.ToolDefinitions[j]
	if def.Kind != contract.ToolKindAction || !def.UIOnly || def.Shadows != "" {
		return contract.JobReply{}, ErrNoJobs
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return contract.JobReply{}, ErrInvalidArguments
	}
	_, body, err := toolargs.BindDefinition(def, raw)
	if err != nil {
		return contract.JobReply{}, err
	}
	body = withExecutionCall(ctx, body)
	ab, ok := t.backend.(ActBackend)
	if !ok {
		return contract.JobReply{}, gateway.ErrUnavailable
	}
	op, err := t.newID()
	if err != nil {
		return contract.JobReply{}, gateway.ErrUnavailable
	}
	request := contract.Request{AddonID: app.ID, ResourceID: toolargs.ActionResourceID(call.conversation), OperationID: op,
		Generation: app.Generation, Action: "act", Arguments: body}
	out, err := ab.Act(hostops.WithBinding(ctx, b), actor, request)
	if err != nil {
		return contract.JobReply{}, err
	}
	if len(out.Payload) > MaxResultBytes {
		return contract.JobReply{}, ErrResultTooLarge
	}
	var reply contract.JobReply
	dec := json.NewDecoder(bytes.NewReader(out.Payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&reply); err != nil {
		return contract.JobReply{}, contract.ErrJobReply
	}
	if err := reply.Validate(args); err != nil {
		return contract.JobReply{}, err
	}
	return reply, nil
}
