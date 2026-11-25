// SPDX-License-Identifier: MIT
// The update sheet of the Android app: the new version, the
// one-time "install unknown apps" consent when Android still needs it, and
// "Update now". The install starts only from that tap.
import { useEffect, useId, useState } from 'react'
import { Button, Callout, Icon } from '@localai/ui-kit'
import type { UpdaterStatus } from '../../shared/types'
import { useBridge, type ApkUpdateView } from '../bridge'
import { Dialog } from '../Dialog'
import { S } from '../strings'
import { formatBytes } from '../viewModel'

export function ApkUpdateSheet({
  installed,
  channel,
  updater,
  onClose,
}: {
  /** The version of this app. */
  installed: string
  channel: string
  /** The updater state; every change reads the sheet's view again. */
  updater?: UpdaterStatus
  onClose: () => void
}) {
  const b = useBridge()
  const ids = useId()
  const [view, setView] = useState<ApkUpdateView>()
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let live = true
    const read = () => {
      void b.getApkUpdate?.().then(
        (v) => {
          if (live) setView(v)
        },
        () => undefined,
      )
    }
    read()
    // Back from the Android settings: the consent may be on now.
    const onVisible = () => {
      if (document.visibilityState === 'visible') read()
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      live = false
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [b, updater])

  const offer = view?.offer
  const phase = updater?.phase
  const consent = view !== undefined && !view.canInstall
  const installing = view?.install === 'installing'
  const updateNow = async () => {
    setBusy(true)
    try {
      await b.installLauncherUpdate?.()
    } finally {
      setBusy(false)
    }
  }

  const title = offer ? S.apkUpdate.sheetTitle(offer.version) : S.apkUpdate.updateTitle
  return (
    <Dialog title={title} titleId={`${ids}-apk`} onClose={onClose} initialFocus="[data-update]" testId="apk-update-sheet" className="lnr-dialog--wide">
      {!offer ? (
        <p className="lnr-dialog__p">{phase === 'error' && updater?.message ? updater.message : S.apkUpdate.noOffer}</p>
      ) : (
        <>
          <p className="lnr-dialog__p dk-muted">{S.apkUpdate.sheetSub(installed, formatBytes(offer.size), channel)}</p>
          {offer.downloaded ? (
            <p className="lnr-dialog__p dk-small" data-testid="apk-downloaded">
              <Icon name="check" /> {S.apkUpdate.downloaded}
            </p>
          ) : (
            phase === 'downloading' && <p className="lnr-dialog__p dk-small">{S.apkUpdate.downloading(updater?.percent)}</p>
          )}
          {updater?.message && (
            <Callout tone={phase === 'error' ? 'error' : 'warn'} role="status" testId="apk-message">
              {updater.message}
            </Callout>
          )}
          <ol className="lnr-steps lnr-dialog__steps">
            {consent && (
              <li className="lnr-step" data-testid="apk-consent">
                <div className="lnr-step__row">
                  <span className="lnr-step__mark lnr-step__mark--active" aria-hidden="true">1</span>
                  <strong>{S.apkUpdate.consentTitle}</strong>
                </div>
                <p className="dk-small dk-muted">
                  {S.apkUpdate.consentBody1}
                  <strong>{S.apkUpdate.consentSetting}</strong>
                  {S.apkUpdate.consentBody2}
                </p>
                <Button size="sm" variant="primary" onClick={() => void b.openInstallSettings?.()} data-testid="apk-open-settings">
                  {S.apkUpdate.openSettings}
                </Button>
              </li>
            )}
            <li className="lnr-step">
              <div className="lnr-step__row">
                <span className={`lnr-step__mark ${consent ? 'lnr-step__mark--todo' : 'lnr-step__mark--active'}`} aria-hidden="true">{consent ? 2 : 1}</span>
                <strong>{S.apkUpdate.updateTitle}</strong>
              </div>
              <p className="dk-small dk-muted">{S.apkUpdate.updateBody(offer.version)}</p>
              {installing ? (
                <p className="dk-small" role="status" data-testid="apk-installing">{S.apkUpdate.installing}</p>
              ) : (
                <>
                  {view?.install === 'confirm' && <p className="dk-small" role="status" data-testid="apk-confirm">{S.apkUpdate.confirm}</p>}
                  <Button
                    size="sm"
                    variant="primary"
                    disabled={consent || phase === 'downloading'}
                    busy={busy}
                    onClick={() => void updateNow()}
                    data-update=""
                    data-testid="apk-update-now"
                  >
                    {S.apkUpdate.updateNow}
                  </Button>
                </>
              )}
            </li>
          </ol>
          <p className="dk-small dk-muted">{S.apkUpdate.androidNote}</p>
        </>
      )}
      <div className="lnr-row lnr-dialog__actions">
        {offer && b.openReleaseNotes && (
          <Button size="sm" variant="ghost" onClick={() => void b.openReleaseNotes?.(offer.version)}>
            {S.apkUpdate.releaseNotes}
          </Button>
        )}
        <span className="lnr-grow" />
        <Button size="sm" onClick={onClose} data-testid="apk-later">
          {offer ? S.apkUpdate.later : S.apkUpdate.close}
        </Button>
      </div>
    </Dialog>
  )
}
