package studio

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/egress/audit"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// The routes of the network of a chat's Sandbox commands: the chat's own
// view and preset for its owner, and the administrator's view.

var (
	// ErrEgressPreset means the preset is not off, registries or all.
	ErrEgressPreset = errors.New("studio: the network preset must be off, registries or all")
	// ErrEgressAboveCeiling means the preset is wider than the administrator
	// allows.
	ErrEgressAboveCeiling = errors.New("studio: the network preset is above the limit an administrator set")
	// ErrEgressNoRule means the chat has no rule for that host and port.
	ErrEgressNoRule = errors.New("studio: the chat has no network rule for this host and port")
)

// The reasons the network settings do not apply to a chat.
const (
	EgressAppliesHost = "host" // commands run on this machine, with its network
	EgressAppliesNone = "none" // no Sandbox serves bash in this chat
)

// EgressView is the network of one chat, as its network panel shows it.
type EgressView struct {
	Preset, Ceiling, Effective egress.Preset
	// Applies is false when commands run on this machine or no Sandbox serves
	// bash; AppliesReason then says which.
	Applies       bool
	AppliesReason string
	Registries    []egress.Registry
	// AllowedHosts is the administrator's always-allowed list, canonical.
	AllowedHosts []string
	Rules        []EgressRuleView
	Cards        []egress.Card
}

// EgressRuleView is a rule with whether the ceiling still admits it.
type EgressRuleView struct {
	egress.Rule
	AboveLimit bool
}

// registriesView is the registries list with the administrator's extra hosts
// as one more entry.
func registriesView(s egress.Settings) []egress.Registry {
	out := append([]egress.Registry(nil), egress.BuiltinRegistries().List...)
	if len(s.ExtraRegistries) > 0 {
		out = append(out, egress.Registry{ID: "extra", Title: "Added by an administrator", Hosts: append([]string(nil), s.ExtraRegistries...)})
	}
	return out
}

// Egress returns the network of a chat for its owner. The chat of another
// user is session.ErrNotFound.
func (s *Service) Egress(ctx context.Context, user, conv string) (EgressView, error) {
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return EgressView{}, err
	}
	set := s.egressSettings()
	pol, err := s.egressPolicyOf(ctx, rec, "")
	if err != nil {
		return EgressView{}, err
	}
	v := EgressView{
		Preset: pol.chat.Preset, Ceiling: egress.Min(set.Ceiling, egress.PresetAll),
		Effective:    egress.Min(pol.chat.Preset, set.Ceiling),
		Registries:   registriesView(set),
		AllowedHosts: set.AllowedHostList(),
		Rules:        []EgressRuleView{},
		Cards:        append([]egress.Card{}, pol.rec.Egress.Cards...),
	}
	for _, r := range pol.rec.Egress.Rules {
		above := r.Allow && !egress.Admits(set, egress.Request{Host: r.Host, Port: r.Port, TLS: r.Port == 443}, egress.BuiltinRegistries())
		v.Rules = append(v.Rules, EgressRuleView{Rule: r, AboveLimit: above})
	}
	if v.Preset == "" {
		v.Preset = egress.PresetOff
	}
	cmds, err := s.CommandRunner(ctx, p, conv)
	switch {
	case err != nil:
		v.AppliesReason = EgressAppliesNone
	case cmds.State == CommandsAvailable && (cmds.Provider == BuiltinProvider || cmds.Isolation == "host"):
		v.AppliesReason = EgressAppliesHost
	case cmds.State == CommandsAvailable:
		v.Applies = true
	default:
		v.AppliesReason = EgressAppliesNone
	}
	return v, nil
}

// SetEgressPreset sets the preset of a chat. A preset above the ceiling is
// refused; a sub-agent follows its parent and has none of its own.
func (s *Service) SetEgressPreset(ctx context.Context, user, conv string, preset egress.Preset) error {
	p := principal(user)
	if !preset.Valid() {
		return ErrEgressPreset
	}
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return err
	}
	if rec.Kind == session.KindSubAgent {
		return ErrSubAgentReadOnly
	}
	if preset.Rank() > egress.Min(s.egressSettings().Ceiling, egress.PresetAll).Rank() {
		return ErrEgressAboveCeiling
	}
	if err := s.d.Store.UpdateEgress(ctx, p, conv, func(st *egress.ChatState) error {
		st.Preset = preset
		return nil
	}); err != nil {
		return err
	}
	s.publishEgress(p, conv, "")
	return nil
}

// DeleteEgressRule removes the allow or deny rule of a host and port.
func (s *Service) DeleteEgressRule(ctx context.Context, user, conv, host string, port int) error {
	p := principal(user)
	host = canonicalHost(host)
	err := s.d.Store.UpdateEgress(ctx, p, conv, func(st *egress.ChatState) error {
		if !st.DeleteRule(host, port) {
			return ErrEgressNoRule
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.publishEgress(p, conv, "")
	return nil
}

// publishEgress tells every open tab of the chat that its network changed:
// a card (id) or the rules and the preset (empty id).
func (s *Service) publishEgress(p, conv, card string) {
	ev := session.Event{Kind: session.KindEgressCard}
	if card != "" {
		ev.IDs = []string{card}
	}
	s.bus.Publish(key(p, conv), ev)
}

// EgressAdmin is the administrator's view of the Sandbox network.
type EgressAdmin struct {
	Settings   egress.Settings
	AuditDays  int
	Registries []egress.Registry
	// Sandbox is the add-on that runs commands for the administrator, and
	// Isolation how ("isolated", "host" or "" when unknown). Empty Sandbox:
	// none serves, and the settings apply to nothing yet.
	Sandbox, Isolation string
	Last24h            audit.Counts
}

// AdminEgress returns the settings in force, the registries list with the
// extra hosts, whether a Sandbox serves and the counts of the last 24 hours.
func (s *Service) AdminEgress(ctx context.Context, user string) (EgressAdmin, error) {
	set := s.egressSettings()
	set.Ceiling = egress.Min(set.Ceiling, egress.PresetAll)
	set.Default = set.DefaultPreset()
	if len(set.Ports) == 0 {
		set.Ports = []int{80, 443}
	}
	out := EgressAdmin{Settings: set, Registries: registriesView(set), AuditDays: DefaultEgressAuditDays}
	if s.d.EgressAuditDays != nil {
		if d := s.d.EgressAuditDays(); d >= 1 && d <= 365 {
			out.AuditDays = d
		}
	}
	s.runners.addons.Range(func(k, _ any) bool {
		out.Sandbox = k.(string)
		return false
	})
	if out.Sandbox != "" {
		if iso, ok := s.d.AddonTools.(CommandIsolation); ok {
			out.Isolation = iso.Isolation(ctx, principal(user), out.Sandbox)
		}
	}
	c, err := s.egressAudit().Count(ctx, s.now().Add(-24*time.Hour))
	if err != nil {
		return EgressAdmin{}, err
	}
	out.Last24h = c
	return out, nil
}

// EgressEvents reads the network audit, newest first.
func (s *Service) EgressEvents(ctx context.Context, q audit.Query) ([]audit.Event, error) {
	return s.egressAudit().Events(ctx, q)
}
