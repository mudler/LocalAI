// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"crypto/tls"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"net"
	"net/http"
	"time"
)

func (a *Application) AddonGateway() *gateway.Gateway { return a.addonGateway }
func (a *Application) startAddonGateway() error {
	cfg := a.applicationConfig.Addons
	if cfg.Transport == nil {
		return nil
	}
	if a.authDB == nil {
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
	g := &gateway.Gateway{Limits: c.Limits, Manager: a.addonManager, Revalidate: func(ctx context.Context, actor contract.Actor) error {
		return auth.ValidateAddonActor(ctx, a.authDB, actor, false)
	}}
	if cfg.InstanceID != cfg.OwnerID {
		g.Forward = carrier
		a.addonGateway = g
		return nil
	}
	handler, e := carrier.OwnerHandler(g)
	if e != nil {
		return e
	}
	if cfg.ListenAddress == "" {
		return gateway.ErrForbidden
	}
	listener, e := net.Listen("tcp", cfg.ListenAddress)
	if e != nil {
		return e
	}
	server := &http.Server{Handler: handler, TLSConfig: carrier.ServerTLS(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10}
	a.addonServer = server
	a.addonGateway = g
	go func() { _ = server.Serve(tls.NewListener(listener, server.TLSConfig)) }()
	go func() { <-a.applicationConfig.Context.Done(); _ = server.Close() }()
	return nil
}
