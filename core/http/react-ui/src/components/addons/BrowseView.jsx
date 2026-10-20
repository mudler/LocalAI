/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import ConfirmDialog from '../ConfirmDialog'
import SideSheet from '../SideSheet'
import CatalogView from '../catalog/CatalogView'
import { paramsToQuery, queryToParams } from '../catalog/catalogState'
import { AppIcon, Fact, UnknownNotice } from './AddonBits'
import { shortDigest } from './addonState'
import { updateView } from './updateState'
import { describeError, describeOutcome } from './errors'
import { useAddonActions } from './useAddonActions'

// Library entries for this host. Install does not activate: the add-on is
// installed and stays disabled until an administrator enables it on the
// Installed tab, so every step that starts a process is its own decision.
export default function BrowseView({ api, adapter }) {
  const { t } = useTranslation('addons')
  const [params, setParams] = useSearchParams()
  const [installed, setInstalled] = useState({})
  const [reload, setReload] = useState(0)
  const [confirm, setConfirm] = useState(null)
  const [details, setDetails] = useState(null)
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

  const actions = useAddonActions(api, { onSettled: () => setReload(n => n + 1) })

  const install = (entry) => {
    setConfirm(null)
    actions.run(`install:${entry.key}`, {
      addonId: entry.id,
      submit: (operationId, signal) => adapter.submitLifecycle({ operationId, action: 'install', addonId: entry.id, sourceId: entry.sourceId, version: entry.version }, signal),
      // A completed status is not proof for this click: the owner answers a
      // replayed operation with its old status and applies nothing.
      verify: async (signal) => (await api.installed(signal)).some(a => a.id === entry.id),
    })
  }

  const renderEntry = (entry) => {
    const have = installed[entry.id]
    const key = `install:${entry.key}`
    const st = actions.state[key]
    const pending = Boolean(actions.unknown[entry.id])
    const working = st?.phase === 'working'
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
            <Fact icon="cpu">{entry.platform.os}/{entry.platform.arch}</Fact>
            <Fact icon={entry.policy.requireSignature ? 'shield' : 'info'} tone={entry.policy.requireSignature ? 'ok' : undefined}>
              {entry.policy.requireSignature ? t('trust.signatureRequired') : t('trust.digestOnly')}
            </Fact>
            {entry.channel && <Fact icon="tag">{entry.channel}</Fact>}
            {have && <Fact icon="check-circle" tone="ok">{have.version === entry.version ? t('browse.installedSame', { version: have.version }) : t('browse.installedOther', { version: have.version })}</Fact>}
            {have && updateView(have.update).state === 'update-available' && <Fact icon="arrow-up" tone="warn">{t('updates.badge.available', { version: updateView(have.update).version })}</Fact>}
          </div>
          {pending && <UnknownNotice addonId={entry.id} actions={actions} />}
          {!pending && st && !working && st.outcome !== 'completed' && (
            <p className="ad-entry__note" data-tone="error" role="alert">{describeOutcome(st.outcome, t)}</p>
          )}
          {st && st.outcome === 'completed' && <p className="ad-entry__note" data-tone="ok" role="status">{t('browse.installedNotEnabled')}</p>}
        </div>
        <div className="ad-entry__acts">
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => setDetails(entry)}>
            <Icon name="info" /> {t('browse.details')}
          </button>
          <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={working || pending || Boolean(have)} onClick={() => setConfirm(entry)}>
            <Icon name={working ? 'spinner' : 'download'} spin={working} /> {working ? t('browse.installing') : t('browse.install')}
          </button>
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
      {details && <EntryDetails adapter={adapter} entry={details} onClose={() => setDetails(null)} />}
      <ConfirmDialog
        open={Boolean(confirm)}
        title={confirm ? t('browse.confirmTitle', { name: confirm.title }) : ''}
        message={confirm ? (
          <div className="ad-confirm">
            <p>{t('browse.confirmBody', { version: confirm.version, library: confirm.sourceId })}</p>
            <dl className="dk-kv">
              <dt>{t('trust.digest')}</dt><dd className="dk-mono">{shortDigest(confirm.digest)}</dd>
              <dt>{t('trust.verification')}</dt><dd>{confirm.policy.requireSignature ? t('trust.signatureRequired') : t('trust.digestOnly')}</dd>
            </dl>
            <p>{t('browse.confirmNotEnabled')}</p>
          </div>
        ) : null}
        confirmLabel={t('browse.install')}
        onConfirm={() => install(confirm)}
        onCancel={() => setConfirm(null)}
      />
    </>
  )
}

function EntryDetails({ adapter, entry, onClose }) {
  const { t } = useTranslation('addons')
  const [state, setState] = useState({ status: 'loading', entry: null, error: null })
  useEffect(() => {
    const ctl = new AbortController()
    adapter.details(entry.key, ctl.signal)
      .then(full => setState({ status: 'ready', entry: full, error: null }))
      .catch(error => { if (!ctl.signal.aborted) setState({ status: 'error', entry: null, error }) })
    return () => ctl.abort()
  }, [adapter, entry.key])
  const e = state.entry || entry
  return (
    <SideSheet title={e.title} description={t('browse.detailsLead', { id: e.id })} onClose={onClose} closeLabel={t('catalog.close')} labelId="entry-details-title">
      {state.status === 'error' && <p className="op-error" role="alert">{describeError(state.error, t)}</p>}
      <dl className="dk-kv ad-kv">
        <dt>{t('trust.version')}</dt><dd>{e.version}</dd>
        <dt>{t('trust.library')}</dt><dd>{e.sourceId}</dd>
        {e.channel && (<><dt>{t('trust.channel')}</dt><dd>{e.channel}</dd></>)}
        <dt>{t('trust.platform')}</dt><dd>{e.platform.os}/{e.platform.arch}</dd>
        <dt>{t('trust.verification')}</dt><dd>{e.policy.requireSignature ? t('trust.signatureRequired') : t('trust.digestOnly')}</dd>
        {e.policy.requireSignature && (<><dt>{t('trust.issuer')}</dt><dd className="dk-mono ad-wrap">{e.policy.issuer}</dd><dt>{t('trust.identity')}</dt><dd className="dk-mono ad-wrap">{e.policy.identity}</dd></>)}
        <dt>{t('trust.digest')}</dt><dd className="dk-mono ad-wrap">{e.digest}</dd>
        {e.installed && (<><dt>{t('browse.installedState')}</dt><dd>{t('browse.installedDetail', { version: e.installed.version, generation: e.installed.generation })}</dd></>)}
      </dl>
      {e.installed && updateView(e.installed.update).state === 'update-available' && (
        <p className="ad-entry__note" role="status" data-testid="details-update">{t('updates.browseUpdate', { version: updateView(e.installed.update).version })}</p>
      )}
      {e.description && <p className="ad-entry__text">{e.description}</p>}
    </SideSheet>
  )
}
