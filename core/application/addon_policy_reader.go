// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"fmt"
	"maps"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/sandboxpolicy"
)

// studioEgressSettings reads a fresh bounded owner policy on every request.
// The no-addon server retains its existing built-in settings; an enabled
// addon deployment must never substitute frontend configuration on failure.
func (a *Application) studioEgressSettings(ctx context.Context) (egress.Settings, error) {
	if err := ctx.Err(); err != nil {
		return egress.Settings{}, err
	}
	if !a.applicationConfig.Addons.Enabled {
		return a.localStudioEgressSettings(), nil
	}
	snapshot, err := a.readAddonHostPolicy(ctx)
	if err != nil {
		return egress.Settings{}, err
	}
	p := snapshot.Policy
	ports, err := config.ParseEgressPorts(p.Ports)
	if err != nil {
		return egress.Settings{}, fmt.Errorf("owner egress ports: %w", err)
	}
	extra, err := config.ParseEgressHosts(p.ExtraRegistries)
	if err != nil {
		return egress.Settings{}, fmt.Errorf("owner egress registries: %w", err)
	}
	hosts, err := config.ParseEgressAllowedHosts(p.AllowedHosts, true)
	if err != nil {
		return egress.Settings{}, fmt.Errorf("owner egress hosts: %w", err)
	}
	out := egress.Settings{Ceiling: egress.Preset(p.Ceiling), Default: egress.Preset(p.Default), AllowPrivate: p.AllowPrivate, Ports: ports, ExtraRegistries: extra}
	if p.Ceiling != "" && !out.Ceiling.Valid() || p.Default != "" && !out.Default.Valid() {
		return egress.Settings{}, gateway.ErrUnavailable
	}
	for _, h := range hosts {
		out.AllowedHosts = append(out.AllowedHosts, egress.AllowedHost{Host: h.Host, Port: h.Port})
	}
	return out, nil
}

// studioSandboxPolicy is the administrator's policy for sandbox profiles, from
// the owner policy. Without add-ons there is no sandbox with profiles, and the
// open default stands. When the owner cannot be read the error goes up: the
// caller runs the fallback profile rather than a profile the policy may close.
func (a *Application) studioSandboxPolicy(ctx context.Context) (sandboxpolicy.Policy, error) {
	if err := ctx.Err(); err != nil {
		return sandboxpolicy.Policy{}, err
	}
	if !a.applicationConfig.Addons.Enabled {
		return sandboxpolicy.Policy{}, nil
	}
	snapshot, err := a.readAddonHostPolicy(ctx)
	if err != nil {
		return sandboxpolicy.Policy{}, err
	}
	return snapshot.Policy.SandboxPolicy(), nil
}

// studioToolPins never substitutes frontend pins when the owner is unavailable.
func (a *Application) studioToolPins(ctx context.Context) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !a.applicationConfig.Addons.Enabled {
		return maps.Clone(a.applicationConfig.ChatsToolProviders), nil
	}
	snapshot, err := a.readAddonHostPolicy(ctx)
	if err != nil {
		return nil, err
	}
	return maps.Clone(snapshot.Policy.ToolProviders), nil
}

// studioConnectorFilesCeiling is the host ceiling of a connector file, from
// the owner policy. Without add-ons there is no owner policy and no connector
// to move a file, so the default stands.
func (a *Application) studioConnectorFilesCeiling(ctx context.Context) (int64, error) {
	n, err := a.policyFilesCeiling(ctx)
	if err != nil {
		return 0, err
	}
	// A file that a sender shares becomes a file of the chat, so the ceiling
	// never passes what one file of a workspace may be.
	return min(n, a.studioUploadLimit()), nil
}

func (a *Application) policyFilesCeiling(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !a.applicationConfig.Addons.Enabled {
		return contract.DefaultFileCeilingBytes, nil
	}
	snapshot, err := a.readAddonHostPolicy(ctx)
	if err != nil {
		return 0, err
	}
	return snapshot.Policy.FilesCeiling(), nil
}

// readAddonHostPolicy is shared by host consumers; the DTO keeps revision
// and policy together and excludes owner-only migration provenance.
func (a *Application) readAddonHostPolicy(ctx context.Context) (gateway.HostSettingsPolicy, error) {
	if a.addonGateway == nil {
		return gateway.HostSettingsPolicy{}, gateway.ErrUnavailable
	}
	return a.addonGateway.ReadHostSettingsInternal(ctx)
}
