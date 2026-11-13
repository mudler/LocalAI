// SPDX-License-Identifier: MIT
package clientdemo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/addons/internal/hostclient"
	"github.com/mudler/LocalAI/addons/internal/idle"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Host is what the add-on asks of the host: the host operation channel. The
// operation ID is the one of the client call being served; the host finds the
// client in its own record of that call.
type Host interface {
	Do(ctx context.Context, operationID, op string, args, out any) error
}

var _ Host = (*hostclient.Client)(nil)

// Job is the state of a job as client.jobs.get answers it.
type Job struct {
	ID          string `json:"id"`
	Task        string `json:"task,omitempty"`
	Status      string `json:"status"`
	Result      string `json:"result,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
	Error       string `json:"error,omitempty"`
	CompletedAt string `json:"completedAt,omitempty"`
}

func ended(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled"
}

// Runtime serves the client routes of the example add-on.
type Runtime struct {
	idle.Runtime
	host Host
	dir  string
	ttl  time.Duration
	poll time.Duration
	now  func() time.Time

	mu     sync.Mutex
	outbox map[string]*Outbox
	// used is when each client last asked for its outbox, and swept is when the
	// stale files were last removed (see box).
	used   map[string]time.Time
	swept  time.Time
	nameRE *regexp.Regexp
}

// Options configure a Runtime. Zero values pick the defaults.
type Options struct {
	// Dir holds the outbox files.
	Dir string
	// TTL is how long an event stays in an outbox.
	TTL time.Duration
	// Poll is how often a stream asks the host for the state of the jobs it
	// watches.
	Poll time.Duration
	Now  func() time.Time
}

// New returns a runtime that talks to host and keeps its files in opt.Dir.
func New(host Host, opt Options) (*Runtime, error) {
	if opt.Dir == "" {
		return nil, errors.New("clientdemo: no data directory")
	}
	if opt.Poll <= 0 {
		opt.Poll = time.Second
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if err := os.MkdirAll(filepath.Join(opt.Dir, "outbox"), 0o700); err != nil {
		return nil, err
	}
	if _, err := sweepOutboxes(filepath.Join(opt.Dir, "outbox"), opt.Now(), StaleOutboxAge, nil); err != nil {
		return nil, err
	}
	return &Runtime{host: host, dir: opt.Dir, ttl: opt.TTL, poll: opt.Poll, now: opt.Now, outbox: map[string]*Outbox{}, used: map[string]time.Time{}, swept: opt.Now(), nameRE: regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)}, nil
}

// Run is the whole behaviour of the executable.
func Run(args []string) int {
	return protocol.Run(args, func(s protocol.Startup) contract.Runtime {
		rt, err := New(hostclient.New(s), Options{Dir: s.DataDir})
		if err != nil {
			return idle.Runtime{}
		}
		return rt
	})
}

// box returns the outbox of a client. The client id is a key id the host made;
// the name is checked all the same before it becomes a file name.
func (r *Runtime) box(client string) (*Outbox, error) {
	if !r.nameRE.MatchString(client) {
		return nil, protocol.ErrBadRequest
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.sweepLocked(now)
	r.used[client] = now
	if o := r.outbox[client]; o != nil {
		return o, nil
	}
	o, err := OpenOutbox(filepath.Join(r.dir, "outbox", client+".json"), r.ttl, r.now)
	if err != nil {
		return nil, err
	}
	r.outbox[client] = o
	return o, nil
}

func reply(status int, v any) (contract.ClientResponse, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return contract.ClientResponse{}, err
	}
	return contract.ClientResponse{Status: status, ContentType: "application/json", Body: b}, nil
}

// hostError turns a refusal of the host into the answer a client can act on.
// Anything else is an outage, which the host reports as unavailable.
//
// A conflict has two causes: the quota of running jobs refuses a start, and a
// cancel of a job that has ended. The route tells them apart, because a client
// should retry the first later and never the second.
func hostError(route string, err error) (contract.ClientResponse, error) {
	var he *contract.HostError
	if !errors.As(err, &he) {
		return contract.ClientResponse{}, err
	}
	switch he.Code {
	case contract.HostNotFound:
		return reply(http.StatusNotFound, map[string]string{"error": "not found"})
	case contract.HostConflict:
		if route == "cancel" {
			return reply(http.StatusConflict, map[string]string{"error": "the job has ended"})
		}
		return reply(http.StatusTooManyRequests, map[string]string{"error": "too many jobs"})
	case contract.HostBadRequest:
		return reply(http.StatusBadRequest, map[string]string{"error": "the request was refused"})
	}
	return contract.ClientResponse{}, err
}

// ClientCall answers the routes that do not stream.
func (r *Runtime) ClientCall(ctx context.Context, q contract.ClientRequest) (contract.ClientResponse, error) {
	switch q.RouteID {
	case "ping":
		return reply(http.StatusOK, map[string]any{"ok": true, "client": q.Client.ID})
	case "start":
		return r.start(ctx, q)
	case "job":
		var j Job
		if err := r.host.Do(ctx, q.OperationID, "client.jobs.get", map[string]string{"id": q.Params["id"]}, &j); err != nil {
			return hostError(q.RouteID, err)
		}
		return reply(http.StatusOK, j)
	case "files":
		return r.listFiles(ctx, q)
	case "file":
		return r.readFile(ctx, q)
	case "save":
		return r.saveFile(ctx, q)
	case "cancel":
		var j Job
		if err := r.host.Do(ctx, q.OperationID, "client.jobs.cancel", map[string]string{"id": q.Params["id"]}, &j); err != nil {
			return hostError(q.RouteID, err)
		}
		return reply(http.StatusOK, j)
	}
	return reply(http.StatusNotFound, map[string]string{"error": "not found"})
}

// The file routes. The route has a wildcard tail, so the host has already
// refused a dot segment, an escape and an empty part. The add-on still checks
// the joined value before it hands it on: the host operation checks it once
// more against the folders the owner granted to the client.
var filePath = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._~-]{0,127}(/[A-Za-z0-9_][A-Za-z0-9._~-]{0,127}){0,7}$`)

func (r *Runtime) filesPath(q contract.ClientRequest) (string, bool) {
	p := q.Params["path"]
	if !filePath.MatchString(p) || strings.Contains(p, "..") {
		return "", false
	}
	return p, true
}

// listFiles answers GET /files: the folders the client may reach.
func (r *Runtime) listFiles(ctx context.Context, q contract.ClientRequest) (contract.ClientResponse, error) {
	var out json.RawMessage
	if err := r.host.Do(ctx, q.OperationID, "client.workspace.list", map[string]string{"path": ""}, &out); err != nil {
		return hostError(q.RouteID, err)
	}
	return contract.ClientResponse{Status: http.StatusOK, ContentType: "application/json", Body: out}, nil
}

// readFile answers GET /files/{path...}. A path that ends in a slash-less name
// is a file; the client asks the list of a folder with the folder route.
func (r *Runtime) readFile(ctx context.Context, q contract.ClientRequest) (contract.ClientResponse, error) {
	p, ok := r.filesPath(q)
	if !ok {
		return reply(http.StatusBadRequest, map[string]string{"error": "invalid path"})
	}
	op := "client.workspace.read"
	if q.Query["list"] == "1" {
		op = "client.workspace.list"
	}
	var out json.RawMessage
	if err := r.host.Do(ctx, q.OperationID, op, map[string]string{"path": p}, &out); err != nil {
		return hostError(q.RouteID, err)
	}
	return contract.ClientResponse{Status: http.StatusOK, ContentType: "application/json", Body: out}, nil
}

// saveFile answers PUT /files/{path...}: the body is {"content": "..."}.
func (r *Runtime) saveFile(ctx context.Context, q contract.ClientRequest) (contract.ClientResponse, error) {
	p, ok := r.filesPath(q)
	if !ok {
		return reply(http.StatusBadRequest, map[string]string{"error": "invalid path"})
	}
	var in struct {
		Content string `json:"content"`
	}
	dec := json.NewDecoder(bytes.NewReader(q.Body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return reply(http.StatusBadRequest, map[string]string{"error": "content is required"})
	}
	var out json.RawMessage
	if err := r.host.Do(ctx, q.OperationID, "client.workspace.write", map[string]string{"path": p, "content": in.Content}, &out); err != nil {
		return hostError(q.RouteID, err)
	}
	return contract.ClientResponse{Status: http.StatusOK, ContentType: "application/json", Body: out}, nil
}

func (r *Runtime) start(ctx context.Context, q contract.ClientRequest) (contract.ClientResponse, error) {
	var in struct {
		Task   string            `json:"task"`
		Params map[string]string `json:"params"`
	}
	dec := json.NewDecoder(bytes.NewReader(q.Body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || in.Task == "" {
		return reply(http.StatusBadRequest, map[string]string{"error": "task is required"})
	}
	box, err := r.box(q.Client.ID)
	if err != nil {
		return contract.ClientResponse{}, err
	}
	// Take the place before the job exists: a job the outbox cannot watch would
	// end with no event, and two parallel starts must not share one place.
	if err := box.Reserve(); err != nil {
		return reply(http.StatusTooManyRequests, map[string]string{"error": "too many jobs"})
	}
	var j Job
	if err := r.host.Do(ctx, q.OperationID, "client.jobs.start", map[string]any{"task": in.Task, "params": in.Params}, &j); err != nil {
		box.Release()
		return hostError(q.RouteID, err)
	}
	if err := box.Commit(j.ID, j.Status); err != nil {
		box.Release()
		return contract.ClientResponse{}, err
	}
	if _, err := box.Append(EventJobProgress, j); err != nil {
		return contract.ClientResponse{}, err
	}
	return reply(http.StatusAccepted, j)
}

func eventType(status string) string {
	switch status {
	case "completed":
		return EventJobDone
	case "failed", "cancelled":
		return EventJobFailed
	}
	return EventJobProgress
}

// refresh asks the host for the state of every watched job and appends an
// event for each change. Jobs run in the host whether or not a stream is open,
// so a client that reconnects finds the end of a job that finished while it was
// away.
func (r *Runtime) refresh(ctx context.Context, op string, box *Outbox) error {
	for id, last := range box.Tracked() {
		var j Job
		if err := r.host.Do(ctx, op, "client.jobs.get", map[string]string{"id": id}, &j); err != nil {
			var he *contract.HostError
			if errors.As(err, &he) && he.Code == contract.HostNotFound {
				// The job is gone (removed by the retention). Say so and stop.
				// Claim it first: of two streams that saw it, one wins.
				won, err := box.Transition(id, "failed", true)
				if err != nil {
					return err
				}
				if won {
					if _, err := box.Append(EventJobFailed, Job{ID: id, Status: "failed", Error: "the job is gone"}); err != nil {
						return err
					}
				}
				continue
			}
			return err
		}
		if j.Status == last {
			continue
		}
		// Claim the change first: of two streams that saw it, one wins.
		won, err := box.Transition(id, j.Status, ended(j.Status))
		if err != nil {
			return err
		}
		if !won {
			continue
		}
		if _, err := box.Append(eventType(j.Status), j); err != nil {
			return err
		}
	}
	return nil
}

func writeEvent(w io.Writer, e Event) error {
	_, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", e.ID(), e.Type, e.Data)
	return err
}

// ClientStream serves the events of the client as server-sent events. It first
// sends what the outbox holds after Last-Event-ID, then watches the jobs and
// sends each change, until the host closes the stream.
func (r *Runtime) ClientStream(ctx context.Context, q contract.ClientRequest) (io.ReadCloser, error) {
	if q.RouteID != "events" {
		return nil, protocol.ErrNotFound
	}
	var after uint64
	if q.LastEventID != "" {
		n, err := strconv.ParseUint(q.LastEventID, 10, 64)
		if err != nil {
			return nil, protocol.ErrBadRequest
		}
		after = n
	}
	box, err := r.box(q.Client.ID)
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	go func() {
		err := r.serve(ctx, q.OperationID, box, after, pw)
		_ = pw.CloseWithError(err)
	}()
	return pr, nil
}

func (r *Runtime) serve(ctx context.Context, op string, box *Outbox, after uint64, w io.Writer) error {
	send := func() error {
		events, gap := box.After(after)
		if gap {
			// What the client missed is gone. Say so once, with an id that does
			// not move its position, so it asks the state of its jobs.
			if _, err := fmt.Fprintf(w, "event: %s\ndata: {\"reason\":\"events_expired\"}\n\n", EventNotice); err != nil {
				return err
			}
			// The notice is not repeated: the position moves past what is gone.
			after = box.Oldest() - 1
		}
		for _, e := range events {
			if err := writeEvent(w, e); err != nil {
				return err
			}
			after = e.Seq
		}
		return nil
	}
	if err := send(); err != nil {
		return err
	}
	tick := time.NewTicker(r.poll)
	defer tick.Stop()
	for {
		changed := box.Changed()
		if err := r.refresh(ctx, op, box); err != nil && ctx.Err() == nil {
			// A host that is away for a moment must not end the stream; the
			// next tick asks again.
			var he *contract.HostError
			if errors.As(err, &he) && he.Code == contract.HostNoLiveOperation {
				return nil
			}
		}
		if err := send(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		case <-tick.C:
		}
	}
}

// StaleOutboxAge is how long the outbox file of a client may go untouched
// before the add-on removes it. A client that was revoked stops writing, and
// the host has no call to tell the add-on, so its file leaves with its age. A
// client that comes back after that starts a new sequence and gets the notice
// for a Last-Event-ID that is above it.
const StaleOutboxAge = 7 * 24 * time.Hour

// sweepEvery is how often a running add-on looks for stale files. It does it
// when a client asks for an outbox, so there is no timer to stop.
const sweepEvery = time.Hour

// sweepLocked removes the stale files and forgets their outboxes, so a revoked
// client costs neither disk nor memory for long. r.mu is held.
func (r *Runtime) sweepLocked(now time.Time) {
	if now.Sub(r.swept) < sweepEvery {
		return
	}
	r.swept = now
	gone, err := sweepOutboxes(filepath.Join(r.dir, "outbox"), now, StaleOutboxAge, func(name string) time.Time { return r.used[name] })
	if err != nil {
		return
	}
	for _, name := range gone {
		delete(r.outbox, name)
		delete(r.used, name)
	}
}

// sweepOutboxes removes the outbox files that nobody wrote or asked for within
// maxAge, and returns the client ids it removed. lastUse, when set, gives the
// time a client last asked for its outbox in memory; the later of that and the
// time of the file counts.
func sweepOutboxes(dir string, now time.Time, maxAge time.Duration, lastUse func(name string) time.Time) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var gone []string
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		last := info.ModTime()
		if lastUse != nil {
			if t := lastUse(name); t.After(last) {
				last = t
			}
		}
		if now.Sub(last) > maxAge {
			if os.Remove(filepath.Join(dir, e.Name())) == nil {
				gone = append(gone, name)
			}
		}
	}
	return gone, nil
}
