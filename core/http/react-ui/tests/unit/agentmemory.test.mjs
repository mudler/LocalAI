import test from 'node:test'
import assert from 'node:assert/strict'
import { agentPayload, CONNECTOR_GRANTS, emptyAgent, MEMORY_CHOICES, MEMORY_SAVE_RULES, memoryChoiceOf, memorySaveOf, memorySaves, newAgent, stricterLevel, withMemoryChoice, withMemoryLevel, memoryLevelOf, MEMORY_LEVELS, MEMORY_WHOSE } from '../../src/utils/chatAgents.js'
import { agentNoteCount, agentNotes, noteTabs } from '../../src/utils/memoryFiles.js'

test('a new agent starts with Mine and its own, the grant to save, and the connector defaults', () => {
  const a = newAgent()
  assert.equal(a.memory, 'mine_and_own')
  assert.ok(a.builtins.includes('memory.write'))
  for (const g of CONNECTOR_GRANTS) assert.ok(a.builtins.includes(g), g)
  assert.equal(emptyAgent().memory, undefined, 'the empty agent has no choice: as before')
})

test('the payload carries a choice only when there is one', () => {
  assert.equal('memory' in agentPayload(emptyAgent()), false, 'an agent as before sends none, so the server keeps the stored one')
  for (const c of MEMORY_CHOICES) assert.equal(agentPayload({ ...emptyAgent(), memory: c }).memory, c)
  assert.equal('memory' in agentPayload({ ...emptyAgent(), memory: 'everything' }), false)
})

test('an agent from the server shows its choice, and as before when it has none', () => {
  assert.equal(memoryChoiceOf({ memory: 'own' }), 'own')
  assert.equal(memoryChoiceOf({}), '')
  assert.equal(memoryChoiceOf({ memory: 'bogus' }), '')
})

test('the level is read from the two saved fields and nothing else', () => {
  assert.deepEqual(MEMORY_LEVELS, ['off', 'reads', 'saves'])
  assert.deepEqual(MEMORY_WHOSE, ['mine_and_own', 'own', 'mine'])
  assert.equal(memoryLevelOf(newAgent()), 'saves', 'a new agent reads and saves, with mine and its own')
  assert.equal(memoryLevelOf({ memory: 'own', builtins: [] }), 'reads')
  assert.equal(memoryLevelOf({ memory: 'mine', builtins: ['memory.write'] }), 'saves')
  assert.equal(memoryLevelOf({ memory: 'none', builtins: ['memory.write'] }), 'off', 'None wins; the grant is not rewritten on load')
  assert.equal(memoryLevelOf({ memory: '', builtins: ['memory.write'] }), 'saves', 'an agent from before the choice goes by its grant')
  assert.equal(memoryLevelOf({ memory: '', builtins: [] }), 'reads')
  assert.equal(memoryLevelOf({}), 'reads')
})

test('Off sets None and removes the grant; Reads removes it; Reads and saves adds it', () => {
  const had = { ...emptyAgent(), memory: 'own', builtins: ['skills.write', 'memory.write'] }
  const off = withMemoryLevel(had, 'off')
  assert.equal(off.memory, 'none')
  assert.deepEqual(off.builtins, ['skills.write'])
  const reads = withMemoryLevel(had, 'reads')
  assert.equal(reads.memory, 'own', 'the choice of whose notes stays')
  assert.deepEqual(reads.builtins, ['skills.write'])
  const saves = withMemoryLevel(reads, 'saves')
  assert.equal(saves.memory, 'own')
  assert.deepEqual(saves.builtins, ['skills.write', 'memory.write'])
  assert.deepEqual(withMemoryLevel(saves, 'saves').builtins, ['skills.write', 'memory.write'], 'the grant is never listed twice')
})

test('coming back from Off restores the choice the agent had, or the default', () => {
  const off = withMemoryLevel({ ...emptyAgent(), memory: 'mine', builtins: ['memory.write'] }, 'off')
  assert.equal(withMemoryLevel(off, 'reads', 'mine').memory, 'mine')
  assert.equal(withMemoryLevel(off, 'saves', 'own').memory, 'own')
  assert.equal(withMemoryLevel(off, 'saves').memory, 'mine_and_own', 'no history: the default')
  assert.equal(withMemoryLevel(off, 'saves', 'bogus').memory, 'mine_and_own')
})

test('an agent from before the choice keeps no choice unless it goes through Off', () => {
  const old = { ...emptyAgent(), builtins: [] }
  assert.equal(withMemoryLevel(old, 'saves').memory, undefined)
  assert.deepEqual(withMemoryLevel(old, 'saves').builtins, ['memory.write'])
  assert.equal(withMemoryLevel(old, 'off').memory, 'none')
})

test('choosing whose notes changes the choice only; None is the level Off', () => {
  const base = { ...emptyAgent(), builtins: ['skills.write'] }
  assert.deepEqual(withMemoryChoice(base, 'own').builtins, ['skills.write'], 'reads stays reads')
  assert.equal(withMemoryChoice(base, 'own').memory, 'own')
  const had = { ...emptyAgent(), memory: 'own', builtins: ['memory.write'] }
  assert.deepEqual(withMemoryChoice(had, 'mine').builtins, ['memory.write'])
  const none = withMemoryChoice(had, 'none')
  assert.equal(none.memory, 'none')
  assert.deepEqual(none.builtins, [])
  assert.deepEqual([...MEMORY_CHOICES.filter(memorySaves)], ['mine_and_own', 'own', 'mine'])
})

test('the save rule of an agent is auto, always or never; ask is not offered', () => {
  assert.deepEqual(MEMORY_SAVE_RULES, ['auto', 'always', 'never'])
  assert.equal(memorySaveOf({ memory_save: 'auto' }), 'auto')
  assert.equal(memorySaveOf({ memory_save: 'always' }), 'always')
  assert.equal(memorySaveOf({ memory_save: 'never' }), 'never')
  assert.equal(memorySaveOf({}), '', 'an agent without a rule uses my setting')
  assert.equal(memorySaveOf({ memory_save: 'ask' }), '')
})

test('the payload sends a save rule only when the form holds one, and use_mine clears it', () => {
  assert.equal('memory_save' in agentPayload(emptyAgent()), false, 'an older form leaves the stored rule alone')
  assert.equal(agentPayload({ ...emptyAgent(), memory_save: 'always' }).memory_save, 'always')
  assert.equal(agentPayload({ ...emptyAgent(), memory_save: 'never' }).memory_save, 'never')
  assert.equal(agentPayload({ ...emptyAgent(), memory_save: 'use_mine' }).memory_save, 'use_mine')
  assert.equal(agentPayload({ ...emptyAgent(), memory_save: 'auto' }).memory_save, 'auto')
  assert.equal('memory_save' in agentPayload({ ...emptyAgent(), memory_save: 'ask' }), false, 'ask is never sent')
  assert.equal('memory_save' in agentPayload({ ...emptyAgent(), memory_save: '' }), false)
})

test('the stricter of the setting and the rule is the level in force', () => {
  assert.equal(stricterLevel('auto', 'always'), 'always')
  assert.equal(stricterLevel('ask', ''), 'ask')
  assert.equal(stricterLevel('auto', ''), 'auto')
  assert.equal(stricterLevel('always', 'never'), 'never')
  assert.equal(stricterLevel('never', 'always'), 'never', 'a rule never loosens')
  assert.equal(stricterLevel('ask', 'auto'), 'auto', 'an explicit auto loosens ask for the agent')
  assert.equal(stricterLevel('auto', 'auto'), 'auto')
  assert.equal(stricterLevel('always', 'auto'), 'always', 'the person always confirm still holds')
  assert.equal(stricterLevel('never', 'auto'), 'never', 'the person never still holds')
  assert.equal(stricterLevel('bogus', ''), 'ask', 'an unknown level counts as ask')
})

const g = (o) => ({ source: 'memory', files: [{ path: `${o.key || 'u'}/index.md` }, { path: `${o.key || 'u'}/b.md` }], ...o })

test('the notes list has a tab for yours, each agent, the deleted agents and the conversations', () => {
  const groups = [
    g({ scope: 'user' }),
    g({ scope: 'agent', key: 'a2', label: 'Zed' }),
    g({ scope: 'agent', key: 'a1', label: 'Helper' }),
    g({ scope: 'agent', key: 'gone', deleted: true }),
    g({ scope: 'channel', key: 'slack-1' }),
    { source: 'profile', scope: 'agent', key: 'a1', label: 'Helper', files: [{ path: 'p.md' }] },
  ]
  const tabs = noteTabs(groups)
  assert.deepEqual(tabs.map((t) => t.id), ['mine', 'agent:a1', 'agent:a2', 'deleted', 'channels'])
  assert.equal(tabs[1].name, 'Helper')
  assert.equal(tabs[1].groups.length, 2, 'the profile sits with its agent')
  assert.equal(tabs[3].groups[0].key, 'gone')
  assert.equal(agentNoteCount(groups, 'a1'), 2, 'the profile is not a note of its own')
  assert.equal(agentNotes(groups, 'gone').length, 1)
})

test('Mine stays when there is nothing else, and an empty list has only Mine', () => {
  assert.deepEqual(noteTabs([]).map((t) => t.id), ['mine'])
  assert.deepEqual(noteTabs([g({ scope: 'user' })]).map((t) => t.id), ['mine'])
})
