// The background install queue of add-ons, as pure functions: what the page
// sends, how it reads the answer, and how a row of /api/operations is told
// apart. No React and no network, so the unit tests drive it directly.

// The value of the confirm header of a queue request. The owner refuses a
// request that does not carry it, so a tool or an API key cannot queue.
export const QUEUE_CONFIRM = 'queue'

// The owner takes at most this many installs in one request.
export const MAX_QUEUE_ITEMS = 32

// What a failed install says when the verified package asked for more host
// access than the administrator reviewed. The server words it in English; the
// start of the text is the contract, and the page words the rest itself.
const NEEDS_APPROVAL = 'the add-on needs your approval again'

// A row of an install of the queue, as opposed to the upgrades this browser
// follows (those carry taskType "upgrade").
export function isAddonInstallOp(op) {
  return Boolean(op) && op.isAddon === true && op.taskType !== 'upgrade'
}

export function needsApproval(op) {
  if (op?.needsApproval === true) return true
  return typeof op?.error === 'string' && op.error.trim().toLowerCase().startsWith(NEEDS_APPROVAL)
}

// What a row is, for the page: a failure that needs a new review is not an
// error the person can retry blindly.
export function rowState(op) {
  if (needsApproval(op)) return 'review'
  if (op?.error) return 'failed'
  if (op?.isQueued) return 'queued'
  return 'running'
}

// The queue rows of the operation list: running ones first, then the waiting
// ones in the order the server gives them, then the failures, which stay until
// they are dismissed.
export function queueRows(operations) {
  const rows = (Array.isArray(operations) ? operations : []).filter(isAddonInstallOp)
  const order = { running: 0, queued: 1, review: 2, failed: 2 }
  return rows
    .map((op, i) => ({ op, i, rank: order[rowState(op)] }))
    .sort((a, b) => a.rank - b.rank || a.i - b.i)
    .map(x => x.op)
}

// The add-on ids that have an install waiting or running, so a card says so
// instead of offering the install again.
export function busyAddonIds(operations) {
  return new Set((Array.isArray(operations) ? operations : []).filter(op => isAddonInstallOp(op) && !op.error).map(op => op.name))
}

// The body of the queue request from the approvals of the dialog (the body that
// approvalBody builds). The action is sent only for an upgrade of an installed
// add-on: the queue installs unless told otherwise.
export function queueBody(approvals) {
  const items = (Array.isArray(approvals) ? approvals : []).slice(0, MAX_QUEUE_ITEMS).map(a => {
    const { operationId, sourceId, addonId, version, approve, action } = a
    return { operationId, ...(action === 'upgrade' ? { action } : {}), sourceId, addonId, version, approve }
  })
  return { items }
}

// Reads the answer of the owner. Items are independent: a refused one does
// not stop the others, so the answer says for each add-on we sent whether it
// has a job or why it was refused. An item the answer does not mention counts
// as refused: it was not queued.
export function queueOutcome(sent, response) {
  const answers = Array.isArray(response?.items) ? response.items : []
  const queued = []
  const refused = []
  const used = new Set()
  for (const approval of Array.isArray(sent) ? sent : []) {
    const at = answers.findIndex((x, i) => !used.has(i) && x && x.addonId === approval.addonId && (!x.operationId || x.operationId === approval.operationId))
    if (at < 0) {
      refused.push({ addonId: approval.addonId, error: { type: 'no_answer', message: '' } })
      continue
    }
    used.add(at)
    const answer = answers[at]
    if (answer.error) refused.push({ addonId: approval.addonId, error: answer.error })
    else if (answer.jobId) queued.push({ addonId: approval.addonId, jobId: answer.jobId })
    else refused.push({ addonId: approval.addonId, error: { type: 'no_answer', message: '' } })
  }
  return { queued, refused }
}

// The review of several add-ons one after another in one dialog. Each add-on
// is accepted with its own ticks or skipped; the accepted ones are sent in one
// request once the last one has been looked at.
export function batchStart(entries) {
  const seen = new Set()
  const list = (Array.isArray(entries) ? entries : []).filter(e => e && !seen.has(e.key) && seen.add(e.key))
  return { entries: list.slice(0, MAX_QUEUE_ITEMS), index: 0, accepted: [] }
}

export function batchCurrent(batch) {
  return batch && batch.index < batch.entries.length ? batch.entries[batch.index] : null
}

export function batchDone(batch) {
  return !batch || batch.index >= batch.entries.length
}

export function batchAccept(batch, approval) {
  return { ...batch, index: batch.index + 1, accepted: [...batch.accepted, approval] }
}

export function batchSkip(batch) {
  return { ...batch, index: batch.index + 1 }
}

// "Add-on 2 of 3" and whether the person is at the last one.
export function batchPosition(batch) {
  const total = batch ? batch.entries.length : 0
  return { number: Math.min(total, (batch ? batch.index : 0) + 1), total, last: Boolean(batch) && batch.index >= total - 1 }
}
