// SPDX-License-Identifier: MIT

// Package testhost gives tests outside addons/ the host operation channel of
// an add-on, so they can run a connector add-on over a local broker. The types
// of the add-on SDK are internal; this package names them.
package testhost

import (
	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/hostclient"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

// Host is the host operation channel as a connector add-on takes it.
type Host = connector.Host

// Client returns the channel to the broker at endpoint.
func Client(endpoint, credential string) Host {
	return hostclient.New(protocol.Startup{HostEndpoint: endpoint, HostCredential: credential, Limits: contract.DefaultLimits()})
}
