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
