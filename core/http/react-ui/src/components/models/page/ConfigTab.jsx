import { useMemo } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import { fromState } from '../../../utils/editorNav'
import { PLACEMENT_KEYS, usePlacementConfig } from '../../../hooks/usePlacementConfig'
import Icon from '../../Icon'
// eslint-disable-next-line no-unused-vars
import PlacementSection from '../placement/PlacementSection'

// The configuration file as it would read after saving, with the lines the
// Placement section writes set apart. Plain text in a scrolling box: it is a
// preview, not an editor.
// eslint-disable-next-line no-unused-vars
function GeneratedFile({ text, t }) {
  const lines = useMemo(() => text.replace(/\n$/, '').split('\n'), [text])
  return (
    <div className="modelpage-yaml" data-testid="placement-file">
      <span className="dk-eyebrow">{t('page.config.generated')}</span>
      <pre className="modelpage-yaml__body" tabIndex={0} aria-label={t('page.config.generated')}>
        {lines.map((line, i) => {
          const key = /^([A-Za-z_][\w-]*):/.exec(line)?.[1]
          const written = key && PLACEMENT_KEYS.includes(key)
          return (
            <span key={i} className={`modelpage-yaml__line${written ? ' modelpage-yaml__line--written' : ''}`} data-written={written ? 'true' : undefined}>
              {line}{'\n'}
            </span>
          )
        })}
      </pre>
    </div>
  )
}

// Configuration: the Placement section on its own, with a save bar, and a way
// into the full editor for everything else. It edits the same file the editor
// does, through the same patch endpoint.
export default function ConfigTab({ view }) {
  const { t, id, running, location } = view
  const config = usePlacementConfig(id, {
    onSaved: () => view.toast?.(t('page.config.saved'), 'success'),
    onError: err => view.toast?.(t('page.config.saveFailed', { message: err.message }), 'error'),
  })

  if (config.status === 'loading') {
    return (
      <div className="modelpage-config" data-testid="model-page-config">
        <span className="dk-skeleton dk-skeleton--block modelpage-fit__skeleton-block" role="status" aria-label={t('page.config.loading')} />
      </div>
    )
  }
  if (config.status === 'error') {
    return (
      <div className="modelpage-config" data-testid="model-page-config">
        <div className="ledger-banner ledger-banner--error" role="alert" data-testid="config-error">
          <Icon name="alert-circle" />
          <span>{t('page.config.loadFailed', { message: config.error })}</span>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={config.reload}>
            <Icon name="refresh" /> {t('ledger.retry')}
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="modelpage-config" data-testid="model-page-config">
      <PlacementSection
        model={id}
        values={config.values}
        onChange={config.setValue}
        headerAction={(
          <Link
            className="dk-link"
            to={`/app/model-editor/${encodeURIComponent(id)}`}
            state={fromState(location, t('page.config.thisModel'))}
            data-testid="open-full-editor"
          >
            <Icon name="edit" className="icon-before" />{t('page.config.fullEditor')}
          </Link>
        )}
      />
      <GeneratedFile text={config.preview} t={t} />
      <div className="modelpage-config__save">
        <button
          type="button"
          className={`dk-btn ${config.dirty ? 'dk-btn--primary' : 'dk-btn--secondary'}`}
          disabled={!config.dirty || config.saving}
          aria-busy={config.saving || undefined}
          onClick={config.save}
          data-testid="config-save"
        >
          <Icon name={config.saving ? 'spinner' : config.dirty ? 'save' : 'check'} spin={config.saving} />
          {config.saving ? t('page.config.saving') : config.dirty ? t('page.config.save') : t('page.config.savedLabel')}
        </button>
        {running && <span className="dk-hint">{t('page.config.appliesNext')}</span>}
      </div>
    </div>
  )
}
