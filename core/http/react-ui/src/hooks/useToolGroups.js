import { useTranslation } from 'react-i18next'
import { connectorRef } from '../utils/chatMCP'
import { unknownTextKey } from '../utils/chatCommands'
import { GRANT_SECTIONS, connectionsMissing, grantKey, placeGrants } from '../utils/toolOptions'
import { reconnectConnections } from '../utils/connectorOAuth'

// The groups of the Tools list, built from the lists of the switches
// (useChatSwitches for a chat, useAgentTools for a saved agent). The Tools
// menu of the composer and the Tools fold of the agent form both draw these
// groups with SwitchMenu/SwitchPanel, so a switch has the same name, the same
// description and the same state line on both.
//
// Every row has the same three parts:
//   label        what the switch is (the title)
//   description  what the model can do when the switch is on, and, once it is
//                off, what it loses (whenOff)
//   note / chip  the state: who serves the tools, whether commands are
//                isolated, why the switch cannot be used now
// The words come from the server (the title and description each built-in
// group and each add-on declares); a translation by group ID, when the locale
// has one, wins.

// The order of the built-in groups in the list: what a chat does with the
// person's machine and files first. A group the server adds later goes last,
// in the server's order.
export const SAVED_AGENTS = 'saved_agents'
export const SHELL = 'shell'
export const SUBAGENTS = 'subagents'
export const MEMORY = 'memory'
export const LOCALAI_CLIENT = 'localai_client'
export const LOCALAI_ADMIN = 'localai_admin'
const BUILTIN_ORDER = [SHELL, 'files', 'web', 'media', LOCALAI_CLIENT, 'collections', MEMORY, SUBAGENTS, SAVED_AGENTS, 'triggers', 'changes', LOCALAI_ADMIN]
export const builtinRank = (g) => {
  const i = BUILTIN_ORDER.indexOf(g)
  return i < 0 ? BUILTIN_ORDER.length : i
}

// switches: { builtins, addons, servers, connectors, commands, set*On, toggle* }
// options.commandState: draw the state of the commands (who runs them, whether
//   they are isolated) on the shell row. The agent form has no chat to ask.
// options.forAgent: the rows are the agent's. The connection choice then names
//   the connection the agent's chats use, and may stay empty (the person picks
//   in each chat).
export function useToolGroups(switches, { commandsInfo = null, commandState = true, forAgent = false } = {}) {
  const { t } = useTranslation('chat')
  const { t: tc } = useTranslation('chats')

  // Who serves a group of built-in tools: the built-in itself, or an add-on
  // that shadows a tool of the group (the server resolves it).
  const builtinNote = (b) => {
    const by = b.providers || {}
    const shadowed = Object.entries(by).filter(([, p]) => p && p !== 'builtin')
    if (shadowed.length === 0) return {}
    const provider = (p) => (p === 'builtin' ? tc('tools.servedByBuiltin') : p)
    const mixed = b.provider === 'mixed'
    const note = mixed
      ? Object.entries(by).map(([tool, p]) => tc('tools.servedByTool', { tool, provider: provider(p) })).join(' · ')
      : tc('tools.servedBy', { provider: b.provider })
    // One provider fits a chip next to the name; one per tool is a line under it.
    return { [mixed ? 'note' : 'chip']: note, title: tc('tools.builtinByAddon', { tools: (b.tools || []).join(', '), provider: mixed ? note : b.provider }) }
  }

  // Who runs the commands of the shell group, as a short chip, and on the
  // line under it whether they are isolated (a warning when they are not). When nothing can run
  // them, the row says why in the notice's words.
  const shellNote = (b) => {
    if (b.usable === false) return commandsInfo ? { reason: tc(`tools.commands.${commandsInfo.textKey}`, { reason: commandsInfo.reason }) } : {}
    const by = (b.providers || {}).bash || b.provider
    const selected = switches.commands || {}
    const chip = by === 'builtin' ? tc('tools.shell.machine') : by ? tc('tools.servedBy', { provider: by }) : undefined
    if (!commandState) return { chip }
    // Group discovery is not evidence of the selected provider's isolation.
    if (!by || selected.provider !== by) return { chip, note: tc('tools.commands.unknown') }
    if (['host', 'host-off', 'host-unavailable', 'provider-host'].includes(selected.state)) return { chip, note: tc('tools.commands.providerHost'), warn: true }
    if (selected.state === 'available') return { chip, note: tc('tools.shell.isolated') }
    return { chip, note: tc(`tools.commands.${unknownTextKey(selected.code)}`) }
  }

  // The memory row: while the person's own switch is off the row cannot be
  // used and says where to turn it on; a chat that switched it off is a
  // private chat.
  const memoryNote = (b) => {
    if (b.usable !== false) return b.off ? { chip: tc('memoryOff.chipPrivate') } : {}
    return { reason: tc('memoryOff.userRow') }
  }

  const builtinRow = (b) => ({
    key: `b-${b.group}`, id: b.group, optIn: !!b.opt_in,
    label: tc(`tools.builtin.${b.group}`, { defaultValue: b.title || b.group }),
    description: tc(`tools.describe.${b.group}`, { defaultValue: b.description || '' }),
    whenOff: tc(`tools.whenOff.${b.group}`, { defaultValue: b.when_off || '' }),
    title: tc('tools.builtinBy', { tools: (b.tools || []).join(', ') }),
    on: !b.off, usable: b.usable, reason: b.reason, onToggle: (on) => switches.toggleBuiltin(b.group, on),
    ...(b.group === SHELL ? shellNote(b) : b.group === MEMORY ? memoryNote(b) : builtinNote(b)),
    // The tools of the group are found with tool_search instead of being sent in full.
    ...(b.deferred ? { chip: tc('tools.onDemand') } : {}),
  })
  // A built-in row with the options that refine it.
  const withOptions = (row) => {
    const own = placed.byGroup[row.id]
    if (!own || own.length === 0) return row
    const parent = { on: row.on && row.usable !== false, label: row.label }
    return { ...row, options: own.map((x) => grantRow(x, parent)) }
  }

  // The grants of a saved agent (what it may change, reach or do on its own).
  // Each one is shown once. A grant that refines a built-in switch sits under
  // that switch as an option; the rest go in a section of their own. A grant
  // is greyed, with the reason, while the switch above it is off: its stored
  // value stays as it is, so it comes back as it was. The agent form edits
  // them; a chat shows what its agent holds, read only. No onSet anywhere:
  // Select all must never grant a right.
  const g = switches.grants
  const connections = switches.connections || []
  const builtinList = switches.builtins || []
  const placed = placeGrants(g?.list, builtinList.map((b) => b.group))
  const grantRow = (x, parent) => {
    const k = grantKey(x.name)
    const blocked = !!parent && !parent.on
    const missing = x.on && !x.gone && !blocked ? connectionsMissing(x.name, connections) : []
    return {
      key: `g-${x.name}`, id: x.name,
      label: x.gone ? x.name : tc(`tools.grant.${k}.title`, { defaultValue: x.title || x.name }),
      description: x.gone ? '' : tc(`tools.grant.${k}.description`, { defaultValue: x.description || '' }),
      note: x.gone ? tc('tools.gone') : (x.sensitive ? tc('tools.grantSensitive') : ''),
      warn: !!x.sensitive && !x.gone,
      blocked, reason: blocked ? tc('tools.optionNeeds', { name: parent.label }) : '',
      notes: missing.map((c) => ({ key: c.id, text: tc('tools.connectionMissing', { name: c.name }), link: { to: c.to, label: tc('tools.connectionOpen') } })),
      on: x.on, readOnly: !g.editable, onToggle: (on) => g.onToggle?.(x.name, on),
    }
  }
  const sectionGroup = (s) => ({
    key: s.key, title: tc(`tools.section.${s.key}.title`), icon: s.icon, intro: tc(`tools.section.${s.key}.intro`),
    rows: (placed.bySection[s.key] || []).map((x) => grantRow(x)),
  })
  const grantGroups = [
    ...GRANT_SECTIONS.map(sectionGroup),
    { key: 'grants', title: tc('tools.grants'), icon: 'shield', rows: placed.other.map((x) => grantRow(x)) },
  ]

  // The connections an add-on that runs with a credential may use. The host
  // picks none for the person: with one it is enabled, with several the person
  // enables the ones this chat may use, and until then the tools are not
  // offered and the row says so. With two or more enabled the assistant names
  // the connection in each action. An agent that names a set decides it; the
  // chat shows it read only.
  const connectionOf = (a) => {
    if (!a.credentialed || a.gone) return {}
    const list = a.connections || []
    const byAgent = a.connection_by === 'agent'
    const none = list.length === 0
    const chosen = a.enabled_connections || []
    const options = list.map((c) => ({ value: c.id, label: c.name }))
    // The only connection of the person is always enabled: there is nothing to choose.
    const sole = list.length === 1 && a.connection_by === 'only'
    // The agent form draws this choice in its Connections section.
    if (forAgent && switches.pickElsewhere) return {}
    // A connection that logs in with OAuth and whose login ended: the row
    // says which, and links to the Connectors page to connect it again.
    const stale = reconnectConnections(a).filter((c) => forAgent || chosen.includes(c.id) || a.connection_state === 'reconnect_needed')
    // The login of a connection that was shared with the person is its
    // owner's: the row says that the owner must connect it again and offers
    // no login to the person.
    const mine = stale.filter((c) => !c.shared)
    const theirs = stale.filter((c) => c.shared)
    const staleNotes = [
      mine.length ? tc('tools.connection.reconnectNeeded', { names: mine.map((c) => c.name).join(', ') }) : '',
      theirs.length ? tc('tools.connection.ownerReconnectNeeded', { names: theirs.map((c) => c.name).join(', ') }) : '',
    ].filter(Boolean)
    const reconnect = stale.length ? { warn: true, note: staleNotes.join(' ') } : {}
    const connectorsLink = (fallback) => (mine.length
      ? { to: '/app/chats/connectors', label: tc('tools.connection.reconnect') }
      : fallback)
    if (forAgent) {
      return {
        ...reconnect,
        pick: {
          label: tc('tools.connection.agentLabel'),
          hint: none ? tc('tools.connection.none') : (chosen.length ? tc('tools.connection.agentHelp') : tc('tools.connection.perChatHelp')),
          values: chosen,
          options,
          disabled: !switches.setConnections,
          onChange: (ids) => switches.setConnections?.(a.id, ids),
          link: connectorsLink(none ? { to: '/app/chats/connectors', label: tc('tools.connection.create') } : undefined),
        },
      }
    }
    return {
      ...reconnect,
      usable: !a.needs_connection,
      reason: a.needs_connection ? (none ? tc('tools.connection.none') : tc('tools.connection.choose')) : undefined,
      pick: {
        label: tc('tools.connection.label'),
        hint: byAgent ? tc('tools.connection.byAgent') : (sole ? tc('tools.connection.sole') : (chosen.length > 1 ? tc('tools.connection.helpMany') : tc('tools.connection.help'))),
        values: chosen,
        options,
        disabled: byAgent || sole || !switches.setConnections,
        onChange: (ids) => switches.setConnections?.(a.id, ids),
        link: connectorsLink(none ? { to: '/app/chats/connectors', label: tc('tools.connection.create') } : undefined),
      },
    }
  }

  const groups = [
    {
      // The tools LocalAI serves itself, such as web search: one switch per group.
      key: 'builtins', title: tc('tools.builtins'), icon: 'globe', onSet: switches.setBuiltinsOn,
      rows: [...builtinList].sort((a, b) => builtinRank(a.group) - builtinRank(b.group)).map((b) => withOptions(builtinRow(b))),
    },
    {
      // An add-on shows the title and the description its manifest declares.
      key: 'addons', title: tc('tools.addons'), icon: 'puzzle', onSet: switches.setAddonsOn,
      rows: switches.addons.map((a) => ({
        key: `a-${a.id}`, id: a.id, label: a.title || a.id,
        description: a.gone ? '' : (a.description || tc('tools.addonDescribe', { count: a.tools || 0 })),
        note: a.gone ? tc('tools.gone') : '',
        meta: a.gone ? undefined : t('tools.toolCount', { count: a.tools }), on: !a.off, onToggle: (on) => switches.toggleAddon(a.id, on),
        ...connectionOf(a),
      })),
    },
    {
      key: 'servers', title: tc('tools.servers'), icon: 'server', onSet: switches.setMCPOn,
      rows: switches.servers.map((s) => ({
        key: `s-${s.name}`, id: s.name, label: s.name, description: tc('tools.serverDescribe'),
        on: !s.off, usable: s.usable, reason: s.reason, onToggle: (on) => switches.toggleMCP(s.name, on),
      })),
    },
    {
      key: 'connectors', title: tc('tools.connectors'), icon: 'plug', onSet: switches.setMCPOn,
      rows: switches.connectors.map((c) => ({
        key: `c-${c.id}`, id: connectorRef(c.id), label: c.name, description: tc('tools.connectorDescribe'),
        note: c.gone ? tc('tools.gone') : (c.hint || ''), on: !c.off, usable: c.usable, reason: c.reason, onToggle: (on) => switches.toggleMCP(connectorRef(c.id), on),
      })),
    },
    ...grantGroups,
  ]
  return groups
}
