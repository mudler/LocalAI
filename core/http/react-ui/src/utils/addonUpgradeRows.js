// The add-on upgrades as the page sees them, in pure functions. An upgrade is
// followed from two sides: the browser store (utils/addonUpgrades) reads the
// owner's operation, and the server lists the same work in /api/operations once
// it is on the shared queue. These functions tell the two apart and join them,
// so one upgrade is one row.

// The owner stops a stop request once it is writing the package or starting
// the new version.
const UNSTOPPABLE = /^(committing|starting)/

// What an owner status says about an upgrade, as fields of a live entry.
// "accepted" means the upgrade waits on the queue, so it reads as "queued".
// The upgrade can be cancelled when the job is known and has not reached the
// writing or starting stage.
export function upgradeStatusFields(status, prev = {}) {
  const st = status && typeof status === 'object' ? status : {}
  const galleryJobId = st.jobId || prev.galleryJobId || ''
  const queued = st.phase === 'accepted'
  const stage = queued ? '' : (st.stage || '')
  const final = st.phase === 'completed' || st.phase === 'failed'
  const fields = {
    phase: queued ? 'queued' : (st.phase || prev.phase || ''),
    stage,
    failureCode: st.failureCode || '',
    galleryJobId,
    message: stage || (queued ? 'queued' : st.phase || ''),
    progress: 0,
  }
  fields.cancellable = prev.state === 'running' && Boolean(galleryJobId) && !final && !UNSTOPPABLE.test(stage)
  return fields
}

// A failed operation that was cancelled (while queued, by anyone) or that was
// stopped while running after this browser asked for it, ends as cancelled.
export function endedAsCancelled(failureCode, { requested = false } = {}) {
  return failureCode === 'cancelled' || (failureCode === 'interrupted' && requested)
}

const isUpgradeRow = (op) => Boolean(op) && op.isAddon === true && op.taskType === 'upgrade'

// The live entries with what the server says about the same job: whether it
// waits, how far it is, whether it can still be cancelled. The words of the
// entry (versions, failure) stay the store's.
export function overlayLive(live, serverOps) {
  const rows = (Array.isArray(serverOps) ? serverOps : []).filter(isUpgradeRow)
  return (Array.isArray(live) ? live : []).map((entry) => {
    const server = entry.galleryJobId ? rows.find(r => r.jobID === entry.galleryJobId) : null
    if (!server || entry.state !== 'running') return entry
    return {
      ...entry,
      phase: server.isQueued ? 'queued' : (entry.phase === 'queued' ? 'running' : entry.phase),
      stage: server.isQueued ? '' : entry.stage,
      progress: server.progress || 0,
      currentBytes: server.currentBytes,
      totalBytes: server.totalBytes,
      etaSeconds: server.etaSeconds,
      bytesPerSecond: server.bytesPerSecond,
      cancellable: Boolean(server.cancellable) && !UNSTOPPABLE.test(entry.stage || ''),
    }
  })
}

// The server rows of add-on upgrades that no live entry follows: started in
// another tab, or before a reload. They are what "leave and come back" shows.
export function unfollowedUpgrades(serverOps, live) {
  const followed = new Set((Array.isArray(live) ? live : []).map(e => e.galleryJobId).filter(Boolean))
  const busy = new Set((Array.isArray(live) ? live : []).filter(e => e.state !== 'failed').map(e => e.addonId))
  return (Array.isArray(serverOps) ? serverOps : []).filter(r => isUpgradeRow(r) && !followed.has(r.jobID) && !busy.has(r.name))
}

// The operations of the instance as one list: the server rows, minus the
// add-on upgrades a live entry already covers, plus the overlaid entries.
// `words(entry)` turns a live entry into the row the lists draw.
export function mergeOperations(serverOps, live, words = (e) => e) {
  const server = Array.isArray(serverOps) ? serverOps : []
  const covered = new Set((Array.isArray(live) ? live : []).map(e => e.galleryJobId).filter(Boolean))
  return [
    ...server.filter(r => !(isUpgradeRow(r) && covered.has(r.jobID))),
    ...overlayLive(live, server).map(words),
  ]
}

// The live entries of the Updates page: the overlaid ones, plus a stand-in for
// each upgrade only the server knows, so its row shows and can be cancelled.
export function liveWithServer(live, serverOps) {
  const own = overlayLive(live, serverOps)
  const others = unfollowedUpgrades(serverOps, live).filter(r => !r.error).map(r => ({
    addonId: r.name, id: r.name, name: r.name, isAddon: true, taskType: 'upgrade', jobID: r.jobID, galleryJobId: r.jobID,
    state: 'running', phase: r.isQueued ? 'queued' : (r.phase || 'running'), stage: '', progress: r.progress || 0, cancellable: Boolean(r.cancellable), fromServer: true,
  }))
  return [...own, ...others]
}
