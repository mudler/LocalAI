// Package studiobot serves connector add-on runtimes (Telegram, Slack) to the
// connector manager of the chats in a test, over platform APIs that the test
// fakes. It lives outside core because the core does not depend on an add-on.
package studiobot

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"sync/atomic"

	"github.com/mudler/LocalAI/addons/testhost"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostops"

	slack "github.com/mudler/LocalAI/addons/slack/transport"
	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Addon is one connector add-on that a BotTransport serves: its ID, the kind
// it declares in its manifest, and its real runtime.
type Addon struct {
	ID      string
	Kind    contract.ConnectorKind
	Runtime contract.ConnectorRuntime
	// WithHost rebuilds the runtime over a host operation channel, as the
	// add-on's main does at start. Nil: the add-on cannot move files.
	WithHost func(testhost.Host) contract.ConnectorRuntime
	// Files is what the manifest declares for files, applied by EnableTransfers.
	Files *contract.ConnectorFiles
}

// Telegram is the Telegram add-on over api. Its kind has drafts, as the
// manifest declares, but no preview interval: the host's EditEvery applies,
// which a spec sets short. TelegramPaced keeps the interval of the manifest.
func Telegram(api *transport.API) Addon {
	return Addon{ID: "telegram", Kind: contract.ConnectorKind{ID: transport.Kind, Title: "Telegram", Delivery: contract.DeliveryPull, Drafts: true}, Runtime: transport.Runtime{API: api},
		WithHost: func(h testhost.Host) contract.ConnectorRuntime { return transport.Runtime{API: api, Host: h} },
		Files:    &contract.ConnectorFiles{Receive: &contract.FileAbility{MaxBytes: transport.MaxDownloadBytes}, Send: &contract.FileAbility{MaxBytes: 50 << 20}}}
}

// TelegramPaced is Telegram with the preview interval of the manifest.
func TelegramPaced(api *transport.API) Addon {
	a := Telegram(api)
	a.Kind.PreviewMs = 400
	return a
}

// Slack is the Slack add-on over api. The caller closes the runtime.
func Slack(rt *slack.Runtime) Addon {
	return Addon{ID: "slack", Kind: contract.ConnectorKind{ID: slack.Kind, Title: "Slack", Delivery: contract.DeliveryPull, History: &contract.ConnectorHistory{Thread: true, MaxMessages: 30}, Options: []contract.ConnectorOption{{
		ID: slack.FollowOption, Title: "Threads the bot follows", Default: slack.FollowOwn,
		Values: []contract.ConnectorOptionValue{{ID: slack.FollowOwn, Title: "Only threads it started"}, {ID: slack.FollowAny, Title: "Any thread it was mentioned in"}, {ID: slack.FollowNever, Title: "None"}}}}}, Runtime: rt,
		WithHost: func(h testhost.Host) contract.ConnectorRuntime { rt.Host = h; return rt },
		Files:    &contract.ConnectorFiles{Receive: &contract.FileAbility{MaxBytes: 1 << 30}, Send: &contract.FileAbility{MaxBytes: 1 << 30}}}
}

// BotTransport is a studio.ConnectorTransport that serves the real connector
// add-on runtimes in the process, over APIs that a test fakes. The tests of
// the chats therefore exercise the same transport code that the add-ons run.
type BotTransport struct {
	// mu guards addons and gateway: EnableTransfers rewrites them while the
	// loops of the manager already read them.
	mu      sync.RWMutex
	addons  []Addon
	gateway *gateway.Gateway
	running atomic.Bool
	// Addon is the ID of the first add-on, for the tests that serve one.
	Addon string
}

// SetRunning plays add-ons that are installed (true) or missing (false). The
// loops of the manager read it while a test changes it, hence the atomic.
func (b *BotTransport) SetRunning(on bool) { b.running.Store(on) }

// NewBotTransport returns a running Telegram add-on over api.
func NewBotTransport(api *transport.API) *BotTransport {
	return NewTransport(Telegram(api))
}

// NewTransport returns running add-ons.
func NewTransport(addons ...Addon) *BotTransport {
	b := &BotTransport{addons: addons}
	if len(addons) > 0 {
		b.Addon = addons[0].ID
	}
	b.running.Store(true)
	return b
}

// Kinds lists the kinds of the add-ons while they run.
func (b *BotTransport) Kinds(context.Context) ([]studio.ConnectorKind, error) {
	if !b.running.Load() {
		return nil, nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]studio.ConnectorKind, len(b.addons))
	for i, a := range b.addons {
		out[i] = studio.ConnectorKind{AddonID: a.ID, ConnectorKind: a.Kind}
	}
	return out, nil
}

// DeclareFiles makes the kinds declare the files ability of their manifests,
// for a spec that reads the limits and moves no byte.
func (b *BotTransport) DeclareFiles() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, a := range b.addons {
		b.addons[i].Kind.Files = a.Files
	}
}

// EnableTransfers makes the add-ons move files by handle, as installed ones do:
// the kinds declare the files ability, the add-ons get a host operation channel
// over a local server that serves the two file operations, and every call goes
// through the real gateway, which registers the handle for the call. t is the
// table; its blob store is the one the chats read the files from. The returned
// function stops the server.
func (b *BotTransport) EnableTransfers(t *hostops.Transfers) (stop func()) {
	br := hostops.New(hostops.NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
	if err := hostops.RegisterConnectorTransfer(br, t); err != nil {
		panic(err)
	}
	var hosts []*httptest.Server
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, a := range b.addons {
		b.addons[i].Kind.Files = a.Files
		if a.WithHost == nil {
			continue
		}
		h := httptest.NewServer(br.Handler(a.ID, "cred", []string{"connector."}))
		hosts = append(hosts, h)
		c := testhost.Client(h.URL, "cred")
		b.addons[i].Runtime = a.WithHost(c)
	}
	b.gateway = &gateway.Gateway{Connectors: gatewaySource{b}, ConnectorAuthority: allowAll{}, Transfers: t}
	return func() {
		for _, h := range hosts {
			h.Close()
		}
	}
}

// UseAuthority makes every call go through the real gateway with the given
// authority, as in the application. Without it a transport that moves no file
// calls the add-on directly and checks no scope.
func (b *BotTransport) UseAuthority(a gateway.ConnectorAuthority) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.gateway == nil {
		b.gateway = &gateway.Gateway{Connectors: gatewaySource{b}, ConnectorAuthority: a}
		return
	}
	b.gateway.ConnectorAuthority = a
}

type allowAll struct{}

func (allowAll) AuthorizeConnector(_ context.Context, _ studio.ConnectorCallScope, r contract.ConnectorRequest) (contract.ConnectorRequest, error) {
	return r, nil
}

// gatewaySource is the add-ons of the transport as the gateway's source.
type gatewaySource struct{ b *BotTransport }

func (g gatewaySource) ConnectorKinds(ctx context.Context) ([]addons.ConnectorKind, error) {
	kinds, err := g.b.Kinds(ctx)
	out := make([]addons.ConnectorKind, len(kinds))
	for i, k := range kinds {
		out[i] = addons.ConnectorKind{AddonID: k.AddonID, ConnectorKind: k.ConnectorKind}
	}
	return out, err
}

func (g gatewaySource) Connector(ctx context.Context, addonID string, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return g.b.run(ctx, addonID, req)
}

// Call runs the connector route of an add-on.
func (b *BotTransport) Call(ctx context.Context, scope studio.ConnectorCallScope, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	b.mu.RLock()
	gw := b.gateway
	b.mu.RUnlock()
	if gw != nil && b.running.Load() {
		return gw.ConnectorCall(ctx, scope, req)
	}
	return b.run(ctx, scope.AddonID, req)
}

func (b *BotTransport) run(ctx context.Context, addonID string, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	if b.running.Load() {
		b.mu.RLock()
		var rt contract.ConnectorRuntime
		for _, a := range b.addons {
			if a.ID == addonID {
				rt = a.Runtime
				break
			}
		}
		b.mu.RUnlock()
		if rt != nil {
			return rt.Connector(ctx, req)
		}
	}
	return contract.ConnectorResponse{}, errors.New("the add-on is not running")
}
