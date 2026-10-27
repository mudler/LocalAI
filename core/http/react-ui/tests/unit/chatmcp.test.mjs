import test from 'node:test'
import assert from 'node:assert/strict'
import {
  BROWSER_KEY, browserImportBody, browserImportName, canSaveMCP, connectorRef, emptyMCP, forgetBrowserServers, joinAgentMCP,
  mcpPayload, mcpTarget, readBrowserServers, splitAgentMCP, toggleOff, toolsCounts,
} from '../../src/utils/chatMCP.js'

function storage(initial) {
  const data = new Map(initial === undefined ? [] : [[BROWSER_KEY, JSON.stringify(initial)]])
  return { getItem: (k) => (data.has(k) ? data.get(k) : null), setItem: (k, v) => data.set(k, String(v)), removeItem: (k) => data.delete(k), data }
}

test('chat MCP: secrets go only when they were typed, and can be cleared on purpose', () => {
  const create = mcpPayload({ ...emptyMCP(), name: ' Docs ', url: ' https://mcp.example/x ', token: ' tok ', headers: [{ name: 'X-Team', value: 'blue' }, { name: '', value: 'x' }] })
  assert.equal(create.name, 'Docs')
  assert.equal(create.url, 'https://mcp.example/x')
  assert.equal(create.token, 'tok')
  assert.deepEqual(create.headers, { 'X-Team': 'blue' })
  assert.equal(create.transport, 'http')
  const update = mcpPayload({ ...emptyMCP(), id: 'c1', name: 'Docs', url: 'https://mcp.example/x', has_token: true, has_headers: true })
  assert.equal('token' in update, false)
  assert.equal('headers' in update, false)
  const clear = mcpPayload({ ...emptyMCP(), id: 'c1', name: 'Docs', url: 'https://mcp.example/x', clearToken: true, clearHeaders: true })
  assert.equal(clear.token, '')
  assert.deepEqual(clear.headers, {})
})

test('chat MCP: a stdio connector sends a command, its arguments and its environment', () => {
  const body = mcpPayload({ ...emptyMCP(), transport: 'stdio', name: 'Files', command: ' npx ', argsText: '-y\n  @scope/server \n', env: [{ name: 'KEY', value: 'v' }] })
  assert.equal(body.command, 'npx')
  assert.deepEqual(body.args, ['-y', '@scope/server'])
  assert.deepEqual(body.env, { KEY: 'v' })
  assert.equal('url' in body, false)
})

test('chat MCP: what the form needs before it can be saved', () => {
  assert.equal(canSaveMCP({ ...emptyMCP(), name: 'a', url: 'https://x.example' }), true)
  assert.equal(canSaveMCP({ ...emptyMCP(), name: '', url: 'https://x.example' }), false)
  assert.equal(canSaveMCP({ ...emptyMCP(), name: 'a', url: ' ' }), false)
  assert.equal(canSaveMCP({ ...emptyMCP(), transport: 'stdio', name: 'a', command: '' }), false)
  assert.equal(canSaveMCP({ ...emptyMCP(), transport: 'stdio', name: 'a', command: 'x' }), true)
})

test('chat MCP: the list shows where a connector points, never the query of the address or a value', () => {
  assert.equal(mcpTarget({ transport: 'http', url: 'https://mcp.example/path?key=abc' }), 'mcp.example')
  assert.equal(mcpTarget({ transport: 'http', url: 'not a url' }), 'not a url')
  assert.equal(mcpTarget({ transport: 'stdio', command: '/usr/bin/npx', args: ['-y', 'x'] }), 'npx')
})

test('chat MCP: a saved agent names connectors with a prefix and model servers without', () => {
  assert.equal(connectorRef('c1'), 'connector:c1')
  assert.deepEqual(splitAgentMCP(['crm', 'connector:c1', 'mail', 'connector:c2']), { servers: ['crm', 'mail'], connectors: ['c1', 'c2'] })
  assert.deepEqual(joinAgentMCP(['crm'], ['c1']), ['crm', 'connector:c1'])
  assert.deepEqual(splitAgentMCP(undefined), { servers: [], connectors: [] })
})

test('chat MCP: switching one off keeps the others and never repeats', () => {
  assert.deepEqual(toggleOff(['a'], 'b', false), ['a', 'b'])
  assert.deepEqual(toggleOff(['a', 'b'], 'a', true), ['b'])
  assert.deepEqual(toggleOff(['a'], 'a', false), ['a'])
})

test('chat MCP: the tools panel counts what is on among what can be used', () => {
  const panel = {
    addons: [{ id: 'o', off: false }, { id: 's', off: true }],
    servers: [{ name: 'crm', off: false, usable: true }, { name: 'manual', off: false, usable: false }],
    connectors: [{ id: 'c1', off: false, usable: true }, { id: 'c2', off: true, usable: true }],
  }
  assert.deepEqual(toolsCounts(panel), { on: 3, total: 6, mcp: 4 })
  assert.deepEqual(toolsCounts({ addons: [{ id: 'o', off: false }] }), { on: 1, total: 1, mcp: 0 })
})

test('browser import: reads the servers the old chat page kept, drops what is not valid and never exposes a value', () => {
  const s = storage([
    { id: 'x1', name: 'Docs', url: 'https://mcp.example/mcp', headers: { Authorization: 'Bearer SECRET-1' }, useProxy: true },
    { id: 'x2', name: '', url: 'http://10.0.0.2:9000/sse', headers: {} },
    { id: 'x3', name: 'Bad', url: 'ftp://x.example' },
    { id: 'x4', name: 'Evil', url: 'https://e.example', headers: { 'X-A': 'v\r\nX-B: 1' } },
    null,
  ])
  const list = readBrowserServers(s)
  assert.deepEqual(list.map(x => x.id), ['x1', 'x2', 'x4'])
  assert.equal(list[2].headerCount, 0)
  assert.equal(list[0].headerCount, 1)
  assert.equal(JSON.stringify(list.map(({ id, name, url, headerCount }) => ({ id, name, url, headerCount }))).includes('SECRET'), false)
  assert.deepEqual(readBrowserServers(storage()), [])
  assert.deepEqual(readBrowserServers({ getItem: () => { throw new Error('blocked') } }), [])
  assert.deepEqual(readBrowserServers(storage('not a list')), [])
})

test('browser import: the request makes a switched-off connector with the headers, under a free name', () => {
  const body = browserImportBody({ id: 'x1', name: 'Docs', url: 'https://mcp.example/mcp', headers: { Authorization: 'Bearer SECRET-1' } }, ['Other'])
  assert.deepEqual(body, { name: 'Docs', transport: 'http', url: 'https://mcp.example/mcp', headers: { Authorization: 'Bearer SECRET-1' }, enabled: false })
  const none = browserImportBody({ id: 'x2', name: '', url: 'http://10.0.0.2:9000/sse', headers: {} }, [])
  assert.equal(none.name, '10.0.0.2')
  assert.equal('headers' in none, false)
  assert.equal(browserImportName({ name: 'Docs', url: 'https://a.example' }, ['Docs', 'Docs (2)']), 'Docs (3)')
})

test('browser import: only the servers that were created are removed from the browser', () => {
  const s = storage([{ id: 'x1', name: 'A', url: 'https://a.example' }, { id: 'x2', name: 'B', url: 'https://b.example' }])
  forgetBrowserServers(s, ['x1'])
  assert.deepEqual(JSON.parse(s.data.get(BROWSER_KEY)).map(x => x.id), ['x2'])
  forgetBrowserServers(s, ['x2'])
  assert.equal(s.data.has(BROWSER_KEY), false)
})
