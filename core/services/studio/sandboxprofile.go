package studio

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/sandboxpolicy"
)

// Sandbox profiles. A commands add-on declares profiles in its manifest (the
// environments its sandboxes can have). The administrator's policy says who
// may use each and the quotas. A chat has a profile of its own choice, its
// agent may name one, and the policy has a default. The host decides which
// applies at every call and hands it to the add-on with the call (see
// contract.ExecutionCall); the add-on never trusts the model for it.

var (
	// ErrSandboxProfileUnknown means the add-on that runs the commands of the
	// chat has no such profile.
	ErrSandboxProfileUnknown = errors.New("studio: the sandbox has no such profile")
	// ErrSandboxProfileNotAllowed means the administrator's policy does not let
	// this user use the profile.
	ErrSandboxProfileNotAllowed = errors.New("studio: you may not use this sandbox profile")
	// ErrNoSandboxProfiles means no add-on that runs commands has profiles.
	ErrNoSandboxProfiles = errors.New("studio: no sandbox with profiles runs commands in this chat")
)

// The sources of the profile of a chat.
const (
	ProfileFromChat    = "chat"    // the person picked it
	ProfileFromAgent   = "agent"   // the saved agent of the chat names it
	ProfileFromDefault = "default" // the administrator's default, or media, or base
)

// ProfileImageState is where the image of a profile is on the host.
type ProfileImageState struct {
	// Phase is idle, pulling, ready, failed or absent (see the add-on).
	Phase   string
	Percent int
	Error   string
}

// SandboxProfileInfo is one profile as a page shows it.
type SandboxProfileInfo struct {
	ID, Title, Description string
	Tools                  []string
	CPUs, MemoryMiB        int
	DiskMiB                int
	// Allowed says whether the policy lets the user use the profile.
	Allowed     bool
	State       ProfileImageState
	HasPrevious bool
}

// SandboxProfileView is the sandbox profile of one chat, as its picker shows it.
type SandboxProfileView struct {
	// Applies is false when the commands of the chat run in no sandbox with
	// profiles (built-in bash, no add-on, or the host backend).
	Applies bool
	Addon   string
	// Profile is the one the next call runs; Selected the person's own choice
	// ("" when none); Source says where Profile comes from; Default is the
	// administrator's default.
	Profile, Selected, Source, Default string
	// Fallback says the next call cannot use Profile (its image is not
	// available) and runs the fallback profile; FallbackReason says why.
	Fallback       bool
	FallbackReason string
	// NewerImage says the sandbox that runs now was made from an older image.
	NewerImage bool
	Profiles   []SandboxProfileInfo
}

// profileCatalog is the commands add-on with profiles that serves a user.
type profileCatalog struct {
	Addon string
	Exec  *contract.ExecutionDescriptor
}

func (c profileCatalog) title(id string) string {
	if p, ok := c.Exec.ProfileByID(id); ok {
		return p.Title
	}
	return id
}

// profileCatalogOf returns the add-on that runs commands for the user, when it
// declares profiles.
func (s *Service) profileCatalogOf(ctx context.Context, user string) (profileCatalog, bool) {
	if s.d.AddonTools == nil {
		return profileCatalog{}, false
	}
	addon, err := s.servingRunner(ctx, user)
	if err != nil || addon == "" {
		return profileCatalog{}, false
	}
	defs, err := s.d.AddonTools.Tools(ctx, user)
	if err != nil {
		return profileCatalog{}, false
	}
	for _, d := range defs {
		if d.AddonID == addon && d.Shadows == commandTool && d.Execution != nil && len(d.Execution.Profiles) > 0 {
			return profileCatalog{Addon: addon, Exec: d.Execution}, true
		}
	}
	return profileCatalog{}, false
}

// sandboxPolicy is the administrator's policy now; open when there is none.
func (s *Service) sandboxPolicy(ctx context.Context) (sandboxpolicy.Policy, error) {
	if s.d.SandboxPolicy == nil {
		return sandboxpolicy.Policy{}, nil
	}
	return s.d.SandboxPolicy(ctx)
}

// profileRole is the role the policy sees for a user.
func (s *Service) profileRole(ctx context.Context, user string) string {
	if s.d.IsAdmin == nil || s.d.IsAdmin(ctx, user) {
		return sandboxpolicy.RoleAdmin
	}
	return sandboxpolicy.RoleUser
}

// chatForProfile is the chat whose choice applies: a sub-agent follows the
// chat that started it.
func (s *Service) chatForProfile(ctx context.Context, rec session.ChatRecord) session.ChatRecord {
	if rec.Kind == session.KindSubAgent && rec.ParentID != "" {
		if parent, err := s.d.Store.GetChat(ctx, rec.Principal, rec.ParentID); err == nil {
			return parent
		}
	}
	return rec
}

// resolveProfile returns the profile the next call of a chat runs and where it
// comes from: the person's pick, then the agent's, then the default, each only
// when the add-on has it and the policy lets the user use it. Base is the end
// of the line.
func (s *Service) resolveProfile(ctx context.Context, rec session.ChatRecord, cat profileCatalog, pol sandboxpolicy.Policy) (id, source string) {
	rec = s.chatForProfile(ctx, rec)
	role := s.profileRole(ctx, rec.Principal)
	usable := func(id string) bool {
		_, ok := cat.Exec.ProfileByID(id)
		return ok && pol.Allows(id, rec.Principal, role)
	}
	if id := rec.Egress.Profile; id != "" && usable(id) {
		return id, ProfileFromChat
	}
	if rec.AgentID != "" && s.d.Agents != nil {
		if ag, err := s.chatAgent(ctx, rec.Principal, rec.AgentID); err == nil && ag.SandboxProfile != "" && usable(ag.SandboxProfile) {
			return ag.SandboxProfile, ProfileFromAgent
		}
	}
	if d := pol.DefaultProfile(); usable(d) {
		return d, ProfileFromDefault
	}
	// The default is not declared by the add-on or the user may not use it.
	return contract.FallbackProfile, ProfileFromDefault
}

// effectiveDefault is the default profile of the policy when the add-on has
// it, else the fallback: the pickers must not name a profile that is not there.
func effectiveDefault(cat profileCatalog, pol sandboxpolicy.Policy) string {
	if _, ok := cat.Exec.ProfileByID(pol.DefaultProfile()); ok {
		return pol.DefaultProfile()
	}
	return contract.FallbackProfile
}

// profileExecution is the descriptor the model sees for a chat: the one the
// add-on declared with the note about the programs built from the profile the
// chat runs.
func profileExecution(e *contract.ExecutionDescriptor, id string) *contract.ExecutionDescriptor {
	p, ok := e.ProfileByID(id)
	if !ok {
		return e
	}
	out := *e
	out.Note = sandbox.ProfileNote(p, e.Profiles)
	return &out
}

// withSandboxProfile gives the commands tool of a chat the note and the
// description of the profile it runs, and makes every call carry the options
// the host decided: the profile, the pin to the previous image, the caps and
// the chat's work folder.
// The profile is chosen again at each call, so a switch applies to the next one.
func (s *Service) withSandboxProfile(ctx context.Context, a workspace.Actor, rec session.ChatRecord, d AddonTool, t session.Tool) session.Tool {
	id := contract.FallbackProfile
	// Without the policy the open default would let in a profile an
	// administrator closed, so a call that cannot read it runs base.
	if pol, err := s.sandboxPolicy(ctx); err == nil {
		id, _ = s.resolveProfile(ctx, rec, profileCatalog{Addon: d.AddonID, Exec: d.Execution}, pol)
	}
	if p, ok := d.Execution.ProfileByID(id); ok {
		t.Execution = profileExecution(d.Execution, id)
		t.Description += " " + sandbox.ProfileToolsSentence(p)
	}
	inner := t.Run
	t.Run = func(ctx context.Context, args json.RawMessage) (string, error) {
		opts := s.callOptions(ctx, a.Principal, a.ChatID, d)
		if prev, ok := contract.ExecutionCallFrom(ctx); ok && prev.ReadOnly {
			opts.ReadOnly = true // the check of a wait must not change files
		}
		// A check starts where the model's own commands start, but never
		// makes the folder.
		opts.WorkDir = s.CommandWorkDir(ctx, a, !opts.ReadOnly)
		return inner(contract.WithExecutionCall(ctx, opts), args)
	}
	return t
}

// withWorkDir makes every call of a commands tool without profiles carry the
// chat's work folder, so the shell starts there (see CommandWorkDir).
func (s *Service) withWorkDir(a workspace.Actor, t session.Tool) session.Tool {
	inner := t.Run
	t.Run = func(ctx context.Context, args json.RawMessage) (string, error) {
		opts, _ := contract.ExecutionCallFrom(ctx)
		opts.WorkDir = s.CommandWorkDir(ctx, a, !opts.ReadOnly)
		return inner(contract.WithExecutionCall(ctx, opts), args)
	}
	return t
}

// callOptions decides the options of one call of the commands tool of a chat.
func (s *Service) callOptions(ctx context.Context, user, chat string, d AddonTool) contract.ExecutionCall {
	rec, err := s.d.Store.GetChat(ctx, user, chat)
	pol, perr := s.sandboxPolicy(ctx)
	if err != nil || perr != nil {
		return contract.ExecutionCall{Profile: contract.FallbackProfile}
	}
	id, _ := s.resolveProfile(ctx, rec, profileCatalog{Addon: d.AddonID, Exec: d.Execution}, pol)
	return contract.ExecutionCall{Profile: id, Pin: pol.Pinned(id), MemoryMiB: pol.MemoryMiB, DiskMiB: pol.DiskMiB}
}

// addonImageStatus is what the add-on's status tool says about the images and
// the chat's sandbox.
type addonImageStatus struct {
	Profiles []struct {
		ID          string `json:"id"`
		Phase       string `json:"phase"`
		Percent     int    `json:"percent"`
		Error       string `json:"error"`
		HasPrevious bool   `json:"hasPrevious"`
	} `json:"profiles"`
	Chat *struct {
		Profile    string `json:"profile"`
		NewerImage bool   `json:"newerImage"`
	} `json:"chat"`
}

// imageStatus asks the add-on for the state of its images. An add-on that does
// not answer leaves the states unknown: the picker still works, without
// progress.
func (s *Service) imageStatus(ctx context.Context, user, conv string, cat profileCatalog) addonImageStatus {
	var out addonImageStatus
	args, _ := json.Marshal(map[string]string{"conversation": conv})
	text, err := s.d.AddonTools.Call(ctx, user, conv, roleToolName(cat.Addon, "status"), args)
	if err != nil {
		return out
	}
	// The broker wraps the payload of a read tool in a result object.
	var wrap struct {
		Result json.RawMessage `json:"result"`
	}
	raw := []byte(text)
	if json.Unmarshal(raw, &wrap) == nil && len(wrap.Result) > 0 {
		raw = wrap.Result
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func infoOf(cat profileCatalog, pol sandboxpolicy.Policy, user, role string, st addonImageStatus) []SandboxProfileInfo {
	out := make([]SandboxProfileInfo, 0, len(cat.Exec.Profiles))
	for _, p := range cat.Exec.Profiles {
		tools := make([]string, len(p.Tools))
		for i, t := range p.Tools {
			tools[i] = t.Text()
		}
		in := SandboxProfileInfo{ID: p.ID, Title: p.Title, Description: p.Description, Tools: tools,
			CPUs: p.CPUs, MemoryMiB: p.MemoryMiB, DiskMiB: p.DiskMiB, Allowed: pol.Allows(p.ID, user, role)}
		for _, s := range st.Profiles {
			if s.ID == p.ID {
				in.State = ProfileImageState{Phase: s.Phase, Percent: s.Percent, Error: s.Error}
				in.HasPrevious = s.HasPrevious
			}
		}
		out = append(out, in)
	}
	return out
}

// SandboxProfiles returns the sandbox profile of a chat for its owner. The chat
// of another user is session.ErrNotFound.
func (s *Service) SandboxProfiles(ctx context.Context, user, conv string) (SandboxProfileView, error) {
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return SandboxProfileView{}, err
	}
	cat, ok := s.profileCatalogOf(ctx, p)
	if !ok {
		return SandboxProfileView{}, nil
	}
	cmds, err := s.commandsFor(ctx, p, rec)
	if err != nil || cmds.State != CommandsAvailable || cmds.Provider != cat.Addon || cmds.Isolation != "isolated" {
		return SandboxProfileView{}, nil
	}
	pol, err := s.sandboxPolicy(ctx)
	if err != nil {
		return SandboxProfileView{}, err
	}
	st := s.imageStatus(ctx, p, conv, cat)
	id, source := s.resolveProfile(ctx, rec, cat, pol)
	role := s.profileRole(ctx, p)
	v := SandboxProfileView{Applies: true, Addon: cat.Addon, Profile: id, Source: source, Default: effectiveDefault(cat, pol),
		Selected: s.chatForProfile(ctx, rec).Egress.Profile, Profiles: infoOf(cat, pol, p, role, st)}
	if i := slices.IndexFunc(v.Profiles, func(i SandboxProfileInfo) bool { return i.ID == id }); i >= 0 {
		switch ph := v.Profiles[i].State; ph.Phase {
		case "failed", "absent":
			v.Fallback = true
			v.FallbackReason = ph.Error
			if ph.Phase == "absent" {
				v.FallbackReason = "this release has no image for it"
			}
		}
	}
	v.NewerImage = st.Chat != nil && st.Chat.Profile == id && st.Chat.NewerImage
	return v, nil
}

// SetSandboxProfile sets the profile of a chat. An empty profile goes back to
// the agent's or the default. The sandbox restarts on the next command: the
// files stay, running processes and installed packages are lost.
func (s *Service) SetSandboxProfile(ctx context.Context, user, conv, profile string) error {
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return err
	}
	if rec.Kind == session.KindSubAgent {
		return ErrSubAgentReadOnly
	}
	if profile != "" {
		cat, ok := s.profileCatalogOf(ctx, p)
		if !ok {
			return ErrNoSandboxProfiles
		}
		if _, known := cat.Exec.ProfileByID(profile); !known {
			return ErrSandboxProfileUnknown
		}
		pol, err := s.sandboxPolicy(ctx)
		if err != nil {
			return err
		}
		if !pol.Allows(profile, p, s.profileRole(ctx, p)) {
			return ErrSandboxProfileNotAllowed
		}
	}
	if err := s.d.Store.UpdateEgress(ctx, p, conv, func(st *egress.ChatState) error {
		st.Profile = profile
		return nil
	}); err != nil {
		return err
	}
	s.publishEgress(p, conv, "")
	return nil
}

// SandboxProfileChoices lists the profiles a user may pick for an agent or a
// chat, and the default. Empty when no add-on with profiles runs commands.
func (s *Service) SandboxProfileChoices(ctx context.Context, user string) (addon string, profiles []SandboxProfileInfo, def string, err error) {
	p := principal(user)
	cat, ok := s.profileCatalogOf(ctx, p)
	if !ok {
		return "", nil, "", nil
	}
	pol, err := s.sandboxPolicy(ctx)
	if err != nil {
		return "", nil, "", err
	}
	return cat.Addon, infoOf(cat, pol, p, s.profileRole(ctx, p), addonImageStatus{}), effectiveDefault(cat, pol), nil
}

// AdminSandboxProfiles is the administrator's view: the profiles with their
// limits and the state of their images, to set the policy against.
func (s *Service) AdminSandboxProfiles(ctx context.Context, user string) (addon string, profiles []SandboxProfileInfo, err error) {
	p := principal(user)
	cat, ok := s.profileCatalogOf(ctx, p)
	if !ok {
		return "", nil, nil
	}
	pol, err := s.sandboxPolicy(ctx)
	if err != nil {
		return "", nil, err
	}
	st := s.imageStatus(ctx, p, "", cat)
	return cat.Addon, infoOf(cat, pol, p, sandboxpolicy.RoleAdmin, st), nil
}

// sandboxHint returns the hint of load_skill for a chat: a skill that wants a
// sandbox profile other than the one the chat runs says so, and the model may
// offer the switch. It never switches. A chat whose commands run in no sandbox
// with profiles gets no hint.
func (s *Service) sandboxHint(a workspace.Actor) func(context.Context, skills.Skill) string {
	return func(ctx context.Context, sk skills.Skill) string {
		if sk.Sandbox == "" {
			return ""
		}
		rec, err := s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
		if err != nil {
			return ""
		}
		cat, ok := s.profileCatalogOf(ctx, a.Principal)
		if !ok {
			return ""
		}
		pol, err := s.sandboxPolicy(ctx)
		if err != nil {
			return ""
		}
		id, _ := s.resolveProfile(ctx, rec, cat, pol)
		known := make([]string, len(cat.Exec.Profiles))
		for i, p := range cat.Exec.Profiles {
			known[i] = p.ID
		}
		return skills.SandboxHint(sk.Sandbox, id, known)
	}
}
