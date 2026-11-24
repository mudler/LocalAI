/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { backendControlApi } from '../../utils/api'
import ConfirmDialog from '../ConfirmDialog'

// Stopping a loaded model, with the same words and the same call as the
// running-models table on This machine: ask first, then POST /backend/shutdown.
// The next request that uses the model loads it again, which the dialog says.
//
// `onDone` runs after the call settles so the caller can read the models again.
export function useUnloadModel({ addToast, onDone }) {
  const { t } = useTranslation('operate')
  const [target, setTarget] = useState(null)
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const invokerRef = useRef(null)

  const ask = useCallback((model, invoker) => {
    invokerRef.current = invoker || null
    setTarget(model)
  }, [])

  const cancel = useCallback(() => {
    setTarget(null)
    requestAnimationFrame(() => invokerRef.current?.focus())
  }, [])

  const confirm = useCallback(async () => {
    if (!target || busyRef.current) return
    busyRef.current = true
    setBusy(true)
    try {
      await backendControlApi.shutdown({ model: target.id })
      addToast?.(t('unload.done', { name: target.id }), 'success')
    } catch (err) {
      addToast?.(t('unload.failed', { name: target.id, message: err.message || String(err) }), 'error')
    } finally {
      await onDone?.()
      setTarget(null)
      setBusy(false)
      busyRef.current = false
    }
  }, [target, addToast, onDone, t])

  const dialog = (
    <ConfirmDialog
      open={!!target}
      title={target ? t('unload.title', { name: target.id }) : t('unload.titleFallback')}
      message={target ? t('unload.message', { backend: target.backend || t('unload.backendFallback'), name: target.id }) : ''}
      confirmLabel={t('unload.confirm')}
      pendingLabel={t('unload.pending')}
      pending={busy}
      danger
      onConfirm={confirm}
      onCancel={cancel}
    />
  )
  return { ask, dialog, busy }
}
