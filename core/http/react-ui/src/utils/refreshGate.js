// Orders concurrent reads of one list. Two rules:
//
//   - Only the newest response is applied. An older request that ends later
//     must not overwrite a newer one.
//   - A refresh asked for while a read runs is not dropped. It is remembered,
//     and one more read runs when the last running one ends.
//
// The functions are pure: each takes the gate and returns the next one.
export const newGate = () => ({ latest: 0, running: 0, pending: false })

// A read begins. The token says which read it is.
export function startFetch(gate) {
  const latest = gate.latest + 1
  return { gate: { ...gate, latest, running: gate.running + 1 }, token: latest }
}

// Someone wants fresh data. runNow is false while a read runs: the request is
// kept and finishFetch reports it.
export function requestRefresh(gate) {
  if (gate.running > 0) return { gate: { ...gate, pending: true }, runNow: false }
  return { gate, runNow: true }
}

// A read ends. apply: its response is the newest. idle: no read is left.
// rerun: a request came in meanwhile, so run one more read now.
export function finishFetch(gate, token) {
  const running = Math.max(0, gate.running - 1)
  const idle = running === 0
  const rerun = idle && gate.pending
  return {
    gate: { ...gate, running, pending: rerun ? false : gate.pending },
    apply: token === gate.latest,
    idle,
    rerun,
  }
}
