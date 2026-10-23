// The statuses in which a job is working. They are the stages of the stage line.
export const ACTIVE = ['queued', 'loading_model', 'loading_dataset', 'training', 'saving']

// How far a job is, in percent, or null when that is not known. The engine
// reads 0 for a job that is running but has reported nothing yet (a snapshot
// has no percent of its own), so a 0 while the job works means "unknown", and
// the page must not draw it as 0%. Steps, when they are known, give a better
// answer than that 0.
export function percentOf(event) {
  if (!event) return null
  const status = event.status
  if (status === 'completed') return 100
  const percent = Number(event.progress_percent) || 0
  if (percent > 0) return Math.min(percent, 100)
  if (ACTIVE.includes(status)) {
    const done = Number(event.current_step) || 0
    const total = Number(event.total_steps) || 0
    if (done > 0 && total > 0) return Math.min((done / total) * 100, 100)
    return null
  }
  return percent
}
