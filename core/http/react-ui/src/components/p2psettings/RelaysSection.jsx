/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import ConfirmDialog from '../ConfirmDialog'
import Dialog from '../Dialog'
import { Button, Callout, EmptyState, LoadError, SkeletonRows, StatusChip } from '../ui'
import { p2pRelaysApi } from '../../utils/api'
import RunRelayDialog from './RunRelayDialog'
import RelayLimitsDialog from './RelayLimitsDialog'
import { ago, hasLimits, limitParts, relayName, relayPass, relayTone } from './p2pSettingsModel'

function Checked({ at }) {
  const { t } = useTranslation('p2psettings')
  const a = ago(at)
  if (!a.key) return null
  return <span className="p2ps-relay__sub">{t(`relays.checked.${a.key}`, { count: a.count })}</span>
}

function Limits({ relay }) {
  const { t } = useTranslation('p2psettings')
  if (relay.state === 'unreachable' || relay.state === 'checking') return <span className="p2ps-relay__sub">—</span>
  if (!hasLimits(relay.limits)) return <span>{t('relays.noLimit')}</span>
  return <span>{t('relays.perCircuit', { limits: limitParts(relay.limits).join(', ') })}</span>
}

// The row of the relay inside this server ("Via your server").
function ThisServerRow({ status }) {
  const { t } = useTranslation('p2psettings')
  const rs = status?.relay_service || {}
  const addr = (status?.addrs || [])[0] || ''
  return (
    <tr data-row data-testid="relay-row-self">
      <td>
        <span className="p2ps-relay__name">
          <span className="dk-table-name">{t('relays.thisServer')}</span>
          {addr && <span className="p2ps-relay__addr" title={addr}>{addr}</span>}
        </span>
      </td>
      <td>
        <StatusChip tone={rs.running ? 'ok' : 'idle'} dot>{rs.running ? t('relays.self.running') : t(`relays.self.reason.${rs.reason || 'off'}`)}</StatusChip>
      </td>
      <td><span className="p2ps-relay__sub">{t('relays.self.pairedOnly')}</span></td>
      <td><span className="p2ps-relay__sub">{t('relays.self.limits')}</span></td>
      <td className="dk-table-actions" />
    </tr>
  )
}

// Relays this server uses. A gated relay this server administers gets a
// relay pass for each invite; an open relay goes into invites with a warning;
// the others are not used in invites until they are fixed.
export default function RelaysSection({ status, relays, error, onReload, addToast }) {
  const { t } = useTranslation('p2psettings')
  const [running, setRunning] = useState(false)
  const [limitsOf, setLimitsOf] = useState(null)
  const [removing, setRemoving] = useState(null)
  const [claiming, setClaiming] = useState(null)
  const [claimCode, setClaimCode] = useState('')
  const [busy, setBusy] = useState('')

  const act = async (key, fn, okText) => {
    setBusy(key)
    try {
      await fn()
      if (okText) addToast?.(okText, 'success')
      await onReload()
    } catch (err) {
      addToast?.(err.message, 'error')
    } finally {
      setBusy('')
    }
  }

  const outOfSync = (relays || []).filter(r => r.state === 'out_of_sync')
  const peerID = status?.peer_id || ''

  return (
    <section className="p2ps-section p2ps-relays" aria-labelledby="p2ps-relays-title" data-testid="p2p-relays">
      <div className="p2ps-head">
        <h2 className="st-h2" id="p2ps-relays-title">{t('relays.title')}</h2>
        <div className="p2ps-head__actions">
          <Button variant="primary" icon="server" onClick={() => setRunning(true)} data-testid="relay-run">{t('relays.run')}</Button>
        </div>
      </div>
      <p className="st-note">{t('relays.lede')}</p>

      {error && <LoadError title={t('relays.loadError')} error={error} onRetry={onReload} />}
      {!error && !relays && <SkeletonRows />}
      {!error && relays && (
        <div className="dk-table-wrap">
          <table className="dk-table dk-table--compact">
            <caption className="dk-sr-only">{t('relays.title')}</caption>
            <thead>
              <tr>
                <th scope="col">{t('relays.col.relay')}</th>
                <th scope="col">{t('relays.col.health')}</th>
                <th scope="col">{t('relays.col.invites')}</th>
                <th scope="col" className="dk-hide-phone">{t('relays.col.limits')}</th>
                <th scope="col"><span className="dk-sr-only">{t('relays.col.actions')}</span></th>
              </tr>
            </thead>
            <tbody>
              <ThisServerRow status={status} />
              {relays.map(r => {
                const pass = relayPass(r)
                return (
                  <tr key={r.id} data-row data-testid="relay-row" data-relay={r.id} data-state={r.state}>
                    <td>
                      <span className="p2ps-relay__name">
                        <span className="dk-table-name">{r.label || t('relays.yourRelay')}</span>
                        <span className="p2ps-relay__addr" title={r.addr}>{r.addr}</span>
                        {r.admins > 0 && r.state !== 'open' && <span className="p2ps-relay__sub">{t('relays.admins', { count: r.admins })}</span>}
                      </span>
                    </td>
                    <td>
                      <span className="p2ps-relay__health">
                        <StatusChip tone={relayTone(r.state)} dot>
                          {t(`relays.state.${r.state}`, { defaultValue: r.state })}
                        </StatusChip>
                        {r.latency_ms > 0 && r.state !== 'unreachable' && <span className="p2ps-relay__sub">{t('relays.latency', { ms: r.latency_ms })}</span>}
                        {r.health && (r.state === 'unreachable' || r.state === 'denied') && <span className="p2ps-relay__sub">{r.health}</span>}
                        <Checked at={r.checked_at} />
                      </span>
                    </td>
                    <td>
                      <span className="p2ps-relay__health">
                        <StatusChip tone={pass === 'pass' ? 'ok' : pass === 'open' ? 'warn' : 'idle'} dot testId="relay-pass">
                          {t(`relays.pass.${pass}`)}
                        </StatusChip>
                        {r.members > 0 && r.state !== 'open' && <span className="p2ps-relay__sub">{t('relays.members', { count: r.members })}</span>}
                      </span>
                    </td>
                    <td className="dk-hide-phone p2ps-relay__limits"><Limits relay={r} /></td>
                    <td className="dk-table-actions">
                      <span className="p2ps-relay__actions">
                        {r.state === 'unclaimed' && (
                          <Button size="sm" onClick={() => { setClaiming(r); setClaimCode('') }} data-testid="relay-claim">{t('relays.claim')}</Button>
                        )}
                        {(r.state === 'open' || r.state === 'in_sync' || r.state === 'out_of_sync') && (
                          <Button size="sm" variant="ghost" icon="sliders" aria-label={t('relays.limitsOf', { name: relayName(r) })} onClick={() => setLimitsOf(r)} data-testid="relay-limits" />
                        )}
                        <Button size="sm" variant="ghost" icon="refresh" aria-label={t('relays.checkOf', { name: relayName(r) })} busy={busy === `check:${r.id}`} onClick={() => act(`check:${r.id}`, () => p2pRelaysApi.check(r.id))} data-testid="relay-check" />
                        <Button size="sm" variant="ghost" icon="trash" aria-label={t('relays.removeOf', { name: relayName(r) })} onClick={() => setRemoving(r)} data-testid="relay-remove" />
                      </span>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
      {!error && relays && relays.length === 0 && (
        <EmptyState
          variant="first-run" title={t('relays.empty.title')} body={t('relays.empty.body')}
          actions={<Button onClick={() => setRunning(true)}>{t('relays.run')}</Button>}
        />
      )}
      {outOfSync.map(r => (
        <Callout
          key={r.id} tone="warn" title={t('relays.outOfSync.title', { name: relayName(r) })} testId="relay-out-of-sync"
          actions={<Button size="sm" busy={busy === `sync:${r.id}`} onClick={() => act(`sync:${r.id}`, () => p2pRelaysApi.sync(r.id), t('relays.synced'))}>{t('relays.sync')}</Button>}
        >
          {t('relays.outOfSync.body', { count: status?.devices || 0 })}
        </Callout>
      ))}

      {running && <RunRelayDialog peerID={peerID} addToast={addToast} onClose={() => { setRunning(false); onReload() }} onAdded={() => { setRunning(false); onReload() }} />}
      {limitsOf && <RelayLimitsDialog relay={limitsOf} addToast={addToast} onSaved={onReload} onClose={() => setLimitsOf(null)} />}
      {claiming && (
        <Dialog
          title={t('relays.claimTitle')} description={t('relays.claimLede')} onClose={() => setClaiming(null)} pending={busy === 'claim'}
          closeLabel={t('actions.close')} testId="relay-claim-dialog" labelId="relay-claim-title"
          foot={(
            <>
              <Button onClick={() => setClaiming(null)}>{t('actions.cancel')}</Button>
              <Button
                variant="primary" busy={busy === 'claim'} disabled={!claimCode.trim()}
                onClick={() => act('claim', () => p2pRelaysApi.claim(claiming.id, claimCode.trim()), t('relays.claimed')).then(() => setClaiming(null))}
              >
                {t('relays.claim')}
              </Button>
            </>
          )}
        >
          <div className="dk-field">
            <label className="dk-label" htmlFor="relay-claim-code">{t('run.claimLabel')}</label>
            <input id="relay-claim-code" className="dk-input dk-input--mono" value={claimCode} placeholder="A972-F9B0-048A" onChange={(e) => setClaimCode(e.target.value)} />
          </div>
        </Dialog>
      )}
      <ConfirmDialog
        open={Boolean(removing)} danger title={t('relays.removeTitle')}
        message={removing ? t('relays.removeBody', { name: relayName(removing) }) : ''}
        confirmLabel={t('relays.remove')} pending={busy === 'remove'}
        onConfirm={() => act('remove', () => p2pRelaysApi.remove(removing.id), t('relays.removed')).then(() => setRemoving(null))}
        onCancel={() => setRemoving(null)}
      />
    </section>
  )
}
