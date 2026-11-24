/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, Callout, Segmented, Switch } from '../ui'
import HostDialog from './HostDialog'
import { BLANKET_CONFIRM, blanketBody, confirmMatches, normalizeOverview } from './hostGrantsState'

function Toggle({ label, hint, checked, onChange, disabled, testId }) {
  const id = useId()
  return (
    <div className="ad-toggle-row">
      <div className="ad-toggle-row__text">
        <span id={id} className="ad-toggle-row__label">{label}</span>
        {hint && <span className="ad-quiet">{hint}</span>}
      </div>
      <Switch checked={checked} onChange={onChange} labelledBy={id} disabled={disabled} testId={testId} />
    </div>
  )
}

// The blanket switch: "approve everything an add-on asks for". Off by
// default. Turning it on takes the administrator's own browser session and a
// typed sentence; the risk is stated before the field. Turning it off keeps
// the grants it made, marked blanket, and offers to revoke them all.
export default function BlanketPanel({ overview, api, locked, busy, run, holders }) {
  const { t } = useTranslation('addons')
  const b = overview.blanket
  const on = b.mode !== 'off'
  const fixed = b.setBy === 'deployment'
  const [dialog, setDialog] = useState(null)
  const readOnly = locked || fixed
  const labelId = useId()

  const openOn = () => setDialog({ kind: 'on', mode: on ? b.mode : 'official', includeOptional: b.includeOptional, applyToInstalled: false, typed: '' })
  const openOff = () => setDialog({ kind: 'off', revokeOnOff: false })
  const submit = async () => {
    const d = dialog
    const body = d.kind === 'on'
      ? blanketBody(overview, { mode: d.mode, includeOptional: d.includeOptional, applyToInstalled: d.applyToInstalled, confirm: d.typed.trim() })
      : blanketBody(overview, { mode: 'off', includeOptional: b.includeOptional, revokeOnOff: d.revokeOnOff })
    const ok = await run(() => api.setBlanket(body), t(d.kind === 'on' ? 'hostAccess.blanket.nowOn' : 'hostAccess.blanket.nowOff'))
    if (ok) setDialog(null)
  }
  const revokeBlanket = () => run(async () => {
    // One add-on at a time, each against the revision the owner holds now.
    const fresh = async () => normalizeOverview(await api.overview())
    for (const h of holders) {
      const cur = await fresh()
      const addon = cur.addons.find(a => a.id === h.id)
      if (!addon) continue
      const keep = [...addon.ops, ...addon.noLongerRequested].filter(o => o.status === 'allowed' && o.source !== 'deployment' && o.source !== 'blanket').map(o => o.name)
      if (keep.length === 0) await api.revoke(h.id, cur.revision)
      else await api.setOps(h.id, { revision: cur.revision, ops: keep })
    }
  }, t('hostAccess.blanket.revoked'))

  return (
    <section className="ad-host__section" aria-labelledby="host-blanket-title" data-testid="host-blanket" data-mode={b.mode}>
      <h2 id="host-blanket-title" className="ad-host__heading">{t('hostAccess.blanket.title')}</h2>
      <p className="ad-host__intro">{t('hostAccess.blanket.intro')}</p>
      {fixed && <Callout tone="info" testId="blanket-fixed" className="ad-callout">{t('hostAccess.blanket.fixed')}</Callout>}
      <div className="ad-toggle-row ad-toggle-row--main">
        <div className="ad-toggle-row__text">
          <span id={labelId} className="ad-toggle-row__label">{t('hostAccess.blanket.switch')}</span>
          <span className="ad-quiet" data-testid="blanket-state">{on ? t(`hostAccess.blanket.scope.${b.mode}`) : t('hostAccess.blanket.offText')}{on && b.includeOptional ? ` ${t('hostAccess.blanket.withOptional')}` : ''}</span>
        </div>
        <Switch checked={on} labelledBy={labelId} disabled={readOnly || busy} onChange={(next) => (next ? openOn() : openOff())} testId="blanket-switch" />
      </div>
      {on && !readOnly && <div><Button size="sm" variant="ghost" onClick={openOn} data-testid="blanket-change">{t('hostAccess.blanket.change')}</Button></div>}
      {holders.length > 0 && (
        <div className="ad-blanket-held" data-testid="blanket-held">
          <p>{t('hostAccess.blanket.held', { count: holders.length })}</p>
          <ul className="ad-plain-list">{holders.map(h => <li key={h.id}><strong>{h.title}</strong> <span className="ad-quiet">{h.ops.join(', ')}</span></li>)}</ul>
          {!locked && <Button size="sm" disabled={busy} onClick={revokeBlanket} data-testid="blanket-revoke-all">{t('hostAccess.blanket.revokeAll')}</Button>}
        </div>
      )}

      {dialog && dialog.kind === 'on' && (
        <HostDialog
          testId="blanket-on-dialog"
          title={t('hostAccess.blanket.onTitle')}
          onClose={() => setDialog(null)}
          busy={busy}
          actions={(
            <>
              <Button onClick={() => setDialog(null)} disabled={busy}>{t('catalog.cancel')}</Button>
              <Button variant="primary" busy={busy} disabled={busy || !confirmMatches(dialog.mode, dialog.typed)} onClick={submit} data-testid="blanket-confirm">{t('hostAccess.blanket.turnOn')}</Button>
            </>
          )}
        >
          <Segmented
            options={[{ id: 'official', label: t('hostAccess.blanket.modeOfficial') }, { id: 'any', label: t('hostAccess.blanket.modeAny') }]}
            value={dialog.mode}
            onChange={(mode) => setDialog(d => ({ ...d, mode, typed: '' }))}
            label={t('hostAccess.blanket.modeLabel')}
          />
          <Callout tone="warn" testId="blanket-risk" role="note">{t(`hostAccess.blanket.risk.${dialog.mode}`)}</Callout>
          <Toggle label={t('hostAccess.blanket.includeOptional')} hint={t('hostAccess.blanket.includeOptionalHint')} checked={dialog.includeOptional} onChange={(v) => setDialog(d => ({ ...d, includeOptional: v }))} testId="blanket-optional" />
          <Toggle label={t('hostAccess.blanket.applyInstalled')} hint={t('hostAccess.blanket.applyInstalledHint')} checked={dialog.applyToInstalled} onChange={(v) => setDialog(d => ({ ...d, applyToInstalled: v }))} testId="blanket-installed" />
          <label className="ad-field">
            <span>{t('hostAccess.blanket.typeLabel', { phrase: BLANKET_CONFIRM[dialog.mode] })}</span>
            <input
              className="dk-input"
              type="text"
              autoComplete="off"
              spellCheck={false}
              value={dialog.typed}
              onChange={(e) => setDialog(d => ({ ...d, typed: e.target.value }))}
              data-testid="blanket-phrase"
              data-autofocus="true"
              aria-describedby="blanket-risk-note"
            />
          </label>
          <span id="blanket-risk-note" className="dk-sr-only">{t(`hostAccess.blanket.risk.${dialog.mode}`)}</span>
        </HostDialog>
      )}
      {dialog && dialog.kind === 'off' && (
        <HostDialog
          testId="blanket-off-dialog"
          title={t('hostAccess.blanket.offTitle')}
          onClose={() => setDialog(null)}
          busy={busy}
          actions={(
            <>
              <Button onClick={() => setDialog(null)} disabled={busy}>{t('catalog.cancel')}</Button>
              <Button variant="primary" busy={busy} disabled={busy} onClick={submit} data-autofocus="true" data-testid="blanket-off-confirm">{t('hostAccess.blanket.turnOff')}</Button>
            </>
          )}
        >
          <p>{t('hostAccess.blanket.offBody')}</p>
          {holders.length > 0 && (
            <Toggle
              label={t('hostAccess.blanket.alsoRevoke', { count: holders.length })}
              hint={t('hostAccess.blanket.alsoRevokeHint', { names: holders.map(h => h.title).join(', ') })}
              checked={dialog.revokeOnOff}
              onChange={(v) => setDialog(d => ({ ...d, revokeOnOff: v }))}
              testId="blanket-also-revoke"
            />
          )}
        </HostDialog>
      )}
    </section>
  )
}
