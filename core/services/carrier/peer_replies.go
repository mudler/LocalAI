package carrier

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/tunnel"
)

// PeerRequester sends one request to another replica over the peer link.
// tunnel.PeerPool is one. openWithin, when it is above zero, bounds the open
// of the stream (and so the dial of the link), and the rest of ctx stays for
// the handler.
type PeerRequester interface {
	Request(ctx context.Context, target, subject string, data []byte, maxBytes int, openWithin time.Duration) ([]byte, error)
}

// QueueMembers lists the replicas that serve a queue subject now.
type QueueMembers func(ctx context.Context) ([]string, error)

// maxQueueTries bounds the members that one queue request tries.
const maxQueueTries = 3

// PeerRepliesOptions is what NewPeerReplies needs.
type PeerRepliesOptions struct {
	// Self is the id of this process in the instances table.
	Self string
	// Peers sends a request to another process over the peer link.
	Peers PeerRequester
	// Local holds the handlers of this process, which the relay of the peer link
	// serves.
	Local *tunnel.Replies
	// Queues lists, for each subject that a queue serves, the processes that
	// serve it now (the chat start).
	Queues map[string]QueueMembers
}

// peerReplies is request and reply between the frontends and chat workers,
// on every deployment: the chat start, the replay of chat events and the
// background jobs of a turn. The peer link is the one transport for it. There is
// no broker to route a subject, so the asking process names the process that
// answers: the caller names it for a request to one process (RequestInstance),
// and a queue request picks it from the members that the queue lists. The
// request goes over the peer link, and the far process runs the handler that it
// registered for the subject in Local.
type peerReplies struct {
	self   string
	peers  PeerRequester
	local  *tunnel.Replies
	queues map[string]QueueMembers
}

var _ messaging.ProcessReplies = (*peerReplies)(nil)

// NewPeerReplies returns request and reply over the peer link.
func NewPeerReplies(o PeerRepliesOptions) (messaging.ProcessReplies, error) {
	switch {
	case o.Self == "":
		return nil, errors.New("request and reply over the peer link need the id of this process")
	case o.Peers == nil || o.Local == nil:
		return nil, errors.New("request and reply over the peer link need the peer pool and the local handlers")
	}
	return &peerReplies{self: o.Self, peers: o.Peers, local: o.Local, queues: o.Queues}, nil
}

// SubscribeReply serves subject on this process. The handler gets no context: a
// request that its caller abandons runs to its end, and the reply is dropped.
func (t *peerReplies) SubscribeReply(subject string, handler func(data []byte, reply func([]byte))) (messaging.Subscription, error) {
	if err := messaging.ValidateSubject(subject); err != nil {
		return nil, err
	}
	return removeFunc(t.local.Handle(subject, func(_ context.Context, data []byte) ([]byte, error) {
		var (
			mu      sync.Mutex
			out     []byte
			replied bool
		)
		handler(data, func(b []byte) {
			mu.Lock()
			defer mu.Unlock()
			if !replied {
				out, replied = bytes.Clone(b), true
			}
		})
		mu.Lock()
		defer mu.Unlock()
		if !replied {
			return nil, errors.New("the handler sent no reply")
		}
		return out, nil
	})), nil
}

// RequestInstance sends the request to the replica instance.
//
// A named replica is never absent here, and its failures are never
// messaging.ErrNoResponders. A replica with no row may be a live one whose
// heartbeats stalled past the reap window, and one that serves nothing for the
// subject may be a live one that has not registered its handler yet.
// Both still run what they ran, and a caller that read either as a process
// that left would give up work that is going on, such as a background job.
// Both prove that nothing ran, so they are messaging.ErrNotDelivered.
func (t *peerReplies) RequestInstance(instance, subject string, data []byte, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := t.ask(ctx, instance, subject, data, 0)
	if isAbsent(err) {
		return nil, fmt.Errorf("%w: %w", messaging.ErrNotDelivered, err)
	}
	return out, mapReplyError(err)
}

// RequestQueue sends the request to one member of the queue of subject. It moves
// to another member only when the failure proves that the first ran nothing
// (tunnel.NotStarted), or when moveOn says that its reply is such a refusal.
// After a request was written its outcome is unknown, and it is returned
// without a retry, so that the work is never done twice.
func (t *peerReplies) RequestQueue(subject string, data []byte, timeout time.Duration, moveOn func([]byte) bool) ([]byte, error) {
	members, ok := t.queues[subject]
	if !ok {
		return nil, fmt.Errorf("%w: the peer link routes %s to no process", messaging.ErrNotDelivered, subject)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ids, err := members(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: listing the replicas that serve %s: %w", messaging.ErrNotDelivered, subject, err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: no replica serves %s", messaging.ErrNoResponders, subject)
	}
	// Random and not round robin: a counter on one replica says nothing about the
	// load, and several replicas keep several counters that agree on nothing.
	// #nosec G404 -- spreads work across equivalent members; the choice is no secret.
	rand.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })

	var (
		refused  []byte
		failures []error
		absent   = true
	)
	tries := ids[:min(len(ids), maxQueueTries)]
	for i, id := range tries {
		// Each member gets a share of what is left to open its stream, so a
		// member in the liveness window that does not answer the dial leaves
		// time for the others. The share bounds the open only: a member that
		// took the request has the rest of the budget for its handler.
		var share time.Duration
		if deadline, ok := ctx.Deadline(); ok {
			share = max(time.Until(deadline)/time.Duration(len(tries)-i), time.Millisecond)
		}
		out, err := t.ask(ctx, id, subject, data, share)
		switch {
		case err == nil && moveOn != nil && moveOn(out):
			refused = out
		case err == nil:
			return out, nil
		case !tunnel.NotStarted(err):
			return nil, mapReplyError(err)
		default:
			failures = append(failures, err)
			absent = absent && isAbsent(err)
		}
	}
	if refused != nil {
		return refused, nil
	}
	err = mapReplyError(errors.Join(failures...))
	if !absent {
		// One member that could not be reached is not "nobody serves it".
		err = fmt.Errorf("%w: %w", messaging.ErrNotDelivered, errors.Join(failures...))
	}
	return nil, err
}

func (t *peerReplies) ask(ctx context.Context, instance, subject string, data []byte, openWithin time.Duration) ([]byte, error) {
	// Refused before any member is tried: no member would take it.
	if len(data) > messaging.MaxBroadcastBytes {
		return nil, fmt.Errorf("%w: a request of %d bytes on %s", messaging.ErrPayloadTooLarge, len(data), subject)
	}
	if instance == t.self {
		return t.local.Dispatch(ctx, subject, data)
	}
	return t.peers.Request(ctx, instance, subject, data, messaging.MaxBroadcastBytes, openWithin)
}

// isAbsent reports a failure that says that nobody serves the subject there: the
// replica is not part of the deployment, or nothing on it serves the subject.
// Only a queue request reads it as absence, and only when every member it tried
// said so: a member of the queue is one of several equivalent ones, and the
// request ran nowhere. A request to a named replica never does (RequestInstance).
func isAbsent(err error) bool {
	return errors.Is(err, tunnel.ErrNoResponder) || errors.Is(err, cluster.ErrInstanceNotFound)
}

// mapReplyError is the one place where the errors of a request on the peer link
// become the conditions of messaging. Only absence becomes
// messaging.ErrNoResponders, and RequestInstance takes absence out before it
// gets here. A request above the bound, which was not sent, is
// messaging.ErrPayloadTooLarge; a reply above the bound is a handler that ran
// (tunnel.ErrReplyTooLarge) and is not. A request that proves that it ran
// nothing for any other reason (a peer that does not answer, a release that
// predates request and reply, a replica that is not the one named, a replica
// that serves too many requests) is messaging.ErrNotDelivered. Everything else
// leaves the outcome unknown and keeps its own identity.
func mapReplyError(err error) error {
	switch {
	case err == nil:
		return nil
	case isAbsent(err):
		return fmt.Errorf("%w: %w", messaging.ErrNoResponders, err)
	case errors.Is(err, tunnel.ErrPayloadTooLarge):
		return fmt.Errorf("%w: %w", messaging.ErrPayloadTooLarge, err)
	case tunnel.NotStarted(err):
		return fmt.Errorf("%w: %w", messaging.ErrNotDelivered, err)
	}
	return err
}

type removeFunc func()

func (f removeFunc) Unsubscribe() error {
	f()
	return nil
}
