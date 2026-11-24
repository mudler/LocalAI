import test from 'node:test'
import assert from 'node:assert/strict'
import {
  skillDeclarationLabel, skillInstallNoticeKey, SEARCH_MIN, changedIds, fallbackNotice, filterSkills, offList, pickerSkills, revertSkills, skillCounts, skillKey, skillSource, withSkillsSet,
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
    { id: 'addon:writing', name: 'writing', layer: 'addon', addon: 'office', addon_title: 'Office', enabled: true },
    { id: 'builtin:writing', name: 'writing', layer: 'builtin', shadowed: 'addon', shadowed_by: 'office', enabled: true },
    { id: 'builtin:apps', name: 'apps', layer: 'builtin', fallback: 'Office', enabled: true },
    { id: 'personal:docx', name: 'docx', layer: 'personal', shadowed: 'x', enabled: true },
    { id: 'addon:bad', name: 'bad', layer: 'addon', invalid: 'no name', enabled: true },
  ]
  const picked = pickerSkills(list)
  assert.deepEqual(picked.skills.map(skillKey), ['addon:writing', 'builtin:apps'])
  assert.deepEqual(picked.replaced.map(skillKey), ['builtin:writing'])
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
    { layer: 'builtin', name: 'writing', fallback: 'Office', enabled: true },
    { layer: 'builtin', name: 'apps', fallback: 'Office', enabled: true },
    { layer: 'builtin', name: 'pdf', fallback: 'Office', enabled: false },
  ])
  assert.deepEqual(n, { skills: ['writing', 'apps'], addons: ['Office'] })
})

// Document slots have providers, not a removed core implementation. Ordinary
// replacements still describe and retain their existing built-in fallback.
test('document declarations use provider-neutral labels, remaining built-ins use replacement labels', () => {
  for (const name of ['docx', 'pptx', 'pdf', 'xlsx']) {
    assert.equal(skillDeclarationLabel({ name, replaces: true }), 'provides')
  }
  assert.equal(skillDeclarationLabel({ name: 'writing', replaces: true }), 'replaces')
  assert.equal(skillDeclarationLabel({ name: 'custom', replaces: false }), null)
})

test('document provider states never invent a built-in or fallback', () => {
  for (const name of ['docx', 'pptx', 'pdf', 'xlsx']) {
    for (const provider of ['office', 'sandbox']) {
      for (const state of [{ enabled: true }, { enabled: false }, { enabled: true, unavailable: 'pinned provider unavailable' }]) {
        const row = { id: `addon:${name}`, name, layer: 'addon', addon: provider, ...state }
        const picked = pickerSkills([row])
        assert.deepEqual(picked, { skills: [row], replaced: [] })
        assert.equal(fallbackNotice(picked.skills), null)
        assert.deepEqual(skillSource(row), { kind: 'addon', name: provider })
      }
    }
  }
  assert.deepEqual(pickerSkills([]), { skills: [], replaced: [] })
})

test('install document notices use flow provider keys and retain built-in replacement notices', async () => {
  const { readFile } = await import('node:fs/promises')
  const en = JSON.parse(await readFile(new URL('../../public/locales/en/addons.json', import.meta.url)))
  assert.equal(en.flow.skillProvides, 'This add-on provides the {{skill}} skill.')
  assert.match(en.flow.skillProvidesWarning, /Another add-on/)
  assert.match(en.flow.skillShadow, /built-in/)
  assert.equal(en.skills.skillShadow, undefined)
  const source = await readFile(new URL('../../src/components/addons/InstallDialog.jsx', import.meta.url), 'utf8')
  assert.match(source, /skillInstallNoticeKey\(c\)/)
  for (const skill of ['docx', 'pptx', 'pdf', 'xlsx']) {
    assert.equal(skillInstallNoticeKey({ skill }), 'flow.skillProvides')
    assert.equal(skillInstallNoticeKey({ skill, name: 'Office' }), 'flow.skillProvidesWarning')
  }
  assert.equal(skillInstallNoticeKey({ skill: 'writing' }), 'flow.skillShadow')
  assert.equal(skillInstallNoticeKey({ skill: 'writing', name: 'Other' }), 'flow.skillShadowWarning')
})

test('skillRef names the owner of an add-on skill or an agent skill, and nothing for the other layers', async () => {
  const { skillRef, skillSource } = await import('../../src/utils/chatSkills.js')
  assert.deepEqual(skillRef({ layer: 'addon', addon: 'office' }), { addon: 'office' })
  assert.deepEqual(skillRef({ layer: 'agent', agent: 'a1', agent_name: 'Sales' }), { agent: 'a1' })
  assert.deepEqual(skillRef({ layer: 'personal' }), {})
  assert.deepEqual(skillSource({ layer: 'agent', agent: 'a1', agent_name: 'Sales' }), { kind: 'agent', name: 'Sales' })
})
