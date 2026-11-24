import { useEffect, useCallback } from 'react'
import { useBlocker } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ConfirmDialog from './ConfirmDialog'

// Guards against losing unsaved work: blocks in-app route changes (via the
// router's useBlocker) and warns on tab close/reload (beforeunload) whenever
// `when` is true. Drop into any page that has a dirty-state signal.
export default function UnsavedChangesGuard({ when }) {
  const { t } = useTranslation('common')
  const blocker = useBlocker(
    useCallback(
      ({ currentLocation, nextLocation }) => when && currentLocation.pathname !== nextLocation.pathname,
      [when]
    )
  )

  useEffect(() => {
    if (!when) return
    const handler = (e) => { e.preventDefault(); e.returnValue = '' }
    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  }, [when])

  return (
    <ConfirmDialog
      open={blocker.state === 'blocked'}
      title={t('unsaved.title')}
      message={t('unsaved.message')}
      confirmLabel={t('unsaved.leave')}
      danger
      onConfirm={() => blocker.proceed?.()}
      onCancel={() => blocker.reset?.()}
    />
  )
}
