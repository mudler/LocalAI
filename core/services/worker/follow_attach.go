package worker

import (
	"cmp"
	"context"
	"errors"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
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
func (a *natsAttachment) InFlight() int   { return a.control.InFlight() }
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
	tls := natsTLSFor(local.TLS, h)
	if local.JWT != "" || local.Seed != "" {
		return connectNATS(url, local.JWT, local.Seed, "", "", local.Required, tls)
	}
	var opts []messaging.Option
	if h.Creds != nil && h.Creds.HasCredentials() {
		opts = append(opts, messaging.WithUserJWTProvider(h.Creds.Provider()))
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
func (a tunnelAttachment) InFlight() int { return a.f.control.InFlight() }
func (a tunnelAttachment) Close() error  { return a.f.Detach() }

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

// NATSReasons says why a worker cannot attach to NATS, or returns an empty
// string. routable is true when the frontends can dial the worker, which is how
// they reach it on NATS.
func NATSReasons(routable bool, local NATSLocal) func(CarrierView) string {
	return func(v CarrierView) string {
		switch {
		case !routable:
			return "this worker has no address that the frontends can dial: start it with --addr to let it follow a change to NATS"
		case v.NATSClientTLS && (local.TLS.Cert == "" || local.TLS.Key == ""):
			return "the NATS server asks for a client certificate, and this worker has none: set LOCALAI_NATS_TLS_CERT and LOCALAI_NATS_TLS_KEY"
		}
		return ""
	}
}

// registerFor builds the function that registers a worker for one carrier. The
// body is rebuilt for each registration, because it carries the load of the host.
func registerFor(client *workerregistry.RegistrationClient, body func() map[string]any, c cluster.Carrier) workerregistry.RegisterFunc {
	return func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
		b := body()
		b["carrier"] = string(c)
		return client.RegisterFull(ctx, b)
	}
}

// beatOf turns the answer to a registration into the news of a heartbeat.
func beatOf(res *workerregistry.RegisterResponse) *Beat {
	return &Beat{
		Carrier: res.Carrier, CarrierEpoch: res.CarrierEpoch, CarrierState: res.CarrierState,
		CarrierTarget: res.CarrierTarget, CarrierDraining: res.CarrierDraining, NatsClientTLS: res.NatsClientTLS,
	}
}
