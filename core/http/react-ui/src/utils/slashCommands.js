// The slash commands of the composer, on Home and in a chat: one registry.
// Pure functions, tested in tests/unit/slashcommands.test.mjs.
//
// A command is a plain object:
//
//   id      unique; the menu row has the test id home-slash-<id>
//   cmd     what the person types, with the slash ('/model')
//   icon    an icon of the kit; group: the menu group it is listed under
//   kind    what choosing it does:
//             'navigate'  the page does something now: onRunAction(id)
//             'insert'    text goes in the box (insert), the person writes on
//             'server'    the page asks the server: onRunAction(id)
//   where   the surfaces that offer it: ['chat'], ['home'] or both (default)
//   needs   (ctx) => boolean, when the command is offered (default: always)
//   insert  the text an 'insert' command puts in the box
//   label, desc, badge
//           set on the commands made from data (a skill); the others take
//           their words from the page's translations by id
//   args    the argument provider, optional: (query, ctx) => items, or a
//           Promise of items. When the box holds "/cmd <query>", the menu
//           lists the items the provider returns for the query (filterArgs
//           does the usual name filter). An item:
//             { key, name, desc, icon, badge, note, insert }
//           Choosing an item with insert puts that text in the box; one
//           without calls onRunAction(id, item).
//
// To add a command (a later /goal or /loop): add an object to
// SHARED_COMMANDS (or to the surface's list), its words under slash.<id> in
// the chat namespace of every locale, and a case in the page's onRunAction
// for 'navigate' and 'server' kinds. A command that takes arguments gives an
// args provider; "/loop stop <id>" is a provider that lists the loops.

import { CHAT_SLASH_ACTIONS, CHAT_SLASH_GROUPS } from '../components/chat/chatActions.js'
import { SLASH_ACTIONS, SLASH_GROUPS, availableActions } from '../components/home/homeActions.js'

// The groups of the new commands, after the page's own.
export const COMMAND_GROUPS = ['context', 'skills', 'help']

// The commands both surfaces share. ctx is described at availableCommands.
export const SHARED_COMMANDS = [
  { id: 'models', cmd: '/models', icon: 'cube', group: 'chat', kind: 'navigate', needs: (c) => c.hasModels },
  {
    id: 'skill', cmd: '/skill', icon: 'book', group: 'skills', kind: 'insert', insert: '/skill ',
    needs: (c) => (c.skills || []).length > 0,
    args: (query, ctx) => filterArgs(skillItems(ctx), query),
  },
  {
    id: 'agent', cmd: '/agent', icon: 'robot', group: 'skills', kind: 'insert', insert: '/agent ',
    needs: (c) => !!c.canAgents && (c.agents || []).length > 0,
    args: (query, ctx) => (ctx.savedAgentsOn
      ? filterArgs(agentItems(ctx), query)
      : [{ key: 'enable-agents', name: '', icon: 'toggle-on', action: 'enableAgents' }]),
  },
  // /goal shows the goal, /goal <text> sets it, /goal clear and /goal resume
  // act on it. The page reads what was typed with parseGoalCommand.
  {
    id: 'goal', cmd: '/goal', icon: 'target', group: 'context', kind: 'navigate', where: ['chat'], needs: (c) => c.canGoal !== false,
    args: (query, ctx) => filterArgs(goalItems(ctx), query),
  },
  { id: 'compact', cmd: '/compact', icon: 'minimize', group: 'context', kind: 'server', where: ['chat'], needs: (c) => !!(c.hasContext ?? c.hasThread) && !c.running },
  { id: 'clear', cmd: '/clear', icon: 'eraser', group: 'context', kind: 'server', where: ['chat'], needs: (c) => !!(c.hasContext ?? c.hasThread) && !c.running },
  {
    id: 'attach', cmd: '/attach', icon: 'paperclip', group: 'context', kind: 'navigate', needs: (c) => !!c.canAttach,
    args: (query, ctx) => ((ctx.staged || 0) > 0 ? filterArgs([{ key: 'clear', name: 'clear', icon: 'trash', action: 'clearFiles' }], query) : []),
  },
  { id: 'help', cmd: '/help', icon: 'help-circle', group: 'help', kind: 'navigate' },
  { id: 'about', cmd: '/about', icon: 'info', group: 'help', kind: 'navigate', where: ['chat'], needs: (c) => !!c.hasChat },
]

// The arguments of /goal that act on the goal the chat has: resume a goal
// that waits, clear any goal. Anything else typed after /goal is a new goal.
function goalItems(ctx) {
  const state = ctx.goalState || ''
  const items = []
  if (state === 'paused' || state === 'blocked') items.push({ key: 'goal-resume', name: 'resume', icon: 'play', action: 'goalResume' })
  if (state) items.push({ key: 'goal-clear', name: 'clear', icon: 'close', action: 'goalClear' })
  return items
}

// What a message typed as /goal asks, or null when it is not one:
//   { action: 'show' }               /goal
//   { action: 'clear' | 'resume' }   /goal clear, /goal resume
//   { action: 'set', text }          /goal <anything else>
export function parseGoalCommand(text) {
  const m = /^\/goal(?:\s+([\s\S]*))?$/i.exec(String(text || '').trim())
  if (!m) return null
  const rest = (m[1] || '').trim()
  if (!rest) return { action: 'show' }
  const word = rest.toLowerCase()
  if (word === 'clear' || word === 'resume') return { action: word }
  return { action: 'set', text: rest }
}

// The source of a skill, for its badge: built-in, add-on, shared or yours.
export function skillBadge(s) {
  if (s.layer === 'builtin') return 'builtin'
  if (s.layer === 'addon') return 'addon'
  if (s.layer === 'shared') return 'shared'
  return 'yours'
}

// The names a skill may not take as a command of its own: every command of
// the registry, on any surface.
export function reservedNames() {
  const names = new Set()
  for (const a of [...CHAT_SLASH_ACTIONS, ...SLASH_ACTIONS, ...SHARED_COMMANDS]) names.add(a.cmd.slice(1))
  return names
}

const RESERVED = reservedNames()

// The skills a chat can run now: switched on and not replaced.
export function usableSkills(skills) {
  return (skills || []).filter((s) => s && s.name && s.enabled !== false && !s.shadowed && !s.invalid)
}

// The items of /skill: every usable skill. One whose name is a command is
// reached only through /skill, and says so (note 'collides').
function skillItems(ctx) {
  return usableSkills(ctx.skills).map((s) => ({
    key: `skill-${s.name}`, name: s.name, desc: s.description || '', icon: 'book', badge: skillBadge(s),
    note: RESERVED.has(s.name) ? 'collides' : undefined, insert: `/skill ${s.name} `,
  }))
}

function agentItems(ctx) {
  return (ctx.agents || []).map((a) => ({
    key: `agent-${a.id}`, name: a.name, desc: a.description || '', icon: 'robot', insert: `/agent ${a.name} `,
  }))
}

// A command per usable skill, /<name>. A skill whose name is a command gets
// /skill <name> instead, marked.
export function skillCommands(skills) {
  return usableSkills(skills).map((s) => {
    const collides = RESERVED.has(s.name)
    const cmd = collides ? `/skill ${s.name}` : `/${s.name}`
    return {
      id: `skill-${s.name}`, cmd, icon: 'book', group: 'skills', kind: 'insert', insert: `${cmd} `,
      label: s.name, desc: s.description || '', badge: skillBadge(s), note: collides ? 'collides' : undefined,
    }
  })
}

// The page's own commands as registry commands: navigation, on one surface.
function pageCommands(actions, surface) {
  return actions.map((a) => ({ ...a, kind: 'navigate', where: [surface] }))
}

// The commands a surface offers now.
//
//   surface  'chat' or 'home'
//   ctx      what the page knows:
//     chat:  { assistant, hasModels, hasThread, hasChat, running, hasContext
//              (the model sees a message: none since the last clear is
//              false; omitted, hasThread decides) }
//     home:  the context of homeActions.availableActions
//     both:  { skills (the chat's or the draft's), agents, canAgents,
//              savedAgentsOn, canAttach, staged (files in the box) }
export function availableCommands(surface, ctx = {}) {
  let own
  if (surface === 'home') {
    own = pageCommands(availableActions(ctx), 'home')
  } else {
    own = pageCommands(CHAT_SLASH_ACTIONS, 'chat').filter((a) => {
      if (a.needs === 'assistant') return !!ctx.assistant
      if (a.needs === 'models') return !!ctx.hasModels
      if (a.needs === 'thread') return !!ctx.hasThread
      if (a.needs === 'chat') return !!ctx.hasChat
      return true
    })
  }
  const shared = SHARED_COMMANDS.filter((a) => (!a.where || a.where.includes(surface)) && (!a.needs || a.needs(ctx)))
  return [...own, ...shared, ...skillCommands(ctx.skills)]
}

// The groups of a surface, in menu order.
export function commandGroups(surface) {
  return [...(surface === 'home' ? SLASH_GROUPS : CHAT_SLASH_GROUPS), ...COMMAND_GROUPS]
}

// What the box asks of the menu.
//   null                              no menu: not a command
//   { mode: 'commands', query }       "/que": the commands that match
//   { mode: 'args', command, query }  "/cmd que": the arguments of cmd
// query is lowercase. Only a command that has an args provider takes the
// second form; "/etc/hosts how do I" is a message.
export function slashQuery(text, commands) {
  if (typeof text !== 'string' || !text.startsWith('/')) return null
  const space = text.search(/\s/)
  if (space < 0) return { mode: 'commands', query: text.slice(1).toLowerCase() }
  if (text[space] !== ' ') return null
  const name = text.slice(1, space).toLowerCase()
  const command = (commands || []).find((c) => c.args && c.cmd === `/${name}`)
  if (!command) return null
  return { mode: 'args', command, query: text.slice(space + 1).toLowerCase() }
}

// The commands whose name starts with the query or whose label holds it, in
// group order.
export function filterCommands(commands, query, labelOf, groups) {
  const q = (query || '').trim()
  const matched = q
    ? commands.filter((a) => a.cmd.slice(1).toLowerCase().startsWith(q) || String(labelOf(a) || '').toLowerCase().includes(q))
    : commands
  return groups.flatMap((g) => matched.filter((a) => a.group === g))
}

// The arguments whose name holds the query; those that start with it come
// first. An item without a name (an action such as "turn on") always stays.
// An argument the person typed in full is done: the menu closes.
export function filterArgs(items, query) {
  const q = (query || '').toLowerCase()
  if (!q) return items
  const named = (it) => String(it.name || '').toLowerCase()
  if (items.some((it) => it.name && q.startsWith(`${named(it)} `))) return []
  const hits = items.filter((it) => !it.name || named(it).includes(q))
  return [...hits.filter((it) => named(it).startsWith(q)), ...hits.filter((it) => !named(it).startsWith(q))]
}

// The longest agent whose name or id starts rest, followed by a space or the end.
function matchAgent(rest, agents) {
  const lower = rest.toLowerCase()
  const keys = []
  for (const a of agents || []) {
    for (const k of [a.name, a.id]) if (k) keys.push([String(k), a])
  }
  keys.sort((x, y) => y[0].length - x[0].length)
  for (const [k, a] of keys) {
    const kl = k.toLowerCase()
    if (lower === kl || lower.startsWith(`${kl} `)) return { agent: a, task: rest.slice(k.length).trim() }
  }
  return null
}

// What a message typed as a command sends.
//   null                                 a plain message
//   { skill, text }                      the message runs with the skill
//   { agent: {id, name}, text }          the task goes to the saved agent
//   { error: 'skill' | 'agent' | 'agentTask' | 'agentsOff', name }
// /skill <name> and /<name> both reach a skill; a skill named like a command
// only through /skill.
export function parseInvocation(text, { skills = [], agents = [], savedAgentsOn = true } = {}) {
  const t = String(text || '').trim()
  const m = /^\/(\S+)(?:\s+([\s\S]*))?$/.exec(t)
  if (!m) return null
  const name = m[1].toLowerCase()
  const rest = (m[2] || '').trim()
  const usable = usableSkills(skills)
  if (name === 'skill') {
    const [first] = rest.split(/\s+/)
    if (!first) return { error: 'skill', name: '' }
    const s = usable.find((x) => x.name === first)
    if (!s) return { error: 'skill', name: first }
    return { skill: s.name, text: rest.slice(first.length).trim() }
  }
  if (name === 'agent') {
    if (!savedAgentsOn) return { error: 'agentsOff', name: '' }
    const hit = matchAgent(rest, agents)
    if (!hit) return { error: 'agent', name: rest.split(/\s+/)[0] || '' }
    if (!hit.task) return { error: 'agentTask', name: hit.agent.name }
    return { agent: { id: hit.agent.id, name: hit.agent.name }, text: hit.task }
  }
  if (RESERVED.has(name)) return null
  const s = usable.find((x) => x.name === m[1])
  if (!s) return null
  return { skill: s.name, text: rest }
}

// Whether the model sees a message of the transcript: one of the person or an
// answer after the last "context cleared" line.
export function contextSinceClear(messages) {
  const list = messages || []
  for (let i = list.length - 1; i >= 0; i--) {
    const r = list[i].role
    if (r === 'cleared') return false
    if (r === 'user' || r === 'assistant') return true
  }
  return false
}
