package mcp

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const helperSecret = "s3cr3t-token-value"

// The test binary acts as a stdio MCP server when MCP_STDERR_HELPER names a
// mode: fail-start, healthy or die-on-call.
func init() {
	mode := os.Getenv("MCP_STDERR_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "fail-start":
		_, _ = os.Stderr.WriteString("fatal: missing API_KEY, got " + os.Getenv("API_KEY") + "\n")
		os.Exit(1)
	case "healthy", "die-on-call":
		_, _ = os.Stderr.WriteString("note: server is starting\n")
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "helper", Version: "1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "echo", Description: "echoes"}, func(_ context.Context, _ *mcp.CallToolRequest, in struct {
		Text string `json:"text"`
	}) (*mcp.CallToolResult, any, error) {
		if mode == "die-on-call" {
			_, _ = os.Stderr.WriteString("panic: lost the database, key " + os.Getenv("API_KEY") + "\n")
			go func() { time.Sleep(50 * time.Millisecond); os.Exit(3) }()
			time.Sleep(time.Second)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + in.Text}}}, nil, nil
	})
	_ = srv.Run(context.Background(), &mcp.StdioTransport{})
	os.Exit(0)
}

func helperServer(mode string) config.MCPGenericConfig[config.MCPSTDIOServers] {
	self, err := os.Executable()
	Expect(err).NotTo(HaveOccurred())
	return config.MCPGenericConfig[config.MCPSTDIOServers]{Servers: config.MCPSTDIOServers{
		"helper": {Command: self, Args: []string{"-test.run=^$"}, Env: map[string]string{"MCP_STDERR_HELPER": mode, "API_KEY": helperSecret}},
	}}
}

// captureLog sends the log of the spec to a buffer until the spec ends. It
// keeps info and above: the debug line of the configuration is not under test.
func captureLog() *bytes.Buffer {
	var buf bytes.Buffer
	xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}), xlog.LogLevel("info")))
	DeferCleanup(func() { xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text")) })
	return &buf
}

var _ = Describe("stdio server output", func() {
	none := config.MCPGenericConfig[config.MCPRemoteServers]{}

	It("explains a named server that fails at startup, without the configured secret", func() {
		logs := captureLog()
		name := "stderr-named-fail"
		DeferCleanup(func() { CloseMCPSessions(name) })
		sessions, err := NamedSessionsFromMCPConfig(name, none, helperServer("fail-start"), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(sessions).To(HaveLen(1))
		msg := sessions[0].Error
		Expect(sessions[0].Session).To(BeNil())
		first, output, found := strings.Cut(msg, "\ncommand output:\n")
		Expect(found).To(BeTrue(), msg)
		Expect(first).To(HavePrefix("startup failed: "))
		Expect(first).To(ContainSubstring("EOF"))
		Expect(output).To(Equal("fatal: missing API_KEY, got ***"))
		Expect(logs.String()).To(ContainSubstring("fatal: missing API_KEY"))
		Expect(logs.String()).NotTo(ContainSubstring(helperSecret))
	})

	It("explains a failed start on the legacy path in the log, and still skips the server", func() {
		logs := captureLog()
		name := "stderr-legacy-fail"
		DeferCleanup(func() { CloseMCPSessions(name) })
		sessions, err := SessionsFromMCPConfig(name, none, helperServer("fail-start"))
		Expect(err).NotTo(HaveOccurred())
		Expect(sessions).To(BeEmpty())
		Expect(logs.String()).To(ContainSubstring("fatal: missing API_KEY, got ***"))
		Expect(logs.String()).NotTo(ContainSubstring(helperSecret))
	})

	It("adds the output to the error of a tool call that kills the server", func() {
		logs := captureLog()
		name := "stderr-named-call"
		DeferCleanup(func() { CloseMCPSessions(name) })
		sessions, err := NamedSessionsFromMCPConfig(name, none, helperServer("die-on-call"), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(sessions[0].Error).To(BeEmpty())
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		tools, err := DiscoverMCPTools(ctx, sessions)
		Expect(err).NotTo(HaveOccurred())
		_, err = ExecuteMCPToolCall(ctx, tools, "echo", `{"text":"hi"}`)
		Expect(err).To(HaveOccurred())
		first, output, found := strings.Cut(err.Error(), "\ncommand output:\n")
		Expect(found).To(BeTrue(), err.Error())
		Expect(first).To(HavePrefix(`MCP tool "echo" call failed`))
		Expect(output).To(ContainSubstring("panic: lost the database, key ***"))
		Expect(output).NotTo(ContainSubstring(helperSecret))
		Expect(logs.String()).To(ContainSubstring("lost the database"))
	})

	It("adds the output to the error of a tool call on the legacy path", func() {
		name := "stderr-legacy-call"
		DeferCleanup(func() { CloseMCPSessions(name) })
		sessions, err := SessionsFromMCPConfig(name, none, helperServer("die-on-call"))
		Expect(err).NotTo(HaveOccurred())
		Expect(sessions).To(HaveLen(1))
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		tools, err := DiscoverMCPTools(ctx, []NamedSession{{Name: "legacy", Type: "stdio", Session: sessions[0]}})
		Expect(err).NotTo(HaveOccurred())
		_, err = ExecuteMCPToolCall(ctx, tools, "echo", `{"text":"hi"}`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(And(ContainSubstring("\ncommand output:\n"), ContainSubstring("panic: lost the database, key ***")))
	})

	It("keeps a healthy server working although it writes to its error stream", func() {
		name := "stderr-healthy"
		DeferCleanup(func() { CloseMCPSessions(name) })
		sessions, err := NamedSessionsFromMCPConfig(name, none, helperServer("healthy"), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(sessions[0].Error).To(BeEmpty())
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		tools, err := DiscoverMCPTools(ctx, sessions)
		Expect(err).NotTo(HaveOccurred())
		res, err := ExecuteMCPToolCall(ctx, tools, "echo", `{"text":"hi"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(ContainSubstring("echo: hi"))
	})
})
