import { useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { upgradeFailureWords } from '../components/addons/updateReview'
import { addonUpgrades } from '../utils/addonUpgrades'
import { ringOf } from '../utils/opsActivity'
import { mergeOperations } from '../utils/addonUpgradeRows'
import { operationsApi } from '../utils/api'
import { useAddonUpgrades } from './useAddonUpgrades'
import { useOperations } from './useOperations'

// The operations of the instance as one list: what /api/operations reports,
// plus the add-on upgrades this browser started and follows (see
// addonUpgrades). They behave like server operations: progress, cancel, retry
// and dismiss. The Activity page and the Activity tab of the notification
// inbox both read it, so they always agree.
export function useActivityFeed() {
  const { t } = useTranslation('operate')
  const { t: ta } = useTranslation('addons')
  const {
    operations: serverOperations, history: serverHistory, fetchHistory,
    clearHistory: clearServerHistory, cancelOperation: cancelServerOperation, pauseOperation,
  } = useOperations()
  const upgrades = useAddonUpgrades()

  // An upgrade the server lists and this browser follows is one row: the
  // server says whether it waits and how far it is, the store keeps the words.
  const operations = useMemo(() => mergeOperations(serverOperations, upgrades.live, (op) => ({
    ...op,
    message: op.waiting
      ? t('activity.upgradeWaiting', { seconds: Math.max(1, Math.round(op.waiting.nextMs / 1000)) })
      : t('activity.upgradeVersions', { from: op.fromVersion || '?', to: op.toVersion || '?' }),
    error: op.state === 'failed' ? upgradeFailureWords(op.errorKey, ta) : undefined,
    isQueued: op.state !== 'failed' && op.phase === 'queued',
  })), [serverOperations, upgrades.live, t, ta])
  const history = useMemo(() => [
    ...serverHistory,
    ...upgrades.records.map((r) => ({ ...r, error: r.errorKey ? upgradeFailureWords(r.errorKey, ta) : '' })),
  ].sort((a, b) => new Date(b.finishedAt).getTime() - new Date(a.finishedAt).getTime()), [serverHistory, upgrades.records, ta])
  // A store entry cancels its job on the server; a row only the server lists
  // is cancelled by its job id.
  const cancelOperation = useCallback((jobID) => (
    addonUpgrades.cancel(jobID, (id) => operationsApi.cancel(id)) || cancelServerOperation(jobID)
  ), [cancelServerOperation])
  const clearHistory = useCallback(() => { addonUpgrades.clearRecords(); return clearServerHistory() }, [clearServerHistory])

  return { operations, history, fetchHistory, cancelOperation, pauseOperation, clearHistory }
}

// What the entry of the inbox needs to draw its ring, with no language file:
// it is mounted in the shell for everyone, and it only reads the numbers.
// Empty for anyone who is not an administrator (the poller does not run).
export function useActivitySummary() {
  const { operations } = useOperations()
  const upgrades = useAddonUpgrades()
  return useMemo(() => ringOf(mergeOperations(operations, upgrades.live)), [operations, upgrades.live])
}
