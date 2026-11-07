import test from 'node:test'
import assert from 'node:assert/strict'
import {
  MAX_NAME, MAX_SETUPS, captureSetup, cleanName, isEdited, setupId, setupsData, toolsOn,
  withAdded, withDefault, withMoved, withRemoved, withReplaced,
} from '../../src/utils/setups.js'
import { applyChoices, choicesFromTools, draftSwitches, emptyChoices } from '../../src/utils/chatDraftSwitches.js'
import { WELL_KNOWN_FILES } from '../../src/utils/wellKnown.js'

const data = (...names) => names.reduce((d, n) => withAdded(d, { id: '', name: n }), { default: '', setups: [] })

test('setups path is well known', () => {
  assert.equal(WELL_KNOWN_FILES.setups, 'LocalAI/setups.json')
})

test('cleanName trims and caps', () => {
  assert.equal(cleanName('  a   b '), 'a b')
  assert.equal(cleanName('x'.repeat(70)).length, MAX_NAME)
  assert.equal(cleanName(undefined), '')
})

test('setupId is unique', () => {
  assert.equal(setupId('Research', []), 'research')
  assert.equal(setupId('Research', ['research']), 'research-2')
  assert.equal(setupId('Research', ['research', 'research-2']), 'research-3')
  assert.equal(setupId('!!!', []), 'setup')
  assert.match(setupId('A é b', []), /^[a-z0-9-]+$/)
  assert.ok(setupId('x'.repeat(80), []).length <= 40)
})

test('setupsData tolerates a bad document', () => {
  assert.deepEqual(setupsData(null), { default: '', setups: [] })
  assert.deepEqual(setupsData({ data: { setups: 'no' } }), { default: '', setups: [] })
  const d = setupsData({ data: { default: 'a', setups: [{ id: 'a', name: 'A' }, null, { name: 'x' }] } })
  assert.equal(d.default, 'a')
  assert.deepEqual(d.setups.map((s) => s.id), ['a'])
})

test('captureSetup omits empty lists', () => {
  const s = captureSetup({ name: ' My  setup ', model: 'm', effort: 'low', tools: { addons_off: ['a'], mcp_off: [], builtins_on: ['x'] } })
  assert.deepEqual(s, { id: '', name: 'My setup', model: 'm', effort: 'low', tools: { addons_off: ['a'], builtins_on: ['x'] } })
  assert.deepEqual(captureSetup({ name: 'n', model: 'm', effort: '', tools: null }).tools, {})
})

test('isEdited ignores list order and empty effort', () => {
  const s = { id: 'a', name: 'A', model: 'm', tools: { addons_off: ['a', 'b'] } }
  assert.equal(isEdited(s, { model: 'm', effort: '', tools: { addons_off: ['b', 'a'] } }), false)
  assert.equal(isEdited(s, { model: 'm', effort: undefined, tools: { addons_off: ['a', 'b'], mcp_off: [] } }), false)
  assert.equal(isEdited(s, { model: 'n', effort: '', tools: { addons_off: ['a', 'b'] } }), true)
  assert.equal(isEdited(s, { model: 'm', effort: 'low', tools: { addons_off: ['a', 'b'] } }), true)
  assert.equal(isEdited(s, { model: 'm', effort: '', tools: null }), true)
  assert.equal(isEdited(s, { model: 'm', effort: '', tools: { addons_off: ['a'] } }), true)
})

test('withAdded refuses the 51st setup', () => {
  let d = { default: '', setups: [] }
  for (let i = 0; i < MAX_SETUPS; i++) d = withAdded(d, { id: '', name: `s${i}` })
  assert.equal(d.setups.length, 50)
  assert.throws(() => withAdded(d, { id: '', name: 'more' }), RangeError)
})

test('withAdded makes names unique case-insensitively', () => {
  const d = data('Work', 'work', 'WORK')
  assert.deepEqual(d.setups.map((s) => s.name), ['Work', 'work 2', 'WORK 3'])
  assert.deepEqual(d.setups.map((s) => s.id), ['work', 'work-2', 'work-3'])
})

test('withRemoved clears the default', () => {
  const d = withDefault(data('A', 'B'), 'a')
  assert.equal(d.default, 'a')
  const r = withRemoved(d, 'a')
  assert.equal(r.default, '')
  assert.deepEqual(r.setups.map((s) => s.id), ['b'])
  assert.equal(withRemoved(withDefault(data('A', 'B'), 'b'), 'a').default, 'b')
  assert.equal(d.setups.length, 2)
})

test('withReplaced patches one setup', () => {
  const d = data('A', 'B')
  const r = withReplaced(d, 'a', { model: 'm' })
  assert.equal(r.setups[0].model, 'm')
  assert.equal(r.setups[0].id, 'a')
  assert.equal(d.setups[0].model, undefined)
  assert.deepEqual(withReplaced(d, 'zz', { model: 'm' }), d)
})

test('withMoved stops at the ends', () => {
  const d = data('A', 'B', 'C')
  assert.deepEqual(withMoved(d, 'a', -1).setups.map((s) => s.id), ['a', 'b', 'c'])
  assert.deepEqual(withMoved(d, 'c', 1).setups.map((s) => s.id), ['a', 'b', 'c'])
  assert.deepEqual(withMoved(d, 'b', 1).setups.map((s) => s.id), ['a', 'c', 'b'])
  assert.deepEqual(withMoved(d, 'b', -1).setups.map((s) => s.id), ['b', 'a', 'c'])
})

test('withDefault ignores an unknown id and clears with empty', () => {
  const d = data('A')
  assert.equal(withDefault(d, 'zz').default, '')
  assert.equal(withDefault(withDefault(d, 'a'), '').default, '')
})

test('toolsOn lists the groups that are on', () => {
  assert.deepEqual(toolsOn([{ group: 'web', off: false }, { group: 'x', off: true }, { group: 'y', off: false, opt_in: true }]), ['web', 'y'])
  assert.deepEqual(toolsOn(undefined), [])
})

const lists = {
  addons: [{ id: 'a' }, { id: 'b' }],
  servers: [{ name: 'srv' }],
  builtins: [{ group: 'web' }, { group: 'files' }, { group: 'agents', opt_in: true, off: true }],
  skills: [{ id: 's1', enabled: true }, { id: 's2', enabled: true }],
}

test('choicesFromTools round-trips', () => {
  const tools = { addons_off: ['a'], mcp_off: ['srv'], builtins_off: ['web'], builtins_on: ['agents'], skills_off: ['s2'] }
  const applied = applyChoices(lists, choicesFromTools(tools))
  assert.deepEqual(draftSwitches({ ...applied, mcpOff: applied.mcpOff }), tools)
  assert.deepEqual(choicesFromTools(null), emptyChoices())
})

test('choicesFromTools ignores names the lists do not have', () => {
  const tools = { builtins_off: ['gone'], addons_off: ['nope'], skills_off: ['s9'] }
  const applied = applyChoices(lists, choicesFromTools(tools))
  const out = draftSwitches({ ...applied, mcpOff: applied.mcpOff })
  assert.equal(out, null)
  assert.equal(applied.builtins.find((b) => b.group === 'files').off, undefined)
})

test('cleanName cuts by code points, so an emoji at the end is not split', () => {
  const name = `${'a'.repeat(MAX_NAME - 1)}😀😀`
  const out = cleanName(name)
  assert.equal(Array.from(out).length, MAX_NAME)
  assert.ok(out.endsWith('😀'))
  assert.doesNotMatch(out, /[\ud800-\udbff](?![\udc00-\udfff])/)
  assert.equal(Array.from(cleanName('😀'.repeat(70))).length, MAX_NAME)
})

test('withAdded keeps an emoji whole when a duplicate name gets a suffix', () => {
  const base = `${'a'.repeat(MAX_NAME - 3)}😀`
  const d = withAdded(withAdded({ default: '', setups: [] }, { id: '', name: base }), { id: '', name: base })
  assert.doesNotMatch(d.setups[1].name, /[\ud800-\udbff](?![\udc00-\udfff])/)
  assert.ok(Array.from(d.setups[1].name).length <= MAX_NAME)
})

test('isEdited does not compare the model of a setup that has none', () => {
  assert.equal(isEdited({ model: '', effort: '', tools: {} }, { model: 'm1', effort: '', tools: {} }), false)
  assert.equal(isEdited({ model: 'm0', effort: '', tools: {} }, { model: 'm1', effort: '', tools: {} }), true)
})
