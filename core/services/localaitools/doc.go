// Package localaitools exposes LocalAI's admin/management surface as
// a Model Context Protocol server. The same package is used in two ways:
//
//   - In-process, by the chat handler, when an admin opts the chat session
//     into the "LocalAI Assistant" modality. The MCP server is wired to the
//     chat session over a paired in-memory transport (net.Pipe()), and the
//     LocalAIClient is implemented by the inproc subpackage, which calls
//     LocalAI services directly.
//
//   - Out of process, by the standalone "local-ai mcp-server" subcommand,
//     which speaks MCP over stdio and uses the httpapi subpackage to talk
//     to a remote LocalAI instance over HTTP.
//
// Tool handlers and the embedded skill prompts only see the LocalAIClient
// interface and are agnostic to the underlying transport or implementation.
package localaitools
