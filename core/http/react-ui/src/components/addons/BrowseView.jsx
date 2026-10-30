/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import CatalogView from '../catalog/CatalogView'
import { paramsToQuery, queryToParams } from '../catalog/catalogState'
import { AppIcon, UnknownNotice, VersionText } from './AddonBits'
import { Button, StatusChip } from '../ui'
import FlowStatus from './InstallFlow'
import InstallDialog from './InstallDialog'
import { addedByEntry } from './addonState'
import { updateView } from './updateState'
import { describeError, describeOutcome } from './errors'
import { useAddonActions } from './useAddonActions'
import { useAddonUpgrade } from './useAddonUpgrade'
import { AddonUpgradeAction, UpgradeConfirm } from './UpgradeControls'
import { UpdateBadge } from '../upgrade/UpgradeBits'

// Library entries for this host. An add-on entry has one button, Install and
// turn on: one confirmation, then the install and the turn on run one after
// the other. An add-on that is installed but off offers Turn on. Backends are
// not listed here: they belong to the Backends page.
export default function BrowseView({ api, adapter }) {
  const { t } = useTranslation('addons')
  const [params, setParams] = useSearchParams()
  const [installed, setInstalled] = useState({})
  const [reload, setReload] = useState(0)
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

  const actions = useAddonActions(api, { onSettled: () => setReload(n => n + 1), adapter })

  const upgrade = useAddonUpgrade(api, actions, { onChecked: () => setReload(n => n + 1) })

  const openInstall = (entry) => setDialog(entry)

  const renderEntry = (entry) => {
    const have = installed[entry.id]
    const flow = actions.flows[`flow:${entry.key}`]
    const pending = Boolean(actions.unknown[entry.id])
    const working = ['install', 'turn-on'].includes(flow?.step)
    const adds = addedByEntry(entry)
    const off = have && have.enabled === false
    const updatable = have && updateView(have.update).state === 'update-available'
    return (
      <article className="ad-entry" aria-labelledby={`entry-${entry.key}`} data-addon={entry.id}>
        <AppIcon src="" id={entry.id} name={entry.title} />
        <div className="ad-entry__body">
          <div className="ad-entry__head">
            <h3 className="ad-entry__title" id={`entry-${entry.key}`}>{entry.title}</h3>
            {have && have.version === entry.version && <StatusChip tone="ok" label={t('browse.installedChip')} testId="entry-installed" />}
            {updatable && <UpdateBadge version={updateView(have.update).version} />}
          </div>
          {entry.description && <p className="ad-entry__text">{entry.description}</p>}
          <p className="ad-meta ad-entry__meta">
            {adds.page && <span className="ad-meta__item"><Icon name="file" aria-hidden="true" /> <span data-testid="entry-adds">{t('card.addsPage')}</span></span>}
            {adds.tools > 0 && <span className="ad-meta__item"><Icon name="wrench" aria-hidden="true" /> <span data-testid="entry-tools">{t('card.addsTools', { count: adds.tools })}</span></span>}
            <span className="ad-meta__item"><Icon name="shield" aria-hidden="true" /> {t(entry.policy && entry.policy.requireSignature ? 'meta.signed' : 'meta.checked', { library: entry.sourceId })}</span>
            <span className="ad-meta__item"><VersionText version={entry.version} /></span>
            {have && have.version !== entry.version && <span className="ad-meta__item" data-testid="entry-have">{t('browse.installedOther', { version: have.version })}</span>}
          </p>
          {updatable && (
            <div className="ad-entry__upgrade" data-testid="entry-upgrade">
              <AddonUpgradeAction addon={have} pending isAdmin upgrade={upgrade} variant="secondary" working={pending || actions.state[`upgrade:${have.id}:${have.generation}`]?.phase === 'working'} />
            </div>
          )}
          <EntryFacts entry={entry} have={have} />
          {pending && <UnknownNotice addonId={entry.id} actions={actions} />}
          {!pending && !(dialog && dialog.key === entry.key) && <FlowStatus flow={flow} name={entry.title} onRetry={flow?.step === 'failed' ? () => actions.installAndTurnOn(entry, { skipInstall: flow.failedStep === 'turn-on' }) : undefined} />}
          {flow && flow.step === 'done' && !(dialog && dialog.key === entry.key) && <p className="ad-entry__note" data-tone="ok" role="status"><Icon name="check-circle" aria-hidden="true" /> {t('browse.installedOn', { name: entry.title })}</p>}
        </div>
        <div className="ad-entry__acts">
          {!have && (
            <Button size="sm" icon="download" busy={working} disabled={working || pending} onClick={() => openInstall(entry)} aria-label={t('flow.installNamed', { name: entry.title })}>{t('flow.install')}</Button>
          )}
          {off && (
            <Button size="sm" icon="toggle-on" busy={working} disabled={working || pending} onClick={() => actions.installAndTurnOn(entry, { skipInstall: true })}>{t('apps.start')}</Button>
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
      <UpgradeConfirm upgrade={upgrade} />
      <InstallDialog entry={dialog} flow={dialog ? actions.flows[`flow:${dialog.key}`] : undefined} actions={actions} onClose={() => setDialog(null)} />
    </>
  )
}

// The details of an entry, folded into the entry itself: what it is made for,
// how it is verified and which build it is. They come from the library entry
// the list already has.
function EntryFacts({ entry: e, have }) {
  const { t } = useTranslation('addons')
  return (
    <details className="ad-entry__details ad-disclosure" data-testid="entry-details">
      <summary><Icon name="chevron-right" aria-hidden="true" className="ad-disclosure__icon" /> {t('browse.details')}</summary>
      <dl className="dk-kv ad-kv">
        <dt>{t('preview.id')}</dt><dd className="dk-mono">{e.id}</dd>
        <dt>{t('browse.kindLabel')}</dt><dd data-testid="details-kind">{t(`browse.kind.${e.kind}`, { defaultValue: e.kind })}</dd>
        <dt>{t('trust.version')}</dt><dd>{e.version}</dd>
        {e.alias && (<><dt>{t('browse.alias')}</dt><dd className="dk-mono">{e.alias}</dd></>)}
        <dt>{t('trust.library')}</dt><dd>{e.sourceId}</dd>
        {e.channel && (<><dt>{t('trust.channel')}</dt><dd>{e.channel}</dd></>)}
        <dt>{t('trust.platform')}</dt><dd>{e.platform.os}/{e.platform.arch}</dd>
        <dt>{t('trust.verification')}</dt><dd>{e.policy.requireSignature ? t('trust.signatureRequired') : t('trust.digestOnly')}</dd>
        {e.policy.requireSignature && (<><dt>{t('trust.issuer')}</dt><dd className="dk-mono ad-wrap">{e.policy.issuer}</dd><dt>{t('trust.identity')}</dt><dd className="dk-mono ad-wrap">{e.policy.identity || e.policy.identityRegex}</dd></>)}
        <dt>{t('trust.digest')}</dt><dd className="dk-mono ad-wrap">{e.digest}</dd>
        {have && (<><dt>{t('browse.installedState')}</dt><dd>{t('browse.installedDetail', { version: have.version, generation: have.generation })}</dd></>)}
      </dl>
    </details>
  )
}
