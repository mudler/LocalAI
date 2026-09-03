// SPDX-License-Identifier: MIT

package routes

import (
	"github.com/mudler/LocalAI/core/application"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
)

// mcpAgentControl returns the port the MCP endpoints reach an agent worker
// through, or a nil interface in a deployment that has no agent workers to
// reach.
//
// One function for four route files, and it exists because of the trap rather
// than to save three lines. Every one of those endpoints decides between its
// local and its distributed path by asking whether this value is nil, and a nil
// *nodes.AgentControlClient assigned straight into the interface is NOT nil:
// the interface carries a type, so the check passes, the distributed path is
// taken, and every MCP request in a standalone deployment fails instead of
// using the in-process sessions it has. Returning the untyped nil explicitly is
// the only spelling that keeps that check meaning what its call sites think it
// means.
func mcpAgentControl(app *application.Application) mcpTools.AgentControl {
	return mcpAgentControlOf(app.Distributed())
}

// mcpAgentControlOf is the half of the rule above that a spec can reach.
//
// It is separate because an *application.Application carries its distributed
// services in an unexported field with no way in from outside the package, so
// the typed-nil conversion could not otherwise be asserted at all, and it is
// precisely the conversion that is easy to get wrong and silent when it is.
func mcpAgentControlOf(d *application.DistributedServices) mcpTools.AgentControl {
	if d == nil || d.AgentControl == nil {
		return nil
	}
	return d.AgentControl
}
