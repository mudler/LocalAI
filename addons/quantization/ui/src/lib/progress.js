// The statuses in which a job is working. They are the stages of the stage line.
export const ACTIVE = ['queued', 'downloading', 'converting', 'quantizing']

// How far a job is, in percent, or null when that is not known. The engine
// reads 0 for a job that is working but has reported nothing yet, so a 0 while
// the job works means "unknown", and the page must not draw it as 0%.
export function percentOf(event) {
  if (!event) return null
  const status = event.status
  if (status === 'completed') return 100
  const percent = Number(event.progress_percent) || 0
  if (percent > 0) return Math.min(percent, 100)
  if (ACTIVE.includes(status)) return null
  return percent
}
