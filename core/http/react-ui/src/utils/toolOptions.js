// The options of the Tools list. A saved agent stores a capability twice: as a
// switch of a built-in group (builtins_off, builtins_on) and as a list of
// grants (builtins). The Tools list shows one switch per capability, and the
// grants that refine it sit under it as options. Nothing here changes what is
// stored. No React, so a node test drives it.

// The grant that is also an opt-in group. The switch is the group.
export const SAVED_AGENTS_GROUP = 'saved_agents'

// The grants nested under the switch of a built-in group, in the order shown.
export const GROUP_OPTIONS = {
  triggers: ['triggers.propose', 'triggers.run'],
  changes: ['memory.write', 'profile.write', 'skills.write', 'pages.write', 'dashboard.write'],
  // The grant to add a text to a collection sits under the Knowledge switch,
  // where the collections are.
  collections: ['knowledge.write'],
  files: ['files.upload', 'act.files'],
}

// The grants that have no switch to hang from. They sit in a section of their
// own, with a line that says what the section is.
export const GRANT_SECTIONS = [
  { key: 'messaging', icon: 'chat', names: ['talkback.channels', 'talkback.new_channel'] },
  { key: 'unattended', icon: 'clock', names: ['act.addons', 'act.send', 'act.spend'] },
]

// The grants a messaging connection also has to allow on its own permissions
// page (the capabilities of a binding). The act.* grants are the agent's alone.
export const BINDING_CAPS = new Set([
  'memory.write', 'profile.write', 'skills.write', 'pages.write', 'dashboard.write', 'knowledge.write',
  'files.upload', 'triggers.propose', 'triggers.run', 'talkback.channels', 'talkback.new_channel',
])

const list = (x) => (Array.isArray(x) ? x : [])

// The key of a grant in the locale files.
export const grantKey = (name) => name.replace(/\./g, '_')

// Where each grant of a list goes: under a group switch (byGroup), in a
// section (bySection), or nowhere known (other: a grant of a newer server, or
// one this server no longer declares). saved_agents is the switch itself and
// has no place. A grant whose group switch is not in `groups` goes to `other`,
// so it is never hidden.
export function placeGrants(grants, groups) {
  const byName = new Map(list(grants).map((g) => [g.name, g]))
  const present = new Set(list(groups))
  const used = new Set([SAVED_AGENTS_GROUP])
  const byGroup = {}
  for (const [group, names] of Object.entries(GROUP_OPTIONS)) {
    if (!present.has(group)) continue
    byGroup[group] = names.filter((n) => byName.has(n)).map((n) => byName.get(n))
    names.forEach((n) => used.add(n))
  }
  const bySection = {}
  for (const s of GRANT_SECTIONS) {
    bySection[s.key] = s.names.filter((n) => byName.has(n)).map((n) => byName.get(n))
    s.names.forEach((n) => used.add(n))
  }
  const other = list(grants).filter((g) => !used.has(g.name))
  // A grant whose group is not listed is not hidden.
  for (const [group, names] of Object.entries(GROUP_OPTIONS)) {
    if (present.has(group)) continue
    for (const n of names) if (byName.has(n)) other.push(byName.get(n))
  }
  return { byGroup, bySection, other }
}

// The saved-agents switch is on when the group is on or the agent holds the
// grant: the server turns the group on at save for an agent that holds it.
export function savedAgentsOn(form) {
  return list(form.builtins_on).includes(SAVED_AGENTS_GROUP) || list(form.builtins).includes(SAVED_AGENTS_GROUP)
}

// The messaging connections of an agent, as the Tools list names them: the
// bindings whose agent is this one, with the capabilities each one allows, the
// name of the kind and the page that edits them. kinds is the list the
// connectors call returns ({ kind, addon, title }).
export function agentConnections(bindings, kinds, agentId) {
  if (!agentId) return []
  const byKind = new Map(list(kinds).map((k) => [k.kind, k]))
  return list(bindings)
    .filter((b) => b.agent_id === agentId)
    .map((b) => {
      const k = byKind.get(b.kind)
      const title = k?.title || b.kind || ''
      return {
        id: b.id,
        name: [title, b.name].filter(Boolean).join(' '),
        caps: list(b.caps),
        // The page of the kind opens the binding and shows its permissions.
        to: k?.addon ? `/app/addons/${encodeURIComponent(k.addon)}/apps?binding=${encodeURIComponent(b.id)}` : '/app/chats/connectors',
      }
    })
}

// The connections that do not allow a grant yet. Only for a grant the agent
// holds and a connection has a permission for: nothing for the act.* grants.
export function connectionsMissing(name, connections) {
  if (!BINDING_CAPS.has(name)) return []
  return list(connections).filter((c) => !c.caps.includes(name))
}
