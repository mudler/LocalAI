// What the proposal card of a Jellyfin change shows, from the plain data the
// host hands it. No React, so a node test drives it. The names come from the
// server and are shown as text; nothing here comes from model prose except the
// ids and numbers of the arguments.

const str = (v) => (typeof v === 'string' ? v : '')
const int = (v) => (Number.isInteger(v) ? v : null)

export const COMMANDS = ['play', 'pause', 'resume', 'stop', 'seek', 'set-volume']

// "1:05:09" or "5:09" for a number of seconds.
export function clock(total) {
  const s = Math.max(0, Math.floor(Number(total) || 0))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = String(s % 60).padStart(2, '0')
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`
}

// The facts of one proposal. `args` are the stored arguments; `sample` is the
// short preview the add-on made from the server's own records.
export function proposalView(tool, args, sample) {
  const a = args && typeof args === 'object' ? args : {}
  const s = sample && typeof sample === 'object' ? sample : {}
  const command = COMMANDS.includes(str(a.command)) ? str(a.command) : ''
  return {
    tool: str(tool),
    server: str(a.server),
    command,
    user: str(s.user),
    device: str(s.device),
    client: str(s.client),
    nowPlaying: str(s.nowPlaying),
    positionSeconds: int(s.positionSeconds),
    toSeconds: int(a.positionSeconds) ?? int(s.toSeconds),
    volume: int(a.volume) ?? int(s.volume),
    item: str(s.item),
    series: str(s.series),
    played: typeof a.played === 'boolean' ? a.played : (typeof s.played === 'boolean' ? s.played : null),
    favorite: typeof a.favorite === 'boolean' ? a.favorite : (typeof s.favorite === 'boolean' ? s.favorite : null),
    playlist: str(s.playlist),
    items: (Array.isArray(s.items) ? s.items : []).filter((x) => typeof x === 'string'),
    itemCount: int(s.itemCount) ?? (Array.isArray(a.itemIds) ? a.itemIds.length : 0),
    forUser: str(a.jellyfinUser),
  }
}

// The connections that list a server, by name, in the order of the list.
export function connectionsFor(connectors, server) {
  const want = String(server || '').toLowerCase()
  if (!want) return []
  return (Array.isArray(connectors) ? connectors : [])
    .filter((c) => c && c.kind === 'jellyfin' && (c.resources || []).some((r) => String(r).toLowerCase() === want))
    .map((c) => String(c.name || ''))
    .filter(Boolean)
}
