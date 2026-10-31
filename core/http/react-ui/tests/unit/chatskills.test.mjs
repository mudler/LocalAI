import test from 'node:test'
import assert from 'node:assert/strict'
import {
  SEARCH_MIN, changedIds, fallbackNotice, filterSkills, offList, pickerSkills, revertSkills, skillCounts, skillKey, skillSource, withSkillsSet,
} from '../../src/utils/chatSkills.js'

const skills = [
  { id: 'personal:review', name: 'review', description: 'Check a change', enabled: true },
  { id: 'shared:notes', name: 'notes', description: 'Keep notes', enabled: false },
  { name: 'old', enabled: true },
]

test('chat skills: the key is the id, or the name from an older server', () => {
  assert.equal(skillKey(skills[0]), 'personal:review')
  assert.equal(skillKey(skills[2]), 'old')
})

test('chat skills: the search needs more than eight skills', () => {
  assert.equal(SEARCH_MIN, 9)
})

test('chat skills: the filter matches name and description, ignoring case and spaces', () => {
  assert.deepEqual(filterSkills(skills, '  CHECK ').map(skillKey), ['personal:review'])
  assert.deepEqual(filterSkills(skills, 'note').map(skillKey), ['shared:notes'])
  assert.equal(filterSkills(skills, '').length, 3)
  assert.equal(filterSkills(skills, 'zzz').length, 0)
})

test('chat skills: the count and the stored list follow the switches', () => {
  assert.deepEqual(skillCounts(skills), { on: 2, total: 3 })
  assert.deepEqual(offList(skills), ['shared:notes'])
  assert.deepEqual(offList(withSkillsSet(skills, new Set(skills.map(skillKey)), false)), ['old', 'personal:review', 'shared:notes'])
  assert.deepEqual(offList(withSkillsSet(skills, new Set(skills.map(skillKey)), true)), [])
})

test('chat skills: a set change touches only the ids named and keeps unchanged rows as they were', () => {
  const next = withSkillsSet(skills, new Set(['shared:notes']), true)
  assert.equal(next[0], skills[0])
  assert.equal(next[1].enabled, true)
  assert.deepEqual(changedIds(skills, new Set(['shared:notes', 'old']), true), ['shared:notes'])
  assert.deepEqual(changedIds(skills, new Set(['shared:notes']), false), [])
})

test('chat skills: a failed request puts back its own ids only', () => {
  const during = withSkillsSet(withSkillsSet(skills, new Set(['shared:notes']), true), new Set(['old']), false)
  const back = revertSkills(during, skills, new Set(['shared:notes']))
  assert.equal(back[1].enabled, false)
  assert.equal(back[2].enabled, false)
})

test('chat skills: an add-on skill wins, and the built-in one it replaces is listed apart', () => {
  const list = [
    { id: 'addon:xlsx', name: 'xlsx', layer: 'addon', addon: 'office', addon_title: 'Office', enabled: true },
    { id: 'builtin:xlsx', name: 'xlsx', layer: 'builtin', shadowed: 'addon', shadowed_by: 'office', enabled: true },
    { id: 'builtin:docx', name: 'docx', layer: 'builtin', fallback: 'Office', enabled: true },
    { id: 'personal:docx', name: 'docx', layer: 'personal', shadowed: 'x', enabled: true },
    { id: 'addon:bad', name: 'bad', layer: 'addon', invalid: 'no name', enabled: true },
  ]
  const picked = pickerSkills(list)
  assert.deepEqual(picked.skills.map(skillKey), ['addon:xlsx', 'builtin:docx'])
  assert.deepEqual(picked.replaced.map(skillKey), ['builtin:xlsx'])
  assert.deepEqual(pickerSkills(undefined), { skills: [], replaced: [] })
})

test('chat skills: the source of a row is the add-on title or the layer', () => {
  assert.deepEqual(skillSource({ layer: 'addon', addon: 'office', addon_title: 'Office' }), { kind: 'addon', name: 'Office' })
  assert.deepEqual(skillSource({ layer: 'addon', addon: 'office' }), { kind: 'addon', name: 'office' })
  assert.deepEqual(skillSource({ layer: 'builtin' }), { kind: 'builtin' })
})

test('chat skills: one notice names the built-in skills that serve for an add-on', () => {
  assert.equal(fallbackNotice([{ layer: 'builtin', name: 'xlsx', enabled: true }]), null)
  const n = fallbackNotice([
    { layer: 'builtin', name: 'xlsx', fallback: 'Office', enabled: true },
    { layer: 'builtin', name: 'docx', fallback: 'Office', enabled: true },
    { layer: 'builtin', name: 'pdf', fallback: 'Office', enabled: false },
  ])
  assert.deepEqual(n, { skills: ['xlsx', 'docx'], addons: ['Office'] })
})
