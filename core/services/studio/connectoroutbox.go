package studio

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The outbox is the one door for text that the host writes to a platform on
// its own, not as the answer to a message: the report of a trigger run and the
// text of a proposal. A platform limits how fast a bot may write, so a binding
// sends at most outboxPerMinute of these in a minute. A message over the limit
// waits in a queue, in order, and goes when the minute allows; it is not
// dropped.
const (
	outboxPerMinute = 20
	outboxMaxQueue  = 100
	outboxPollEvery = time.Second
)

// Errors of the outbox.
var (
	// ErrBindingOff means the binding is missing or off, so nothing is sent.
	ErrBindingOff = errors.New("studio: the connector binding is not on")
	// ErrOutboxFull means a binding has the most messages it may have waiting.
	ErrOutboxFull = errors.New("studio: too many connector messages are waiting to be sent")
)

// Post is one message the host writes to a chat of a binding.
type Post struct {
	Binding, Chat, Thread, Text string
	// OnFail is called, from another goroutine, when a message that waited in
	// the queue could not be sent. A message that goes at once reports its
	// error as the result of Connectors.Post.
	OnFail func(error)
}

type outItem struct {
	token string
	Post
}

type outbox struct {
	sent     []time.Time
	queue    []outItem
	draining bool
}

type outboxes struct {
	mu sync.Mutex
	by map[string]*outbox
}

func (o *outboxes) get(id string) *outbox {
	if o.by == nil {
		o.by = map[string]*outbox{}
	}
	q := o.by[id]
	if q == nil {
		q = &outbox{}
		o.by[id] = q
	}
	return q
}

// prune forgets the sends of more than a minute ago.
func (q *outbox) prune(now time.Time) {
	kept := q.sent[:0]
	for _, t := range q.sent {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	q.sent = kept
}

// Post sends text to a chat of a binding. It sends at once when the binding is
// under its limit and nothing waits, and otherwise queues the message and
// reports queued. The binding must be on.
func (c *Connectors) Post(ctx context.Context, p Post) (queued bool, err error) {
	b, err := c.o.Store.GetBinding(ctx, p.Binding)
	if err != nil || !b.Enabled {
		return false, ErrBindingOff
	}
	token, err := c.openToken(ctx, b)
	if err != nil {
		return false, err
	}
	it := outItem{token: token, Post: p}
	o := &c.outbox
	o.mu.Lock()
	q := o.get(b.ID)
	q.prune(c.o.Clock())
	if len(q.queue) == 0 && len(q.sent) < outboxPerMinute {
		q.sent = append(q.sent, c.o.Clock())
		o.mu.Unlock()
		return false, c.sendPost(ctx, b, it)
	}
	if len(q.queue) >= outboxMaxQueue {
		o.mu.Unlock()
		return false, ErrOutboxFull
	}
	q.queue = append(q.queue, it)
	start := !q.draining
	q.draining = true
	o.mu.Unlock()
	if start && !c.spawn(func() { c.drain(b) }) {
		o.mu.Lock()
		q.draining = false
		o.mu.Unlock()
	}
	return true, nil
}

func (c *Connectors) sendPost(ctx context.Context, b connectors.Binding, it outItem) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return c.send(ctx, b, contract.ConnectorRequest{Op: contract.ConnectorOpSend, Token: it.token, Chat: it.Chat, Thread: it.Thread, Text: it.Text})
}

// drain sends the queue of a binding as the limit allows, then stops.
func (c *Connectors) drain(b connectors.Binding) {
	o := &c.outbox
	poll := c.o.OutboxPoll
	if poll <= 0 {
		poll = outboxPollEvery
	}
	for {
		o.mu.Lock()
		q := o.get(b.ID)
		q.prune(c.o.Clock())
		if len(q.queue) == 0 {
			q.draining = false
			o.mu.Unlock()
			return
		}
		if len(q.sent) >= outboxPerMinute {
			o.mu.Unlock()
			select {
			case <-c.done:
				return
			case <-time.After(poll):
			}
			continue
		}
		it := q.queue[0]
		q.queue = q.queue[1:]
		q.sent = append(q.sent, c.o.Clock())
		o.mu.Unlock()
		if err := c.sendPost(context.Background(), b, it); err != nil {
			c.log.Warn("studio: could not send a queued connector message", "binding", b.ID, "error", err)
			if it.OnFail != nil {
				it.OnFail(err)
			}
		}
	}
}

// Queued is how many messages of a binding wait for the limit.
func (c *Connectors) Queued(bindingID string) int {
	c.outbox.mu.Lock()
	defer c.outbox.mu.Unlock()
	return len(c.outbox.get(bindingID).queue)
}
