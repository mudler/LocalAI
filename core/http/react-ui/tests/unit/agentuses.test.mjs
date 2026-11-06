import test from 'node:test'
import assert from 'node:assert/strict'
import { filterMcp, mcpRows, mcpUnknown, accountsAfter, addonNow, addonSkillRows, addonSkillState, addonSwitch, grantedSkills, memorySkillsState, pickedResources, resourcesAfter, resourcesPayload, switchableSkills } from '../../src/utils/agentUses.js'
import { agentAddons } from '../../src/utils/agentTools.js'
import { agentPayload, emptyAgent } from '../../src/utils/chatAgents.js'
import { effectiveLines, flatRows } from '../../src/utils/agentEffective.js'

const WORK = { id: 'b1', name: 'Work' }
const HOME = { id: 'b2', name: 'Home' }
const tools = [{ name: 'read_a', kind: 'read' }, { name: 'read_b', kind: 'read' }, { name: 'put_a', kind: 'write' }]

// The rows the form draws: the draft add-on, with the agent's picks applied.
const row = (addon, form = {}) => agentAddons([addon], { addons: [], ...form })[0]
const credentialed = (connections, extra = {}) => ({ id: 'cred', tools: 3, tool_list: tools, credentialed: true, connections, ...extra })

// The rule table of ConnectionsOf (core/services/studio/connectorcred.go) for a
// chat that runs a saved agent. One case per line of the table in agentUses.js.
test('right now: an add-on that is off offers nothing and keeps the accounts for later', () => {
  assert.equal(addonNow(row(credentialed([WORK, HOME]), { addons: [] })).key, 'off')
  const kept = addonNow(row(credentialed([WORK, HOME]), { addons: [], connections: { cred: ['b2'] } }))
  assert.equal(kept.key, 'offKept')
  assert.equal(kept.vars.names, 'Home')
  assert.equal(kept.tone, 'off')
})

test('right now: an add-on without a credential needs no account', () => {
  const n = addonNow(row({ id: 'plain', tools: 3, tool_list: tools }, { addons: ['plain'] }))
  assert.deepEqual([n.tone, n.key], ['ok', 'noAccountNeeded'])
  assert.deepEqual([n.vars.count, n.vars.read, n.vars.write], [3, 2, 1])
})

test('right now: a credentialed add-on and a person with no account', () => {
  const n = addonNow(row(credentialed([]), { addons: ['cred'] }))
  assert.deepEqual([n.tone, n.key, n.code], ['warn', 'noConnection', 'addon_no_connection'])
})

test('right now: the accounts the agent names are used, one or several', () => {
  const one = addonNow(row(credentialed([WORK, HOME]), { addons: ['cred'], connections: { cred: ['b2'] } }))
  assert.deepEqual([one.tone, one.key, one.vars.names], ['ok', 'using', 'Home'])
  const two = addonNow(row(credentialed([WORK, HOME]), { addons: ['cred'], connections: { cred: ['b1', 'b2'] } }))
  assert.deepEqual([two.key, two.vars.names], ['usingMany', 'Work, Home'])
})

test('right now: the agent names accounts that no longer exist, so the server uses an empty set', () => {
  // Even with one account left, the server does not fall back to it (rule 2).
  const n = addonNow(row(credentialed([WORK]), { addons: ['cred'], connections: { cred: ['gone'] } }))
  assert.deepEqual([n.tone, n.key, n.code], ['warn', 'stale', 'addon_account_gone'])
})

test('right now: no account named, one account is used, two or more need a pick', () => {
  const only = addonNow(row(credentialed([WORK]), { addons: ['cred'] }))
  assert.deepEqual([only.tone, only.key, only.vars.names], ['ok', 'only', 'Work'])
  const pick = addonNow(row(credentialed([WORK, HOME]), { addons: ['cred'] }))
  assert.deepEqual([pick.tone, pick.key, pick.code], ['warn', 'pick', 'addon_account_missing'])
})

const scoped = (over = {}) => ({ id: 'res', tools: 3, tool_list: tools, resources: { noun: 'thing', noun_plural: 'things' }, ...over })

test('right now: an add-on with resources per agent and an empty list can use none', () => {
  const n = addonNow(row(scoped(), { addons: ['res'] }))
  assert.deepEqual([n.tone, n.key, n.code], ['warn', 'resourcesNone', 'agent_resources_none'])
  assert.equal(n.vars.noun, 'thing')
})

test('right now: resources that are all read only cannot run a tool that changes data', () => {
  const form = { addons: ['res'], resources: { res: [{ id: 'r1', mode: 'read' }, { id: 'r2', mode: 'read' }] } }
  const n = addonNow(row(scoped(), form))
  assert.deepEqual([n.tone, n.key, n.code, n.vars.n], ['ok', 'resourcesRead', 'agent_resources_read_only', 2])
  const mixed = addonNow(row(scoped(), { ...form, resources: { res: [{ id: 'r1', mode: 'write' }, { id: 'r2', mode: 'read' }] } }), { total: 4 })
  assert.deepEqual([mixed.key, mixed.vars.w, mixed.code], ['resources', 1, undefined])
  // No tool changes data: read only is not a limit worth a code.
  const readers = scoped({ tool_list: tools.filter((t) => t.kind === 'read') })
  assert.equal(addonNow(row(readers, form)).key, 'resources')
})

test('right now: an add-on without a tool list still counts its tools', () => {
  const n = addonNow(row({ id: 'plain', tools: 4 }, { addons: ['plain'] }))
  assert.equal(n.vars.split, false)
  assert.equal(n.vars.count, 4)
})

const skillRows = [
  { layer: 'addon', addon: 'a', name: 'pdf', addon_title: 'Alpha' },
  { layer: 'addon', addon: 'a', name: 'xlsx', addon_title: 'Alpha' },
  { layer: 'addon', addon: 'b', name: 'beta-skill', addon_title: 'Beta' },
  { layer: 'builtin', name: 'writing' },
  { layer: 'builtin', name: 'memory', provider: 'memory', locked: true },
  { layer: 'addon', addon: 'a', name: 'gone-skill', gone: true },
]

test('add-on skills: only the rows of that add-on, not granted ones and not gone ones', () => {
  assert.deepEqual(addonSkillRows(skillRows, 'a').map((s) => s.name), ['pdf', 'xlsx'])
  assert.deepEqual(addonSkillRows(skillRows, 'none'), [])
})

test('add-on skills: switching on turns on the skills that come with it and names what changed', () => {
  const r = addonSwitch({ addons: [], skills: ['writing', 'pdf'] }, 'a', true, skillRows)
  assert.deepEqual(r.addons, ['a'])
  assert.deepEqual(r.skills, ['writing', 'pdf', 'xlsx'])
  assert.deepEqual(r.turned, ['xlsx'])
})

test('add-on skills: switching off turns off only the skills that came with it', () => {
  const form = { addons: ['a', 'b'], skills: ['writing', 'pdf', 'xlsx', 'beta-skill', 'mine'] }
  const r = addonSwitch(form, 'a', false, skillRows)
  assert.deepEqual(r.addons, ['b'])
  assert.deepEqual(r.skills, ['writing', 'beta-skill', 'mine'])
  // An add-on that is not running has no rows: nothing can be derived, nothing is removed.
  assert.deepEqual(addonSwitch({ addons: ['z'], skills: ['x'] }, 'z', false, skillRows).skills, ['x'])
})

test('add-on skills: ticking an account on an add-on that is off switches it on, with its skills', () => {
  const r = accountsAfter({ addons: [], skills: [], connections: {} }, 'a', ['b2'], skillRows)
  assert.deepEqual(r.connections, { a: ['b2'] })
  assert.deepEqual(r.addons, ['a'])
  assert.deepEqual(r.skills, ['pdf', 'xlsx'])
  assert.equal(r.switchedOn, true)
})

test('add-on skills: ticking on an add-on that is on, or unticking the last account, switches nothing', () => {
  const on = accountsAfter({ addons: ['a'], skills: [], connections: {} }, 'a', ['b2'], skillRows)
  assert.equal(on.switchedOn, false)
  assert.equal(on.addons, undefined)
  const off = accountsAfter({ addons: [], skills: [], connections: { a: ['b2'] } }, 'a', [], skillRows)
  assert.deepEqual(off.connections, {})
  assert.equal(off.switchedOn, false)
})

test('add-on skills: a ticked skill of an add-on that is off is dead, the others are live', () => {
  const st = addonSkillState({ addons: ['a'], skills: ['pdf', 'beta-skill', 'writing'] }, skillRows)
  assert.deepEqual(st.live.map((s) => s.name), ['pdf'])
  assert.deepEqual(st.dead.map((s) => s.name), ['beta-skill'])
})

test('memory skills: the ones the host grants are listed apart and are not switches', () => {
  assert.deepEqual(grantedSkills(skillRows).map((s) => s.name), ['memory'])
  assert.ok(!switchableSkills(skillRows).some((s) => s.name === 'memory'))
  assert.equal(switchableSkills(skillRows).length, skillRows.length - 1)
})

test('memory skills: the line is on while the agent reads notes', () => {
  assert.equal(memorySkillsState({ memory: 'mine_and_own' }, { enabled: true }), 'on')
  assert.equal(memorySkillsState({ memory: '' }, null), 'on')
  assert.equal(memorySkillsState({ memory: 'none' }, { enabled: true }), 'off')
  assert.equal(memorySkillsState({ memory: 'own' }, { enabled: false }), 'offUser')
})

test('resources: ticking adds an entry at read, the mode changes it, unticking the last removes the add-on', () => {
  let form = { resources: {} }
  form = { resources: resourcesAfter(form, 'res', 'r1', 'read') }
  form = { resources: resourcesAfter(form, 'res', 'r2', 'read') }
  form = { resources: resourcesAfter(form, 'res', 'r1', 'write') }
  assert.deepEqual(pickedResources(form, 'res'), [{ id: 'r1', mode: 'write' }, { id: 'r2', mode: 'read' }])
  form = { resources: resourcesAfter(form, 'res', 'r1', null) }
  form = { resources: resourcesAfter(form, 'res', 'r2', null) }
  assert.deepEqual(form.resources, {})
  assert.deepEqual(pickedResources({}, 'res'), [])
})

test('resources: the payload keeps valid modes, one entry per ID and no empty list', () => {
  assert.deepEqual(resourcesPayload({ res: [{ id: 'r1', mode: 'write' }, { id: 'r1', mode: 'read' }, { id: ' ', mode: 'read' }, { id: 'r2', mode: 'x' }], none: [] }),
    { res: [{ id: 'r1', mode: 'write' }, { id: 'r2', mode: 'read' }] })
})

test('resources: a save sends the field once the form has it, {} clears, and a form without it sends nothing', () => {
  assert.equal('resources' in agentPayload({ ...emptyAgent(), name: 'a' }), false)
  assert.deepEqual(agentPayload({ ...emptyAgent(), name: 'a', resources: {} }).resources, {})
  assert.deepEqual(agentPayload({ ...emptyAgent(), name: 'a', resources: { res: [{ id: 'r1', mode: 'read' }] } }).resources, { res: [{ id: 'r1', mode: 'read' }] })
})

test('agent add-on rows carry what the sentence needs: stored accounts and the resource picks', () => {
  const r = row(credentialed([WORK, HOME], { resources: { noun: 'x' } }), { addons: ['cred'], connections: { cred: ['b2', 'gone'] }, resources: { cred: [{ id: 'r1', mode: 'read' }] } })
  assert.deepEqual(r.enabled_connections, ['b2'])
  assert.equal(r.stored_connections, 2)
  assert.deepEqual(r.agent_resources, [{ id: 'r1', mode: 'read' }])
})

// The Effective view.
const effRows = flatRows([{ key: 'addons', rows: [{ id: 'cred', label: 'Cred', on: true, usable: true }, { id: 'res', label: 'Res', on: true, usable: true }] }])
const eff = (form, addons, extra = {}) => effectiveLines({ form: { ...emptyAgent(), ...form }, rows: effRows, skills: [], memory: null, access: [], isEdit: true, addons: addons.map((a) => row(a, form)), skillRows: [], ...extra })
const line = (lines, id) => lines.find((l) => l.id === id)

test('effective: an add-on that is on and has no account is one blocked line with a code and a fix, not a tool', () => {
  const lines = eff({ addons: ['cred'] }, [credentialed([WORK, HOME])])
  const l = line(lines, 'addon-cred')
  assert.deepEqual([l.state, l.code, l.text], ['blocked', 'addon_account_missing', 'addons.pick'])
  assert.deepEqual(l.fix, { kind: 'section', section: 'capabilities' })
  // Only the other add-on row counts as a tool: this one cannot work.
  assert.equal(line(lines, 'tools').vars.count, 1)
})

test('effective: the account an add-on uses, and its resources, are named', () => {
  const form = { addons: ['cred', 'res'], connections: { cred: ['b1'] }, resources: { res: [{ id: 'r1', mode: 'write' }, { id: 'r2', mode: 'read' }] } }
  const lines = eff(form, [credentialed([WORK, HOME]), scoped()])
  assert.equal(line(lines, 'addon-account-cred').vars.names, 'Work')
  const res = line(lines, 'addon-resources-res')
  assert.deepEqual([res.state, res.vars.n, res.vars.w], ['allowed', 2, 1])
  assert.equal(line(lines, 'tools').vars.count, 2)
})

test('effective: resources with no entry are blocked, all read only is a note with its code', () => {
  assert.equal(line(eff({ addons: ['res'] }, [scoped()]), 'addon-res').code, 'agent_resources_none')
  const ro = line(eff({ addons: ['res'], resources: { res: [{ id: 'r1', mode: 'read' }] } }, [scoped()]), 'addon-resources-res')
  assert.deepEqual([ro.state, ro.code], ['info', 'agent_resources_read_only'])
})

test('effective: an add-on that is off adds no line, and a skill of one that is off is blocked', () => {
  const rows = [{ layer: 'addon', addon: 'cred', name: 'cred-skill', addon_title: 'Cred' }]
  const lines = eff({ addons: [], skills: ['cred-skill'] }, [credentialed([WORK])], { skillRows: rows })
  assert.equal(lines.some((l) => l.id.startsWith('addon-')), false)
  const dead = lines.find((l) => l.code === 'skill_addon_off')
  assert.deepEqual([dead.state, dead.vars.addon], ['blocked', 'Cred'])
  const live = eff({ addons: ['cred'], connections: { cred: ['b1'] }, skills: ['cred-skill'] }, [credentialed([WORK])], { skillRows: rows })
  assert.equal(line(live, 'addon-skills').vars.count, 1)
})

// A bare checkbox is the browser's own and looks unstyled next to the app's.
// The editor and its parts use the kit's checkbox (class dk-check).
test('the agent editor draws no bare checkbox', async () => {
  const { readFileSync, readdirSync } = await import('node:fs')
  const { join, dirname } = await import('node:path')
  const { fileURLToPath } = await import('node:url')
  const src = join(dirname(fileURLToPath(import.meta.url)), '../../src')
  const files = [
    'pages/AgentCreate.jsx', 'components/chat/SwitchMenu.jsx', 'components/chat/SkillsMenu.jsx', 'components/chat/SwitchList.jsx',
    ...readdirSync(join(src, 'components/agents')).filter((f) => f.endsWith('.jsx')).map((f) => `components/agents/${f}`),
  ]
  const bare = []
  for (const f of files) {
    const text = readFileSync(join(src, f), 'utf8')
    for (const m of text.matchAll(/<input\b[^>]*?type="checkbox"[^>]*?(\/>|>)/gs)) {
      if (!/className="[^"]*\bdk-check\b/.test(m[0])) bare.push(f)
    }
  }
  assert.deepEqual(bare, [])
})

// MCP servers: one list over what the host defines.
const conns = [{ id: 'c1', name: 'Docs', off: false }, { id: 'c2', name: 'Mail', off: true, hint: 'needs sign-in' }, { id: 'gone', name: 'gone', gone: true, off: false }]

test('mcp rows: a defined connector is a row with its switch and status, a stored one that is not defined is unknown', () => {
  const rows = mcpRows({ connectors: conns, servers: [], stored: [], checkable: false })
  assert.deepEqual(rows.map((r) => [r.name, r.kind, r.on]), [['Docs', 'connector', true], ['Mail', 'connector', false], ['gone', 'unknown', true]])
  assert.equal(rows[1].hint, 'needs sign-in')
  assert.equal(rows[0].ref, 'connector:c1')
  assert.deepEqual(mcpUnknown(rows), ['gone'])
})

test('mcp rows: the servers of the model are rows, on when stored; a stored name the model lacks is unknown only when it can be checked', () => {
  const checked = mcpRows({ connectors: [], servers: ['crm', 'mail'], stored: ['crm', 'typo'], checkable: true })
  assert.deepEqual(checked.map((r) => [r.name, r.kind, r.on]), [['crm', 'server', true], ['mail', 'server', false], ['typo', 'unknown', true]])
  // With no fixed model the name cannot be told from one the model has: kept, marked unverified, not amber.
  const free = mcpRows({ connectors: [], servers: [], stored: ['typo'], checkable: false })
  assert.deepEqual([free[0].kind, free[0].unverified], ['server', true])
  assert.deepEqual(mcpUnknown(free), [])
})

test('mcp rows: the search keeps the rows whose name contains the text, ignoring case', () => {
  const rows = mcpRows({ connectors: conns, servers: ['crm'], stored: [], checkable: true })
  assert.deepEqual(filterMcp(rows, 'DOC').map((r) => r.name), ['Docs'])
  assert.deepEqual(filterMcp(rows, '  ').length, rows.length)
  assert.deepEqual(filterMcp(rows, 'zzz'), [])
})

test('effective: an MCP name that nothing defined matches is blocked as ignored, with the code and the fix', () => {
  const lines = effectiveLines({ form: emptyAgent(), rows: [], skills: [], memory: null, access: [], isEdit: true, mcpUnknown: ['gone'] })
  const l = lines.find((x) => x.id === 'mcp-gone')
  assert.deepEqual([l.state, l.code, l.text, l.vars.name], ['blocked', 'mcp_not_defined', 'mcp.ignored', 'gone'])
  assert.deepEqual(l.fix, { kind: 'section', section: 'capabilities' })
})
