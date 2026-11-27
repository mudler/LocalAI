// Pure helpers of the plugins of a chat: the Skills menu groups, the
// per-chat plugin switch, the / commands and the plugin connectors. Tested
// in tests/unit/chatplugins.test.mjs.
//
// A plugin contributes to a chat when it is installed for everyone or for
// the chat's user, that user turned it on, the chat did not switch it off
// (plugins_off, install IDs) and, in a chat with a saved agent, the agent
// grants it (an exact list: an agent with no list grants none). The server
// decides; these helpers only draw what it would do.

export const PLUGIN_LAYER = 'plugin'

// The installs a chat can switch: on for the person, not waiting for a
// re-enable, and granted by the chat's agent when it has one.
export function chatPluginRows(installs, { off = [], agent = null } = {}) {
  const offSet = new Set(off || [])
  const grant = agent ? new Set(Array.isArray(agent.plugins) ? agent.plugins : []) : null
  return (installs || [])
    .filter((i) => i && i.enabled && !i.needsReenable && (!grant || grant.has(i.id)))
    .map((i) => ({ id: i.id, plugin: i.plugin, name: i.name || pluginName(i.plugin), digest: i.digest, off: offSet.has(i.id) }))
    .sort((a, b) => a.name.localeCompare(b.name))
}

// "review@acme" -> "review".
export function pluginName(pluginId) {
  const s = String(pluginId || '')
  const at = s.lastIndexOf('@')
  return at > 0 ? s.slice(0, at) : s
}

// The stored list after one switch: install IDs that are off, sorted.
export function withPluginOff(off, id, on) {
  const set = new Set(off || [])
  if (on) set.delete(id)
  else set.add(id)
  return [...set].sort()
}

// The skills of the Skills menu split in two: the plain ones, and one group
// per plugin with its skills. A plugin switched off in the chat contributes
// no skill, so its group is drawn from the plugin rows and stays empty.
export function skillGroups(skills, plugins = []) {
  const plain = []
  const byInstall = new Map()
  for (const p of plugins) byInstall.set(p.id, { ...p, skills: [] })
  for (const s of skills || []) {
    if (s.layer !== PLUGIN_LAYER) { plain.push(s); continue }
    const id = s.plugin_install || s.plugin || ''
    if (!byInstall.has(id)) byInstall.set(id, { id, plugin: s.plugin || '', name: s.plugin_title || pluginName(s.plugin), off: false, skills: [] })
    byInstall.get(id).skills.push(s)
  }
  return { plain, groups: [...byInstall.values()].sort((a, b) => a.name.localeCompare(b.name)) }
}

// The commands of the plugins that are on in a chat, from the models of
// their packages (GET /api/plugins/installs/{id}). models: { [installId]:
// plugins.Model }. A command is named <plugin>:<command>.
export function pluginCommandList(plugins, models) {
  const out = []
  for (const p of plugins || []) {
    if (p.off) continue
    const m = models?.[p.id]
    if (!m) continue
    for (const c of m.commands || []) {
      out.push({ name: `${m.name || p.name}:${c.name}`, description: c.description || '', hint: c.argumentHint || '', install: p.id, plugin: p.plugin })
    }
  }
  return out
}

// The plugin skills a person may start with /: on, not replaced, and not
// kept out of the menu by the plugin.
export function slashPluginSkills(skills) {
  return (skills || []).filter((s) => s && s.layer === PLUGIN_LAYER && s.enabled !== false && !s.no_menu && !s.shadowed && !s.invalid)
}

// What "/<plugin>:<name> args" asks, or null when the text is not one: the
// skill or command name and the arguments. A plugin skill wins over a
// command of the same name, as the server does.
export function parsePluginInvocation(text, { skills = [], commands = [] } = {}) {
  const m = /^\/(\S+:\S+)(?:\s+([\s\S]*))?$/.exec(String(text || '').trim())
  if (!m) return null
  const name = m[1]
  const args = (m[2] || '').trim()
  if (slashPluginSkills(skills).some((s) => s.name === name)) return { skill: name, text: args, kind: 'skill' }
  if ((commands || []).some((c) => c.name === name)) return { skill: name, text: args, kind: 'command' }
  return null
}

// The plugin connectors of the Tools list grouped by their plugin, for one
// switch per plugin: [{ id, name, off, connectors: [names] }].
export function pluginConnectorRows(plugins, connectors) {
  const by = new Map()
  for (const c of connectors || []) {
    if (!c || !c.plugin_install) continue
    if (!by.has(c.plugin_install)) by.set(c.plugin_install, [])
    by.get(c.plugin_install).push(c.name)
  }
  return (plugins || []).filter((p) => by.has(p.id)).map((p) => ({ id: p.id, name: p.name, off: p.off, connectors: by.get(p.id) }))
}

// The plugin name of each install, for a connector's "Plugin: x" mark.
export function pluginNames(installs) {
  return Object.fromEntries((installs || []).map((i) => [i.id, i.name || pluginName(i.plugin)]))
}

// The create request's switches with the plugins switched off in the draft.
export function withPluginDraft(switches, plugins) {
  if (!plugins || !(plugins.plugins_off || []).length) return switches
  return { ...(switches || {}), plugins_off: plugins.plugins_off }
}
