import test from 'node:test'
import assert from 'node:assert/strict'
import { activePreset, presetPatch, snapshot } from '../../src/utils/agentPresets.js'
import { modelCaps } from '../../src/utils/modelCaps.js'
import { AGENT_TEMPLATES } from '../../src/utils/agentConfigTools.js'
import { emptyAgent, newAgent, MEMORY_GRANT } from '../../src/utils/chatAgents.js'

const groups = ['shell', 'files', 'web', 'media', 'collections', 'memory', 'subagents', 'saved_agents', 'triggers', 'changes']
  .map((group) => ({ group, opt_in: group === 'saved_agents' || group === 'shell' }))
const catalog = [
  { name: 'memory.write', sensitive: false }, { name: 'triggers.propose', sensitive: false }, { name: 'triggers.run', sensitive: false },
  { name: 'talkback.channels', sensitive: false }, { name: 'files.upload', sensitive: false },
  { name: 'saved_agents', sensitive: false }, { name: 'skills.write', sensitive: true }, { name: 'act.send', sensitive: true },
]

const apply = (id, form) => ({ ...form, ...presetPatch(id, form, groups, catalog) })

test('presets: nothing is known until the lists are loaded', () => {
  assert.equal(presetPatch('readonly', emptyAgent(), [], catalog), null)
  assert.equal(presetPatch('nope', emptyAgent(), groups, catalog), null)
  assert.equal(activePreset(emptyAgent(), [], catalog), null)
})

test('presets: read only turns off everything that changes things and keeps the grant of the memory choice', () => {
  const form = apply('readonly', newAgent())
  assert.deepEqual(form.builtins_off.sort(), ['changes', 'files', 'subagents', 'triggers'].sort())
  assert.deepEqual(form.builtins_on, [])
  assert.deepEqual(form.builtins, [MEMORY_GRANT])
  assert.equal(activePreset(form, groups, catalog), 'readonly')
})

test('presets: assistant adds saved agents and triggers, with the connector grants and no sensitive grant', () => {
  const form = apply('assistant', apply('readonly', newAgent()))
  assert.ok(form.builtins_on.includes('saved_agents'))
  assert.ok(!form.builtins_off.includes('triggers'))
  assert.ok(!form.builtins_off.includes('saved_agents'))
  assert.ok(form.builtins_off.includes('files'))
  assert.deepEqual([...form.builtins].sort(), ['files.upload', 'memory.write', 'talkback.channels', 'triggers.propose', 'triggers.run'])
  assert.equal(activePreset(form, groups, catalog), 'assistant')
})

test('presets: full turns every listed group on but never a sensitive grant', () => {
  const form = apply('full', apply('readonly', newAgent()))
  assert.deepEqual(form.builtins_off, [])
  assert.ok(form.builtins_on.includes('saved_agents') && form.builtins_on.includes('shell'))
  assert.ok(!form.builtins.includes('skills.write') && !form.builtins.includes('act.send'))
  assert.ok(form.builtins.includes('triggers.run'))
  assert.equal(activePreset(form, groups, catalog), 'full')
})

test('presets: a change by hand makes it a custom mix, and the level comes back by picking it', () => {
  const form = apply('assistant', newAgent())
  const edited = { ...form, builtins_off: [...form.builtins_off, 'web'] }
  assert.equal(activePreset(edited, groups, catalog), null)
  assert.equal(activePreset(apply('assistant', edited), groups, catalog), 'assistant')
})

test('presets: a grant this server does not declare is kept, and groups the server does not list stay as they are', () => {
  const form = apply('readonly', { ...newAgent(), builtins: ['memory.write', 'future.grant'], builtins_off: ['mystery'] })
  assert.ok(form.builtins.includes('future.grant'))
  assert.ok(form.builtins_off.includes('mystery'))
})

test('presets: a level only touches the fields it names, so a snapshot of them undoes it', () => {
  const before = { ...newAgent(), name: 'x', network: 'all' }
  const patch = presetPatch('readonly', before, groups, catalog)
  assert.deepEqual(Object.keys(patch).sort(), ['builtins', 'builtins_off', 'builtins_on'])
  const snap = snapshot(before, ['builtins', 'builtins_off', 'builtins_on'])
  assert.deepEqual({ ...before, ...patch, ...snap }, before)
  snap.builtins.push('z')
  assert.ok(!before.builtins.includes('z'), 'a snapshot is a copy')
})

test('presets: every template names a level that exists', () => {
  for (const t of AGENT_TEMPLATES) {
    if (t.preset) assert.ok(presetPatch(t.preset, emptyAgent(), groups, catalog), t.id)
  }
  assert.ok(AGENT_TEMPLATES.some((t) => t.preset))
})

test('model caps: flags and plain names read the same, chat and tokenize are left out', () => {
  assert.deepEqual(modelCaps({ capabilities: ['FLAG_CHAT', 'FLAG_VISION', 'vision', 'tokenize', 'FLAG_EMBEDDINGS'] }), ['vision', 'embeddings'])
  assert.deepEqual(modelCaps(undefined), [])
})

test('presets: Look only and Assistant mean No files, Full means All my files', async () => {
  const { filesChoiceOf } = await import('../../src/utils/chatAgents.js')
  assert.equal(filesChoiceOf(apply('readonly', newAgent())), 'none')
  assert.equal(filesChoiceOf(apply('assistant', newAgent())), 'none')
  assert.equal(filesChoiceOf(apply('full', apply('readonly', newAgent()))), 'all')
  assert.equal(filesChoiceOf(newAgent()), 'all', 'a new agent keeps All my files')
})
