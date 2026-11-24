package cluster

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/xlog"
)

// p2pHelloTimeout bounds the hello of a tunnel stream over P2P, and the
// checks and the reply that follow it.
const p2pHelloTimeout = 10 * time.Second

// P2PTunnelServer serves the second door of the worker tunnel: a stream of
// the P2P layer for each lane, opened by a worker that joined with a worker
// invite. The checks are those of the connect endpoint, with the peer ID that
// the P2P layer authenticated added to them; the session on the stream is the
// same as on a websocket and goes to the same registry, so every replica
// reaches the worker in the same way.
//
// The stream reaches here only from a paired peer: the gate of the P2P host
// lets an unpaired peer use the pairing protocol and nothing else.
type P2PTunnelServer struct {
	Registry *nodes.NodeRegistry
	Tunnels  *tunnel.Registry
}

// ServeConn serves one tunnel stream of lane: it reads the hello, runs the
// checks, answers, and gives the session to the registry. It returns once the
// session is attached or the stream is refused.
func (s P2PTunnelServer) ServeConn(ctx context.Context, conn net.Conn, lane tunnel.Lane) {
	_ = conn.SetDeadline(time.Now().Add(p2pHelloTimeout))
	hello, err := tunnel.ReadHello(conn)
	if err != nil {
		xlog.Debug("A P2P tunnel stream sent no valid hello", "error", err)
		_ = conn.Close()
		return
	}
	peer, ok := p2pnet.PeerOf(conn)
	refuse := func(status int, msg string) {
		_ = tunnel.WriteHelloReply(conn, tunnel.HelloReply{Status: status, Error: msg})
		_ = conn.Close()
	}
	switch {
	case !ok:
		refuse(http.StatusUnauthorized, "unauthorized")
		return
	case hello.Lane != lane:
		refuse(http.StatusBadRequest, "the hello names another lane than the protocol")
		return
	case hello.Token == "":
		refuse(http.StatusUnauthorized, "unauthorized")
		return
	case s.Registry == nil || s.Tunnels == nil:
		refuse(http.StatusServiceUnavailable, "distributed mode not enabled")
		return
	case hello.NodeID == "":
		refuse(http.StatusBadRequest, "missing node id")
		return
	}

	checkCtx, cancel := context.WithTimeout(ctx, p2pHelloTimeout)
	_, refusal := admitTunnel(checkCtx, s.Registry, s.Tunnels, hello.NodeID, hello.Token, string(lane), peer.String())
	cancel()
	if refusal != nil {
		refuse(refusal.status, refusal.msg)
		return
	}
	if err := tunnel.WriteHelloReply(conn, tunnel.HelloReply{Status: http.StatusOK}); err != nil {
		_ = conn.Close()
		return
	}
	_ = conn.SetDeadline(time.Time{})

	sess, err := tunnel.ServerSessionConn(conn, lane)
	if err != nil {
		xlog.Error("Worker tunnel session setup failed", "node", hello.NodeID, "error", err)
		_ = conn.Close()
		return
	}
	attachSession(ctx, s.Tunnels, hello.NodeID, lane, sess, "p2p", peer.String())
}
