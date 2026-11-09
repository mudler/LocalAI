import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { CONNECTION_TABS, MCP_PATH, forTab, isAccountKind, tabOfKind, tabOfKindParam, tabOfSearch, visibleConnectionTabs } from '../../src/utils/connectionsTabs.js'
import { groupConnectors } from '../../src/utils/chatConnectors.js'

const KINDS = [
  { addon: 'gmail', kind: 'gmail', title: 'Gmail', agent_optional: true, auth_kind: 'oauth2' },
  { addon: 'github', kind: 'github', title: 'GitHub', agent_optional: true, tool_only: true },
  { addon: 'telegram', kind: 'telegram', title: 'Telegram' },
  { addon: 'slack', kind: 'slack', title: 'Slack' },
]

test('the tabs: accounts and messaging are views of the connectors page, MCP servers is its own route', () => {
  assert.deepEqual(CONNECTION_TABS.map((t) => t.id), ['accounts', 'messaging', 'mcp'])
  assert.deepEqual(CONNECTION_TABS.map((t) => t.to), ['/app/chats/connectors', '/app/chats/connectors?tab=messaging', '/app/mcp-connectors'])
  assert.equal(MCP_PATH, '/app/mcp-connectors')
})

test('a viewer sees the tabs their features allow', () => {
  const ids = (has) => visibleConnectionTabs(has).map((t) => t.id)
  assert.deepEqual(ids(() => true), ['accounts', 'messaging', 'mcp'])
  assert.deepEqual(ids((f) => f === 'chats'), ['mcp'])
  assert.deepEqual(ids((f) => f === 'connectors'), ['accounts', 'messaging'])
})

test('the tab comes from ?tab=: accounts is the default, an unknown tab is accounts', () => {
  assert.equal(tabOfSearch(''), 'accounts')
  assert.equal(tabOfSearch('?tab=messaging'), 'messaging')
  assert.equal(tabOfSearch('tab=messaging&x=1'), 'messaging')
  assert.equal(tabOfSearch('?tab=nope'), 'accounts')
})

test('a kind that needs an agent is messaging; one that holds an account only is accounts', () => {
  assert.deepEqual(KINDS.map(isAccountKind), [true, true, false, false])
  assert.deepEqual(KINDS.map(tabOfKind), ['accounts', 'accounts', 'messaging', 'messaging'])
})

test('forTab splits the groups by tab and puts the waiting bindings on messaging', () => {
  const { groups, waiting } = groupConnectors(KINDS, [{ id: 'w', kind: 'discord', addon: 'discord' }])
  const a = forTab('accounts', groups, waiting, KINDS)
  const m = forTab('messaging', groups, waiting, KINDS)
  assert.deepEqual(a.groups.map((g) => g.kind), ['gmail', 'github'])
  assert.deepEqual(m.groups.map((g) => g.kind), ['telegram', 'slack'])
  assert.deepEqual(a.waiting, [])
  assert.deepEqual(m.waiting.map((c) => c.id), ['w'])
})

test('an older ?kind=<kind> link opens the tab of that kind; an unknown kind opens none', () => {
  assert.equal(tabOfKindParam('?kind=telegram', KINDS), 'messaging')
  assert.equal(tabOfKindParam('?kind=gmail', KINDS), 'accounts')
  assert.equal(tabOfKindParam('?kind=nope', KINDS), '')
  assert.equal(tabOfKindParam('', KINDS), '')
  assert.equal(tabOfKindParam('?kind=telegram', null), '')
})

test('both pages draw the tab strip, and the cross link between them is gone', () => {
  const src = (f) => readFileSync(new URL(`../../src/pages/${f}`, import.meta.url), 'utf8')
  assert.match(src('ChatConnectors.jsx'), /<ConnectionsTabs current=\{tab\}/)
  assert.match(src('ChatMCPConnectors.jsx'), /<ConnectionsTabs current="mcp"/)
  assert.doesNotMatch(src('ChatMCPConnectors.jsx'), /mcpPage\.family/)
  assert.doesNotMatch(src('ChatConnectors.jsx'), /cn-related/)
})
