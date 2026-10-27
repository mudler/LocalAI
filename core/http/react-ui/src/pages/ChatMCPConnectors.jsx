/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
import Icon from '../components/Icon'
import { useAuth } from '../context/AuthContext'
import { chatMCPConnectorsApi } from '../utils/api'
import {
  browserImportBody, canSaveMCP, emptyMCP, forgetBrowserServers, mcpPayload, mcpTarget, readBrowserServers,
} from '../utils/chatMCP'
import './chatskills.css'
import './chatauto.css'

const storage = () => { try { return window.localStorage } catch { return null } }

// Rows of name and value that only go in: a saved value is never shown, so the
// form shows what is new and says when something is saved.
function PairRows({ label, help, rows, onChange, nameLabel, valueLabel, addLabel, removeLabel, testId }) {
  return (
    <div className="cs-field" data-testid={testId}>
      <span>{label}</span>
      {rows.map((r, i) => (
        <div className="ca-add" key={i}>
          <input className="input" aria-label={`${nameLabel} ${i + 1}`} placeholder={nameLabel} value={r.name} autoComplete="off"
            onChange={(e) => onChange(rows.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} />
          <input className="input" type="password" aria-label={`${valueLabel} ${i + 1}`} placeholder={valueLabel} value={r.value} autoComplete="off"
            onChange={(e) => onChange(rows.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))} />
          <button type="button" className="btn btn-secondary btn-sm" aria-label={`${removeLabel} ${i + 1}`}
            onClick={() => onChange(rows.filter((_, j) => j !== i))}><Icon name="close" /></button>
        </div>
      ))}
      <div>
        <button type="button" className="btn btn-secondary btn-sm" onClick={() => onChange([...rows, { name: '', value: '' }])}>{addLabel}</button>
      </div>
      {help && <span className="cs-muted">{help}</span>}
    </div>
  )
}

function ImportBanner({ servers, busy, onImport, t }) {
  return (
    <div className="cs-notice" role="note" data-testid="mcp-import">
      <strong>{t('mcpPage.import.title')}</strong>
      <p className="cs-muted">{t('mcpPage.import.text')}</p>
      <ul className="ca-runs">
        {servers.map((s) => (
          <li key={s.id || s.url}>
            <span>{s.name || mcpTarget({ transport: 'http', url: s.url })}</span>
            <span className="cs-muted">{mcpTarget({ transport: 'http', url: s.url })}</span>
            {s.headerCount > 0 && <span className="cs-muted">{t('mcpPage.import.headersCount', { count: s.headerCount })}</span>}
          </li>
        ))}
      </ul>
      <button type="button" className="btn btn-primary btn-sm" disabled={busy} onClick={onImport}>
        {busy ? t('mcpPage.import.busy') : servers.length === 1 ? t('mcpPage.import.buttonOne') : t('mcpPage.import.button', { count: servers.length })}
      </button>
    </div>
  )
}

export default function ChatMCPConnectors() {
  const { t } = useTranslation('chats')
  const { isAdmin } = useAuth()
  const [items, setItems] = useState(null)
  const [error, setError] = useState('')
  const [forbidden, setForbidden] = useState('')
  const [note, setNote] = useState('')
  const [results, setResults] = useState({})
  const [form, setForm] = useState(null)
  const [busy, setBusy] = useState(false)
  const [toDelete, setToDelete] = useState(null)
  const [browser, setBrowser] = useState(() => { const s = storage(); return s ? readBrowserServers(s) : [] })
  const [importing, setImporting] = useState(false)

  const fail = useCallback((e) => {
    if (e?.status === 403) { setForbidden(e.message || t('mcpPage.forbidden')); return }
    setError(e?.message || t('errors.send'))
  }, [t])

  const load = useCallback(async () => {
    try {
      const res = await chatMCPConnectorsApi.list()
      setItems(res.connectors || [])
      setForbidden('')
    } catch (e) { setItems([]); fail(e) }
  }, [fail])

  useEffect(() => { load() }, [load])

  const open = (c) => { setForm(c); setNote('') }

  const save = async (e) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const body = mcpPayload(form)
      if (form.id) await chatMCPConnectorsApi.update(form.id, body)
      else await chatMCPConnectorsApi.create(body)
      setForm(null)
      await load()
    } catch (err) { fail(err) } finally { setBusy(false) }
  }

  const act = async (fn) => {
    setError('')
    try { await fn(); await load() } catch (e) { fail(e) }
  }

  const test = async (c) => {
    setResults((r) => ({ ...r, [c.id]: { busy: true } }))
    try {
      const res = await chatMCPConnectorsApi.test(c.id)
      setResults((r) => ({ ...r, [c.id]: { ok: res.ok, tools: res.tools || [], error: res.error || '' } }))
    } catch (e) {
      setResults((r) => ({ ...r, [c.id]: { ok: false, tools: [], error: e?.message || t('errors.send') } }))
    }
  }

  const importAll = async () => {
    const s = storage()
    if (!s) return
    setImporting(true)
    setError('')
    setNote('')
    const taken = (items || []).map((c) => c.name)
    const done = []
    const problems = []
    for (const server of browser) {
      const body = browserImportBody(server, taken)
      try {
        await chatMCPConnectorsApi.create(body)
        taken.push(body.name)
        done.push(server.id)
      } catch (e) {
        problems.push(t('mcpPage.import.failed', { name: server.name || mcpTarget({ transport: 'http', url: server.url }), error: e?.message || '' }))
      }
    }
    // A token leaves the browser only for a server the API accepted.
    forgetBrowserServers(s, done)
    setBrowser(readBrowserServers(s))
    setNote([done.length ? t('mcpPage.import.done', { count: done.length }) : '', ...problems, problems.length ? t('mcpPage.import.left') : ''].filter(Boolean).join(' '))
    setImporting(false)
    await load()
  }

  if (forbidden) return <div className="cs-notice" role="note">{forbidden}</div>

  const stdio = form?.transport === 'stdio'
  return (
    <section data-testid="mcp-connectors">
      <header className="cs-head">
        <p className="cs-muted">{t('mcpPage.intro')}</p>
        <button type="button" className="btn btn-primary btn-sm" onClick={() => open(emptyMCP())}>
          <Icon name="plus" /> {t('mcpPage.new')}
        </button>
      </header>

      {error && (
        <div className="cs-error" role="alert">
          <span>{error}</span>
          <button type="button" className="cs-link" onClick={() => setError('')}>{t('errors.dismiss')}</button>
        </div>
      )}
      {note && <p className="cs-muted" role="status">{note}</p>}
      {browser.length > 0 && <ImportBanner servers={browser} busy={importing} onImport={importAll} t={t} />}

      {form && (
        <form className="cs-form" onSubmit={save} aria-label={form.id ? t('mcpPage.edit') : t('mcpPage.new')}>
          <label className="cs-field">
            <span>{t('mcpPage.name')}</span>
            <input className="input" value={form.name} required maxLength={80} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </label>
          {(isAdmin || stdio) && (
            <label className="cs-field">
              <span>{t('mcpPage.transport')}</span>
              <select className="input" aria-label={t('mcpPage.transport')} value={form.transport} disabled={Boolean(form.id)}
                onChange={(e) => setForm({ ...form, transport: e.target.value })}>
                <option value="http">{t('mcpPage.http')}</option>
                <option value="stdio" disabled={!isAdmin}>{t('mcpPage.stdio')}</option>
              </select>
            </label>
          )}
          {!stdio && (
            <>
              <label className="cs-field">
                <span>{t('mcpPage.url')}</span>
                <input className="input" type="url" inputMode="url" value={form.url} required placeholder="https://" autoComplete="off"
                  onChange={(e) => setForm({ ...form, url: e.target.value })} />
                <span className="cs-muted">{t('mcpPage.urlHelp')}</span>
              </label>
              <label className="cs-field">
                <span>{t('mcpPage.token')}</span>
                <input className="input" type="password" autoComplete="off" value={form.token} disabled={form.clearToken}
                  placeholder={form.has_token ? t('mcpPage.tokenSaved') : ''} onChange={(e) => setForm({ ...form, token: e.target.value })} />
                <span className="cs-muted">{t('mcpPage.tokenHelp')}</span>
              </label>
              {form.has_token && (
                <label className="ca-radio">
                  <input type="checkbox" checked={form.clearToken} onChange={(e) => setForm({ ...form, clearToken: e.target.checked })} />
                  <span>{t('mcpPage.clearToken')}</span>
                </label>
              )}
              <PairRows label={t('mcpPage.headers')} help={form.has_headers ? t('mcpPage.headersSaved') : t('mcpPage.headersHelp')} rows={form.headers}
                onChange={(rows) => setForm({ ...form, headers: rows })} nameLabel={t('mcpPage.headerName')} valueLabel={t('mcpPage.headerValue')}
                addLabel={t('mcpPage.addHeader')} removeLabel={t('mcpPage.removeRow')} testId="mcp-headers" />
              {form.has_headers && (
                <label className="ca-radio">
                  <input type="checkbox" checked={form.clearHeaders} onChange={(e) => setForm({ ...form, clearHeaders: e.target.checked })} />
                  <span>{t('mcpPage.clearHeaders')}</span>
                </label>
              )}
            </>
          )}
          {stdio && (
            <>
              <label className="cs-field">
                <span>{t('mcpPage.command')}</span>
                <input className="input" value={form.command} required autoComplete="off" onChange={(e) => setForm({ ...form, command: e.target.value })} />
              </label>
              <label className="cs-field">
                <span>{t('mcpPage.args')}</span>
                <textarea className="input cs-body" rows={3} value={form.argsText} onChange={(e) => setForm({ ...form, argsText: e.target.value })} />
              </label>
              <PairRows label={t('mcpPage.env')} help={form.has_env ? t('mcpPage.envSaved') : t('mcpPage.envHelp')} rows={form.env}
                onChange={(rows) => setForm({ ...form, env: rows })} nameLabel={t('mcpPage.envName')} valueLabel={t('mcpPage.headerValue')}
                addLabel={t('mcpPage.addEnv')} removeLabel={t('mcpPage.removeRow')} testId="mcp-env" />
              {form.has_env && (
                <label className="ca-radio">
                  <input type="checkbox" checked={form.clearEnv} onChange={(e) => setForm({ ...form, clearEnv: e.target.checked })} />
                  <span>{t('mcpPage.clearEnv')}</span>
                </label>
              )}
            </>
          )}
          <label className="ca-radio">
            <input type="checkbox" checked={form.enabled} onChange={(e) => setForm({ ...form, enabled: e.target.checked })} />
            <span>{t('mcpPage.enabled')}</span>
          </label>
          <span className="cs-muted">{t('mcpPage.enabledHelp')}</span>
          <div className="cs-actions">
            <button type="submit" className="btn btn-primary btn-sm" disabled={busy || !canSaveMCP(form)}>{t('mcpPage.save')}</button>
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => setForm(null)}>{t('mcpPage.cancel')}</button>
          </div>
        </form>
      )}

      <ul className="cs-list">
        {(items || []).map((c) => {
          const r = results[c.id]
          return (
            <li key={c.id} className="cs-row" data-testid="mcp-row">
              <div className="cs-row__text">
                <span className="cs-row__name">{c.name}</span>
                <span className="cs-muted">
                  {[mcpTarget(c), c.transport === 'stdio' ? t('mcpPage.local') : '', c.has_token ? t('mcpPage.tokenSet') : '',
                    c.has_headers ? t('mcpPage.headersSet') : '', c.has_env ? t('mcpPage.envSet') : ''].filter(Boolean).join(' · ')}
                </span>
                {r && !r.busy && (
                  <span className={r.ok ? 'cs-muted' : 'cs-error'} role="status" data-testid="mcp-test-result">
                    {r.ok ? (r.tools.length ? t('mcpPage.testOk', { tools: r.tools.join(', ') }) : t('mcpPage.testNoTools')) : t('mcpPage.testFailed', { error: r.error })}
                  </span>
                )}
              </div>
              <span className="cs-row__actions">
                <label className="ca-radio">
                  <input type="checkbox" checked={c.enabled} aria-label={t('mcpPage.onFor', { name: c.name })}
                    onChange={(e) => {
                      const on = e.target.checked
                      setItems((list) => list.map((x) => (x.id === c.id ? { ...x, enabled: on } : x)))
                      act(() => (on ? chatMCPConnectorsApi.enable(c.id) : chatMCPConnectorsApi.disable(c.id)))
                    }} />
                  <span>{t('mcpPage.on')}</span>
                </label>
                <button type="button" className="btn btn-secondary btn-sm" disabled={r?.busy} onClick={() => test(c)}>
                  {r?.busy ? t('mcpPage.testing') : t('mcpPage.test')}
                </button>
                <button type="button" className="btn btn-secondary btn-sm"
                  onClick={() => open({ ...emptyMCP(), ...c, argsText: (c.args || []).join('\n'), token: '', headers: [], env: [] })}>{t('mcpPage.editButton')}</button>
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => setToDelete(c)}>{t('mcpPage.delete')}</button>
              </span>
            </li>
          )
        })}
      </ul>
      {items && items.length === 0 && !form && <p className="cs-muted">{t('mcpPage.empty')}</p>}
      <ChatDeleteConfirm item={toDelete} kind="mcp" onConfirm={(c) => { setToDelete(null); act(() => chatMCPConnectorsApi.remove(c.id)) }} onCancel={() => setToDelete(null)} />
    </section>
  )
}
