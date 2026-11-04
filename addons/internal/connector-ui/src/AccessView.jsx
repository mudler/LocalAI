/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// A read-only answer to "what can this sender do". The host runs the same
// check it uses for each turn, for a sender on the allowlist, a paired sender
// and a sender of each role, and lists its latest decisions. It shows the
// saved binding: unsaved changes on this page do not count yet.
import { useCallback, useEffect, useState } from 'react'
import { Notice } from './controls'
import { connectorsApi } from './api'

export const ACCESS_TEXT = {
  'access.title': 'What can a sender do',
  'access.help': 'The check the host runs for each message, shown for the saved settings. It changes nothing.',
  'access.needSave': 'Save this bot first to see what its senders may do.',
  'access.refresh': 'Refresh',
  'access.loading': 'Loading',
  'access.can': 'May: {{list}}',
  'access.cannot': 'May not: {{list}}',
  'access.nothing': 'nothing',
  'access.recentTitle': 'Latest decisions',
  'access.recentHelp': 'The latest checks of this server, newest first. They hold IDs and names of permissions, never message text. They reset when the server restarts.',
  'access.recentNone': 'No decision yet.',
  'access.allowed': 'allowed',
  'access.denied': 'denied',
  'access.who': 'sender {{senders}}',
  'access.roles': 'roles {{roles}}',
  'access.via': 'via {{via}}',
}

function Subject({ sub, title, s }) {
  const ok = sub.decisions.filter((d) => d.allowed)
  const no = sub.decisions.filter((d) => !d.allowed)
  return (
    <li className="cn-access__subject" data-testid={`connector-access-${sub.id}`}>
      <span className="cn-access__who">{sub.title}</span>
      <span data-testid="connector-access-can">{s('access.can', { list: ok.length ? ok.map((d) => title(d.capability)).join(', ') : s('access.nothing') })}</span>
      {no.length > 0 && (
        <details className="cn-access__no">
          <summary>{s('access.cannot', { list: no.map((d) => title(d.capability)).join(', ') })}</summary>
          <ul>{no.map((d) => <li key={d.capability}><span>{title(d.capability)}</span>{d.reason && <span className="cn-muted">: {d.reason}</span>}</li>)}</ul>
        </details>
      )}
    </li>
  )
}

export default function AccessView({ binding, capabilities, s, version }) {
  const [view, setView] = useState(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const id = binding.id
  const load = useCallback(async () => {
    if (!id) return
    setLoading(true); setError('')
    try { setView(await connectorsApi.access(id)) } catch (e) { setError(e?.message || '') } finally { setLoading(false) }
  }, [id])
  useEffect(() => { load() }, [load, version])
  const title = (name) => (capabilities || []).find((c) => c.name === name)?.title || name
  return (
    <fieldset className="cn-group" data-testid="connector-access">
      <legend>{s('access.title')}</legend>
      <p className="dk-hint">{s('access.help')}</p>
      {!id ? <p className="cn-muted">{s('access.needSave')}</p> : (<>
        {error && <Notice tone="error" role="alert">{error}</Notice>}
        <div>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-busy={loading ? 'true' : undefined} disabled={loading} onClick={load}>{s('access.refresh')}</button>
        </div>
        {view && (<>
          <ul className="cn-access" aria-label={s('access.title')}>
            {(view.subjects || []).map((sub) => <Subject key={sub.id} sub={sub} title={title} s={s} />)}
          </ul>
          <h3 className="cn-sub__title">{s('access.recentTitle')}</h3>
          <p className="dk-hint">{s('access.recentHelp')}</p>
          {(view.recent || []).length === 0 ? <p className="cn-muted">{s('access.recentNone')}</p> : (
            <ul className="cn-runs" aria-label={s('access.recentTitle')} data-testid="connector-audit">
              {view.recent.slice(0, 20).map((e, i) => (
                <li key={i}>
                  <span className={`cn-status cn-status--${e.allowed ? 'done' : 'failed'}`}>{s(e.allowed ? 'access.allowed' : 'access.denied')}</span>
                  <span>{title(e.capability)}</span>
                  <span className="cn-muted">{new Date(e.at).toLocaleString([], { dateStyle: 'short', timeStyle: 'medium' })}</span>
                  {(e.senders || []).length > 0 && <span className="cn-muted cn-mono">{s('access.who', { senders: e.senders.join(', ') })}</span>}
                  {(e.roles || []).length > 0 && <span className="cn-muted">{s('access.roles', { roles: e.roles.join(', ') })}</span>}
                  {e.via && <span className="cn-muted">{s('access.via', { via: e.via })}</span>}
                  {e.reason && <span className="cn-muted">{e.reason}</span>}
                </li>
              ))}
            </ul>
          )}
        </>)}
      </>)}
    </fieldset>
  )
}
