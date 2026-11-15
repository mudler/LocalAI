package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// The ring of a running chat is on the process that runs its turn. A client
// whose stream reconnects to another frontend gets what it missed by an ask to
// that process: the turn records its owner in the store (Deps.Instance, see
// store.Store.SetTurnOwner), and the frontend sends the ask over Deps.Replay.
// The ring stays per process on purpose: it is a cache of the live tail, the
// transcript is the record, and every failure of an ask ends in a resync, after
// which the client reads the transcript.

// ReplayRequest asks the process that runs a chat's turn for the events that
// follow After.
type ReplayRequest struct {
	Key   string `json:"key"`
	After string `json:"after"`
}

// ReplayReply is the answer to a ReplayRequest. Resync says the events cannot be
// given; Head is then the newest id the owner knows ("" when none). More says
// the events did not fit in one reply: ask again after the last one.
type ReplayReply struct {
	Events []session.Event `json:"events,omitempty"`
	Resync bool            `json:"resync,omitempty"`
	Head   string          `json:"head,omitempty"`
	More   bool            `json:"more,omitempty"`
}

// ReplayCarrier carries a ReplayRequest to the process named by an instance and
// its answer back. Ask returns an error when no process of that name answers in
// time.
type ReplayCarrier interface {
	Ask(instance string, req ReplayRequest) (ReplayReply, error)
	// Serve answers the asks sent to instance with h until cancel.
	Serve(instance string, h func(ReplayRequest) ReplayReply) (cancel func(), err error)
}

// ReplyRequester is the request/reply part of the message bus
// (messaging.MessagingClient has it).
type ReplyRequester interface {
	SubscribeReply(subject string, handler func(data []byte, reply func([]byte))) (messaging.Subscription, error)
	Request(subject string, data []byte, timeout time.Duration) ([]byte, error)
}

// replayTimeout is how long a frontend waits for the owner. Past it the client
// gets a resync: a slow owner must not hold the reconnect.
const replayTimeout = 2 * time.Second

// replayPageBytes bounds the events of one reply, well under the 1 MiB a NATS
// message may have by default. The ring holds up to 4 MiB, so a long gap takes
// a few replies.
const replayPageBytes = 512 << 10

// replayMaxPages bounds the asks of one catch-up; the ring fits in fewer.
const replayMaxPages = 16

func replaySubject(instance string) string { return "chats.replay." + token(instance) }

// NewReplayCarrier returns a ReplayCarrier on the cluster's message bus, one
// request/reply subject per instance under the chats root.
func NewReplayCarrier(mc ReplyRequester, log *slog.Logger) ReplayCarrier {
	if log == nil {
		log = slog.Default()
	}
	return &busReplay{mc: mc, log: log}
}

type busReplay struct {
	mc  ReplyRequester
	log *slog.Logger
}

func (b *busReplay) Ask(instance string, req ReplayRequest) (ReplayReply, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return ReplayReply{}, err
	}
	data, err := b.mc.Request(replaySubject(instance), raw, replayTimeout)
	if err != nil {
		return ReplayReply{}, err
	}
	var rep ReplayReply
	if err := json.Unmarshal(data, &rep); err != nil {
		return ReplayReply{}, fmt.Errorf("studio: a replay reply: %w", err)
	}
	return rep, nil
}

func (b *busReplay) Serve(instance string, h func(ReplayRequest) ReplayReply) (func(), error) {
	sub, err := b.mc.SubscribeReply(replaySubject(instance), func(data []byte, reply func([]byte)) {
		var req ReplayRequest
		rep := ReplayReply{Resync: true}
		if err := json.Unmarshal(data, &req); err == nil {
			rep = h(req)
		}
		raw, err := json.Marshal(rep)
		if err != nil {
			b.log.Warn("studio: could not encode a replay reply", "error", err)
			raw, _ = json.Marshal(ReplayReply{Resync: true})
		}
		reply(raw)
	})
	if err != nil {
		return nil, err
	}
	return func() { _ = sub.Unsubscribe() }, nil
}

// serveReplay answers the asks for the ring of a turn this process runs.
func (s *Service) serveReplay() error {
	if s.d.Replay == nil || s.d.Instance == "" {
		return nil
	}
	stop, err := s.d.Replay.Serve(s.d.Instance, s.answerReplay)
	if err != nil {
		return fmt.Errorf("studio: serve event replay: %w", err)
	}
	s.stopServing = stop
	return nil
}

// answerReplay gives a page of the events that follow req.After from the ring.
// The key comes from another frontend, which checked that the chat is the
// user's before it asked.
func (s *Service) answerReplay(req ReplayRequest) ReplayReply {
	r := s.ringOf(req.Key)
	if r == nil {
		return ReplayReply{Resync: true}
	}
	evs, ok := r.After(req.After)
	if !ok {
		return ReplayReply{Resync: true, Head: r.Head()}
	}
	var page []session.Event
	size := 0
	for _, ev := range evs {
		ev = withoutAppPayload(ev)
		raw, err := json.Marshal(ev)
		if err != nil {
			return ReplayReply{Resync: true, Head: r.Head()}
		}
		if size+len(raw) > replayPageBytes {
			if len(page) == 0 {
				// One merged answer larger than a reply: the transcript, with the
				// text saved while it streams, holds it.
				return ReplayReply{Resync: true, Head: r.Head()}
			}
			return ReplayReply{Events: page, More: true}
		}
		size += len(raw)
		page = append(page, ev)
	}
	return ReplayReply{Events: page}
}

// The reasons of a resync, the values of the reason label of its metric.
const (
	resyncLocal       = "local"       // this process runs the turn and its ring cannot give the events
	resyncOwner       = "owner"       // the owner answered that its ring cannot give them
	resyncUnreachable = "unreachable" // the owner did not answer: gone, or too slow
	resyncNoOwner     = "no_owner"    // no other process is known to run the turn
)

// askOwner asks the process that runs the chat's turn for the events that
// follow from. It is not ok, with the reason, when they cannot be had.
func (s *Service) askOwner(p, conv, from string) (evs []session.Event, head string, ok bool, why string) {
	if s.d.Replay == nil || s.d.Instance == "" {
		return nil, "", false, resyncNoOwner
	}
	ctx, cancel := context.WithTimeout(context.Background(), replayTimeout)
	info, err := s.d.Store.Turn(ctx, p, conv)
	cancel()
	if err != nil || info.Owner == "" || info.Owner == s.d.Instance {
		// The owner is this process, which has no ring for the chat: the run
		// ended here more than ringGrace ago, or before a restart.
		return nil, "", false, resyncNoOwner
	}
	k := key(p, conv)
	after := from
	for range replayMaxPages {
		rep, err := s.d.Replay.Ask(info.Owner, ReplayRequest{Key: k, After: after})
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				s.log.Debug("studio: the owner of a turn did not answer a replay", "chat", conv, "error", err)
			}
			return nil, "", false, resyncUnreachable
		}
		if rep.Resync {
			return nil, rep.Head, false, resyncOwner
		}
		evs = append(evs, rep.Events...)
		if !rep.More || len(rep.Events) == 0 {
			return evs, "", true, ""
		}
		after = rep.Events[len(rep.Events)-1].ID
	}
	// Still more after the last page: the client takes what it has and the
	// gap check of the live events asks for the rest.
	return evs, "", true, ""
}

// recordOwner names this process as the owner of the turn runID, so another
// frontend can ask it for the ring. A failure only costs a resync later.
func (s *Service) recordOwner(ctx context.Context, p, conv, runID string) {
	if s.d.Instance == "" {
		return
	}
	if err := s.d.Store.SetTurnOwner(context.WithoutCancel(ctx), p, conv, runID, s.d.Instance); err != nil {
		s.log.Debug("studio: could not record the owner of a turn", "chat", conv, "error", err)
	}
}
