import test from 'node:test'
import assert from 'node:assert/strict'
import { NAME_COUNTER_FROM, NAME_MAX, DESCRIPTION_CONTEXT_CHARS, entryState, memoryTopicCount, pickerChoices, sortProjects, unavailableCount } from '../../src/utils/projects.js'
import { addonOptions, collectionOptions, connectorOptions, emptySources, labelOf, serverOptions, skillOptions, TOOL_KINDS } from '../../src/utils/projectToolSources.js'

test('project tools: the limits match the server and the counter shows near the limit', () => {
  assert.equal(NAME_MAX, 120)
  assert.ok(NAME_COUNTER_FROM < NAME_MAX)
  assert.equal(DESCRIPTION_CONTEXT_CHARS, 200)
})

test('project tools: the list sorts by name ignoring case, then by slug, without changing the input', () => {
  const input = [
    { name: 'zeta', slug: 'zeta' }, { name: 'Alpha', slug: 'alpha-2' }, { name: 'alpha', slug: 'alpha' }, { name: 'Beta', slug: 'beta' },
  ]
  const copy = structuredClone(input)
  assert.deepEqual(sortProjects(input).map(p => p.slug), ['alpha', 'alpha-2', 'beta', 'zeta'])
  assert.deepEqual(input, copy)
  assert.deepEqual(sortProjects(null), [])
})

test('project tools: an index that holds only headings is not a memory topic', () => {
  assert.equal(memoryTopicCount({ index: '# Memory\n', files: [{ name: 'index.md' }] }), 0)
  assert.equal(memoryTopicCount({ index: '# Memory\n\nPlain words.', files: [{ name: 'index.md' }] }), 1)
  assert.equal(memoryTopicCount({ index: '', files: [{ name: 'index.md' }, { name: 'a.md' }, { name: 'b.md' }] }), 2)
  assert.equal(memoryTopicCount(null), 0)
})

test('project tools: an entry is flagged only when its source loaded and does not list it', () => {
  const opts = [{ value: 'docs', usable: true }, { value: 'mail', usable: false }]
  assert.equal(entryState(opts, true, 'docs'), 'ok')
  assert.equal(entryState(opts, true, 'mail'), 'off')
  assert.equal(entryState(opts, true, 'nope'), 'missing')
  assert.equal(entryState(opts, false, 'nope'), 'unknown')
  assert.equal(unavailableCount(opts, true, ['docs', 'mail', 'nope']), 2)
  assert.equal(unavailableCount(opts, false, ['docs', 'mail', 'nope']), 0)
})

test('project tools: the picker offers what is not chosen and allows a freeform name', () => {
  const opts = [{ value: 'brand-voice', label: 'brand-voice' }, { value: 'github', label: 'GitHub' }, { value: 'docs', label: 'Docs' }]
  assert.deepEqual(pickerChoices(opts, ['docs'], '').list.map(o => o.value), ['brand-voice', 'github'])
  assert.deepEqual(pickerChoices(opts, [], 'git').list.map(o => o.value), ['github'])
  assert.equal(pickerChoices(opts, [], 'git').freeform, 'git')
  assert.equal(pickerChoices(opts, [], 'github').freeform, '', 'an installed value is not freeform')
  assert.equal(pickerChoices(opts, ['own'], 'own').freeform, '', 'a chosen name is not offered again')
  assert.equal(pickerChoices(opts, [], '  ').freeform, '')
})

test('project tool sources: each kind keys the value the server matches and shows a readable label', () => {
  assert.deepEqual(TOOL_KINDS, ['skills', 'addons', 'mcp_servers', 'connectors', 'collections'])
  assert.ok(TOOL_KINDS.every(k => emptySources()[k].ready === false))
  const skills = skillOptions({ skills: [{ name: 'b', description: 'B', layer: 'user' }, { name: 'a' }, { name: 'x', invalid: 'bad' }, { name: 'y', shadowed: 'addon' }, { name: 'l', locked: true }] })
  assert.deepEqual(skills.map(s => s.value), ['a', 'b', 'l'])
  assert.equal(skills.find(s => s.value === 'l').usable, false)
  const addons = addonOptions({ addons: [{ id: 'mail-addon', title: 'Mail' }, { id: 'plain' }] })
  assert.deepEqual(addons.map(a => [a.value, a.label]), [['mail-addon', 'Mail'], ['plain', 'plain']])
  const conns = connectorOptions({ connectors: [{ id: 'c1', name: 'Docs server', usable: true }, { id: 'c2', name: 'Off one', usable: false, reason: 'switched off' }] })
  assert.deepEqual(conns.map(c => [c.value, c.label, c.usable]), [['c1', 'Docs server', true], ['c2', 'Off one', false]])
  assert.deepEqual(serverOptions([['b', 'a'], ['a', 'c']]).map(s => s.value), ['a', 'b', 'c'])
  assert.deepEqual(collectionOptions({ collections: ['papers', { name: 'faq' }, 'papers'] }).map(c => c.value), ['faq', 'papers'])
  assert.equal(labelOf(addons, 'mail-addon'), 'Mail')
  assert.equal(labelOf(addons, 'gone'), 'gone')
})
