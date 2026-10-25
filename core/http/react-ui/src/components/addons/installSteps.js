// The steps of a backend install from a library, in the order the host runs
// them. The host reports the step it is in as the phase of the job; this module
// only turns that phase into one state per step. It never decides that a step
// passed: a step before the reported phase has been left, and that is all.
export const STEP_ORDER = ['resolving', 'verifying', 'size', 'fetching', 'writing']

// `signed` says the library requires a signature. Without it the host skips
// the verifying step, so the page does not list it.
export function installSteps(job, { signed = true } = {}) {
  const ids = STEP_ORDER.filter(id => signed || id !== 'verifying')
  const phase = job && typeof job.phase === 'string' ? job.phase : ''
  const finished = Boolean(job && job.processed && !job.error)
  const failed = Boolean(job && job.error)
  let at = ids.indexOf(phase)
  if (at < 0) at = job ? 0 : -1
  return ids.map((id, i) => {
    let state = 'pending'
    if (finished) state = 'done'
    else if (i < at) state = 'done'
    else if (i === at) state = failed ? 'failed' : 'active'
    const step = { id, state }
    if (id === 'fetching' && state === 'active' && Number(job.progress) > 0) {
      step.progress = Math.min(100, Math.round(Number(job.progress)))
    }
    return step
  })
}
