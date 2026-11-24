// What the proposal card of a Home Assistant change shows, from the plain data
// the host hands it. No React, so a node test drives it. The facts come from
// the add-on's preview (names and states read from Home Assistant); nothing
// here comes from model prose except the service data, which is shown as text.

const str = (v) => (typeof v === 'string' ? v : '')

export function proposalView(tool, args, sample) {
  const a = args && typeof args === 'object' ? args : {}
  const s = sample && typeof sample === 'object' ? sample : {}
  const targets = (Array.isArray(s.targets) ? s.targets : []).filter((t) => t && typeof t === 'object').map((t) => ({
    entityId: str(t.entityId), name: str(t.name), state: str(t.state),
  }))
  const ids = Array.isArray(a.entityIds) ? a.entityIds.filter((x) => typeof x === 'string') : []
  const dataObj = a.data && typeof a.data === 'object' ? a.data : null
  return {
    tool: str(tool),
    physical: tool === 'call-physical-service' || s.physical === true,
    action: str(s.action) || (str(a.domain) && str(a.service) ? `${a.domain}.${a.service}` : ''),
    instance: str(a.instance) || str(s.instance),
    targets: targets.length ? targets : ids.map((id) => ({ entityId: id, name: '', state: '' })),
    // The stored arguments hold the whole data; the preview holds a cut copy.
    data: dataObj ? JSON.stringify(dataObj) : str(s.data),
    dataCut: !dataObj && s.dataCut === true,
  }
}

// The connections that list an address, by name.
export function connectionsFor(connectors, instance) {
  const want = String(instance || '').toLowerCase()
  if (!want) return []
  return (Array.isArray(connectors) ? connectors : [])
    .filter((c) => c && c.kind === 'home-assistant' && (c.resources || []).some((r) => String(r).toLowerCase() === want))
    .map((c) => String(c.name || ''))
    .filter(Boolean)
}
