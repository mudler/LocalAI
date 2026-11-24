/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, Callout } from '../ui'
import InstallDialog from './InstallDialog'
import { describeError, describeOutcome } from './errors'

// Install from a file. The button takes the add-on zip that `make addons-zip`
// writes; the owner checks it and the person sees the same dialog as for a
// library entry. The state lives in useFileInstall, which the page keeps above
// its tabs.
export default function InstallFromFile({ install }) {
  const { t } = useTranslation('addons')
  const input = useRef(null)
  const { busy, error, entry, actions } = install
  const words = error ? (error.type === 'not_found' ? t('file.gone') : describeError(error, t)) : ''
  return (
    <>
      <input ref={input} type="file" accept=".zip,application/zip" hidden onChange={(e) => { const file = e.target.files && e.target.files[0]; e.target.value = ''; install.pick(file) }} aria-label={t('file.pick')} data-testid="install-file-input" />
      <Button icon="upload" busy={busy} disabled={busy} onClick={() => input.current && input.current.click()} data-testid="addons-install-file">
        {busy ? t('file.checking') : t('file.button')}
      </Button>
      {error && (
        <div className="ad-file-error" role="alert" data-testid="install-file-error">
          <Callout tone="error" actions={<Button size="sm" onClick={install.clearError}>{t('catalog.close')}</Button>}>
            {t('file.rejected')} {words}
          </Callout>
        </div>
      )}
      <InstallDialog entry={entry} installed={{}} flow={install.flow} actions={actions} onClose={install.close} />
    </>
  )
}

// What the install of a closed dialog is doing, on the page: a note while it
// runs, and a failure that stays until it is dismissed. The failure says why
// and how to try again.
export function FileInstallBanner({ install, onRetry }) {
  const { t } = useTranslation('addons')
  const { kept, keptFlow } = install
  if (!kept || !keptFlow) return null
  if (keptFlow.step === 'failed') {
    const reason = describeOutcome(keptFlow.outcome, t, keptFlow.error)
    return (
      <Callout
        tone="error"
        role="alert"
        className="ad-callout"
        testId="file-install-failed"
        data-app={kept.id}
        title={t('file.failedTitle', { name: kept.title })}
        actions={(
          <>
            <Button size="sm" icon="refresh" onClick={() => { onRetry?.(); install.retryKept() }} data-testid="file-install-retry">{t('catalog.retry')}</Button>
            <Button size="sm" variant="ghost" onClick={install.dismissKept} data-testid="file-install-dismiss">{t('catalog.close')}</Button>
          </>
        )}
      >
        {reason} {t('file.failedHint')}
      </Callout>
    )
  }
  return <Callout tone="info" role="status" className="ad-callout" testId="file-install-running" data-app={kept.id}>{t('file.progress', { name: kept.title })}</Callout>
}
