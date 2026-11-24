import test from 'node:test'
import assert from 'node:assert/strict'
import { attachedCollections, attachedOf, hasAttached, markAddons, markConnectors, markServers, markSkills } from '../../src/utils/projectAttached.js'
import { attachedCount, cleanAttached, outsideLimit, projectSkillPath } from '../../src/utils/projects.js'
import { bulkIds, bulkable } from '../../src/utils/chatSwitchRows.js'
import { effectsOf } from '../../src/utils/agentProject.js'
import { skillRef } from '../../src/utils/chatSkills.js'

test('project attached: a project without the field reads as five empty lists', () => {
  assert.deepEqual(attachedOf({}), { skills: [], addons: [], mcp_servers: [], connectors: [], collections: [] })
  assert.deepEqual(attachedOf(null).skills, [])
  assert.equal(hasAttached(attachedOf({})), false)
  assert.equal(hasAttached(attachedOf({ attached: { addons: ['mail'] } })), true)
})

test('project attached: the clean step trims, de-duplicates and drops empty lists', () => {
  assert.deepEqual(cleanAttached({ skills: [' a ', 'a', ''], addons: [] }), { skills: ['a'] })
  assert.equal(attachedCount({ skills: ['a'], connectors: ['x', 'y'] }), 3)
})

test('project attached: an entry outside a non-empty limit is reported, an empty limit limits nothing', () => {
  const attached = { skills: ['a', 'b'], addons: ['mail'], collections: ['docs'] }
  assert.deepEqual(outsideLimit(attached, { skills: ['a'], collections: [] }), [{ key: 'skills', name: 'b' }])
  assert.deepEqual(outsideLimit(attached, {}), [])
  assert.deepEqual(outsideLimit({}, { skills: ['a'] }), [])
  assert.deepEqual(outsideLimit(attached, { addons: ['other'] }), [{ key: 'addons', name: 'mail' }])
})

test('project attached: attached skills and project-layer skills show on and locked, others stay', () => {
  const rows = [
    { name: 'brand', layer: 'personal', enabled: false },
    { name: 'plan', layer: 'project', enabled: true },
    { name: 'other', layer: 'personal', enabled: false },
    { name: 'brand', layer: 'builtin', enabled: true, shadowed: 'personal' },
  ]
  const out = markSkills(rows, { skills: ['brand'] })
  assert.deepEqual(out.map(s => [s.name, s.enabled, !!s.locked, !!s.fromProject]), [['brand', true, true, true], ['plan', true, true, true], ['other', false, false, false], ['brand', true, false, false]])
  assert.deepEqual(markSkills(rows, null)[0], rows[0])
})

test('project attached: add-ons, servers and connectors are marked on and read only by their own key', () => {
  const attached = { addons: ['mail'], mcp_servers: ['docs'], connectors: ['connector:c1', 'c2'] }
  const addons = markAddons([{ id: 'mail', off: true }, { id: 'cal', off: true }], attached)
  assert.deepEqual(addons.map(a => [a.off, !!a.readOnly, !!a.fromProject]), [[false, true, true], [true, false, false]])
  const servers = markServers([{ name: 'docs', off: true }, { name: 'web', off: false }], attached)
  assert.deepEqual(servers.map(s => !!s.fromProject), [true, false])
  const connectors = markConnectors([{ id: 'c1' }, { id: 'c2' }, { id: 'c3' }], attached)
  assert.deepEqual(connectors.map(c => !!c.fromProject), [true, true, false])
  assert.deepEqual(attachedCollections({ collections: ['papers'] }), ['papers'])
  assert.deepEqual(attachedCollections(null), [])
})

test('project attached: Select all and Disable all leave a read only row alone', () => {
  const rows = [{ id: 'a', on: true, readOnly: true }, { id: 'b', on: true }, { id: 'c', on: false }]
  assert.deepEqual(bulkIds(rows, false), ['b'])
  assert.deepEqual(bulkIds(rows, true), ['c'])
  assert.equal(bulkable(rows), 2)
})

test('project attached: the move report names what the old project attached and the new one attaches', () => {
  const out = effectsOf({
    chats: 0, loops: 0, shares: 0, proposals: 0, triggers_outside: [],
    attached_old: { skills: ['old-skill'], connectors: ['mail'] },
    attached_new: { addons: ['calendar'], collections: [] },
  })
  assert.deepEqual(out.map(e => e.key), ['attachedLost', 'attachedGained'])
  assert.deepEqual(out[0].names, ['old-skill', 'mail'])
  assert.equal(out[1].count, 1)
  assert.deepEqual(effectsOf({ attached_old: {}, attached_new: undefined }), [])
  assert.deepEqual(effectsOf({ project_skills_switch: true }).map(e => e.key), ['projectSkills'])
  assert.deepEqual(effectsOf({ project_skills_switch: false }), [])
})

test('project attached: a project skill names its project in the editor route and the API reference', () => {
  assert.equal(projectSkillPath({ id: 'p 1' }), '/app/skills/new?layer=project&project=p%201')
  assert.equal(projectSkillPath({ id: 'p1' }, 'plan'), '/app/skills/edit/project/plan?project=p1')
  assert.deepEqual(skillRef({ layer: 'project', project: 'p1' }), { project: 'p1' })
  assert.deepEqual(skillRef({ layer: 'personal' }), {})
})
