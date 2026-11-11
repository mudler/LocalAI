import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  RESERVED_COMMANDS, argValues, cleanCommands, commandNameOk, commandProblems, expandCommand, matchTyped, readCommands, splitTyped, templateArgs, withArgsFromTemplate,
} from '../../src/utils/userCommands.js'
import { availableCommands, commandGroups, expandTypedUserCommand, filterCommands, reservedNames, slashQuery, userCommandEntries } from '../../src/utils/slashCommands.js'
import { WELL_KNOWN_FILES } from '../../src/utils/wellKnown.js'
import { slotItemOk } from '../../src/utils/slots.js'

const here = dirname(fileURLToPath(import.meta.url))
const schema = JSON.parse(readFileSync(join(here, '../../../../services/studio/builtinskills/tree/localai-workspace/schemas/commands.schema.json'), 'utf8'))

const standup = {
  name: 'standup', description: 'Write my standup', type: 'prompt',
  template: 'Look at my chats from the last {{days}} day(s). Keep it under {{words}} words.',
  args: [{ name: 'days', default: 1 }, { name: 'words', default: 120 }],
}
const translate = { name: 'translate', description: 'Translate text', type: 'prompt', template: 'Translate this into French:' }
const summarize = { name: 'summarize', description: 'Summarize this chat', type: 'tool', tool: 'summarize_chat' }
const toolText = (tool) => `Use the ${tool} tool.`

test('commands: the path is well known and the built-in names are the ones the server and the registry have', () => {
  assert.equal(WELL_KNOWN_FILES.commands, 'LocalAI/commands.json')
  assert.deepEqual([...RESERVED_COMMANDS].sort(), [...schema['x-reserved-names']].sort())
  assert.deepEqual([...reservedNames()].sort(), [...RESERVED_COMMANDS].sort())
})

test('commands: a name is lowercase letters, digits and dashes, starting with a letter', () => {
  for (const ok of ['standup', 'a', 'my-cmd2']) assert.ok(commandNameOk(ok), ok)
  for (const bad of ['Standup', '/standup', '1abc', 'a b', '', 'x'.repeat(33), 'a_b']) assert.ok(!commandNameOk(bad), bad)
})

test('commands: the arguments of a template, in order, once each, with spaces allowed', () => {
  assert.deepEqual(templateArgs('a {{days}} b {{ words }} c {{days}}'), ['days', 'words'])
  assert.deepEqual(templateArgs('no arguments, {one brace}'), [])
})

test('commands: typed values fill the arguments in order and the last takes the rest', () => {
  assert.deepEqual(splitTyped('2 80', 2), ['2', '80'])
  assert.deepEqual(splitTyped('French please now', 1), ['French please now'])
  assert.deepEqual(splitTyped('a b c d', 2), ['a', 'b c d'])
  assert.deepEqual(splitTyped('', 2), [])
  assert.deepEqual(splitTyped('x', 0), [])
})

test('commands: a prompt command expands with its defaults and with typed values', () => {
  assert.equal(expandCommand(standup), 'Look at my chats from the last 1 day(s). Keep it under 120 words.')
  assert.equal(expandCommand(standup, '2'), 'Look at my chats from the last 2 day(s). Keep it under 120 words.')
  assert.equal(expandCommand(standup, '2 80'), 'Look at my chats from the last 2 day(s). Keep it under 80 words.')
  assert.deepEqual(argValues(standup, '2').map(v => [v.name, v.value, v.typed]), [['days', '2', true], ['words', '120', false]])
})

test('commands: a value is put in once and never read as a template again', () => {
  assert.equal(expandCommand(standup, '{{words}} 5'), 'Look at my chats from the last {{words}} day(s). Keep it under 5 words.')
})

test('commands: a command without arguments adds what was typed after its template', () => {
  assert.equal(expandCommand(translate), 'Translate this into French:')
  assert.equal(expandCommand(translate, 'good morning'), 'Translate this into French: good morning')
})

test('commands: a tool command only asks for the tool, in words the person sees', () => {
  assert.equal(expandCommand(summarize, '', { toolText }), 'Use the summarize_chat tool.')
  assert.equal(expandCommand(summarize, 'keep it short', { toolText }), 'Use the summarize_chat tool. keep it short')
})

test('commands: matchTyped finds the command a text starts with', () => {
  const list = readCommands({ commands: [standup, translate] })
  assert.equal(matchTyped('/standup 2', list).command.name, 'standup')
  assert.equal(matchTyped('/standup 2', list).rest, '2')
  assert.equal(matchTyped('  /translate  ', list).rest, '')
  assert.equal(matchTyped('/unknown', list), null)
  assert.equal(matchTyped('standup', list), null)
  assert.equal(matchTyped('/standupx', list), null)
})

test('commands: readCommands keeps well-formed commands in file order and drops the rest', () => {
  const got = readCommands({ commands: [standup, null, { name: 'Bad', description: 'x', type: 'prompt', template: 'x' }, { name: 'a', description: 'x', type: 'script' }, standup, summarize, { name: 'b', description: '', type: 'tool', tool: 't' }] })
  assert.deepEqual(got.map(c => c.name), ['standup', 'summarize'])
  assert.deepEqual(readCommands(undefined), [])
  assert.deepEqual(readCommands({ commands: 'x' }), [])
})

test('commands: the editor names each problem of a command', () => {
  const ok = { name: 'a1', description: 'x', type: 'prompt', template: 'Do {{d}}', args: [{ name: 'd', default: '1' }], tool: '' }
  assert.deepEqual(commandProblems(ok), [])
  const has = (c, code, others) => assert.ok(commandProblems(c, others).includes(code), `${code}: ${commandProblems(c, others)}`)
  has({ ...ok, name: '' }, 'nameEmpty')
  has({ ...ok, name: 'Bad' }, 'nameFormat')
  has({ ...ok, name: 'model' }, 'nameBuiltin')
  has({ ...ok, name: 'dup' }, 'nameTaken', [{ name: 'dup' }])
  has({ ...ok, description: ' ' }, 'descriptionEmpty')
  has({ ...ok, template: '' }, 'templateEmpty')
  has({ ...ok, args: [] }, 'argMissing')
  has({ ...ok, args: [{ name: 'd', default: '1' }, { name: 'x', default: '1' }] }, 'argUnused')
  has({ ...ok, args: [{ name: 'd', default: '' }] }, 'argDefault')
  has({ ...ok, args: [{ name: 'd', default: '1' }, { name: 'd', default: '2' }] }, 'argTwice')
  has({ ...ok, template: 'Do {{d}} {{' }, 'braces')
  has({ name: 'a1', description: 'x', type: 'tool', tool: '' }, 'toolEmpty')
  has({ name: 'a1', description: 'x', type: 'tool', tool: 'do it' }, 'toolFormat')
  assert.deepEqual(commandProblems({ name: 'a1', description: 'x', type: 'tool', tool: 'summarize_chat' }), [])
})

test('commands: the editor keeps the arguments in step with the template', () => {
  const row = withArgsFromTemplate({ ...standup, args: [{ name: 'words', default: 99 }], template: 'x {{days}} y {{words}}' })
  assert.deepEqual(row.args, [{ name: 'days', default: '' }, { name: 'words', default: 99 }])
  const gone = withArgsFromTemplate({ ...standup, template: 'only {{days}}' })
  assert.deepEqual(gone.args.map(a => a.name), ['days'], 'an argument the template no longer names is left out')
})

test('commands: a save shapes the rows with only the keys of the type', () => {
  const rows = [
    { name: ' standup ', description: ' d ', type: 'prompt', template: 'Do {{d}}', args: [{ name: 'd', default: 1 }], tool: 'ignored' },
    { name: 't', description: 'x', type: 'tool', tool: ' summarize_chat ', template: 'ignored', args: [{ name: 'q', default: 1 }] },
    { name: 'plain', description: 'x', type: 'prompt', template: 'x', args: [] },
  ]
  assert.deepEqual(cleanCommands(rows), [
    { name: 'standup', description: 'd', type: 'prompt', template: 'Do {{d}}', args: [{ name: 'd', default: 1 }] },
    { name: 't', description: 'x', type: 'tool', tool: 'summarize_chat' },
    { name: 'plain', description: 'x', type: 'prompt', template: 'x' },
  ])
})

const ctx = { hasModels: true, hasThread: true, hasChat: true, userCommands: readCommands({ commands: [standup, translate, summarize] }), toolText }

test('commands in the slash menu: yours come first, with their description, kind and text', () => {
  const list = availableCommands('chat', ctx)
  const mine = list.filter(c => c.group === 'yours')
  assert.deepEqual(mine.map(c => c.cmd), ['/standup', '/translate', '/summarize'])
  assert.deepEqual(list.slice(0, 3).map(c => c.id), ['user-standup', 'user-translate', 'user-summarize'])
  assert.equal(mine[0].desc, 'Write my standup')
  assert.deepEqual(mine.map(c => c.badge), ['prompt', 'prompt', 'tool'])
  assert.equal(mine[0].insert, 'Look at my chats from the last 1 day(s). Keep it under 120 words.')
  assert.equal(mine[2].insert, 'Use the summarize_chat tool.')
  assert.equal(commandGroups('chat')[0], 'yours')
  assert.equal(commandGroups('home')[0], 'yours')
  assert.equal(new Set(commandGroups('chat')).size, commandGroups('chat').length, 'no group twice')
})

test('commands in the slash menu: typing / lists them, and a prefix narrows them', () => {
  const list = availableCommands('chat', ctx)
  const labelOf = (c) => c.label || c.id
  const all = filterCommands(list, '', labelOf, commandGroups('chat'))
  assert.deepEqual(all.slice(0, 3).map(c => c.cmd), ['/standup', '/translate', '/summarize'])
  assert.deepEqual(filterCommands(list, 'stan', labelOf, commandGroups('chat')).map(c => c.cmd), ['/standup'])
})

test('commands in the slash menu: "/name 2" shows the filled prompt as the one row, and choosing it inserts it', () => {
  const list = availableCommands('chat', ctx)
  const sq = slashQuery('/standup 2', list)
  assert.equal(sq.mode, 'args')
  const [row, ...rest] = sq.command.args(sq.query, {})
  assert.equal(rest.length, 0)
  assert.equal(row.insert, 'Look at my chats from the last 2 day(s). Keep it under 120 words.')
  assert.equal(row.name, 'days = 2, words = 120')
  const t = slashQuery('/translate good morning', list)
  assert.equal(t.command.args(t.query, {})[0].insert, 'Translate this into French: good morning')
})

test('commands in the slash menu: a typed command is expanded in the box, never sent as typed', () => {
  const list = availableCommands('chat', ctx)
  assert.equal(expandTypedUserCommand('/standup 3', list, ctx), 'Look at my chats from the last 3 day(s). Keep it under 120 words.')
  assert.equal(expandTypedUserCommand('/summarize', list, ctx), 'Use the summarize_chat tool.')
  assert.equal(expandTypedUserCommand('/model gpt', list, ctx), null, 'a built-in command is not one of yours')
  assert.equal(expandTypedUserCommand('hello', list, ctx), null)
})

test('commands in the slash menu: a tool command whose tool the chat lacks says so; a known one does not', () => {
  const known = userCommandEntries(readCommands({ commands: [summarize] }), { toolNames: new Set(['summarize_chat']), toolText })
  assert.equal(known[0].note, undefined)
  const missing = userCommandEntries(readCommands({ commands: [summarize] }), { toolNames: new Set(['other']), toolText })
  assert.equal(missing[0].note, 'unavailable')
  const unknown = userCommandEntries(readCommands({ commands: [summarize] }), { toolNames: new Set(), toolText })
  assert.equal(unknown[0].note, undefined, 'no list of tools yet: no judgement')
})

test('commands in the slash menu: a skill with the name of a command of yours is reached through /skill', () => {
  const skills = [{ id: 'personal:standup', name: 'standup', description: 'skill', layer: 'personal', enabled: true }]
  const got = availableCommands('chat', { ...ctx, skills })
  const sk = got.find(c => c.id === 'skill-standup')
  assert.equal(sk.cmd, '/skill standup')
  assert.equal(got.find(c => c.id === 'user-standup').cmd, '/standup')
})

test('commands in the slash menu: without commands the menu is the one it was', () => {
  const none = availableCommands('chat', { hasModels: true, hasThread: true, hasChat: true })
  assert.ok(!none.some(c => c.group === 'yours'))
})

test('commands: a tool command and a slot tool item take the same tool names, in the schemas and the UI', () => {
  const ui = JSON.parse(readFileSync(join(here, '../../../../services/studio/builtinskills/tree/localai-workspace/schemas/ui.schema.json'), 'utf8'))
  const slotPatterns = new Set(Object.values(ui.properties.slots.properties).map((s) => s.items.properties.tool.pattern))
  assert.equal(slotPatterns.size, 1, 'one pattern for every slot')
  const [slotPattern] = slotPatterns
  assert.equal(schema.properties.commands.items.properties.tool.pattern, slotPattern, 'commands.json and ui.json agree')
  assert.equal(slotPattern, '^[A-Za-z0-9_.:-]{1,128}$')
  // An MCP tool name has a colon, and a long one fits.
  const named = (tool) => commandProblems({ name: 'a1', description: 'x', type: 'tool', tool })
  assert.deepEqual(named('github:create_issue'), [])
  assert.deepEqual(named('t'.repeat(128)), [])
  assert.deepEqual(named('t'.repeat(129)), ['toolFormat'])
  assert.ok(slotItemOk({ type: 'tool', tool: 'github:create_issue', label: 'Issue' }))
})
