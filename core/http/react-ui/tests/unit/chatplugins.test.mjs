import test from 'node:test'
import assert from 'node:assert/strict'
import {
  chatPluginRows, parsePluginInvocation, pluginCommandList, pluginConnectorRows, pluginName, pluginNames, skillGroups, slashPluginSkills,
  withPluginDraft, withPluginOff,
} from '../../src/utils/chatPlugins.js'
import { availableCommands, commandGroups, parseInvocation, pluginCommands } from '../../src/utils/slashCommands.js'

const installs = [
  { id: 'i1', plugin: 'review@acme', name: 'review', digest: 'd1', enabled: true },
  { id: 'i2', plugin: 'docs@acme', name: 'docs', digest: 'd2', enabled: false },
  { id: 'i3', plugin: 'lint@acme', name: 'lint', digest: 'd3', enabled: true, needsReenable: true },
  { id: 'i4', plugin: 'ops@acme', digest: 'd4', enabled: true },
]

const skills = [
  { id: 'personal:notes', name: 'notes', layer: 'personal', enabled: true },
  { id: 'plugin:review:check', name: 'review:check', layer: 'plugin', plugin: 'review@acme', plugin_title: 'review', plugin_install: 'i1', enabled: true, argument_hint: '<file>' },
  { id: 'plugin:review:hidden', name: 'review:hidden', layer: 'plugin', plugin: 'review@acme', plugin_install: 'i1', enabled: true, no_menu: true },
  { id: 'plugin:review:off', name: 'review:off', layer: 'plugin', plugin: 'review@acme', plugin_install: 'i1', enabled: false },
]

test('chat plugins: a chat switches the plugins that are on for the person and granted by its agent', () => {
  assert.deepEqual(chatPluginRows(installs).map((r) => r.id), ['i4', 'i1'])
  assert.deepEqual(chatPluginRows(installs, { off: ['i1'] }).find((r) => r.id === 'i1').off, true)
  assert.deepEqual(chatPluginRows(installs, { agent: { plugins: ['i1'] } }).map((r) => r.id), ['i1'])
  assert.deepEqual(chatPluginRows(installs, { agent: {} }), [])
  assert.equal(chatPluginRows(installs)[0].name, 'ops')
  assert.equal(pluginName('a@b@c'), 'a@b')
})

test('chat plugins: the stored off list stays sorted and unique; a draft carries it', () => {
  assert.deepEqual(withPluginOff(['b'], 'a', false), ['a', 'b'])
  assert.deepEqual(withPluginOff(['a', 'b'], 'a', true), ['b'])
  assert.deepEqual(withPluginOff(['a'], 'a', false), ['a'])
  assert.deepEqual(withPluginDraft({ skills_off: ['x'] }, { plugins_off: ['i1'] }), { skills_off: ['x'], plugins_off: ['i1'] })
  assert.deepEqual(withPluginDraft(null, { plugins_off: ['i1'] }), { plugins_off: ['i1'] })
  assert.equal(withPluginDraft(null, null), null)
})

test('chat plugins: the Skills menu groups plugin skills under their plugin, and keeps an off plugin', () => {
  const rows = chatPluginRows(installs, { off: ['i4'] })
  const { plain, groups } = skillGroups(skills, rows)
  assert.deepEqual(plain.map((s) => s.name), ['notes'])
  assert.deepEqual(groups.map((g) => [g.id, g.off, g.skills.length]), [['i4', true, 0], ['i1', false, 3]])
})

test('chat plugins: commands come from the packages of the plugins that are on', () => {
  const rows = chatPluginRows(installs, { off: ['i4'] })
  const models = { i1: { name: 'review', commands: [{ name: 'pr', description: 'Review a PR', argumentHint: '<number>' }] }, i4: { name: 'ops', commands: [{ name: 'deploy' }] } }
  assert.deepEqual(pluginCommandList(rows, models), [{ name: 'review:pr', description: 'Review a PR', hint: '<number>', install: 'i1', plugin: 'review@acme' }])
})

test('chat plugins: / lists plugin commands and menu skills with their argument hint', () => {
  const cmds = [{ name: 'review:pr', description: 'Review a PR', hint: '<number>' }, { name: 'review:check', description: 'same name' }]
  const list = pluginCommands(skills, cmds)
  assert.deepEqual(list.map((c) => [c.cmd, c.note, c.badge, c.group]), [
    ['/review:check', '<file>', 'plugin', 'plugins'], ['/review:pr', '<number>', 'command', 'plugins'],
  ])
  assert.deepEqual(slashPluginSkills(skills).map((s) => s.name), ['review:check'])
  const all = availableCommands('chat', { skills, pluginCommands: cmds, hasChat: true })
  assert.ok(all.some((c) => c.cmd === '/review:pr'))
  assert.ok(commandGroups('chat').includes('plugins'))
})

test('chat plugins: /plugin:cmd args sends the command as the skill with its arguments', () => {
  const cmds = [{ name: 'review:pr' }]
  assert.deepEqual(parseInvocation('/review:pr 42 please', { skills, pluginCommands: cmds }), { skill: 'review:pr', text: '42 please', kind: 'command' })
  assert.deepEqual(parseInvocation('/review:check a.go', { skills, pluginCommands: cmds }), { skill: 'review:check', text: 'a.go', kind: 'skill' })
  assert.equal(parseInvocation('/review:hidden', { skills, pluginCommands: cmds }), null)
  assert.equal(parseInvocation('/review:off', { skills, pluginCommands: cmds }), null)
  assert.equal(parseInvocation('/nope:x', { skills, pluginCommands: cmds }), null)
  assert.equal(parsePluginInvocation('/notes hi', { skills }), null)
})

test('chat plugins: connectors group by their plugin; names come from the installs', () => {
  const rows = chatPluginRows(installs)
  const conns = [{ name: 'gh', plugin_install: 'i1' }, { name: 'mine' }, { name: 'gh2', plugin_install: 'i1' }]
  assert.deepEqual(pluginConnectorRows(rows, conns), [{ id: 'i1', name: 'review', off: false, connectors: ['gh', 'gh2'] }])
  assert.deepEqual(pluginNames(installs).i4, 'ops')
})
