import test from 'node:test'
import assert from 'node:assert/strict'
import {
  BROWSER_KEY, browserImportBody, browserImportName, canSaveMCP, connectorRef, emptyMCP, forgetBrowserServers, joinAgentMCP,
  mcpPayload, mcpTarget, readBrowserServers, splitAgentMCP, toggleOff, toolsCounts,
  CHECKS_KEY, checkRecord, connectorSig, connectorState, currentCheck, dropCheck, filterConnectors, filterTools, formFingerprint,
  formOf, isAuthError, mcpAddress, nameProblem, readChecks, sentence, urlProblem, writeChecks,
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
  // A group of built-in tools (web) is one switch like an add-on.
  assert.deepEqual(toolsCounts({ ...panel, builtins: [{ group: 'web', off: false }] }), { on: 4, total: 7, mcp: 4 })
  assert.deepEqual(toolsCounts({ builtins: [{ group: 'web', off: true }] }), { on: 0, total: 1, mcp: 0 })
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

test('chat MCP: an address is checked before it is sent, and a name must be new', () => {
  assert.equal(urlProblem(''), 'urlRequired')
  assert.equal(urlProblem('not a url'), 'urlInvalid')
  assert.equal(urlProblem('ftp://x.example'), 'urlScheme')
  assert.equal(urlProblem('https://u:p@x.example/mcp'), 'urlCredentials')
  assert.equal(urlProblem(' https://x.example/mcp '), '')
  assert.equal(nameProblem(' '), 'nameRequired')
  assert.equal(nameProblem('Docs ', ['docs']), 'nameTaken')
  assert.equal(nameProblem('Docs', ['Wiki']), '')
  assert.equal(canSaveMCP({ ...emptyMCP(), name: 'a', url: 'ftp://x' }), false)
  assert.equal(canSaveMCP({ ...emptyMCP(), name: 'Docs', url: 'https://x.example' }, ['docs']), false)
})

test('chat MCP: the details show the address without its query, and a command with its arguments', () => {
  assert.equal(mcpAddress({ transport: 'http', url: 'https://mcp.example/a/b?key=secret' }), 'https://mcp.example/a/b?…')
  assert.equal(mcpAddress({ transport: 'http', url: 'https://mcp.example/' }), 'https://mcp.example')
  assert.equal(mcpAddress({ transport: 'stdio', command: 'npx', args: ['-y', 'server'] }), 'npx -y server')
})

test('chat MCP: a test result becomes a state, and a 401 asks for sign-in', () => {
  const c = { id: 'c1', transport: 'http', url: 'https://x.example', has_token: true, enabled: true }
  assert.equal(connectorState({ ...c, enabled: false }, null), 'off')
  assert.equal(connectorState(c, null), 'unchecked')
  const ok = checkRecord(c, { ok: true, tools: ['a'], tool_details: [{ name: 'a', description: 'does a' }] }, 5)
  assert.deepEqual(ok.tools, [{ name: 'a', description: 'does a' }])
  assert.equal(ok.at, 5)
  assert.equal(connectorState(c, ok), 'ok')
  // An older server sends names only.
  assert.deepEqual(checkRecord(c, { ok: true, tools: ['b'] }).tools, [{ name: 'b', description: '' }])
  assert.equal(connectorState(c, checkRecord(c, { ok: false, error: 'the server answered 503' })), 'error')
  assert.equal(connectorState(c, checkRecord(c, { ok: false, error: 'the server answered 401 Unauthorized' })), 'auth')
  assert.equal(isAuthError('invalid_token'), true)
  assert.equal(isAuthError('connection refused'), false)
  assert.equal(sentence('the server answered 503'), 'The server answered 503.')
  assert.equal(sentence('Done!'), 'Done!')
})

test('chat MCP: a kept test holds until the address or the secrets change, and never stores the address', () => {
  const c = { id: 'c1', transport: 'http', url: 'https://x.example/mcp?key=SPEC-secret', has_token: true, enabled: true }
  const checks = { c1: checkRecord(c, { ok: true, tools: [] }) }
  assert.equal(JSON.stringify(checks).includes('SPEC-secret'), false)
  assert.ok(currentCheck({ ...c, enabled: false, updated: 'later' }, checks), 'switching does not make it stale')
  assert.equal(currentCheck({ ...c, url: 'https://y.example/mcp' }, checks), null)
  assert.equal(currentCheck({ ...c, has_token: false }, checks), null)
  assert.notEqual(connectorSig(c), connectorSig({ ...c, has_headers: true }))
  assert.deepEqual(dropCheck(checks, 'c1'), {})
  const data = new Map()
  const st = { getItem: k => data.get(k) ?? null, setItem: (k, v) => data.set(k, v) }
  writeChecks(st, checks)
  assert.deepEqual(readChecks(st), checks)
  data.set(CHECKS_KEY, 'not json')
  assert.deepEqual(readChecks(st), {})
})

test('chat MCP: search and the state filter narrow the list and count each filter', () => {
  const mk = (id, name, enabled, url = `https://${id}.example`) => ({ id, name, enabled, transport: 'http', url })
  const items = [mk('a', 'Docs', true), mk('b', 'Weather', false), mk('c', 'Calendar', true), mk('d', 'Wiki', true)]
  const checks = { c: checkRecord(items[2], { ok: false, error: 'the server answered 401' }), d: checkRecord(items[3], { ok: true, tools: [] }) }
  const all = filterConnectors(items, { checks })
  assert.deepEqual(all.counts, { all: 4, on: 3, attention: 1, off: 1 })
  assert.deepEqual(filterConnectors(items, { filter: 'attention', checks }).rows.map(x => x.id), ['c'])
  assert.deepEqual(filterConnectors(items, { filter: 'off', checks }).rows.map(x => x.id), ['b'])
  assert.deepEqual(filterConnectors(items, { query: 'd.example', checks }).rows.map(x => x.id), ['d'])
  assert.equal(filterConnectors(items, { query: 'nothing' }).counts.all, 0)
  assert.equal(all.states.get('a'), 'unchecked')
  const tools = [{ name: 'search', description: 'Find pages' }, { name: 'read', description: 'Reads a calendar' }]
  assert.deepEqual(filterTools(tools, 'calendar').map(x => x.name), ['read'])
  assert.equal(filterTools(tools, '').length, 2)
})

test('chat MCP: the edit form starts with empty secrets, and only a real change makes it dirty', () => {
  const f = formOf({ id: 'c1', name: 'Docs', transport: 'http', url: 'https://x.example', args: ['a'], has_token: true })
  assert.equal(f.token, '')
  assert.deepEqual(f.headers, [])
  assert.equal(f.argsText, 'a')
  const start = formFingerprint(f)
  assert.equal(formFingerprint({ ...f, headers: [{ name: '', value: '' }] }), start, 'an empty row is no change')
  assert.notEqual(formFingerprint({ ...f, name: 'Other' }), start)
  assert.notEqual(formFingerprint({ ...f, token: 'x' }), start)
})
