// What the runs of a trigger may do with nobody present: the act.* names of a
// trigger. A run starts read only; each name adds one class of action, within
// the grants of the trigger's agent. These functions are the rules the editor,
// the details page and the proposal card share, so they say the same thing.
// The server applies the same rules at each tool call. No React, so a node test
// drives them.

const list = (x) => (Array.isArray(x) ? x : [])

export const ACT_FILES = 'act.files'
export const ACT_ADDONS = 'act.addons'

/** True for the name of an act: act.files, act.addons, act.addons:<id>, act.send, act.spend. */
export const isActGrant = (name) => typeof name === 'string' && name.startsWith('act.')

/** The act.* entries of the builtin grants the server declares. */
export function actCatalog(grants) {
  return list(grants).filter((g) => isActGrant(g?.name))
}

/**
 * Whether an agent's grants cover an act: the name itself, or act.addons for
 * act.addons:<id>. Without an agent (null) nothing but the trigger's own list
 * limits it.
 */
export function withinAgent(name, agentGrants) {
  if (agentGrants == null) return true
  const held = list(agentGrants)
  return held.includes(name) || (name.startsWith(`${ACT_ADDONS}:`) && held.includes(ACT_ADDONS))
}

/**
 * The rows of the permission list: every act the server declares, on when the
 * trigger holds it, and blocked when the agent does not hold it (a blocked
 * row can be switched off but not on). An act the trigger holds that the
 * server does not list, such as act.addons:<id>, stays as an extra row so it
 * can be taken away.
 */
export function actRows(catalog, acts, agentGrants) {
  const on = new Set(list(acts))
  const known = new Set(list(catalog).map((g) => g.name))
  const rows = list(catalog).map((g) => ({
    name: g.name, title: g.title, description: g.description, sensitive: !!g.sensitive,
    on: on.has(g.name), blocked: !withinAgent(g.name, agentGrants), extra: false,
  }))
  for (const name of list(acts)) {
    if (known.has(name)) continue
    rows.push({ name, title: name, description: '', sensitive: true, on: true, blocked: !withinAgent(name, agentGrants), extra: true })
  }
  return rows
}

/** The acts after one is switched on or off. */
export function actsAfter(acts, name, on) {
  const rest = list(acts).filter((x) => x !== name)
  return on ? [...rest, name] : rest
}

/**
 * What the runs of a trigger can do, from the fields of the form:
 * { readOnly, why, held, unused }. held are the acts that apply; unused the
 * ones the trigger lists but its agent does not hold. why is declared or
 * binding (set by how the trigger was made, and nothing ticked changes it),
 * agent (it lists acts its agent holds none of) or no_acts. A trigger whose
 * plan names a file it writes also holds act.files, as the server does.
 */
export function actsStatus({ acts, agentGrants = null, writes = '', lock = '' }) {
  const listed = list(acts)
  if (lock) return { readOnly: true, why: lock, held: [], unused: listed }
  const held = listed.filter((n) => withinAgent(n, agentGrants))
  if (writes && !held.includes(ACT_FILES) && withinAgent(ACT_FILES, agentGrants)) held.push(ACT_FILES)
  const unused = listed.filter((n) => !held.includes(n))
  if (held.length === 0) return { readOnly: true, why: listed.length > 0 ? 'agent' : 'no_acts', held, unused }
  return { readOnly: false, why: '', held, unused }
}

/** The same answer from a trigger as the server returns it (acts, acts_held, read_only, read_only_why). */
export function actsStatusOf(trigger) {
  const acts = list(trigger?.acts)
  const held = list(trigger?.acts_held)
  return {
    readOnly: !!trigger?.read_only, why: trigger?.read_only_why || '', held,
    unused: trigger?.read_only ? acts : acts.filter((n) => !held.includes(n)),
  }
}

/** The rows of a list of act names, with the title and weight the server declares. */
export function actNamed(catalog, names) {
  const byName = new Map(list(catalog).map((g) => [g.name, g]))
  return list(names).map((name) => {
    const g = byName.get(name)
    return g
      ? { name, title: g.title, description: g.description, sensitive: !!g.sensitive }
      : { name, title: name, description: '', sensitive: name.startsWith(`${ACT_ADDONS}:`) }
  })
}
