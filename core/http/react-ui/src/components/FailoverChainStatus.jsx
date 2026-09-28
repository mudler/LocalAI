import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import StatusPill from './StatusPill'
import ConfirmDialog from './ConfirmDialog'
import useFailoverChains from '../hooks/useFailoverChains'
import { useAuth } from '../context/AuthContext'
import { failoverApi } from '../utils/api'

const UNITS = [
  ['day', 86_400],
  ['hour', 3_600],
  ['minute', 60],
]

// Localized "5 minutes ago" from an RFC 3339 timestamp. Intl keeps the phrase
// in the viewer's language without a translation key per unit. Exported so
// other failover surfaces (the overview table) share the same phrasing.
export function relative(ts, lng) {
  const ms = Date.parse(ts)
  if (!ms) return null
  const seconds = Math.round((ms - Date.now()) / 1000)
  const rtf = new Intl.RelativeTimeFormat(lng, { numeric: 'auto' })
  for (const [unit, size] of UNITS) {
    if (Math.abs(seconds) >= size) return rtf.format(Math.round(seconds / size), unit)
  }
  return rtf.format(seconds, 'second')
}

// FailoverChainStatus renders the live health of one failover chain: its
// state, the target serving it, and a per-target health table. Pin controls
// appear only when canPin, and every pin change needs a confirmation because
// it overrides automatic failover for all callers.
export default function FailoverChainStatus({ chain, onPin, onUnpin, canPin = false }) {
  const { t, i18n } = useTranslation('models')
  const [confirm, setConfirm] = useState(null)
  const [pending, setPending] = useState(false)

  const runConfirmed = async () => {
    setPending(true)
    try {
      if (confirm.kind === 'pin') await onPin?.(confirm.target)
      else await onUnpin?.()
    } finally {
      setPending(false)
      setConfirm(null)
    }
  }

  const since = relative(chain.active_since, i18n.language)

  return (
    <section className="failover-status" aria-label={t('failover.title')}>
      <div className="failover-status__summary">
        <span className="failover-status__label">{t('failover.title')}</span>
        <span className="failover-status__state">
          <StatusPill status={chain.state} label={t(`failover.states.${chain.state}`, chain.state)} />
        </span>
        <span className="failover-status__meta">
          {t('failover.servedBy')} <code className="failover-status__active">{chain.active}</code>
        </span>
        {since && (
          <span className="text-muted" title={new Date(chain.active_since).toLocaleString(i18n.language)}>
            {t('failover.changed', { time: since })}
          </span>
        )}
        {chain.pinned && (
          <span className="failover-status__pinned">
            <i className="fas fa-thumbtack" aria-hidden="true" /> {t('failover.pinnedTo', { target: chain.pinned })}
          </span>
        )}
        {canPin && chain.pinned && (
          <button type="button" className="btn btn-ghost btn-sm failover-status__unpin" onClick={() => setConfirm({ kind: 'unpin' })}>
            {t('failover.actions.unpin')}
          </button>
        )}
      </div>

      <div className="table-container">
        <table className="table failover-status__table">
          <thead>
            <tr>
              <th>{t('failover.columns.target')}</th>
              <th>{t('failover.columns.kind')}</th>
              <th>{t('failover.columns.warm')}</th>
              <th>{t('failover.columns.health')}</th>
              <th>{t('failover.columns.lastProbe')}</th>
              <th>{t('failover.columns.lastError')}</th>
              {canPin && <th><span className="sr-only">{t('failover.columns.actions')}</span></th>}
            </tr>
          </thead>
          <tbody>
            {(chain.targets || []).map(target => (
              <tr key={target.model} className={target.model === chain.active ? 'failover-status__row--active' : undefined}>
                <td><code>{target.model}</code></td>
                <td>{t(`failover.kinds.${target.kind}`, target.kind)}</td>
                <td>{target.warm ? t('failover.warm') : <span className="text-muted">-</span>}</td>
                <td><StatusPill status={target.state} label={t(`failover.states.${target.state}`, target.state)} /></td>
                <td className="text-muted">{relative(target.last_probe, i18n.language) || t('failover.never')}</td>
                <td>
                  {target.last_error
                    ? <span className="failover-status__error" title={target.last_error}>{target.last_error}</span>
                    : <span className="text-muted">-</span>}
                </td>
                {canPin && (
                  <td className="failover-status__action">
                    {chain.pinned !== target.model && (
                      <button type="button" className="btn btn-ghost btn-sm" onClick={() => setConfirm({ kind: 'pin', target: target.model })}>
                        {t('failover.actions.pin')}
                      </button>
                    )}
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <ConfirmDialog
        open={!!confirm}
        title={confirm?.kind === 'pin'
          ? t('failover.confirm.pinTitle', { target: confirm.target })
          : t('failover.confirm.unpinTitle', { chain: chain.name })}
        message={confirm?.kind === 'pin'
          ? t('failover.confirm.pinMessage', { chain: chain.name, target: confirm.target })
          : t('failover.confirm.unpinMessage', { chain: chain.name })}
        confirmLabel={confirm?.kind === 'pin' ? t('failover.actions.pin') : t('failover.actions.unpin')}
        pendingLabel={confirm?.kind === 'pin' ? t('failover.actions.pinning') : t('failover.actions.unpinning')}
        pending={pending}
        onConfirm={runConfirmed}
        onCancel={() => setConfirm(null)}
      />
    </section>
  )
}

// ModelFailoverStatus is the Model Editor mount point: it renders the strip
// only when the edited model is a failover chain, and owns the SSE
// subscription so create mode never opens one.
export function ModelFailoverStatus({ name, addToast }) {
  const { t } = useTranslation('models')
  const { isAdmin } = useAuth()
  const { byName, refresh } = useFailoverChains()
  const chain = byName[name]
  if (!chain) return null

  const act = async (call, errorKey) => {
    try {
      await call()
    } catch (err) {
      addToast?.(t(errorKey, { error: err.message }), 'error')
    }
    refresh()
  }

  return (
    <FailoverChainStatus
      chain={chain}
      canPin={isAdmin}
      onPin={(target) => act(() => failoverApi.pin(name, target), 'failover.errors.pin')}
      onUnpin={() => act(() => failoverApi.unpin(name), 'failover.errors.unpin')}
    />
  )
}
