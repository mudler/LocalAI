/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import LoadingSpinner from '../LoadingSpinner'
import { AppIcon } from './AddonBits'
import FlowStatus from './InstallFlow'
import InstallDialog from './InstallDialog'
import { featuredEntries } from './addonState'
import { describeError } from './errors'

// Nothing is installed yet. An administrator sees three add-ons from the
// sources, each with one button that installs it and turns it on, and a link
// to everything else.
export default function FirstRun({ adapter, actions }) {
  const { t } = useTranslation('addons')
  const [state, setState] = useState({ status: 'loading', entries: [], error: null })
  const [confirm, setConfirm] = useState(null)

  useEffect(() => {
    const ctl = new AbortController()
    adapter.search({ text: '', sourceId: '', page: 1 }, ctl.signal)
      .then(out => setState({ status: 'ready', entries: featuredEntries(out.entries), error: null }))
      .catch(error => { if (!ctl.signal.aborted) setState({ status: 'error', entries: [], error }) })
    return () => ctl.abort()
  }, [adapter])

  const flowOf = (entry) => actions.flows[`flow:${entry.key}`]
  const busy = (entry) => ['install', 'turn-on'].includes(flowOf(entry)?.step) || Boolean(actions.unknown[entry.id])

  return (
    <section className="ad-card ad-first" data-testid="addons-first-run" aria-labelledby="ad-first-title">
      <h2 className="ad-card__title" id="ad-first-title">{t('apps.firstRun.title')}</h2>
      <p className="ad-card__text">{t('apps.firstRun.lead')}</p>
      {state.status === 'loading' && <div className="op-loading" role="status" aria-label={t('catalog.loading')}><LoadingSpinner /></div>}
      {state.status === 'error' && <p className="ad-card__note" data-tone="error" role="alert">{t('apps.firstRun.featuredFailed')} {describeError(state.error, t)}</p>}
      {state.status === 'ready' && state.entries.length === 0 && <p className="ad-card__note" data-testid="first-run-none">{t('apps.firstRun.noFeatured')}</p>}
      {state.entries.length > 0 && (
        <ul className="ad-featured" aria-label={t('apps.firstRun.featuredLabel')} data-testid="featured">
          {state.entries.map(entry => (
            <li key={entry.key} className="ad-featured__item" data-addon={entry.id}>
              <div className="ad-card__head">
                <AppIcon src="" />
                <div className="ad-card__lead">
                  <h3 className="ad-card__title">{entry.title}</h3>
                  <p className="ad-card__meta">{t('apps.version', { version: entry.version })}</p>
                </div>
              </div>
              {entry.description && <p className="ad-card__text">{entry.description}</p>}
              {entry.ui && entry.ui.mainPage && <p className="ad-tile__adds"><span className="ad-tile__add"><Icon name="file" aria-hidden="true" /> {t('card.addsPage')}</span></p>}
              <FlowStatus flow={confirm && confirm.key === entry.key ? undefined : flowOf(entry)} name={entry.title} onRetry={flowOf(entry)?.step === 'failed' ? () => actions.installAndTurnOn(entry, { skipInstall: flowOf(entry).failedStep === 'turn-on' }) : undefined} />
              <div className="ad-card__acts">
                <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={busy(entry)} onClick={() => setConfirm(entry)}>
                  <Icon name={busy(entry) ? 'spinner' : 'download'} spin={busy(entry)} /> {t('flow.installAndTurnOn')}
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}
      <div className="ad-section__acts">
        <Link className="dk-btn dk-btn--sm" to="/app/addons?tab=browse" data-testid="addons-first-run-browse"><Icon name="store" /> {t('apps.firstRun.browse')}</Link>
        {state.status === 'ready' && state.entries.length === 0 && (
          <Link className="dk-btn dk-btn--ghost dk-btn--sm" to="/app/addons?tab=libraries">{t('tabs.libraries')}</Link>
        )}
      </div>
      <InstallDialog entry={confirm} flow={confirm ? actions.flows[`flow:${confirm.key}`] : undefined} actions={actions} onClose={() => setConfirm(null)} />
    </section>
  )
}
