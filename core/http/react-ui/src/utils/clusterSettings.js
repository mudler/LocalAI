// What the cluster settings form says about the settings it shows. No React
// here, so the unit tests read it.

// The broadcast bus of this frontend, from the `bus` of GET /api/cluster/settings:
// NATS while it reaches the NATS server, PostgreSQL otherwise. The result is
// the key of its words (cluster:bus.<key>) and the tone of its chip.
export function busState(bus) {
  if (bus?.bus === 'nats') return { key: 'nats', tone: 'ok' }
  if (bus?.bus === 'postgres') return { key: 'postgres', tone: bus?.nats_configured ? 'warn' : 'idle' }
  return { key: 'unknown', tone: 'idle' }
}

// True when NATS is configured and this frontend does not reach it: it
// broadcasts over PostgreSQL and is not ready until NATS is back.
export function natsOutOfReach(bus) {
  return Boolean(bus?.nats_configured && !bus?.nats_connected)
}

// The state of the NATS section: whether workers can be offered NATS.
//   connected    this frontend reaches the NATS server
//   unreachable  configured, not reached
//   off          no NATS address
export function natsState(settings) {
  const bus = settings?.bus
  if (bus?.nats_configured && bus?.nats_connected) return 'connected'
  if (bus?.nats_configured) return 'unreachable'
  return 'off'
}

// NATS is offered to a new worker only when it is configured: the worker needs
// an address to dial.
export function natsOffered(settings) {
  return Boolean(settings?.bus?.nats_configured || settings?.nats_url)
}

// The sections of the form. Settings search finds a section by these words, and
// by the words of its heading (the form adds those from its translation).
export const SECTIONS = ['bus', 'nats', 'weights', 'joining']

export const SECTION_WORDS = {
  bus: 'broadcast bus frontend frontends replica replicas postgresql postgres nats',
  nats: 'nats address url worker workers server reachable',
  weights: 'connection connections preference weight weights slider websocket p2p tie priority',
  joining: 'joining join auto approve approval token workers',
}

// The sections whose words match every word of the query. `text` gives the
// translated heading and note of a section, so a person who searches in their
// language finds it. An empty query matches nothing.
export function matchSections(query, text = () => '') {
  const words = String(query || '').toLowerCase().split(/\s+/).filter(Boolean)
  if (!words.length) return []
  return SECTIONS.filter(id => {
    const hay = `cluster ${SECTION_WORDS[id]} ${text(id)}`.toLowerCase()
    return words.every(word => hay.includes(word))
  })
}
