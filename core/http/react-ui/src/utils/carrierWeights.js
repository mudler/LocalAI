// The carrier weights of a distributed cluster (routing.carrier_weights). A
// weight only breaks a tie between nodes that fit a request equally well. It
// never overrides fit. The server checks the same range; the panel checks it
// first so that a typo does not cost a round trip.

export const WEIGHT_MIN = 0
export const WEIGHT_MAX = 1000

export const CARRIERS = [
  { key: 'nats', label: 'NATS' },
  { key: 'websocket', label: 'WebSocket' },
  { key: 'p2p', label: 'P2P' },
]

export const DEFAULT_WEIGHTS = { nats: 100, websocket: 50, p2p: 25 }

// The weights in force: what the server reports, and the default for a carrier
// it did not report.
export function weightsInForce(settings) {
  const sent = settings?.carrier_weights || {}
  const out = {}
  for (const { key } of CARRIERS) {
    out[key] = Number.isInteger(sent[key]) ? sent[key] : DEFAULT_WEIGHTS[key]
  }
  return out
}

export function defaultWeights(settings) {
  const sent = settings?.carrier_weights_default || {}
  const out = {}
  for (const { key } of CARRIERS) {
    out[key] = Number.isInteger(sent[key]) ? sent[key] : DEFAULT_WEIGHTS[key]
  }
  return out
}

// Reads one field. Only a whole number from 0 to 1000 is a weight.
export function parseWeight(text) {
  const raw = String(text ?? '').trim()
  if (raw === '') return { ok: false, error: 'Enter a whole number from 0 to 1000.' }
  if (!/^\d+$/.test(raw)) return { ok: false, error: 'Use a whole number from 0 to 1000.' }
  const value = Number(raw)
  if (value < WEIGHT_MIN || value > WEIGHT_MAX) return { ok: false, error: 'A weight is from 0 to 1000.' }
  return { ok: true, value }
}

// The error of each field that is not a weight, by carrier key.
export function weightErrors(draft) {
  const errors = {}
  for (const { key } of CARRIERS) {
    const parsed = parseWeight(draft?.[key])
    if (!parsed.ok) errors[key] = parsed.error
  }
  return errors
}

// The request body that saves the draft: only the weights that differ from the
// ones in force, so that a save never writes a carrier nobody touched. null when
// nothing changed or a field is not a weight.
export function weightsPayload(draft, current) {
  if (Object.keys(weightErrors(draft)).length > 0) return null
  const changed = {}
  for (const { key } of CARRIERS) {
    const value = parseWeight(draft[key]).value
    if (value !== current[key]) changed[key] = value
  }
  return Object.keys(changed).length > 0 ? { carrier_weights: changed } : null
}

// The request body that puts the defaults back: null for every carrier that
// is not at its default.
export function resetPayload(current, defaults) {
  const changed = {}
  for (const { key } of CARRIERS) {
    if (current[key] !== defaults[key]) changed[key] = null
  }
  return Object.keys(changed).length > 0 ? { carrier_weights: changed } : null
}
