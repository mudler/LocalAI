// SPDX-License-Identifier: MIT

package cluster

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

// The framing every stream on a PEER link opens with.
//
// A worker holds one tunnel and it lands on one frontend replica, so every
// other replica reaches that worker by relaying through the one that holds it.
// The peer link carries traffic for every worker its far side owns, so a stream
// on it means nothing until it says which worker it is for; that is this frame.
//
// A relayed stream therefore carries TWO request frames back to back: this one,
// which the owning replica consumes, and the worker tunnel's own (tunnelproto)
// frame, which crosses untouched and is answered by the worker. A dialler reads
// one reply from each, in that order.
//
// The two vocabularies are deliberately disjoint. "relay-ok" is not "ok", and
// none of the three refusal codes below is spelled like a tunnel code, so a
// reader applied to the wrong hop fails with "unrecognised reply" instead of
// handing back a plausible sentinel that belongs to the other hop. Getting that
// wrong would report a worker's refusal as the owning replica's, and a caller
// would retry against the wrong end of the path.
const (
	relayReplyAccepted   = "relay-ok"
	relayCodeNotOwner    = "relay-not-owner"
	relayCodeUnavailable = "relay-unavailable"
	relayCodeBadRequest  = "relay-bad-request"
)

// The refusals the relay hop can send, beyond ErrNotOwner which it shares with
// the local path.
//
// Three conditions, kept apart, for the reason the worker's three are kept
// apart. ErrNotOwner is a ROUTING fact: the worker may be perfectly healthy on
// another replica, and the caller should resolve the owner again. This one is
// INFRASTRUCTURE at the owning replica: the tunnel is held right here and its
// session will not carry a stream, so a retry is worth something and looking
// elsewhere is not. ErrRelayRequestInvalid is the CALLER's bug and no retry
// helps.
//
// None of them is, or may ever be built over, an absence error. A refusal is
// proof that a replica answered, and reporting absence would tell a scheduler
// that a worker which is connected has gone away.
var (
	ErrRelayUnavailable    = errors.New("cluster: the owning replica could not open a stream to that worker")
	ErrRelayRequestInvalid = errors.New("cluster: the owning replica rejected the relay request as malformed")
)

// WriteRelayRequest names the worker a peer stream is for, and how much time
// the ORIGINAL client still has.
//
// The budget is what makes the relay's own open bound honest. Everything on the
// far side of this frame is work done on behalf of a caller the relay cannot
// see, so without it the relay can only fall back to a deployment-wide constant
// that no operator has the information to set (see relayOpenTimeout). The
// dialler does have the information, because it holds the caller's context, so
// it is the one that states it.
//
// A zero budget means "not stated" and is written as no budget at all, which is
// also what an older replica sends. It is NEVER written as the number zero: on
// the far side that would be indistinguishable from a caller with no time left,
// and the relay would refuse traffic that is perfectly healthy.
//
// An empty node id is refused here rather than on the wire, so a caller with a
// bug learns at once instead of a round trip later. So is a node id containing
// the separator, because the split below takes the FIRST one and a node id with
// a space in it would silently move part of itself into the budget.
func WriteRelayRequest(w io.Writer, nodeID string, budget time.Duration) error {
	if nodeID == "" {
		return fmt.Errorf("writing a relay request: empty node id")
	}
	if strings.Contains(nodeID, streamRequestSeparator) {
		return fmt.Errorf("writing a relay request: node id %q contains a space", nodeID)
	}
	if budget <= 0 {
		return writeFrame(w, nodeID)
	}
	// A plain count of milliseconds rather than a duration string: an integer
	// has one spelling, so two replicas cannot disagree about it the way they
	// could about a units vocabulary that grew between their versions. Rounded
	// UP so a sub-millisecond budget stays positive and keeps meaning "almost
	// none" rather than collapsing into "not stated".
	millis := (budget + time.Millisecond - 1) / time.Millisecond
	return writeFrame(w, nodeID+streamRequestSeparator+strconv.FormatInt(int64(millis), 10))
}

// ReadRelayRequest reads the opening frame of a peer stream. The budget is zero
// when the dialling replica stated none, which is also what a replica too old
// to state one sends.
//
// A malformed frame is an ordinary error, NOT ErrRelayRequestInvalid: that
// sentinel is what a relay SENDS to describe a refusal, and producing it here
// would leave a caller unable to tell "the peer refused my request" from "I
// could not read the peer's".
func ReadRelayRequest(r io.Reader) (string, time.Duration, error) {
	payload, err := readFrame(r)
	if err != nil {
		return "", 0, fmt.Errorf("reading a relay request: %w", err)
	}
	nodeID, budgetText, stated := strings.Cut(payload, streamRequestSeparator)
	if nodeID == "" {
		// An empty payload is a well-formed frame naming no worker. Treating
		// it as a node called "" would send the caller a routing refusal for a
		// request no replica can ever serve, so it stays the caller's bug.
		return "", 0, fmt.Errorf("reading a relay request: empty node id")
	}
	if !stated {
		return nodeID, 0, nil
	}
	millis, err := strconv.ParseInt(budgetText, 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("reading a relay request for node %q: budget %q is not a number of milliseconds: %w", nodeID, budgetText, err)
	}
	if millis > maxRelayBudgetMillis {
		// time.Duration is nanoseconds in an int64, so multiplying by
		// time.Millisecond overflows past about 2.9e11 ms. Overflow here is
		// bounded in the safe direction (it can only produce a negative or a
		// small value, and both shorten the declaring peer's OWN open), but a
		// bound that holds by arithmetic accident is not a bound. Anything past
		// the ceiling is clamped to it, because a caller claiming to wait
		// longer than the relay's own backstop gets the backstop either way.
		millis = maxRelayBudgetMillis
	}
	if millis <= 0 {
		// A caller with nothing left to spend. Reported as such rather than
		// folded into "not stated", so the relay refuses at once instead of
		// waiting out a backstop on behalf of a client that has already gone.
		return nodeID, 0, fmt.Errorf("reading a relay request for node %q: budget %d ms has already expired", nodeID, millis)
	}
	return nodeID, time.Duration(millis) * time.Millisecond, nil
}

// WriteRelayAccepted tells the peer the stream now carries the worker tunnel's
// own conversation. Everything after this frame belongs to that hop.
func WriteRelayAccepted(w io.Writer) error { return writeFrame(w, relayReplyAccepted) }

// WriteRelayRefusal reports why a peer's stream will not be relayed. The caller
// closes the stream afterwards; this only says why.
//
// An unclassified reason is sent as bad-request with its text attached, rather
// than dropped: a refusal a peer cannot read is indistinguishable from a
// replica that hung up, and those are different problems.
func WriteRelayRefusal(w io.Writer, reason error) error {
	code := relayCodeBadRequest
	switch {
	case errors.Is(reason, ErrNotOwner):
		code = relayCodeNotOwner
	case errors.Is(reason, ErrRelayUnavailable):
		code = relayCodeUnavailable
	}

	text := ""
	if reason != nil {
		text = strings.Map(func(r rune) rune {
			// The frame is length-prefixed so a newline would not corrupt it,
			// but this text lands in a log line on the far side, and a cause
			// spanning lines is what makes one unsearchable.
			if r == '\n' || r == '\r' {
				return ' '
			}
			return r
		}, reason.Error())
	}
	frame := replyPrefixRefused + code + streamRequestSeparator + text
	return writeFrame(w, truncateRunes(frame, maxTunnelFrame))
}

// ReadRelayReply reads the owning replica's answer to a relay request. nil
// means the stream is now the worker tunnel's.
//
// A failure to READ the reply is returned as itself and never as one of the
// refusal sentinels: a refusal means a replica answered, a read failure means
// the peer link broke, and reporting the second as the first would present a
// dead link as a policy decision.
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
		return fmt.Errorf("reading a relay reply: unrecognised reply %q", payload)
	}
	code, text, _ := strings.Cut(rest, streamRequestSeparator)
	switch code {
	case relayCodeNotOwner:
		// ErrNotOwner and nothing else. It is a routing fact, and the sentinels
		// it must never be confused with are ErrNoConnection (the worker is
		// connected nowhere) and ErrPeerUnreachable (a replica will not
		// answer): a caller acts on those by giving up on the worker or by
		// retrying the peer, and on this one by resolving the owner again.
		return fmt.Errorf("%w: %s", ErrNotOwner, text)
	case relayCodeUnavailable:
		return fmt.Errorf("%w: %s", ErrRelayUnavailable, text)
	case relayCodeBadRequest:
		return fmt.Errorf("%w: %s", ErrRelayRequestInvalid, text)
	default:
		// A code from a newer replica. Carried out as-is rather than mapped
		// onto the nearest known one, so a caller does not retry forever
		// against a refusal that means something else entirely.
		return fmt.Errorf("relay stream refused with unrecognised code %q: %s", code, text)
	}
}

// maxRelayBudgetMillis is the largest budget a peer may declare, and exists so
// the conversion below cannot overflow. A day is many orders of magnitude past
// relayOpenTimeout, which is the only thing a budget is ever compared against,
// so clamping to it changes no honest caller's behaviour.
const maxRelayBudgetMillis = int64(24 * 60 * 60 * 1000)

const (
	// relayHeaderTimeout bounds how long a peer stream may go without naming
	// the worker it is for. Without it, a dialler killed between OpenStream and
	// its first write holds a relay goroutine and a stream slot until the whole
	// peer link dies, which is minutes on the default keepalive.
	relayHeaderTimeout = 15 * time.Second

	// relayOpenTimeout is the CEILING on opening the worker-side stream. yamux
	// blocks an Open once AcceptBacklog SYNs are in flight, waiting on synCh
	// rather than failing (go-yamux/v5@v5.1.0/session.go:205-212); it honours
	// the context, which is the only reason there is one here. Without the
	// bound, a worker that has stopped accepting would turn a refusable
	// condition into a parked peer, which is the one outcome this path exists
	// to avoid.
	//
	// It is deliberately NOT configurable, and it is no longer the whole
	// answer. The number that actually matters is how long the ORIGINAL client
	// is willing to wait, which no deployment-wide constant can stand in for;
	// the dialling replica now states it in the request frame and accept takes
	// the SMALLER of the two. This remains the backstop for a caller that
	// stated nothing, generous on purpose, because refusing healthy traffic
	// costs more than waiting.
	//
	// The stated budget only ever SHORTENS the wait. A caller willing to wait
	// an hour must not be able to park this replica's relay goroutine and a
	// yamux stream slot for an hour on a worker that has stopped accepting.
	relayOpenTimeout = 15 * time.Second
)

// Relay splices a stream a peer opened onto a worker tunnel this replica holds.
//
// It is the piece that makes more than one frontend replica work at all: a
// worker holds ONE tunnel, it lands on ONE replica, and with N replicas behind
// a load balancer roughly (N-1)/N of requests arrive somewhere else. Those
// requests reach the worker through here.
//
// One hop, always. A stream naming a worker this replica does not hold is
// refused, never resolved and relayed onward. A second hop would turn a stale
// ownership row into a loop between two replicas, each certain the other holds
// the worker, and the loop would carry the caller's request around it; the
// dialling replica re-resolving the owner is both cheaper and terminating.
type Relay struct {
	tunnels *TunnelRegistry

	// Timeouts are fields rather than constants read directly so a spec can
	// exercise the deadline without waiting out a production value. They are
	// not operator knobs and are not plumbed to configuration.
	headerTimeout time.Duration
	openTimeout   time.Duration
}

// NewRelay returns the relay for the tunnels this replica holds. Its Stream
// method is the SessionStore stream handler.
func NewRelay(tunnels *TunnelRegistry) *Relay { return newRelay(tunnels, 0, 0) }

func newRelay(tunnels *TunnelRegistry, headerTimeout, openTimeout time.Duration) *Relay {
	return &Relay{
		tunnels:       tunnels,
		headerTimeout: cmp.Or(headerTimeout, relayHeaderTimeout),
		openTimeout:   cmp.Or(openTimeout, relayOpenTimeout),
	}
}

// Stream relays one peer stream. It owns closing that stream on every path.
func (r *Relay) Stream(peerID string, stream net.Conn) {
	// SessionStore runs this on a bare goroutine, so an unrecovered panic here
	// ends the PROCESS, taking down every other replica's traffic through this
	// one. It covers what runs on this goroutine: the frame read, the registry
	// lookup and the open. It cannot cover a panic inside Splice's own copy
	// goroutines, and it deliberately does not re-panic, because there is no
	// recovery middleware above a goroutine the HTTP layer has already
	// returned from.
	defer func() {
		if p := recover(); p != nil {
			xlog.Error("Panic while relaying a peer stream", "peer", peerID, "panic", p)
			_ = stream.Close()
		}
	}()

	local, ok := r.accept(peerID, stream)
	if !ok {
		// accept has already answered and closed the stream.
		return
	}

	// Splice owns closing both ends from here.
	//
	// The error is logged at DEBUG and nowhere else. Every relayed request that
	// a client abandons mid-stream produces one, so a warning here would be one
	// line per cancelled inference; and the failures that are not cancellations
	// are already visible to the frontend, whose gRPC or HTTP client sees a
	// response that ended without its trailers or its final chunk. What this
	// line adds is the only view from the middle of the path: which node, on
	// which peer link, and what yamux actually said.
	if err := Splice(stream, local); err != nil {
		xlog.Debug("relayed peer stream ended with an error", "peer", peerID, "error", err)
	}
}

// accept reads which worker the stream is for and opens the worker-side stream.
// The second result is false when the stream was refused, in which case the
// refusal has been sent and the stream closed.
func (r *Relay) accept(peerID string, stream net.Conn) (net.Conn, bool) {
	if err := stream.SetReadDeadline(time.Now().Add(r.headerTimeout)); err != nil {
		// Nothing is readable on a stream whose deadline cannot be set, so this
		// is reported as infrastructure rather than pushed past.
		r.refuse(peerID, stream, fmt.Errorf("%w: arming the request deadline: %v", ErrRelayUnavailable, err))
		return nil, false
	}

	nodeID, budget, err := ReadRelayRequest(stream)
	if err != nil {
		// Includes the deadline above expiring. Both are "this stream never
		// said which worker it wanted", which is the dialling replica's bug
		// and not something a retry against this one resolves.
		r.refuse(peerID, stream, fmt.Errorf("%w: %v", ErrRelayRequestInvalid, err))
		return nil, false
	}

	// Cleared before the open rather than after the reply, and NOTHING arms
	// another deadline on this stream afterwards. That is the intent rather
	// than an omission: what follows is a relayed request whose length is the
	// caller's business, and a header deadline left armed here would abort a
	// long inference stream after any quiet moment in the middle of it. What
	// still bounds the conversation is the peer link's own keepalive, which
	// kills the session under it when the far side stops answering, and
	// whatever deadline the original client is holding.
	if err := stream.SetReadDeadline(time.Time{}); err != nil {
		r.refuse(peerID, stream, fmt.Errorf("%w: clearing the request deadline: %v", ErrRelayUnavailable, err))
		return nil, false
	}

	// Not the peer's deadline, because there is none to inherit: a yamux stream
	// carries no context. This bounds only the open, so a request that gets
	// past it is never cut short by it.
	//
	// The SMALLER of the ceiling and what the caller said it still has. Taking
	// the caller's number when it is larger would let one patient client park a
	// relay goroutine on a worker that has stopped accepting for as long as it
	// liked; taking the ceiling when the caller's is smaller would keep waiting
	// on behalf of a client that has already given up.
	open := r.openTimeout
	if budget > 0 && budget < open {
		open = budget
	}
	ctx, cancel := context.WithTimeout(context.Background(), open)
	defer cancel()

	local, err := r.tunnels.Open(ctx, nodeID)
	if err != nil {
		if errors.Is(err, ErrNotOwner) {
			// Passed through as itself. The worker is very likely connected and
			// healthy somewhere else, and this is the one answer that tells the
			// caller to look for it there.
			r.refuse(peerID, stream, err)
			return nil, false
		}
		// Everything else is this replica failing, and it must NOT become
		// ErrNotOwner. The tunnel is held right here, so sending the caller
		// looking elsewhere would send it back to this same replica; and it
		// must not become absence either, because the worker is attached and a
		// scheduler told otherwise would reclaim what it is running.
		r.refuse(peerID, stream, fmt.Errorf("%w: %v", ErrRelayUnavailable, err))
		return nil, false
	}

	if err := WriteRelayAccepted(stream); err != nil {
		// The peer never learns the stream was accepted, so it cannot be used.
		// Closing the worker-side stream here is what stops one leaking per
		// failed reply.
		xlog.Debug("could not accept a peer stream for relaying", "peer", peerID, "node", nodeID, "error", err)
		_ = local.Close()
		_ = stream.Close()
		return nil, false
	}
	return local, true
}

// refuse reports why a stream will not be relayed and then ENDS it.
//
// The close is the part that matters and it is not optional. A replica that
// says why and leaves the stream open has parked the peer on a request that
// will never be served, which reads as a slow replica rather than a refused
// request, and no deadline on the far side can tell those apart. The reply is
// what makes the refusal legible; the close is what makes it prompt.
//
// The reply is therefore best-effort and the close is not.
func (r *Relay) refuse(peerID string, stream net.Conn, reason error) {
	if err := WriteRelayRefusal(stream, reason); err != nil {
		xlog.Debug("could not tell a peer why its stream was refused", "peer", peerID, "reason", reason, "error", err)
	}
	_ = stream.Close()
}
