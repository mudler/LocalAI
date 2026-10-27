package mcpconns

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/mcpapps"
)

// AnonymousOwner is the owner of a server without accounts.
const AnonymousOwner = "anonymous"

// Prefix names a connector in the MCP list of a saved agent: "connector:<id>".
// A name without it is a server of the model.
const Prefix = "connector:"

// Defaults of Options.
const (
	DefaultIdleTimeout    = 10 * time.Minute
	DefaultConnectTimeout = 15 * time.Second
	DefaultCallTimeout    = 60 * time.Second
)

// Options configure a Service.
type Options struct {
	Store Store
	// Key returns the sealing key. The studio shares the key of its Telegram
	// connectors; a sealed value is tied to its connector and owner either way.
	Key func(ctx context.Context) ([]byte, error)
	// Permitted says whether the user may use MCP at all: the feature is on for
	// the user and the server has not turned MCP off. It is asked on every use.
	Permitted func(ctx context.Context, user string) bool
	// IsAdmin says whether the user is an administrator. Only an administrator
	// may use a local command. Nil: nobody is.
	IsAdmin func(ctx context.Context, user string) bool
	// AllowPrivate is the administrator's switch for addresses on private
	// networks. Nil: never allowed. It is read at each connection.
	AllowPrivate func() bool
	// Distributed refuses local commands: they would run on whichever frontend
	// serves the request.
	Distributed                              bool
	Clock                                    func() time.Time
	IdleTimeout, ConnectTimeout, CallTimeout time.Duration
}

// Input is a connector as its owner writes it.
type Input struct {
	Name, Transport string
	URL             string
	Command         string
	Args            []string
	// Token, Headers and Env are write-only. Nil keeps what is stored; an empty
	// value clears it.
	Token   *string
	Headers *map[string]string
	Env     *map[string]string
	Enabled bool
}

// Probe is the outcome of a connection test. Error is empty when it worked.
type Probe struct {
	Tools []string
	Error string
}

// Tool is a tool of a connector, ready to run.
type Tool struct {
	ConnectorID, Connector string
	Name, Description      string
	Schema                 json.RawMessage
	// Meta is the _meta of the tool, where MCP Apps declare their view.
	Meta map[string]any
	Run  func(ctx context.Context, args json.RawMessage) (string, error)
	// RunApp runs the tool like Run, and also returns the full result for an
	// app: the structured content that the model's text leaves out.
	RunApp func(ctx context.Context, args json.RawMessage) (mcpapps.CallResult, error)
}

type session struct {
	owner   string
	version int64
	conn    *conn
	last    time.Time
	// private is true when the session was opened while the administrator
	// allowed private addresses. It must not outlive that permission.
	private bool
}

// Service is the connectors of the users and the MCP sessions they open. The
// session cache lives in the memory of one frontend: in a cluster the records
// are shared in the database and each frontend opens its own sessions. A
// frontend notices an edit made on another by the record's update time.
type Service struct {
	o      Options
	mu     sync.Mutex
	wmu    sync.Mutex // serialises writes, so the per-user limit holds
	sealer *connectors.Sealer
	sess   map[string]*session
	stop   chan struct{}
	once   sync.Once
}

// New returns a Service.
func New(o Options) (*Service, error) {
	if o.Store == nil || o.Key == nil {
		return nil, errors.New("mcpconns: a store and a key are required")
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Permitted == nil {
		o.Permitted = func(context.Context, string) bool { return true }
	}
	if o.IsAdmin == nil {
		o.IsAdmin = func(context.Context, string) bool { return false }
	}
	if o.AllowPrivate == nil {
		o.AllowPrivate = func() bool { return false }
	}
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = DefaultIdleTimeout
	}
	if o.ConnectTimeout <= 0 {
		o.ConnectTimeout = DefaultConnectTimeout
	}
	if o.CallTimeout <= 0 {
		o.CallTimeout = DefaultCallTimeout
	}
	s := &Service{o: o, sess: map[string]*session{}, stop: make(chan struct{})}
	s.startReaper()
	return s, nil
}

// Close ends every session and the reaper.
func (s *Service) Close() {
	s.once.Do(func() { close(s.stop) })
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, se := range s.sess {
		se.conn.close()
		delete(s.sess, id)
	}
}

// Sessions is the number of open sessions.
func (s *Service) Sessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sess)
}

// ReapIdle closes the sessions that nobody used for the idle timeout.
func (s *Service) ReapIdle() {
	cutoff := s.o.Clock().Add(-s.o.IdleTimeout)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, se := range s.sess {
		if se.last.Before(cutoff) {
			se.conn.close()
			delete(s.sess, id)
		}
	}
}

func (s *Service) startReaper() {
	go func() {
		t := time.NewTicker(max(s.o.IdleTimeout/2, time.Second))
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.ReapIdle()
			}
		}
	}()
}

func (s *Service) invalidate(id string) {
	s.mu.Lock()
	se := s.sess[id]
	delete(s.sess, id)
	s.mu.Unlock()
	if se != nil {
		se.conn.close()
	}
}

func owner(user string) string {
	if user == "" {
		return AnonymousOwner
	}
	return user
}

func aad(id, own string) string { return "mcpconn|" + id + "|" + own }

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Service) sealerFor(ctx context.Context) (*connectors.Sealer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sealer != nil {
		return s.sealer, nil
	}
	key, err := s.o.Key(ctx)
	if err != nil {
		return nil, err
	}
	sl, err := connectors.NewSealer(key)
	if err != nil {
		return nil, err
	}
	s.sealer = sl
	return sl, nil
}

func (s *Service) seal(ctx context.Context, c Connector, sec Secrets) ([]byte, error) {
	if sec.Token == "" && len(sec.Headers) == 0 && len(sec.Env) == 0 {
		return nil, nil
	}
	sl, err := s.sealerFor(ctx)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(sec)
	return sl.Seal(aad(c.ID, c.Owner), raw)
}

func (s *Service) open(ctx context.Context, c Connector) (Secrets, error) {
	var sec Secrets
	if len(c.Secrets) == 0 {
		return sec, nil
	}
	sl, err := s.sealerFor(ctx)
	if err != nil {
		return sec, err
	}
	raw, err := sl.Open(aad(c.ID, c.Owner), c.Secrets)
	if err != nil {
		return sec, errors.New("the saved secrets cannot be read: save the connector again with them")
	}
	return sec, json.Unmarshal(raw, &sec)
}

func (s *Service) permit(ctx context.Context, user string) error {
	if !s.o.Permitted(ctx, user) {
		return fmt.Errorf("%w: MCP is not available to this account", ErrForbidden)
	}
	return nil
}

// stdioAllowed says why a local command may not run, or nil.
func (s *Service) stdioAllowed(ctx context.Context, user string) error {
	if s.o.Distributed {
		return bad("a local command cannot run in distributed mode: it would run on whichever frontend answers. Use an http server")
	}
	if !s.o.IsAdmin(ctx, user) {
		return fmt.Errorf("%w: only an administrator can use a local command", ErrForbidden)
	}
	return nil
}

func public(c Connector) Connector {
	c.Secrets = nil
	return c.clone()
}

func (s *Service) owned(ctx context.Context, user, id string) (Connector, error) {
	c, err := s.o.Store.Get(ctx, id)
	if err != nil || c.Owner != owner(user) {
		return Connector{}, ErrNotFound // another user's connector is a missing one
	}
	return c, nil
}

// apply builds the connector of an input over the stored one (zero for a new one).
func (s *Service) apply(ctx context.Context, user string, old Connector, in Input) (Connector, error) {
	c := old
	c.Name, c.Transport, c.Enabled = strings.TrimSpace(in.Name), in.Transport, in.Enabled
	c.URL, c.Command, c.Args = strings.TrimSpace(in.URL), strings.TrimSpace(in.Command), in.Args
	if err := Validate(c, s.o.AllowPrivate()); err != nil {
		return c, err
	}
	if c.Transport == TransportStdio {
		if err := s.stdioAllowed(ctx, user); err != nil {
			return c, err
		}
	}
	sec := Secrets{}
	if len(old.Secrets) > 0 {
		var err error
		if sec, err = s.open(ctx, old); err != nil {
			sec = Secrets{} // unreadable: whatever is given now replaces it
			c.HasToken, c.HasHeaders, c.HasEnv = false, false, false
		}
	}
	if in.Token != nil {
		sec.Token = strings.TrimSpace(*in.Token)
	}
	if in.Headers != nil {
		sec.Headers = *in.Headers
	}
	if in.Env != nil {
		sec.Env = *in.Env
	}
	if c.Transport == TransportHTTP {
		sec.Env = nil
	} else {
		sec.Token, sec.Headers = "", nil
	}
	if err := ValidateSecrets(sec); err != nil {
		return c, err
	}
	c.HasToken, c.HasHeaders, c.HasEnv = sec.Token != "", len(sec.Headers) > 0, len(sec.Env) > 0
	var err error
	c.Secrets, err = s.seal(ctx, c, sec)
	return c, err
}

// Create makes a connector of the user.
func (s *Service) Create(ctx context.Context, user string, in Input) (Connector, error) {
	if err := s.permit(ctx, user); err != nil {
		return Connector{}, err
	}
	own := owner(user)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	mine, err := s.o.Store.List(ctx, own)
	if err != nil {
		return Connector{}, err
	}
	if len(mine) >= MaxPerUser {
		return Connector{}, bad("you can have at most %d connectors", MaxPerUser)
	}
	c, err := s.apply(ctx, user, Connector{ID: newID(), Owner: own}, in)
	if err != nil {
		return Connector{}, err
	}
	c.Created = s.o.Clock().UTC()
	c.Updated = c.Created
	if err := s.o.Store.Put(ctx, c); err != nil {
		return Connector{}, err
	}
	return public(c), nil
}

// Update replaces the connector's definition. The secrets that the input does
// not name are kept.
func (s *Service) Update(ctx context.Context, user, id string, in Input) (Connector, error) {
	if err := s.permit(ctx, user); err != nil {
		return Connector{}, err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	old, err := s.owned(ctx, user, id)
	if err != nil {
		return Connector{}, err
	}
	c, err := s.apply(ctx, user, old, in)
	if err != nil {
		return Connector{}, err
	}
	c.Updated = s.o.Clock().UTC()
	if !c.Updated.After(old.Updated) {
		c.Updated = old.Updated.Add(time.Nanosecond)
	}
	if err := s.o.Store.Put(ctx, c); err != nil {
		return Connector{}, err
	}
	s.invalidate(id)
	return public(c), nil
}

// SetEnabled switches a connector on or off.
func (s *Service) SetEnabled(ctx context.Context, user, id string, on bool) (Connector, error) {
	if err := s.permit(ctx, user); err != nil {
		return Connector{}, err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	c, err := s.owned(ctx, user, id)
	if err != nil {
		return Connector{}, err
	}
	c.Enabled = on
	if on {
		if err := Validate(c, s.o.AllowPrivate()); err != nil {
			return Connector{}, err
		}
		if c.Transport == TransportStdio {
			if err := s.stdioAllowed(ctx, user); err != nil {
				return Connector{}, err
			}
		}
	}
	c.Updated = s.o.Clock().UTC()
	if err := s.o.Store.Put(ctx, c); err != nil {
		return Connector{}, err
	}
	s.invalidate(id)
	return public(c), nil
}

// Get returns a connector of the user without its secrets.
func (s *Service) Get(ctx context.Context, user, id string) (Connector, error) {
	if err := s.permit(ctx, user); err != nil {
		return Connector{}, err
	}
	c, err := s.owned(ctx, user, id)
	return public(c), err
}

// List returns the user's connectors without their secrets.
func (s *Service) List(ctx context.Context, user string) ([]Connector, error) {
	if err := s.permit(ctx, user); err != nil {
		return nil, err
	}
	all, err := s.o.Store.List(ctx, owner(user))
	if err != nil {
		return nil, err
	}
	for i := range all {
		all[i] = public(all[i])
	}
	return all, nil
}

// Delete removes a connector of the user and closes its session. It works
// without the MCP feature, so a user can always clean up.
func (s *Service) Delete(ctx context.Context, user, id string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.owned(ctx, user, id); err != nil {
		return err
	}
	if err := s.o.Store.Delete(ctx, id); err != nil {
		return err
	}
	s.invalidate(id)
	return nil
}

// Test connects with the stored definition and lists the tool names. The
// session is not kept. It works for a connector that is switched off, so a
// person can check one before turning it on.
func (s *Service) Test(ctx context.Context, user, id string) (Probe, error) {
	if err := s.permit(ctx, user); err != nil {
		return Probe{}, err
	}
	c, err := s.owned(ctx, user, id)
	if err != nil {
		return Probe{}, err
	}
	if c.Transport == TransportStdio {
		if err := s.stdioAllowed(ctx, user); err != nil {
			return Probe{}, err
		}
	}
	sec, err := s.open(ctx, c)
	if err != nil {
		return Probe{Error: reason(err)}, nil
	}
	cn, err := s.openConn(c, sec)
	if err != nil {
		return Probe{Error: redact(reason(err), c, sec)}, nil
	}
	defer cn.close()
	p := Probe{Tools: make([]string, 0, len(cn.tools))}
	for _, t := range cn.tools {
		p.Tools = append(p.Tools, t.Name)
	}
	return p, nil
}

// reason is the text of err for a person: the package's own marker is left out,
// as the routes leave it out of their errors.
func reason(err error) string {
	return strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")
}

// session returns the open session of a connector, opening one when there is
// none or when the record changed since it was opened.
func (s *Service) session(ctx context.Context, c Connector) (*session, error) {
	version := c.Updated.UnixNano()
	s.mu.Lock()
	if se := s.sess[c.ID]; se != nil {
		if se.owner == c.Owner && se.version == version && !s.stale(se) {
			se.last = s.o.Clock()
			s.mu.Unlock()
			return se, nil
		}
		delete(s.sess, c.ID)
		defer se.conn.close()
	}
	s.mu.Unlock()
	sec, err := s.open(ctx, c)
	if err != nil {
		return nil, err
	}
	cn, err := s.openConn(c, sec)
	if err != nil {
		return nil, errors.New(redact(err.Error(), c, sec))
	}
	se := &session{owner: c.Owner, version: version, conn: cn, last: s.o.Clock(), private: s.o.AllowPrivate()}
	s.mu.Lock()
	if other := s.sess[c.ID]; other != nil && other.version == version && other.owner == c.Owner {
		s.mu.Unlock()
		cn.close()
		return other, nil
	}
	s.sess[c.ID] = se
	s.mu.Unlock()
	return se, nil
}

// stale says that the administrator's policy no longer allows the session.
func (s *Service) stale(se *session) bool { return se.private && !s.o.AllowPrivate() }

// drop closes and forgets the session of a connector, if there is one.
func (s *Service) drop(id string) {
	s.mu.Lock()
	se := s.sess[id]
	delete(s.sess, id)
	s.mu.Unlock()
	if se != nil {
		se.conn.close()
	}
}

func label(c Connector, reason string) string {
	return "MCP connector " + c.Name + " (" + reason + ")"
}

// Resolve returns the tools of the user's connectors, and a phrase for each
// connector that could not be used, for a note to the model. With all, every
// enabled connector of the user is used; otherwise refs are connector IDs. A
// connector that is switched off, deleted, another user's or unreachable is a
// phrase, never an error: a turn must not fail because of it.
func (s *Service) Resolve(ctx context.Context, user string, refs []string, all bool) ([]Tool, []string) {
	own := owner(user)
	if err := s.permit(ctx, user); err != nil {
		if all {
			return nil, nil
		}
		out := make([]string, len(refs))
		for i, r := range refs {
			out[i] = "MCP connector " + r + " (not permitted)"
		}
		return nil, out
	}
	var list []Connector
	var missing []string
	if all {
		mine, err := s.o.Store.List(ctx, own)
		if err != nil {
			xlog.Warn("mcpconns: could not list connectors", "error", err)
			return nil, nil
		}
		for _, c := range mine {
			if c.Enabled {
				list = append(list, c)
			}
		}
	} else {
		seen := map[string]bool{}
		for _, id := range refs {
			if seen[id] {
				continue
			}
			seen[id] = true
			c, err := s.o.Store.Get(ctx, id)
			switch {
			case err != nil || c.Owner != own:
				missing = append(missing, "MCP connector "+id+" (not found)")
			case !c.Enabled:
				missing = append(missing, label(c, "switched off"))
			default:
				list = append(list, c)
			}
		}
	}
	type outcome struct {
		tools   []Tool
		missing string
	}
	res := make([]outcome, len(list))
	var wg sync.WaitGroup
	for i, c := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res[i].tools, res[i].missing = s.toolsOf(ctx, user, c)
		}()
	}
	wg.Wait()
	var tools []Tool
	for _, r := range res {
		tools = append(tools, r.tools...)
		if r.missing != "" {
			missing = append(missing, r.missing)
		}
	}
	return tools, missing
}

func (s *Service) toolsOf(ctx context.Context, user string, c Connector) ([]Tool, string) {
	if c.Transport == TransportStdio {
		if err := s.stdioAllowed(ctx, user); err != nil {
			s.drop(c.ID) // a command that is not allowed any more must not keep running
			return nil, label(c, "a local command is not allowed here")
		}
	}
	se, err := s.session(ctx, c)
	if err != nil {
		xlog.Warn("mcpconns: a connector could not be reached", "connector", c.ID, "error", err)
		return nil, label(c, "cannot be reached")
	}
	tools := make([]Tool, 0, len(se.conn.tools))
	for _, t := range se.conn.tools {
		name := t.Name
		schema := json.RawMessage(`{"type":"object"}`)
		if t.InputSchema != nil {
			if raw, err := json.Marshal(t.InputSchema); err == nil {
				schema = raw
			}
		}
		tools = append(tools, Tool{ConnectorID: c.ID, Connector: c.Name, Name: name, Description: t.Description, Schema: schema, Meta: t.Meta,
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				out, _, err := s.call(ctx, c, se, name, args)
				return out, err
			},
			RunApp: func(ctx context.Context, args json.RawMessage) (mcpapps.CallResult, error) {
				out, raw, err := s.call(ctx, c, se, name, args)
				return mcpapps.CallResult{Text: out, Result: raw}, err
			}})
	}
	return tools, ""
}

// call runs a tool and returns the text for the model and the full result for
// an app.
func (s *Service) call(ctx context.Context, c Connector, se *session, name string, args json.RawMessage) (string, json.RawMessage, error) {
	var in map[string]any
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", nil, fmt.Errorf("the arguments of %s are not a JSON object", name)
		}
	}
	s.mu.Lock()
	stale := s.stale(se)
	if stale && s.sess[c.ID] == se {
		// The administrator turned private addresses off after this tool was
		// handed out. Close the session; the next turn connects under the new rule.
		delete(s.sess, c.ID)
		defer se.conn.close()
	}
	s.mu.Unlock()
	if stale {
		return "", nil, fmt.Errorf("the connector %s is on a private address, which an administrator no longer allows", c.Name)
	}
	cctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
	defer cancel()
	res, err := se.conn.cs.CallTool(cctx, &mcp.CallToolParams{Name: name, Arguments: in})
	if err != nil {
		// A broken session is dropped so that the next turn reconnects.
		s.mu.Lock()
		if s.sess[c.ID] == se {
			delete(s.sess, c.ID)
			defer se.conn.close()
		}
		s.mu.Unlock()
		if cctx.Err() != nil {
			return "", nil, fmt.Errorf("the tool %s of %s did not answer within %s", name, c.Name, s.o.CallTimeout)
		}
		sec, _ := s.open(ctx, c)
		return "", nil, fmt.Errorf("the tool %s of %s failed: %s", name, c.Name, redact(err.Error(), c, sec))
	}
	s.mu.Lock()
	se.last = s.o.Clock()
	s.mu.Unlock()
	var texts []string
	for _, ct := range res.Content {
		if tc, ok := ct.(*mcp.TextContent); ok {
			texts = append(texts, tc.Text)
		}
	}
	var out string
	switch {
	case len(texts) == 0:
		raw, _ := json.Marshal(res.Content)
		out = string(raw)
	case len(texts) == 1:
		out = texts[0]
	default:
		raw, _ := json.Marshal(texts)
		out = string(raw)
	}
	if res.IsError {
		out = "tool error: " + out
	}
	return out, mcpapps.FromCallResult(res), nil
}

// ReadResource reads a ui:// resource of the user's own enabled connector.
// The caller decides whether a tool of the connector declared the resource.
// An error never holds a secret of the connector.
func (s *Service) ReadResource(ctx context.Context, user, id, uri string) (mcpapps.Resource, error) {
	if err := s.permit(ctx, user); err != nil {
		return mcpapps.Resource{}, err
	}
	if err := mcpapps.CheckURI(uri); err != nil {
		return mcpapps.Resource{}, bad("%s", err)
	}
	c, err := s.owned(ctx, user, id)
	if err != nil {
		return mcpapps.Resource{}, err
	}
	if !c.Enabled {
		return mcpapps.Resource{}, fmt.Errorf("%w: the connector is switched off", ErrForbidden)
	}
	if c.Transport == TransportStdio {
		if err := s.stdioAllowed(ctx, user); err != nil {
			return mcpapps.Resource{}, err
		}
	}
	se, err := s.session(ctx, c)
	if err != nil {
		return mcpapps.Resource{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
	defer cancel()
	res, err := se.conn.cs.ReadResource(cctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		sec, _ := s.open(ctx, c)
		return mcpapps.Resource{}, fmt.Errorf("the connector could not read the resource: %s", redact(err.Error(), c, sec))
	}
	s.mu.Lock()
	se.last = s.o.Clock()
	s.mu.Unlock()
	return mcpapps.FromReadResult(res, uri)
}
