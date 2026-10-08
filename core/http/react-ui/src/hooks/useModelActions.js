import { useCallback, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { backendControlApi, modelsApi } from '../utils/api'

// The lifecycle actions of an installed model: load it, stop it, turn it off,
// pin it, remove it. The Installed table and the model page share this so a
// button means the same thing wherever it sits.
//
//   addToast     shows the outcome.
//   afterAction  async, runs after an action succeeded, so the caller can read
//                its lists again.
//
// Stop and remove do not run at once: they set `confirmDialog`, which the
// caller renders with ConfirmDialog, and run when it is confirmed.
export function useModelActions({ addToast, afterAction }) {
  const { t } = useTranslation('models')
  const [pendingActions, setPendingActions] = useState(() => new Set())
  const [actionErrors, setActionErrors] = useState({})
  const [confirmDialog, setConfirmDialog] = useState(null)
  const after = useRef(afterAction)
  after.current = afterAction

  const setPending = useCallback((name, pending) => {
    setPendingActions(previous => {
      const next = new Set(previous)
      if (pending) next.add(name)
      else next.delete(name)
      return next
    })
  }, [])

  const runAction = useCallback(async (modelName, action, request, successMessage) => {
    setPending(modelName, true)
    setActionErrors(previous => ({ ...previous, [modelName]: null }))
    try {
      await request()
      if (successMessage) addToast(successMessage, 'success')
      await after.current?.()
      return true
    } catch (err) {
      setActionErrors(previous => ({
        ...previous,
        [modelName]: t('lifecycle.errors.action', { action, model: modelName, message: err.message }),
      }))
      return false
    } finally {
      setPending(modelName, false)
    }
  }, [addToast, setPending, t])

  const load = useCallback(modelName => runAction(
    modelName,
    t('lifecycle.actionNames.load'),
    () => backendControlApi.load({ model: modelName }),
    t('lifecycle.toasts.loaded', { model: modelName }),
  ), [runAction, t])

  const stop = useCallback(modelName => {
    setConfirmDialog({
      title: t('lifecycle.confirm.stopTitle'),
      message: t('lifecycle.confirm.stopMessage', { model: modelName }),
      confirmLabel: t('lifecycle.actions.stop'),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        await runAction(
          modelName,
          t('lifecycle.actionNames.stop'),
          () => backendControlApi.shutdown({ model: modelName }),
          t('lifecycle.toasts.stopped', { model: modelName }),
        )
      },
    })
  }, [runAction, t])

  const toggleState = useCallback((modelName, disabled) => {
    const operation = disabled ? 'enable' : 'disable'
    return runAction(
      modelName,
      t(`lifecycle.actionNames.${operation}`),
      () => modelsApi.toggleState(modelName, operation),
      t(`lifecycle.toasts.${operation}d`, { model: modelName }),
    )
  }, [runAction, t])

  const togglePinned = useCallback((modelName, pinned) => {
    const operation = pinned ? 'unpin' : 'pin'
    return runAction(
      modelName,
      t(`lifecycle.actionNames.${operation}`),
      () => modelsApi.togglePinned(modelName, operation),
      t(`lifecycle.toasts.${operation}ned`, { model: modelName }),
    )
  }, [runAction, t])

  // onDeleted runs only when the model really went.
  const remove = useCallback((modelName, onDeleted) => {
    setConfirmDialog({
      title: t('lifecycle.confirm.deleteTitle'),
      message: t('lifecycle.confirm.deleteMessage', { model: modelName }),
      confirmLabel: t('lifecycle.actions.delete'),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        const deleted = await runAction(
          modelName,
          t('lifecycle.actionNames.delete'),
          () => modelsApi.deleteByName(modelName),
          t('lifecycle.toasts.deleted', { model: modelName }),
        )
        if (deleted) onDeleted?.()
      },
    })
  }, [runAction, t])

  const reload = useCallback(() => runAction(
    'models',
    t('lifecycle.actionNames.update'),
    modelsApi.reload,
    t('lifecycle.toasts.updated'),
  ), [runAction, t])

  return { pendingActions, actionErrors, confirmDialog, setConfirmDialog, load, stop, toggleState, togglePinned, remove, reload }
}
