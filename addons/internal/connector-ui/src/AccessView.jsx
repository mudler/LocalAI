/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The latest decisions the host made for this binding. What the form will do
// is in CapabilitiesPanel; this list is the log of what happened.
import { useCallback, useEffect, useState } from 'react'
import { Notice } from './controls'
import { connectorsApi } from './api'
import { pageLocale } from './connector'

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
      {!id ? <p className="cn-muted">{s('access.needSave')}</p> : (<>
        {error && <Notice tone="error" role="alert">{error}</Notice>}
        <div>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-busy={loading ? 'true' : undefined} disabled={loading} onClick={load}>{s('access.refresh')}</button>
        </div>
        {view && (<>
          <p className="dk-hint">{s('access.recentHelp')}</p>
          {(view.recent || []).length === 0 ? <p className="cn-muted">{s('access.recentNone')}</p> : (
            <ul className="cn-runs" aria-label={s('access.recentTitle')} data-testid="connector-audit">
              {view.recent.slice(0, 20).map((e, i) => (
                <li key={i}>
                  <span className={`cn-status cn-status--${e.allowed ? 'done' : 'failed'}`}>{s(e.allowed ? 'access.allowed' : 'access.denied')}</span>
                  <span>{title(e.capability)}</span>
                  <span className="cn-muted">{new Date(e.at).toLocaleString(pageLocale(), { dateStyle: 'short', timeStyle: 'medium' })}</span>
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
