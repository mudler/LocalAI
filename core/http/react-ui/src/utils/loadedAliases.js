// The server lists a loaded model under its real name. A model config that is
// an alias (`alias: <real-model>`) is never in that list, yet a message to it
// runs on the loaded target at once. This widens the set of loaded ids with
// every listed alias whose target is loaded, so "is this model warm" is one
// lookup for a real model and for an alias alike.
//
// `models` is the capabilities list (each entry has `id` and, for an alias,
// `alias_of`). An alias that does not resolve has no `alias_of` and stays cold.
export function withLoadedAliases(loadedIds, models) {
  const ids = loadedIds instanceof Set ? loadedIds : new Set()
  if (!Array.isArray(models) || ids.size === 0) return ids
  let widened = null
  for (const m of models) {
    if (m?.alias_of && !m.alias_error && ids.has(m.alias_of) && !ids.has(m.id)) {
      if (!widened) widened = new Set(ids)
      widened.add(m.id)
    }
  }
  return widened || ids
}
