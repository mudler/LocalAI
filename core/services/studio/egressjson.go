package studio

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/egress/audit"
)

// The JSON forms of the Sandbox network, shared by the routes and the
// in-process assistant tools so that both answer the same.

func registriesJSON(list []egress.Registry) []schema.ChatsEgressRegistry {
	out := make([]schema.ChatsEgressRegistry, 0, len(list))
	for _, r := range list {
		out = append(out, schema.ChatsEgressRegistry{ID: r.ID, Title: r.Title, Hosts: append([]string{}, r.Hosts...)})
	}
	return out
}

// EgressCardJSON is the JSON form of a card.
func EgressCardJSON(c egress.Card) schema.ChatsEgressCard {
	out := schema.ChatsEgressCard{ID: c.ID, Host: c.Host, Port: c.Port, Kind: c.Kind, State: c.State, Private: c.Private, ToolSeq: c.ToolSeq,
		AskedAt: c.AskedAt, AnsweredBy: c.AnsweredBy}
	if !c.AnsweredAt.IsZero() {
		at := c.AnsweredAt
		out.AnsweredAt = &at
	}
	return out
}

// JSON is the answer of GET /api/chats/{id}/egress.
func (v EgressView) JSON() schema.ChatsEgress {
	out := schema.ChatsEgress{Preset: string(v.Preset), Ceiling: string(v.Ceiling), Effective: string(v.Effective), Applies: v.Applies,
		AppliesReason: v.AppliesReason, Registries: registriesJSON(v.Registries), AllowedHosts: v.AllowedHosts, Rules: []schema.ChatsEgressRule{}, Cards: []schema.ChatsEgressCard{}}
	for _, r := range v.Rules {
		out.Rules = append(out.Rules, schema.ChatsEgressRule{Host: r.Host, Port: r.Port, Allow: r.Allow, By: r.By, Admin: r.Admin, At: r.At, AboveLimit: r.AboveLimit})
	}
	for _, c := range v.Cards {
		card := EgressCardJSON(c)
		card.AboveCeiling = v.CardAbove[c.ID]
		out.Cards = append(out.Cards, card)
	}
	return out
}

// JSON is the answer of GET /api/chats/admin/egress.
func (a EgressAdmin) JSON() schema.ChatsEgressAdmin {
	extra := a.Settings.ExtraRegistries
	if extra == nil {
		extra = []string{}
	}
	return schema.ChatsEgressAdmin{
		Ceiling: string(a.Settings.Ceiling), Default: string(a.Settings.Default), AllowPrivate: a.Settings.AllowPrivate,
		Ports: a.Settings.Ports, ExtraRegistries: extra, AllowedHosts: a.Settings.AllowedHostList(), AuditDays: a.AuditDays, Registries: registriesJSON(a.Registries),
		Sandbox: a.Sandbox, Isolation: a.Isolation, Allowed: a.Last24h.Allowed, Blocked: a.Last24h.Blocked,
	}
}

// EgressEventsJSON is the answer of GET /api/chats/admin/egress/events.
func (s *Service) EgressEventsJSON(ctx context.Context, evs []audit.Event) schema.ChatsEgressEventList {
	out := schema.ChatsEgressEventList{Events: make([]schema.ChatsEgressEvent, 0, len(evs))}
	for _, e := range evs {
		out.Events = append(out.Events, schema.ChatsEgressEvent{User: e.User, UserName: s.UserName(ctx, e.User), Chat: e.Chat, Host: e.Host, Port: e.Port,
			Kind: e.Kind, Verdict: e.Verdict, Reason: e.Reason, Addr: e.Addr, BytesUp: e.BytesUp, BytesDown: e.BytesDown, DurationMS: e.DurationMS,
			At: e.At.UTC().Truncate(time.Millisecond)})
	}
	return out
}
