/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import LoadingSpinner from '../LoadingSpinner'
import ConfirmDialog from '../ConfirmDialog'
import SideSheet from '../SideSheet'
import CatalogEmpty from '../catalog/CatalogEmpty'
import { apiUrl } from '../../utils/basePath'
import { StateChip, AppIcon, Fact, UnknownNotice } from './AddonBits'
import { formatBytes, rangeText, shortDigest, updateStatus } from './addonState'
import { describeError, describeOutcome } from './errors'
import { useAddonActions } from './useAddonActions'

// Everything the owner knows of what is installed: verified package facts,
// the desired state (enabled) and the observed one (health) apart, retained
// generations, data size and, when the owner can say so, an update. A headless
// add-on is managed here exactly like one with pages.
export default function InstalledView({ api, adapter, manage, onManage, onChanged }) {
  const { t } = useTranslation('addons')
  const [state, setState] = useState({ status: 'loading', addons: [], error: null })
  const [reload, setReload] = useState(0)
  const [confirm, setConfirm] = useState(null)

  useEffect(() => {
    const ctl = new AbortController()
    api.installed(ctl.signal)
      .then(addons => setState({ status: 'ready', addons, error: null }))
      .catch(error => { if (!ctl.signal.aborted && error.name !== 'AbortError') setState(s => ({ status: s.status === 'ready' ? 'ready' : 'error', addons: s.addons, error })) })
    return () => ctl.abort()
  }, [api, reload])

  const actions = useAddonActions(api, { onSettled: () => { setReload(n => n + 1); if (onChanged) onChanged() } })

  const lifecycle = useCallback((addon, action) => {
    actions.run(`${action}:${addon.id}:${addon.generation}`, {
      addonId: addon.id,
      submit: (operationId, signal) => adapter.submitLifecycle({
        operationId,
        action,
        addonId: addon.id,
        generation: action === 'enable' || action === 'disable' ? addon.generation : 0,
        sourceId: action === 'upgrade' ? addon.sourceId : undefined,
        version: action === 'upgrade' ? addon.update?.version : undefined,
      }, signal),
      // A completed status is not proof for this click: the owner answers a
      // replayed operation with its old status and applies nothing.
      verify: async (signal) => {
        const now = (await api.installed(signal)).find(a => a.id === addon.id)
        if (action === 'uninstall') return !now
        if (!now) return false
        if (action === 'enable') return now.enabled === true
        if (action === 'disable') return now.enabled === false
        return now.generation > addon.generation
      },
    })
  }, [actions, adapter, api])

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
            <InstalledRow addon={addon} t={t} actions={actions} onLifecycle={lifecycle} onManage={onManage} />
          </li>
        ))}
      </ul>
      {selected && (
        <ManageSheet
          addon={selected}
          t={t}
          actions={actions}
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

function InstalledRow({ addon, t, actions, onLifecycle, onManage }) {
  const working = busy(actions, addon, ['enable', 'disable', 'upgrade', 'uninstall'])
  const result = latest(actions, addon, ['enable', 'disable', 'upgrade', 'uninstall'])
  const update = updateStatus(addon)
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
          <Fact icon={addon.verification === 'sha256+sigstore' ? 'shield' : 'info'} tone={addon.verification === 'sha256+sigstore' ? 'ok' : undefined}>{t(`trust.mode.${addon.verification === 'sha256+sigstore' ? 'signed' : 'digest'}`)}</Fact>
          <Fact icon="cpu">{addon.platform.os}/{addon.platform.arch}</Fact>
          {update.kind === 'available' && <Fact icon="arrow-up" tone="warn">{t('installed.updateAvailable', { version: update.version })}</Fact>}
        </div>
        {working && <p className="ad-entry__note" role="status">{t('apps.working')}</p>}
        {actions.unknown[addon.id]
          ? <UnknownNotice addonId={addon.id} actions={actions} />
          : result && result.outcome !== 'completed' && (
            <p className="ad-entry__note" data-tone="error" role="alert">{describeOutcome(result.outcome, t)}</p>
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

function ManageSheet({ addon, t, actions, onLifecycle, onUninstall, onClose }) {
  const working = busy(actions, addon, ['enable', 'disable', 'upgrade', 'uninstall'])
  const update = updateStatus(addon)
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

      <section className="ad-section" aria-labelledby="manage-updates">
        <h3 id="manage-updates" className="ad-section__title">{t('manage.updates')}</h3>
        {update.kind === 'available' && (
          <>
            <p>{t('manage.updateAvailable', { version: update.version })}</p>
            <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={working} onClick={() => onLifecycle(addon, 'upgrade')}>
              <Icon name="arrow-up" /> {t('manage.upgrade')}
            </button>
          </>
        )}
        {update.kind === 'current' && <p>{t('manage.updateCurrent')}</p>}
        {update.kind === 'unknown' && <p className="ad-quiet">{t('manage.updateUnknown')}</p>}
      </section>

      <section className="ad-section" aria-labelledby="manage-logs">
        <h3 id="manage-logs" className="ad-section__title">{t('manage.logs')}</h3>
        <p className="ad-quiet">{t('manage.logsNone')}</p>
      </section>
    </SideSheet>
  )
}
