package worker

import (
	"context"
	"net/http"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// VerbServer is a carrier on which a worker serves its control verbs. It is the
// exported face of the control server, for code that serves verbs of its own.
type VerbServer interface {
	// Carrier says which carrier this server is on.
	Carrier() cluster.Carrier
	// Handle serves a verb with one reply. A nil reply is a verb without one.
	Handle(verb string, h func(ctx context.Context, body []byte) (reply any, err error)) error
	// HandleWithProgress serves a verb that reports progress while it runs.
	HandleWithProgress(verb string, h func(ctx context.Context, body []byte, progress func(workerctl.BackendInstallProgressEvent)) (reply any, err error)) error
}

type verbServer struct {
	carrier cluster.Carrier
	cs      controlServer
}

func (v verbServer) Carrier() cluster.Carrier { return v.carrier }

func (v verbServer) Handle(verb string, h func(context.Context, []byte) (any, error)) error {
	return v.cs.handle(controlVerb(verb), h)
}

func (v verbServer) HandleWithProgress(verb string, h func(context.Context, []byte, func(workerctl.BackendInstallProgressEvent)) (any, error)) error {
	return v.cs.handleWithProgress(controlVerb(verb), func(ctx context.Context, body []byte, p progressSink) (any, error) {
		return h(ctx, body, p)
	})
}

// BackendPlane is what a backend worker holds to attach to either carrier: the
// control plane on its HTTP server, the tunnel behind it, and the NATS
// connection with the verbs on the subjects of the node. Run builds one, and so
// can any code that runs a worker's carriers with verbs of its own.
type BackendPlane struct {
	cfg    *Config
	nodeID string
	local  NATSLocal
	tunnel *tunnelFollower
	slot   *nodes.ControlSlot
	serve  func(controlServer) error
}

// NewBackendPlane returns the plane of a worker. httpAddr is where the HTTP
// server of the worker listens.
func NewBackendPlane(cfg *Config, nodeID, httpAddr string, local NATSLocal) *BackendPlane {
	p := &BackendPlane{cfg: cfg, nodeID: nodeID, local: local}
	p.tunnel = newTunnelFollower(tunnelFollowerOptions{Config: cfg, NodeID: nodeID, HTTPAddr: httpAddr})
	if p.tunnel.CanAttach() {
		p.slot = &nodes.ControlSlot{}
		p.tunnel.slot = p.slot
	}
	return p
}

// Control is the handler to mount on the HTTP server of the worker under the
// control prefix. It is nil when the worker has no frontend URL a tunnel can use.
func (p *BackendPlane) Control() http.Handler {
	if p.slot == nil {
		return nil
	}
	return p.slot
}

// CanTunnel reports whether the worker can attach to a tunnel at all.
func (p *BackendPlane) CanTunnel() bool { return p.tunnel.CanAttach() }

// Serve sets the verbs of the worker. They are claimed on each carrier when the
// worker attaches to it.
func (p *BackendPlane) Serve(verbs func(VerbServer) error) {
	p.serve = func(cs controlServer) error {
		c := cluster.CarrierNATS
		if _, ok := cs.(*httpControlServer); ok {
			c = cluster.CarrierTunnel
		}
		return verbs(verbServer{carrier: c, cs: cs})
	}
	p.tunnel.register = p.serve
}

// setServe sets the verbs of the worker from inside the package.
func (p *BackendPlane) setServe(serve func(controlServer) error) {
	p.serve = serve
	p.tunnel.register = serve
}

// Attachers returns the attacher of each carrier.
func (p *BackendPlane) Attachers() map[cluster.Carrier]Attacher {
	return map[cluster.Carrier]Attacher{
		cluster.CarrierNATS: natsAttacher(p.local, p.nodeID, func(cs controlServer) error {
			return p.serve(cs)
		}),
		cluster.CarrierTunnel: tunnelAttacher(p.tunnel),
	}
}

// Cannot says, for each carrier, why the worker cannot attach to it. routable is
// true when the frontends can dial the worker.
func (p *BackendPlane) Cannot(routable bool) map[cluster.Carrier]func(CarrierView) string {
	return map[cluster.Carrier]func(CarrierView) string{
		cluster.CarrierNATS: NATSReasons(routable, p.local),
		cluster.CarrierTunnel: func(CarrierView) string {
			if !p.tunnel.CanAttach() {
				return "this worker has no frontend URL that a tunnel can use"
			}
			return ""
		},
	}
}
