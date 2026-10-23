/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import LoadingSpinner from '../LoadingSpinner'
import ConfirmDialog from '../ConfirmDialog'
import { newOperationId } from '../catalog/catalogState.js'
import { Fact } from './AddonBits'
import { formatBytes } from './addonState'
import {
  bannerKind, phaseProgress, verificationOf, recoveryErrorKey, restoreBlocked, restoreBody,
  needsMapping, purgeBody, purgeBlocked, confirmMatches,
} from './recoveryState.js'

// The data recovery of one add-on, for administrators: its backups with their
// verification state, the state of its data, an explicit restore with its
// warnings, and the purge of retained data. Every decision is the owner's;
// this panel only asks, and says what it answered.
export default function RecoveryPanel({ api, addon }) {
  const { t, i18n } = useTranslation('addons')
  const [state, setState] = useState({ status: 'loading', data: null, error: null })
  const [reload, setReload] = useState(0)
  const [notice, setNotice] = useState(null)
  const [working, setWorking] = useState('')
  const [dialog, setDialog] = useState(null)
  // One operation ID per user action, kept across retries of that action.
  const ops = useRef({})
  const live = useRef(true)
  useEffect(() => () => { live.current = false }, [])

  useEffect(() => {
    const ctl = new AbortController()
    api.recovery.status(addon.id, ctl.signal)
      .then(data => setState({ status: 'ready', data, error: null }))
      .catch(error => { if (!ctl.signal.aborted && error.name !== 'AbortError') setState(s => ({ status: s.data ? 'ready' : 'error', data: s.data, error })) })
    return () => ctl.abort()
  }, [api, addon.id, reload])

  // A phase in progress is polled, so the page follows an upgrade.
  const data = state.data
  const banner = bannerKind(data)
  useEffect(() => {
    if (!data || !(banner === 'upgrading' || banner === 'restoring' || banner === 'purging')) return undefined
    const timer = setTimeout(() => setReload(n => n + 1), 2000)
    return () => clearTimeout(timer)
  }, [data, banner])

  const refresh = useCallback(() => setReload(n => n + 1), [])

  const run = useCallback(async (key, call, done) => {
    const operationId = ops.current[key] || (ops.current[key] = newOperationId())
    setWorking(key)
    setNotice(null)
    try {
      const out = await call(operationId)
      delete ops.current[key]
      if (live.current) setNotice({ tone: 'ok', text: done ? done(out) : t('recovery.done'), result: out })
      return out
    } catch (error) {
      if (!live.current) return null
      const k = recoveryErrorKey(error)
      // An unknown outcome keeps its operation ID: the same action repeated
      // is the same operation, and reading the state shows whether it ran.
      if (k !== 'unknownOutcome') delete ops.current[key]
      setNotice({ tone: 'error', text: t(`recovery.errors.${k}`) })
      return null
    } finally {
      if (live.current) { setWorking(''); refresh() }
    }
  }, [refresh, t])

  const when = (iso) => {
    const d = new Date(iso)
    return Number.isNaN(d.getTime()) ? '' : d.toLocaleString(i18n.language)
  }

  if (state.status === 'loading') return <div className="op-loading" role="status" aria-label={t('recovery.loading')}><LoadingSpinner size="sm" /></div>
  if (state.status === 'error') {
    return (
      <div className="op-error" role="alert">
        <Icon name="alert-circle" />
        <span>{t(`recovery.errors.${recoveryErrorKey(state.error)}`)}</span>
        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={refresh}><Icon name="refresh" /> {t('catalog.retry')}</button>
      </div>
    )
  }
  const backups = Array.isArray(data.backups) ? data.backups : []
  const progress = phaseProgress(data.phase)
  const blocked = restoreBlocked(addon, data)

  return (
    <div className="ad-recovery" data-testid="recovery-panel" data-state={data.state}>
      {banner && (
        <div className="ad-recovery__banner" role="alert" data-tone={banner === 'needs-recovery' ? 'error' : 'warn'} data-testid="recovery-banner">
          <Icon name={banner === 'needs-recovery' ? 'alert-circle' : 'refresh'} aria-hidden="true" />
          <div>
            <strong>{t(`recovery.banner.${banner}.title`)}</strong>
            <p>{t(`recovery.banner.${banner}.body`)}</p>
            {data.reason && <p className="dk-mono ad-wrap" data-testid="recovery-reason">{data.reason}</p>}
            {progress && <p data-testid="recovery-phase">{t('recovery.phase', { step: t(`recovery.steps.${progress.step}`), index: progress.index, total: progress.total })}</p>}
            {banner === 'needs-recovery' && (
              <p>{data.recoveryBackup ? t('recovery.banner.needs-recovery.hint') : t('recovery.banner.needs-recovery.hintNone')}</p>
            )}
          </div>
        </div>
      )}

      {notice && (
        <div className="ad-entry__note" data-tone={notice.tone === 'ok' ? undefined : 'error'} role={notice.tone === 'ok' ? 'status' : 'alert'} data-testid="recovery-notice">
          <p>{notice.text}</p>
          {notice.result && Array.isArray(notice.result.warnings) && notice.result.warnings.length > 0 && (
            <ul className="ad-recovery__warnings" data-testid="recovery-warnings">
              {notice.result.warnings.map((w, i) => <li key={i}>{w}</li>)}
            </ul>
          )}
        </div>
      )}

      <div className="ad-recovery__bar">
        <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" data-testid="recovery-backup"
          disabled={Boolean(working) || banner === 'restoring' || banner === 'upgrading'}
          onClick={() => run('backup', (operationId) => api.recovery.backup(addon.id, { operationId }), (b) => t('recovery.backedUp', { id: b.id }))}>
          <Icon name="save" aria-hidden="true" /> {working === 'backup' ? t('recovery.backingUp') : t('recovery.backup')}
        </button>
        <span className="ad-quiet">{t('recovery.retention', { count: data.retention?.keep ?? 3 })}</span>
      </div>

      {backups.length === 0
        ? <p className="ad-quiet" data-testid="recovery-empty">{t('recovery.none')}</p>
        : (
          <ul className="ad-recovery__list" aria-label={t('recovery.listLabel')} data-testid="recovery-list">
            {backups.map(b => {
              const v = verificationOf(b)
              return (
                <li key={b.id} className="ad-recovery__item" data-backup={b.id} data-verification={b.verification}>
                  <div className="ad-recovery__main">
                    <span className="dk-mono ad-wrap">{b.id}</span>
                    <span className="ad-quiet">{when(b.created)}</span>
                  </div>
                  <div className="ad-facts">
                    <Fact icon={v.key === 'verified' ? 'shield' : 'alert-circle'} tone={v.tone}>{t(`recovery.verification.${v.key}`)}</Fact>
                    <Fact icon="database">{formatBytes(b.size)}</Fact>
                    <Fact icon="clock">{t(`recovery.reason.${b.reason || 'manual'}`)}</Fact>
                    {b.version && <Fact icon="tag">{t('apps.version', { version: b.version })}</Fact>}
                    {b.pinned && <Fact icon="lock" tone="ok">{t('recovery.pinned')}</Fact>}
                  </div>
                  <div className="ad-recovery__acts">
                    <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" disabled={Boolean(working)}
                      onClick={() => run(`verify:${b.id}`, (operationId) => api.recovery.verify(addon.id, { operationId, backupId: b.id }), () => t('recovery.verified', { id: b.id }))}>
                      <Icon name="shield" aria-hidden="true" /> {t('recovery.verify')}
                    </button>
                    <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" data-testid="recovery-restore" disabled={Boolean(working) || b.verification === 'failed'}
                      onClick={() => setDialog({ kind: 'restore', backup: b })}>
                      <Icon name="refresh" aria-hidden="true" /> {t('recovery.restore')}
                    </button>
                    <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" disabled={Boolean(working) || b.pinned}
                      title={b.pinned ? t('recovery.pinnedWhy') : undefined}
                      onClick={() => run(`delete:${b.id}`, (operationId) => api.recovery.deleteBackup(addon.id, { operationId, backupId: b.id }), () => t('recovery.deleted', { id: b.id }))}>
                      <Icon name="trash" aria-hidden="true" /> {t('recovery.delete')}
                    </button>
                  </div>
                </li>
              )
            })}
          </ul>
        )}

      {data.lastRestore && (
        <section className="ad-recovery__last" aria-labelledby={`last-restore-${addon.id}`} data-testid="recovery-last-restore">
          <h4 id={`last-restore-${addon.id}`}>{t('recovery.lastRestore', { when: when(data.lastRestore.at) })}</h4>
          <ul className="ad-recovery__warnings">
            {(data.lastRestore.warnings || []).map((w, i) => <li key={i}>{w}</li>)}
          </ul>
        </section>
      )}

      <div className="ad-recovery__purge">
        <p>{t('recovery.purgeLead')}</p>
        <button type="button" className="dk-btn dk-btn--danger dk-btn--sm" data-testid="recovery-purge" disabled={Boolean(working)}
          onClick={() => setDialog({ kind: 'purge' })}>
          <Icon name="trash" aria-hidden="true" /> {t('recovery.purge')}
        </button>
      </div>

      {dialog && dialog.kind === 'restore' && (
        <RestoreDialog t={t} addon={addon} status={data} backup={dialog.backup} blocked={blocked} when={when}
          onCancel={() => setDialog(null)}
          onConfirm={(mapping) => {
            const b = dialog.backup
            setDialog(null)
            run(`restore:${b.id}`, (operationId) => api.recovery.restore(addon.id, restoreBody({ operationId, backup: b, addonId: addon.id, mapping })), () => t('recovery.restored'))
          }} />
      )}
      {dialog && dialog.kind === 'purge' && (
        <PurgeDialog t={t} addon={addon} status={data}
          onCancel={() => setDialog(null)}
          onConfirm={(scope) => {
            setDialog(null)
            run('purge', (operationId) => api.recovery.purge(addon.id, purgeBody({ operationId, addonId: addon.id, scope })), () => t('recovery.purged'))
          }} />
      )}
    </div>
  )
}

function TypedConfirm({ t, addonId, value, onChange, inputRef }) {
  return (
    <label className="ad-recovery__typed">
      <span>{t('recovery.typeToConfirm', { id: addonId })}</span>
      <input ref={inputRef} className="dk-input" type="text" value={value} autoComplete="off" spellCheck={false}
        aria-label={t('recovery.typeToConfirmLabel')} data-testid="recovery-confirm-input"
        onChange={(e) => onChange(e.target.value)} />
    </label>
  )
}

function RestoreDialog({ t, addon, status, backup, blocked, when, onCancel, onConfirm }) {
  const [typed, setTyped] = useState('')
  const [mapping, setMapping] = useState('')
  const input = useRef(null)
  useEffect(() => { input.current?.focus() }, [])
  const mapped = needsMapping(backup, status)
  const missingMapping = mapped && mapping.trim() === ''
  return (
    <ConfirmDialog
      open
      danger
      title={t('recovery.restoreTitle')}
      confirmLabel={t('recovery.restore')}
      confirmDisabled={!confirmMatches(typed, addon.id) || Boolean(blocked) || missingMapping}
      onConfirm={() => onConfirm(mapping)}
      onCancel={onCancel}
      message={(
        <div className="ad-recovery__dialog" data-testid="recovery-restore-dialog">
          <p>{t('recovery.restoreBody', { when: when(backup.created) })}</p>
          <ul className="ad-recovery__warnings">
            <li>{t('recovery.warnLost')}</li>
            <li>{t('recovery.warnAccess')}</li>
            <li>{t('recovery.warnProposals')}</li>
            <li>{t('recovery.warnCopy')}</li>
          </ul>
          {blocked === 'running' && <p className="ad-entry__note" data-tone="error" role="alert">{t('recovery.stopFirst')}</p>}
          {mapped && (
            <label className="ad-recovery__typed">
              <span>{t('recovery.mapping')}</span>
              <input className="dk-input" type="text" value={mapping} autoComplete="off" spellCheck={false}
                data-testid="recovery-mapping-input" onChange={(e) => setMapping(e.target.value)} />
            </label>
          )}
          <TypedConfirm t={t} addonId={addon.id} value={typed} onChange={setTyped} inputRef={input} />
        </div>
      )}
    />
  )
}

function PurgeDialog({ t, addon, status, onCancel, onConfirm }) {
  const [typed, setTyped] = useState('')
  const [scope, setScope] = useState({ data: true, backups: false, recoveryCopies: false })
  const input = useRef(null)
  useEffect(() => { input.current?.focus() }, [])
  const blocked = purgeBlocked(addon, scope)
  const flip = (name) => setScope(s => ({ ...s, [name]: !s[name] }))
  return (
    <ConfirmDialog
      open
      danger
      title={t('recovery.purgeTitle')}
      confirmLabel={t('recovery.purge')}
      confirmDisabled={!confirmMatches(typed, addon.id) || Boolean(blocked)}
      onConfirm={() => onConfirm(scope)}
      onCancel={onCancel}
      message={(
        <div className="ad-recovery__dialog" data-testid="recovery-purge-dialog">
          <p>{t('recovery.purgeBody')}</p>
          <fieldset className="ad-recovery__scope">
            <legend>{t('recovery.scope')}</legend>
            <label><input type="checkbox" checked={scope.data} onChange={() => flip('data')} data-testid="scope-data" /> {t('recovery.scopeData')}</label>
            <label><input type="checkbox" checked={scope.backups} onChange={() => flip('backups')} data-testid="scope-backups" /> {t('recovery.scopeBackups', { count: status.backups?.length || 0 })}</label>
            <label><input type="checkbox" checked={scope.recoveryCopies} onChange={() => flip('recoveryCopies')} data-testid="scope-copies" /> {t('recovery.scopeCopies', { count: status.previousCopies || 0 })}</label>
          </fieldset>
          {blocked === 'running' && <p className="ad-entry__note" data-tone="error" role="alert">{t('recovery.stopFirst')}</p>}
          {blocked === 'scope' && <p className="ad-quiet" role="status">{t('recovery.scopeNone')}</p>}
          <TypedConfirm t={t} addonId={addon.id} value={typed} onChange={setTyped} inputRef={input} />
        </div>
      )}
    />
  )
}
