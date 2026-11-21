package tunnel

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mudler/xlog"
)

// Request and reply between replicas.
//
// A stream of a peer link that carries a request opens with a frame of four
// fields: the replica that must answer, the word "reply" where a relay frame
// names its lane, the budget of the caller in milliseconds, and the subject.
// The request body follows (a 4-byte length, then the bytes). The replica
// answers with one status frame, and with a body after "reply-ok".
//
// One stream carries one request, so requests are not ordered. The caller ends
// a request by closing its stream, and the replica then cancels the context of
// the handler. The budget bounds the work of the replica in the same way.
//
// A replica that predates this frame reads it as a relay frame with an unknown
// lane and refuses it with a relay code. That code is read as
// ErrRequestUnsupported, and no handler ran.
const relayLaneReply = "reply"

// A malformed request frame has no code of its own: the relay refuses it with
// its relay code for a bad request, which the caller reads as
// ErrRequestUnsupported, a request that ran nothing.
const (
	rrAccepted          = "reply-ok"
	rrCodeNotOwner      = "reply-not-owner"
	rrCodeNoResponder   = "reply-no-responder"
	rrCodeTooLarge      = "reply-too-large"
	rrCodeBusy          = "reply-busy"
	rrCodeFailed        = "reply-failed"
	rrCodeReplyTooLarge = "reply-answer-too-large"
	relayCodePrefix     = "relay-"
)

// The number of requests that a replica serves at once, for one peer link and
// in all. A request holds its slot from before its body is read until its reply
// is written, so the slots also bound the memory that peers can make it hold.
// The handlers that a replica serves (the chat start, the replay, the job asks)
// are short, and a request above the bound is refused as one that ran nothing.
const (
	defaultRepliesPerPeer = 64
	defaultRepliesTotal   = 256
)

// The ways a request can fail. NotStarted tells the ones that prove that no
// handler ran from the ones that leave the outcome unknown.
var (
	// ErrRequestNotSent means that the request did not reach the replica: the
	// peer link could not be opened, or the request could not be written whole.
	ErrRequestNotSent = errors.New("tunnel: the request did not reach the replica")
	// ErrNoResponder means that the replica answered, and that nothing on it
	// serves the subject.
	ErrNoResponder = errors.New("tunnel: nothing on the replica serves that subject")
	// ErrRequestUnsupported means that the replica does not serve request and
	// reply, because its release predates it.
	ErrRequestUnsupported = errors.New("tunnel: the replica does not serve request and reply")
	// ErrReplyLost means that the request was sent and its reply did not
	// arrive. The handler may have run.
	ErrReplyLost = errors.New("tunnel: the request was sent and its reply did not arrive")
	// ErrHandlerFailed means that the handler ran and gave no reply. Its work
	// may be done.
	ErrHandlerFailed = errors.New("tunnel: the handler on the replica gave no reply")
	// ErrPayloadTooLarge means that a request is above the bound of the
	// replicas, and it was not sent or not served. A reply above the bound is
	// ErrReplyTooLarge.
	ErrPayloadTooLarge = errors.New("tunnel: the request is above the bound")
	// ErrReplyTooLarge means that the handler ran and its reply is above the
	// bound. It always comes with ErrHandlerFailed, wherever the handler ran.
	ErrReplyTooLarge = errors.New("tunnel: the reply of the handler is above the bound")
	// ErrReplicaBusy means that the replica serves as many requests as it may,
	// for the peer link or in all, and refused this one before it ran anything.
	ErrReplicaBusy = errors.New("tunnel: the replica serves too many requests")
)

// NotStarted reports whether err, an error of a request, proves that no handler
// ran. Only such a failure lets a caller send the same request elsewhere.
func NotStarted(err error) bool {
	for _, s := range []error{ErrRequestNotSent, ErrNoResponder, ErrRequestUnsupported, ErrNotOwner, ErrReplicaBusy} {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}

// ReplyHandler answers one request. ctx ends when the caller closes its stream
// or its budget runs out.
type ReplyHandler func(ctx context.Context, data []byte) ([]byte, error)

// Replies holds the handlers of this replica, by subject, and serves the
// requests that peers send over the peer link. The tunnel package names no
// subject: the caller registers what it serves.
type Replies struct {
	self string
	max  int
	// bodyTimeout bounds how long a peer may take to send the body of a request
	// once it named the subject. It is a field so that a spec can reach it.
	bodyTimeout time.Duration

	mu       sync.Mutex
	handlers map[string][]*ReplyHandler

	// slotsMu guards the requests in service, by peer and in all.
	slotsMu        sync.Mutex
	perPeer, total int
	serving        map[string]int
	servingAll     int
}

// NewReplies returns the handlers of the replica self. A request or a reply
// above maxBytes is refused.
func NewReplies(self string, maxBytes int) *Replies {
	return &Replies{
		self: self, max: maxBytes, bodyTimeout: relayHeaderTimeout, handlers: map[string][]*ReplyHandler{},
		perPeer: defaultRepliesPerPeer, total: defaultRepliesTotal, serving: map[string]int{},
	}
}

// acquire takes a slot for a request of peerID, or reports that there is none.
func (r *Replies) acquire(peerID string) bool {
	r.slotsMu.Lock()
	defer r.slotsMu.Unlock()
	if r.servingAll >= r.total || r.serving[peerID] >= r.perPeer {
		return false
	}
	r.servingAll++
	r.serving[peerID]++
	return true
}

func (r *Replies) release(peerID string) {
	r.slotsMu.Lock()
	defer r.slotsMu.Unlock()
	r.servingAll--
	if r.serving[peerID]--; r.serving[peerID] <= 0 {
		delete(r.serving, peerID)
	}
}

// Handle serves subject with h until remove is called. A subject can have more
// than one handler, for example while a handler is replaced by a new one before
// the old one is removed; a request goes to the newest.
func (r *Replies) Handle(subject string, h ReplyHandler) (remove func()) {
	entry := &h
	r.mu.Lock()
	r.handlers[subject] = append(r.handlers[subject], entry)
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			list := r.handlers[subject]
			for i, e := range list {
				if e == entry {
					list = append(list[:i:i], list[i+1:]...)
					break
				}
			}
			if len(list) == 0 {
				delete(r.handlers, subject)
			} else {
				r.handlers[subject] = list
			}
		})
	}
}

func (r *Replies) lookup(subject string) ReplyHandler {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.handlers[subject]
	if len(list) == 0 {
		return nil
	}
	return *list[len(list)-1]
}

// Dispatch runs the handler of subject in this process, for a request that a
// caller addresses to its own replica.
func (r *Replies) Dispatch(ctx context.Context, subject string, data []byte) ([]byte, error) {
	h := r.lookup(subject)
	if h == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoResponder, subject)
	}
	out, err := runHandler(ctx, h, data)
	if err != nil {
		return nil, err
	}
	if len(out) > r.max {
		return nil, replyTooLarge(len(out), r.max)
	}
	return out, nil
}

// runHandler runs h and returns when it does or when ctx ends. A handler that
// does not watch ctx goes on in the background, and its reply is dropped.
func runHandler(ctx context.Context, h ReplyHandler, data []byte) ([]byte, error) {
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- result{err: fmt.Errorf("%w: panic: %v", ErrHandlerFailed, p)}
			}
		}()
		out, err := h(ctx, data)
		if err != nil {
			err = fmt.Errorf("%w: %w", ErrHandlerFailed, err)
		}
		done <- result{out: out, err: err}
	}()
	select {
	case res := <-done:
		return res.out, res.err
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ErrHandlerFailed, ctx.Err())
	}
}

// serve answers one request that a peer sent. It owns closing the stream.
func (r *Replies) serve(peerID string, stream net.Conn, target, subject string, budget time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), cmp.Or(budget, defaultReplyBudget))
	defer cancel()
	defer func() {
		// The deadline ends the read of the watcher below at once.
		_ = stream.SetDeadline(time.Now())
		_ = stream.Close()
	}()

	if target != r.self {
		r.refuse(peerID, stream, rrCodeNotOwner, "this replica is "+r.self)
		return
	}
	// Before the body: a peer that opens many streams must not make this
	// replica hold a body for each.
	if !r.acquire(peerID) {
		r.refuse(peerID, stream, rrCodeBusy, "this replica serves too many requests")
		return
	}
	defer r.release(peerID)
	if err := stream.SetReadDeadline(time.Now().Add(r.bodyTimeout)); err != nil {
		return
	}
	body, err := readBody(stream, r.max)
	if err != nil {
		if errors.Is(err, ErrPayloadTooLarge) {
			r.refuse(peerID, stream, rrCodeTooLarge, err.Error())
			return
		}
		xlog.Debug("a peer request ended before its body", "peer", peerID, "subject", subject, "error", err)
		return
	}
	if err := stream.SetReadDeadline(time.Time{}); err != nil {
		return
	}
	h := r.lookup(subject)
	if h == nil {
		r.refuse(peerID, stream, rrCodeNoResponder, subject)
		return
	}

	// The caller sends nothing after the body. A read that returns means that it
	// closed its stream or that the link died, and the handler is cancelled.
	go func() {
		var one [1]byte
		_, _ = stream.Read(one[:])
		cancel()
	}()

	out, err := runHandler(ctx, h, body)
	if deadline, ok := ctx.Deadline(); ok {
		_ = stream.SetWriteDeadline(deadline)
	}
	switch {
	case err != nil:
		r.refuse(peerID, stream, rrCodeFailed, err.Error())
	case len(out) > r.max:
		r.refuse(peerID, stream, rrCodeReplyTooLarge, fmt.Sprintf("a reply of %d bytes, the bound is %d", len(out), r.max))
	default:
		msg, ferr := appendFrame(nil, rrAccepted)
		if ferr == nil {
			msg = appendBody(msg, out)
			_, ferr = stream.Write(msg)
		}
		if ferr != nil {
			xlog.Debug("could not send a reply to a peer", "peer", peerID, "subject", subject, "error", ferr)
		}
	}
}

func (r *Replies) refuse(peerID string, stream net.Conn, code, text string) {
	if err := writeFrame(stream, truncateRunes(replyPrefixRefused+code+streamRequestSeparator+oneLine(text), maxFrame)); err != nil {
		xlog.Debug("could not tell a peer why its request was refused", "peer", peerID, "code", code, "error", err)
	}
}

// defaultReplyBudget bounds the work of a request whose caller stated no
// budget.
const defaultReplyBudget = time.Minute

// Request sends one request to the replica target over the peer link and
// returns its reply. The deadline of ctx is the budget that the replica gets,
// and the end of ctx closes the stream, which cancels the handler.
//
// openWithin, when it is above zero, bounds the open of the stream, which can
// include the dial of the peer link. A caller that tries several replicas gives
// each its share, so that one that does not answer leaves time for the others.
// An open that runs out of its share is a request that was not sent.
func (p *PeerPool) Request(ctx context.Context, target, subject string, data []byte, maxBytes int, openWithin time.Duration) ([]byte, error) {
	return request(ctx, p.Open, target, subject, data, maxBytes, openWithin)
}

func request(ctx context.Context, open func(context.Context, string) (net.Conn, error), target, subject string, data []byte, maxBytes int, openWithin time.Duration) ([]byte, error) {
	if len(data) > maxBytes {
		return nil, fmt.Errorf("%w: %w: a request of %d bytes, the bound is %d", ErrRequestNotSent, ErrPayloadTooLarge, len(data), maxBytes)
	}
	var budget time.Duration
	deadline, hasDeadline := ctx.Deadline()
	if hasDeadline {
		if budget = time.Until(deadline); budget <= 0 {
			return nil, fmt.Errorf("%w: %w", ErrRequestNotSent, context.DeadlineExceeded)
		}
	}
	msg, err := replyRequestFrame(target, subject, budget)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRequestNotSent, err)
	}
	msg = appendBody(msg, data)

	st, err := openBounded(ctx, open, target, openWithin)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRequestNotSent, err)
	}
	defer func() { _ = st.Close() }()
	stop := context.AfterFunc(ctx, func() {
		_ = st.SetDeadline(time.Now())
		_ = st.Close()
	})
	defer stop()
	if hasDeadline {
		_ = st.SetDeadline(deadline)
	}

	// The replica runs nothing before it has the whole body, so a write that
	// fails is a request that did not start.
	if _, err := st.Write(msg); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRequestNotSent, causeOf(ctx, err))
	}
	status, err := readFrame(st)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrReplyLost, causeOf(ctx, err))
	}
	if status != rrAccepted {
		return nil, replyRefusal(status)
	}
	out, err := readBody(st, maxBytes)
	if errors.Is(err, ErrPayloadTooLarge) {
		// The handler ran: what is too large is its reply.
		return nil, replyTooLarge(-1, maxBytes)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrReplyLost, causeOf(ctx, err))
	}
	return out, nil
}

// openBounded opens the stream within openWithin, when it is above zero. The
// bound covers the open alone: the stream that it returns lives on the context
// of the caller.
func openBounded(ctx context.Context, open func(context.Context, string) (net.Conn, error), target string, openWithin time.Duration) (net.Conn, error) {
	if openWithin <= 0 {
		return open(ctx, target)
	}
	octx, cancel := context.WithTimeout(ctx, openWithin)
	defer cancel()
	st, err := open(octx, target)
	if err != nil && ctx.Err() == nil && octx.Err() != nil {
		return nil, fmt.Errorf("opening a stream to %s took longer than its share of %s: %w", target, openWithin, err)
	}
	return st, err
}

// replyTooLarge is the error of a handler that ran and gave a reply above the
// bound. size is negative when it is not known.
func replyTooLarge(size, maxBytes int) error {
	if size < 0 {
		return fmt.Errorf("%w: %w: the bound is %d", ErrHandlerFailed, ErrReplyTooLarge, maxBytes)
	}
	return fmt.Errorf("%w: %w: a reply of %d bytes, the bound is %d", ErrHandlerFailed, ErrReplyTooLarge, size, maxBytes)
}

// causeOf prefers the end of the caller's context over the error of the stream
// that it caused.
func causeOf(ctx context.Context, err error) error {
	if ctxErr := callerRanOut(ctx); ctxErr != nil {
		return ctxErr
	}
	return err
}

func replyRefusal(status string) error {
	rest, ok := strings.CutPrefix(status, replyPrefixRefused)
	if !ok {
		return fmt.Errorf("reading a reply: %w: %q", ErrProtocol, status)
	}
	code, text, _ := strings.Cut(rest, streamRequestSeparator)
	switch code {
	case rrCodeNotOwner:
		return fmt.Errorf("%w: %s", ErrNotOwner, text)
	case rrCodeNoResponder:
		return fmt.Errorf("%w: %s", ErrNoResponder, text)
	case rrCodeTooLarge:
		return fmt.Errorf("%w: %w: %s", ErrRequestNotSent, ErrPayloadTooLarge, text)
	case rrCodeBusy:
		return fmt.Errorf("%w: %s", ErrReplicaBusy, text)
	case rrCodeFailed:
		return fmt.Errorf("%w: %s", ErrHandlerFailed, text)
	case rrCodeReplyTooLarge:
		return fmt.Errorf("%w: %w: %s", ErrHandlerFailed, ErrReplyTooLarge, text)
	}
	if strings.HasPrefix(code, relayCodePrefix) {
		return fmt.Errorf("%w: %s %s", ErrRequestUnsupported, code, text)
	}
	// A code of a newer replica. The outcome is unknown, so it is not mapped
	// onto a code that would let the caller send the request elsewhere.
	return fmt.Errorf("reply refused with unrecognised code %q: %w: %s", code, ErrProtocol, text)
}

func replyRequestFrame(target, subject string, budget time.Duration) ([]byte, error) {
	for _, f := range []string{target, subject} {
		if f == "" || strings.Contains(f, streamRequestSeparator) {
			return nil, fmt.Errorf("writing a request: %q is empty or has a space", f)
		}
	}
	var millis int64
	if budget > 0 {
		millis = int64((budget + time.Millisecond - 1) / time.Millisecond)
	}
	return appendFrame(nil, strings.Join([]string{target, relayLaneReply, strconv.FormatInt(millis, 10), subject}, streamRequestSeparator))
}

// parseReplyRequest reads the opening frame of a request. ok is false for a
// frame that is not one, which is a relay frame or nothing.
func parseReplyRequest(payload string) (target, subject string, budget time.Duration, ok bool, err error) {
	fields := strings.Split(payload, streamRequestSeparator)
	if len(fields) < 2 || fields[1] != relayLaneReply {
		return "", "", 0, false, nil
	}
	if len(fields) != 4 || fields[0] == "" || fields[3] == "" {
		return "", "", 0, true, fmt.Errorf("reading a request: want \"target reply budget subject\", got %d fields", len(fields))
	}
	millis, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || millis < 0 {
		return "", "", 0, true, fmt.Errorf("reading a request: budget %q is not a number of milliseconds", fields[2])
	}
	return fields[0], fields[3], time.Duration(min(millis, maxRelayBudgetMillis)) * time.Millisecond, true, nil
}

func appendBody(buf, data []byte) []byte {
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(data))) // #nosec G115 -- bounded by the caller's maxBytes, far below 4 GiB
	return append(buf, data...)
}

func readBody(r io.Reader, maxBytes int) ([]byte, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return nil, err
	}
	n := int64(binary.BigEndian.Uint32(size[:]))
	if n > int64(maxBytes) {
		return nil, fmt.Errorf("%w: %d bytes, the bound is %d", ErrPayloadTooLarge, n, maxBytes)
	}
	// The buffer grows with the bytes that arrive and not with the size that the
	// peer declared: a peer that declares the bound and sends nothing makes this
	// side hold nothing.
	var body bytes.Buffer
	body.Grow(int(min(n, bodyFirstChunk)))
	if _, err := io.CopyN(&body, r, n); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return body.Bytes(), nil
}

// bodyFirstChunk is the room that a body gets before its bytes arrive.
const bodyFirstChunk = 64 << 10

func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
}
