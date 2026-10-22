import { useTranslation } from 'react-i18next'
import Icon from '../Icon'

// A workspace opened from the Studio front page with a result as its starting
// point says so, and says plainly when that result could not be loaded. It
// renders nothing when the page was opened any other way.
//
//   source   what useHandoffSource() returned
//   handoff  what useStudioHandoff() returned
//   onClear  drop the source and keep the rest of the form
export default function HandoffNote({ source, handoff, onClear, wanted = true }) {
  const { t } = useTranslation('media')
  if (!handoff.from || !wanted || source.status === 'none') return null
  const name = source.item?.title || t('studio.handoff.aResult')
  const failed = source.status === 'missing' || source.status === 'error'
  return (
    <div className={`studio-handoff${failed ? ' studio-handoff--failed' : ''}`} role={failed ? 'alert' : 'status'} data-testid="studio-handoff" data-status={source.status}>
      <Icon name={failed ? 'alert-circle' : 'link'} />
      <span className="studio-handoff__text">
        {source.status === 'loading' && t('studio.handoff.loading', { name })}
        {source.status === 'ready' && t('studio.handoff.ready', { name })}
        {source.status === 'missing' && t('studio.handoff.missing')}
        {source.status === 'error' && t('studio.handoff.error', { name })}
      </span>
      {onClear && source.status === 'ready' && (
        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={onClear}>{t('studio.handoff.remove')}</button>
      )}
    </div>
  )
}
