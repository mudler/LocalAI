import { useTranslation } from 'react-i18next'
import { connectorRef } from '../utils/chatMCP'
import { unknownTextKey } from '../utils/chatCommands'

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
const BUILTIN_ORDER = [SHELL, 'files', 'web', 'media', 'collections', SUBAGENTS, SAVED_AGENTS, 'triggers', 'changes']
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

  const builtinRow = (b) => ({
    key: `b-${b.group}`, id: b.group, optIn: !!b.opt_in,
    label: tc(`tools.builtin.${b.group}`, { defaultValue: b.title || b.group }),
    description: tc(`tools.describe.${b.group}`, { defaultValue: b.description || '' }),
    whenOff: tc(`tools.whenOff.${b.group}`, { defaultValue: b.when_off || '' }),
    title: tc('tools.builtinBy', { tools: (b.tools || []).join(', ') }),
    on: !b.off, usable: b.usable, reason: b.reason, onToggle: (on) => switches.toggleBuiltin(b.group, on),
    ...(b.group === SHELL ? shellNote(b) : builtinNote(b)),
  })

  // The builtin grants of a saved agent: what the agent may ask to change
  // (memory, its profile, skills, pages...). Each row says in plain words what
  // the right allows, and a sensitive one says so. No onSet: Select all must
  // never grant a right. The agent form edits them; a chat shows what its
  // agent holds, read only.
  const g = switches.grants
  const grantsGroup = {
    key: 'grants', title: tc('tools.grants'), icon: 'shield', rows: (g?.list || []).map((x) => ({
      key: `g-${x.name}`, id: x.name,
      label: tc(`tools.grant.${x.name.replace(/\./g, '_')}.title`, { defaultValue: x.title || x.name }),
      description: x.gone ? '' : tc(`tools.grant.${x.name.replace(/\./g, '_')}.description`, { defaultValue: x.description || '' }),
      note: x.gone ? tc('tools.gone') : (x.sensitive ? tc('tools.grantSensitive') : ''),
      warn: !!x.sensitive && !x.gone,
      on: x.on, readOnly: !g.editable, onToggle: (on) => g.onToggle?.(x.name, on),
    })),
  }

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
    if (forAgent) {
      return {
        pick: {
          label: tc('tools.connection.agentLabel'),
          hint: none ? tc('tools.connection.none') : (chosen.length ? tc('tools.connection.agentHelp') : tc('tools.connection.perChatHelp')),
          values: chosen,
          options,
          disabled: !switches.setConnections,
          onChange: (ids) => switches.setConnections?.(a.id, ids),
          link: none ? { to: '/app/chats/connectors', label: tc('tools.connection.create') } : undefined,
        },
      }
    }
    return {
      usable: !a.needs_connection,
      reason: a.needs_connection ? (none ? tc('tools.connection.none') : tc('tools.connection.choose')) : undefined,
      pick: {
        label: tc('tools.connection.label'),
        hint: byAgent ? tc('tools.connection.byAgent') : (sole ? tc('tools.connection.sole') : (chosen.length > 1 ? tc('tools.connection.helpMany') : tc('tools.connection.help'))),
        values: chosen,
        options,
        disabled: byAgent || sole || !switches.setConnections,
        onChange: (ids) => switches.setConnections?.(a.id, ids),
        link: none ? { to: '/app/chats/connectors', label: tc('tools.connection.create') } : undefined,
      },
    }
  }

  const groups = [
    {
      // The tools LocalAI serves itself, such as web search: one switch per group.
      key: 'builtins', title: tc('tools.builtins'), icon: 'globe', onSet: switches.setBuiltinsOn,
      rows: [...switches.builtins].sort((a, b) => builtinRank(a.group) - builtinRank(b.group)).map(builtinRow),
    },
    grantsGroup,
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
  ]
  return groups
}
