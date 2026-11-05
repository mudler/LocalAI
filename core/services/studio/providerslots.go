package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Provider slots. A slot is a job the host does itself and lets an add-on do in
// its place (pkg/addons/contract/providerslots.go). This file is the generic
// part: it lists the providers of a slot, stores the choice of a person and
// resolves it, and says which add-ons contribute to a turn. It knows no slot
// and no add-on; a slot brings its own wiring (for the notes, memory.go).
//
// The choice is one value per person and slot: the built-in provider, off, or
// the ID of an add-on that declares the slot. It is kept with the person's
// other host settings, so every frontend of a cluster reads the same answer.
//
// The rule that matters is the failure one. A person who picked an add-on and
// whose add-on stops, is disabled or goes away does not fall back to the
// built-in provider: their data would be written somewhere they did not
// choose. The slot is off, with a reason, until they pick again.

// AddonProviderSet is the provider slots one add-on declares, as the host sees
// them.
type AddonProviderSet struct {
	AddonID, Title, Description string
	Provides                    []contract.ProviderDeclaration
	// Running is true when the add-on can serve now: installed, enabled and
	// running.
	Running bool
}

// AddonProviderLister is implemented by a broker that knows the provider slots
// the add-ons declare. It lists the installed add-ons that declare some and
// that the user can see. It is optional; a broker without it has no add-on
// providers.
type AddonProviderLister interface {
	AddonProviders(ctx context.Context, user string) ([]AddonProviderSet, error)
}

// Errors of the provider slots.
var (
	// ErrUnknownSlot means the slot is not one of the host.
	ErrUnknownSlot = errors.New("studio: unknown provider slot")
	// ErrUnknownProvider means the provider is neither the built-in one, off
	// nor an add-on that declares the slot.
	ErrUnknownProvider = errors.New("studio: unknown provider for this slot")
	// ErrProviderUnavailable means the add-on is installed but cannot serve now.
	ErrProviderUnavailable = errors.New("studio: the provider cannot serve now")
	// ErrProviderSettings means the choice cannot be stored.
	ErrProviderSettings = errors.New("studio: the provider choice cannot be stored on this server")
)

// The reason codes of a slot that is off because of its provider.
const (
	// ProviderUnavailable: the selected add-on is stopped, disabled, removed or
	// not visible to the person.
	ProviderUnavailable = "provider_unavailable"
	// ProviderUnreadable: the stored choice could not be read.
	ProviderUnreadable = "provider_unreadable"
	// ProviderChosenOff: the person chose Off.
	ProviderChosenOff = "provider_off"
)

// providerKey is the key of the choice of a slot among the host settings.
func providerKey(slot string) string { return "provider-" + slot }

// SlotResolution is what serves a slot for a person now.
type SlotResolution struct {
	Slot string `json:"slot"`
	// Selected is the stored choice, or the default of the slot when the person
	// never chose. Stored says which.
	Selected string `json:"selected"`
	Stored   bool   `json:"stored"`
	// Active is what serves: ProviderBuiltin, an add-on ID or ProviderOff.
	Active string `json:"active"`
	Off    bool   `json:"off"`
	// Code says why a slot is off: ProviderChosenOff, ProviderUnavailable or
	// ProviderUnreadable. Empty when the slot is on.
	Code string `json:"code,omitempty"`
	// Reason is an English sentence for logs and tests.
	Reason string `json:"reason,omitempty"`
	// AddonID and AddonTitle name the add-on a failure is about.
	AddonID    string `json:"addon_id,omitempty"`
	AddonTitle string `json:"addon_title,omitempty"`
}

// Addon is the add-on that serves the slot now, or empty.
func (r SlotResolution) Addon() string {
	if r.Off || r.Active == contract.ProviderBuiltin {
		return ""
	}
	return r.Active
}

// ProviderView is one provider of a slot in the list a person picks from.
type ProviderView struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Kind is builtin, addon or off.
	Kind string `json:"kind"`
	// Available is false for an add-on that cannot serve now.
	Available bool `json:"available"`
	// Properties are the facts the add-on declared about itself (remote, undo).
	Properties map[string]any `json:"properties,omitempty"`
	// Roles lists the roles the add-on fills with tools.
	Roles    []string `json:"roles,omitempty"`
	Selected bool     `json:"selected"`
}

// SlotView is a slot with its providers and the choice in force.
type SlotView struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// OffMeans is what Off does for this slot.
	OffMeans   string         `json:"off_means,omitempty"`
	Providers  []ProviderView `json:"providers"`
	Resolution SlotResolution `json:"resolution"`
	// Health says when the host leaves the provider alone after repeated
	// failures, with a plain notice. Zero while the provider answers.
	Health ProviderHealth `json:"health"`
}

// slotWiring is what a slot brings to the generic code.
type slotWiring struct {
	// legacy gives a choice for a person who never made one in this setting
	// (an older switch the slot kept), or false.
	legacy func(ctx context.Context, user string) (string, bool)
	// narrowed reports that a chat turns the slot off for itself (a private
	// chat, an agent that wants none), whatever the person chose.
	narrowed func(ctx context.Context, rec session.ChatRecord) bool
	// onSelect runs after a choice was stored, to keep an older switch of the
	// slot in step with it.
	onSelect func(ctx context.Context, user, id string)
}

func (s *Service) slotWiringOf(slot string) slotWiring { return s.slotWirings[slot] }

// providerSets lists the add-on providers of a person. A broker that cannot
// answer gives an error, which callers read as "no provider can serve".
func (s *Service) providerSets(ctx context.Context, user string) ([]AddonProviderSet, error) {
	if s.d.AddonTools == nil {
		return nil, nil
	}
	lister, ok := s.d.AddonTools.(AddonProviderLister)
	if !ok {
		return nil, nil
	}
	sets, err := lister.AddonProviders(ctx, user)
	if err != nil {
		return nil, err
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].AddonID < sets[j].AddonID })
	return sets, nil
}

// storedProvider reads the choice of a person. ok is false when none was
// stored; an error is a store that cannot answer.
func (s *Service) storedProvider(ctx context.Context, user, slot string) (string, bool, error) {
	if s.d.UserSetting == nil {
		return "", false, nil
	}
	return s.d.UserSetting(ctx, principal(user), HostSettingsID, providerKey(slot))
}

// ResolveProvider says what serves a slot for a person. A store or a broker
// that cannot answer turns the slot off for the call: a failure never serves
// a provider the person may have switched off.
func (s *Service) ResolveProvider(ctx context.Context, user, slot string) SlotResolution {
	var legacy func() (string, bool)
	if w := s.slotWiringOf(slot).legacy; w != nil {
		legacy = func() (string, bool) { return w(ctx, user) }
	}
	return s.resolveSlot(ctx, user, slot, legacy)
}

// resolveSlot is ResolveProvider with the older choice of the slot already
// at hand.
func (s *Service) resolveSlot(ctx context.Context, user, slot string, legacy func() (string, bool)) SlotResolution {
	desc, ok := contract.ProviderSlotByID(slot)
	out := SlotResolution{Slot: slot, Selected: contract.ProviderBuiltin, Active: contract.ProviderBuiltin}
	if !ok {
		return slotOff(out, ProviderUnreadable, "the slot is not known")
	}
	out.Selected, out.Active = desc.Selection.Default, desc.Selection.Default
	v, stored, err := s.storedProvider(ctx, user, slot)
	if err != nil {
		s.log.Warn("studio: the provider choice could not be read, the slot stays off for this call", "slot", slot, "error", err)
		return slotOff(out, ProviderUnreadable, "the choice of provider could not be read")
	}
	if stored {
		out.Selected, out.Stored = v, true
	} else if legacy != nil {
		if lv, ok := legacy(); ok {
			out.Selected = lv
		}
	}
	// An add-on the built-in provider stands in for means the built-in one.
	out.Selected = desc.Canonical(out.Selected)
	switch out.Selected {
	case contract.ProviderBuiltin:
		out.Active = contract.ProviderBuiltin
		return out
	case contract.ProviderOff:
		if desc.Selection.OffAllowed {
			return slotOff(out, ProviderChosenOff, "the person chose Off")
		}
		out.Active = contract.ProviderBuiltin
		return out
	}
	// An add-on. It serves only while it declares the slot and runs.
	sets, err := s.providerSets(ctx, user)
	if err != nil {
		s.log.Warn("studio: add-on providers unavailable, the slot stays off for this call", "slot", slot, "error", err)
		out.AddonID = out.Selected
		return slotOff(out, ProviderUnavailable, "the add-ons could not be listed")
	}
	for _, set := range sets {
		if set.AddonID != out.Selected || !declares(set.Provides, slot) {
			continue
		}
		out.AddonID, out.AddonTitle = set.AddonID, set.Title
		if !set.Running {
			return slotOff(out, ProviderUnavailable, "the add-on is not running")
		}
		if gate, ok := s.d.AddonTools.(AddonGate); ok {
			if why := gate.Unavailable(ctx, principal(user), set.AddonID, s.d.Files.Available()); why != "" {
				return slotOff(out, ProviderUnavailable, why)
			}
		}
		out.Active = set.AddonID
		return out
	}
	out.AddonID = out.Selected
	return slotOff(out, ProviderUnavailable, "the add-on is not installed or not visible to this person")
}

func slotOff(r SlotResolution, code, reason string) SlotResolution {
	r.Active, r.Off, r.Code, r.Reason = contract.ProviderOff, true, code, reason
	return r
}

func declares(list []contract.ProviderDeclaration, slot string) bool {
	return slices.ContainsFunc(list, func(d contract.ProviderDeclaration) bool { return d.Slot == slot })
}

// isSlotAlias reports an add-on that the built-in provider of the slot stands
// in for. It is never listed as a provider and never serves.
func isSlotAlias(slot, addonID string) bool {
	desc, ok := contract.ProviderSlotByID(slot)
	return ok && desc.IsAlias(addonID)
}

// ProviderSlots lists the slots of the host with their providers and the
// choice in force for a person.
func (s *Service) ProviderSlots(ctx context.Context, user string) ([]SlotView, error) {
	var out []SlotView
	for _, desc := range contract.ProviderSlots() {
		v, err := s.slotView(ctx, user, desc)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ProviderSlot is one slot, or ErrUnknownSlot.
func (s *Service) ProviderSlot(ctx context.Context, user, slot string) (SlotView, error) {
	desc, ok := contract.ProviderSlotByID(slot)
	if !ok {
		return SlotView{}, ErrUnknownSlot
	}
	return s.slotView(ctx, user, desc)
}

func (s *Service) slotView(ctx context.Context, user string, desc contract.ProviderSlot) (SlotView, error) {
	res := s.ResolveProvider(ctx, user, desc.ID)
	view := SlotView{ID: desc.ID, Version: desc.Version, Title: desc.Title, Description: desc.Description, OffMeans: desc.Selection.OffMeans, Resolution: res,
		Health: s.providerHealthOf(desc.ID, res)}
	view.Providers = append(view.Providers, ProviderView{ID: contract.ProviderBuiltin, Title: desc.Builtin.Title, Description: desc.Builtin.Description,
		Kind: "builtin", Available: true, Selected: res.Selected == contract.ProviderBuiltin})
	sets, err := s.providerSets(ctx, user)
	if err != nil {
		s.log.Warn("studio: add-on providers unavailable", "slot", desc.ID, "error", err)
	}
	listed := false
	for _, set := range sets {
		for _, d := range set.Provides {
			if d.Slot != desc.ID || desc.IsAlias(set.AddonID) {
				continue
			}
			p := ProviderView{ID: set.AddonID, Title: set.Title, Description: set.Description, Kind: "addon", Available: set.Running,
				Properties: d.Properties, Selected: res.Selected == set.AddonID}
			for role, tools := range d.Roles {
				if len(tools) > 0 {
					p.Roles = append(p.Roles, role)
				}
			}
			sort.Strings(p.Roles)
			view.Providers = append(view.Providers, p)
			listed = listed || p.Selected
		}
	}
	// A stored choice of an add-on that the list no longer holds stays visible,
	// so the person sees what they chose and why it is not serving.
	if !listed && res.Selected != contract.ProviderBuiltin && res.Selected != contract.ProviderOff {
		title := res.AddonTitle
		if title == "" {
			title = res.Selected
		}
		view.Providers = append(view.Providers, ProviderView{ID: res.Selected, Title: title, Kind: "addon", Available: false, Selected: true})
	}
	if desc.Selection.OffAllowed {
		view.Providers = append(view.Providers, ProviderView{ID: contract.ProviderOff, Title: "Off", Description: desc.Selection.OffMeans, Kind: "off",
			Available: true, Selected: res.Selected == contract.ProviderOff})
	}
	return view, nil
}

// SelectProvider stores the choice of a person for a slot and returns the slot
// as it stands. The built-in provider and off need no check (off only when the
// slot allows it); an add-on must declare the slot and run now.
func (s *Service) SelectProvider(ctx context.Context, user, slot, id string) (SlotView, error) {
	desc, ok := contract.ProviderSlotByID(slot)
	if !ok {
		return SlotView{}, ErrUnknownSlot
	}
	id = desc.Canonical(id)
	switch id {
	case contract.ProviderBuiltin:
	case contract.ProviderOff:
		if !desc.Selection.OffAllowed {
			return SlotView{}, fmt.Errorf("%w: %q", ErrUnknownProvider, id)
		}
	default:
		sets, err := s.providerSets(ctx, user)
		if err != nil {
			return SlotView{}, fmt.Errorf("%w: the add-ons could not be listed", ErrProviderUnavailable)
		}
		i := slices.IndexFunc(sets, func(set AddonProviderSet) bool { return set.AddonID == id && declares(set.Provides, slot) })
		if i < 0 {
			return SlotView{}, fmt.Errorf("%w: %q", ErrUnknownProvider, id)
		}
		if !sets[i].Running {
			return SlotView{}, fmt.Errorf("%w: %q", ErrProviderUnavailable, id)
		}
	}
	if s.d.SetUserSetting == nil {
		return SlotView{}, ErrProviderSettings
	}
	if err := s.d.SetUserSetting(ctx, principal(user), HostSettingsID, providerKey(slot), id); err != nil {
		return SlotView{}, fmt.Errorf("%w: %v", ErrProviderSettings, err)
	}
	if w := s.slotWiringOf(slot).onSelect; w != nil {
		w(ctx, user, id)
	}
	return s.slotView(ctx, user, desc)
}

// providerGate says which add-ons contribute to a turn. An add-on that declares
// a slot contributes its context files and skills, and its role tools, only
// while it serves a slot it declares. An add-on that declares none is not
// gated.
type providerGate struct {
	declared map[string]bool            // add-ons that declare a slot
	serving  map[string]bool            // add-ons that serve one of their slots now
	roles    map[string]map[string]bool // add-on -> full names of the tools of its roles
	// filled says which role of which slot a tool of an add-on that serves
	// fills: add-on -> full tool name -> role.
	filled map[string]map[string]providerRoleRef
}

// roleOf returns the role a tool fills while its add-on serves the slot.
func (g providerGate) roleOf(addonID, tool string) (providerRoleRef, bool) {
	r, ok := g.filled[addonID][tool]
	return r, ok && g.serving[addonID]
}

// contributes reports whether the add-on's files and skills count.
func (g providerGate) contributes(addonID string) bool {
	return !g.declared[addonID] || g.serving[addonID]
}

// hidden reports whether a tool is a role tool of an add-on that does not serve.
func (g providerGate) hidden(addonID, tool string) bool {
	return g.declared[addonID] && !g.serving[addonID] && g.roles[addonID][tool]
}

// providerGateFor reads the providers once for a turn. rec, when it is not
// nil, narrows the slots a chat turns off for itself.
func (s *Service) providerGateFor(ctx context.Context, user string, rec *session.ChatRecord) providerGate {
	g := providerGate{declared: map[string]bool{}, serving: map[string]bool{}, roles: map[string]map[string]bool{}, filled: map[string]map[string]providerRoleRef{}}
	sets, err := s.providerSets(ctx, user)
	if err != nil {
		// Cannot tell: every add-on that might declare a slot is held back, and
		// the ones we know nothing about are not gated.
		s.log.Warn("studio: add-on providers unavailable, no provider contributes to this turn", "user", user, "error", err)
		return g
	}
	resolved := map[string]SlotResolution{}
	for _, set := range sets {
		g.declared[set.AddonID] = true
		g.roles[set.AddonID] = map[string]bool{}
		g.filled[set.AddonID] = map[string]providerRoleRef{}
		for _, d := range set.Provides {
			for role, tools := range d.Roles {
				for _, t := range tools {
					g.roles[set.AddonID][roleToolName(set.AddonID, t)] = true
					g.filled[set.AddonID][roleToolName(set.AddonID, t)] = providerRoleRef{Slot: d.Slot, Role: role, Props: d.Properties}
				}
			}
			res, ok := resolved[d.Slot]
			if !ok {
				res = s.ResolveProvider(ctx, user, d.Slot)
				if rec != nil {
					if narrowed := s.slotWiringOf(d.Slot).narrowed; narrowed != nil && narrowed(ctx, *rec) {
						res = slotOff(res, ProviderChosenOff, "the chat turns the slot off")
					}
				}
				resolved[d.Slot] = res
			}
			if res.Addon() == set.AddonID {
				g.serving[set.AddonID] = true
			}
		}
	}
	return g
}
