package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// countingServer is a remote MCP server that counts the requests it gets by
// method, so a spec can tell a cached answer from a round trip.
type countingServer struct {
	srv   *mcp.Server
	ts    *httptest.Server
	mu    sync.Mutex
	calls map[string]int
}

func newCountingServer(tools ...string) *countingServer {
	c := &countingServer{calls: map[string]int{}}
	c.srv = mcp.NewServer(&mcp.Implementation{Name: "counting", Version: "v0.0.1"}, nil)
	for _, name := range tools {
		c.addTool(name)
	}
	c.srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			c.mu.Lock()
			c.calls[method]++
			c.mu.Unlock()
			return next(ctx, method, req)
		}
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return c.srv }, nil)
	c.ts = httptest.NewServer(handler)
	// The client keeps a stream open; Close would wait for it.
	DeferCleanup(func() { c.ts.CloseClientConnections(); c.ts.Close() })
	return c
}

func (c *countingServer) addTool(name string) {
	mcp.AddTool(c.srv, &mcp.Tool{Name: name, Description: "test tool"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
		})
}

func (c *countingServer) count(method string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[method]
}

func remoteServers(servers map[string]string) config.MCPGenericConfig[config.MCPRemoteServers] {
	out := config.MCPGenericConfig[config.MCPRemoteServers]{Servers: config.MCPRemoteServers{}}
	for name, url := range servers {
		out.Servers[name] = config.MCPRemoteServer{URL: url}
	}
	return out
}

var noStdio = config.MCPGenericConfig[config.MCPSTDIOServers]{}

// lookupTools is what a chat turn does: look the sessions up, then list
// their tools. It returns the sessions and the sorted tool names.
func lookupTools(model string, remote config.MCPGenericConfig[config.MCPRemoteServers]) ([]NamedSession, []string) {
	sessions, err := NamedSessionsFromMCPConfig(model, remote, noStdio, nil)
	Expect(err).NotTo(HaveOccurred())
	tools, err := DiscoverMCPTools(context.Background(), sessions)
	Expect(err).NotTo(HaveOccurred())
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.ToolName)
	}
	sort.Strings(names)
	return sessions, names
}

func sessionNamed(sessions []NamedSession, name string) NamedSession {
	for _, s := range sessions {
		if s.Name == name {
			return s
		}
	}
	return NamedSession{}
}

var _ = Describe("MCP tool list cache", func() {
	var model string
	BeforeEach(func() {
		model = "tool-cache-" + CurrentSpecReport().LeafNodeText
		DeferCleanup(func() { CloseMCPSessions(model) })
	})

	It("answers a warm lookup without any round trip to the server", func() {
		s := newCountingServer("echo")
		remote := remoteServers(map[string]string{"a": s.ts.URL})
		_, names := lookupTools(model, remote)
		Expect(names).To(Equal([]string{"echo"}))
		Expect(s.count("tools/list")).To(Equal(1))
		before := map[string]int{"ping": s.count("ping"), "tools/list": s.count("tools/list"), "initialize": s.count("initialize")}

		for range 3 {
			_, names = lookupTools(model, remote)
			Expect(names).To(Equal([]string{"echo"}))
		}
		Expect(s.count("ping")).To(Equal(before["ping"]), "a session that answered recently is not pinged")
		Expect(s.count("tools/list")).To(Equal(before["tools/list"]), "the tool list comes from the cache")
		Expect(s.count("initialize")).To(Equal(before["initialize"]))
	})

	It("pings a session again only after the liveness interval", func() {
		old := livenessInterval
		livenessInterval = 0
		DeferCleanup(func() { livenessInterval = old })
		s := newCountingServer("echo")
		remote := remoteServers(map[string]string{"a": s.ts.URL})
		lookupTools(model, remote)
		pings := s.count("ping")
		lookupTools(model, remote)
		Expect(s.count("ping")).To(Equal(pings+1), "a session with no recent answer is pinged")
		Expect(s.count("tools/list")).To(Equal(1), "a ping does not list the tools again")
	})

	It("lists the tools again when the server says they changed", func() {
		s := newCountingServer("echo")
		remote := remoteServers(map[string]string{"a": s.ts.URL})
		_, names := lookupTools(model, remote)
		Expect(names).To(Equal([]string{"echo"}))
		s.addTool("added")
		Eventually(func() []string {
			_, names := lookupTools(model, remote)
			return names
		}, 5*time.Second, 20*time.Millisecond).Should(Equal([]string{"added", "echo"}))
	})

	It("lists the tools again when the cached list is older than the TTL", func() {
		old := toolListTTL
		toolListTTL = 20 * time.Millisecond
		DeferCleanup(func() { toolListTTL = old })
		s := newCountingServer("echo")
		remote := remoteServers(map[string]string{"a": s.ts.URL})
		lookupTools(model, remote)
		Expect(s.count("tools/list")).To(Equal(1))
		time.Sleep(40 * time.Millisecond)
		lookupTools(model, remote)
		Expect(s.count("tools/list")).To(Equal(2))
	})

	It("drops the sessions and their lists when the configuration changes", func() {
		s := newCountingServer("echo")
		other := newCountingServer("other")
		lookupTools(model, remoteServers(map[string]string{"a": s.ts.URL}))
		Expect(s.count("initialize")).To(Equal(1))
		_, names := lookupTools(model, remoteServers(map[string]string{"a": s.ts.URL, "b": other.ts.URL}))
		Expect(names).To(Equal([]string{"echo", "other"}))
		Expect(s.count("initialize")).To(Equal(2), "a new configuration connects again")
		Expect(s.count("tools/list")).To(Equal(2), "a new configuration does not reuse the old lists")
	})

	It("connects again and lists the tools again after a session closes", func() {
		s := newCountingServer("echo")
		remote := remoteServers(map[string]string{"a": s.ts.URL})
		sessions, _ := lookupTools(model, remote)
		first := sessionNamed(sessions, "a").Session
		Expect(first).NotTo(BeNil())
		Expect(first.Close()).To(Succeed())
		Eventually(func() *mcp.ClientSession {
			sessions, _ := lookupTools(model, remote)
			return sessionNamed(sessions, "a").Session
		}, 5*time.Second, 20*time.Millisecond).ShouldNot(BeIdenticalTo(first))
		Expect(s.count("initialize")).To(Equal(2))
		Expect(s.count("tools/list")).To(Equal(2))
	})

	It("keeps the lists of the live servers while another server is down", func() {
		live := newCountingServer("echo")
		var deadRequests int
		var mu sync.Mutex
		dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			deadRequests++
			mu.Unlock()
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		}))
		DeferCleanup(dead.Close)
		remote := remoteServers(map[string]string{"live": live.ts.URL, "dead": dead.URL})
		deadCount := func() int {
			mu.Lock()
			defer mu.Unlock()
			return deadRequests
		}
		var afterRetry int
		for i := range 4 {
			sessions, names := lookupTools(model, remote)
			Expect(names).To(Equal([]string{"echo"}))
			Expect(sessionNamed(sessions, "dead").Error).To(ContainSubstring("connection failed"))
			if i == 1 {
				afterRetry = deadCount()
			}
		}
		Expect(live.count("initialize")).To(Equal(1))
		Expect(live.count("tools/list")).To(Equal(1))
		// The first lookup connects and the second retries at once; after
		// that the server waits for its backoff instead of costing every turn.
		Expect(afterRetry).To(BeNumerically(">", 0))
		Expect(deadCount()).To(Equal(afterRetry))
	})
})
