package tunnel

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/xlog"
)

// The framing that every stream of a peer link opens with.
//
// A worker holds one tunnel and it lands on one frontend replica, so every other
// replica reaches that worker through the one that holds it. A stream of a peer
// link carries the traffic of every worker that the far replica owns, so it means
// nothing until it says which worker it is for. That is this frame.
//
// A relayed stream carries two request frames one after the other. The first is
// this one, and the owning replica consumes it. The second is the frame of the
// worker tunnel, which crosses untouched and is answered by the worker. A dialler
// reads one reply from each, in that order.
//
// The two vocabularies are disjoint on purpose. "relay-ok" is not "ok", and no
// refusal code below is spelled like a code of the tunnel, so a reader applied to
// the wrong hop fails with "unrecognised reply" and does not hand back a
// plausible sentinel of the other hop. Reporting a refusal of the worker as one of
// the owning replica would send the caller to retry against the wrong end.
const (
	relayReplyAccepted   = "relay-ok"
	relayCodeNotOwner    = "relay-not-owner"
	relayCodeUnavailable = "relay-unavailable"
	relayCodeNoBulk      = "relay-no-bulk"
	relayCodeBadRequest  = "relay-bad-request"
)

// The lanes a relay request can name. The third keeps the refusal to fall back:
// the dialler asked for the bulk lane and for no other.
const (
	relayLaneInference = "inference"
	relayLaneBulk      = "bulk"
	relayLaneBulkOnly  = "bulk-only"
)

func relayLane(o dialOptions) string {
	switch {
	case o.lane == LaneBulk && o.noFallback:
		return relayLaneBulkOnly
	case o.lane == LaneBulk:
		return relayLaneBulk
	}
	return relayLaneInference
}

// The refusals that the relay hop can send, beyond ErrNotOwner, which it shares
// with the local path.
//
// They are kept apart. ErrNotOwner is a routing fact: the worker may be well on
// another replica, and the caller resolves the owner again. ErrRelayUnavailable
// is the owning replica failing: it holds the tunnel and its session will not
// carry a stream, so a retry is worth something and looking elsewhere is not.
// ErrRelayRequestInvalid is a bug of the caller, and no retry helps.
//
// The owner also reports a bulk lane that is down (ErrNoBulkSession), so that the
// dialling replica reports it as that and not as a route that does not exist. A
// worker whose bulk session is being dialled again has a tunnel that carries
// model calls, and a scheduler that demoted it for the lane would lose it for the
// requests that work.
//
// None of them is built over an absence error. A refusal proves that a replica
// answered.
var (
	ErrRelayUnavailable    = errors.New("tunnel: the owning replica could not open a stream to that worker")
	ErrRelayRequestInvalid = errors.New("tunnel: the owning replica rejected the relay request as malformed")
)

// WriteRelayRequest names the worker that a peer stream is for, the lane, and how
// much time the original caller still has.
//
// The budget is what makes the open of the relay honest. Everything past this
// frame is work for a caller that the relay cannot see, so without a budget the
// relay can only use a constant that no operator can set. Zero means that the
// budget is not stated, and it is written as 0. A caller with no time left is
// about to fail on its own context, and stating less than a millisecond is
// written as 1.
func WriteRelayRequest(w io.Writer, nodeID, lane string, budget time.Duration) error {
	if nodeID == "" {
		return errors.New("writing a relay request: empty node id")
	}
	if strings.Contains(nodeID, streamRequestSeparator) {
		return fmt.Errorf("writing a relay request: node id %q contains a space", nodeID)
	}
	switch lane {
	case relayLaneInference, relayLaneBulk, relayLaneBulkOnly:
	default:
		return fmt.Errorf("writing a relay request: unknown lane %q", lane)
	}
	var millis int64
	if budget > 0 {
		// A count of milliseconds has one spelling. It is rounded up, so that a
		// sub-millisecond budget stays positive and keeps meaning "almost none".
		millis = int64((budget + time.Millisecond - 1) / time.Millisecond)
	}
	return writeFrame(w, nodeID+streamRequestSeparator+lane+streamRequestSeparator+strconv.FormatInt(millis, 10))
}

// ReadRelayRequest reads the opening frame of a peer stream. The budget is zero
// when the dialling replica stated none.
//
// A malformed frame is an ordinary error and not ErrRelayRequestInvalid. That
// sentinel is what a relay sends to describe a refusal, and returning it here
// would leave a caller unable to tell "the peer refused my request" from "I could
// not read the request of the peer".
func ReadRelayRequest(r io.Reader) (nodeID, lane string, budget time.Duration, err error) {
	payload, err := readFrame(r)
	if err != nil {
		return "", "", 0, fmt.Errorf("reading a relay request: %w", err)
	}
	fields := strings.Split(payload, streamRequestSeparator)
	if len(fields) != 3 || fields[0] == "" {
		return "", "", 0, fmt.Errorf("reading a relay request: want \"node lane budget\", got %d fields", len(fields))
	}
	nodeID, lane = fields[0], fields[1]
	switch lane {
	case relayLaneInference, relayLaneBulk, relayLaneBulkOnly:
	default:
		return "", "", 0, fmt.Errorf("reading a relay request for node %q: unknown lane %q", nodeID, lane)
	}
	millis, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || millis < 0 {
		return "", "", 0, fmt.Errorf("reading a relay request for node %q: budget %q is not a number of milliseconds", nodeID, fields[2])
	}
	// time.Duration is nanoseconds in an int64, so a large count overflows when it
	// is multiplied. A caller that claims to wait longer than the backstop of the
	// relay gets the backstop either way, so the count is clamped.
	millis = min(millis, maxRelayBudgetMillis)
	return nodeID, lane, time.Duration(millis) * time.Millisecond, nil
}

// WriteRelayAccepted tells the peer that the stream now carries the conversation
// of the worker tunnel. Everything after this frame belongs to that hop.
func WriteRelayAccepted(w io.Writer) error { return writeFrame(w, relayReplyAccepted) }

// WriteRelayRefusal reports why a peer stream is not relayed. The caller closes
// the stream after it. A reason that has no class is sent as bad-request with its
// text, and not dropped: a refusal that a peer cannot read looks like a replica
// that hung up.
func WriteRelayRefusal(w io.Writer, reason error) error {
	code := relayCodeBadRequest
	switch {
	case errors.Is(reason, ErrNotOwner):
		code = relayCodeNotOwner
	case errors.Is(reason, ErrRelayUnavailable):
		code = relayCodeUnavailable
	case errors.Is(reason, ErrNoBulkSession):
		code = relayCodeNoBulk
	}
	text := ""
	if reason != nil {
		text = strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' {
				return ' '
			}
			return r
		}, reason.Error())
	}
	return writeFrame(w, truncateRunes(replyPrefixRefused+code+streamRequestSeparator+text, maxFrame))
}

// ReadRelayReply reads the answer of the owning replica. A nil result means that
// the stream is now the stream of the worker tunnel.
//
// A failure to read the reply is returned as itself and never as one of the
// refusals. A refusal means that a replica answered, and a failed read means that
// the peer link broke. Reporting the second as the first would present a dead
// link as a decision.
func ReadRelayReply(r io.Reader) error {
	payload, err := readFrame(r)
	if err != nil {
		return fmt.Errorf("reading a relay reply: %w", err)
	}
	if payload == relayReplyAccepted {
		return nil
	}
	rest, ok := strings.CutPrefix(payload, replyPrefixRefused)
	if !ok {
		return fmt.Errorf("reading a relay reply: %w: %q", ErrProtocol, payload)
	}
	code, text, _ := strings.Cut(rest, streamRequestSeparator)
	switch code {
	case relayCodeNotOwner:
		// ErrNotOwner and nothing else: a routing fact. It is not ErrNoConnection
		// (the worker is connected nowhere) and not ErrPeerUnreachable (a replica
		// will not answer).
		return fmt.Errorf("%w: %s", ErrNotOwner, text)
	case relayCodeUnavailable:
		return fmt.Errorf("%w: %s", ErrRelayUnavailable, text)
	case relayCodeNoBulk:
		return fmt.Errorf("%w: %s", ErrNoBulkSession, text)
	case relayCodeBadRequest:
		return fmt.Errorf("%w: %s", ErrRelayRequestInvalid, text)
	}
	// A code of a newer replica. It is not mapped onto the nearest known code, so
	// that a caller does not retry for ever against a refusal that means something
	// else.
	return fmt.Errorf("relay stream refused with unrecognised code %q: %w: %s", code, ErrProtocol, text)
}

// maxRelayBudgetMillis is the largest budget a peer may declare. A day is far
// past relayOpenTimeout, which is all a budget is compared with.
const maxRelayBudgetMillis = int64(24 * 60 * 60 * 1000)

const (
	// relayHeaderTimeout bounds how long a peer stream may go without naming the
	// worker it is for. Without it, a dialler that is killed between OpenStream
	// and its first write holds a goroutine and a slot of the link until the link
	// dies.
	relayHeaderTimeout = 15 * time.Second

	// relayOpenTimeout is the ceiling on opening the stream to the worker. yamux
	// blocks an open once its accept backlog is full, and honours the context,
	// which is the reason there is one. It is the backstop for a caller that
	// stated no budget, and the stated budget only ever shortens it: a caller
	// willing to wait an hour must not be able to park a goroutine and a slot on a
	// worker that stopped accepting.
	relayOpenTimeout = 15 * time.Second
)

// Relay splices a stream that a peer opened onto a worker tunnel that this
// replica holds.
//
// It is what makes more than one replica work. With N replicas behind a load
// balancer, (N-1)/N of the requests arrive at a replica that does not hold the
// tunnel, and they reach the worker through here.
//
// One hop, always. A stream that names a worker this replica does not hold is
// refused and never relayed onward. A second hop would turn a stale ownership row
// into a loop between two replicas, each sure that the other holds the worker.
type Relay struct {
	tunnels *Registry

	// The timeouts are fields so that a spec can reach a deadline without waiting
	// out a production value.
	headerTimeout time.Duration
	openTimeout   time.Duration
}

// NewRelay returns the relay for the tunnels that this replica holds. Its Stream
// method is the handler of a PeerSessions.
func NewRelay(tunnels *Registry) *Relay { return newRelay(tunnels, 0, 0) }

func newRelay(tunnels *Registry, headerTimeout, openTimeout time.Duration) *Relay {
	return &Relay{
		tunnels:       tunnels,
		headerTimeout: cmp.Or(headerTimeout, relayHeaderTimeout),
		openTimeout:   cmp.Or(openTimeout, relayOpenTimeout),
	}
}

// Stream relays one peer stream. It owns closing the stream on every path.
func (r *Relay) Stream(peerID string, stream net.Conn) {
	// It runs on a bare goroutine, and a panic that is not recovered ends the
	// process, with the traffic of every other replica that goes through this one.
	// The recovery covers the frame read, the lookup and the open. It cannot cover
	// the copy goroutines of Splice, and it does not panic again, because no
	// middleware sits above a goroutine that the HTTP layer already left.
	defer func() {
		if p := recover(); p != nil {
			xlog.Error("Panic while relaying a peer stream", "peer", peerID, "panic", p)
			_ = stream.Close()
		}
	}()

	local, ok := r.accept(peerID, stream)
	if !ok {
		return
	}
	// Splice owns closing both ends. A relayed request that a client abandons
	// ends with an error, so it is logged at debug level only.
	if err := Splice(stream, local); err != nil {
		xlog.Debug("relayed peer stream ended with an error", "peer", peerID, "error", err)
	}
}

// accept reads which worker the stream is for and opens the stream to the worker.
// The second result is false when the stream was refused, and then the refusal
// was sent and the stream closed.
func (r *Relay) accept(peerID string, stream net.Conn) (net.Conn, bool) {
	if err := stream.SetReadDeadline(time.Now().Add(r.headerTimeout)); err != nil {
		r.refuse(peerID, stream, fmt.Errorf("%w: arming the request deadline: %v", ErrRelayUnavailable, err))
		return nil, false
	}

	nodeID, lane, budget, err := ReadRelayRequest(stream)
	if err != nil {
		// This includes the deadline above running out. Both mean that the stream
		// never said which worker it wanted, which is a bug of the dialling
		// replica, and a retry against this one does not cure it.
		r.refuse(peerID, stream, fmt.Errorf("%w: %v", ErrRelayRequestInvalid, err))
		return nil, false
	}

	// Cleared before the open, and nothing arms another deadline after it. What
	// follows is a relayed request whose length is the business of the caller, and
	// a deadline left armed would end a long inference after a quiet moment. The
	// keepalive of the peer link kills a session whose far side stopped answering.
	if err := stream.SetReadDeadline(time.Time{}); err != nil {
		r.refuse(peerID, stream, fmt.Errorf("%w: clearing the request deadline: %v", ErrRelayUnavailable, err))
		return nil, false
	}

	// The smaller of the ceiling and what the caller says it still has.
	open := r.openTimeout
	if budget > 0 && budget < open {
		open = budget
	}
	ctx, cancel := context.WithTimeout(context.Background(), open)
	defer cancel()

	var (
		openLane = LaneInference
		openOpts []OpenOption
	)
	switch lane {
	case relayLaneBulk:
		openLane = LaneBulk
	case relayLaneBulkOnly:
		openLane = LaneBulk
		openOpts = append(openOpts, WithoutFallback())
	}
	local, err := r.tunnels.Open(ctx, nodeID, openLane, openOpts...)
	if err != nil {
		if errors.Is(err, ErrNotOwner) || errors.Is(err, ErrNoBulkSession) {
			// As itself. ErrNotOwner: the worker is probably well on another
			// replica, and this is the answer that tells the caller to look there.
			// ErrNoBulkSession: the tunnel is held here and only its bulk lane is
			// down, which the caller must not read as a failure of this replica.
			r.refuse(peerID, stream, err)
			return nil, false
		}
		// Everything else is this replica failing. It must not become ErrNotOwner,
		// because the tunnel is held here and the caller would come back to this
		// replica. It must not become absence either: the worker is attached.
		r.refuse(peerID, stream, fmt.Errorf("%w: %v", ErrRelayUnavailable, err))
		return nil, false
	}

	if err := WriteRelayAccepted(stream); err != nil {
		// The peer never learns that the stream was accepted, so it cannot use it.
		// The stream to the worker is closed, or one would leak for every failed
		// reply.
		xlog.Debug("could not accept a peer stream for relaying", "peer", peerID, "node", nodeID, "error", err)
		_ = local.Close()
		_ = stream.Close()
		return nil, false
	}
	return local, true
}

// refuse reports why a stream is not relayed and then ends it. The close is not
// optional: a replica that says why and leaves the stream open has parked the
// peer on a request that will never be served, and that looks like a slow replica
// and not like a refusal. The reply is best effort and the close is not.
func (r *Relay) refuse(peerID string, stream net.Conn, reason error) {
	if err := WriteRelayRefusal(stream, reason); err != nil {
		xlog.Debug("could not tell a peer why its stream was refused", "peer", peerID, "reason", reason, "error", err)
	}
	_ = stream.Close()
}
