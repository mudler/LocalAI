// Pure helpers of the per-chat skills picker. The server keeps a list of the
// skills that are OFF for a chat, so a skill added later starts on, also after
// "Disable all". Every change is one PUT of the full list.

// The id the server gives a skill; an older server sends the name only.
export const skillKey = (s) => s.id || s.name

// Below this many skills the picker has no search field.
export const SEARCH_MIN = 9

// The skills whose name or description contains the query, ignoring case.
export function filterSkills(skills, query) {
  const q = (query || '').trim().toLowerCase()
  if (!q) return skills
  return skills.filter((s) => `${s.name} ${s.description || ''}`.toLowerCase().includes(q))
}

// How many skills are on, and how many there are.
export function skillCounts(skills) {
  return { on: skills.filter((s) => s.enabled).length, total: skills.length }
}

// The list the server stores: the ids of the skills that are off, sorted.
export function offList(skills) {
  return skills.filter((s) => !s.enabled).map(skillKey).sort()
}

// The skills with the given ids (a Set) switched on or off; the rest as they were.
export function withSkillsSet(skills, ids, on) {
  return skills.map((s) => (ids.has(skillKey(s)) && s.enabled !== on ? { ...s, enabled: on } : s))
}

// The ids in `ids` whose state changes if they are set to `on`.
export function changedIds(skills, ids, on) {
  return skills.filter((s) => ids.has(skillKey(s)) && s.enabled !== on).map(skillKey)
}

// Puts back only the given ids to the state they had in `before`, so a failed
// request does not undo a change made while it was running.
export function revertSkills(current, before, ids) {
  const was = new Map(before.map((s) => [skillKey(s), s.enabled]))
  return current.map((s) => (ids.has(skillKey(s)) && was.has(skillKey(s)) ? { ...s, enabled: was.get(skillKey(s)) } : s))
}

// The skills the picker shows: the ones that win (switchable), and the
// built-in skills an add-on replaces, which it shows dimmed with no switch.
export function pickerSkills(list) {
  const all = Array.isArray(list) ? list : []
  return {
    skills: all.filter((s) => !s.shadowed && !s.invalid),
    replaced: all.filter((s) => !s.invalid && s.layer === 'builtin' && s.shadowed === 'addon' && s.shadowed_by),
  }
}

// What names the owner of a skill besides its layer and name: the add-on of an
// add-on skill, the saved agent of an agent skill. It is the last argument of
// chatSkillsApi.get and remove.
export function skillRef(s) {
  if (s.layer === 'addon' && s.addon) return { addon: s.addon }
  if (s.layer === 'agent' && s.agent) return { agent: s.agent }
  return {}
}

// Where a skill comes from, for the label of its row: the add-on's title for
// an add-on skill, else its layer.
export function skillSource(s) {
  if (s.layer === 'addon') return { kind: 'addon', name: s.addon_title || s.addon || '' }
  if (s.layer === 'agent') return { kind: 'agent', name: s.agent_name || s.agent || '' }
  return { kind: s.layer || '' }
}

// The built-in skills that serve because the add-on skill that replaces them
// is off or not running in the chat, and the add-ons, for one line.
export function fallbackNotice(skills) {
  const on = (skills || []).filter((s) => s.layer === 'builtin' && s.fallback && s.enabled !== false)
  if (on.length === 0) return null
  return { skills: on.map((s) => s.name), addons: [...new Set(on.map((s) => s.fallback))] }
}

// These stable names are provider slots; none has a core implementation.
export function skillDeclarationLabel(skill) {
  if (!skill.replaces) return null
  return ['docx', 'pptx', 'pdf', 'xlsx'].includes(skill.name) ? 'provides' : 'replaces'
}

// Only document slots lack a core fallback; other shadows still replace one.
export function skillInstallNoticeKey({ skill, name }) {
  const provides = skillDeclarationLabel({ name: skill, replaces: true }) === 'provides'
  return `flow.${provides ? 'skillProvides' : 'skillShadow'}${name ? 'Warning' : ''}`
}
