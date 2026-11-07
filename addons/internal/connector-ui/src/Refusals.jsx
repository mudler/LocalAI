/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// "Recent refusals": one line for each message or permission the host refused,
// from the access log and the sender log of this server, with the buttons that
// fix the common cases. Both logs are kept in memory and reset when the server
// restarts; the page says so.
import { pageLocale } from './connector'
import { refusalActions, refusalRows } from './check'
import { chatText, personText } from './people'
import { usePeople } from './Person'

export default function Refusals({ recent, senders, titleOf, form, setForm, onAllow, s }) {
  const people = usePeople()
  const rows = refusalRows({ recent, senders, titleOf })
  return (
    <div className="cn-sub" data-testid="connector-refusals">
      <h3 className="cn-sub__title">{s('refusals.title')}</h3>
      <p className="dk-hint" data-testid="connector-refusals-note">{s('refusals.help')}</p>
      {rows.length === 0 ? <p className="cn-muted" data-testid="connector-refusals-none">{s('refusals.none')}</p> : (
        <ul className="cn-runs cn-refusals" aria-label={s('refusals.title')} data-testid="connector-audit">
          {rows.map((r) => {
            const name = people?.get(r.sender)?.name || r.name || r.sender
            return (
              <li key={r.key} data-source={r.source} data-code={r.code || undefined}>
                <span className="cn-status cn-status--failed">{s('refusals.refused')}</span>
                {r.sender && <span data-testid="connector-audit-who">{personText(people, r.sender, r.name)}</span>}
                {r.chat && <span className="cn-muted" data-testid="connector-audit-chat">{s('access.chat', { chat: chatText(r.chat, r.chatName) })}</span>}
                <span data-testid="connector-audit-action">{r.action ? s('refusals.action', { action: r.action }) : s('refusals.talk')}</span>
                <span className="cn-muted cn-refusals__reason" data-testid="connector-audit-reason">{r.reason || (r.code ? s(`sender.reason.${r.code}`, { defaultValue: r.code }) : '')}</span>
                <span className="cn-muted">{r.at ? new Date(r.at).toLocaleString(pageLocale(), { dateStyle: 'short', timeStyle: 'medium' }) : ''}</span>
                {refusalActions(r, form).map((a) => a.type === 'allow' ? (
                  <button key="allow" type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="connector-audit-allow" onClick={() => onAllow(a.sender)}>
                    {s('refusals.allow', { name })}
                  </button>
                ) : (
                  <button key="act" type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="connector-audit-act"
                    onClick={() => setForm((f) => ({ ...f, allow_trust: { ...(f.allow_trust || {}), [a.chat]: 'act' } }))}>
                    {s('refusals.act', { channel: r.chatName || r.chat })}
                  </button>
                ))}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
