// The scheduling page used to browse node labels in a card that stood open
// above the rules whether or not anyone was writing one. Labels are only ever
// needed while filling a rule's node selector, so discovery moved into that
// field: these helpers turn the node roster into what the field offers as the
// user types.
//
// The roster is already fetched for the page, so this costs no request.

// labelIndex reduces the node roster to the label vocabulary the cluster
// actually uses: every distinct key, and the values each key takes.
//
// Nodes carrying no labels are not an error, they simply contribute nothing.
export function labelIndex(nodes) {
  const values = {}
  for (const node of Array.isArray(nodes) ? nodes : []) {
    for (const [key, value] of Object.entries(node?.labels || {})) {
      const seen = values[key] || (values[key] = [])
      const text = String(value)
      if (!seen.includes(text)) seen.push(text)
    }
  }
  for (const key of Object.keys(values)) values[key].sort()
  return { keys: Object.keys(values).sort(), values }
}

// rank orders matches so what the user is most likely typing comes first: a
// prefix match beats a match buried in the middle of the string.
function rank(candidates, query) {
  const needle = query.trim().toLowerCase()
  if (!needle) return candidates
  return candidates
    .filter(candidate => candidate.toLowerCase().includes(needle))
    .sort((a, b) => {
      const ap = a.toLowerCase().startsWith(needle)
      const bp = b.toLowerCase().startsWith(needle)
      if (ap !== bp) return ap ? -1 : 1
      return a.localeCompare(b)
    })
}

// suggestKeys offers the label keys matching what has been typed, minus the
// ones this selector already carries: re-adding a key would silently overwrite
// the pair the user just built.
export function suggestKeys(index, query, exclude = []) {
  return rank(index.keys.filter(key => !exclude.includes(key)), query)
}

// suggestValues offers only the values the key being filled actually takes, so
// a selector cannot be built out of a pair no node in the cluster matches.
export function suggestValues(index, key, query) {
  return rank(index.values[key] || [], query)
}
