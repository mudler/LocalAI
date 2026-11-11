package bgjob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"syscall"
	"time"
)

// A job lives in the memory of the node that started it, but the turns of its
// chat can run on any node. The carrier sends a request about a job to the
// node that runs it, on the subject chats.jobs.<instance>: request/reply,
// one subscription per node, a copy of the carrier of the event replay
// (studio.ReplayCarrier). Nothing is queued. A reply says what the node knows;
// silence says nothing:
//
//   - the node answers "unknown": it does not know the job (it restarted, or
//     the job was dropped). A caller that holds a live stub marks it lost.
//   - nobody listens (ErrNoRoute): the node left the cluster. Same reading.
//   - the ask times out: the caller changes nothing and may try again.

// AskTimeout is how long a caller waits for the node that runs a job.
const AskTimeout = 2 * time.Second

// ErrNoRoute says no node listens for the instance: it is gone.
var ErrNoRoute = errors.New("bgjob: no route to that node")

// The actions of a Request.
const (
	ActionList    = "list"
	ActionObserve = "observe"
	ActionRead    = "read"
	ActionInput   = "input"
	ActionKill    = "kill"
	ActionEndChat = "end_chat"
)

// Request is one question or order for the node that runs a job. Owner and
// Chat are checked on every request, never the id alone.
type Request struct {
	Action string `json:"action"`
	Owner  string `json:"owner"`
	Chat   string `json:"chat"`
	ID     string `json:"id,omitempty"`
	// Since is an absolute offset for observe and read; nil on read means
	// where the last read of the chat ended.
	Since     *int64 `json:"since,omitempty"`
	MaxBytes  int    `json:"max_bytes,omitempty"`
	TailBytes int    `json:"tail_bytes,omitempty"`
	// Input and CloseInput are for input.
	Input      string `json:"input,omitempty"`
	CloseInput bool   `json:"close_input,omitempty"`
	// Signal is term, int or kill, for kill.
	Signal string `json:"signal,omitempty"`
	// Reason is the end reason of a kill (killed or stopped) or of end_chat.
	Reason EndReason `json:"reason,omitempty"`
}

// The codes of a Reply that is not a success.
const (
	CodeUnknown   = "unknown"
	CodeEnded     = "ended"
	CodeForbidden = "forbidden"
	CodeTimeout   = "input_timeout"
	CodeClosed    = "input_closed"
	CodeBad       = "bad"
)

// Reply is the answer of the node.
type Reply struct {
	// Code is empty on success, else one of the Code constants.
	Code    string       `json:"code,omitempty"`
	Message string       `json:"message,omitempty"`
	Info    *Info        `json:"info,omitempty"`
	Obs     *Observation `json:"obs,omitempty"`
	List    []Info       `json:"list,omitempty"`
	Wrote   int          `json:"wrote,omitempty"`
	Ended   int          `json:"ended,omitempty"`
}

// Err turns a failing Reply back into the error the registry returned:
// ErrUnknown, *EndedError, ErrForbidden, ErrInputTimeout, ErrInputClosed, or a plain error.
func (r Reply) Err() error {
	switch r.Code {
	case "":
		return nil
	case CodeUnknown:
		return ErrUnknown
	case CodeEnded:
		e := &EndedError{}
		if r.Info != nil {
			e.Info = *r.Info
		}
		return e
	case CodeForbidden:
		return ErrForbidden
	case CodeTimeout:
		return ErrInputTimeout
	case CodeClosed:
		return ErrInputClosed
	}
	return fmt.Errorf("bgjob: the node refused the request: %s", r.Message)
}

func errReply(err error) Reply {
	var ended *EndedError
	switch {
	case errors.Is(err, ErrUnknown):
		return Reply{Code: CodeUnknown}
	case errors.As(err, &ended):
		return Reply{Code: CodeEnded, Info: &ended.Info}
	case errors.Is(err, ErrForbidden):
		return Reply{Code: CodeForbidden}
	case errors.Is(err, ErrInputTimeout):
		return Reply{Code: CodeTimeout}
	case errors.Is(err, ErrInputClosed):
		return Reply{Code: CodeClosed}
	}
	return Reply{Code: CodeBad, Message: err.Error()}
}

// ParseSignal reads the signal of a kill: term (the default), int or kill.
func ParseSignal(s string) (syscall.Signal, bool) {
	switch s {
	case "", "term":
		return syscall.SIGTERM, true
	case "int":
		return syscall.SIGINT, true
	case "kill":
		return syscall.SIGKILL, true
	}
	return 0, false
}

// Handle answers one request against this registry. It is what the carrier
// serves and what a call in the same process uses, so both behave alike.
func (r *Registry) Handle(ctx context.Context, q Request) Reply {
	return r.handle(ctx, q, 0)
}

// RemoteInputWait is how long an input that came over the carrier waits for a
// child that does not read. It is below AskTimeout, so the node answers
// before the asker gives up: a retry of an input the asker never saw answered
// would write it twice. The bus also hands a node one request at a time per
// subscription, so the wait is kept short: a stalled input delays the next
// ask to the node by at most this long.
const RemoteInputWait = AskTimeout - 500*time.Millisecond

func (r *Registry) handle(ctx context.Context, q Request, inputWait time.Duration) Reply {
	switch q.Action {
	case ActionList:
		l, err := r.List(ctx, q.Owner, q.Chat)
		if err != nil {
			return errReply(err)
		}
		return Reply{List: l}
	case ActionObserve:
		o, err := r.Observe(ctx, ObserveRequest{Owner: q.Owner, Chat: q.Chat, ID: q.ID, MaxBytes: q.MaxBytes, TailBytes: q.TailBytes, Since: derefSince(q.Since)})
		if err != nil {
			return errReply(err)
		}
		return Reply{Obs: &o}
	case ActionRead:
		o, err := r.Read(ctx, ReadRequest{Owner: q.Owner, Chat: q.Chat, ID: q.ID, Since: q.Since, MaxBytes: q.MaxBytes})
		if err != nil {
			return errReply(err)
		}
		return Reply{Obs: &o}
	case ActionInput:
		if len(q.Input) > MaxInput {
			return Reply{Code: CodeBad, Message: "input is longer than the most one call may write"}
		}
		n, err := r.input(q.Owner, q.Chat, q.ID, q.Input, q.CloseInput, inputWait)
		if err != nil {
			return errReply(err)
		}
		return Reply{Wrote: n}
	case ActionKill:
		sig, ok := ParseSignal(q.Signal)
		if !ok {
			return Reply{Code: CodeBad, Message: "unknown signal"}
		}
		reason := q.Reason
		if reason != EndStopped {
			reason = EndKilled
		}
		in, err := r.Kill(q.Owner, q.Chat, q.ID, reason, sig)
		if err != nil {
			rep := errReply(err)
			if rep.Info == nil && rep.Code != CodeUnknown {
				rep.Info = &in
			}
			return rep
		}
		return Reply{Info: &in}
	case ActionEndChat:
		reason := q.Reason
		if reason == "" {
			reason = EndChatDeleted
		}
		return Reply{Ended: r.EndChat(q.Owner, q.Chat, reason)}
	}
	return Reply{Code: CodeBad, Message: "unknown action"}
}

func derefSince(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// Carrier carries a Request to the node named by an instance and its Reply
// back. Ask returns ErrNoRoute (wrapped) when no node of that name listens
// and another error when none answers in time.
type Carrier interface {
	Ask(instance string, req Request) (Reply, error)
	// Serve answers the asks sent to instance with h until cancel.
	Serve(instance string, h func(Request) Reply) (cancel func(), err error)
}

// Subject is the subject a node serves its jobs on. The instance goes through
// a hash, as for the other chat subjects: it may hold characters a subject
// token cannot.
func Subject(instance string) string {
	sum := sha256.Sum256([]byte(instance))
	return "chats.jobs." + hex.EncodeToString(sum[:])
}

// Router sends a request to the registry of this process, or over the carrier
// to the node that runs the job. In one process (no carrier) every instance is
// this one.
type Router struct {
	// Local is the registry of this process.
	Local *Registry
	// Self is the instance of this process; "" without a cluster.
	Self string
	// Carrier reaches the other nodes; nil without a cluster.
	Carrier Carrier
}

// Serve starts answering the asks of other nodes. The returned function stops
// it; it is a no-op without a carrier or an instance.
func (r *Router) Serve() (func(), error) {
	if r.Carrier == nil || r.Self == "" || r.Local == nil {
		return func() {}, nil
	}
	return r.Carrier.Serve(r.Self, func(q Request) Reply { return r.Local.handle(context.Background(), q, RemoteInputWait) })
}

// Do runs req on the node named by instance. The error is a failure to ask
// (ErrNoRoute, a timeout, a transport error) or the error the node answered
// with (ErrUnknown, *EndedError, ErrForbidden, ErrInputTimeout). A reply that
// is not an error comes back with a nil error.
func (r *Router) Do(ctx context.Context, instance string, req Request) (Reply, error) {
	if instance == r.Self || r.Carrier == nil {
		if instance != r.Self {
			return Reply{}, ErrNoRoute
		}
		if r.Local == nil {
			return Reply{}, ErrNoRoute
		}
		rep := r.Local.Handle(ctx, req)
		return rep, rep.Err()
	}
	if err := ctx.Err(); err != nil {
		return Reply{}, err
	}
	rep, err := r.Carrier.Ask(instance, req)
	if err != nil {
		return Reply{}, err
	}
	return rep, rep.Err()
}

// Observer returns the Observer of the jobs on one instance: a wait that is
// pinned to a node reads through it.
func (r *Router) Observer(instance string) Observer { return routedObserver{r: r, instance: instance} }

type routedObserver struct {
	r        *Router
	instance string
}

func (o routedObserver) Observe(ctx context.Context, q ObserveRequest) (Observation, error) {
	since := q.Since
	rep, err := o.r.Do(ctx, o.instance, Request{Action: ActionObserve, Owner: q.Owner, Chat: q.Chat, ID: q.ID, Since: &since, MaxBytes: q.MaxBytes, TailBytes: q.TailBytes})
	if err != nil {
		return Observation{}, err
	}
	if rep.Obs == nil {
		return Observation{}, ErrUnknown
	}
	return *rep.Obs, nil
}

func (o routedObserver) List(ctx context.Context, owner, chat string) ([]Info, error) {
	rep, err := o.r.Do(ctx, o.instance, Request{Action: ActionList, Owner: owner, Chat: chat})
	return rep.List, err
}

// Gone reports whether err says the node cannot hold the job any more: it
// does not know it, or nobody listens. A timeout is not gone.
func Gone(err error) bool { return errors.Is(err, ErrUnknown) || errors.Is(err, ErrNoRoute) }
