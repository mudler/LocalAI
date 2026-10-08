import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { backendsApi, modelsApi, nodesApi } from '../utils/api'
import { useOperations } from './useOperations'

// Retry only ever means "install this again". A failed deletion would need the
// delete endpoint and a staging operation is driven by the router rather than
// by a user action, so neither is retryable.
export function isRetryable(op) {
  return Boolean(op?.error) && !op.isDeletion && op.taskType !== 'staging'
}

// What a failed operation offers: install again, or let it go. Shared by the
// Activity page and the Status page's "Needs you" row, so both do the same
// thing with the same calls.
export function useOperationActions(addToast) {
  const { t } = useTranslation('operate')
  const { dismissFailedOp } = useOperations()

  const retry = useCallback(async (op) => {
    // Dismiss before reinstalling, never after: the reinstall reuses the same
    // opcache key, and overwriting a failed entry in place skips recordTerminal
    // so the failure would never reach the record. Dismissing first is what
    // puts it there.
    //
    // By jobID, because the guarantee only holds while both calls address the
    // same job. Two ops can share an id (a local and a node-scoped install of
    // one backend), and dismissing by id could retire the other one instead,
    // leaving this failure to be overwritten in place by the reinstall below.
    await dismissFailedOp(op.jobID)
    // fullName is the gallery-qualified id the install endpoints expect;
    // `name` has the repo prefix stripped for display. Node-scoped ops already
    // had their prefix removed server side, so fullName is the bare slug there.
    const target = op.fullName || op.id
    try {
      if (op.nodeID) {
        await nodesApi.installBackend(op.nodeID, target)
      } else if (op.isBackend) {
        await backendsApi.install(target)
      } else {
        // The variant is not on the payload yet, so a pinned model retries as
        // an auto-select. ui_api.go already reads ?variant= at enqueue and
        // stores it on the ManagementOp; it only has to reach the
        // /api/operations payload and this call. Until then Retry stays,
        // because nothing here tells a pinned install from an unpinned one and
        // dropping it would cost every model the button.
        await modelsApi.install(target)
      }
    } catch (err) {
      addToast?.(t('activity.retryFailed', { message: err.message }), 'error')
    }
  }, [dismissFailedOp, addToast, t])

  return { retry, dismiss: dismissFailedOp }
}
