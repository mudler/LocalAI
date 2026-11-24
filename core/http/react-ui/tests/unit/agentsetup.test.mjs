import test from 'node:test'
import assert from 'node:assert/strict'
import { loadSetup, saveSetup, setupRows, setupProgress, setupVisible, wishOf, SETUP_PREFIX } from '../../src/utils/agentSetup.js'

const memory = () => {
  const m = new Map()
  return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, v), m }
}

const chat = { key: 'kind:chatty/chatty', type: 'kind', addon: 'chatty', kind: 'chatty', page: 'bots', title: 'Chatty', setup: 'A bot token.', credentialed: false }
const mail = { key: 'kind:mailer/mail', type: 'tool', addon: 'mailer', kind: 'mail', page: 'accounts', title: 'Mailer', setup: '', credentialed: true }
const notes = { key: 'tool:notes', type: 'tool', addon: 'notes', kind: '', page: '', title: 'Notes', setup: '', credentialed: false }
const wave = { key: 'catalog:wave', type: 'catalog', addon: 'wave', kind: '', page: '', title: 'Wave', setup: '', credentialed: false }
const kinds = [
  { addon: 'chatty', kind: 'chatty', title: 'Chatty', page: 'bots', setup: 'Paste a bot token.' },
  { addon: 'mailer', kind: 'mail', title: 'Mailer', agent_optional: true },
]
const agent = { id: 'a1', addons: [], connections: {} }
const server = (over = {}) => ({ agent, kinds, bindings: [], installed: ['chatty', 'mailer', 'notes'], ...over })

test('setup: the wish and the dismissal are kept per agent and survive a broken store', () => {
  const store = memory()
  assert.deepEqual(loadSetup('a1', store), { wanted: [], dismissed: false })
  assert.equal(saveSetup('a1', { wanted: [wishOf(chat)], dismissed: true }, store), true)
  assert.equal(store.m.has(`${SETUP_PREFIX}a1`), true)
  assert.deepEqual(loadSetup('a1', store).wanted.map((w) => w.key), [chat.key])
  assert.equal(loadSetup('a1', store).dismissed, true)
  assert.deepEqual(loadSetup('a2', store), { wanted: [], dismissed: false })
  const broken = { getItem: () => { throw new Error('blocked') }, setItem: () => { throw new Error('full') } }
  assert.deepEqual(loadSetup('a1', broken), { wanted: [], dismissed: false })
  assert.equal(saveSetup('a1', { wanted: [] }, broken), false)
  assert.deepEqual(loadSetup('a1', { getItem: () => '{not json' }), { wanted: [], dismissed: false })
})

test('setup: a chat app is done when the server has a connection of that kind for this agent', () => {
  const rows = (bindings) => setupRows([chat], server({ bindings }))
  assert.equal(rows([])[0].done, false)
  // Another agent, or another kind, does not count.
  assert.equal(rows([{ id: 'b1', kind: 'chatty', addon: 'chatty', agent_id: 'other' }])[0].done, false)
  assert.equal(rows([{ id: 'b1', kind: 'other', addon: 'other', agent_id: 'a1' }])[0].done, false)
  assert.equal(rows([{ id: 'b1', kind: 'chatty', addon: 'chatty', agent_id: 'a1' }])[0].done, true)
})

test('setup: the chat app button opens the page of the kind with the agent chosen', () => {
  const [row] = setupRows([chat], server())
  assert.equal(row.kind, 'chat')
  assert.equal(row.href, '/app/addons/chatty/bots?new=1&agent=a1')
  // The live kind wins over the remembered words.
  assert.equal(row.setup, 'Paste a bot token.')
})

test('setup: an add-on that needs an account is done with an account, one that does not with its switch', () => {
  assert.equal(setupRows([mail], server())[0].done, false)
  assert.equal(setupRows([mail], server())[0].kind, 'account')
  assert.equal(setupRows([mail], server())[0].href, '/app/agents/a1/edit#aed-sec-capabilities')
  assert.equal(setupRows([mail], server({ agent: { ...agent, connections: { mailer: ['m1'] } } }))[0].done, false)
  assert.equal(setupRows([mail], server({ agent: { ...agent, addons: ['mailer'], connections: { mailer: ['m1'] } } }))[0].done, true)
  assert.equal(setupRows([notes], server())[0].done, false)
  assert.equal(setupRows([notes], server({ agent: { ...agent, addons: ['notes'] } }))[0].done, true)
})

test('setup: an add-on that is not installed opens its catalog entry and is not done', () => {
  const [row] = setupRows([wave], server())
  assert.equal(row.kind, 'install')
  assert.equal(row.installed, false)
  assert.equal(row.done, false)
  assert.equal(row.href, '/app/addons?tab=browse&q=wave')
})

test('setup: once a wished add-on is installed it is asked about as what it is', () => {
  const live = [...kinds, { addon: 'wave', kind: 'wave', title: 'Wave', page: 'home' }]
  const [row] = setupRows([wave], server({ kinds: live }))
  assert.equal(row.kind, 'chat')
  assert.equal(row.href, '/app/addons/wave/home?new=1&agent=a1')
  const [tool] = setupRows([wave], server({ installed: ['wave'] }))
  assert.equal(tool.kind, 'switch')
})

test('setup: progress counts the done rows and the card goes when all are done or it is dismissed', () => {
  const wanted = [chat, notes]
  const rows = setupRows(wanted, server({ bindings: [{ id: 'b1', kind: 'chatty', addon: 'chatty', agent_id: 'a1' }] }))
  assert.deepEqual(setupProgress(rows), { done: 1, total: 2, complete: false })
  assert.equal(setupVisible({ dismissed: false }, rows), true)
  assert.equal(setupVisible({ dismissed: true }, rows), false)
  const all = setupRows(wanted, server({ bindings: [{ id: 'b1', kind: 'chatty', addon: 'chatty', agent_id: 'a1' }], agent: { ...agent, addons: ['notes'] } }))
  assert.equal(setupProgress(all).complete, true)
  assert.equal(setupVisible({ dismissed: false }, all), false)
  assert.equal(setupVisible({ dismissed: false }, []), false)
})
