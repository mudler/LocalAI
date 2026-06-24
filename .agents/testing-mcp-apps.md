# Testing MCP Apps (Interactive Tool UIs)

MCP Apps is an extension to MCP where tools declare interactive HTML UIs via `_meta.ui.resourceUri`. When the LLM calls such a tool, the UI renders the app in a sandboxed iframe inline in the chat. The app communicates bidirectionally with the host via `postMessage` (JSON-RPC) and can call server tools, send messages, and update model context.

Spec: https://modelcontextprotocol.io/extensions/apps/overview

## Quick Start: Run a Test MCP App Server

The `@modelcontextprotocol/server-basic-react` npm package is a ready-to-use test server that exposes a `get-time` tool with an interactive React clock UI. It requires Node >= 20, so run it in Docker:

```bash
docker run -d --name mcp-app-test -p 3001:3001 node:22-slim \
  sh -c 'npx -y @modelcontextprotocol/server-basic-react'
```

Wait ~10 seconds for it to start, then verify:

```bash
# Check it's running
docker logs mcp-app-test
# Expected: "MCP server listening on http://localhost:3001/mcp"

# Verify MCP protocol works
curl -s -X POST http://localhost:3001/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}'

# List tools — should show get-time with _meta.ui.resourceUri
curl -s -X POST http://localhost:3001/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

The `tools/list` response should contain:
```json
{
  "name": "get-time",
  "_meta": {
    "ui": { "resourceUri": "ui://get-time/mcp-app.html" }
  }
}
```

## Testing in LocalAI's UI

1. Make sure LocalAI is running (e.g. `http://localhost:8080`)
2. Build the React UI: `cd core/http/react-ui && npm install && npm run build`
3. Open the Chat page in your browser
4. Click **"Client MCP"** in the chat header
5. Add a new client MCP server:
   - **URL**: `http://localhost:3001/mcp`
   - **Use CORS proxy**: enabled (default) — required because the browser can't hit `localhost:3001` directly due to CORS; LocalAI's proxy at `/api/cors-proxy` handles it
6. The server should connect and discover the `get-time` tool
7. Select a model and send: **"What time is it?"**
8. The LLM should call the `get-time` tool
9. The tool result should render the interactive React clock app in an iframe as a standalone chat message (not inside the collapsed activity group)

## What to Verify

- [ ] Tool appears in the connected tools list (not filtered — `get-time` is callable by the LLM)
- [ ] The iframe renders as a standalone chat message with a puzzle-piece icon
- [ ] The app loads and is interactive (clock UI, buttons work)
- [ ] No "Reconnect to MCP server" overlay (connection is live)
- [ ] Console logs show bidirectional communication:
  - `tools/call` messages from app to host (app calling server tools)
  - `ui/message` notifications (app sending messages)
- [ ] After the app renders, the LLM continues and produces a text response with the time
- [ ] Non-UI tools continue to work normally (text-only results)
- [ ] Page reload shows the HTML statically with a reconnect overlay until you reconnect

## Console Log Patterns

Healthy bidirectional communication looks like:

```
Parsed message { jsonrpc: "2.0", id: N, result: {...} }     // Bridge init
get-time result: { content: [...] }                          // Tool result received
Calling get-time tool...                                     // App calls tool
Sending message { method: "tools/call", ... }                // App -> host -> server
Parsed message { jsonrpc: "2.0", id: N, result: {...} }     // Server response
Sending message text to Host: ...                            // App sends message
Sending message { method: "ui/message", ... }                // Message notification
Message accepted                                             // Host acknowledged
```

Benign warnings to ignore:
- `Source map error: ... about:srcdoc` — browser devtools can't find source maps for srcdoc iframes
- `Ignoring message from unknown source` — duplicate postMessage from iframe navigation
- `notifications/cancelled` — app cleaning up previous requests

## Architecture Notes

- **No server-side changes needed** — the MCP App protocol runs entirely in the browser
- `PostMessageTransport` wraps `window.postMessage` between host and `srcdoc` iframe
- `AppBridge` (from `@modelcontextprotocol/ext-apps`) auto-forwards `tools/call`, `resources/read`, `resources/list` from the app to the MCP server via the host's `Client`
- The iframe uses `sandbox="allow-scripts allow-forms"` (no `allow-same-origin`) — opaque origin, no access to host cookies/DOM/localStorage
- App-only tools (`_meta.ui.visibility: "app-only"`) are filtered from the LLM's tool list but remain callable by the app iframe

## Key Files

- `core/http/react-ui/src/components/MCPAppFrame.jsx` — iframe + AppBridge component
- `core/http/react-ui/src/hooks/useMCPClient.js` — MCP client hook with app UI helpers (`hasAppUI`, `getAppResource`, `getClientForTool`, `getToolDefinition`)
- `core/http/react-ui/src/hooks/useChat.js` — agentic loop, attaches `appUI` to tool_result messages
- `core/http/react-ui/src/pages/Chat.jsx` — renders MCPAppFrame as standalone chat messages

## Other Test Servers

The `@modelcontextprotocol/ext-apps` repo has many example servers:
- `@modelcontextprotocol/server-basic-react` — simple clock (React)
- More examples at https://github.com/modelcontextprotocol/ext-apps/tree/main/examples

All examples support both stdio and HTTP transport. Run without `--stdio` for HTTP mode on port 3001.

## Cleanup

```bash
docker rm -f mcp-app-test
```
