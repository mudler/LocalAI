// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"sort"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/egress"
)

// A saved agent can be shared with other people (share kind "agent"). The
// grant gives the definition only: a person who uses a shared agent runs it
// as themselves, with their own chats, memory, files, folders, add-ons,
// collections and connections. Only the owner edits, deletes, exports or
// shares it, and the owner's triggers, tasks and connectors stay the owner's.
//
// The grants live in the sharing policy of the add-on owner. AgentShares is
// the seam to it, so this package decides nothing about who holds a grant.

// AgentShareKind is the share kind of saved agents.
const AgentShareKind = "agent"

// The roles on a shared agent. Owner is implicit and never granted.
const (
	AgentRoleOwner = "owner"
	// AgentRoleUse runs the agent in your own chats and shows its name, its
	// description and what it needs.
	AgentRoleUse = "use"
	// AgentRoleView is use plus the full definition: instructions, tools and
	// references.
	AgentRoleView = "viewer"
)

var (
	// ErrAgentNotOwner means the caller may use a shared agent but the action
	// is the owner's alone: edit, delete, export, share, or a trigger, a task
	// or a connector on it.
	ErrAgentNotOwner = errors.New("studio: only the owner of this agent can do this")
	// ErrAgentNotShared means a chat names an agent of another person that is
	// no longer shared with its owner. The chat stays readable; it takes no
	// new turn with that agent.
	ErrAgentNotShared = errors.New("studio: this agent is no longer shared with you")
	// ErrAgentSharingOff means agents cannot be shared on this server: it has
	// no named accounts, or the sharing policy does not run in this process.
	ErrAgentSharingOff = errors.New("studio: agents cannot be shared on this server")
)

// AgentShareView is one sharing record as the caller sees it.
type AgentShareView struct {
	Ref string
	// Role is AgentRoleOwner, AgentRoleUse or AgentRoleView.
	Role string
	// Everyone says the role comes from the grant to every account.
	Everyone bool
	// OwnerID is the immutable owner of the record.
	OwnerID string
	// SharedWith is the number of grants, for the owner only.
	SharedWith int
}

// AgentShares is the seam to the sharing policy for the kind "agent". Every
// call is made with the caller's own identity; the policy checks the account
// and the agents feature on each one. An implementation that cannot decide
// fails closed.
type AgentShares interface {
	// Available says whether agents can be shared now.
	Available() bool
	// Create makes a new sharing record owned by user and returns its ID.
	Create(ctx context.Context, user string) (string, error)
	// Role returns the caller's view of one record. A record the caller has
	// no role on is an error.
	Role(ctx context.Context, user, ref string) (AgentShareView, error)
	// Listing returns every record the caller owns or holds a grant on.
	Listing(ctx context.Context, user string) ([]AgentShareView, error)
	// Admit runs run while the caller may run the agent. A lowering or a
	// revocation of the grant cancels the context given to run and waits for
	// run to return.
	Admit(ctx context.Context, user, ref string, run func(context.Context) error) error
	// RevokeAll removes every grant of a record of the owner.
	RevokeAll(ctx context.Context, user, ref string) error
	// AddonResourceReadable says whether the caller may read an add-on
	// resource, for the list of what a shared agent needs.
	AddonResourceReadable(ctx context.Context, user, addonID, resourceID string) bool
}

// AgentAccess is how the caller holds an agent.
type AgentAccess struct {
	Role string
	// OwnerName is the owner's display name, for an agent shared with the
	// caller only. The owner's account ID is never shown to a grantee.
	OwnerName string
	Everyone  bool
	// SharedWith is the number of grants of an agent the caller owns.
	SharedWith int
	// ShareRef is the sharing record of an agent the caller owns, empty when
	// it was never shared.
	ShareRef string
}

// Owner says the caller owns the agent.
func (a AgentAccess) Owner() bool { return a.Role == AgentRoleOwner }

// AgentEntry is one agent of a listing with the caller's access.
type AgentEntry struct {
	Agent  agents.Agent
	Access AgentAccess
}

func (s *Service) shares() AgentShares {
	if s.d.AgentShares == nil || !s.d.AgentShares.Available() {
		return nil
	}
	return s.d.AgentShares
}

// AgentSharingAvailable says whether the caller's agents can be shared here.
func (s *Service) AgentSharingAvailable() bool { return s.shares() != nil && s.d.Agents != nil }

// sharedAgent returns an agent of another person that is shared with p, and
// the view of its record. Any other agent, an unknown one included, is
// agents.ErrNotFound: a caller cannot tell an agent they may not see from
// one that does not exist.
func (s *Service) sharedAgent(ctx context.Context, p, id string) (agents.Agent, AgentShareView, error) {
	sh := s.shares()
	if sh == nil || s.d.Agents == nil || id == "" {
		return agents.Agent{}, AgentShareView{}, agents.ErrNotFound
	}
	ag, err := s.d.Agents.GetShared(ctx, id)
	if err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			return agents.Agent{}, AgentShareView{}, agents.ErrNotFound
		}
		return agents.Agent{}, AgentShareView{}, err
	}
	if ag.Principal == p {
		return agents.Agent{}, AgentShareView{}, agents.ErrNotFound
	}
	v, err := sh.Role(ctx, p, ag.ShareRef)
	if err != nil || (v.Role != AgentRoleUse && v.Role != AgentRoleView) {
		return agents.Agent{}, AgentShareView{}, agents.ErrNotFound
	}
	return ag, v, nil
}

// SharedAgent returns an agent the user owns or that is shared with them,
// with the user's access. It is the lookup of the agent page and of starting
// a chat; every edit keeps the owner filter.
func (s *Service) SharedAgent(ctx context.Context, user, id string) (agents.Agent, AgentAccess, error) {
	if s.d.Agents == nil {
		return agents.Agent{}, AgentAccess{}, ErrAgentsOff
	}
	p := principal(user)
	ag, err := s.d.Agents.Get(ctx, p, id)
	if err == nil {
		acc := AgentAccess{Role: AgentRoleOwner, ShareRef: ag.ShareRef}
		if sh := s.shares(); sh != nil && ag.ShareRef != "" {
			if v, verr := sh.Role(ctx, p, ag.ShareRef); verr == nil {
				acc.SharedWith = v.SharedWith
			}
		}
		return ag, acc, nil
	}
	if !errors.Is(err, agents.ErrNotFound) {
		return agents.Agent{}, AgentAccess{}, err
	}
	ag, v, err := s.sharedAgent(ctx, p, id)
	if err != nil {
		return agents.Agent{}, AgentAccess{}, err
	}
	return ag, AgentAccess{Role: v.Role, Everyone: v.Everyone, OwnerName: s.displayName(ctx, ag.Principal)}, nil
}

// chatAgent is the lookup of a chat's agent: the agent of the chat's owner p,
// or an agent shared with p. The definition is the owner's; everything the
// chat then resolves (files, add-ons, collections, connections, grants) is
// resolved for p.
func (s *Service) chatAgent(ctx context.Context, p, id string) (agents.Agent, error) {
	if s.d.Agents == nil {
		return agents.Agent{}, agents.ErrNotFound
	}
	ag, err := s.d.Agents.Get(ctx, p, id)
	if !errors.Is(err, agents.ErrNotFound) {
		return ag, err
	}
	ag, _, err = s.sharedAgent(ctx, p, id)
	return ag, err
}

// ownAgent is Agent for the actions that are the owner's alone. An agent
// that is shared with the user, and not theirs, is ErrAgentNotOwner, so the
// person learns why; any other missing agent stays agents.ErrNotFound.
func (s *Service) ownAgent(ctx context.Context, user, id string) (agents.Agent, error) {
	ag, err := s.Agent(ctx, user, id)
	if errors.Is(err, agents.ErrNotFound) {
		if _, _, serr := s.sharedAgent(ctx, principal(user), id); serr == nil {
			return agents.Agent{}, ErrAgentNotOwner
		}
	}
	return ag, err
}

// ownerOnly maps the not-found answer of an owner-only action to
// ErrAgentNotOwner when the agent is shared with the user.
func (s *Service) ownerOnly(ctx context.Context, user, id string, err error) error {
	if errors.Is(err, agents.ErrNotFound) {
		if _, _, serr := s.sharedAgent(ctx, principal(user), id); serr == nil {
			return ErrAgentNotOwner
		}
	}
	return err
}

// displayName is the name of an account as other people see it, never its
// ID: a person without a name shows as empty.
func (s *Service) displayName(ctx context.Context, id string) string {
	if s.d.Users == nil {
		return ""
	}
	u, err := s.d.Users(ctx, id)
	if err != nil || u == nil {
		return ""
	}
	return u.Name
}

// AgentsWithShared lists the agents of the user, then the agents shared with
// them, each with the user's access. Own agents come first, sorted by name;
// shared ones follow, sorted by name.
func (s *Service) AgentsWithShared(ctx context.Context, user string) ([]AgentEntry, error) {
	own, err := s.Agents(ctx, user)
	if err != nil {
		return nil, err
	}
	p := principal(user)
	out := make([]AgentEntry, 0, len(own))
	sh := s.shares()
	var views []AgentShareView
	if sh != nil {
		// The listing is a convenience on top of the user's own agents: a
		// policy that cannot answer leaves the own list as it is.
		if views, err = sh.Listing(ctx, p); err != nil {
			s.log.Warn("studio: could not list the shared agents", "error", err)
			views = nil
		}
	}
	byRef := map[string]AgentShareView{}
	for _, v := range views {
		byRef[v.Ref] = v
	}
	for _, a := range own {
		acc := AgentAccess{Role: AgentRoleOwner, ShareRef: a.ShareRef}
		if v, ok := byRef[a.ShareRef]; ok && a.ShareRef != "" && v.Role == AgentRoleOwner {
			acc.SharedWith = v.SharedWith
		}
		out = append(out, AgentEntry{Agent: a, Access: acc})
	}
	var refs []string
	for _, v := range views {
		if v.Role == AgentRoleUse || v.Role == AgentRoleView {
			refs = append(refs, v.Ref)
		}
	}
	if len(refs) == 0 {
		return out, nil
	}
	list, err := s.d.Agents.ListShared(ctx, refs)
	if err != nil {
		s.log.Warn("studio: could not read the shared agents", "error", err)
		return out, nil
	}
	names := map[string]string{}
	var shared []AgentEntry
	for _, a := range list {
		v, ok := byRef[a.ShareRef]
		// The record's owner must be the agent's owner: a record that names
		// someone else never lends its grants to this agent.
		if !ok || a.Principal == p || a.Principal != v.OwnerID {
			continue
		}
		if _, seen := names[a.Principal]; !seen {
			names[a.Principal] = s.displayName(ctx, a.Principal)
		}
		shared = append(shared, AgentEntry{Agent: a, Access: AgentAccess{Role: v.Role, Everyone: v.Everyone, OwnerName: names[a.Principal]}})
	}
	sort.SliceStable(shared, func(i, j int) bool {
		return agents.NameKey(shared[i].Agent.Name) < agents.NameKey(shared[j].Agent.Name)
	})
	return append(out, shared...), nil
}

// agentShareStripes is the number of locks that keep two first shares of one
// agent from making two records in one process. The store's first-wins update
// keeps a cluster right; the locks only save a record that would go unused.
// The set is fixed, so it never grows with the number of agents: two agents
// may share a lock, which only makes their first shares wait for each other.
const agentShareStripes = 64

var agentShareLocks [agentShareStripes]sync.Mutex

// agentShareLock is the lock of the first share of the agent id.
func agentShareLock(id string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return &agentShareLocks[h.Sum32()%agentShareStripes]
}

// AgentShareRef returns the sharing record of an agent of the user, making
// it on the first share. Only the owner may call it.
func (s *Service) AgentShareRef(ctx context.Context, user, id string) (string, error) {
	sh := s.shares()
	if sh == nil || s.d.Agents == nil {
		return "", ErrAgentSharingOff
	}
	p := principal(user)
	ag, err := s.ownAgent(ctx, user, id)
	if err != nil {
		return "", err
	}
	if ag.ShareRef != "" {
		return ag.ShareRef, nil
	}
	mu := agentShareLock(id)
	mu.Lock()
	defer mu.Unlock()
	if ag, err = s.d.Agents.Get(ctx, p, id); err != nil {
		return "", err
	}
	if ag.ShareRef != "" {
		return ag.ShareRef, nil
	}
	ref, err := sh.Create(ctx, p)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrAgentSharingOff, err)
	}
	return s.d.Agents.SetShareRef(ctx, p, id, ref)
}

// revokeAgentShares removes the grants of a deleted agent, so nothing stays
// shared under its record and running chats of other people stop now.
func (s *Service) revokeAgentShares(ctx context.Context, p string, ag agents.Agent) {
	sh := s.shares()
	if sh == nil || ag.ShareRef == "" {
		return
	}
	if err := sh.RevokeAll(ctx, p, ag.ShareRef); err != nil {
		s.log.Warn("studio: could not remove the grants of a deleted agent", "agent", ag.ID, "error", err)
	}
}

// admitAgentTurn admits a turn of a chat whose agent is shared with its
// owner p. It returns the release to call when the turn ends, and the check
// to call once the turn is registered: a lowering or a revocation that came
// before the turn could be stopped stops it then. A chat with the user's own
// agent, with no agent or with an agent that was deleted needs no admission.
// A chat whose agent is no longer shared is refused with ErrAgentNotShared.
// A lowering or a revocation during the turn stops it.
func (s *Service) admitAgentTurn(ctx context.Context, p, conv string) (release, recheck func(), err error) {
	noop := func() {}
	release, recheck, err = s.admitShared(ctx, p, conv)
	if release == nil {
		release = noop
	}
	if recheck == nil {
		recheck = noop
	}
	return release, recheck, err
}

func (s *Service) admitShared(ctx context.Context, p, conv string) (func(), func(), error) {
	noop := func() {}
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil || rec.AgentID == "" || s.d.Agents == nil {
		return noop, noop, nil
	}
	if _, err := s.d.Agents.Get(ctx, p, rec.AgentID); err == nil || !errors.Is(err, agents.ErrNotFound) {
		return noop, noop, nil
	}
	ag, err := s.d.Agents.GetShared(ctx, rec.AgentID)
	if errors.Is(err, agents.ErrNotFound) {
		// A deleted agent: the turn runs as for any chat whose agent is gone.
		return noop, noop, nil
	}
	if err != nil {
		return noop, noop, err
	}
	sh := s.shares()
	if sh == nil || ag.Principal == p {
		return noop, noop, ErrAgentNotShared
	}
	admitted := make(chan error, 1)
	done := make(chan struct{})
	// lost is closed when the grant no longer allows the run.
	lost := make(chan struct{})
	go func() { // #nosec G118 -- the admission lives as long as the turn and ends with release
		err := sh.Admit(context.Background(), p, ag.ShareRef, func(run context.Context) error {
			admitted <- nil
			select {
			case <-done:
			case <-run.Done():
				close(lost)
				// The grant was lowered or revoked: stop the turn and hold
				// the admission until it has ended, so the owner's change
				// answers only once nothing runs under the old grant.
				if err := s.stopLocal(context.Background(), p, conv); err != nil {
					s.log.Warn("studio: could not stop a turn of an agent that is no longer shared", "chat", conv, "error", err)
				}
				<-done
			}
			return nil
		})
		if err != nil {
			admitted <- err
		}
	}()
	if err := <-admitted; err != nil {
		return noop, noop, ErrAgentNotShared
	}
	var once sync.Once
	recheck := func() {
		select {
		case <-lost:
			if err := s.stopLocal(context.Background(), p, conv); err != nil {
				s.log.Warn("studio: could not stop a turn of an agent that is no longer shared", "chat", conv, "error", err)
			}
		default:
		}
	}
	return func() { once.Do(func() { close(done) }) }, recheck, nil
}

// AgentReference is one thing a saved agent uses.
type AgentReference struct {
	// Kind is addon, collection, skill, folder, connection, addon-resource,
	// mcp or builtin.
	Kind string
	// ID names the thing for its kind: an add-on ID, a collection or skill
	// name, a folder node, a binding ID, a resource ID, a server name or a
	// grant name. It is shown to the owner only.
	ID string
	// Label is what a person reads.
	Label string
	// AddonID is the add-on of a connection or an add-on resource.
	AddonID string
	// Mode is read or write for a folder or an add-on resource.
	Mode string
	// Share says how the owner can share the thing with the same person,
	// nil when it cannot be shared by a grant.
	Share *ReferenceShare
}

// ReferenceShare is the grant a reference can get.
type ReferenceShare struct {
	// Kind is the share kind (collection, connection) or "addon" for an
	// add-on resource, shared through the add-on routes.
	Kind string
	// ResourceID is the ID of the thing in its sharing API.
	ResourceID string
	// Options are the choices the owner has, out of view and use. none is
	// always possible.
	Options []string
	// Roles maps each option to the role it grants on the thing, one of the
	// roles its kind declares.
	Roles map[string]string
}

// The choices of a reference in the Share dialog of an agent.
const (
	ReferenceChoiceView = "view"
	ReferenceChoiceUse  = "use"
)

// ReferenceShareTarget is a reference of a share kind as its sharing API
// knows it: its ID there and the roles its kind declares as grantable.
type ReferenceShareTarget struct {
	ResourceID string
	Grantable  []string
}

// ReferenceChoices maps the roles a kind grants to the choices the owner gets
// for a reference of that kind. view is the role viewer. use is the role use;
// for a thing the agent writes (write is true: an add-on resource in write
// mode) it is the role editor when the kind has no use. A kind that grants
// neither offers nothing. A collection grants viewer and editor: the agent
// only searches it, so it offers view alone, and editor stays a choice of the
// collection's own Share dialog.
func ReferenceChoices(grantable []string, write bool) (options []string, roles map[string]string) {
	roles = map[string]string{}
	if slices.Contains(grantable, "viewer") {
		options = append(options, ReferenceChoiceView)
		roles[ReferenceChoiceView] = "viewer"
	}
	switch {
	case slices.Contains(grantable, "use"):
		options = append(options, ReferenceChoiceUse)
		roles[ReferenceChoiceUse] = "use"
	case write && slices.Contains(grantable, "editor"):
		options = append(options, ReferenceChoiceUse)
		roles[ReferenceChoiceUse] = "editor"
	}
	if len(options) == 0 {
		return nil, nil
	}
	return options, roles
}

// addonResourceRoles are the roles an add-on resource grants: the add-on
// kinds of the sharing policy all have these two.
var addonResourceRoles = []string{"viewer", "editor"}

// The states of a reference for the caller.
const (
	ReferenceOK          = "ok"
	ReferenceMissing     = "missing"
	ReferenceUnavailable = "unavailable"
	// ReferenceAsk is a network or a group of built-in tools that the agent
	// asks for and that a chat of the caller does not start with: the caller
	// may turn it on in the chat.
	ReferenceAsk = "ask"
)

// AgentNeed is one reference of an agent and its state for the caller.
type AgentNeed struct {
	Kind, Label, State string
}

// ReferenceShareResolver maps a reference of a share kind (collection,
// connection) of the owner to its ID in the sharing API, making its record as
// the kind's own share route does, and gives the roles the kind grants. ok is
// false when the thing cannot be shared here: the owner does not have it, or
// this server does not share the kind.
type ReferenceShareResolver func(ctx context.Context, user, kind, id string) (target ReferenceShareTarget, ok bool)

// AgentReferences lists what an agent of the user uses. Only the owner may
// read it: it names the owner's own collections, folders and connections.
func (s *Service) AgentReferences(ctx context.Context, user, id string) ([]AgentReference, error) {
	ag, err := s.ownAgent(ctx, user, id)
	if err != nil {
		return nil, err
	}
	return s.referencesOf(ctx, principal(user), ag, true), nil
}

// referencesOf lists the references of ag. labels from the owner's own data
// (folder names) are used only when owner is true.
func (s *Service) referencesOf(ctx context.Context, p string, ag agents.Agent, owner bool) []AgentReference {
	var out []AgentReference
	resolve := func(kind, id string) *ReferenceShare {
		if !owner || s.d.ReferenceShares == nil {
			return nil
		}
		t, ok := s.d.ReferenceShares(ctx, p, kind, id)
		if !ok || t.ResourceID == "" {
			return nil
		}
		opts, roles := ReferenceChoices(t.Grantable, false)
		if len(opts) == 0 {
			return nil
		}
		return &ReferenceShare{Kind: kind, ResourceID: t.ResourceID, Options: opts, Roles: roles}
	}
	for _, a := range ag.Addons {
		out = append(out, AgentReference{Kind: "addon", ID: a, Label: a})
	}
	for _, c := range ag.Collections {
		out = append(out, AgentReference{Kind: "collection", ID: c, Label: c, Share: resolve("collection", c)})
	}
	for _, sk := range ag.Skills {
		out = append(out, AgentReference{Kind: "skill", ID: sk, Label: sk})
	}
	for _, m := range ag.MCPServers {
		out = append(out, AgentReference{Kind: "mcp", ID: m, Label: m})
	}
	for _, b := range ag.Builtins {
		out = append(out, AgentReference{Kind: "builtin", ID: b, Label: b})
	}
	var folderNames []AgentFolderState
	if owner {
		folderNames = s.AgentFolderRows(ctx, p, []agents.Agent{ag})[ag.ID]
	}
	for i, f := range ag.FolderList() {
		label := fmt.Sprintf("folder %d", i+1)
		if i < len(folderNames) && folderNames[i].Name != "" {
			label = folderNames[i].Name
		}
		out = append(out, AgentReference{Kind: "folder", ID: fmt.Sprint(f.Node), Label: label, Mode: f.Mode})
	}
	addons := make([]string, 0, len(ag.Connections))
	for a := range ag.Connections {
		addons = append(addons, a)
	}
	sort.Strings(addons)
	for _, a := range addons {
		for i, b := range ag.Connections[a] {
			out = append(out, AgentReference{Kind: "connection", ID: b, AddonID: a, Label: fmt.Sprintf("%s connection %d", a, i+1), Share: resolve("connection", b)})
		}
	}
	addons = addons[:0]
	for a := range ag.Resources {
		addons = append(addons, a)
	}
	sort.Strings(addons)
	for _, a := range addons {
		for i, r := range ag.Resources[a] {
			ref := AgentReference{Kind: "addon-resource", ID: r.ID, AddonID: a, Mode: r.Mode, Label: fmt.Sprintf("%s resource %d", a, i+1)}
			if owner {
				opts, roles := ReferenceChoices(addonResourceRoles, r.Mode == "write")
				ref.Share = &ReferenceShare{Kind: "addon", ResourceID: r.ID, Options: opts, Roles: roles}
			}
			out = append(out, ref)
		}
	}
	return out
}

// AgentNeeds lists the references of an agent the user may use, each with
// its state for the user: ok, missing (not shared with you) or unavailable
// (the server or your account does not offer it). It says nothing about a
// reference beyond its label and its state.
func (s *Service) AgentNeeds(ctx context.Context, user, id string) ([]AgentNeed, error) {
	ag, acc, err := s.SharedAgent(ctx, user, id)
	if err != nil {
		return nil, err
	}
	p := principal(user)
	refs := s.referencesOf(ctx, p, ag, acc.Owner())
	addons := map[string]bool{}
	if s.d.AddonTools != nil {
		if tools, terr := s.d.AddonTools.Tools(ctx, p); terr == nil {
			for _, t := range tools {
				addons[t.AddonID] = true
			}
		}
	}
	var colls []string
	if s.d.Collections != nil && s.d.Collections.Allowed(ctx, p) {
		colls, _ = s.d.Collections.List(ctx, p)
	}
	var skillNames []string
	if s.d.Files.Available() {
		if list, _, serr := s.Skills(ctx, user, ""); serr == nil {
			for _, sk := range list {
				skillNames = append(skillNames, sk.Name)
			}
		}
	}
	var mcp []string
	if s.d.MCPServers != nil && ag.Model != "" {
		for _, m := range s.d.MCPServers(ctx, p, ag.Model) {
			mcp = append(mcp, m.Name)
		}
	}
	held := s.usableGrants(ctx, p, ag.Builtins)
	sh := s.shares()
	out := make([]AgentNeed, 0, len(refs))
	for _, r := range refs {
		state := ReferenceOK
		switch r.Kind {
		case "addon":
			if !addons[r.ID] {
				state = ReferenceUnavailable
			}
		case "collection":
			if s.d.Collections == nil || !s.d.Collections.Allowed(ctx, p) {
				state = ReferenceUnavailable
			} else if !slices.Contains(colls, r.ID) {
				state = ReferenceMissing
			}
		case "skill":
			if !slices.Contains(skillNames, r.ID) {
				state = ReferenceMissing
			}
		case "mcp":
			if !slices.Contains(mcp, r.ID) {
				state = ReferenceUnavailable
			}
		case "builtin":
			if !slices.Contains(held, r.ID) {
				state = ReferenceUnavailable
			}
		case "folder":
			var node int64
			_, _ = fmt.Sscan(r.ID, &node)
			if node <= 0 {
				state = ReferenceMissing
			} else if _, ok := s.FolderResolver().PathOf(ctx, p, node); !ok {
				state = ReferenceMissing
			}
		case "connection":
			// The binding is the owner's. A grantee's chat uses it only while
			// the owner shares it with them (see ConnectionsOf).
			if !acc.Owner() && (s.connectors == nil || !s.connectors.sharedTo(ctx, user, r.ID)) {
				state = ReferenceMissing
			}
		case "addon-resource":
			if !addons[r.AddonID] {
				state = ReferenceUnavailable
			} else if !acc.Owner() && (sh == nil || !sh.AddonResourceReadable(ctx, p, r.AddonID, r.ID)) {
				state = ReferenceMissing
			}
		}
		out = append(out, AgentNeed{Kind: r.Kind, Label: r.Label, State: state})
	}
	return append(out, s.askedAuthority(ctx, ag, acc.Owner())...), nil
}

// askedAuthority lists the network and the opt-in groups of built-in tools
// that an agent asks for, with their state for the caller. For the owner they
// are ok: the owner's chats start with them. A chat of another person starts
// with the narrower network and with no opt-in group (see CreateAs), so each
// is ok when that start already has it, ask when the person may turn it on in
// the chat, and unavailable when this server does not allow it at all. A
// shared agent with the role use hides its instructions; what it asks for is
// shown so that the person who runs it knows.
func (s *Service) askedAuthority(ctx context.Context, ag agents.Agent, owner bool) []AgentNeed {
	var out []AgentNeed
	if n := egress.Preset(ag.Network); n.Valid() && n != egress.PresetOff {
		state := ReferenceOK
		if !owner {
			set, err := s.egressSettings(ctx)
			switch {
			case err != nil || n.Rank() > set.Ceiling.Rank():
				state = ReferenceUnavailable
			case egress.Min(n, set.DefaultPreset()) != n:
				state = ReferenceAsk
			}
		}
		out = append(out, AgentNeed{Kind: "network", Label: string(n), State: state})
	}
	for _, g := range ag.BuiltinsOn {
		state := ReferenceOK
		if !owner {
			state = ReferenceAsk
		}
		out = append(out, AgentNeed{Kind: "tool-group", Label: g, State: state})
	}
	return out
}
