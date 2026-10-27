// A small MCP server over HTTP for the specs that need a real connector: an
// echo tool, and a clock tool that declares an MCP App page. It listens on a
// free loopback port, one stateless transport per request.
import http from 'node:http'
import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { StreamableHTTPServerTransport } from '@modelcontextprotocol/sdk/server/streamableHttp.js'
import { z } from 'zod'

export const APP_URI = 'ui://clock/app.html'
export const APP_HTML = '<!doctype html><html><body><p id="out">clock app ready</p><script>' +
  'parent.postMessage({jsonrpc:"2.0",id:1,method:"ui/initialize",params:{appInfo:{name:"clock",version:"1"},appCapabilities:{},protocolVersion:"2025-06-18"}},"*")' +
  '</script></body></html>'

function build() {
  const server = new McpServer({ name: 'spec-mcp', version: '1.0.0' })
  server.registerTool('echo', { description: 'Echo the text back', inputSchema: { text: z.string() } },
    async ({ text }) => ({ content: [{ type: 'text', text: `echo: ${text}` }] }))
  server.registerTool('get-time', {
    description: 'The time, with a clock page',
    inputSchema: {},
    _meta: { ui: { resourceUri: APP_URI } },
  }, async () => ({ content: [{ type: 'text', text: 'It is 12:00.' }], structuredContent: { time: '12:00' } }))
  server.registerResource('clock', APP_URI, { mimeType: 'text/html;profile=mcp-app' },
    async () => ({ contents: [{ uri: APP_URI, mimeType: 'text/html;profile=mcp-app', text: APP_HTML }] }))
  return server
}

export async function startMcpFixture() {
  const srv = http.createServer(async (req, res) => {
    if (req.method !== 'POST') { res.writeHead(405).end(); return }
    const chunks = []
    for await (const c of req) chunks.push(c)
    let body
    try { body = JSON.parse(Buffer.concat(chunks).toString() || '{}') } catch { res.writeHead(400).end(); return }
    const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined, enableJsonResponse: true })
    const server = build()
    res.on('close', () => { transport.close(); server.close() })
    await server.connect(transport)
    await transport.handleRequest(req, res, body)
  })
  await new Promise(r => srv.listen(0, '127.0.0.1', r))
  const url = `http://127.0.0.1:${srv.address().port}/mcp`
  return { url, close: () => new Promise(r => srv.close(r)) }
}
