// Package egress is the network policy of the commands a chat runs in the
// Sandbox. The guest has no network device; its only way out is a proxy on the
// host, and every connection the proxy is asked for is decided here: by the
// administrator's ceiling, the chat's preset, the rules the person stored for
// the chat and the answers to the cards that asked them. The package holds the
// policy and nothing that does I/O, so the same decision is made on every
// frontend.
package egress

import (
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/web/safehttp"
)

// Preset is how much of the network the commands of a chat may reach.
type Preset string

// The presets, from the narrowest.
const (
	PresetOff        Preset = "off"
	PresetRegistries Preset = "registries"
	PresetAll        Preset = "all"
)

// Rank orders the presets: off 0, registries 1, all 2. An unknown preset is off.
func (p Preset) Rank() int {
	switch p {
	case PresetRegistries:
		return 1
	case PresetAll:
		return 2
	}
	return 0
}

// Valid says whether p is one of the presets.
func (p Preset) Valid() bool { return p == PresetOff || p == PresetRegistries || p == PresetAll }

// Min returns the narrower of two presets. An unknown preset counts as off.
func Min(a, b Preset) Preset {
	if a.Rank() <= b.Rank() {
		return normal(a)
	}
	return normal(b)
}

func normal(p Preset) Preset {
	if !p.Valid() {
		return PresetOff
	}
	return p
}

// The kinds of a request.
const (
	KindConnect = "connect" // CONNECT host:port, a tunnel
	KindHTTP    = "http"    // an absolute-form plain HTTP request
)

// Request is one connection a command asked the proxy for.
type Request struct {
	Host string // canonical: lower case, no trailing dot, punycode
	Port int
	Kind string
	// TLS is true for a CONNECT to a TLS port; a plain HTTP request is not.
	TLS bool
	// Private is set when an earlier dial of this host found private answers:
	// the host can only be reached with the private switch and an
	// administrator's rule or card answer.
	Private bool
}

// Rule is a person's answer for one host and port, kept with the chat. An
// allow rule lets the host through; a deny rule blocks it without asking again.
type Rule struct {
	Host  string    `json:"host"`
	Port  int       `json:"port"`
	Allow bool      `json:"allow"`
	By    string    `json:"by,omitempty"`
	Admin bool      `json:"admin,omitempty"` // the person who stored it was an administrator
	At    time.Time `json:"at"`
	Card  string    `json:"card,omitempty"` // the card it answered, if any
}

// Grant is an "allow once": the host is allowed for the rest of one command
// (Op is the operation of that call). A late answer grants the next call of
// the chat: Op is empty until a call takes it, and it lapses at Expires.
type Grant struct {
	Host    string    `json:"host"`
	Port    int       `json:"port"`
	Op      string    `json:"op,omitempty"`
	Admin   bool      `json:"admin,omitempty"`
	Expires time.Time `json:"expires"`
}

// The states of a card.
const (
	CardPending = "pending"
	CardOnce    = "once"
	CardChat    = "chat"
	CardDenied  = "denied"
	CardExpired = "expired"
	// CardRefused: the ceiling was lowered under the host while the card waited.
	CardRefused = "refused"
)

// Card asks the person whether a command of the chat may reach one host.
type Card struct {
	ID         string    `json:"id"`
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	Kind       string    `json:"kind"`
	State      string    `json:"state"`
	Private    bool      `json:"private,omitempty"` // only an administrator may allow it
	Op         string    `json:"op,omitempty"`      // the call that asked
	ToolSeq    int64     `json:"tool_seq,omitempty"`
	AskedAt    time.Time `json:"asked_at"`
	AnsweredAt time.Time `json:"answered_at,omitempty"`
	AnsweredBy string    `json:"answered_by,omitempty"`
}

// ChatState is what a chat keeps about its network: the preset, the rules,
// the cards and the "once" grants. It lives in the chat record.
type ChatState struct {
	Preset Preset  `json:"preset,omitempty"` // empty: off
	Rules  []Rule  `json:"rules,omitempty"`
	Cards  []Card  `json:"cards,omitempty"`
	Once   []Grant `json:"once,omitempty"`
}

// Limits of the state of one chat.
const (
	MaxRules        = 200
	MaxCards        = 50
	MaxPendingCards = 3
	MaxGrants       = 50
	// LateOnce is how long a late "allow once" waits for the next call.
	LateOnce = 10 * time.Minute
)

// Clone returns a deep copy of s.
func (s ChatState) Clone() ChatState {
	s.Rules = append([]Rule(nil), s.Rules...)
	s.Cards = append([]Card(nil), s.Cards...)
	s.Once = append([]Grant(nil), s.Once...)
	return s
}

// Empty says whether s holds nothing.
func (s ChatState) Empty() bool {
	return s.Preset == "" && len(s.Rules) == 0 && len(s.Cards) == 0 && len(s.Once) == 0
}

// Rule returns the rule of a host and port.
func (s ChatState) Rule(host string, port int) (Rule, bool) {
	for _, r := range s.Rules {
		if r.Host == host && r.Port == port {
			return r, true
		}
	}
	return Rule{}, false
}

// PutRule stores r, replacing the rule of the same host and port. The oldest
// rule gives way past MaxRules.
func (s *ChatState) PutRule(r Rule) {
	out := s.Rules[:0:0]
	for _, x := range s.Rules {
		if x.Host != r.Host || x.Port != r.Port {
			out = append(out, x)
		}
	}
	out = append(out, r)
	if n := len(out) - MaxRules; n > 0 {
		out = out[n:]
	}
	s.Rules = out
}

// DeleteRule removes the rule of a host and port and says whether one was there.
func (s *ChatState) DeleteRule(host string, port int) bool {
	out := s.Rules[:0:0]
	found := false
	for _, x := range s.Rules {
		if x.Host == host && x.Port == port {
			found = true
			continue
		}
		out = append(out, x)
	}
	s.Rules = out
	return found
}

// Card returns the card with an ID.
func (s ChatState) Card(id string) (Card, bool) {
	for _, c := range s.Cards {
		if c.ID == id {
			return c, true
		}
	}
	return Card{}, false
}

// PutCard stores c, replacing the card with the same ID. Past MaxCards the
// oldest answered cards are dropped; the audit keeps them.
func (s *ChatState) PutCard(c Card) {
	replaced := false
	for i := range s.Cards {
		if s.Cards[i].ID == c.ID {
			s.Cards[i] = c
			replaced = true
		}
	}
	if !replaced {
		s.Cards = append(s.Cards, c)
	}
	for len(s.Cards) > MaxCards {
		drop := -1
		for i, x := range s.Cards {
			if x.State != CardPending {
				drop = i
				break
			}
		}
		if drop < 0 {
			break
		}
		s.Cards = append(s.Cards[:drop], s.Cards[drop+1:]...)
	}
}

// Pending returns the cards that wait for an answer.
func (s ChatState) Pending() []Card {
	var out []Card
	for _, c := range s.Cards {
		if c.State == CardPending {
			out = append(out, c)
		}
	}
	return out
}

// Grants returns the "once" grants that apply to the call op at now: the ones
// of that call, and a late one no call took yet.
func (s ChatState) Grants(op string, now time.Time) []Grant {
	var out []Grant
	for _, g := range s.Once {
		if !now.Before(g.Expires) {
			continue
		}
		if g.Op == op || g.Op == "" {
			out = append(out, g)
		}
	}
	return out
}

// AddGrant stores g. Lapsed grants are dropped, and the oldest past MaxGrants.
func (s *ChatState) AddGrant(g Grant, now time.Time) {
	out := s.Once[:0:0]
	for _, x := range s.Once {
		if now.Before(x.Expires) {
			out = append(out, x)
		}
	}
	out = append(out, g)
	if n := len(out) - MaxGrants; n > 0 {
		out = out[n:]
	}
	s.Once = out
}

// Settings are the administrator's limits, from the runtime settings.
type Settings struct {
	// Ceiling is the widest preset any chat may use.
	Ceiling Preset
	// Default is the preset of a new chat, capped by the ceiling.
	Default Preset
	// AllowPrivate lets an administrator's rule or card answer reach private
	// addresses. A preset alone never does.
	AllowPrivate bool
	// Ports are the ports a command may reach. Empty: 80 and 443.
	Ports []int
	// ExtraRegistries are host names added to the registries list.
	ExtraRegistries []string
}

// PortAllowed says whether port is one of the settings' ports.
func (s Settings) PortAllowed(port int) bool {
	ports := s.Ports
	if len(ports) == 0 {
		ports = []int{80, 443}
	}
	return slices.Contains(ports, port)
}

// DefaultPreset is the preset of a new chat: the default, capped by the ceiling.
func (s Settings) DefaultPreset() Preset { return Min(normal(s.Default), s.Ceiling) }

// The actions of a verdict.
const (
	Allow = "allow"
	Block = "block"
	Ask   = "ask"
)

// The reasons of a verdict, and of the audit lines the proxy adds.
const (
	ReasonPreset        = "preset"
	ReasonRule          = "rule"
	ReasonOnce          = "once"
	ReasonCard          = "card" // ask: a card was shown
	ReasonDenied        = "denied"
	ReasonPort          = "port"
	ReasonCeiling       = "ceiling"
	ReasonOff           = "off"
	ReasonRegistriesTLS = "registries-tls"
	ReasonUnattended    = "unattended"
	ReasonCardsFull     = "cards-full"
	ReasonNoAnswer      = "no-answer"
	ReasonPrivate       = "private"
	ReasonSelf          = "self"
	ReasonMetadata      = "metadata"
	ReasonResolve       = "resolve"
	ReasonSNI           = "sni"
	ReasonIdle          = "idle"
	ReasonHost          = "host"
	ReasonLimit         = "limit"
)

// Verdict is the decision about one request.
type Verdict struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
	// AllowPrivate says the dial may reach private addresses. It is true only
	// with the private switch on and an administrator's rule or answer.
	AllowPrivate bool `json:"allowPrivate,omitempty"`
}

// Chat is what Decide needs to know about the chat of a request.
type Chat struct {
	// Preset is the chat's preset (for a sub-agent its parent's, for an
	// unattended run its agent's).
	Preset Preset
	Rules  []Rule
	// Once holds the grants that apply to the call in progress.
	Once []Grant
	// Attended says a person watches the chat, so a card can ask them.
	Attended bool
	// Pending holds the cards that wait for an answer.
	Pending []Card
}

// privateLiteral says whether host is an IP literal outside the public internet.
func privateLiteral(host string) bool {
	a, err := netip.ParseAddr(host)
	return err == nil && !safehttp.Allowed(a, false)
}

// needs is the lowest ceiling that can admit a host: registries for a
// registry host on TLS, all for anything else.
func needs(r Request, reg *Registries, extra []string) Preset {
	if r.TLS && reg.Has(r.Host, extra) {
		return PresetRegistries
	}
	return PresetAll
}

// Decide decides one request. The first match wins: the port, a deny rule,
// the effective preset (the chat's preset under the ceiling), an allow rule or
// a "once" grant the ceiling still admits, then a card when one can ask, else
// a block.
func Decide(s Settings, c Chat, r Request, reg *Registries) Verdict {
	if !s.PortAllowed(r.Port) {
		return Verdict{Action: Block, Reason: ReasonPort}
	}
	for _, x := range c.Rules {
		if x.Host == r.Host && x.Port == r.Port && !x.Allow {
			return Verdict{Action: Block, Reason: ReasonDenied}
		}
	}
	ceiling := normal(s.Ceiling)
	eff := Min(c.Preset, ceiling)
	private := r.Private || privateLiteral(r.Host)
	registry := reg.Has(r.Host, s.ExtraRegistries)
	if !private {
		if registry && eff.Rank() >= PresetRegistries.Rank() {
			if r.TLS {
				return Verdict{Action: Allow, Reason: ReasonPreset}
			}
			if eff != PresetAll {
				return Verdict{Action: Block, Reason: ReasonRegistriesTLS}
			}
		}
		if eff == PresetAll {
			return Verdict{Action: Allow, Reason: ReasonPreset}
		}
	}
	need := needs(r, reg, s.ExtraRegistries)
	if private {
		need = PresetAll
	}
	admits := ceiling.Rank() >= need.Rank() && (!private || s.AllowPrivate)
	for _, x := range c.Rules {
		if x.Host == r.Host && x.Port == r.Port && x.Allow && admits && (!private || x.Admin) {
			return Verdict{Action: Allow, Reason: ReasonRule, AllowPrivate: s.AllowPrivate && x.Admin}
		}
	}
	for _, g := range c.Once {
		if g.Host == r.Host && g.Port == r.Port && admits && (!private || g.Admin) {
			return Verdict{Action: Allow, Reason: ReasonOnce, AllowPrivate: s.AllowPrivate && g.Admin}
		}
	}
	if !admits {
		if ceiling == PresetOff || ceiling.Rank() < need.Rank() {
			return Verdict{Action: Block, Reason: ReasonCeiling}
		}
		return Verdict{Action: Block, Reason: ReasonPrivate}
	}
	if !c.Attended {
		return Verdict{Action: Block, Reason: ReasonUnattended}
	}
	if len(c.Pending) >= MaxPendingCards {
		return Verdict{Action: Block, Reason: ReasonCardsFull}
	}
	return Verdict{Action: Ask, Reason: ReasonCard}
}

// Admits says whether the ceiling of s still admits a card or a rule for r:
// the check a card answer makes again, since the ceiling may have changed
// while the card waited.
func Admits(s Settings, r Request, reg *Registries) bool {
	private := r.Private || privateLiteral(r.Host)
	need := needs(r, reg, s.ExtraRegistries)
	if private {
		need = PresetAll
	}
	return normal(s.Ceiling).Rank() >= need.Rank() && (!private || s.AllowPrivate)
}

// Describe is the one line the model reads about the network of its commands.
func Describe(p Preset, reg *Registries) string {
	switch normal(p) {
	case PresetRegistries:
		return "Network for commands: package registries only (" + strings.Join(reg.Titles(), ", ") +
			"). A blocked host asks the user; do not try to get around the proxy."
	case PresetAll:
		return "Network for commands: all public sites, through the proxy the sandbox sets. A blocked host asks the user; do not try to get around the proxy."
	}
	return "Network for commands: off. A blocked host asks the user; do not try to get around the proxy."
}

// Hint is the sentence after Describe in the turn's note: which tool to use
// for what. With the network off, curl and the installers cannot reach
// anything, so the sentence says that instead. web says whether the chat has
// web_fetch or web_search (the built-in ones or an add-on's): only then are
// they named.
func Hint(p Preset, web bool) string {
	if normal(p) == PresetOff {
		return "curl, pip, npm and git cannot reach anything now; ask the user to turn the network on if a task needs it."
	}
	if web {
		return "curl, pip, npm and git work in the sandbox through the proxy. To read or search web pages use web_fetch or web_search; use curl for APIs, downloads and installs."
	}
	return "curl, pip, npm and git work in the sandbox through the proxy."
}
