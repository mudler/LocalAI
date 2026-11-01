import assert from 'node:assert/strict'
import test from 'node:test'

import {
  agentSkills, collectionUsers, estimateTokens, kbResults,
  normaliseSource, passageScore, passageSource, skillLoadTokens, skillsMode,
  skillUsers, withCollection, withoutCollection, withSkill, withoutSkill,
} from './library.js'

test('the skills of an agent are its exact list, and an empty list grants none', () => {
  assert.deepEqual(agentSkills({ skills: [] }), { on: false, all: false, names: [] })
  assert.deepEqual(agentSkills({}), { on: false, all: false, names: [] })
  assert.deepEqual(agentSkills({ skills: ['cite', '', 'triage'] }), { on: true, all: false, names: ['cite', 'triage'] })
})

test('the users of a collection are the agents that list it', () => {
  const agents = [
    { name: 'research', config: { skills: ['cite'], collections: ['papers'] } },
    { name: 'support', config: { skills: [], collections: ['papers', 'faq'] } },
    { name: 'bare', config: {} },
  ]
  assert.deepEqual(collectionUsers(agents, 'papers').map(u => u.name), ['research', 'support'])
  assert.deepEqual(collectionUsers(agents, 'faq').map(u => u.name), ['support'])
  assert.deepEqual(collectionUsers(agents, 'none'), [])
})

test('a collection is granted and taken away by editing the list', () => {
  assert.deepEqual(withCollection({ name: 'a' }, 'papers'), { name: 'a', collections: ['papers'] })
  assert.deepEqual(withCollection({ collections: ['faq'] }, 'papers').collections, ['faq', 'papers'])
  assert.equal(withCollection({ collections: ['papers'] }, 'papers'), null)
  assert.deepEqual(withoutCollection({ collections: ['faq', 'papers'] }, 'papers').collections, ['faq'])
  assert.equal(withoutCollection({}, 'papers'), null)
})

test('a skill is granted and taken away by editing the list, and users are found by the skill name', () => {
  assert.deepEqual(withSkill({ name: 'a' }, 'review'), { name: 'a', skills: ['review'] })
  assert.equal(withSkill({ skills: ['review'] }, 'review'), null)
  assert.deepEqual(withoutSkill({ skills: ['x', 'review'] }, 'review').skills, ['x'])
  assert.equal(withoutSkill({}, 'review'), null)
  const agents = [{ name: 'a', id: '1', config: { skills: ['review'] } }, { name: 'b', id: '2', config: {} }]
  assert.deepEqual(skillUsers(agents, 'review'), [{ name: 'a', id: '1', all: false }])
})

test('the token estimate is characters divided by four, and a skill adds nothing up front', () => {
  assert.equal(estimateTokens(''), 0)
  assert.equal(estimateTokens('abcd'), 1)
  assert.equal(estimateTokens('a'.repeat(400)), 100)
  assert.equal(skillLoadTokens(), 0)
  assert.equal(skillsMode(), 'tools')
  assert.equal(kbResults(), 5)
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
