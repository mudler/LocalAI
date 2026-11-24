import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import '../components/tools/tools.css'

// AmbiguityAlert renders the inline picker shown when the import endpoint
// returns a 400 with { modality, candidates }. It turns a failure into forward
// progress by letting the user pick one of the candidate backends inline: no
// separate dialog, no disappearing toast. A candidate that is not installed
// carries a download mark, so the pick is never an implicit download. The
// server sends no word on how the candidates differ, so the card shows only
// what the backend list knows: its description, when it has one.
const MODALITIES = ['tts', 'asr', 'embeddings', 'image', 'reranker', 'detection']

export default function AmbiguityAlert({ modality, candidates = [], knownBackends = [], onPick, onDismiss }) {
  const { t } = useTranslation('importModel')
  const known = new Map((knownBackends || []).filter(Boolean).map(b => [b.name, b]))
  const message = MODALITIES.includes(modality)
    ? t(`ambiguity.${modality}`)
    : t('ambiguity.other', { modality: modality || 'unknown' })

  return (
    <section className="import-ambiguity dk-card" data-testid="ambiguity-alert" role="status">
      <span className="import-ambiguity__mark"><Icon name="warning" /></span>
      <div className="import-ambiguity__main">
        <h2 className="import-ambiguity__title">{t('ambiguity.title')}</h2>
        <p className="import-ambiguity__text">{message}</p>
        {candidates.length > 0 && (
          <ul className="import-ambiguity__list">
            {candidates.map(name => {
              const backend = known.get(name)
              const installed = !!backend?.installed
              return (
                <li key={name} className="import-ambiguity__item">
                  <div>
                    <span className="dk-mono import-ambiguity__name">{name}</span>
                    {backend?.description && <span className="dk-hint">{backend.description}</span>}
                    <span className="dk-hint">{installed ? t('ambiguity.installed') : t('ambiguity.notInstalled')}</span>
                  </div>
                  <button
                    type="button"
                    className="dk-btn dk-btn--secondary dk-btn--sm"
                    data-testid={`ambiguity-chip-${name}`}
                    onClick={() => onPick && onPick(name)}
                    title={installed ? t('ambiguity.useTitle', { name }) : t('ambiguity.useInstallTitle', { name })}
                  >
                    {!installed && <Icon name="download" aria-hidden="true" />} {t('ambiguity.use', { name })}
                  </button>
                </li>
              )
            })}
          </ul>
        )}
      </div>
      {onDismiss && (
        <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" data-testid="ambiguity-dismiss" onClick={onDismiss} aria-label={t('ambiguity.dismiss')} title={t('ambiguity.dismiss')}>
          <Icon name="close" />
        </button>
      )}
    </section>
  )
}
