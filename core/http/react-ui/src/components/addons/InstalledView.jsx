/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import LoadingSpinner from '../LoadingSpinner'
import ConfirmDialog from '../ConfirmDialog'
import SideSheet from '../SideSheet'
import CatalogEmpty from '../catalog/CatalogEmpty'
import { apiUrl } from '../../utils/basePath'
import { StateChip, AppIcon, Fact, UnknownNotice } from './AddonBits'
import { formatBytes, rangeText, shortDigest } from './addonState'
import { describeError, describeOutcome } from './errors'
import { useAddonActions } from './useAddonActions'
import { updateView, upgradeBody, formatWhen, UPDATE_TYPES } from './updateState'
import { newOperationId } from '../catalog/catalogState'
import { AddonsError } from '../../utils/addonsApi'
import RecoveryPanel from './RecoveryPanel'

// Everything the owner knows of what is installed: verified package facts,
// the desired state (enabled) and the observed one (health) apart, retained
// generations, data size and, when the owner can say so, an update. A headless
// add-on is managed here exactly like one with pages.
export default function InstalledView({ api, adapter, manage, onManage, onChanged }) {
  const { t, i18n } = useTranslation('addons')
  const [state, setState] = useState({ status: 'loading', addons: [], error: null })
  const [reload, setReload] = useState(0)
  const [confirm, setConfirm] = useState(null)
  const [confirmUpgrade, setConfirmUpgrade] = useState(null)
  // What the last check and the last upgrade of each add-on said, by add-on.
  const [checks, setChecks] = useState({})
  const [upgraded, setUpgraded] = useState({})
  const aborter = useRef(null)
  useEffect(() => {
    aborter.current = new AbortController()
    return () => aborter.current.abort()
  }, [])

  useEffect(() => {
    const ctl = new AbortController()
    api.installed(ctl.signal)
      .then(addons => setState({ status: 'ready', addons, error: null }))
      .catch(error => { if (!ctl.signal.aborted && error.name !== 'AbortError') setState(s => ({ status: s.status === 'ready' ? 'ready' : 'error', addons: s.addons, error })) })
    return () => ctl.abort()
  }, [api, reload])

  const actions = useAddonActions(api, {
    onSettled: (key, result) => {
      setReload(n => n + 1)
      if (onChanged) onChanged()
      // The key names the generation the click saw, which the list moves past
      // on success, so the outcome of an upgrade is kept by add-on.
      const [verb, id] = String(key).split(':')
      if (verb === 'upgrade' && result && result.outcome === 'completed') setUpgraded(u => ({ ...u, [id]: true }))
      if (verb === 'upgrade' && result && result.outcome !== 'completed') setUpgraded(u => { const n = { ...u }; delete n[id]; return n })
    },
  })

  const lifecycle = useCallback((addon, action) => {
    actions.run(`${action}:${addon.id}:${addon.generation}`, {
      addonId: addon.id,
      submit: (operationId, signal) => adapter.submitLifecycle({
        operationId,
        action,
        addonId: addon.id,
        generation: action === 'enable' || action === 'disable' ? addon.generation : 0,
      }, signal),
      // A completed status is not proof for this click: the owner answers a
      // replayed operation with its old status and applies nothing.
      verify: async (signal) => {
        const now = (await api.installed(signal)).find(a => a.id === addon.id)
        if (action === 'uninstall') return !now
        if (!now) return false
        if (action === 'enable') return now.enabled === true
        return now.enabled === false
      },
    })
  }, [actions, adapter, api])

  // The explicit upgrade: the digest of the installed package this page
  // showed and the digest of the candidate the owner's check resolved. The
  // owner checks both again and refuses before it changes anything.
  const upgrade = useCallback((addon) => {
    actions.run(`upgrade:${addon.id}:${addon.generation}`, {
      addonId: addon.id,
      submit: (operationId, signal) => {
        const body = upgradeBody(addon, operationId)
        if (!body) return Promise.reject(new AddonsError({ kind: 'invalid' }))
        return api.updates.upgrade(addon.id, body, signal)
      },
      verify: async (signal) => {
        const now = (await api.installed(signal)).find(a => a.id === addon.id)
        return Boolean(now) && now.generation > addon.generation
      },
    })
  }, [actions, api])

  // Asks the owner to read the library again. It never applies anything.
  const check = useCallback(async (addon) => {
    const signal = aborter.current.signal
    setChecks(c => ({ ...c, [addon.id]: { phase: 'working' } }))
    try {
      const status = await api.updates.check(addon.id, { operationId: newOperationId() }, signal)
      setChecks(c => ({ ...c, [addon.id]: { phase: 'done', throttled: Boolean(status && status.throttled) } }))
      setReload(n => n + 1)
    } catch (error) {
      if (error && error.name === 'AbortError') return
      setChecks(c => ({ ...c, [addon.id]: { phase: 'error', error } }))
    }
  }, [api])

  const selected = useMemo(() => state.addons.find(a => a.id === manage) || null, [state.addons, manage])

  if (state.status === 'loading') return <div className="op-loading" role="status" aria-label={t('installed.loading')}><LoadingSpinner size="lg" /></div>
  if (state.status === 'error') {
    return (
      <div className="op-error" role="alert">
        <Icon name="alert-circle" />
        <span>{describeError(state.error, t)}</span>
        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => { setState({ status: 'loading', addons: [], error: null }); setReload(n => n + 1) }}>
          <Icon name="refresh" /> {t('catalog.retry')}
        </button>
      </div>
    )
  }
  if (state.addons.length === 0) {
    return <CatalogEmpty icon="puzzle" title={t('installed.emptyTitle')} text={t('installed.emptyBody')} />
  }

  return (
    <>
      <ul className="cat-list" aria-label={t('installed.listLabel')}>
        {state.addons.map(addon => (
          <li key={addon.id} className="cat-item">
            <InstalledRow addon={addon} t={t} actions={actions} onLifecycle={lifecycle} onManage={onManage} upgraded={Boolean(upgraded[addon.id])} />
          </li>
        ))}
      </ul>
      {selected && (
        <ManageSheet
          addon={selected}
          api={api}
          t={t}
          language={i18n.language}
          actions={actions}
          check={checks[selected.id]}
          upgraded={Boolean(upgraded[selected.id])}
          onCheck={() => check(selected)}
          onUpgrade={() => setConfirmUpgrade(selected)}
          onLifecycle={lifecycle}
          onUninstall={() => setConfirm(selected)}
          onClose={() => onManage('')}
        />
      )}
      {manage && !selected && state.status === 'ready' && (
        <div className="op-notice" role="status"><Icon name="info" /><span>{t('installed.missing', { id: manage })}</span></div>
      )}
      <ConfirmDialog
        open={Boolean(confirm)}
        danger
        title={confirm ? t('installed.uninstallTitle', { name: confirm.title }) : ''}
        message={confirm ? <p>{t('installed.uninstallBody')}</p> : null}
        confirmLabel={t('installed.uninstall')}
        onConfirm={() => { const a = confirm; setConfirm(null); lifecycle(a, 'uninstall') }}
        onCancel={() => setConfirm(null)}
      />
      <ConfirmDialog
        open={Boolean(confirmUpgrade)}
        title={confirmUpgrade ? t('updates.confirm.title', { name: confirmUpgrade.title, version: updateView(confirmUpgrade.update).version }) : ''}
        message={confirmUpgrade ? <UpgradeSummary addon={confirmUpgrade} t={t} /> : null}
        confirmLabel={t('updates.confirm.action')}
        onConfirm={() => { const a = confirmUpgrade; setConfirmUpgrade(null); upgrade(a) }}
        onCancel={() => setConfirmUpgrade(null)}
      />
    </>
  )
}

function busy(actions, addon, verbs) {
  if (actions.unknown[addon.id]) return true
  return verbs.some(v => actions.state[`${v}:${addon.id}:${addon.generation}`]?.phase === 'working')
}

function latest(actions, addon, verbs) {
  for (const v of verbs) {
    const s = actions.state[`${v}:${addon.id}:${addon.generation}`]
    if (s && s.phase !== 'working' && s.outcome) return s
  }
  return null
}

// The words for the outcome of an action. A typed refusal of the update
// routes has its own words; everything else is the shared description.
function outcomeText(result, t) {
  const type = result && result.error && result.error.type
  if (type && UPDATE_TYPES.has(type)) return t(`updates.errors.${type}`)
  return describeOutcome(result.outcome, t)
}

function UpdateFact({ view, t }) {
  switch (view.state) {
    case 'update-available': return <Fact icon="arrow-up" tone="warn">{t('updates.badge.available', { version: view.version })}</Fact>
    case 'incompatible': return <Fact icon="alert-circle" tone="warn">{t('updates.badge.incompatible', { version: view.version })}</Fact>
    case 'check-failed': return <Fact icon="alert-circle" tone="error">{t('updates.badge.failed')}</Fact>
    case 'integrity-error': return <Fact icon="alert-circle" tone="error">{t('updates.badge.integrity')}</Fact>
    default: return null
  }
}

function InstalledRow({ addon, t, actions, onLifecycle, onManage, upgraded }) {
  const working = busy(actions, addon, ['enable', 'disable', 'upgrade', 'uninstall'])
  const result = latest(actions, addon, ['enable', 'disable', 'upgrade', 'uninstall'])
  const update = updateView(addon.update)
  return (
    <article className="ad-entry" aria-labelledby={`inst-${addon.id}`} data-addon={addon.id}>
      <AppIcon src={addon.icon ? apiUrl(addon.icon) : ''} />
      <div className="ad-entry__body">
        <h3 className="ad-entry__title" id={`inst-${addon.id}`}>{addon.title}</h3>
        <p className="ad-entry__meta">
          <span className="dk-mono">{addon.id}</span>
          <span aria-hidden="true">·</span>
          <span>{t('apps.version', { version: addon.version })}</span>
          <span aria-hidden="true">·</span>
          <span>{t('installed.generation', { generation: addon.generation })}</span>
        </p>
        <div className="ad-facts">
          <span className="ad-fact" data-tone={addon.enabled ? 'ok' : undefined}>{addon.enabled ? t('installed.enabled') : t('installed.disabled')}</span>
          <StateChip app={addon} />
          {addon.recovery && <Fact icon="alert-circle" tone={addon.recovery === 'needs-recovery' ? 'error' : 'warn'}>{t(`recovery.state.${addon.recovery}`)}</Fact>}
          <Fact icon={addon.verification === 'sha256+sigstore' ? 'shield' : 'info'} tone={addon.verification === 'sha256+sigstore' ? 'ok' : undefined}>{t(`trust.mode.${addon.verification === 'sha256+sigstore' ? 'signed' : 'digest'}`)}</Fact>
          <Fact icon="cpu">{addon.platform.os}/{addon.platform.arch}</Fact>
          <UpdateFact view={update} t={t} />
        </div>
        {working && <p className="ad-entry__note" role="status">{t('apps.working')}</p>}
        {upgraded && !working && <p className="ad-entry__note" data-tone="ok" role="status" data-testid="upgrade-done">{t('updates.done', { version: addon.version })}</p>}
        {actions.unknown[addon.id]
          ? <UnknownNotice addonId={addon.id} actions={actions} />
          : result && result.outcome !== 'completed' && (
            <p className="ad-entry__note" data-tone="error" role="alert" data-testid="upgrade-error">{outcomeText(result, t)}</p>
          )}
      </div>
      <div className="ad-entry__acts">
        {addon.enabled ? (
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" disabled={working} onClick={() => onLifecycle(addon, 'disable')}>
            <Icon name="stop" /> {t('installed.disable')}
          </button>
        ) : (
          <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={working} onClick={() => onLifecycle(addon, 'enable')}>
            <Icon name="play" /> {t('installed.enable')}
          </button>
        )}
        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => onManage(addon.id)}>
          <Icon name="settings" /> {t('installed.manage')}
        </button>
      </div>
    </article>
  )
}

function ManageSheet({ addon, api, t, language, actions, check, upgraded, onCheck, onUpgrade, onLifecycle, onUninstall, onClose }) {
  const working = busy(actions, addon, ['enable', 'disable', 'upgrade', 'uninstall'])
  const compat = addon.compatibility || {}
  return (
    <SideSheet
      title={addon.title}
      description={addon.description || t('installed.manageLead', { id: addon.id })}
      onClose={onClose}
      closeLabel={t('catalog.close')}
      labelId="manage-title"
      testId="addon-manage"
      footer={(
        <>
          <button type="button" className="dk-btn dk-btn--danger dk-btn--sm" disabled={working} onClick={onUninstall}>
            <Icon name="trash" /> {t('installed.uninstall')}
          </button>
          {addon.enabled ? (
            <button type="button" className="dk-btn dk-btn--sm" disabled={working} onClick={() => onLifecycle(addon, 'disable')}>
              <Icon name="stop" /> {t('installed.disable')}
            </button>
          ) : (
            <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={working} onClick={() => onLifecycle(addon, 'enable')}>
              <Icon name="play" /> {t('installed.enable')}
            </button>
          )}
        </>
      )}
    >
      <section className="ad-section" aria-labelledby="manage-health">
        <h3 id="manage-health" className="ad-section__title">{t('manage.health')}</h3>
        <dl className="dk-kv ad-kv">
          <dt>{t('manage.desired')}</dt><dd>{addon.enabled ? t('installed.enabled') : t('installed.disabled')}</dd>
          <dt>{t('manage.observed')}</dt><dd><StateChip app={addon} /></dd>
          <dt>{t('manage.restarts')}</dt><dd>{addon.restarts}</dd>
        </dl>
        {addon.enabled && addon.state === 'failed' && <p className="ad-entry__note" data-tone="error" role="alert">{t('manage.failedNote', { count: addon.restarts })}</p>}
        {addon.mainPage && <Link className="dk-btn dk-btn--ghost dk-btn--sm" to={addon.mainPage}><Icon name="external-link" /> {t('apps.open')}</Link>}
      </section>

      <section className="ad-section" aria-labelledby="manage-trust">
        <h3 id="manage-trust" className="ad-section__title">{t('manage.trust')}</h3>
        <dl className="dk-kv ad-kv">
          <dt>{t('trust.verification')}</dt><dd>{t(`trust.mode.${addon.verification === 'sha256+sigstore' ? 'signed' : 'digest'}`)}</dd>
          <dt>{t('trust.library')}</dt><dd>{addon.sourceId || '—'}</dd>
          {addon.channel && (<><dt>{t('trust.channel')}</dt><dd>{addon.channel}</dd></>)}
          {addon.selection && (<><dt>{t('trust.selection')}</dt><dd>{addon.selection}</dd></>)}
          <dt>{t('trust.digest')}</dt><dd className="dk-mono ad-wrap" title={addon.digest}>{shortDigest(addon.digest)}</dd>
        </dl>
      </section>

      <section className="ad-section" aria-labelledby="manage-compat">
        <h3 id="manage-compat" className="ad-section__title">{t('manage.compatibility')}</h3>
        <dl className="dk-kv ad-kv">
          <dt>{t('trust.platform')}</dt><dd>{addon.platform.os}/{addon.platform.arch}</dd>
          <dt>{t('manage.host')}</dt><dd>{rangeText(compat.host)}</dd>
          <dt>{t('manage.api')}</dt><dd>{rangeText(compat.api)}</dd>
          <dt>{t('manage.protocol')}</dt><dd>{rangeText(compat.protocol)}</dd>
          <dt>{t('manage.dataFormat')}</dt><dd>{rangeText(compat.dataFormat)}</dd>
          <dt>{t('manage.tools')}</dt><dd>{addon.toolCount}{addon.tools?.length ? `: ${addon.tools.join(', ')}` : ''}</dd>
          <dt>{t('manage.interface')}</dt><dd>{addon.hasUI ? t('manage.hasUI') : t('manage.headless')}</dd>
        </dl>
      </section>

      <section className="ad-section" aria-labelledby="manage-storage">
        <h3 id="manage-storage" className="ad-section__title">{t('manage.storage')}</h3>
        <dl className="dk-kv ad-kv">
          <dt>{t('manage.data')}</dt><dd>{formatBytes(addon.storage?.bytes)}{addon.storage?.truncated ? ` ${t('manage.atLeast')}` : ''}</dd>
          <dt>{t('manage.generations')}</dt><dd>{addon.generations}</dd>
        </dl>
      </section>

      <section className="ad-section" aria-labelledby="manage-recovery" data-testid="manage-recovery">
        <h3 id="manage-recovery" className="ad-section__title">{t('recovery.title')}</h3>
        <RecoveryPanel api={api} addon={addon} />
      </section>

      <section className="ad-section" aria-labelledby="manage-updates" data-testid="manage-updates">
        <h3 id="manage-updates" className="ad-section__title">{t('updates.title')}</h3>
        <UpdatePanel addon={addon} t={t} language={language} actions={actions} check={check} upgraded={upgraded} working={working} onCheck={onCheck} onUpgrade={onUpgrade} />
      </section>

      <section className="ad-section" aria-labelledby="manage-logs">
        <h3 id="manage-logs" className="ad-section__title">{t('manage.logs')}</h3>
        <p className="ad-quiet">{t('manage.logsNone')}</p>
      </section>
    </SideSheet>
  )
}

// The owner's update check of one add-on: what it says, how old it is, and the
// two actions. Check only reads; Upgrade opens a confirmation first.
function UpdatePanel({ addon, t, language, actions, check, upgraded, working, onCheck, onUpgrade }) {
  const view = updateView(addon.update)
  const checking = check && check.phase === 'working'
  const upgrading = actions.state[`upgrade:${addon.id}:${addon.generation}`]?.phase === 'working'
  const result = latest(actions, addon, ['upgrade'])
  const checkedAt = formatWhen(view.checkedAt, language)
  const lastSuccess = formatWhen(view.lastSuccess, language)
  return (
    <>
      <p data-testid="update-state" data-state={view.state} className={view.state === 'check-failed' || view.state === 'integrity-error' ? 'ad-entry__note' : undefined} data-tone={view.tone === 'error' ? 'error' : undefined}>
        {t(`updates.state.${view.state}`, { version: view.version })}
      </p>
      <dl className="dk-kv ad-kv">
        <dt>{t('updates.installed')}</dt>
        <dd>{addon.version} <span className="dk-mono ad-wrap" title={addon.digest}>{shortDigest(addon.digest)}</span></dd>
        {view.version && (view.state === 'update-available' || view.state === 'incompatible' || view.state === 'integrity-error') && (
          <>
            <dt>{t('updates.candidate')}</dt>
            <dd data-testid="update-candidate">{view.version} {view.digest && <span className="dk-mono ad-wrap" title={view.digest}>{shortDigest(view.digest)}</span>}</dd>
          </>
        )}
        <dt>{t('trust.library')}</dt><dd>{addon.sourceId || '—'}{addon.channel ? ` · ${addon.channel}` : addon.selection ? ` · ${addon.selection}` : ''}</dd>
      </dl>
      {view.reasons.length > 0 && (
        <div data-testid="update-reasons">
          <p className="ad-quiet">{t('updates.reasons')}</p>
          <ul className="ad-reasons">{view.reasons.map(r => <li key={r}>{t(`updates.reason.${r}`)}</li>)}</ul>
        </div>
      )}
      {view.state === 'check-failed' && view.error && <p className="ad-entry__note" data-tone="error" role="status" data-testid="update-error">{t(`updates.class.${view.error}`)}</p>}
      <p className="ad-quiet" data-testid="update-times">
        {checkedAt ? t('updates.checkedAt', { when: checkedAt }) : t('updates.neverChecked')}
        {lastSuccess ? ` · ${t('updates.lastSuccess', { when: lastSuccess })}` : ''}
      </p>
      {view.stale && <p className="ad-quiet" data-testid="update-stale">{t('updates.stale')}</p>}
      {check && check.phase === 'done' && check.throttled && <p className="ad-quiet" role="status">{t('updates.throttled')}</p>}
      {check && check.phase === 'error' && <p className="ad-entry__note" data-tone="error" role="alert">{describeError(check.error, t)}</p>}
      {upgrading && <p className="ad-entry__note" role="status" data-testid="upgrade-progress">{t('updates.progress', { name: addon.title })}</p>}
      {upgraded && !working && <p className="ad-entry__note" data-tone="ok" role="status">{t('updates.done', { version: addon.version })}</p>}
      {actions.unknown[addon.id]
        ? <UnknownNotice addonId={addon.id} actions={actions} />
        : result && result.outcome !== 'completed' && <p className="ad-entry__note" data-tone="error" role="alert">{outcomeText(result, t)}</p>}
      <div className="ad-section__acts">
        <button type="button" className="dk-btn dk-btn--sm" disabled={checking || working} onClick={onCheck} data-testid="update-check">
          <Icon name={checking ? 'spinner' : 'refresh'} spin={checking} /> {checking ? t('updates.checking') : t('updates.checkNow')}
        </button>
        {view.canUpgrade && (
          <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={working} onClick={onUpgrade} data-testid="update-upgrade">
            <Icon name="arrow-up" /> {t('updates.upgrade')}
          </button>
        )}
      </div>
    </>
  )
}

// What the confirmation shows before an upgrade: which library and channel,
// which version and digest goes to which, and that the data is backed up.
function UpgradeSummary({ addon, t }) {
  const view = updateView(addon.update)
  return (
    <div data-testid="upgrade-confirm">
      <dl className="dk-kv ad-kv">
        <dt>{t('updates.confirm.library')}</dt><dd>{addon.sourceId || '—'}</dd>
        <dt>{t('updates.confirm.channel')}</dt><dd>{addon.channel || addon.selection || '—'}</dd>
        <dt>{t('updates.confirm.from')}</dt><dd>{addon.version} <span className="dk-mono">{shortDigest(addon.digest)}</span></dd>
        <dt>{t('updates.confirm.to')}</dt><dd>{view.version} <span className="dk-mono">{shortDigest(view.digest)}</span></dd>
      </dl>
      <p>{t('updates.confirm.body')}</p>
      <p>{t('updates.confirm.data')}</p>
    </div>
  )
}
