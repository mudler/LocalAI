package worker

import (
	"cmp"
	"context"
	"errors"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/xlog"
)

// natsAttachment is a worker's connection to NATS with its control verbs served
// on the subjects of the node.
type natsAttachment struct {
	client  *messaging.Client
	control *natsControlServer
	cancel  context.CancelFunc
	failed  atomic.Bool
}

func (a *natsAttachment) Connected() bool { return a.client.IsConnected() }
func (a *natsAttachment) Failed() bool    { return a.failed.Load() }
func (a *natsAttachment) Close() error {
	a.cancel()
	a.client.Close()
	return nil
}

// NATSLocal is what an operator can set on a worker about NATS. It overrides what
// the frontend hands over.
type NATSLocal struct {
	URL      string
	JWT      string
	Seed     string
	Required bool
	TLS      messaging.TLSFiles
	// FallbackJWT and FallbackSeed are used when the frontend minted no
	// credential, as an operator of an agent worker may configure.
	FallbackJWT  string
	FallbackSeed string
}

// natsTLSFor returns the TLS files to use: the files of the operator, and the CA
// that the frontend handed over when the operator gave none.
func natsTLSFor(local messaging.TLSFiles, h Handover) messaging.TLSFiles {
	tls := local
	if tls.CA == "" && h.NATSCAPEM != "" {
		tls.CAPEM = []byte(h.NATSCAPEM)
	}
	return tls
}

// ConnectNATS connects to NATS as a worker. The address and the CA of the
// operator win over the ones the frontend handed over. The credential comes from
// the environment when it is set there, and otherwise from the manager, which
// keeps it fresh. onFatal is called when a refresh fails for good.
func ConnectNATS(ctx context.Context, local NATSLocal, h Handover, onFatal func()) (*messaging.Client, error) {
	url := cmp.Or(local.URL, h.NATSURL)
	if url == "" {
		return nil, errors.New("no NATS URL: set LOCALAI_NATS_URL, or set the NATS address in the cluster settings")
	}
	// The frontend cannot hand over a client certificate, so a server that asks
	// for one is reached only with the files of the operator.
	if h.NATSClientTLS && (local.TLS.Cert == "" || local.TLS.Key == "") {
		return nil, errors.New("the NATS server asks for a client certificate, and this worker has none: set LOCALAI_NATS_TLS_CERT and LOCALAI_NATS_TLS_KEY")
	}
	tls := natsTLSFor(local.TLS, h)
	if local.JWT != "" || local.Seed != "" {
		return connectNATS(url, local.JWT, local.Seed, "", "", local.Required, tls)
	}
	var opts []messaging.Option
	if h.Creds != nil && h.Creds.HasCredentials() {
		opts = append(opts, messaging.WithUserJWTProvider(h.Creds.Provider()))
	} else if local.FallbackJWT != "" || local.FallbackSeed != "" {
		if (local.FallbackJWT == "") != (local.FallbackSeed == "") {
			return nil, errors.New("the fallback NATS JWT and seed must be set together")
		}
		opts = append(opts, messaging.WithUserJWT(local.FallbackJWT, local.FallbackSeed))
	} else if local.Required {
		return nil, errors.New("NATS JWT+seed required: set LOCALAI_NATS_JWT/LOCALAI_NATS_USER_SEED or enable frontend minting")
	}
	if tls.Enabled() {
		opts = append(opts, messaging.WithTLS(tls))
	}
	client, err := messaging.New(url, opts...)
	if err == nil && h.Creds != nil && h.Creds.HasCredentials() {
		go func() {
			if err := h.Creds.RefreshLoop(ctx); err != nil {
				xlog.Error("NATS credential refresh permanently failed", "error", err)
				onFatal()
			}
		}()
	}
	return client, err
}

// natsAttacher attaches a backend worker to NATS and serves the verbs on it.
func natsAttacher(local NATSLocal, nodeID string, serve func(controlServer) error) Attacher {
	return func(ctx context.Context, h Handover) (Attachment, error) {
		actx, cancel := context.WithCancel(ctx)
		a := &natsAttachment{cancel: cancel}
		client, err := ConnectNATS(actx, local, h, func() { a.failed.Store(true) })
		if err != nil {
			cancel()
			return nil, err
		}
		a.client = client
		a.control = newNATSControlServer(client, nodeID)
		if err := serve(a.control); err != nil {
			_ = a.Close()
			return nil, err
		}
		return a, nil
	}
}

// tunnelAttachment is a worker's tunnel with its control plane mounted behind it.
type tunnelAttachment struct{ f *tunnelFollower }

func (a tunnelAttachment) Connected() bool {
	a.f.mu.Lock()
	t := a.f.tunnel
	a.f.mu.Unlock()
	return t.Connected()
}
func (a tunnelAttachment) Close() error { return a.f.Detach() }

// TunnelRoute says how the tunnel travels: over P2P with the path that the P2P
// layer found, for a worker that joined with an invite, and over a websocket
// otherwise.
func (a tunnelAttachment) TunnelRoute() nodes.TunnelRoute {
	if a.f.cfg != nil && a.f.cfg.join != nil {
		return a.f.cfg.join.Route()
	}
	return nodes.TunnelRoute{Transport: nodes.TunnelWebSocket}
}

// tunnelAttacher attaches a backend worker to a tunnel.
func tunnelAttacher(f *tunnelFollower) Attacher {
	return func(ctx context.Context, h Handover) (Attachment, error) {
		if h.Creds == nil {
			return nil, ErrNoCredentials
		}
		f.UseCredentials(h.Creds.TunnelToken, h.Creds.Reregister)
		if err := f.Attach(ctx); err != nil {
			return nil, err
		}
		return tunnelAttachment{f}, nil
	}
}

// registerFor builds the function that registers a worker and asks for the
// credentials of the given routes. The body is rebuilt for each registration,
// because it carries the load of the host. A registration that names no route
// gets the identity of the node and no credential.
func registerFor(client *workerregistry.RegistrationClient, body func() map[string]any, routes ...Route) workerregistry.RegisterFunc {
	names := make([]string, 0, len(routes))
	for _, r := range routes {
		names = append(names, string(r))
	}
	return func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
		b := body()
		b["routes"] = names
		return client.RegisterFull(ctx, b)
	}
}
