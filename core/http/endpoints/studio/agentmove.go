package studio

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
)

// MoveAgentProject moves a saved agent to another project, or out of one.
// @Summary Move a saved agent to another project, or out of its project.
// @Description The one way to change the project of an agent: a normal save (PUT /api/chats/agents/{id}) refuses a changed project. project_id is an active project of yours, or empty to leave the project. Into a project the agent gets the project's files folder (files_mode read or write, write by default) and the memory folder at read; the grant to write the project's memory is removed, and its waiting project notes are voided. Out of a project, folders (or whole_home true) are required, so the agent never gets all your files by accident. The agent's own notes start empty: the old ones are moved to Memory/Moved agent notes in your files, not deleted. Chats the agent already has keep their history but take no new turn (the answer is 409 agent_moved: start a new chat), and their loops end. The move is refused with 409 agent_move_blocked while a chat of the agent runs a turn, while the agent is shared and enters a project (revoke_shares true removes the grants), or while an enabled file trigger watches a folder outside the new folders (disable_triggers true switches it off). The answer also lists what the new project attaches (attached_new), what the old project attached and the new one does not (attached_old, these stop applying), and project_skills_switch, true when the skills of the old project stop and those of the new one start. With dry_run true nothing is written and the answer lists what the move touches, including the blockers.
// @Tags chats
// @Param id path string true "the agent's id"
// @Param request body schema.ChatsAgentMove true "the target project and the options"
// @Success 200 {object} schema.ChatsAgentMoveReport "What the move did, or would do"
// @Failure 400 {object} schema.ErrorResponse "The target or the folders are not valid, or the target is archived"
// @Failure 404 {object} schema.ErrorResponse "No such agent or project, or it is another user's"
// @Failure 409 {object} schema.ErrorResponse "The move is blocked (agent_move_blocked)"
// @Router /api/chats/agents/{id}/project [put]
func (h *Handlers) MoveAgentProject(c echo.Context) error {
	var req schema.ChatsAgentMove
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, "the body must be JSON")
	}
	folders := make([]studiosvc.AgentFolderWrite, 0, len(req.Folders))
	for _, f := range req.Folders {
		folders = append(folders, studiosvc.AgentFolderWrite{Path: f.Path, Mode: f.Mode})
	}
	rep, err := h.svc.MoveAgentProject(c.Request().Context(), userID(c), c.Param("id"), studiosvc.AgentMove{
		ProjectID: req.ProjectID, DryRun: req.DryRun, FilesMode: req.FilesMode, Folders: folders, WholeHome: req.WholeHome,
		RevokeShares: req.RevokeShares, DisableTriggers: req.DisableTriggers,
	})
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, moveReportOut(rep))
}

// nonNil is l, or an empty list for nil, so the answer has [] and not null.
func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func attachedOut(a studiosvc.AttachedTools) schema.ChatsAgentMoveAttached {
	return schema.ChatsAgentMoveAttached{Skills: nonNil(a.Skills), Addons: nonNil(a.Addons), Connectors: nonNil(a.Connectors),
		MCPServers: nonNil(a.MCPServers), Collections: nonNil(a.Collections)}
}

func moveReportOut(r studiosvc.AgentMoveReport) schema.ChatsAgentMoveReport {
	out := schema.ChatsAgentMoveReport{
		DryRun: r.DryRun, Moved: r.Moved, From: r.From, To: r.To, Chats: r.Chats, Loops: r.Loops, Shares: r.Shares, Proposals: r.Proposals,
		TriggersOutside: []schema.ChatsAgentMoveTrigger{}, MemoryGrantDropped: r.MemoryGrantDropped,
		DroppedTools: schema.ChatsAgentMoveTools{Skills: nonNil(r.DroppedTools.Skills), Addons: nonNil(r.DroppedTools.Addons),
			MCPServers: nonNil(r.DroppedTools.MCPServers), Collections: nonNil(r.DroppedTools.Collections)},
		Blockers: nonNil(r.Blockers), Warnings: r.Warnings,
		AttachedNew: attachedOut(r.AttachedNew), AttachedOld: attachedOut(r.AttachedOld), ProjectSkillsSwitch: r.ProjectSkillsSwitch,
	}
	for _, t := range r.TriggersOutside {
		out.TriggersOutside = append(out.TriggersOutside, schema.ChatsAgentMoveTrigger{ID: t.ID, Name: t.Name})
	}
	return out
}
