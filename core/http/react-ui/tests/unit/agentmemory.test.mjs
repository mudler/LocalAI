import test from 'node:test'
import assert from 'node:assert/strict'
import { agentPayload, CONNECTOR_GRANTS, emptyAgent, MEMORY_CHOICES, memoryChoiceOf, memorySaves, newAgent, withMemoryChoice } from '../../src/utils/chatAgents.js'
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

test('a choice that saves switches the grant on, and none leaves the grants alone', () => {
  const base = { ...emptyAgent(), builtins: ['skills.write'] }
  assert.deepEqual(withMemoryChoice(base, 'own').builtins, ['skills.write', 'memory.write'])
  assert.deepEqual(withMemoryChoice(base, 'mine').builtins, ['skills.write', 'memory.write'])
  assert.deepEqual(withMemoryChoice(base, 'none').builtins, ['skills.write'])
  assert.equal(withMemoryChoice(base, 'none').memory, 'none')
  const had = { ...emptyAgent(), builtins: ['memory.write'] }
  assert.deepEqual(withMemoryChoice(had, 'own').builtins, ['memory.write'])
  assert.deepEqual([...MEMORY_CHOICES.filter(memorySaves)], ['mine_and_own', 'own', 'mine'])
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
