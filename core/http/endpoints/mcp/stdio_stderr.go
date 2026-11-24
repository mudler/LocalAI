package mcp

import (
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/stderrtail"
)

// stdioTailKeep is how long the output of an ended stdio server stays
// available for the error of a call made on its dead session.
const stdioTailKeep = 10 * time.Minute

type stdioTail struct {
	buf *stderrtail.Buffer
	env map[string]string // configured values, removed from the output
}

// stdioTails maps a live stdio session to the output of its command.
var stdioTails sync.Map // *mcp.ClientSession -> *stdioTail

// newStdioCommand builds the command of a stdio server. Its error stream goes
// to a bounded buffer, so a server that fails can be explained.
func newStdioCommand(server config.MCPSTDIOServer) (*exec.Cmd, *stderrtail.Buffer) {
	command := exec.Command(server.Command, server.Args...) // #nosec G204 -- the command is the administrator's own MCP server configuration
	command.Env = os.Environ()                              //nolint:forbidigo // the administrator's own MCP command inherits the server environment
	for key, value := range server.Env {
		command.Env = append(command.Env, key+"="+value)
	}
	tail := stderrtail.New(stderrtail.DefaultSize)
	command.Stderr = tail
	return command, tail
}

// stdioFailureOutput is the output of a command that failed, without the
// environment values that the administrator configured for it.
func stdioFailureOutput(tail *stderrtail.Buffer, server config.MCPSTDIOServer) string {
	tail.Settle(250 * time.Millisecond)
	return stderrtail.RedactValues(tail.Text(), server.Env)
}

// rememberTail links a connected session to the output of its command, for
// the error of a later call. The link ends some time after the session does.
func rememberTail(cs *mcp.ClientSession, tail *stderrtail.Buffer, server config.MCPSTDIOServer) {
	stdioTails.Store(cs, &stdioTail{buf: tail, env: server.Env})
	go func() {
		_ = cs.Wait()
		time.AfterFunc(stdioTailKeep, func() { stdioTails.Delete(cs) })
	}()
}

// tailOutput is the recent output of the command behind cs, or "" for a
// session that is not a stdio one.
func tailOutput(cs *mcp.ClientSession) string {
	v, ok := stdioTails.Load(cs)
	if !ok {
		return ""
	}
	t := v.(*stdioTail)
	t.buf.Settle(250 * time.Millisecond)
	return stderrtail.RedactValues(t.buf.Text(), t.env)
}
