package studio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// ErrCoordinatorsOff means the service has no coordinator host.
var ErrCoordinatorsOff = errors.New("studio: coordinators are not available")

// ErrNotACoordinator means the agent has no coordinator config. It is a
// coordinator.ErrNotFound.
var ErrNotACoordinator = fmt.Errorf("studio: the agent is not a coordinator: %w", coordinator.ErrNotFound)

func invalidCoordinator(format string, args ...any) error {
	return fmt.Errorf("%w: %s", coordinator.ErrInvalid, fmt.Sprintf(format, args...))
}

// CoordinatorLimits are the operator limits of the coordinators. Zero fields
// take the defaults of the runtime settings.
type CoordinatorLimits struct{ HelpersCeiling, DepthCeiling, RunsPerDay, MaxRunning, AuditDays int }

func (l CoordinatorLimits) withDefaults() CoordinatorLimits {
	for _, f := range []struct {
		v *int
		d int
	}{
		{&l.HelpersCeiling, config.DefaultChatsCoordinatorHelpersCeiling},
		{&l.DepthCeiling, config.DefaultChatsCoordinatorDepthCeiling},
		{&l.RunsPerDay, config.DefaultChatsCoordinatorRunsPerDay},
		{&l.MaxRunning, config.DefaultChatsCoordinatorMaxRunning},
		{&l.AuditDays, config.DefaultChatsCoordinatorAuditDays},
	} {
		if *f.v <= 0 {
			*f.v = f.d
		}
	}
	return l
}

// CoordinatorOptions configures the coordinator host.
type CoordinatorOptions struct {
	Store coordinator.Store
	// Limits returns the operator limits that apply now. Nil: the defaults.
	Limits func() CoordinatorLimits
	// RunLimit bounds a wake turn; the application passes the trigger run
	// limit. Nil: 15 minutes.
	RunLimit func() time.Duration
	Clock    func() time.Time
	Logger   *slog.Logger
}

// Coordinators keeps the coordinator agents: a saved agent plus a
// coordinator.Config, and its one long-lived chat.
//
// State mode: shared. The configs, counters and audit log are in the store, so
// every frontend of a cluster sees the same state. Wake-ups run on one
// frontend at a time (the leader of the reaper).
type Coordinators struct {
	svc  *Service
	o    CoordinatorOptions
	log  *slog.Logger
	wake wakeState
}

// NewCoordinators returns the coordinator host of a service.
func NewCoordinators(svc *Service, o CoordinatorOptions) (*Coordinators, error) {
	if svc == nil || o.Store == nil {
		return nil, errors.New("studio: the coordinator host needs a service and a store")
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	h := &Coordinators{svc: svc, o: o, log: o.Logger}
	h.wake.init()
	return h, nil
}

// SetCoordinators gives the service its coordinator host. Without one there
// are no coordinators.
func (s *Service) SetCoordinators(h *Coordinators) { s.coordinators = h }

// Coordinators returns the coordinator host, or nil when it is off.
func (s *Service) Coordinators() *Coordinators { return s.coordinators }

// Limits returns the operator limits that apply now.
func (h *Coordinators) Limits() CoordinatorLimits {
	var l CoordinatorLimits
	if h.o.Limits != nil {
		l = h.o.Limits()
	}
	return l.withDefaults()
}

func (h *Coordinators) now() time.Time { return h.o.Clock().UTC() }

// CoordinatorInput is what a person sends to make or change a coordinator:
// the agent (its model, instructions, tools) and the coordinator config. The
// runtime fields of the config (chat, pause, wake times) are ignored.
type CoordinatorInput struct {
	Agent  agents.Agent
	Config coordinator.Config
}

// CoordinatorUsage is what a coordinator used today (UTC) against its limits.
type CoordinatorUsage struct{ RunsToday, SentToday, RunsLimit, SendLimit int }

// CoordinatorView is a coordinator as a person sees it.
type CoordinatorView struct {
	Agent  agents.Agent
	Config coordinator.Config
	Usage  CoordinatorUsage
}

func (h *Coordinators) view(ctx context.Context, a agents.Agent, c coordinator.Config) CoordinatorView {
	now := h.now()
	runs, _ := h.o.Store.Used(ctx, c.AgentID, coordinator.DayBucket("runs", now))
	sent, _ := h.o.Store.Used(ctx, c.AgentID, coordinator.DayBucket("send", now))
	return CoordinatorView{Agent: a, Config: c, Usage: CoordinatorUsage{
		RunsToday: runs, SentToday: sent,
		RunsLimit: h.Limits().RunsPerDay, SendLimit: c.WithDefaults().SendPerDay,
	}}
}

// checkConfig validates what the creator set against the stores and the
// operator ceilings.
func (h *Coordinators) checkConfig(ctx context.Context, p string, c coordinator.Config) error {
	if err := coordinator.Validate(c); err != nil {
		return err
	}
	lim := h.Limits()
	if c.MaxHelpers > lim.HelpersCeiling {
		return invalidCoordinator("helpers: the highest allowed here is %d", lim.HelpersCeiling)
	}
	if c.MaxDepth > lim.DepthCeiling {
		return invalidCoordinator("helper depth: the highest allowed here is %d", lim.DepthCeiling)
	}
	for _, ct := range c.Contacts {
		var err error
		switch ct.Kind {
		case coordinator.ContactUser:
			err = h.checkUserContact(ctx, p, ct)
		case coordinator.ContactExternal:
			err = h.checkExternalContact(ctx, p, ct)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// checkUserContact accepts the owner, and another user only when the account
// lookup finds them.
func (h *Coordinators) checkUserContact(ctx context.Context, p string, ct coordinator.Contact) error {
	if ct.User == p {
		return nil
	}
	d := h.svc.d
	var (
		u   *auth.User
		err error
	)
	switch {
	case d.Users != nil:
		u, err = d.Users(ctx, ct.User)
	case d.FindUser != nil:
		u, err = d.FindUser(ctx, ct.User)
	default:
		return invalidCoordinator("contact %q: this server has no accounts, so only you can be a user contact", ct.ID)
	}
	if errors.Is(err, ErrNoUser) || (err == nil && u == nil) {
		return invalidCoordinator("contact %q: the user does not exist", ct.ID)
	}
	if err != nil {
		return err
	}
	return nil
}

// checkExternalContact accepts a contact on an own binding that keeps one chat
// per sender or per chat and that accepts the contact's sender.
func (h *Coordinators) checkExternalContact(ctx context.Context, p string, ct coordinator.Contact) error {
	if h.svc.connectors == nil {
		return invalidCoordinator("contact %q: connectors are not available", ct.ID)
	}
	b, err := h.svc.connectors.o.Store.GetBinding(ctx, ct.Binding)
	if errors.Is(err, connectors.ErrNotFound) || (err == nil && b.Owner != p) {
		// Another user's binding looks the same as a missing one.
		return invalidCoordinator("contact %q: the binding does not exist", ct.ID)
	}
	if err != nil {
		return err
	}
	if m := b.ConversationMode(); m != connectors.ConversationSender && m != connectors.ConversationChat {
		return invalidCoordinator("contact %q: the binding keeps conversations by %s; a coordinator needs one chat per sender or per chat", ct.ID, m)
	}
	sender := ct.Sender
	if sender == "" {
		sender = ct.Chat
	}
	if !b.AcceptAny && !b.Allows(sender, "") && !b.Allows(ct.Chat, "") {
		return invalidCoordinator("contact %q: the binding does not accept this sender", ct.ID)
	}
	return nil
}

// Create makes a coordinator for user: the agent, the config and the chat.
func (h *Coordinators) Create(ctx context.Context, user string, in CoordinatorInput) (CoordinatorView, error) {
	s := h.svc
	if s.d.Agents == nil {
		return CoordinatorView{}, ErrAgentsOff
	}
	p := principal(user)
	cfg := in.Config
	cfg.AgentID, cfg.Principal = "", p
	cfg.ChatID, cfg.Paused, cfg.PausedBy = "", false, ""
	cfg.PausedAt, cfg.NextWake, cfg.LastWake = time.Time{}, time.Time{}, time.Time{}
	if err := h.checkConfig(ctx, p, cfg); err != nil {
		return CoordinatorView{}, err
	}
	a := in.Agent
	a.ID = ""
	if strings.TrimSpace(a.Model) == "" {
		return CoordinatorView{}, ErrNoModel
	}
	folders, err := h.pageFolders(ctx, p, cfg.Page)
	if err != nil {
		return CoordinatorView{}, err
	}
	saved, err := s.SaveAgentWithFolders(ctx, user, Preset(a, cfg.Page), folders)
	if err != nil {
		return CoordinatorView{}, err
	}
	cfg.AgentID = saved.ID
	cfg.Created = h.now()
	cfg.Updated = cfg.Created
	if err := h.o.Store.Put(ctx, cfg); err != nil {
		_ = s.d.Agents.Delete(ctx, p, saved.ID)
		return CoordinatorView{}, err
	}
	if _, err := h.EnsureChat(ctx, cfg); err != nil {
		_ = h.o.Store.Delete(ctx, saved.ID)
		_ = s.d.Agents.Delete(ctx, p, saved.ID)
		return CoordinatorView{}, err
	}
	return h.Get(ctx, user, saved.ID)
}

// pageFolders is the folder fence of the agent: its page, writable. A
// coordinator with no page keeps the folders the agent carries.
func (h *Coordinators) pageFolders(ctx context.Context, p, page string) (*[]AgentFolderWrite, error) {
	if page == "" {
		return nil, nil
	}
	rel, err := h.ensurePage(ctx, p, page)
	if err != nil {
		return nil, err
	}
	return &[]AgentFolderWrite{{Path: rel, Mode: agents.FolderWrite}}, nil
}

// Update changes the agent and the config of a coordinator of user. The chat,
// the pause state and the wake times stay as they are.
func (h *Coordinators) Update(ctx context.Context, user, agentID string, in CoordinatorInput) (CoordinatorView, error) {
	s := h.svc
	p := principal(user)
	old, err := h.Get(ctx, user, agentID)
	if err != nil {
		return CoordinatorView{}, err
	}
	cfg := in.Config
	cfg.AgentID, cfg.Principal = agentID, p
	if err := h.checkConfig(ctx, p, cfg); err != nil {
		return CoordinatorView{}, err
	}
	a := in.Agent
	a.ID = agentID
	if a.Builtins == nil {
		a.Builtins = old.Agent.Builtins
	}
	folders, err := h.pageFolders(ctx, p, cfg.Page)
	if err != nil {
		return CoordinatorView{}, err
	}
	if _, err := s.SaveAgentWithFolders(ctx, user, Preset(a, cfg.Page), folders); err != nil {
		return CoordinatorView{}, err
	}
	cfg.Created, cfg.Updated = old.Config.Created, h.now()
	if err := h.o.Store.Put(ctx, cfg); err != nil {
		return CoordinatorView{}, err
	}
	return h.Get(ctx, user, agentID)
}

// Get returns a coordinator of user. An agent that is not theirs, or not a
// coordinator, is an error that names which.
func (h *Coordinators) Get(ctx context.Context, user, agentID string) (CoordinatorView, error) {
	s := h.svc
	if s.d.Agents == nil {
		return CoordinatorView{}, ErrAgentsOff
	}
	p := principal(user)
	a, err := s.d.Agents.Get(ctx, p, agentID)
	if err != nil {
		return CoordinatorView{}, err
	}
	c, err := h.o.Store.Get(ctx, agentID)
	if errors.Is(err, coordinator.ErrNotFound) {
		return CoordinatorView{}, ErrNotACoordinator
	}
	if err != nil {
		return CoordinatorView{}, err
	}
	if c.Principal != p {
		return CoordinatorView{}, agents.ErrNotFound
	}
	return h.view(ctx, a, c), nil
}

// List returns the coordinators of user, by agent name.
func (h *Coordinators) List(ctx context.Context, user string) ([]CoordinatorView, error) {
	p := principal(user)
	return h.list(ctx, func(c coordinator.Config) bool { return c.Principal == p })
}

// ListAll returns the coordinators of every user.
func (h *Coordinators) ListAll(ctx context.Context) ([]CoordinatorView, error) {
	return h.list(ctx, func(coordinator.Config) bool { return true })
}

func (h *Coordinators) list(ctx context.Context, keep func(coordinator.Config) bool) ([]CoordinatorView, error) {
	if h.svc.d.Agents == nil {
		return nil, ErrAgentsOff
	}
	cfgs, err := h.o.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := []CoordinatorView{}
	for _, c := range cfgs {
		if !keep(c) {
			continue
		}
		a, err := h.svc.d.Agents.Get(ctx, c.Principal, c.AgentID)
		if errors.Is(err, agents.ErrNotFound) {
			continue // the agent is gone and its config follows
		}
		if err != nil {
			return nil, err
		}
		out = append(out, h.view(ctx, a, c))
	}
	return out, nil
}

// Is says whether the agent is a coordinator.
func (h *Coordinators) Is(ctx context.Context, agentID string) bool {
	if h == nil {
		return false
	}
	_, err := h.o.Store.Get(ctx, agentID)
	return err == nil
}

// EnsureChat returns the chat of the coordinator, making it when it was never
// made or was deleted, and stores its ID. Two frontends that race end with
// one chat. A coordinator whose config is gone gets no chat: nothing is made
// again for a deleted coordinator.
func (h *Coordinators) EnsureChat(ctx context.Context, cfg coordinator.Config) (session.ChatRecord, error) {
	s := h.svc
	cur, err := h.o.Store.Get(ctx, cfg.AgentID)
	if errors.Is(err, coordinator.ErrNotFound) {
		return session.ChatRecord{}, ErrNotACoordinator
	}
	if err != nil {
		return session.ChatRecord{}, err
	}
	if cur.ChatID != "" {
		rec, err := s.d.Store.GetChat(ctx, cur.Principal, cur.ChatID)
		if err == nil {
			return rec, nil
		}
		if !errors.Is(err, session.ErrNotFound) {
			return session.ChatRecord{}, err
		}
	}
	title := "Coordinator"
	if a, err := s.d.Agents.Get(ctx, cur.Principal, cur.AgentID); err == nil && a.Name != "" {
		title = a.Name
	}
	rec, err := s.CreateAs(ctx, cur.Principal, CreateInput{Title: title, AgentID: cur.AgentID},
		Origin{Kind: session.OriginCoordinator, ID: cur.AgentID})
	if err != nil {
		return session.ChatRecord{}, err
	}
	var stored string
	if cur.ChatID == "" {
		stored, err = h.o.Store.SetChat(ctx, cur.AgentID, rec.ID)
	} else {
		stored, err = h.o.Store.ReplaceChat(ctx, cur.AgentID, cur.ChatID, rec.ID)
	}
	if err != nil || stored != rec.ID {
		// Lost the race, or the config went away: the chat made here is not
		// the coordinator's.
		_ = s.Delete(ctx, cur.Principal, rec.ID)
		if err != nil {
			if errors.Is(err, coordinator.ErrNotFound) {
				return session.ChatRecord{}, ErrNotACoordinator
			}
			return session.ChatRecord{}, err
		}
		return s.d.Store.GetChat(ctx, cur.Principal, stored)
	}
	return rec, nil
}

// forgetCoordinator drops the config of a deleted agent. The chat keeps its
// record, as the chats of any deleted agent do; nothing wakes it again.
func (s *Service) forgetCoordinator(ctx context.Context, agentID string) {
	h := s.coordinators
	if h == nil {
		return
	}
	if err := h.o.Store.Delete(ctx, agentID); err != nil && !errors.Is(err, coordinator.ErrNotFound) {
		s.log.Warn("studio: could not drop the coordinator config of a deleted agent", "agent", agentID, "error", err)
	}
}
