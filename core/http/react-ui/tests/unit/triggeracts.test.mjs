import test from 'node:test'
import assert from 'node:assert/strict'
import {
  actCatalog, actNamed, actRows, actsAfter, actsStatus, actsStatusOf, isActGrant, withinAgent,
} from '../../src/utils/triggerActs.js'
import { emptyTrigger, triggerForm, triggerPayload } from '../../src/utils/chatTrigger.js'

const grants = [
  { name: 'memory.write', title: 'Save memory', description: 'd', sensitive: false },
  { name: 'act.files', title: 'Write files on its own', description: 'files', sensitive: true },
  { name: 'act.addons', title: 'Use add-on actions on its own', description: 'addons', sensitive: true },
  { name: 'act.send', title: 'Send messages on its own', description: 'send', sensitive: true },
  { name: 'act.spend', title: 'Use paid or external tools on its own', description: 'spend', sensitive: true },
]

test('trigger acts: the catalog keeps only the act names', () => {
  assert.deepEqual(actCatalog(grants).map((g) => g.name), ['act.files', 'act.addons', 'act.send', 'act.spend'])
  assert.deepEqual(actCatalog(undefined), [])
  assert.equal(isActGrant('act.files'), true)
  assert.equal(isActGrant('memory.write'), false)
})

test('trigger acts: an agent covers an act by name, or act.addons covers one add-on', () => {
  assert.equal(withinAgent('act.files', ['act.files']), true)
  assert.equal(withinAgent('act.spend', ['act.files']), false)
  assert.equal(withinAgent('act.addons:mail', ['act.addons']), true)
  assert.equal(withinAgent('act.addons:mail', ['act.files']), false)
  assert.equal(withinAgent('act.spend', null), true, 'no agent, no ceiling')
  assert.equal(withinAgent('act.spend', []), false, 'an agent with no grants covers nothing')
})

test('trigger acts: rows are on when held, blocked when the agent lacks them, and keep an unlisted act', () => {
  const rows = actRows(actCatalog(grants), ['act.files', 'act.addons:mail'], ['act.files'])
  const by = Object.fromEntries(rows.map((r) => [r.name, r]))
  assert.equal(by['act.files'].on, true)
  assert.equal(by['act.files'].blocked, false)
  assert.equal(by['act.spend'].on, false)
  assert.equal(by['act.spend'].blocked, true)
  assert.equal(by['act.files'].sensitive, true)
  assert.equal(by['act.addons:mail'].extra, true)
  assert.equal(by['act.addons:mail'].on, true)
  assert.equal(by['act.addons:mail'].blocked, true, 'the agent holds act.files only')
  assert.equal(rows.length, 5)
})

test('trigger acts: switching an act on or off', () => {
  assert.deepEqual(actsAfter(['act.files'], 'act.send', true), ['act.files', 'act.send'])
  assert.deepEqual(actsAfter(['act.files', 'act.send'], 'act.files', false), ['act.send'])
  assert.deepEqual(actsAfter(undefined, 'act.send', false), [])
})

test('trigger acts: a trigger with no acts is read only and says so', () => {
  const s = actsStatus({ acts: [], agentGrants: ['act.files'] })
  assert.deepEqual([s.readOnly, s.why], [true, 'no_acts'])
})

test('trigger acts: acts the agent does not hold do not apply, and the reason is the agent', () => {
  const s = actsStatus({ acts: ['act.spend'], agentGrants: ['act.files'] })
  assert.deepEqual([s.readOnly, s.why, s.held, s.unused], [true, 'agent', [], ['act.spend']])
  const mixed = actsStatus({ acts: ['act.spend', 'act.files'], agentGrants: ['act.files'] })
  assert.deepEqual([mixed.readOnly, mixed.held, mixed.unused], [false, ['act.files'], ['act.spend']])
})

test('trigger acts: a plan that names a file it writes holds act.files', () => {
  const s = actsStatus({ acts: [], agentGrants: ['act.files'], writes: 'data/out.json' })
  assert.deepEqual([s.readOnly, s.held], [false, ['act.files']])
  assert.equal(actsStatus({ acts: [], agentGrants: [], writes: 'data/out.json' }).readOnly, true, 'the agent must hold it too')
})

test('trigger acts: a lock from the way it was made wins over every act', () => {
  for (const lock of ['declared', 'binding']) {
    const s = actsStatus({ acts: ['act.files'], agentGrants: ['act.files'], lock })
    assert.deepEqual([s.readOnly, s.why, s.held], [true, lock, []])
  }
})

test('trigger acts: the status of a trigger as the server returns it', () => {
  const t = { acts: ['act.files', 'act.spend'], acts_held: ['act.files'], read_only: false }
  assert.deepEqual(actsStatusOf(t), { readOnly: false, why: '', held: ['act.files'], unused: ['act.spend'] })
  const ro = actsStatusOf({ acts: ['act.files'], read_only: true, read_only_why: 'binding' })
  assert.deepEqual([ro.readOnly, ro.why, ro.unused], [true, 'binding', ['act.files']])
  assert.equal(actsStatusOf(null).readOnly, false)
})

test('trigger acts: names get the title and weight of the catalog', () => {
  const rows = actNamed(grants, ['act.send', 'act.addons:mail'])
  assert.deepEqual(rows.map((r) => [r.name, r.title, r.sensitive]), [
    ['act.send', 'Send messages on its own', true],
    ['act.addons:mail', 'act.addons:mail', true],
  ])
})

test('trigger acts: the editor sends the acts it loaded, and an empty list when none', () => {
  const loaded = triggerForm({ id: 'x', name: 'n', prompt: 'p', enabled: true, kind: 'schedule', schedule: 'daily 08:00', acts: ['act.files'], read_only: false,
    plan: { writes: 'data/out.json' } })
  assert.deepEqual(loaded.acts, ['act.files'])
  assert.equal(loaded.writes, 'data/out.json')
  assert.deepEqual(triggerPayload(loaded).acts, ['act.files'])
  assert.deepEqual(triggerPayload(emptyTrigger()).acts, [], 'a new trigger sends an empty list: read only')
  const locked = triggerForm({ id: 'x', name: 'n', prompt: 'p', enabled: true, kind: 'schedule', schedule: 'daily 08:00', read_only: true, read_only_why: 'declared' })
  assert.equal(locked.lock, 'declared')
  assert.deepEqual(triggerPayload(locked).acts, [])
})
