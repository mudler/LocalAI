/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, Callout, EmptyState, SkeletonRows } from '../ui'
import { AppIcon, EntryAdds, VersionText } from './AddonBits'
import FlowStatus from './InstallFlow'
import InstallDialog from './InstallDialog'
import { featuredEntries } from './addonState'
import { describeError } from './errors'

// Nothing is installed yet. An administrator sees what add-ons are for, the
// way to find them, and three add-ons from the sources as rows with Install:
// one confirmation, then the install and the turn on run one after the other.
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
  const none = state.status === 'ready' && state.entries.length === 0

  return (
    <section className="ad-first" data-testid="addons-first-run" aria-labelledby="ad-first-title">
      <EmptyState
        title={<span id="ad-first-title">{t('apps.firstRun.title')}</span>}
        body={t('apps.firstRun.lead')}
        facts={[{ text: t('apps.firstRun.factChecked') }, { text: t('apps.firstRun.factRemove') }]}
        actions={(
          <>
            <Button variant="primary" icon="store" to="/app/addons?tab=browse" data-testid="addons-first-run-browse">{t('apps.firstRun.browse')}</Button>
            {none && <Button variant="ghost" to="/app/addons?tab=libraries">{t('tabs.libraries')}</Button>}
          </>
        )}
      />
      {state.status === 'loading' && <SkeletonRows count={3} label={t('catalog.loading')} />}
      {state.status === 'error' && <Callout tone="error" role="alert">{t('apps.firstRun.featuredFailed')} {describeError(state.error, t)}</Callout>}
      {none && <p className="ad-quiet ad-first__none" data-testid="first-run-none">{t('apps.firstRun.noFeatured')}</p>}
      {state.entries.length > 0 && (
        <>
          <h3 className="ad-first__label">{t('apps.firstRun.featuredLabel')}</h3>
          <ul className="ad-featured" aria-label={t('apps.firstRun.featuredLabel')} data-testid="featured">
            {state.entries.map(entry => (
              <li key={entry.key} className="ad-entry ad-featured__item" data-addon={entry.id}>
                <AppIcon src="" id={entry.id} name={entry.title} />
                <div className="ad-entry__body">
                  <div className="ad-entry__head"><h4 className="ad-entry__title">{entry.title}</h4></div>
                  {entry.description && <p className="ad-entry__text">{entry.description}</p>}
                  <div className="ad-entry__line">
                    <EntryAdds entry={entry} />
                    <span className="ad-meta"><span className="ad-meta__item"><VersionText version={entry.version} /></span></span>
                  </div>
                  <FlowStatus flow={confirm && confirm.key === entry.key ? undefined : flowOf(entry)} name={entry.title} onRetry={flowOf(entry)?.step === 'failed' ? () => actions.installAndTurnOn(entry, { skipInstall: flowOf(entry).failedStep === 'turn-on' }) : undefined} />
                </div>
                <div className="ad-entry__acts">
                  <Button size="sm" icon="download" busy={busy(entry)} disabled={busy(entry)} onClick={() => setConfirm(entry)} aria-label={t('flow.installNamed', { name: entry.title })}>{t('flow.install')}</Button>
                </div>
              </li>
            ))}
          </ul>
        </>
      )}
      <InstallDialog entry={confirm} flow={confirm ? actions.flows[`flow:${confirm.key}`] : undefined} actions={actions} onClose={() => setConfirm(null)} />
    </section>
  )
}
