// What the proposal card of a PiKVM change shows, from the plain data the host
// hands it. No React, so a node test drives it. The facts come from the
// add-on's preview (the power state read from the device); the text and the
// keys are shown as the person will send them, never hidden.

const str = (v) => (typeof v === 'string' ? v : '')

export function proposalView(tool, args, sample) {
  const a = args && typeof args === 'object' ? args : {}
  const s = sample && typeof sample === 'object' ? sample : {}
  const keys = (Array.isArray(s.keys) ? s.keys : Array.isArray(a.keys) ? a.keys : []).filter((k) => typeof k === 'string')
  const text = str(s.text) || str(a.text)
  return {
    tool: str(tool),
    kind: tool === 'power-action' ? 'power' : tool === 'type-text' ? 'text' : tool === 'send-key' ? 'keys' : '',
    device: str(a.device) || str(s.device),
    action: str(s.action) || str(a.command),
    hard: s.hard === true,
    effect: str(s.effect),
    powerNow: s.powerNow === 'on' || s.powerNow === 'off' ? s.powerNow : '',
    text,
    chars: Number.isFinite(s.chars) ? s.chars : Array.from(text).length,
    lines: Number.isFinite(s.lines) ? s.lines : 0,
    keys,
  }
}

// The connections that list an address (without its pin), by name.
export function connectionsFor(connectors, device) {
  const want = String(device || '').split('#')[0].toLowerCase()
  if (!want) return []
  return (Array.isArray(connectors) ? connectors : [])
    .filter((c) => c && c.kind === 'pikvm' && (c.resources || []).some((r) => String(r).split('#')[0].toLowerCase() === want))
    .map((c) => String(c.name || ''))
    .filter(Boolean)
}
