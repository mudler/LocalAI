/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import { Button, Callout, EmptyState, LoadError, SkeletonRows, StatusChip, useLoad } from '../ui'
import PairPhoneDialog from './PairPhoneDialog'
import { myP2PApi } from '../../utils/api'
import { clock, pairBlocked, pathKey, pathTone, pendingInvites, relativeTime, secondsLeft, sortedDevices } from '../../utils/p2p'
import './p2p.css'

// How often the tab reads the invites and devices again: an invite counts
// down and a device's path changes while the tab is open.
const REFRESH_MS = 10_000

// Account, "My devices": the phones a user paired with this server. Each one
// signs in as the user. The user pairs a new phone, renames or revokes one,
// and cancels an invite that no phone used.
//
//   me        useLoad of GET /api/p2p/me; the Account page loaded it to decide
//             whether the tab shows, and the tab reloads it after a change
//   isAdmin   the user is an administrator: a paired phone gets an admin session
//   addToast  the shell's toast
export default function MyDevicesPanel({ me, isAdmin, addToast }) {
  const { t } = useTranslation('p2p')
  const invites = useLoad(() => myP2PApi.invites(), [])
  const devices = useLoad(() => myP2PApi.devices(), [])
  const [now, setNow] = useState(Date.now())
  const [pairing, setPairing] = useState(false)
  const [renaming, setRenaming] = useState(null)
  const [revoking, setRevoking] = useState(null)

  const reloadMe = me.reload
  const reloadInvites = invites.reload
  const reloadDevices = devices.reload
  const reload = useCallback(() => { reloadMe(); reloadInvites(); reloadDevices() }, [reloadMe, reloadInvites, reloadDevices])
  useEffect(() => {
    const id = setInterval(() => { reloadInvites(); reloadDevices() }, REFRESH_MS)
    return () => clearInterval(id)
  }, [reloadInvites, reloadDevices])
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])

  const info = me.data || {}
  const limits = info.limits || {}
  const blocked = pairBlocked(info)

  const cancel = async (inv) => {
    try {
      await myP2PApi.cancelInvite(inv.id)
      addToast?.(t('invites.canceled', { label: inv.label || t('my.phone') }), 'success')
    } catch (err) {
      addToast?.(t('invites.cancelFailed', { message: err?.message || '' }), 'error')
    }
    reload()
  }

  return (
    <div className="p2p-my" data-testid="my-devices">
      <div className="p2p-my__head">
        <div className="p2p-my__text">
          <p className="p2p-my__lede">{t('my.lede')}</p>
          <p className="p2p-section__note" data-testid="my-devices-count">
            {t('my.count', { count: info.devices ?? 0, max: limits.max_devices ?? 0 })}
          </p>
        </div>
        <Button
          variant="primary" icon="plus" disabled={blocked !== ''} onClick={() => setPairing(true)}
          aria-describedby={blocked ? 'my-pair-blocked' : undefined} data-testid="my-pair-open"
        >
          {t('my.pair.open')}
        </Button>
      </div>
      {blocked && blocked !== 'unavailable' && (
        <Callout tone="info" testId="my-pair-blocked" id="my-pair-blocked">
          {t(`my.blocked.${blocked}`, { max: blocked === 'devices' ? limits.max_devices : limits.max_pending })}
        </Callout>
      )}
      {isAdmin && <Callout tone="warn" title={t('my.admin.title')} testId="my-admin-warning">{t('my.admin.body')}</Callout>}

      <MyInvites invites={invites} now={now} onCancel={cancel} />
      <MyDevices devices={devices} onRename={setRenaming} onRevoke={setRevoking} onPair={blocked ? null : () => setPairing(true)} />

      {pairing && (
        <PairPhoneDialog me={info} isAdmin={isAdmin} addToast={addToast} onChanged={reload} onClose={() => setPairing(false)} />
      )}
      {renaming && (
        <RenameDialog
          device={renaming} onClose={() => setRenaming(null)}
          onDone={(label) => {
            addToast?.(t('my.renamed', { label }), 'success')
            setRenaming(null)
            reloadDevices()
          }}
        />
      )}
      {revoking && (
        <RevokeMineDialog
          device={revoking} onClose={() => setRevoking(null)}
          onDone={() => {
            addToast?.(t('my.revoked', { label: revoking.label || t('my.phone') }), 'success')
            setRevoking(null)
            reload()
          }}
        />
      )}
    </div>
  )
}

// The invites the user made that no phone used yet. Hidden when there are none.
function MyInvites({ invites, now, onCancel }) {
  const { t } = useTranslation('p2p')
  const rows = invites.status === 'ready' ? pendingInvites(invites.data?.invites, now) : []
  if (invites.status === 'error') return <LoadError title={t('invites.loadFailed')} error={invites.error} onRetry={invites.retry} />
  if (rows.length === 0) return null
  return (
    <section className="p2p-section" aria-labelledby="my-invites-title" data-testid="my-invites">
      <div className="p2p-section__head">
        <h2 className="p2p-h2" id="my-invites-title">{t('my.invites.title')} <span className="p2p-count dk-mono">{rows.length}</span></h2>
        <p className="p2p-section__note">{t('my.invites.note')}</p>
      </div>
      <div className="dk-table-wrap">
        <table className="dk-table p2p-table">
          <caption className="dk-sr-only">{t('my.invites.title')}</caption>
          <thead>
            <tr>
              <th scope="col">{t('invites.col.label')}</th>
              <th scope="col">{t('invites.col.expires')}</th>
              <th scope="col"><span className="dk-sr-only">{t('invites.col.actions')}</span></th>
            </tr>
          </thead>
          <tbody>
            {rows.map(inv => (
              <tr key={inv.id} data-testid={`my-invite-${inv.id}`}>
                <td><span className="p2p-cell-title">{inv.label || t('my.phone')}</span></td>
                <td className="dk-mono">{clock(secondsLeft(inv.expires_at, now))}</td>
                <td className="p2p-cell-actions">
                  <Button size="sm" variant="danger-ghost" onClick={() => onCancel(inv)} data-testid={`my-invite-cancel-${inv.id}`}>
                    {t('invites.cancel')}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function MyDevices({ devices, onRename, onRevoke, onPair }) {
  const { t, i18n } = useTranslation('p2p')
  const rows = devices.status === 'ready' ? sortedDevices(devices.data?.devices) : []
  return (
    <section className="p2p-section" aria-labelledby="my-devices-title" data-testid="my-devices-list">
      <div className="p2p-section__head">
        <h2 className="p2p-h2" id="my-devices-title">{t('my.devices.title')}</h2>
      </div>
      {devices.status === 'loading' && <SkeletonRows count={2} />}
      {devices.status === 'error' && <LoadError title={t('devices.loadFailed')} error={devices.error} onRetry={devices.retry} />}
      {devices.status === 'ready' && rows.length === 0 && (
        <EmptyState
          variant="first-run" icon="phone" level={3} testId="my-devices-empty"
          title={t('my.devices.empty.title')} body={t('my.devices.empty.body')}
          actions={onPair ? <Button icon="plus" onClick={onPair}>{t('my.pair.open')}</Button> : undefined}
        />
      )}
      {rows.length > 0 && (
        <div className="dk-table-wrap">
          <table className="dk-table p2p-table">
            <caption className="dk-sr-only">{t('my.devices.title')}</caption>
            <thead>
              <tr>
                <th scope="col">{t('devices.col.label')}</th>
                <th scope="col">{t('devices.col.path')}</th>
                <th scope="col" className="dk-hide-phone">{t('devices.col.seen')}</th>
                <th scope="col"><span className="dk-sr-only">{t('devices.col.actions')}</span></th>
              </tr>
            </thead>
            <tbody>
              {rows.map(d => {
                const revoked = d.status === 'revoked'
                const seen = d.last_seen_at ? relativeTime(d.last_seen_at, i18n.language) : t('devices.never')
                return (
                  <tr key={d.id} data-testid={`my-device-${d.id}`} data-revoked={revoked || undefined}>
                    <td>
                      <span className="p2p-cell-title">{d.label || t('my.phone')}</span>
                      {revoked
                        ? <span className="p2p-cell-sub">{t('devices.status.revoked')}</span>
                        : <span className="p2p-cell-sub dk-only-phone">{t('my.devices.seen', { when: seen })}</span>}
                    </td>
                    <td><MyDevicePath device={d} /></td>
                    <td className="dk-hide-phone">{seen}</td>
                    <td className="p2p-cell-actions">
                      {!revoked && (
                        <>
                          <Button size="sm" variant="ghost" onClick={() => onRename(d)} data-testid={`my-device-rename-${d.id}`}>{t('my.rename.open')}</Button>
                          <Button size="sm" variant="danger-ghost" onClick={() => onRevoke(d)} data-testid={`my-device-revoke-${d.id}`}>{t('devices.revoke')}</Button>
                        </>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

function MyDevicePath({ device }) {
  const { t } = useTranslation('p2p')
  if (device.status === 'revoked') return <span className="p2p-cell-sub">-</span>
  if (!device.online) return <StatusChip tone="idle" dot>{t('devices.path.offline')}</StatusChip>
  const path = device.path || {}
  const { key, relay } = pathKey(path)
  return <StatusChip tone={pathTone(path)} dot>{t(`devices.path.${key}`, { relay })}</StatusChip>
}

function RenameDialog({ device, onClose, onDone }) {
  const { t } = useTranslation('p2p')
  const id = useId()
  const [label, setLabel] = useState(device.label || '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const name = label.trim()
  const save = async (e) => {
    e?.preventDefault()
    if (!name) return
    setBusy(true); setError('')
    try {
      await myP2PApi.renameDevice(device.id, name)
      onDone(name)
    } catch (err) {
      setError(err?.message || '')
      setBusy(false)
    }
  }
  return (
    <Dialog
      title={t('my.rename.title')} onClose={onClose} pending={busy} closeLabel={t('share.close')} labelId={`${id}-title`} testId="my-rename-dialog"
      foot={(
        <>
          <Button onClick={onClose} disabled={busy}>{t('share.cancel')}</Button>
          <Button variant="primary" busy={busy} disabled={!name || name === device.label} onClick={save} data-testid="my-rename-save">{t('my.rename.save')}</Button>
        </>
      )}
    >
      <form className="p2p-dialog" onSubmit={save}>
        <div className="dk-field">
          <label className="dk-label" htmlFor={`${id}-label`}>{t('share.label')}</label>
          <input
            id={`${id}-label`} className="dk-input" autoComplete="off" maxLength={80} value={label} data-autofocus
            onChange={e => setLabel(e.target.value)} data-testid="my-rename-input"
          />
        </div>
        {error && <Callout tone="error" role="alert" testId="my-rename-error">{error}</Callout>}
      </form>
    </Dialog>
  )
}

function RevokeMineDialog({ device, onClose, onDone }) {
  const { t } = useTranslation('p2p')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const revoke = async () => {
    setBusy(true); setError('')
    try {
      await myP2PApi.revokeDevice(device.id)
      onDone()
    } catch (err) {
      setError(err?.message || '')
      setBusy(false)
    }
  }
  return (
    <Dialog
      role="alertdialog" title={t('devices.revokeTitle', { label: device.label || t('my.phone') })} onClose={onClose} pending={busy}
      closeLabel={t('share.close')} labelId="my-revoke-title" testId="my-revoke-dialog"
      foot={(
        <>
          <Button onClick={onClose} disabled={busy} data-autofocus>{t('share.cancel')}</Button>
          <Button variant="danger" busy={busy} onClick={revoke} data-testid="my-revoke-confirm">{t('devices.revokeConfirm')}</Button>
        </>
      )}
    >
      <p className="p2p-revoke__text">{t('my.revokeBody')}</p>
      {error && <Callout tone="error" role="alert" testId="my-revoke-error">{error}</Callout>}
    </Dialog>
  )
}
