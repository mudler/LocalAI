# Testing MCP Apps (Interactive Tool UIs)

MCP Apps is an extension to MCP where tools declare interactive HTML UIs via `_meta.ui.resourceUri`. When the model calls such a tool, the chat shows the app in a sandboxed iframe under the tool line. The app talks to the host with `postMessage` (JSON-RPC) and can call tools of its own server.

Spec: https://modelcontextprotocol.io/extensions/apps/overview

## How it works in the chats page

The MCP connection is on the **server**, not in the browser. There is no browser MCP client, no `AppBridge` and no CORS proxy in this path.

1. A turn calls a tool that has `_meta.ui.resourceUri`. The tool wrapper (`core/services/studio/mcpapps.go`, `withApp`) runs the tool, keeps the full MCP result, and sets a card on the call with `session.SetToolCard`. The card holds the app reference: server (`connector:<id>` or the model's server name), tool, resource URI, visibility, input and result.
2. The card is stored with the tool entry of the transcript. `GET /api/chats/{id}/messages` returns it as `app` (with `input` and `result`). The live `tool_end` event carries the reference only (the bus drops the payload) and `seq`, the number of that transcript entry. The entry is stored before the event is sent, in the shared store, so any frontend can serve it.
   The page shows the app live: on `tool_end` it reads that one entry with `GET /api/chats/{id}/messages?after=<seq-1>&limit=1` and renders the app in the tool card at once. The live card and the transcript line have the same key (`tool-<seq>`, `src/utils/chatLive.js`) in one flat list, so the frame is not remounted when the turn ends and the transcript is read again. If the entry cannot be read, the card shows the result as text and the transcript read at the turn end brings the app.
3. `ChatAppFrame.jsx` reads the page with `GET /api/chats/{id}/mcp-apps/resource?server=&uri=` and renders it in `<iframe sandbox="allow-scripts">` with a CSP meta tag put before the page. `src/utils/chatApps.js` (`createHost`) is the host side of the JSON-RPC bridge. A tool call of the page goes to `POST /api/chats/{id}/mcp-apps/call`.
4. The server checks on every request that the chat may use the server now (chat owner, not switched off in the tools panel, agent grant, connector owned and enabled), that the resource is declared by a tool of that server, and that the tool's `_meta.ui.visibility` includes `"app"`.

A tool whose visibility lacks `"model"` is not offered to the model.

## Tests

- Go, the rules of the routes: `core/services/studio/mcpapps_test.go` (a real MCP server over HTTP with a clock app, a connector and a model server) and `core/http/routes/studio_mcpapps_test.go` (status codes, auth, no secret in an answer).
- Go, the parts: `pkg/mcpapps`, `core/http/endpoints/mcp/apps_test.go`, `core/services/studio/mcpconns/apps_test.go`, `core/services/studio/session/cardapp_test.go`.
- UI units: `node tests/unit/run.mjs chatapps.test.mjs` (CSP, srcdoc, the host bridge, no DOM) and `chatlive.test.mjs` (live tool calls, stable keys, the merge of the live turn into the transcript).
- Playwright, API stubbed: `e2e/chats-apps.spec.js` (sandbox attribute, bridge round trip, isolation probes, suggestions and links, foreign `postMessage`, fallback, phone width) and, with the event stream faked, the live cases: the app shows when `tool_end` arrives and the frame is not reloaded when the turn ends or the stream reconnects, and a card that cannot be read falls back to text.

Playwright runs one process at a time, against the server on port 8089. Build the UI and the test server first:

```bash
cd core/http/react-ui && npx vite build
cd ../../.. && go build -tags auth -o tests/e2e-ui/ui-test-server ./tests/e2e-ui
cd core/http/react-ui && PLAYWRIGHT_CHROMIUM_PATH=/usr/bin/google-chrome PW_WORKERS=1 npx playwright test e2e/chats-apps.spec.js --project=chromium
```

## Try it by hand

The `@modelcontextprotocol/server-basic-react` package is a ready-made test server with a `get-time` tool and a React clock UI. It needs Node >= 20, so run it in Docker:

```bash
docker run -d --name mcp-app-test -p 3001:3001 node:22-slim \
  sh -c 'npx -y @modelcontextprotocol/server-basic-react'
curl -s -X POST http://localhost:3001/mcp \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
# get-time has "_meta": {"ui": {"resourceUri": "ui://get-time/mcp-app.html"}}
```

1. Run LocalAI with chats on and the `mcp` feature for your user. Connectors refuse private addresses by default; for a server on localhost an administrator sets `mcp_connectors_allow_private`.
2. Open **Chats**, **Connectors**, **MCP server**, and add `http://localhost:3001/mcp`. Use **Test connection**, then enable it.
3. Start a chat and send "What time is it?".
4. The clock app appears under the `get-time` tool line as soon as the tool call ends, before the answer is complete.

## What to verify

- [ ] The tool is offered to the model; a tool with `visibility: ["app"]` is not.
- [ ] The app shows under its tool line as soon as the call ends (during the turn), is not reloaded or duplicated when the turn ends, and shows again after a reload of the page.
- [ ] The iframe has `sandbox="allow-scripts"` and nothing else, and the page cannot read the host (no cookies, no storage, no parent DOM, no network).
- [ ] A click in the app that calls a tool reaches the server (`POST .../mcp-apps/call`) and the result comes back.
- [ ] `ui/message` shows a suggestion that you send or dismiss; `ui/open-link` shows an Open link that works after a click and refuses `javascript:` and `data:`.
- [ ] Switching the connector off in the tools panel makes the page and its calls answer `403`.
- [ ] A page that cannot load shows the result as text.
- [ ] Non-UI tools keep working with text results.

Benign console output: `Source map error: ... about:srcdoc` (devtools cannot find source maps for srcdoc iframes).

## Limits to know

- MCP servers of the model (not connectors) serve apps only when the frontend holds their sessions. In distributed mode they run on workers and the routes answer `409 mcp_app_unsupported`. Connectors work in both modes.
- Browser permissions an app asks for (camera, microphone) are not granted.
- Out of scope: OAuth, apps from add-on tools (add-ons have their own UI slots), apps in triggers or connectors (no UI there).

## Other test servers

The `@modelcontextprotocol/ext-apps` repo has many example servers: https://github.com/modelcontextprotocol/ext-apps/tree/main/examples . They run over HTTP on port 3001 without `--stdio`.

## Cleanup

```bash
docker rm -f mcp-app-test
```
