package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/LocalAI/pkg/mcptransport"
	"github.com/mudler/xlog"
)

// The sessions of a model's MCP configuration are shared by every request to
// the model, and a chat looks them up at every turn. A warm lookup makes no
// round trip: a session that answered within livenessInterval is not pinged,
// and its tool list comes from the cache until the server says the list
// changed, the session closes or reconnects, the configuration changes, or the
// list is older than toolListTTL (for a server that never says it changed).
var (
	toolListTTL      = 5 * time.Minute
	livenessInterval = 30 * time.Second
	pingTimeout      = 5 * time.Second
	// retryBackoff is how long a server that failed n times in a row waits
	// before the next connection attempt: the first retry is at the next
	// lookup, then the wait grows, so a server that is down does not cost
	// every turn its connection timeout.
	retryBackoff = []time.Duration{0, 5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}
	// mcpParallel bounds the connections, pings and tool listings of one
	// lookup that run at the same time.
	mcpParallel = 8
)

// sessionState is what the cache knows about one live session. It is shared
// by every copy of the NamedSession and holds the raw tool list of the
// server: the filters of DiscoverMCPTools run on every read, so nothing that
// depends on the caller is cached.
type sessionState struct {
	mu      sync.Mutex
	tools   []*mcp.Tool
	toolsAt time.Time // zero: no valid list
	gen     uint64    // bumped by invalidate, so a list fetched before a change is not kept
	lastOK  time.Time // the last answer of the server
	closed  atomic.Bool
}

// sessionStates maps a live session to its state, for the notification
// handler of the shared client and for the tool calls.
var sessionStates sync.Map // *mcp.ClientSession -> *sessionState

func stateOf(cs *mcp.ClientSession) *sessionState {
	if cs == nil {
		return nil
	}
	if st, ok := sessionStates.Load(cs); ok {
		return st.(*sessionState)
	}
	return nil
}

// track gives a new session its state, and marks it closed when its
// connection ends (an exited stdio server, a closed session).
func track(cs *mcp.ClientSession) *sessionState {
	st := &sessionState{lastOK: time.Now()}
	sessionStates.Store(cs, st)
	go func() {
		_ = cs.Wait()
		st.closed.Store(true)
		st.invalidate()
		sessionStates.Delete(cs)
	}()
	return st
}

func (st *sessionState) invalidate() {
	st.mu.Lock()
	st.toolsAt = time.Time{}
	st.gen++
	st.mu.Unlock()
}

// cached returns the tool list when it is still valid, and the generation a
// fetch must present to store a new one.
func (st *sessionState) cached() ([]*mcp.Tool, uint64, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	ok := !st.toolsAt.IsZero() && time.Since(st.toolsAt) < toolListTTL && !st.closed.Load()
	return st.tools, st.gen, ok
}

func (st *sessionState) store(gen uint64, tools []*mcp.Tool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.lastOK = time.Now()
	if gen == st.gen {
		st.tools, st.toolsAt = tools, time.Now()
	}
}

func (st *sessionState) answered() {
	st.mu.Lock()
	st.lastOK = time.Now()
	st.mu.Unlock()
}

// suspect makes the next lookup ping the session: a request to it failed.
func (st *sessionState) suspect() {
	st.mu.Lock()
	st.lastOK = time.Time{}
	st.mu.Unlock()
}

func (st *sessionState) fresh() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return !st.closed.Load() && !st.lastOK.IsZero() && time.Since(st.lastOK) < livenessInterval
}

// onToolListChanged is the handler of notifications/tools/list_changed for
// every session of the shared client.
func onToolListChanged(_ context.Context, req *mcp.ToolListChangedRequest) {
	if st := stateOf(req.Session); st != nil {
		st.invalidate()
	}
}

// serverEntry is one configured server of a model.
type serverEntry struct {
	ns       NamedSession
	failures int // connection failures in a row
	tried    time.Time
}

// modelSessions holds the sessions of one model's configuration. Its lock is
// held across the connections and pings of a lookup, so two lookups of one
// model never connect the same server twice; lookups of other models do not
// wait for it.
type modelSessions struct {
	mu      sync.Mutex
	hash    [sha256.Size]byte
	ctx     context.Context
	cancel  context.CancelFunc
	servers []*serverEntry // nil until the first lookup connects them
	closed  bool
}

type namedSessionCache struct {
	mu     sync.Mutex // guards models only; never held across a network call
	models map[string]*modelSessions
}

var namedCache = namedSessionCache{models: make(map[string]*modelSessions)}

func (m *modelSessions) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	for _, e := range m.servers {
		if e.ns.Session != nil {
			if err := e.ns.Session.Close(); err != nil {
				xlog.Debug("Failed to close MCP session", "server", e.ns.Name, "error", err)
			}
		}
	}
	m.cancel()
}

// takeNamed removes the sessions of name from the cache and returns them, to
// be closed without the cache lock.
func takeNamed(name string) *modelSessions {
	namedCache.mu.Lock()
	defer namedCache.mu.Unlock()
	m := namedCache.models[name]
	delete(namedCache.models, name)
	return m
}

func closeAllNamed() {
	namedCache.mu.Lock()
	models := namedCache.models
	namedCache.models = make(map[string]*modelSessions)
	namedCache.mu.Unlock()
	for _, m := range models {
		m.close()
	}
}

// parallel runs fn for 0..n-1 with at most mcpParallel at a time.
func parallel(n int, fn func(i int)) {
	sem := make(chan struct{}, mcpParallel)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			fn(i)
		}()
	}
	wg.Wait()
}

// connectNamedSession connects one configured server. A failure is kept as a
// session with no client and the error, so the server stays visible.
func connectNamedSession(ctx context.Context, serverName, kind string,
	remote config.MCPGenericConfig[config.MCPRemoteServers],
	stdio config.MCPGenericConfig[config.MCPSTDIOServers],
) NamedSession {
	if kind == "remote" {
		server := remote.Servers[serverName]
		xlog.Debug("[MCP remote server] Configuration", "name", serverName, "server", server)
		authTransport, err := newRemoteServerTransport(server, httpclient.HardenedTransport())
		if err != nil {
			xlog.Error("Invalid MCP server authentication", "error", err, "name", serverName, "url", server.URL)
			return NamedSession{Name: serverName, Type: "remote", Error: fmt.Sprintf("authentication setup failed: %v", err)}
		}
		httpClient := httpclient.New(
			httpclient.WithTimeout(config.DefaultMCPToolTimeout),
			httpclient.WithTransport(authTransport),
		)
		mcpSession, err := mcptransport.Connect(ctx, client, server.URL, httpClient, config.DefaultMCPDiscoveryTimeout)
		if err != nil {
			xlog.Error("Failed to connect to MCP server", "error", err, "name", serverName, "url", server.URL)
			return NamedSession{Name: serverName, Type: "remote", Error: fmt.Sprintf("connection failed: %v", err)}
		}
		xlog.Debug("[MCP remote server] Connected", "name", serverName, "url", server.URL)
		return NamedSession{Name: serverName, Type: "remote", Session: mcpSession, state: track(mcpSession)}
	}
	server := stdio.Servers[serverName]
	xlog.Debug("[MCP stdio server] Configuration", "name", serverName, "server", server)
	command := exec.Command(server.Command, server.Args...) // #nosec G204 -- the command is the administrator's own MCP server configuration
	command.Env = os.Environ()
	for key, value := range server.Env {
		command.Env = append(command.Env, key+"="+value)
	}
	transport := &mcp.CommandTransport{Command: command}
	mcpSession, err := connectMCP(ctx, transport, config.DefaultMCPDiscoveryTimeout)
	if err != nil {
		xlog.Error("Failed to start MCP server", "error", err, "name", serverName, "command", command)
		return NamedSession{Name: serverName, Type: "stdio", Error: fmt.Sprintf("startup failed: %v", err)}
	}
	xlog.Debug("[MCP stdio server] Connected", "name", serverName, "command", command)
	return NamedSession{Name: serverName, Type: "stdio", Session: mcpSession, state: track(mcpSession)}
}

// connect connects e again and counts the failures for its backoff.
func (m *modelSessions) connect(e *serverEntry, remote config.MCPGenericConfig[config.MCPRemoteServers], stdio config.MCPGenericConfig[config.MCPSTDIOServers]) {
	e.tried = time.Now()
	e.ns = connectNamedSession(m.ctx, e.ns.Name, e.ns.Type, remote, stdio)
	if e.ns.Session == nil {
		e.failures++
	} else {
		e.failures = 0
	}
}

// refresh brings the sessions of a cached model up to date. A session that
// answered recently is kept as it is. One that did not is pinged, and
// connected again if the ping fails or its connection has ended. A server
// that failed to connect is tried again once its backoff has passed. The
// other servers keep their sessions and their tool lists.
func (m *modelSessions) refresh(remote config.MCPGenericConfig[config.MCPRemoteServers], stdio config.MCPGenericConfig[config.MCPSTDIOServers]) {
	parallel(len(m.servers), func(i int) {
		e := m.servers[i]
		if e.ns.Session == nil {
			wait := retryBackoff[min(max(e.failures-1, 0), len(retryBackoff)-1)]
			if time.Since(e.tried) >= wait {
				m.connect(e, remote, stdio)
			}
			return
		}
		st := e.ns.state
		if st == nil || st.fresh() {
			return
		}
		if !st.closed.Load() {
			ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
			err := e.ns.Session.Ping(ctx, nil)
			cancel()
			if err == nil {
				st.answered()
				return
			}
			xlog.Warn("MCP session dead, reconnecting", "server", e.ns.Name, "error", err)
		}
		if err := e.ns.Session.Close(); err != nil {
			xlog.Debug("Failed to close MCP session", "server", e.ns.Name, "error", err)
		}
		m.connect(e, remote, stdio)
	})
}

// NamedSessionsFromMCPConfig returns sessions with their server names preserved.
// If enabledServers is non-empty, only servers with matching names are returned.
// The remote servers come first, then the stdio servers, each in name order.
func NamedSessionsFromMCPConfig(
	name string,
	remote config.MCPGenericConfig[config.MCPRemoteServers],
	stdio config.MCPGenericConfig[config.MCPSTDIOServers],
	enabledServers []string,
) ([]NamedSession, error) {
	configJSON, _ := json.Marshal(struct {
		Remote config.MCPGenericConfig[config.MCPRemoteServers] `json:"remote"`
		Stdio  config.MCPGenericConfig[config.MCPSTDIOServers]  `json:"stdio"`
	}{Remote: remote, Stdio: stdio})
	configHash := sha256.Sum256(configJSON)

	var all []NamedSession
	for {
		namedCache.mu.Lock()
		m := namedCache.models[name]
		var stale *modelSessions
		if m != nil && m.hash != configHash {
			stale, m = m, nil
		}
		if m == nil {
			ctx, cancel := context.WithCancel(context.Background())
			m = &modelSessions{hash: configHash, ctx: ctx, cancel: cancel}
			namedCache.models[name] = m
		}
		namedCache.mu.Unlock()
		if stale != nil {
			stale.close()
		}

		m.mu.Lock()
		if m.closed {
			// Closed or replaced while this lookup waited: look again.
			m.mu.Unlock()
			continue
		}
		if m.servers == nil {
			m.servers = []*serverEntry{}
			for _, n := range sortedKeys(remote.Servers) {
				m.servers = append(m.servers, &serverEntry{ns: NamedSession{Name: n, Type: "remote"}})
			}
			for _, n := range sortedKeys(stdio.Servers) {
				m.servers = append(m.servers, &serverEntry{ns: NamedSession{Name: n, Type: "stdio"}})
			}
			parallel(len(m.servers), func(i int) { m.connect(m.servers[i], remote, stdio) })
		} else {
			m.refresh(remote, stdio)
		}
		all = make([]NamedSession, len(m.servers))
		for i, e := range m.servers {
			all[i] = e.ns
		}
		m.mu.Unlock()
		break
	}

	if len(enabledServers) == 0 {
		return all, nil
	}
	enabled := make(map[string]bool, len(enabledServers))
	for _, s := range enabledServers {
		enabled[s] = true
	}
	var filtered []NamedSession
	for _, ns := range all {
		if enabled[ns.Name] {
			filtered = append(filtered, ns)
		}
	}
	return filtered, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// listTools returns the raw tool list of each session, from the cache where it
// is valid. The misses are listed in parallel, each within the discovery
// timeout. A session that cannot list its tools gives nil and is pinged at the
// next lookup.
func listTools(ctx context.Context, sessions []NamedSession) [][]*mcp.Tool {
	lists := make([][]*mcp.Tool, len(sessions))
	var misses []int
	gens := make([]uint64, len(sessions))
	for i, ns := range sessions {
		if ns.Session == nil {
			continue
		}
		if ns.state != nil {
			tools, gen, ok := ns.state.cached()
			if ok {
				lists[i] = tools
				continue
			}
			gens[i] = gen
		}
		misses = append(misses, i)
	}
	parallel(len(misses), func(j int) {
		i := misses[j]
		ns := sessions[i]
		lctx, cancel := context.WithTimeout(ctx, config.DefaultMCPDiscoveryTimeout)
		defer cancel()
		res, err := ns.Session.ListTools(lctx, nil)
		if err != nil {
			xlog.Error("Failed to list tools from MCP server", "error", err, "server", ns.Name)
			if ns.state != nil {
				ns.state.suspect()
			}
			return
		}
		lists[i] = res.Tools
		if ns.state != nil {
			ns.state.store(gens[i], res.Tools)
		}
	})
	return lists
}
