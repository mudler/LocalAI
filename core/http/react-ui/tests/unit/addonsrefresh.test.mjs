import test from 'node:test'
import assert from 'node:assert/strict'
import { CATEGORIES, GROUPS, categoryOf, groupOf, groupTiles } from '../../src/components/addons/addonState.js'
import { connectionFacts, connectionLine, rowState } from '../../src/utils/chatConnectors.js'

// The refreshed Add-ons page: icons for add-ons the host has no file for,
// the groups of the installed list, the categories of the library and the
// words of a connection row.

test('categories: a connector kind makes a chat app, whatever the id', () => {
  assert.equal(categoryOf({ id: 'wave', toolCount: 3 }, { connector: true }), 'chat')
  assert.equal(categoryOf({ id: 'slack' }), 'tools', 'an id alone says nothing about the category')
})

test('categories: tools make a tool, a page alone a models page, nothing at all a tool', () => {
  assert.equal(categoryOf({ id: 'acme', toolCount: 2, tools: ['a', 'b'] }), 'tools')
  assert.equal(categoryOf({ id: 'acme', hasUI: true }), 'models')
  assert.equal(categoryOf({ id: 'acme' }), 'tools')
  assert.equal(categoryOf({ id: 'home-assistant', toolCount: 8 }), 'tools')
})

test('categories: a library entry is read from its ui and tool hints', () => {
  assert.equal(categoryOf({ id: 'acme', ui: { mainPage: 'home' }, toolCount: 0 }), 'models')
  assert.equal(categoryOf({ id: 'acme', ui: { mainPage: 'home' }, toolCount: 2, tools: ['a'] }), 'tools')
  assert.ok(CATEGORIES.includes(categoryOf({ id: 'face' })))
  assert.deepEqual(CATEGORIES, ['chat', 'tools', 'models'])
})

const tile = (id, over = {}) => ({ id, source: 'app', app: { id, hasUI: false, toolCount: 0, tools: [] }, ...over })

test('groups: tiles split into chat apps, tools and services, media and models, then built in; empty groups are left out', () => {
  const tiles = [
    tile('github', { app: { id: 'github', toolCount: 9, tools: [] } }),
    tile('slack'),
    tile('voice', { app: { id: 'voice', hasUI: true } }),
    tile('jellyfin', { app: { id: 'jellyfin', toolCount: 4, tools: [] } }),
    { id: 'chats', source: 'builtin', app: null },
  ]
  const out = groupTiles(tiles, { connectorAddons: new Set(['slack']) })
  assert.deepEqual(out.map(g => g.id), ['chat', 'tools', 'models', 'builtin'])
  assert.deepEqual(out.map(g => g.tiles.map(t => t.id)), [['slack'], ['github', 'jellyfin'], ['voice'], ['chats']])
  assert.deepEqual(groupTiles([tile('github', { app: { id: 'github', toolCount: 1, tools: [] } })]).map(g => g.id), ['tools'])
  assert.deepEqual(groupTiles([]), [])
  assert.deepEqual(GROUPS, ['chat', 'tools', 'models', 'builtin'])
})

test('groups: an add-on that serves a connector kind is a chat app even without a known id', () => {
  assert.equal(groupOf(tile('wave'), { connectorAddons: new Set(['wave']) }), 'chat')
  assert.equal(groupOf(tile('wave')), 'tools')
})

const agents = [{ id: 'a1', name: 'Support' }]
const t = (key, opts = {}) => `${key}${opts.count !== undefined ? `:${opts.count}` : ''}`

test('connection rows: the facts are the agent name and who is accepted, never the raw kind', () => {
  const c = { id: 'c1', name: 'Support bot', kind: 'telegram', agent_id: 'a1', accept_any: false, allow: ['1', '2'] }
  assert.deepEqual(connectionFacts(c, agents), { agentName: 'Support', accepts: { key: 'listed', count: 2 } })
  assert.deepEqual(connectionLine(c, agents, t), { agentName: 'Support', who: 'connection.acceptsListed:2' })
  assert.equal(connectionLine({ ...c, accept_any: true }, agents, t).who, 'connection.accepts.anyone')
  assert.equal(connectionLine({ ...c, allow: [] }, agents, t).who, 'connection.accepts.nobody')
  assert.equal(connectionFacts({ ...c, agent_id: 'gone' }, agents).agentName, '')
})

test('connection rows: no token means the connection needs setup', () => {
  assert.equal(rowState({ enabled: true, has_token: false }), 'attention')
  assert.equal(rowState({ enabled: true, has_token: true }), 'running')
})
