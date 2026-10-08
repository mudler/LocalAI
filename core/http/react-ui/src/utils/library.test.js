import assert from 'node:assert/strict'
import test from 'node:test'

import {
  agentSkills, collectionUsers, estimateTokens, kbMode, kbResults, kbSearchesEveryMessage,
  normaliseSource, passageScore, passageSource, skillLoadTokens, skillUsers, withKb,
  withoutKb, withoutSkill, withSkill,
} from './library.js'

const ALL = ['cite', 'translate', 'triage']

test('an empty selection means every skill, and skills off means none', () => {
  assert.deepEqual(agentSkills({ enable_skills: false, selected_skills: ['cite'] }, ALL), { on: false, all: false, names: [] })
  assert.deepEqual(agentSkills({ enable_skills: true }, ALL), { on: true, all: true, names: ALL })
  assert.deepEqual(agentSkills({ enable_skills: true, selected_skills: ['cite'] }, ALL), { on: true, all: false, names: ['cite'] })
})

test('a skill is used by the agents that load it, and by nobody otherwise', () => {
  const agents = [
    { name: 'research', config: { enable_skills: true, selected_skills: ['cite'] } },
    { name: 'support', config: { enable_skills: true } },
    { name: 'idle', config: { enable_skills: false, selected_skills: ['translate'] } },
  ]
  assert.deepEqual(skillUsers(agents, 'cite', ALL), [{ name: 'research', all: false }, { name: 'support', all: true }])
  assert.deepEqual(skillUsers(agents, 'translate', ALL), [{ name: 'support', all: true }])
  assert.deepEqual(skillUsers([agents[0], agents[2]], 'translate', ALL), [])
})

test('a collection is used by the agent with the same name when its knowledge base is on', () => {
  const agents = [
    { name: 'handbook', config: { enable_kb: true } },
    { name: 'notes', config: { enable_kb: false } },
    { name: 'other', config: { enable_kb: true } },
  ]
  assert.deepEqual(collectionUsers(agents, 'handbook'), [{ name: 'handbook', all: false }])
  assert.deepEqual(collectionUsers(agents, 'notes'), [])
  assert.deepEqual(collectionUsers(agents, 'unknown'), [])
})

test('adding a skill turns skills on and keeps an explicit list', () => {
  assert.equal(withSkill({ enable_skills: true }, 'cite', ALL), null)
  assert.deepEqual(withSkill({ enable_skills: false, model: 'm' }, 'cite', ALL), { enable_skills: true, selected_skills: ['cite'], model: 'm' })
  assert.deepEqual(withSkill({ enable_skills: true, selected_skills: ['cite'] }, 'triage', ALL).selected_skills, ['cite', 'triage'])
  assert.equal(withSkill({ enable_skills: true, selected_skills: ['cite'] }, 'cite', ALL), null)
})

test('removing a skill never turns an empty list into "every skill"', () => {
  const one = withoutSkill({ enable_skills: true, selected_skills: ['cite'] }, 'cite', ALL)
  assert.equal(one.switchedOff, true)
  assert.equal(one.config.enable_skills, false)
  assert.deepEqual(one.config.selected_skills, [])
  const two = withoutSkill({ enable_skills: true, selected_skills: ['cite', 'triage'] }, 'cite', ALL)
  assert.deepEqual(two.config.selected_skills, ['triage'])
  assert.equal(two.switchedOff, false)
  // From "every skill" the rest are named, so the others stay on.
  const all = withoutSkill({ enable_skills: true }, 'cite', ALL)
  assert.deepEqual(all.config.selected_skills, ['translate', 'triage'])
  assert.equal(withoutSkill({ enable_skills: false }, 'cite', ALL), null)
})

test('the knowledge base switches with one flag', () => {
  assert.deepEqual(withKb({ name: 'a' }), { name: 'a', enable_kb: true })
  assert.equal(withKb({ enable_kb: true }), null)
  assert.deepEqual(withoutKb({ enable_kb: true }), { enable_kb: false })
  assert.equal(withoutKb({}), null)
})

test('the token estimate is characters divided by four, and tools mode loads nothing up front', () => {
  assert.equal(estimateTokens(''), 0)
  assert.equal(estimateTokens('abcd'), 1)
  assert.equal(estimateTokens('a'.repeat(400)), 100)
  assert.equal(skillLoadTokens({ content: 'a'.repeat(400) }, {}), 100)
  assert.equal(skillLoadTokens({ content: 'a'.repeat(400) }, { skills_mode: 'both' }), 100)
  assert.equal(skillLoadTokens({ content: 'a'.repeat(400) }, { skills_mode: 'tools' }), 0)
})

test('knowledge base mode falls back to the legacy flags', () => {
  assert.equal(kbMode({}), 'auto_search')
  assert.equal(kbMode({ kb_as_tools: true }), 'tools')
  assert.equal(kbMode({ kb_as_tools: true, kb_auto_search: true }), 'both')
  assert.equal(kbMode({ kb_mode: 'tools' }), 'tools')
  assert.equal(kbSearchesEveryMessage({ enable_kb: true }), true)
  assert.equal(kbSearchesEveryMessage({ enable_kb: true, kb_mode: 'tools' }), false)
  assert.equal(kbSearchesEveryMessage({ enable_kb: false }), false)
  assert.equal(kbResults({}), 5)
  assert.equal(kbResults({ kb_results: 8 }), 8)
})

test('search results and sources are read in whichever shape the server sends', () => {
  assert.equal(passageScore({ similarity: 0.7 }), 0.7)
  assert.equal(passageScore({ score: '0.5' }), 0.5)
  assert.equal(passageScore({}), null)
  assert.equal(passageSource({ metadata: { filename: 'a.pdf' } }), 'a.pdf')
  assert.equal(passageSource({}), '')
  assert.deepEqual(normaliseSource('https://x.example.org'), { url: 'https://x.example.org', interval: 0, lastUpdate: null })
  const s = normaliseSource({ url: 'https://x.example.org', update_interval: 60, last_update: '0001-01-01T00:00:00Z' })
  assert.equal(s.interval, 60)
  assert.equal(s.lastUpdate, null)
  assert.ok(normaliseSource({ url: 'u', last_update: '2026-10-01T10:00:00Z' }).lastUpdate > 0)
})
