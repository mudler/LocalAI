/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The details of one connection in the host's docked pane: the facts, the last
// test (kept in this browser), the recent refusals and senders. Remove, Edit
// and Test connection sit at the foot.
import { useEffect, useState } from 'react'
import SidePane from '../../host-compat/src/components/ui/SidePane'
import { getSdk } from '../../host-compat/src/runtime.js'
import Icon from '../../host-compat/src/components/Icon'
import { connectorsApi } from './api'
import { refusalRows } from './check'
import { pageLocale } from './connector'
import { Notice, Switch } from './controls'
import { StateChip } from './ConnectionRow'
import { chatText } from './people'

const when = (at) => (at ? new Date(at).toLocaleString(pageLocale(), { dateStyle: 'short', timeStyle: 'short' }) : '')

// The scope the add-on style sheet needs: the pane and the sheet draw in the
// host's overlay layer, outside the root element of the add-on.
export function AddonScope({ className = '', children }) {
  return (
    <div data-addon={getSdk().addonId} className="cn-scope">
      <div className={className || undefined}>{children}</div>
    </div>
  )
}

// The last refusals of the host for this connection: a few lines, newest first.
function RecentRefusals({ id, s }) {
  const [rows, setRows] = useState(null)
  useEffect(() => {
    let live = true
    Promise.all([connectorsApi.access(id).catch(() => null), connectorsApi.senders(id).catch(() => null)]).then(([view, log]) => {
      if (live) setRows(refusalRows({ recent: view?.recent || [], senders: log?.senders || [], titleOf: (x) => x, limit: 3 }))
    })
    return () => { live = false }
  }, [id])
  if (rows === null) return null
  return (
    <section className="cn-pane__section" aria-labelledby={`cn-pane-ref-${id}`}>
      <h3 className="cn-pane__title" id={`cn-pane-ref-${id}`}>{s('refusals.title')}</h3>
      {rows.length === 0 ? <p className="cn-muted" data-testid="connector-pane-refusals-none">{s('refusals.none')}</p> : (
        <ul className="cn-runs cn-refusals" data-testid="connector-pane-refusals">
          {rows.map((r) => (
            <li key={r.key}>
              <span>{r.name || r.sender || s('refusals.talk')}</span>
              {r.chat && <span className="cn-muted">{s('access.chat', { chat: chatText(r.chat, r.chatName) })}</span>}
              <span className="cn-muted cn-refusals__reason">{r.reason || (r.code ? s(`sender.reason.${r.code}`, { defaultValue: r.code }) : '')}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

export default function ConnectionPane({ c, state, attention, check, testing, busy, messaging, facts, Senders, RowInfo, t, s, onClose, onToggle, onTest, onFix, fix, onEdit, onDelete, onShare }) {
  const stateText = s(`conn.state.${state}`)
  const reasonText = attention
    ? attention.cause === 'inactive' ? t('inactive', { reason: attention.reason })
      : attention.cause === 'noToken' ? s('conn.reason.noToken')
        : attention.cause === 'duplicate' ? s('conn.reason.duplicate', { name: attention.reason })
          : s('conn.reason.testFailed', { reason: attention.reason })
    : ''
  return (
    <SidePane open title={c.name} onClose={onClose} testId="connector-pane" meta={<StateChip state={state} label={stateText} />}
      footer={(
        <div className="cn-pane__foot">
          <button type="button" className="dk-btn dk-btn--danger-ghost dk-btn--sm" data-testid="connector-pane-delete" onClick={() => onDelete(c)}>{s('conn.pane.remove')}</button>
          <span className="cn-pane__grow" />
          {onShare && (
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" data-testid="connector-share" aria-label={s('share.button', { name: c.name })}
              onClick={() => onShare(c)}>{s('share.label')}</button>
          )}
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" data-testid="connector-pane-edit" onClick={() => onEdit(c)}>{t('editButton')}</button>
          <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" aria-busy={testing ? 'true' : undefined} disabled={testing} data-testid="connector-pane-test" onClick={() => onTest(c)}>
            {testing ? s('conn.pane.testing') : s('conn.pane.test')}
          </button>
        </div>
      )}>
      <AddonScope className="cn-pane__body">
        {reasonText && (
          <Notice tone="warn" role="status" testId="connector-pane-attention">
            <p className="cn-notice__text">{reasonText}</p>
            {fix && <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid={`connector-pane-fix-${fix.kind}`} onClick={() => onFix(c, fix.kind)}>{fix.label}</button>}
          </Notice>
        )}
        {messaging && !c.credentials_only && (
          <div className="cn-pane__use">
            <span id={`cn-pane-use-${c.id}`}>{t('on')}</span>
            <Switch checked={Boolean(c.enabled)} disabled={busy} label={t('onFor', { name: c.name })} testId="connector-pane-switch" onChange={(on) => onToggle(c, on)} />
          </div>
        )}
        <section className="cn-pane__section" aria-labelledby={`cn-pane-test-${c.id}`}>
          <h3 className="cn-pane__title" id={`cn-pane-test-${c.id}`}>{s('conn.pane.lastTest')}</h3>
          {!check && <p className="cn-muted" data-testid="connector-pane-never">{s('conn.pane.never')}</p>}
          {check?.ok && <p className="cn-pane__ok" data-testid="connector-pane-ok"><Icon name="check-circle" /> {s('conn.pane.passed', { when: when(check.at) })}{check.text ? <span className="cn-muted"> {check.text}</span> : null}</p>}
          {check && !check.ok && <p className="cn-pane__bad" data-testid="connector-pane-failed"><Icon name="alert-circle" /> {s('conn.pane.failed', { when: when(check.at) })}{check.reason ? <span className="cn-muted"> {check.reason}</span> : null}</p>}
        </section>
        <section className="cn-pane__section" aria-labelledby={`cn-pane-conn-${c.id}`}>
          <h3 className="cn-pane__title" id={`cn-pane-conn-${c.id}`}>{s('conn.pane.connection')}</h3>
          <dl className="cn-kv" data-testid="connector-pane-facts">
            {facts.map(([k, v]) => (<span className="cn-kv__row" key={k}><dt>{k}</dt><dd>{v}</dd></span>))}
          </dl>
          {RowInfo && <div className="cn-pane__info"><RowInfo c={c} t={t} /></div>}
        </section>
        {messaging && <RecentRefusals id={c.id} s={s} />}
        {messaging && (
          <section className="cn-pane__section" aria-labelledby={`cn-pane-snd-${c.id}`}>
            <h3 className="cn-pane__title" id={`cn-pane-snd-${c.id}`}>{t('senders')}</h3>
            {Senders}
          </section>
        )}
      </AddonScope>
    </SidePane>
  )
}
