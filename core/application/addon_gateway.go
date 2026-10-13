// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/xlog"
)

func (a *Application) AddonGateway() *gateway.Gateway { return a.addonGateway }

// AddonPolicy is the owner-local resource policy. It is nil on frontends and on
// hosts without the addon transport, which then deny every resource.
func (a *Application) AddonPolicy() *policy.Policy { return a.addonPolicy }

// AddonOwnershipMode is the configured ownership mode. Named-user is the
// default; local-owner exists only when the deployment asked for it.
func (a *Application) AddonOwnershipMode() string {
	if m := a.applicationConfig.Addons.OwnershipMode; m != "" {
		return m
	}
	return auth.AddonModeNamedUser
}

// addonStatus is the manager view that creation authority consults.
type addonStatus interface {
	Status(ctx context.Context, id string) (addons.Status, error)
}

// addonCreateAuthority lets an actor create a resource only for an addon that
// is installed and enabled on the owner, with no lifecycle change in flight.
// A manager that cannot answer is unavailable, never a grant.
func addonCreateAuthority(status addonStatus, revalidate func(context.Context, contract.Actor) error) func(context.Context, contract.Actor, string) error {
	return func(ctx context.Context, actor contract.Actor, addonID string) error {
		if status == nil || revalidate == nil {
			return gateway.ErrForbidden
		}
		if err := revalidate(ctx, actor); err != nil {
			return err
		}
		s, err := status.Status(ctx, addonID)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("addon status: %w: %w", gateway.ErrUnavailable, err)
		}
		if !s.Installed || !s.DesiredEnabled || !s.StartEligible {
			return gateway.ErrForbidden
		}
		return nil
	}
}

func (a *Application) startAddonGateway() error {
	cfg := a.applicationConfig.Addons
	if cfg.Transport == nil {
		return nil
	}
	mode := a.AddonOwnershipMode()
	if mode == auth.AddonModeNamedUser && a.authDB == nil {
		return gateway.ErrForbidden
	}
	c := *cfg.Transport
	if c.InstanceID != cfg.InstanceID || c.OwnerID != cfg.OwnerID || c.OwnerAddress != cfg.OwnerAddress {
		return gateway.ErrForbidden
	}
	carrier, e := gateway.NewCarrier(c)
	if e != nil {
		return e
	}
	if mode == auth.AddonModeLocalOwner {
		xlog.Warn(localOwnerWarning)
	}
	g := &gateway.Gateway{Limits: c.Limits, Manager: a.addonManager, Revalidate: auth.AddonRevalidate(a.authDB, mode)}
	if cfg.InstanceID != cfg.OwnerID {
		g.Forward = carrier
		a.addonGateway = g
		return nil
	}
	// Only the owner holds the policy store. Other frontends reach it through the
	// gateway, and without it the gateway keeps denying every resource.
	if !filepath.IsAbs(cfg.Root) || cfg.ListenAddress == "" {
		return gateway.ErrForbidden
	}
	store, e := policy.OpenStore(filepath.Join(cfg.Root, "policy"), policy.Mode(mode))
	if e != nil {
		return e
	}
	// Proposals are durable beside the policy store and held only here. The
	// broker is the policy's proposal invalidator, so a revocation cancels the
	// pending proposals of that user before it is acknowledged.
	brokerOpts := policy.BrokerOptions{}
	if a.addonManager != nil {
		brokerOpts.Runtime = a.addonManager
	}
	broker, e := policy.OpenBroker(filepath.Join(cfg.Root, "proposals"), brokerOpts)
	if e != nil {
		_ = store.Close()
		return e
	}
	p := policy.New(store, policy.Options{
		Revalidate:      g.Revalidate,
		CreateAuthority: addonCreateAuthority(a.addonManager, g.Revalidate),
		Proposals:       broker,
	})
	broker.Attach(p)
	closeState := func() { _ = broker.Close(); _ = store.Close() }
	// Registrations left pending by a crash are settled before anything is
	// served. Without a backing-resource check they are dropped and committed
	// resources are kept; the check is supplied by the code that creates them.
	exists := a.addonResourceExists
	if exists == nil {
		exists = func(context.Context, policy.ResourceKey) (bool, error) { return false, nil }
	}
	if e = p.Reconcile(a.applicationConfig.Context, exists); e != nil {
		closeState()
		return errors.Join(e, addons.ErrRecoveryRequired)
	}
	// A proposal that was executing when the owner stopped is settled by
	// receipt lookup, or recorded as outcome-unknown. Nothing is applied again.
	if e = broker.Recover(a.applicationConfig.Context); e != nil {
		closeState()
		return errors.Join(e, addons.ErrRecoveryRequired)
	}
	// Unresolved outcomes are looked up again with backoff for a finite
	// window, and expiry stays recorded. Run ends with the application
	// context or when the broker is closed.
	go broker.Run(a.applicationConfig.Context)
	provider := p.Gateway()
	g.Admission, g.Resolver = provider, provider
	g.Proposals = broker.Gateway()
	handler, e := carrier.OwnerHandler(g)
	if e != nil {
		closeState()
		return e
	}
	listener, e := net.Listen("tcp", cfg.ListenAddress)
	if e != nil {
		closeState()
		return e
	}
	server := &http.Server{Handler: handler, TLSConfig: carrier.ServerTLS(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10}
	a.addonServer = server
	a.addonGateway = g
	a.addonPolicy, a.addonStore = p, store
	go func() { _ = server.Serve(tls.NewListener(listener, server.TLSConfig)) }()
	go func() { <-a.applicationConfig.Context.Done(); _ = server.Close(); closeState() }()
	return nil
}

// localOwnerWarning is logged at startup in local-owner mode. There is no
// session in that mode, so the confirmation boundary is the browser headers
// only and does not stop a script that sets them.
const localOwnerWarning = "addons: local-owner mode is enabled; unauthenticated callers share one authority over every addon resource and sharing is disabled; any caller that can reach this port can confirm addon proposals"
