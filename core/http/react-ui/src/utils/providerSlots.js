// The rules of the provider picker, apart from the screen. A provider slot is a
// job the host does itself and lets an add-on do in its place; the server lists
// the providers of a slot (GET /api/chats/providers) and the choice in force.

// The kinds of a provider, as the server names them.
export const PROVIDER_BUILTIN = 'builtin'
export const PROVIDER_OFF = 'off'

// The reason codes of a slot that is off.
export const PROVIDER_UNAVAILABLE = 'provider_unavailable'

// The slot with this id in the answer of the list, or null.
export function slotOf(list, id) {
  return list?.slots?.find((s) => s.id === id) || null
}

// null while the slot works, or the add-on that was picked and cannot serve.
// The slot is off then, and nothing has moved to the built-in provider.
export function providerFailure(slot) {
  const r = slot?.resolution
  if (!r || !r.off || r.code !== PROVIDER_UNAVAILABLE) return null
  return { id: r.addon_id || r.selected, title: r.addon_title || r.addon_id || r.selected }
}

// The facts to show under a provider: where its data goes and whether a change
// can be undone, from what the add-on declared. A built-in provider and Off
// declare nothing and show nothing. Each is a translation key suffix.
export function providerFacts(provider) {
  if (!provider || provider.kind !== 'addon') return []
  const props = provider.properties || {}
  const out = [props.remote === true ? 'remote' : 'local']
  if (props.undo === 'host') out.push('undoHost')
  else if (props.undo === 'provider') out.push('undoProvider')
  else if (props.undo === 'none') out.push('undoNone')
  return out
}

// The id to store when a provider is picked, or null when the pick changes
// nothing or the provider cannot be picked (an add-on that is not available).
export function pickable(slot, id) {
  const p = slot?.providers?.find((x) => x.id === id)
  if (!p || !p.available || slot.resolution?.selected === id) return null
  return id
}

// The add-on that serves the slot now when it can forget what it keeps, as
// { id, title, undo }, or null. It needs a provider that serves (not Off, not a
// failed one) and the forget role in what it declared. The built-in provider
// declares none.
export function forgettable(slot) {
  const r = slot?.resolution
  if (!r || r.off || !r.active || r.active === PROVIDER_BUILTIN) return null
  const p = slot.providers?.find((x) => x.id === r.active && x.kind === 'addon')
  if (!p || !p.available || !(p.roles || []).includes('forget')) return null
  return { id: p.id, title: p.title, undo: p.properties?.undo || '' }
}
