import test from 'node:test'
import assert from 'node:assert/strict'
import {
  availableCommands, commandGroups, filterArgs, filterCommands, parseInvocation, reservedNames, skillCommands, slashQuery, SHARED_COMMANDS,
} from '../../src/utils/slashCommands.js'
import { CHAT_SLASH_ACTIONS } from '../../src/components/chat/chatActions.js'
import { SLASH_ACTIONS } from '../../src/components/home/homeActions.js'

const skills = [
  { id: 'personal:review', name: 'review', description: 'check a change', layer: 'personal', enabled: true },
  { id: 'builtin:pdf', name: 'pdf', description: 'read and write PDFs', layer: 'builtin', enabled: true },
  { id: 'addon:office:slides', name: 'slides', description: 'make decks', layer: 'addon', enabled: true },
  { id: 'shared:new', name: 'new', description: 'a skill named like a command', layer: 'shared', enabled: true },
  { id: 'personal:off', name: 'sleepy', description: 'switched off', layer: 'personal', enabled: false },
]
const agents = [
  { id: 'rev1', name: 'Reviewer', description: 'checks drafts' },
  { id: 'rev2', name: 'Reviewer Bot', description: 'checks code' },
]
const chatCtx = { hasModels: true, hasThread: true, hasChat: true, skills, agents, canAgents: true, savedAgentsOn: true, canAttach: true }
const ids = (list) => list.map((c) => c.id)
const label = (c) => c.label || c.id

test('slash commands: the chat keeps every command it had and adds the shared ones', () => {
  const got = ids(availableCommands('chat', { ...chatCtx, assistant: true }))
  for (const a of CHAT_SLASH_ACTIONS) assert.ok(got.includes(a.id), `${a.id} is kept`)
  for (const id of ['models', 'skill', 'agent', 'compact', 'clear', 'attach', 'help', 'about']) assert.ok(got.includes(id), `${id} is offered`)
  assert.equal(new Set(got).size, got.length, 'ids are unique')
})

test('slash commands: Home keeps its commands and has no context commands', () => {
  const got = ids(availableCommands('home', { isAdmin: true, hasModels: true, hasChats: true, skills, agents, canAgents: true, savedAgentsOn: true }))
  for (const id of ['model', 'new', 'chat', 'gallery', 'docs']) assert.ok(got.includes(id))
  for (const id of ['compact', 'clear', 'about']) assert.ok(!got.includes(id), `${id} needs a thread`)
  assert.ok(got.includes('skill') && got.includes('agent') && got.includes('help'))
})

test('slash commands: compact and clear need a thread and an idle chat', () => {
  const noThread = ids(availableCommands('chat', { ...chatCtx, hasThread: false }))
  assert.ok(!noThread.includes('compact') && !noThread.includes('clear'))
  const running = ids(availableCommands('chat', { ...chatCtx, running: true }))
  assert.ok(!running.includes('compact') && !running.includes('clear'))
})

test('slash commands: skill and agent are offered only when there is something to pick', () => {
  const none = ids(availableCommands('chat', { ...chatCtx, skills: [], agents: [] }))
  assert.ok(!none.includes('skill') && !none.includes('agent'))
  const noFeature = ids(availableCommands('chat', { ...chatCtx, canAgents: false }))
  assert.ok(!noFeature.includes('agent'))
})

test('slash commands: every usable skill is a command, and one named like a command goes through /skill', () => {
  const cmds = skillCommands(skills)
  assert.deepEqual(cmds.map((c) => c.cmd), ['/review', '/pdf', '/slides', '/skill new'])
  assert.deepEqual(cmds.map((c) => c.badge), ['yours', 'builtin', 'addon', 'shared'])
  assert.equal(cmds[3].note, 'collides')
  assert.equal(cmds[0].insert, '/review ')
  assert.ok(reservedNames().has('new') && reservedNames().has('compact'))
})

test('slash commands: the query of the box picks commands or arguments', () => {
  const cmds = availableCommands('chat', chatCtx)
  assert.deepEqual(slashQuery('/rev', cmds), { mode: 'commands', query: 'rev' })
  assert.equal(slashQuery('hello', cmds), null)
  assert.equal(slashQuery('/etc/hosts how do I', cmds), null, 'a path is a message')
  const q = slashQuery('/agent Rev', cmds)
  assert.equal(q.mode, 'args')
  assert.equal(q.command.id, 'agent')
  assert.equal(q.query, 'rev')
  assert.equal(slashQuery('/review the diff', cmds), null, 'a skill takes free text, not arguments')
  assert.equal(slashQuery('/skill\tx', cmds), null)
})

test('slash commands: filtering matches the name start or the label, in group order', () => {
  const cmds = availableCommands('chat', chatCtx)
  const groups = commandGroups('chat')
  assert.deepEqual(ids(filterCommands(cmds, 'com', label, groups)), ['compact'])
  const sk = filterCommands(cmds, 'sl', label, groups)
  assert.ok(ids(sk).includes('skill-slides'))
  assert.equal(filterCommands(cmds, '', label, groups).length, cmds.filter((c) => groups.includes(c.group)).length)
})

test('slash commands: the argument providers list, filter and complete', async () => {
  const ctx = chatCtx
  const agent = SHARED_COMMANDS.find((c) => c.id === 'agent')
  const all = await agent.args('', ctx)
  assert.deepEqual(all.map((i) => i.name), ['Reviewer', 'Reviewer Bot'])
  assert.equal(all[1].insert, '/agent Reviewer Bot ')
  assert.deepEqual((await agent.args('bot', ctx)).map((i) => i.name), ['Reviewer Bot'])
  assert.deepEqual(await agent.args('reviewer bot check this', ctx), [], 'a complete name closes the menu')
  const off = await agent.args('', { ...ctx, savedAgentsOn: false })
  assert.equal(off.length, 1)
  assert.equal(off[0].action, 'enableAgents')

  const skill = SHARED_COMMANDS.find((c) => c.id === 'skill')
  const listed = await skill.args('', ctx)
  assert.deepEqual(listed.map((i) => i.name), ['review', 'pdf', 'slides', 'new'], 'an off skill is not listed')
  assert.equal(listed.find((i) => i.name === 'new').note, 'collides')
  assert.deepEqual((await skill.args('p', ctx)).map((i) => i.name), ['pdf'])

  const attach = SHARED_COMMANDS.find((c) => c.id === 'attach')
  assert.deepEqual(await attach.args('', { staged: 0 }), [])
  assert.equal((await attach.args('cl', { staged: 2 }))[0].action, 'clearFiles')
})

test('slash commands: filterArgs puts the names that start with the query first and keeps nameless actions', () => {
  const items = [{ key: 'a', name: 'alpha beta' }, { key: 'b', name: 'beta' }, { key: 'c', name: '' }]
  assert.deepEqual(filterArgs(items, 'beta').map((i) => i.key), ['b', 'a', 'c'])
  assert.deepEqual(filterArgs(items, '').map((i) => i.key), ['a', 'b', 'c'])
})

test('slash commands: a message typed as a command carries a skill or an agent', () => {
  const opts = { skills, agents }
  assert.deepEqual(parseInvocation('/review look at the diff', opts), { skill: 'review', text: 'look at the diff' })
  assert.deepEqual(parseInvocation('/skill review look', opts), { skill: 'review', text: 'look' })
  assert.deepEqual(parseInvocation('/pdf', opts), { skill: 'pdf', text: '' })
  assert.deepEqual(parseInvocation('/skill new go', opts), { skill: 'new', text: 'go' }, 'a skill named like a command through /skill')
  assert.equal(parseInvocation('/new go', opts), null, '/new stays the command')
  assert.equal(parseInvocation('/sleepy go', opts), null, 'an off skill is a plain message')
  assert.deepEqual(parseInvocation('/skill sleepy', opts), { error: 'skill', name: 'sleepy' })
  assert.equal(parseInvocation('/etc/hosts how do I', opts), null)
  assert.equal(parseInvocation('hello', opts), null)
  assert.deepEqual(parseInvocation('/agent Reviewer Bot check the code', opts), { agent: { id: 'rev2', name: 'Reviewer Bot' }, text: 'check the code' })
  assert.deepEqual(parseInvocation('/agent reviewer check the draft', opts), { agent: { id: 'rev1', name: 'Reviewer' }, text: 'check the draft' })
  assert.deepEqual(parseInvocation('/agent rev1 go', opts), { agent: { id: 'rev1', name: 'Reviewer' }, text: 'go' })
  assert.deepEqual(parseInvocation('/agent Reviewer', opts), { error: 'agentTask', name: 'Reviewer' })
  assert.deepEqual(parseInvocation('/agent Nobody go', opts), { error: 'agent', name: 'Nobody' })
  assert.deepEqual(parseInvocation('/agent Reviewer go', { ...opts, savedAgentsOn: false }), { error: 'agentsOff', name: '' })
})

test('slash commands: every command has words in every locale, without internals', async () => {
  const { readdirSync, readFileSync } = await import('node:fs')
  const dir = new URL('../../public/locales/', import.meta.url)
  const keys = [...SHARED_COMMANDS.map((c) => c.id), 'group.context', 'group.skills', 'group.help']
  for (const locale of readdirSync(dir)) {
    const chat = JSON.parse(readFileSync(new URL(`${locale}/chat.json`, dir), 'utf8'))
    for (const k of keys) {
      const node = k.split('.').reduce((o, p) => (o ? o[p] : undefined), chat.slash)
      assert.ok(node, `${locale}: slash.${k}`)
      if (typeof node === 'object') {
        assert.ok(node.label && node.desc, `${locale}: slash.${k} has a label and a description`)
      }
    }
  }
  // The navigation commands keep their own words.
  assert.ok(SLASH_ACTIONS.length > 0)
})
