// What the proposal card of an SSH command shows, from the plain data the host
// hands it. No React, so a node test drives it. The facts come from the
// add-on's preview. The command is shown exactly as it will be sent, never
// shortened or hidden.

const str = (v) => (typeof v === 'string' ? v : '')
const int = (v) => (Number.isInteger(v) ? v : null)

export function proposalView(tool, args, sample) {
  const a = args && typeof args === 'object' ? args : {}
  const s = sample && typeof sample === 'object' ? sample : {}
  const command = str(s.command) || str(a.command)
  return {
    tool: str(tool),
    host: str(a.host) || str(s.host),
    user: str(s.user),
    command,
    lines: command === '' ? 0 : command.split('\n').length,
    timeoutSeconds: int(s.timeoutSeconds) ?? int(a.timeoutSeconds),
    hostKey: str(s.hostKey),
    login: s.login === 'key' || s.login === 'password' ? s.login : '',
    effect: str(s.effect),
  }
}

// The connections that list an address (without its pin), by name.
export function connectionsFor(connectors, host) {
  const want = String(host || '').split('#')[0].toLowerCase()
  if (!want) return []
  return (Array.isArray(connectors) ? connectors : [])
    .filter((c) => c && c.kind === 'ssh' && (c.resources || []).some((r) => String(r).split('#')[0].toLowerCase() === want))
    .map((c) => String(c.name || ''))
    .filter(Boolean)
}
