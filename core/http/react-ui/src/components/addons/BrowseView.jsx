/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import ConfirmDialog from '../ConfirmDialog'
import CatalogView from '../catalog/CatalogView'
import { paramsToQuery, queryToParams } from '../catalog/catalogState'
import { AppIcon, Fact, InstallSteps, UnknownNotice } from './AddonBits'
import FlowStatus from './InstallFlow'
import InstallDialog from './InstallDialog'
import { shortDigest } from './addonState'
import { updateView } from './updateState'
import { describeError, describeOutcome } from './errors'
import { useAddonActions } from './useAddonActions'

// Library entries for this host. An add-on entry has one button, Install and
// turn on: one confirmation, then the install and the turn on run one after
// the other. An add-on that is installed but off offers Turn on. A backend has
// no on and off, so its button stays Install.
export default function BrowseView({ api, adapter }) {
  const { t } = useTranslation('addons')
  const [params, setParams] = useSearchParams()
  const [installed, setInstalled] = useState({})
  // Backends are installed by the backend code of the host, not by the add-on
  // owner, so they have their own installed list and their own install state.
  const [installedBackends, setInstalledBackends] = useState({})
  const [backendWork, setBackendWork] = useState({})
  const [reload, setReload] = useState(0)
  const [confirm, setConfirm] = useState(null)
  const [dialog, setDialog] = useState(null)
  const query = paramsToQuery(k => params.get(k))

  const onQueryChange = useCallback((next) => {
    setParams(prev => {
      const out = new URLSearchParams(prev)
      for (const key of ['q', 'source', 'page']) out.delete(key)
      for (const [k, v] of Object.entries(queryToParams(next))) out.set(k, v)
      return out
    }, { replace: true })
  }, [setParams])

  // What is already installed, so an entry says so instead of offering it twice.
  useEffect(() => {
    const ctl = new AbortController()
    api.installed(ctl.signal)
      .then(list => setInstalled(Object.fromEntries(list.map(a => [a.id, a]))))
      .catch(() => { if (!ctl.signal.aborted) setInstalled({}) })
    return () => ctl.abort()
  }, [api, reload])

  useEffect(() => {
    const ctl = new AbortController()
    api.backends.installed(ctl.signal)
      .then(list => setInstalledBackends(Object.fromEntries(list.map(b => [b.id, b]))))
      .catch(() => { if (!ctl.signal.aborted) setInstalledBackends({}) })
    return () => ctl.abort()
  }, [api, reload])

  const actions = useAddonActions(api, { onSettled: () => setReload(n => n + 1), adapter })

  const installBackend = async (entry) => {
    setConfirm(null)
    const key = `install:${entry.key}`
    setBackendWork(w => ({ ...w, [key]: { phase: 'working', job: { phase: 'resolving' } } }))
    try {
      const job = await api.backends.install(
        { sourceId: entry.sourceId, name: entry.id, version: entry.version }, undefined,
        { onStatus: status => setBackendWork(w => ({ ...w, [key]: { phase: 'working', job: status } })) },
      )
      setBackendWork(w => ({ ...w, [key]: { phase: 'done', job } }))
    } catch (error) {
      setBackendWork(w => ({ ...w, [key]: { phase: 'failed', error, job: { phase: error.phase, processed: true, error: error.message } } }))
    }
    setReload(n => n + 1)
  }

  // A backend has its own job and its own confirmation. An add-on goes through
  // the one dialog of Install and turn on, which stays open for the steps.
  const install = (entry) => installBackend(entry)
  const openInstall = (entry) => { if (entry.kind === 'backend') setConfirm(entry); else setDialog(entry) }

  const renderEntry = (entry) => {
    const isBackend = entry.kind === 'backend'
    const have = isBackend ? installedBackends[entry.id] : installed[entry.id]
    const key = `install:${entry.key}`
    const bw = isBackend ? backendWork[key] : null
    const flow = isBackend ? null : actions.flows[`flow:${entry.key}`]
    const pending = !isBackend && Boolean(actions.unknown[entry.id])
    const working = isBackend ? bw?.phase === 'working' : ['install', 'turn-on'].includes(flow?.step)
    const addsPage = Boolean(entry.ui && entry.ui.mainPage)
    const off = !isBackend && have && have.enabled === false
    return (
      <article className="ad-entry" aria-labelledby={`entry-${entry.key}`}>
        <AppIcon src="" />
        <div className="ad-entry__body">
          <h3 className="ad-entry__title" id={`entry-${entry.key}`}>{entry.title}</h3>
          <p className="ad-entry__meta">
            <span className="dk-mono">{entry.id}</span>
            <span aria-hidden="true">·</span>
            <span>{t('apps.version', { version: entry.version })}</span>
            <span aria-hidden="true">·</span>
            <span>{t('browse.fromLibrary', { library: entry.sourceId })}</span>
          </p>
          {entry.description && <p className="ad-entry__text">{entry.description}</p>}
          <div className="ad-facts">
            {isBackend && <Fact icon="cpu"><span data-testid="entry-kind" data-kind={entry.kind}>{t(`browse.kind.${entry.kind}`, { defaultValue: entry.kind })}</span></Fact>}
            {!isBackend && addsPage && <Fact icon="file"><span data-testid="entry-adds">{t('card.addsPage')}</span></Fact>}
            {isBackend && have && <Fact icon="check-circle" tone="ok">{t('browse.installedBackend')}</Fact>}
            {!isBackend && have && <Fact icon="check-circle" tone="ok">{have.version === entry.version ? t('browse.installedSame', { version: have.version }) : t('browse.installedOther', { version: have.version })}</Fact>}
            {!isBackend && have && updateView(have.update).state === 'update-available' && <Fact icon="arrow-up" tone="warn">{t('updates.badge.available', { version: updateView(have.update).version })}</Fact>}
          </div>
          <EntryFacts entry={entry} have={have} isBackend={isBackend} />
          {pending && <UnknownNotice addonId={entry.id} actions={actions} />}
          {!pending && !(dialog && dialog.key === entry.key) && <FlowStatus flow={flow} name={entry.title} onRetry={flow?.step === 'failed' ? () => actions.installAndTurnOn(entry, { skipInstall: flow.failedStep === 'turn-on' }) : undefined} />}
          {flow && flow.step === 'done' && !(dialog && dialog.key === entry.key) && <p className="ad-entry__note" data-tone="ok" role="status">{t('browse.installedOn', { name: entry.title })}</p>}
          {bw && bw.phase !== 'done' && <InstallSteps job={bw.job} signed={entry.policy.requireSignature} />}
          {bw?.phase === 'failed' && (
            <p className="ad-entry__note" data-tone="error" role="alert">
              {t('browse.backendFailed')} {(bw.error?.kind === 'install' || bw.error?.status >= 400) && bw.error.message && bw.error.message !== bw.error.kind ? bw.error.message : describeError(bw.error, t)}
            </p>
          )}
          {bw?.phase === 'done' && <p className="ad-entry__note" data-tone="ok" role="status">{t('browse.installedBackendNote')}</p>}
        </div>
        <div className="ad-entry__acts">
          {isBackend && !have && (
            <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={working} onClick={() => setConfirm(entry)}>
              <Icon name={working ? 'spinner' : 'download'} spin={working} /> {working ? t('browse.installing') : t('browse.install')}
            </button>
          )}
          {!isBackend && !have && (
            <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={working || pending} onClick={() => openInstall(entry)}>
              <Icon name={working ? 'spinner' : 'download'} spin={working} /> {t('flow.installAndTurnOn')}
            </button>
          )}
          {off && (
            <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={working || pending} onClick={() => actions.installAndTurnOn(entry, { skipInstall: true })}>
              <Icon name={working ? 'spinner' : 'toggle-on'} spin={working} /> {t('apps.start')}
            </button>
          )}
        </div>
      </article>
    )
  }

  return (
    <>
      <CatalogView
        adapter={adapter}
        query={query}
        onQueryChange={onQueryChange}
        renderEntry={renderEntry}
        reloadToken={reload}
        describeError={e => describeError(e, t)}
        searchLabel={t('browse.search')}
        emptyTitle={t('browse.emptyTitle')}
        emptyText={t('browse.emptyBody')}
      />
      <InstallDialog entry={dialog} flow={dialog ? actions.flows[`flow:${dialog.key}`] : undefined} actions={actions} onClose={() => setDialog(null)} />
      <ConfirmDialog
        open={Boolean(confirm)}
        title={confirm ? t('browse.confirmTitle', { name: confirm.title }) : ''}
        message={confirm ? (
          <div className="ad-confirm">
            <p>{t('browse.confirmBackendBody', { version: confirm.version, library: confirm.sourceId })}</p>
            <dl className="dk-kv">
              <dt>{t('trust.digest')}</dt><dd className="dk-mono">{shortDigest(confirm.digest)}</dd>
              <dt>{t('trust.verification')}</dt><dd>{confirm.policy.requireSignature ? t('trust.signatureRequired') : t('trust.digestOnly')}</dd>
            </dl>
            <p>{t('browse.confirmBackendNote')}</p>
          </div>
        ) : null}
        confirmLabel={t('browse.install')}
        onConfirm={() => { const e = confirm; setConfirm(null); install(e) }}
        onCancel={() => setConfirm(null)}
      />
    </>
  )
}

// The details of an entry, folded into the entry itself: what it is made for,
// how it is verified and which build it is. They come from the library entry
// the list already has.
function EntryFacts({ entry: e, have, isBackend }) {
  const { t } = useTranslation('addons')
  return (
    <details className="ad-entry__details" data-testid="entry-details">
      <summary>{t('browse.details')}</summary>
      <dl className="dk-kv ad-kv">
        <dt>{t('browse.kindLabel')}</dt><dd data-testid="details-kind">{t(`browse.kind.${e.kind}`, { defaultValue: e.kind })}</dd>
        <dt>{t('trust.version')}</dt><dd>{e.version}</dd>
        {e.alias && (<><dt>{t('browse.alias')}</dt><dd className="dk-mono">{e.alias}</dd></>)}
        <dt>{t('trust.library')}</dt><dd>{e.sourceId}</dd>
        {e.channel && (<><dt>{t('trust.channel')}</dt><dd>{e.channel}</dd></>)}
        <dt>{t('trust.platform')}</dt><dd>{e.platform.os}/{e.platform.arch}</dd>
        <dt>{t('trust.verification')}</dt><dd>{e.policy.requireSignature ? t('trust.signatureRequired') : t('trust.digestOnly')}</dd>
        {e.policy.requireSignature && (<><dt>{t('trust.issuer')}</dt><dd className="dk-mono ad-wrap">{e.policy.issuer}</dd><dt>{t('trust.identity')}</dt><dd className="dk-mono ad-wrap">{e.policy.identity || e.policy.identityRegex}</dd></>)}
        <dt>{t('trust.digest')}</dt><dd className="dk-mono ad-wrap">{e.digest}</dd>
        {!isBackend && have && (<><dt>{t('browse.installedState')}</dt><dd>{t('browse.installedDetail', { version: have.version, generation: have.generation })}</dd></>)}
      </dl>
    </details>
  )
}
