package mcp

import (
	"context"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("connectMCP", func() {
	// A stdio server that starts but never completes the MCP initialize
	// handshake models the real-world hang from mudler/LocalAI#10880: an
	// unreachable/misbehaving MCP server would otherwise block the caller (and,
	// because the session-cache mutex is held across connection setup, every
	// other MCP request for the model) until the 360s httpClient timeout, which
	// surfaces in the UI as the server widget "spinning forever".
	It("returns promptly with an error when the handshake never completes", func() {
		// `sleep` stays alive but never reads its stdin nor emits an MCP
		// initialize response, so client.Connect blocks on the handshake.
		// (`cat` would echo the request back and the SDK would treat it as a
		// bogus response, returning immediately instead of hanging.)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel() // reap the abandoned goroutine/subprocess after the test

		transport := &mcp.CommandTransport{Command: exec.CommandContext(ctx, "sleep", "60")}

		start := time.Now()
		session, err := connectMCP(ctx, transport, 200*time.Millisecond)
		elapsed := time.Since(start)

		Expect(err).To(HaveOccurred())
		Expect(session).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("timed out"))
		// It must return around the timeout, not hang until the 360s httpClient
		// timeout. Allow generous slack for slow CI.
		Expect(elapsed).To(BeNumerically("<", 5*time.Second))
	})
})
