// The connection preference weights of a distributed cluster
// (routing.carrier_weights on the wire). A weight only breaks a tie between
// workers that fit a request equally well. It never overrides fit. The server
// checks the same range; the form checks it first so that a typo does not cost
// a round trip.

export const WEIGHT_MIN = 0
export const WEIGHT_MAX = 1000

export const CONNECTIONS = [
  { key: 'nats', label: 'NATS' },
  { key: 'websocket', label: 'WebSocket' },
  { key: 'p2p', label: 'P2P' },
]

export const DEFAULT_WEIGHTS = { nats: 100, websocket: 50, p2p: 25 }

// The range of the slider: a whole number from 0 to 1000.
export const WEIGHT_RANGE = { min: WEIGHT_MIN, max: WEIGHT_MAX, step: 1, integer: true }

function pick(sent, fallback) {
  const out = {}
  for (const { key } of CONNECTIONS) out[key] = Number.isInteger(sent?.[key]) ? sent[key] : fallback[key]
  return out
}

// The weights in force: what the server reports, and the default for a
// connection it did not report.
export function weightsInForce(settings) {
  return pick(settings?.carrier_weights, DEFAULT_WEIGHTS)
}

export function defaultWeights(settings) {
  return pick(settings?.carrier_weights_default, DEFAULT_WEIGHTS)
}

// Reads one field. Only a whole number from 0 to 1000 is a weight. The error
// is a code (empty, whole, range) that the form turns into a sentence.
export function parseWeight(text) {
  const raw = String(text ?? '').trim()
  if (raw === '') return { ok: false, error: 'empty' }
  if (!/^\d+$/.test(raw)) return { ok: false, error: 'whole' }
  const value = Number(raw)
  if (value < WEIGHT_MIN || value > WEIGHT_MAX) return { ok: false, error: 'range' }
  return { ok: true, value }
}

// The error code of each field that is not a weight, by connection key.
export function weightErrors(draft) {
  const errors = {}
  for (const { key } of CONNECTIONS) {
    const parsed = parseWeight(draft?.[key])
    if (!parsed.ok) errors[key] = parsed.error
  }
  return errors
}

// The request body that saves the draft: only the weights that differ from the
// ones in force, so that a save never writes a connection nobody touched. null
// when nothing changed or a field is not a weight.
export function weightsPayload(draft, current) {
  if (Object.keys(weightErrors(draft)).length > 0) return null
  const changed = {}
  for (const { key } of CONNECTIONS) {
    const value = parseWeight(draft[key]).value
    if (value !== current[key]) changed[key] = value
  }
  return Object.keys(changed).length > 0 ? { carrier_weights: changed } : null
}

// The request body that puts the defaults back: null for every connection that
// is not at its default.
export function resetPayload(current, defaults) {
  const changed = {}
  for (const { key } of CONNECTIONS) {
    if (current[key] !== defaults[key]) changed[key] = null
  }
  return Object.keys(changed).length > 0 ? { carrier_weights: changed } : null
}

// Which of two connections wins a tie, as a pair of keys: the sentence under
// the sliders says it live. null when the weights tie (the router then falls
// back to its own order). A weight of 0 means "only when nothing else fits",
// so it never wins.
export function tieWinner(weights, a, b) {
  const wa = Number(weights?.[a])
  const wb = Number(weights?.[b])
  if (!Number.isFinite(wa) || !Number.isFinite(wb) || wa === wb) return null
  return wa > wb ? [a, b] : [b, a]
}

// The connections ordered from the one that wins a tie to the one that loses.
export function preferenceOrder(weights) {
  return [...CONNECTIONS].map(c => c.key).sort((a, b) => Number(weights?.[b]) - Number(weights?.[a]))
}
