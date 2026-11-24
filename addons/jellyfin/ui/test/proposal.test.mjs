import test from 'node:test'
import assert from 'node:assert/strict'
import { clock, connectionsFor, proposalView } from '../src/proposal.js'
import { finishTest, forget, resultOf, startTest, subscribe } from '../src/results.js'
import en from '../src/i18n/jellyfin.en.json' with { type: 'json' }
import manifest from '../../manifest.json' with { type: 'json' }

test('jellyfin proposal: a player command shows the device, the user and what plays', () => {
  const v = proposalView('control-playback', { server: 'http://m.example', playerId: 's1', command: 'stop' },
    { user: 'Alice', device: 'Living room', client: 'Android TV', nowPlaying: 'Dune', positionSeconds: 3725 })
  assert.equal(v.command, 'stop')
  assert.equal(v.device, 'Living room')
  assert.equal(v.user, 'Alice')
  assert.equal(v.nowPlaying, 'Dune')
  assert.equal(clock(v.positionSeconds), '1:02:05')
})

test('jellyfin proposal: an unknown command is not shown as one, and numbers must be numbers', () => {
  assert.equal(proposalView('control-playback', { command: 'rm -rf' }, {}).command, '')
  const v = proposalView('control-playback', { command: 'set-volume', volume: '50' }, { volume: 40 })
  assert.equal(v.volume, 40)
  assert.equal(proposalView('control-playback', { command: 'seek', positionSeconds: 90 }, {}).toSeconds, 90)
  assert.equal(clock(65), '1:05')
  assert.equal(clock(-3), '0:00')
})

test('jellyfin proposal: watched marks, favourites and playlists read from the arguments first', () => {
  assert.equal(proposalView('mark-played', { played: false }, { played: true, item: 'Dune' }).played, false)
  assert.equal(proposalView('set-favorite', {}, { favorite: true }).favorite, true)
  const v = proposalView('add-to-playlist', { itemIds: ['a', 'b', 'c'] }, { playlist: 'Friday', items: ['A', 'B', 3] })
  assert.deepEqual(v.items, ['A', 'B'])
  assert.equal(v.itemCount, 3)
})

test('jellyfin proposal: the connection is found by the list, in order, any case', () => {
  const list = [{ kind: 'jellyfin', name: 'Home', resources: ['HTTP://m.example'] }, { kind: 'github', name: 'Bot', resources: ['http://m.example'] }, { kind: 'jellyfin', name: 'Other', resources: ['http://m.example'] }]
  assert.deepEqual(connectionsFor(list, 'http://m.example'), ['Home', 'Other'])
  assert.deepEqual(connectionsFor(list, 'http://x'), [])
  assert.deepEqual(connectionsFor(null, 'http://x'), [])
})

test('jellyfin proposal: every write tool has a card and words', () => {
  const ui = manifest.capabilities.ui
  const writes = manifest.capabilities.toolDefinitions.filter((d) => d.kind === 'write').map((d) => d.name)
  assert.equal(writes.length, 4)
  for (const tool of writes) {
    assert.ok(ui.renderers.some((r) => r.kind === 'approval-card' && r.tool === tool && r.component === 'ProposalCard'), tool)
    if (tool !== 'control-playback') assert.ok(en.approval.action[tool], tool)
  }
  for (const c of ['play', 'pause', 'resume', 'stop', 'seek', 'set-volume']) assert.ok(en.approval.command[c], c)
  assert.match(en.approval.others, /interrupts/)
})

test('jellyfin test results: a new run clears the old one and the row hears about it', () => {
  let calls = 0
  const off = subscribe(() => { calls++ })
  finishTest('b1', [{ server: 'x', tone: 'ok', text: 'x' }], 5)
  assert.equal(resultOf('b1').busy, false)
  startTest('b1')
  assert.deepEqual(resultOf('b1'), { busy: true, items: [], at: 0 })
  forget('b1')
  assert.equal(resultOf('b1'), null)
  assert.equal(calls, 3)
  off()
})
