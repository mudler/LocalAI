// What the Activity tab of the notification inbox says about long-running
// operations, as pure functions so the unit tests can check them without a
// browser. The words come from two language files: `operate` (the verbs, the
// phases and the add-on stages, shared with the Activity page so the two never
// word one operation differently) and `chats` (the few strings that exist only
// in the inbox). `t` is a translator that can reach both.
import { formatBytes } from './format.js'

// How many finished operations "Recent" lists, and how long one stays there.
export const RECENT_MAX = 5
export const RECENT_MAX_AGE_MS = 24 * 60 * 60 * 1000

const OP = { ns: 'operate' }

const PHASE_KEYS = {
  resolving: 'activity.phase.resolving',
  downloading: 'activity.phase.downloading',
  verifying: 'activity.phase.verifying',
  committing: 'activity.phase.committing',
  persisting: 'activity.phase.persisting',
}

// An add-on upgrade is followed by the browser and reports a failure through
// state, a server operation through error. Either one needs a decision.
export function isFailedOp(op) {
  return Boolean(op?.error) || op?.state === 'failed'
}

// Splits the live operations into what needs attention, what runs and what
// waits. The queue goes last: a running operation is the one to watch.
export function splitOps(operations) {
  const list = operations || []
  const failed = list.filter(isFailedOp)
  const live = list.filter(op => !isFailedOp(op))
  const running = live.filter(op => !op.isQueued)
  const queued = live.filter(op => op.isQueued)
  return { failed, running, queued, live: [...running, ...queued] }
}

// The mean progress of the operations that run, 0 to 100. A queue has nothing
// to average.
export function meanProgress(running) {
  const list = running || []
  if (list.length === 0) return 0
  const sum = list.reduce((acc, op) => acc + (Number.isFinite(op.progress) ? op.progress : 0), 0)
  return Math.max(0, Math.min(100, sum / list.length))
}

// What the ring on the inbox entry shows: whether anything is live, how far
// along the running operations are (null while only a queue waits, so the ring
// turns instead of filling) and whether something failed. A whole number, so
// the entry does not redraw on every fraction of a percent.
export function ringOf(operations) {
  const { failed, running, queued } = splitOps(operations)
  const active = running.length + queued.length > 0
  return {
    active,
    value: running.length > 0 ? Math.round(meanProgress(running)) : null,
    failed: failed.length > 0,
    failedCount: failed.length,
    count: running.length + queued.length,
  }
}

// The tab the panel opens on, from the ring of the entry: Activity when
// something runs, waits or failed, else Notifications.
export function openingTab(ring) {
  return ring && (ring.count > 0 || ring.failed) ? 'activity' : 'notifications'
}

// What kind of operation this is, for the verb. A removal and a staging job
// are named before an upgrade so a removal never reads as an install.
export function verbOf(op) {
  if (op?.isDeletion) return 'removing'
  if (op?.taskType === 'staging') return 'staging'
  if (op?.taskType === 'upgrade') return 'upgrading'
  return 'installing'
}

export function nameOf(op) {
  return op?.name || op?.id || ''
}

// "Installing qwen3-14b", "Updating llama-cpp". A queued operation keeps its
// verb: the detail line says it is waiting.
export function titleOf(op, t) {
  return t(`inbox.ops.title.${verbOf(op)}`, { name: nameOf(op) })
}

// "Couldn't install qwen3-14b".
export function failedTitleOf(op, t) {
  return t(`inbox.ops.failedTitle.${verbOf(op)}`, { name: nameOf(op) })
}

// The reason a failed operation gives. An add-on failure carries a code the
// UI words itself; anything else is the server's own text.
export function reasonOf(op, t) {
  if (op?.isAddon && op.failureCode) return t(`activity.addonFailure.${op.failureCode}`, { ...OP, defaultValue: op.error })
  return op?.error || ''
}

// The one line under the title:
//   "Node 3 of 5 · Downloading · 1.2 / 1.9 GB · 48 MB/s"
// A queued operation says it waits. The pieces a job does not report are left
// out: a legacy install has no phase and no bytes, a single-node install no
// node count.
export function detailOf(op, t) {
  if (op?.isQueued) return t('inbox.ops.waiting')
  const parts = []
  const nodes = Array.isArray(op?.nodes) ? op.nodes : []
  if (nodes.length > 1) {
    const done = nodes.filter(node => node.status === 'success').length
    parts.push(t('inbox.ops.node', { current: Math.min(nodes.length, done + 1), total: nodes.length }))
  } else if (op?.taskType === 'staging' && op.nodeName) {
    parts.push(t('activity.toNode', { ...OP, node: op.nodeName }))
  }
  if (op?.isAddon && op.fromVersion && op.toVersion) {
    parts.push(t('activity.upgradeVersions', { ...OP, from: op.fromVersion, to: op.toVersion }))
  }
  const stage = op?.stage || op?.phase
  let phaseText = ''
  if (op?.isAddon) phaseText = stage ? t(`activity.addonStage.${stage}`, OP) : ''
  else if (PHASE_KEYS[op?.phase]) phaseText = t(PHASE_KEYS[op.phase], OP)
  // Only the managed-artifact path reports phases, so the server's own message
  // is all a legacy install has to say.
  if (phaseText) parts.push(phaseText)
  else if (op?.message && !op?.isAddon) parts.push(op.message)
  if (Number.isFinite(op?.currentBytes) && Number.isFinite(op?.totalBytes) && op.totalBytes > 0) {
    parts.push(`${formatBytes(op.currentBytes)} / ${formatBytes(op.totalBytes)}`)
  }
  if (Number.isFinite(op?.bytesPerSecond) && op.bytesPerSecond > 0) parts.push(`${formatBytes(op.bytesPerSecond)}/s`)
  return parts.join(' · ')
}

// Cancel is offered while the operation can still be called off. An
// operation that failed has nothing left to cancel.
export function canCancel(op, cancelling = false) {
  return Boolean(op?.cancellable) && !isFailedOp(op) && !cancelling
}

// The finished operations "Recent" lists: the ones that completed, newest
// first, within the last day, at most `max`. A cancelled operation is not a
// result and a failed one that was dismissed was already read, so neither is
// listed (the Activity page keeps both in its record). Add-on records have no
// outcome of their own in this list beyond completed.
export function recentRecords(history, { now = Date.now(), max = RECENT_MAX, maxAgeMs = RECENT_MAX_AGE_MS } = {}) {
  const seen = new Set()
  const out = []
  const sorted = [...(history || [])].sort((a, b) => Date.parse(b?.finishedAt || '') - Date.parse(a?.finishedAt || ''))
  for (const rec of sorted) {
    if (rec?.outcome !== 'completed') continue
    const at = Date.parse(rec.finishedAt || '')
    if (!at || now - at > maxAgeMs) continue
    if (seen.has(rec.jobID)) continue
    seen.add(rec.jobID)
    out.push(rec)
    if (out.length >= max) break
  }
  return out
}

// "Installed qwen3-14b", "Updated llama-cpp", "Removed whisper", "Staged x".
export function recentTitle(rec, t) {
  let kind = 'installed'
  if (rec?.taskType === 'deletion') kind = 'removed'
  else if (rec?.taskType === 'staging') kind = 'staged'
  else if (rec?.taskType === 'upgrade') kind = 'upgraded'
  return t(`inbox.ops.recent.${kind}`, { name: nameOf(rec) })
}

// The tab a key moves to from `current`, or '' for a key that is not a tab
// key. Two tabs, so the arrows wrap and Home and End are the two ends.
const TABS = ['notifications', 'activity']
export function nextTab(current, key) {
  const at = TABS.indexOf(current)
  if (at < 0) return ''
  if (key === 'ArrowRight') return TABS[(at + 1) % TABS.length]
  if (key === 'ArrowLeft') return TABS[(at - 1 + TABS.length) % TABS.length]
  if (key === 'Home') return TABS[0]
  if (key === 'End') return TABS[TABS.length - 1]
  return ''
}
