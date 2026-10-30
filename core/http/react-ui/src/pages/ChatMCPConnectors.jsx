/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useOutletContext } from 'react-router-dom'
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
import Icon from '../components/Icon'
import { Button, Callout, EmptyState, LoadError, PageHeader, SkeletonRows } from '../components/ui'
import { useAuth } from '../context/AuthContext'
import { chatMCPConnectorsApi } from '../utils/api'
import {
  browserImportBody, canSaveMCP, emptyMCP, forgetBrowserServers, mcpPayload, mcpTarget, readBrowserServers,
} from '../utils/chatMCP'
import './connectors.css'

const storage = () => { try { return window.localStorage } catch { return null } }
const DOCS = 'https://localai.io/features/chats/#mcp-tools'

// Rows of name and value that only go in: a saved value is never shown, so the
// form shows what is new and says when something is saved.
function PairRows({ label, help, rows, onChange, nameLabel, valueLabel, addLabel, removeLabel, testId }) {
  return (
    <div className="dk-field" data-testid={testId} role="group" aria-label={label}>
      <span className="dk-label">{label}</span>
      {rows.map((r, i) => (
        <div className="cn-pair" key={i}>
          <input className="dk-input" aria-label={`${nameLabel} ${i + 1}`} placeholder={nameLabel} value={r.name} autoComplete="off"
            onChange={(e) => onChange(rows.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} />
          <input className="dk-input" type="password" aria-label={`${valueLabel} ${i + 1}`} placeholder={valueLabel} value={r.value} autoComplete="off"
            onChange={(e) => onChange(rows.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))} />
          <Button variant="ghost" icon="close" aria-label={`${removeLabel} ${i + 1}`}
            onClick={() => onChange(rows.filter((_, j) => j !== i))} />
        </div>
      ))}
      <div>
        <Button size="sm" icon="plus" onClick={() => onChange([...rows, { name: '', value: '' }])}>{addLabel}</Button>
      </div>
      {help && <p className="dk-hint">{help}</p>}
    </div>
  )
}

function Check({ checked, onChange, label, ...rest }) {
  return (
    <label className="dk-choice cn-choice">
      <input type="checkbox" className="dk-check" checked={checked} onChange={onChange} {...rest} />
      <span>{label}</span>
    </label>
  )
}

function ImportBanner({ servers, busy, onImport, t }) {
  return (
    <Callout
      className="cn-import"
      title={t('mcpPage.import.title')}
      testId="mcp-import"
      role="note"
      actions={(
        <Button variant="primary" size="sm" icon="import" busy={busy} onClick={onImport}>
          {busy ? t('mcpPage.import.busy') : servers.length === 1 ? t('mcpPage.import.buttonOne') : t('mcpPage.import.button', { count: servers.length })}
        </Button>
      )}
    >
      {t('mcpPage.import.text')}
      <ul className="cn-import__list">
        {servers.map((s) => (
          <li key={s.id || s.url}>
            <span className="cn-import__name">{s.name || mcpTarget({ transport: 'http', url: s.url })}</span>
            <span className="cn-mono">{mcpTarget({ transport: 'http', url: s.url })}</span>
            {s.headerCount > 0 && <span>{t('mcpPage.import.headersCount', { count: s.headerCount })}</span>}
          </li>
        ))}
      </ul>
    </Callout>
  )
}

export default function ChatMCPConnectors() {
  const { t } = useTranslation('chats')
  const { isAdmin } = useAuth()
  const { addToast } = useOutletContext() || {}
  const [items, setItems] = useState(null)
  const [loadError, setLoadError] = useState(null)
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

  // A failed read replaces the list with an error; it never looks like an
  // empty list. A refresh after an action keeps what is on screen.
  const load = useCallback(async () => {
    try {
      const res = await chatMCPConnectorsApi.list()
      setItems(res.connectors || [])
      setLoadError(null)
      setForbidden('')
    } catch (e) {
      if (e?.status === 403) { fail(e); return }
      setLoadError(e)
    }
  }, [fail])

  useEffect(() => { load() }, [load])

  const open = (c) => { setForm(c); setNote(''); setError('') }

  const save = async (e) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const body = mcpPayload(form)
      if (form.id) await chatMCPConnectorsApi.update(form.id, body)
      else await chatMCPConnectorsApi.create(body)
      setForm(null)
      addToast?.(t('mcpPage.saved', { name: body.name }), 'success')
      await load()
    } catch (err) { fail(err) } finally { setBusy(false) }
  }

  const act = async (fn, done) => {
    setError('')
    try {
      await fn()
      if (done) addToast?.(done, 'success')
      await load()
    } catch (e) { fail(e) }
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

  const loading = items === null && !loadError && !forbidden
  const empty = items !== null && items.length === 0 && !form && !loadError
  const header = (
    <PageHeader
      title={t('mcpPage.title')}
      supporting={t('mcpPage.supporting')}
      actions={!empty && !form && !forbidden && !loadError && (
        <Button variant="primary" icon="plus" disabled={loading} onClick={() => open(emptyMCP())}>{t('mcpPage.new')}</Button>
      )}
    />
  )

  if (forbidden) {
    return (
      <section className="cn-body" data-testid="mcp-connectors">
        {header}
        <EmptyState variant="unavailable" title={forbidden} testId="mcp-forbidden" />
      </section>
    )
  }

  const stdio = form?.transport === 'stdio'
  return (
    <section className="cn-body" data-testid="mcp-connectors">
      {header}

      {error && (
        <Callout tone="error" role="alert" className="cn-gap"
          actions={<Button variant="ghost" size="sm" onClick={() => setError('')}>{t('errors.dismiss')}</Button>}>
          {error}
        </Callout>
      )}
      {note && <Callout tone="ok" role="status" className="cn-gap">{note}</Callout>}
      {browser.length > 0 && <ImportBanner servers={browser} busy={importing} onImport={importAll} t={t} />}

      {form && (
        <form className="dk-card cn-form" onSubmit={save} aria-labelledby="mcp-form-title">
          <h2 className="cn-form__title" id="mcp-form-title">{form.id ? t('mcpPage.edit') : t('mcpPage.new')}</h2>
          <div className="dk-field">
            <label className="dk-label" htmlFor="mcp-name">{t('mcpPage.name')}</label>
            <input id="mcp-name" className="dk-input" value={form.name} required maxLength={80} autoFocus
              onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </div>
          {(isAdmin || stdio) && (
            <div className="dk-field">
              <label className="dk-label" htmlFor="mcp-transport">{t('mcpPage.transport')}</label>
              <span className="dk-select-wrap">
                <select id="mcp-transport" className="dk-input dk-select" value={form.transport} disabled={Boolean(form.id)}
                  onChange={(e) => setForm({ ...form, transport: e.target.value })}>
                  <option value="http">{t('mcpPage.http')}</option>
                  <option value="stdio" disabled={!isAdmin}>{t('mcpPage.stdio')}</option>
                </select>
              </span>
            </div>
          )}
          {!stdio && (
            <>
              <div className="dk-field">
                <label className="dk-label" htmlFor="mcp-url">{t('mcpPage.url')}</label>
                <input id="mcp-url" className="dk-input" type="url" inputMode="url" value={form.url} required placeholder="https://" autoComplete="off"
                  aria-describedby="mcp-url-hint" onChange={(e) => setForm({ ...form, url: e.target.value })} />
                <p className="dk-hint" id="mcp-url-hint">{t('mcpPage.urlHelp')}</p>
              </div>
              <div className="dk-field">
                <label className="dk-label" htmlFor="mcp-token">{t('mcpPage.token')}</label>
                <input id="mcp-token" className="dk-input" type="password" autoComplete="off" value={form.token} disabled={form.clearToken}
                  aria-describedby="mcp-token-hint"
                  placeholder={form.has_token ? t('mcpPage.tokenSaved') : ''} onChange={(e) => setForm({ ...form, token: e.target.value })} />
                <p className="dk-hint" id="mcp-token-hint">{t('mcpPage.tokenHelp')}</p>
                {form.has_token && (
                  <Check checked={form.clearToken} label={t('mcpPage.clearToken')} onChange={(e) => setForm({ ...form, clearToken: e.target.checked })} />
                )}
              </div>
              <PairRows label={t('mcpPage.headers')} help={form.has_headers ? t('mcpPage.headersSaved') : t('mcpPage.headersHelp')} rows={form.headers}
                onChange={(rows) => setForm({ ...form, headers: rows })} nameLabel={t('mcpPage.headerName')} valueLabel={t('mcpPage.headerValue')}
                addLabel={t('mcpPage.addHeader')} removeLabel={t('mcpPage.removeRow')} testId="mcp-headers" />
              {form.has_headers && (
                <Check checked={form.clearHeaders} label={t('mcpPage.clearHeaders')} onChange={(e) => setForm({ ...form, clearHeaders: e.target.checked })} />
              )}
            </>
          )}
          {stdio && (
            <>
              <div className="dk-field">
                <label className="dk-label" htmlFor="mcp-command">{t('mcpPage.command')}</label>
                <input id="mcp-command" className="dk-input dk-input--mono" value={form.command} required autoComplete="off"
                  onChange={(e) => setForm({ ...form, command: e.target.value })} />
              </div>
              <div className="dk-field">
                <label className="dk-label" htmlFor="mcp-args">{t('mcpPage.args')}</label>
                <textarea id="mcp-args" className="dk-textarea dk-input--mono" rows={3} value={form.argsText}
                  onChange={(e) => setForm({ ...form, argsText: e.target.value })} />
              </div>
              <PairRows label={t('mcpPage.env')} help={form.has_env ? t('mcpPage.envSaved') : t('mcpPage.envHelp')} rows={form.env}
                onChange={(rows) => setForm({ ...form, env: rows })} nameLabel={t('mcpPage.envName')} valueLabel={t('mcpPage.headerValue')}
                addLabel={t('mcpPage.addEnv')} removeLabel={t('mcpPage.removeRow')} testId="mcp-env" />
              {form.has_env && (
                <Check checked={form.clearEnv} label={t('mcpPage.clearEnv')} onChange={(e) => setForm({ ...form, clearEnv: e.target.checked })} />
              )}
            </>
          )}
          <div className="dk-field">
            <Check checked={form.enabled} label={t('mcpPage.enabled')} aria-describedby="mcp-enabled-hint"
              onChange={(e) => setForm({ ...form, enabled: e.target.checked })} />
            <p className="dk-hint" id="mcp-enabled-hint">{t('mcpPage.enabledHelp')}</p>
          </div>
          <div className="cn-form__actions">
            <Button type="submit" variant="primary" busy={busy} disabled={!canSaveMCP(form)}>{t('mcpPage.save')}</Button>
            <Button variant="ghost" onClick={() => setForm(null)}>{t('mcpPage.cancel')}</Button>
          </div>
        </form>
      )}

      {loading && <SkeletonRows count={3} />}
      {loadError && (
        <LoadError title={t('mcpPage.loadFailed')} error={loadError} onRetry={load} />
      )}

      {empty && (
        <EmptyState
          title={t('mcpPage.emptyTitle')}
          body={t('mcpPage.emptyBody')}
          facts={[{ text: t('mcpPage.factToken') }, { text: t('mcpPage.factOAuth'), ok: false }]}
          actions={(
            <>
              <Button variant="primary" icon="plus" onClick={() => open(emptyMCP())}>{t('mcpPage.new')}</Button>
              <Button variant="ghost" icon="book" href={DOCS} target="_blank" rel="noopener noreferrer">{t('mcpPage.learn')}</Button>
            </>
          )}
          testId="mcp-empty"
        />
      )}

      {items && items.length > 0 && !loadError && (
        <>
          <ul className="cn-list" aria-label={t('mcpPage.title')}>
            {items.map((c) => {
              const r = results[c.id]
              const meta = [
                c.transport === 'stdio' ? t('mcpPage.local') : '', c.has_token ? t('mcpPage.tokenSet') : '',
                c.has_headers ? t('mcpPage.headersSet') : '', c.has_env ? t('mcpPage.envSet') : '',
              ].filter(Boolean)
              return (
                <li key={c.id} className="cn-row cn-row--tools" data-testid="mcp-row" data-on={c.enabled ? 'true' : 'false'}>
                  <span className="cn-row__lead" aria-hidden="true"><Icon name={c.enabled ? 'plug' : 'plug-off'} /></span>
                  <div className="cn-row__main">
                    <span className="cn-row__name">{c.name}</span>
                    <span className="cn-row__meta">
                      <span className="cn-mono">{mcpTarget(c)}</span>
                      {meta.map((m) => <span key={m}>{m}</span>)}
                    </span>
                    {r && !r.busy && (
                      <span className={`cn-result cn-result--${r.ok ? 'ok' : 'error'}`} role="status" data-testid="mcp-test-result">
                        <Icon name={r.ok ? 'check-circle' : 'alert-circle'} />
                        <span>{r.ok ? (r.tools.length ? t('mcpPage.testOk', { tools: r.tools.join(', ') }) : t('mcpPage.testNoTools')) : t('mcpPage.testFailed', { error: r.error })}</span>
                      </span>
                    )}
                  </div>
                  <span className="cn-row__actions">
                    <Check checked={c.enabled} label={t('mcpPage.on')} aria-label={t('mcpPage.onFor', { name: c.name })}
                      onChange={(e) => {
                        const on = e.target.checked
                        setItems((list) => list.map((x) => (x.id === c.id ? { ...x, enabled: on } : x)))
                        act(() => (on ? chatMCPConnectorsApi.enable(c.id) : chatMCPConnectorsApi.disable(c.id)))
                      }} />
                    <Button size="sm" busy={r?.busy} onClick={() => test(c)}>
                      {r?.busy ? t('mcpPage.testing') : t('mcpPage.test')}
                    </Button>
                    <span className="cn-row__more">
                      <Button variant="ghost" size="sm"
                        onClick={() => open({ ...emptyMCP(), ...c, argsText: (c.args || []).join('\n'), token: '', headers: [], env: [] })}>{t('mcpPage.editButton')}</Button>
                      <Button variant="danger-ghost" size="sm" onClick={() => setToDelete(c)}>{t('mcpPage.delete')}</Button>
                    </span>
                  </span>
                </li>
              )
            })}
          </ul>
          <p className="dk-hint cn-rules">{t('mcpPage.rules')}</p>
        </>
      )}

      <nav className="cn-related" aria-label={t('mcpPage.related')}>
        <span className="cn-eyebrow">{t('mcpPage.related')}</span>
        <Link to="/app/chats/connectors">{t('connectorsPage.title')}</Link>
        <Link to="/app/chats">{t('mcpPage.chats')}</Link>
      </nav>
      <ChatDeleteConfirm item={toDelete} kind="mcp"
        onConfirm={(c) => { setToDelete(null); act(() => chatMCPConnectorsApi.remove(c.id), t('mcpPage.deleted', { name: c.name })) }}
        onCancel={() => setToDelete(null)} />
    </section>
  )
}
