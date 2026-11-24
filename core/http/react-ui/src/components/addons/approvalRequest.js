import { requestFingerprint } from './actionBook.js'
import { AddonsError } from '../../utils/addonsApi.js'
import { queueBody, queueOutcome } from '../../utils/addonQueue.js'

// One identity per distinct approval: the same ticks of the same preview are a
// retry and keep it; other ticks or a fresh preview are a new request.
export function approveKey(addon, body) {
  return `approve:${addon.id}:${addon.pendingUpgrade.version}:${requestFingerprint(body)}`
}

// The request the upgrade store runs for an approval. The approval goes to the
// queue of the host, which records it (and spends its one-time nonce) at once
// and runs the upgrade as the operation of the add-on on the shared queue, the
// same operation a plain upgrade is. `queue` is the queue call of the host
// access client (hostGrantsApi.queue). `send` answers the accepted status with
// the job of /api/operations, or throws the refusal of the owner.
export function approvalRequest(addon, body, queue) {
  const item = { ...body, action: 'upgrade' }
  return {
    key: approveKey(addon, body),
    fingerprint: requestFingerprint(body),
    version: addon.pendingUpgrade.version,
    send: async (operationId, signal) => {
      const sent = { ...item, operationId }
      const out = await queue(queueBody([sent]), signal)
      const { queued, refused } = queueOutcome([sent], out)
      if (queued.length === 0) {
        const error = refused[0] && refused[0].error ? refused[0].error : { type: 'no_answer' }
        throw new AddonsError({ kind: error.type === 'no_answer' ? 'unavailable' : 'invalid', type: error.type, message: error.message, status: error.code })
      }
      return { operationId, addonId: addon.id, phase: 'accepted', jobId: queued[0].jobId }
    },
  }
}
