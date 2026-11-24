package studio

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/sandboxpolicy"
)

// The errors of the profiles live here, not in the table of studio.go: that
// file writes no add-on name, and these texts name the environment.
func init() {
	chatRules = append(chatRules,
		fixed(http.StatusBadRequest, "sandbox_profile_unknown", "the sandbox has no such profile", studiosvc.ErrSandboxProfileUnknown),
		fixed(http.StatusForbidden, "sandbox_profile_not_allowed", "an administrator did not give you this sandbox profile", studiosvc.ErrSandboxProfileNotAllowed),
		fixed(http.StatusConflict, "no_sandbox_profiles", "the commands of this chat do not run in a sandbox with profiles", studiosvc.ErrNoSandboxProfiles),
	)
}

func profilesJSON(in []studiosvc.SandboxProfileInfo) []schema.ChatsSandboxProfile {
	out := make([]schema.ChatsSandboxProfile, 0, len(in))
	for _, p := range in {
		tools := p.Tools
		if tools == nil {
			tools = []string{}
		}
		out = append(out, schema.ChatsSandboxProfile{ID: p.ID, Title: p.Title, Description: p.Description, Tools: tools,
			CPUs: p.CPUs, MemoryMiB: p.MemoryMiB, DiskMiB: p.DiskMiB, Allowed: p.Allowed, HasPrevious: p.HasPrevious,
			State: schema.ChatsSandboxProfileState{Phase: stateOf(p.State.Phase), Percent: p.State.Percent, Error: p.State.Error}})
	}
	return out
}

// stateOf names the phase of an add-on that did not report one.
func stateOf(phase string) string {
	if phase == "" {
		return "idle"
	}
	return phase
}

func sandboxViewJSON(v studiosvc.SandboxProfileView) schema.ChatsSandboxProfileView {
	return schema.ChatsSandboxProfileView{Applies: v.Applies, Addon: v.Addon, Profile: v.Profile, Selected: v.Selected,
		Source: v.Source, Default: v.Default, Fallback: v.Fallback, FallbackReason: v.FallbackReason, NewerImage: v.NewerImage,
		Profiles: profilesJSON(v.Profiles)}
}

// GetSandboxProfile returns the sandbox profile of a chat.
// @Summary Read the sandbox profile of a chat.
// @Description The profile the next command of the chat runs (the one you picked, else the saved agent's, else the administrator's default, else media, else base), where it comes from, the profiles the Sandbox add-on offers with their tools and limits, whether the administrator's policy lets you use each one (allowed), and where the image of each is on the host (state: pulling shows percent). fallback is true when the next command cannot use the profile because its image failed or is missing, and runs base. newer_image is true when the running sandbox is older than the current image. applies is false when the commands of the chat run in no sandbox with profiles. Another user's chat is 404.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsSandboxProfileView "The profile of the chat"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/sandbox-profile [get]
func (h *Handlers) GetSandboxProfile(c echo.Context) error {
	v, err := h.svc.SandboxProfiles(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, sandboxViewJSON(v))
}

// PutSandboxProfile sets the sandbox profile of a chat.
// @Summary Set the sandbox profile of a chat.
// @Description Switches the environment the commands of the chat run in. The sandbox restarts at the next command: your files stay, running processes and installed packages are lost. An empty profile goes back to the saved agent's or the default profile. A profile the Sandbox does not have is 400 sandbox_profile_unknown; one the administrator did not give you is 403 sandbox_profile_not_allowed. A sub-agent follows its parent: 409.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsSandboxProfileSet true "the profile"
// @Success 200 {object} schema.ChatsSandboxProfileView "The profile of the chat"
// @Failure 400 {object} schema.ErrorResponse "No such profile"
// @Failure 403 {object} schema.ErrorResponse "The administrator did not give you this profile"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/sandbox-profile [put]
func (h *Handlers) PutSandboxProfile(c echo.Context) error {
	var in schema.ChatsSandboxProfileSet
	if err := c.Bind(&in); err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, "the body must be {profile}")
	}
	ctx := c.Request().Context()
	if err := h.svc.SetSandboxProfile(ctx, userID(c), c.Param("id"), in.Profile); err != nil {
		return failFor(c, err)
	}
	v, err := h.svc.SandboxProfiles(ctx, userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, sandboxViewJSON(v))
}

// SandboxProfileChoices lists the profiles for an agent.
// @Summary List the sandbox profiles you may pick.
// @Description The profiles of the Sandbox add-on that runs your commands, whether you may use each one, and the administrator's default. Empty when no add-on with profiles runs commands. Use it for the sandbox_profile of a saved agent.
// @Tags chats
// @Success 200 {object} schema.ChatsSandboxProfileChoices "The profiles"
// @Router /api/chats/sandbox-profiles [get]
func (h *Handlers) SandboxProfileChoices(c echo.Context) error {
	addon, list, def, err := h.svc.SandboxProfileChoices(c.Request().Context(), userID(c))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsSandboxProfileChoices{Addon: addon, Default: def, Profiles: profilesJSON(list)})
}

// AdminSandboxProfiles is the administrator's view of the profiles.
// @Summary Read the sandbox profiles (administrators).
// @Description The profiles of the Sandbox add-on with their limits, tools and the state of their images (has_previous says an older image is kept, so a profile can be pinned to it), and the roles a policy can name. The policy is the owner host setting sandbox_profiles (default, access by role and user, memory_mib and disk_mib quota, pin_previous); change it with the add-on host settings.
// @Tags chats
// @Success 200 {object} schema.ChatsSandboxProfilesAdmin "The profiles"
// @Router /api/chats/admin/sandbox-profiles [get]
func (h *Handlers) AdminSandboxProfiles(c echo.Context) error {
	addon, list, err := h.svc.AdminSandboxProfiles(c.Request().Context(), userID(c))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsSandboxProfilesAdmin{Addon: addon, Roles: []string{sandboxpolicy.RoleAdmin, sandboxpolicy.RoleUser}, Profiles: profilesJSON(list)})
}
