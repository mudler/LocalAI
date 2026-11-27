/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Dialog from '../components/Dialog'
import AdminPage from '../components/admin/AdminPage'
import { useHubAuth } from '../components/hub/useHubAuth'
import P2PTabs, { P2P_SETTINGS_PATH } from '../components/p2p/P2PTabs'
import ShareInstanceDialog from '../components/p2p/ShareInstanceDialog'
import { Button, Callout, EmptyState, LoadError, Notice, SkeletonRows, StatusChip, Tag, useLoad } from '../components/ui'
import { copyToClipboard } from '../utils/clipboard'
import { p2pNetApi } from '../utils/api'
import {
  NO_OWNER, clock, deviceOwners, devicesOfOwner, modelGrant, pathKey, pathTone, pendingInvites, relativeTime, secondsLeft, shortPeer, sortedDevices, splitWorkers,
} from '../utils/p2p'
import '../components/p2p/p2p.css'

// How often the page reads the invites and devices again: an invite counts
// down and a device's path changes while the page is open.
const REFRESH_MS = 10_000

// Sharing: what other people hold on this server. The invites that wait for a
// device or a remote, and the devices and remotes paired with it, each with how
// it reaches the server now and which models a remote may use. "Create invite"
// makes an invite. A worker that joined over P2P is a worker of the cluster and
// is on Workers, so it is not listed here. All of it needs P2P mode "Connect
// and share"; in another mode the page says so and links to the settings.
export default function P2P() {
  const { t } = useTranslation('p2p')
  const { addToast } = useOutletContext() || {}
  const status = useLoad(() => p2pNetApi.status(), [])
  const sharing = status.status === 'ready' && status.data?.mode === 'share'
  const [sharingOpen, setSharingOpen] = useState(false)

  return (
    <AdminPage
      width="full"
      className="p2p-page"
      data-testid="p2p-page"
      title={t('title')}
      supporting={t('supporting')}
      action={sharing ? <Button variant="primary" icon="share" onClick={() => setSharingOpen(true)} data-testid="p2p-share-open">{t('share.open')}</Button> : null}
    >
      <P2PTabs />
      {status.status === 'loading' && <SkeletonRows count={3} />}
      {status.status === 'error' && <LoadError title={t('loadStatusFailed')} error={status.error} onRetry={status.retry} />}
      {status.status === 'ready' && !sharing && <ModeNotice status={status.data} />}
      {sharing && <SharingLists status={status.data} addToast={addToast} sharingOpen={sharingOpen} setSharingOpen={setSharingOpen} />}
    </AdminPage>
  )
}

// "Sharing is on", the share address of this server, and the link to where the
// mode and the relays are set.
function ShareState({ status, addToast }) {
  const { t } = useTranslation('p2p')
  const peer = status?.peer_id
  const copy = async () => {
    const ok = await copyToClipboard(peer)
    addToast?.(t(ok ? 'sharing.addressCopied' : 'sharing.addressCopyFailed'), ok ? 'success' : 'error')
  }
  return (
    <div className="p2p-state" data-testid="p2p-share-state">
      <StatusChip tone="ok" dot>{t('sharing.on')}</StatusChip>
      {peer && (
        <>
          <span className="p2p-state__label">{t('sharing.address')}</span>
          <span className="dk-mono" title={peer} data-testid="p2p-share-address">{shortPeer(peer)}</span>
          <Button size="sm" variant="ghost" icon="copy" onClick={copy}>{t('sharing.copy')}</Button>
        </>
      )}
      <Button size="sm" variant="ghost" to={P2P_SETTINGS_PATH} data-testid="p2p-settings-link">{t('sharing.settings')}</Button>
    </div>
  )
}

// Workers that joined over P2P are on Workers: say so, with the link, on a
// cluster and wherever the page hid some.
function WorkersNote({ hidden }) {
  const { t } = useTranslation('p2p')
  const { features } = useHubAuth()
  if (!features.distributed && hidden === 0) return null
  return (
    <Callout tone="info" testId="p2p-workers-note" actions={<Button size="sm" to="/app/nodes" data-testid="p2p-workers-link">{t('sharing.workersLink')}</Button>}>
      {t('sharing.workersNote')}
    </Callout>
  )
}

// Why the lists are not shown: P2P is off, or it only dials out.
function ModeNotice({ status }) {
  const { t } = useTranslation('p2p')
  const mode = status?.mode === 'connect' ? 'connect' : 'off'
  return (
    <Notice
      severity="info"
      title={t(`mode.${mode}.title`)}
      reason={t(`mode.${mode}.reason`)}
      fix={{ label: t('mode.fix'), to: P2P_SETTINGS_PATH, testId: 'p2p-mode-fix' }}
      testId="p2p-mode-notice"
    />
  )
}

function SharingLists({ status, addToast, sharingOpen, setSharingOpen }) {
  const { t } = useTranslation('p2p')
  const invites = useLoad(() => p2pNetApi.invites(), [])
  const devices = useLoad(() => p2pNetApi.devices(), [])
  const [now, setNow] = useState(Date.now())
  const [revoking, setRevoking] = useState(null)

  const reloadInvites = invites.reload
  const reloadDevices = devices.reload
  const reload = useCallback(() => { reloadInvites(); reloadDevices() }, [reloadInvites, reloadDevices])
  useEffect(() => {
    const id = setInterval(reload, REFRESH_MS)
    return () => clearInterval(id)
  }, [reload])
  // The countdowns tick each second; the rows come from the last read.
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])

  const cancel = async (inv) => {
    try {
      await p2pNetApi.cancelInvite(inv.id)
      addToast?.(t('invites.canceled', { label: inv.label || t(`kinds.${inv.kind}.title`) }), 'success')
    } catch (err) {
      addToast?.(t('invites.cancelFailed', { message: err?.message || '' }), 'error')
    }
    reloadInvites()
  }

  const hiddenWorkers = (invites.status === 'ready' ? splitWorkers(invites.data?.invites).workers.length : 0)
    + (devices.status === 'ready' ? splitWorkers(devices.data?.devices).workers.length : 0)

  return (
    <>
      <ShareState status={status} addToast={addToast} />
      <WorkersNote hidden={hiddenWorkers} />
      <InvitesSection invites={invites} now={now} onCancel={cancel} onShare={() => setSharingOpen(true)} />
      <DevicesSection devices={devices} onRevoke={setRevoking} />
      {sharingOpen && (
        <ShareInstanceDialog status={status} addToast={addToast} onChanged={reload} onClose={() => setSharingOpen(false)} />
      )}
      {revoking && (
        <RevokeDialog
          device={revoking}
          onClose={() => setRevoking(null)}
          onDone={() => {
            addToast?.(t('devices.revoked', { label: revoking.label || shortPeer(revoking.peer_id) }), 'success')
            setRevoking(null)
            reloadDevices()
          }}
        />
      )}
    </>
  )
}

function InvitesSection({ invites, now, onCancel, onShare }) {
  const { t, i18n } = useTranslation('p2p')
  const rows = invites.status === 'ready' ? pendingInvites(splitWorkers(invites.data?.invites).shared, now) : []
  return (
    <section className="p2p-section" aria-labelledby="p2p-invites-title" data-testid="p2p-invites">
      <div className="p2p-section__head">
        <h2 className="p2p-h2" id="p2p-invites-title">{t('invites.title')} <span className="p2p-count dk-mono">{rows.length}</span></h2>
        <p className="p2p-section__note">{t('invites.note')}</p>
      </div>
      {invites.status === 'loading' && <SkeletonRows count={2} />}
      {invites.status === 'error' && <LoadError title={t('invites.loadFailed')} error={invites.error} onRetry={invites.retry} />}
      {invites.status === 'ready' && rows.length === 0 && (
        <EmptyState
          variant="first-run" icon="share" level={3} testId="p2p-invites-empty"
          title={t('invites.empty.title')} body={t('invites.empty.body')}
          actions={<Button icon="share" onClick={onShare}>{t('share.open')}</Button>}
        />
      )}
      {rows.length > 0 && (
        <div className="dk-table-wrap">
          <table className="dk-table p2p-table">
            <caption className="dk-sr-only">{t('invites.title')}</caption>
            <thead>
              <tr>
                <th scope="col">{t('invites.col.label')}</th>
                <th scope="col" className="dk-hide-phone">{t('invites.col.kind')}</th>
                <th scope="col" className="dk-hide-phone">{t('invites.col.created')}</th>
                <th scope="col">{t('invites.col.expires')}</th>
                <th scope="col" className="dk-hide-phone">{t('invites.col.pass')}</th>
                <th scope="col"><span className="dk-sr-only">{t('invites.col.actions')}</span></th>
              </tr>
            </thead>
            <tbody>
              {rows.map(inv => (
                <tr key={inv.id} data-testid={`p2p-invite-${inv.id}`}>
                  <td>
                    <span className="p2p-cell-title">{inv.label || t(`kinds.${inv.kind}.title`, inv.kind)}</span>
                    <span className="p2p-cell-sub">{t(`kinds.${inv.kind}.title`, inv.kind)}</span>
                    <ModelGrant row={inv} />
                    <span className="p2p-cell-sub dk-only-phone">{inv.relay_pass ? t('invites.passOn', { relay: inv.relay_pass }) : t('invites.passNone')}</span>
                  </td>
                  <td className="dk-hide-phone">{t(`kinds.${inv.kind}.title`, inv.kind)}</td>
                  <td className="dk-hide-phone">
                    {relativeTime(inv.created_at, i18n.language, now)}
                  </td>
                  <td className="dk-mono">{clock(secondsLeft(inv.expires_at, now))}</td>
                  <td className="dk-hide-phone">
                    {inv.relay_pass
                      ? <StatusChip tone="ok" dot>{t('invites.passOn', { relay: inv.relay_pass })}</StatusChip>
                      : <span className="p2p-cell-sub">{t('invites.passNone')}</span>}
                  </td>
                  <td className="p2p-cell-actions">
                    <Button size="sm" variant="danger-ghost" onClick={() => onCancel(inv)} data-testid={`p2p-invite-cancel-${inv.id}`}>
                      {t('invites.cancel')}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

function DevicesSection({ devices, onRevoke }) {
  const { t, i18n } = useTranslation('p2p')
  const [params, setParams] = useSearchParams()
  const all = devices.status === 'ready' ? sortedDevices(splitWorkers(devices.data?.devices).shared) : []
  const owners = deviceOwners(all)
  // The owner filter lives in the address (?owner=<user ID>), so a link can
  // open the devices of one user. An owner with no device left shows all.
  const asked = params.get('owner') || ''
  const owner = owners.some(o => o.id === asked) ? asked : ''
  const rows = devicesOfOwner(all, owner)
  const active = rows.filter(d => d.status !== 'revoked').length
  const pickOwner = (id) => {
    const next = new URLSearchParams(params)
    if (id) next.set('owner', id)
    else next.delete('owner')
    setParams(next, { replace: true })
  }
  const ownerName = (o) => (o.id === NO_OWNER ? t('devices.owner.none') : o.name || o.id)
  return (
    <section className="p2p-section" aria-labelledby="p2p-devices-title" data-testid="p2p-devices">
      <div className="p2p-section__head">
        <h2 className="p2p-h2" id="p2p-devices-title">{t('devices.title')} <span className="p2p-count">{t('devices.active', { count: active })}</span></h2>
        <p className="p2p-section__note">{t('devices.note')}</p>
      </div>
      {owners.length > 1 && (
        <div className="p2p-owner-filter">
          <label className="dk-label" htmlFor="p2p-owner-filter">{t('devices.owner.filter')}</label>
          <div className="dk-select-wrap">
            <select id="p2p-owner-filter" className="dk-select" value={owner} onChange={e => pickOwner(e.target.value)} data-testid="p2p-owner-filter">
              <option value="">{t('devices.owner.all')}</option>
              {owners.map(o => <option key={o.id} value={o.id}>{ownerName(o)}</option>)}
            </select>
          </div>
        </div>
      )}
      {devices.status === 'loading' && <SkeletonRows count={3} />}
      {devices.status === 'error' && <LoadError title={t('devices.loadFailed')} error={devices.error} onRetry={devices.retry} />}
      {devices.status === 'ready' && rows.length === 0 && (
        <EmptyState variant="first-run" icon="monitor" level={3} testId="p2p-devices-empty" title={t('devices.empty.title')} body={t('devices.empty.body')} />
      )}
      {rows.length > 0 && (
        <div className="dk-table-wrap">
          <table className="dk-table p2p-table">
            <caption className="dk-sr-only">{t('devices.title')}</caption>
            <thead>
              <tr>
                <th scope="col">{t('devices.col.label')}</th>
                <th scope="col" className="dk-hide-phone">{t('devices.col.kind')}</th>
                <th scope="col" className="dk-hide-phone">{t('devices.col.owner')}</th>
                <th scope="col" className="dk-hide-phone">{t('devices.col.peer')}</th>
                <th scope="col" className="dk-hide-phone">{t('devices.col.seen')}</th>
                <th scope="col">{t('devices.col.path')}</th>
                <th scope="col" className="dk-hide-phone">{t('devices.col.status')}</th>
                <th scope="col"><span className="dk-sr-only">{t('devices.col.actions')}</span></th>
              </tr>
            </thead>
            <tbody>
              {rows.map(d => {
                const revoked = d.status === 'revoked'
                return (
                  <tr key={d.id} data-testid={`p2p-device-${d.id}`} data-revoked={revoked || undefined}>
                    <td>
                      <span className="p2p-label-line">
                        <span className="p2p-cell-title">{d.label || shortPeer(d.peer_id)}</span>
                        {d.sign_in && <Tag tone="accent" icon="user" title={t('devices.signsInHint')} testId={`p2p-device-signin-${d.id}`}>{t('devices.signsIn')}</Tag>}
                      </span>
                      <span className="p2p-cell-sub">{t(`kinds.${d.kind}.title`, d.kind)}</span>
                      <span className="p2p-cell-sub dk-only-phone">{deviceOwner(d, t)}</span>
                      <ModelGrant row={d} />
                    </td>
                    <td className="dk-hide-phone">{t(`kinds.${d.kind}.title`, d.kind)}</td>
                    <td className="dk-hide-phone p2p-cell-owner" data-testid={`p2p-device-owner-${d.id}`}>{deviceOwner(d, t)}</td>
                    <td className="dk-hide-phone dk-mono" title={d.peer_id}>{shortPeer(d.peer_id)}</td>
                    <td className="dk-hide-phone">{d.last_seen_at ? relativeTime(d.last_seen_at, i18n.language) : t('devices.never')}</td>
                    <td><DevicePath device={d} /></td>
                    <td className="dk-hide-phone">
                      <StatusChip tone={revoked ? 'idle' : 'ok'} dot>{t(revoked ? 'devices.status.revoked' : 'devices.status.active')}</StatusChip>
                    </td>
                    <td className="p2p-cell-actions">
                      {!revoked && (
                        <Button size="sm" variant="danger-ghost" onClick={() => onRevoke(d)} data-testid={`p2p-device-revoke-${d.id}`}>
                          {t('devices.revoke')}
                        </Button>
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

// Who a device belongs to: the owner's name, the user ID when the name is not
// known, or "No account" for a device the API key of LOCALAI_API_KEY paired.
function deviceOwner(d, t) {
  if (!d.owner_id) return t('devices.owner.none')
  return d.owner_name || d.owner_id
}

// What a remote invite or device may use: "All models" or the number of
// models, with the names in the title. Other kinds show nothing.
function ModelGrant({ row }) {
  const { t } = useTranslation('p2p')
  const grant = modelGrant(row)
  if (!grant) return null
  return (
    <span className="p2p-cell-sub" data-testid={`p2p-grant-${row.id}`} title={grant.all ? undefined : grant.names.join(', ')}>
      {grant.all ? t('grant.all') : t('grant.some', { count: grant.names.length })}
    </span>
  )
}

// How a device reaches the server now. A relay that limits the data or the
// time of a connection gets a "Limited" chip: bulk transfers and model splits
// need a direct path or a relay without a small limit.
function DevicePath({ device }) {
  const { t } = useTranslation('p2p')
  if (device.status === 'revoked') return <span className="p2p-cell-sub">-</span>
  if (!device.online) return <StatusChip tone="idle" dot>{t('devices.path.offline')}</StatusChip>
  const path = device.path || {}
  const { key, relay } = pathKey(path)
  const rtt = path.rtt_ms > 0 ? t('devices.path.rtt', { ms: path.rtt_ms }) : ''
  return (
    <span className="p2p-path">
      <StatusChip tone={pathTone(path)} dot>{t(`devices.path.${key}`, { relay })}</StatusChip>
      {path.limited && <Tag title={t('devices.path.limitedHint')}>{t('devices.path.limited')}</Tag>}
      {rtt && <span className="p2p-cell-sub">{rtt}</span>}
    </span>
  )
}

// The confirmation of a revoke: what stops at once and what does not.
function RevokeDialog({ device, onClose, onDone }) {
  const { t } = useTranslation('p2p')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const name = device.label || shortPeer(device.peer_id)
  const revoke = async () => {
    setBusy(true); setError('')
    try {
      await p2pNetApi.revokeDevice(device.id)
      onDone()
    } catch (err) {
      setError(err?.message || '')
      setBusy(false)
    }
  }
  return (
    <Dialog
      role="alertdialog" title={t('devices.revokeTitle', { label: name })} onClose={onClose} pending={busy}
      closeLabel={t('share.close')} labelId="p2p-revoke-title" testId="p2p-revoke-dialog"
      foot={(
        <>
          <Button onClick={onClose} disabled={busy} data-autofocus>{t('share.cancel')}</Button>
          <Button variant="danger" busy={busy} onClick={revoke} data-testid="p2p-revoke-confirm">{t('devices.revokeConfirm')}</Button>
        </>
      )}
    >
      <p className="p2p-revoke__text">{t(`devices.revokeBody.${device.kind === 'remote' ? 'remote' : device.kind === 'device' ? 'device' : 'other'}`)}</p>
      {error && <Callout tone="error" role="alert" testId="p2p-revoke-error">{error}</Callout>}
    </Dialog>
  )
}
