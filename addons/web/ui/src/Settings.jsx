import { settingsModel, draftOf, rows, toggle, move, setKey, clearKey, change, testRows } from './lib/model.js'

// The settings section of the Web add-on, drawn on the Settings page for an
// administrator: the search providers in the order they are tried, a field
// for each key (a saved key is never shown, only that it is set), the Google
// search engine ID, the SearXNG address, and a test of each provider.
export function createSettings(sdk, t) {
  const { React } = sdk
  const h = React.createElement
  // eslint-disable-next-line no-unused-vars
  const { useEffect, useState, useCallback } = React

  const call = (args, options) => sdk.api.read({ arguments: args }, options)

  function WebSettingsView({ model, draft, busy, error, saved, tests, onDraft, onSave, onTest }) {
    if (model.hidden) return null
    const list = rows(model, draft)
    return (
      <div className="web-settings" data-testid="web-settings">
        <p>{t('web.settings.intro')}</p>
        {model.note && <p className="web-settings__note" role="status">{model.note}</p>}
        <p data-testid="web-serving">
          {model.serving.length > 0
            ? t('web.settings.serving').replace('{{list}}', model.serving.map(n => t(`web.provider.${n}`)).join(', '))
            : t('web.settings.servingNone')}
        </p>
        <ol className="web-settings__providers">
          {list.map(p => (
            <li key={p.name} className="web-settings__provider" data-testid="web-provider" data-provider={p.name} data-on={p.on ? 'true' : 'false'}>
              <label className="web-settings__check">
                <input type="checkbox" checked={p.on} disabled={busy} onChange={() => onDraft(toggle(draft, p.name))} data-testid={`web-use-${p.name}`} />
                <strong>{t(`web.provider.${p.name}`)}</strong>
              </label>
              {p.on && (
                <span className="web-settings__move">
                  <button type="button" className="btn btn-secondary btn-sm" disabled={busy || p.position === 0} onClick={() => onDraft(move(draft, p.name, -1))} aria-label={t('web.settings.up').replace('{{name}}', t(`web.provider.${p.name}`))}>↑</button>
                  <button type="button" className="btn btn-secondary btn-sm" disabled={busy || p.position === draft.order.length - 1} onClick={() => onDraft(move(draft, p.name, 1))} aria-label={t('web.settings.down').replace('{{name}}', t(`web.provider.${p.name}`))}>↓</button>
                </span>
              )}
              <span className="web-settings__state">{p.ready ? t('web.settings.ready') : p.missing}</span>
              {p.keyed && (
                <label className="web-settings__field">
                  <span>{p.keySet && !draft.clear.includes(p.name) ? t('web.settings.keySet') : t('web.settings.keyNotSet')}</span>
                  <input type="password" value={draft.keys[p.name]} autoComplete="off" disabled={busy} placeholder={t('web.settings.keyPlaceholder')} onChange={e => onDraft(setKey(draft, p.name, e.target.value))} data-testid={`web-key-${p.name}`} />
                  {p.keySet && !draft.clear.includes(p.name) && (
                    <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => onDraft(clearKey(draft, p.name))} data-testid={`web-clear-${p.name}`}>{t('web.settings.removeKey')}</button>
                  )}
                </label>
              )}
              {p.name === 'google' && (
                <label className="web-settings__field">
                  <span>{t('web.settings.googleEngine')}</span>
                  <input type="text" value={draft.googleEngine} disabled={busy} onChange={e => onDraft({ ...draft, googleEngine: e.target.value })} data-testid="web-google-engine" />
                </label>
              )}
              {p.name === 'searxng' && (
                <label className="web-settings__field">
                  <span>{t('web.settings.searxngUrl')}</span>
                  <input type="url" value={draft.searxngUrl} disabled={busy} placeholder="https://searx.example.org" onChange={e => onDraft({ ...draft, searxngUrl: e.target.value })} data-testid="web-searxng-url" />
                </label>
              )}
            </li>
          ))}
        </ol>
        {error && <p className="web-settings__error" role="alert" data-testid="web-error">{error}</p>}
        {saved && <p role="status" data-testid="web-saved">{t('web.settings.saved')}</p>}
        <div className="web-settings__actions">
          <button type="button" className="btn btn-primary btn-sm" onClick={onSave} disabled={busy || draft.order.length === 0} data-testid="web-save">{t('web.settings.save')}</button>
          <button type="button" className="btn btn-secondary btn-sm" onClick={onTest} disabled={busy} data-testid="web-test">{t('web.settings.test')}</button>
        </div>
        {tests.length > 0 && (
          <ul className="web-settings__tests" data-testid="web-tests">
            {tests.map(r => (
              <li key={r.name} data-testid="web-test-row" data-ok={r.ok ? 'true' : 'false'}>
                <strong>{t(`web.provider.${r.name}`)}</strong>: {r.ok ? t('web.settings.testOk').replace('{{count}}', String(r.results)).replace('{{host}}', r.host) : r.error}
              </li>
            ))}
          </ul>
        )}
      </div>
    )
  }

  function WebSettings() {
    const [model, setModel] = useState(null)
    const [draft, setDraft] = useState(null)
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState('')
    const [saved, setSaved] = useState(false)
    const [tests, setTests] = useState([])
    const refresh = useCallback(async () => {
      try {
        const m = settingsModel(await call({ tool: 'status' }))
        setModel(m)
        if (!m.hidden) setDraft(draftOf(m))
      } catch {
        setModel({ hidden: true })
      }
    }, [])
    useEffect(() => { refresh() }, [refresh])
    if (model === null) return <p aria-busy="true" data-testid="web-loading">{t('web.settings.loading')}</p>
    if (model.hidden || !draft) return null
    const onDraft = (d) => { setDraft(d); setSaved(false); setError('') }
    const onSave = async () => {
      setBusy(true)
      setError('')
      setSaved(false)
      try {
        const done = await call({ tool: 'set-settings', ...change(draft) })
        if (!done || done.ok !== true) {
          setError((done && done.error) || t('web.settings.failed'))
        } else {
          setSaved(true)
          await refresh()
        }
      } catch {
        setError(t('web.settings.failed'))
      } finally {
        setBusy(false)
      }
    }
    const onTest = async () => {
      setBusy(true)
      setError('')
      try {
        const answer = await call({ tool: 'test-search' })
        if (!answer || answer.ok !== true) setError((answer && answer.error) || t('web.settings.failed'))
        setTests(testRows(answer))
      } catch {
        setError(t('web.settings.failed'))
      } finally {
        setBusy(false)
      }
    }
    return h(WebSettingsView, { model, draft, busy, error, saved, tests, onDraft, onSave, onTest })
  }

  return { WebSettings, WebSettingsView }
}
