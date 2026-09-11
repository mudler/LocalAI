const WINDOW_MS = 5_000

export function createTransferRateSampler() {
  const histories = new Map()

  const reset = (jobID) => {
    if (jobID === undefined) histories.clear()
    else histories.delete(jobID)
  }

  const retain = (jobIDs) => {
    const active = new Set(jobIDs)
    for (const jobID of histories.keys()) {
      if (!active.has(jobID)) histories.delete(jobID)
    }
  }

  const sample = (jobID, currentBytes, totalBytes, now = Date.now()) => {
    const valid = jobID !== undefined
      && jobID !== null
      && Number.isFinite(currentBytes)
      && Number.isFinite(totalBytes)
      && Number.isFinite(now)
      && currentBytes >= 0
      && totalBytes > 0
      && currentBytes < totalBytes
    if (!valid) {
      if (jobID !== undefined && jobID !== null) reset(jobID)
      return {}
    }

    let history = histories.get(jobID) || []
    const newest = history.at(-1)
    if (newest && (currentBytes < newest.bytes || now < newest.at)) history = []

    history.push({ bytes: currentBytes, at: now })
    history = history.filter((entry) => entry.at >= now - WINDOW_MS)
    histories.set(jobID, history)

    const oldest = history[0]
    const elapsedSeconds = (now - oldest.at) / 1_000
    const bytesPerSecond = (currentBytes - oldest.bytes) / elapsedSeconds
    if (!Number.isFinite(bytesPerSecond) || bytesPerSecond <= 0) return {}

    return {
      bytesPerSecond,
      etaSeconds: Math.round((totalBytes - currentBytes) / bytesPerSecond),
    }
  }

  return { sample, retain, reset }
}
