/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Callout } from '../ui'

// What the inspector says above an online model, when there is something to
// say: access, listing or sizing problems. Each is one callout; none blocks
// the page.
//
//   files      { loading } | { data } | { error, message } of the repository
//   estimate   the remote estimate, or { error }
export default function RemoteNotice({ row, files, estimate, onRetry, t }) {
  const data = files?.data
  const notes = []
  if (files?.error) {
    notes.push(
      <Callout
        key="files"
        tone={files.error === 'rateLimited' ? 'warn' : 'error'}
        testId="remote-files-error"
        title={t(`remote.files.${files.error}.title`)}
        actions={<button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={onRetry}>{t('ledger.retry')}</button>}
      >
        {t(`remote.files.${files.error}.body`)}
      </Callout>,
    )
  }
  if (data?.gated || row.gated) {
    notes.push(
      <Callout key="gated" tone="warn" testId="remote-gated" title={t('remote.gatedTitle')}>
        {t(row.provider === 'pirateface' ? 'remote.gatedP2p' : 'remote.gatedBody')}
      </Callout>,
    )
  }
  if (data && data.size_known === false) {
    notes.push(
      <Callout key="sizes" tone="info" testId="remote-sizes-unknown">{t('remote.sizesUnknown')}</Callout>,
    )
  }
  if (data?.import_error) {
    notes.push(
      <Callout key="import" tone="warn" testId="remote-import-error" title={t('remote.importError')}>
        {t('remote.importErrorBody')}
      </Callout>,
    )
  }
  if (estimate && !estimate.error && estimate.approximate) {
    // The figure is size plus a rule of thumb per token: say so where the
    // figure is read, not only in the docs.
    notes.push(
      <Callout key="approx" tone="info" testId="remote-approximate">{t('remote.approximate')}</Callout>,
    )
  }
  if (notes.length === 0) return null
  return <div className="remote-notices">{notes}</div>
}
