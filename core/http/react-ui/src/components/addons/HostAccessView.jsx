/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, Callout, EmptyState, LoadError, Switch, SkeletonRows } from '../ui'
import { SwitchGroup, SwitchRow } from '../chat/SwitchList'
import ConfirmDialog from '../ConfirmDialog'
import { hostGrantsApi, addonsApi } from '../../utils/addonsClient'
import { useAddonActions } from './useAddonActions'
import ApproveUpgradeDialog, { WaitingBanner } from './ApproveUpgradeDialog'
import BlanketPanel from './BlanketPanel'
import DenySection from './DenySection'
import { opNote } from './HostAccessList'
import { isLocalFile } from './fileInstall'
import { stateChip } from './addonState'
import { describeError } from './errors'
import { blanketHolders, editedOps, groupOps, opEditable, pageOps, pendingApprovals } from './hostGrantsState'

const SOURCE_KEY = { deployment: 'deployment', you: 'you', blanket: 'blanket' }

function when(at, language) {
  const ms = Date.parse(at)
  if (!at || Number.isNaN(ms)) return ''
  try { return new Date(ms).toLocaleDateString(language || undefined, { dateStyle: 'medium' }) } catch { return at.slice(0, 10) }
}

// The Host access page: what each add-on holds on this server, where each
// permission came from, and the switches that revoke it. Administrators only;
// the owner checks again on every call, and only the administrator's own
// browser session can change anything here.
export default function HostAccessView({ grants, api = hostGrantsApi }) {
  const { t, i18n } = useTranslation('addons')
  const actions = useAddonActions(addonsApi, { onSettled: () => grants.reload() })
  const [notice, setNotice] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [approving, setApproving] = useState(null)

  const data = grants.data
  const pending = useMemo(() => (data ? pendingApprovals(data) : []), [data])
  const holders = useMemo(() => (data ? blanketHolders(data) : []), [data])

  const run = async (fn, okText) => {
    setNotice(null)
    const { error } = await grants.mutate(fn)
    if (error) setNotice({ tone: 'error', text: describeError(error, t) })
    else if (okText) setNotice({ tone: 'ok', text: okText })
    return !error
  }

  if (grants.status === 'loading') return <SkeletonRows count={3} label={t('hostAccess.loadingPage')} className="ad-skeleton" />
  if (grants.status === 'error') {
    return (
      <LoadError title={t('hostAccess.loadError')} testId="hostaccess-load-error" nothingChanged={false} onRetry={grants.reload}>
        {describeError(grants.error, t)}
      </LoadError>
    )
  }

  const locked = data.locked
  const setOne = (addon, name, next) => {
    const ops = editedOps(addon, { [name]: next })
    const op = pageOps(addon).find(o => o.name === name)
    const go = () => run(() => api.setOps(addon.id, { revision: data.revision, ops }))
    if (!next && op && op.required) setConfirm({ kind: 'stop', addon, op, go })
    else go()
  }
  const revokeAll = (addon) => run(() => api.revoke(addon.id, data.revision))
  const keepOwn = (addon, names) => run(() => api.setOps(addon.id, { revision: data.revision, ops: editedOps(addon, {}), keep: names }))

  return (
    <div className="ad-host" data-testid="host-access">
      <p className="ad-host__intro">{t('hostAccess.intro')}</p>
      {grants.stale && <Callout tone="info" role="status" className="ad-callout">{t('apps.stale')}</Callout>}
      {locked && <Callout tone="info" testId="host-locked" className="ad-callout">{t(data.lockedReason === 'no_auth' ? 'hostAccess.lockedNoAuth' : 'hostAccess.locked')}</Callout>}
      {notice && <Callout tone={notice.tone} role={notice.tone === 'error' ? 'alert' : 'status'} testId="host-notice" className="ad-callout">{notice.text}</Callout>}
      {pending.map(a => <WaitingBanner key={a.id} addon={a} onReview={() => setApproving(a)} />)}

      <section className="ad-host__section" aria-labelledby="host-addons-title">
        <h2 id="host-addons-title" className="ad-host__heading">{t('hostAccess.addonsTitle')}</h2>
        {data.addons.length === 0 && <EmptyState variant="unavailable" title={t('hostAccess.emptyTitle')} body={t('hostAccess.emptyBody')} />}
        {data.addons.map(addon => {
          const ops = pageOps(addon)
          const stateKey = stateChip({ state: addon.state }).key
          const blanketOps = ops.filter(o => o.source === 'blanket' && o.status === 'allowed').map(o => o.name)
          const holdsUi = ops.some(o => o.status === 'allowed' && o.source !== 'deployment')
          const stoppedText = addon.stopped ? t('hostAccess.stopped', { ops: addon.stopped }) : ''
          return (
            <article className="ad-host-card" key={addon.id} data-testid="host-addon" data-app={addon.id} aria-labelledby={`host-${addon.id}`}>
              <header className="ad-host-card__head">
                <div>
                  <h3 id={`host-${addon.id}`} className="ad-host-card__title">{addon.title}</h3>
                  <span className="ad-quiet">{[addon.version, t(`state.${stateKey}`)].filter(Boolean).join(' · ')}</span>
                </div>
                <div className="ad-host-card__acts">
                  {blanketOps.length > 0 && !locked && (
                    <Button size="sm" variant="ghost" disabled={grants.busy} onClick={() => keepOwn(addon, blanketOps)} data-testid="host-keep-own" aria-label={t('hostAccess.keepOwnNamed', { name: addon.title })}>{t('hostAccess.keepOwn')}</Button>
                  )}
                  {holdsUi && !locked && (
                    <Button size="sm" variant="ghost" disabled={grants.busy} onClick={() => setConfirm({ kind: 'revoke', addon, go: () => revokeAll(addon) })} data-testid="host-revoke-all" aria-label={t('hostAccess.revokeAllNamed', { name: addon.title })}>{t('hostAccess.revokeAll')}</Button>
                  )}
                </div>
              </header>
              {isLocalFile(addon.sourceId) && <Callout tone="warn" testId="host-local-file" className="ad-callout" title={t('file.unsignedTitle')}>{t('file.hostNotice')}</Callout>}
              {stoppedText && <Callout tone="warn" testId="host-stopped" className="ad-callout">{stoppedText}</Callout>}
              {addon.identityMismatch && <Callout tone="warn" testId="host-mismatch" className="ad-callout" actions={!locked && <Button size="sm" disabled={grants.busy} onClick={() => revokeAll(addon)}>{t('hostAccess.removeStale')}</Button>}>{t('hostAccess.identityMismatch')}</Callout>}
              {addon.newOptional.length > 0 && <Callout tone="info" testId="host-new-optional" className="ad-callout">{t('hostAccess.newOptional', { ops: addon.newOptional.join(', ') })}</Callout>}
              {ops.length === 0 && <p className="ad-quiet">{t('hostAccess.asksNothing')}</p>}
              {groupOps(ops).map(g => (
                <SwitchGroup key={g.family} title={t(`hostAccess.family.${g.family}`, { defaultValue: g.family })} testId={`host-group-${g.family}`}>
                  {g.ops.map(op => {
                    const on = op.status === 'allowed'
                    const note = opNote(op, t)
                    const stale = addon.noLongerRequested.some(o => o.name === op.name)
                    const by = [on && op.source ? t(`hostAccess.source.${SOURCE_KEY[op.source]}`) : '', on ? when(op.at, i18n.language) : ''].filter(Boolean).join(' · ')
                    return (
                      <SwitchRow
                        key={op.name}
                        name={op.title}
                        blurb={op.text}
                        desc={stale ? t('hostAccess.noLongerRequested') : on && op.source === 'deployment' ? t('hostAccess.fileEntry') : note.desc}
                        reason={op.status === 'blocked' || op.status === 'unsupported' ? note.reason : undefined}
                        meta={by || (op.risk ? t(`hostAccess.risk.${op.risk}`) : undefined)}
                        metaKind={on ? op.source : undefined}
                        checked={on}
                        disabled={grants.busy || !opEditable(op, locked)}
                        onChange={(next) => setOne(addon, op.name, next)}
                        stacked
                        testId="host-op"
                        data={{ op: op.name, status: op.status, source: op.source || undefined, app: addon.id }}
                      />
                    )
                  })}
                </SwitchGroup>
              ))}
            </article>
          )
        })}
      </section>

      <DenySection overview={data} api={api} locked={locked} busy={grants.busy} run={run} />
      <BlanketPanel overview={data} api={api} locked={locked} busy={grants.busy} run={run} holders={holders} />

      {approving && (
        <ApproveUpgradeDialog
          addon={approving}
          api={addonsApi}
          actions={actions}
          onClose={() => setApproving(null)}
          onDone={() => { setApproving(null); grants.reload(); setNotice({ tone: 'ok', text: t('hostAccess.approve.done', { name: approving.title }) }) }}
        />
      )}
      <ConfirmDialog
        open={Boolean(confirm)}
        danger
        title={confirm ? t(confirm.kind === 'stop' ? 'hostAccess.confirm.stopTitle' : 'hostAccess.confirm.revokeTitle', { name: confirm.addon.title }) : ''}
        message={confirm ? <p>{confirm.kind === 'stop' ? t('hostAccess.confirm.stopBody', { name: confirm.addon.title, op: confirm.op.title }) : t('hostAccess.confirm.revokeBody', { name: confirm.addon.title })}</p> : null}
        confirmLabel={confirm ? t(confirm.kind === 'stop' ? 'hostAccess.confirm.stopAction' : 'hostAccess.revokeAll') : ''}
        onConfirm={() => { const c = confirm; setConfirm(null); c.go() }}
        onCancel={() => setConfirm(null)}
      />
    </div>
  )
}
