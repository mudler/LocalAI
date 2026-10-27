/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useSearchParams } from 'react-router-dom'
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
import ChatMCPConnectors from './ChatMCPConnectors'
import Icon from '../components/Icon'
import NameList from '../components/ChatNameList'
import { chatAgentsApi, chatConnectorsApi } from '../utils/api'
import { canSave, connectorPayload, emptyConnector, senderLine } from '../utils/chatConnectors'
import './chatskills.css'
import './chatauto.css'

function Senders({ id, onAllowed, t }) {
  const [list, setList] = useState(null)
  const [error, setError] = useState('')
  const load = useCallback(() => {
    chatConnectorsApi.senders(id).then((r) => setList(r.senders || [])).catch((e) => setError(e?.message || ''))
  }, [id])
  useEffect(() => { load() }, [load])
  if (error) return <p className="cs-error" role="alert">{error}</p>
  if (list === null) return null
  if (list.length === 0) return <p className="cs-muted">{t('connectorsPage.noSenders')}</p>
  return (
    <ul className="ca-runs" aria-label={t('connectorsPage.senders')} data-testid="sender-log">
      {list.map((s) => (
        <li key={s.id}>
          <span className={`ca-status ca-status--${s.allowed ? 'done' : 'skipped'}`}>{s.allowed ? t('connectorsPage.accepted') : t('connectorsPage.ignored')}</span>
          <span>{senderLine(s)}</span>
          {!s.allowed && (
            <button type="button" className="btn btn-secondary btn-sm"
              onClick={async () => {
                setError('')
                try { await chatConnectorsApi.allow(id, s.id); load(); onAllowed() } catch (e) { setError(e?.message || '') }
              }}>{t('connectorsPage.allowButton')}</button>
          )}
        </li>
      ))}
    </ul>
  )
}

export default function ChatConnectors() {
  const { t } = useTranslation('chats')
  const [params, setParams] = useSearchParams()
  const kind = params.get('kind') === 'mcp' ? 'mcp' : 'telegram'
  const [items, setItems] = useState([])
  const [agents, setAgents] = useState([])
  const [off, setOff] = useState('')
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  const [form, setForm] = useState(null)
  const [confirmed, setConfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [toDelete, setToDelete] = useState(null)
  const [openSenders, setOpenSenders] = useState('')

  const fail = useCallback((e) => {
    if (e?.status === 409 && e.body?.error?.type === 'connectors_off') { setOff(e.body.error.message); return }
    setError(e?.message || t('errors.send'))
  }, [t])

  const load = useCallback(async () => {
    try {
      const res = await chatConnectorsApi.list()
      setItems(res.connectors || [])
      setOff('')
    } catch (e) { fail(e) }
  }, [fail])

  useEffect(() => { load() }, [load])
  useEffect(() => { chatAgentsApi.list().then((r) => setAgents(r.agents || [])).catch(() => {}) }, [])

  const agentName = (id) => agents.find((a) => a.id === id)?.name || id
  const open = (c) => { setForm(c); setConfirmed(Boolean(c.accept_any)); setNote('') }

  const save = async (e) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const body = connectorPayload(form)
      if (form.id) await chatConnectorsApi.update(form.id, body)
      else await chatConnectorsApi.create(body)
      setForm(null)
      await load()
    } catch (err) { fail(err) } finally { setBusy(false) }
  }

  const act = async (fn) => {
    setError('')
    try { await fn(); await load() } catch (e) { fail(e) }
  }

  const confirmDelete = (c) => { setToDelete(null); act(() => chatConnectorsApi.remove(c.id)) }

  const kinds = (
    <div className="cs-kinds" role="tablist" aria-label={t('connectorsPage.kindSwitch')}>
      {[['telegram', 'kindTelegram'], ['mcp', 'kindMCP']].map(([k, label]) => (
        <button key={k} type="button" role="tab" aria-selected={kind === k} className={`btn btn-sm ${kind === k ? 'btn-primary' : 'btn-secondary'}`}
          onClick={() => setParams(k === 'mcp' ? { kind: 'mcp' } : {}, { replace: true })}>{t(`connectorsPage.${label}`)}</button>
      ))}
    </div>
  )

  if (off && kind === 'telegram') {
    return (
      <div className="cs-page" data-testid="chat-connectors-page">
        <h1 className="cs-title">{t('connectorsPage.title')}</h1>
        {kinds}
        <div className="cs-notice" role="note">{off}</div>
      </div>
    )
  }

  return (
    <div className="cs-page" data-testid="chat-connectors-page">
      <header className="cs-head">
        <h1 className="cs-title">{t('connectorsPage.title')}</h1>
        {kind === 'telegram' && (
          <button type="button" className="btn btn-primary btn-sm" onClick={() => open(emptyConnector())}>
            <Icon name="plus" /> {t('connectorsPage.new')}
          </button>
        )}
      </header>
      <p className="cs-muted">{t('connectorsPage.intro')}</p>
      {kinds}
      {kind === 'mcp' ? <ChatMCPConnectors /> : (
      <>

      {error && (
        <div className="cs-error" role="alert">
          <span>{error}</span>
          <button type="button" className="cs-link" onClick={() => setError('')}>{t('errors.dismiss')}</button>
        </div>
      )}
      {note && <p className="cs-muted" role="status">{note}</p>}

      {form && (
        <form className="cs-form" onSubmit={save} aria-label={form.id ? t('connectorsPage.edit') : t('connectorsPage.new')}>
          <label className="cs-field">
            <span>{t('connectorsPage.name')}</span>
            <input className="input" value={form.name} required maxLength={80} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </label>
          <label className="cs-field">
            <span>{t('connectorsPage.token')}</span>
            <input className="input" type="password" autoComplete="off" value={form.token} required={!form.id}
              placeholder={form.id ? t('connectorsPage.tokenSaved') : ''} onChange={(e) => setForm({ ...form, token: e.target.value })} />
            <span className="cs-muted">{t('connectorsPage.tokenHelp')}</span>
          </label>
          <label className="cs-field">
            <span>{t('connectorsPage.agent')}</span>
            <select className="input" aria-label={t('connectorsPage.agent')} value={form.agent_id} required onChange={(e) => setForm({ ...form, agent_id: e.target.value })}>
              <option value="">{t('connectorsPage.agentNone')}</option>
              {agents.map((a) => (
                <option key={a.id} value={a.id} disabled={!a.model}>{a.model ? a.name : t('connectorsPage.agentNoModel', { name: a.name })}</option>
              ))}
            </select>
          </label>
          <fieldset className="cs-field">
            <legend>{t('connectorsPage.who')}</legend>
            <label className="ca-radio">
              <input type="radio" name="who" checked={!form.accept_any} onChange={() => { setForm({ ...form, accept_any: false }); setConfirmed(false) }} />
              <span>{t('connectorsPage.onlyListed')}</span>
            </label>
            <label className="ca-radio">
              <input type="radio" name="who" checked={form.accept_any} onChange={() => setForm({ ...form, accept_any: true })} />
              <span>{t('connectorsPage.anyone')}</span>
            </label>
          </fieldset>
          {form.accept_any && (
            <div className="cs-error ca-warning" role="alert" data-testid="anyone-warning">
              <span>{t('connectorsPage.anyoneWarning')}</span>
              <label className="ca-radio">
                <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} />
                <span>{t('connectorsPage.anyoneConfirm')}</span>
              </label>
            </div>
          )}
          {!form.accept_any && (
            <NameList label={t('connectorsPage.allow')} hint={t('connectorsPage.allowHint')} value={form.allow}
              onChange={(v) => setForm({ ...form, allow: v })} testId="connector-allow" />
          )}
          <label className="cs-field">
            <span>{t('connectorsPage.notifyChat')}</span>
            <input className="input" inputMode="numeric" value={form.notify_chat} onChange={(e) => setForm({ ...form, notify_chat: e.target.value })} />
            <span className="cs-muted">{t('connectorsPage.notifyHelp')}</span>
          </label>
          <div className="cs-actions">
            <button type="submit" className="btn btn-primary btn-sm" disabled={busy || !canSave(form, confirmed)}>{t('connectorsPage.save')}</button>
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => setForm(null)}>{t('connectorsPage.cancel')}</button>
          </div>
        </form>
      )}

      <ul className="cs-list">
        {items.map((c) => (
          <li key={c.id} className="cs-row" data-testid="connector-row">
            <div className="cs-row__text">
              <span className="cs-row__name">{c.name}</span>
              <span className="cs-muted">
                {t('connectorsPage.agentOf', { name: agentName(c.agent_id) })}
                {' · '}
                {t('connectorsPage.accepts', { who: c.accept_any ? t('connectorsPage.modes.anyone') : (c.allow || []).length === 0 ? t('connectorsPage.modes.nobody') : t('connectorsPage.modes.listed', { count: c.allow.length }) })}
              </span>
              {openSenders === c.id && <Senders id={c.id} onAllowed={load} t={t} />}
            </div>
            <span className="cs-row__actions">
              <label className="ca-radio">
                <input type="checkbox" checked={c.enabled} aria-label={t('connectorsPage.onFor', { name: c.name })}
                  onChange={(e) => {
                    const on = e.target.checked
                    setItems((list) => list.map((x) => (x.id === c.id ? { ...x, enabled: on } : x)))
                    act(() => (on ? chatConnectorsApi.enable(c.id) : chatConnectorsApi.disable(c.id)))
                  }} />
                <span>{t('connectorsPage.on')}</span>
              </label>
              <button type="button" className="btn btn-secondary btn-sm"
                onClick={async () => { setError(''); try { const r = await chatConnectorsApi.test(c.id); setNote(t('connectorsPage.testOk', { bot: r.bot_username })) } catch (e) { fail(e) } }}>
                {t('connectorsPage.test')}
              </button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setOpenSenders(openSenders === c.id ? '' : c.id)}>{t('connectorsPage.senders')}</button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => open({ ...emptyConnector(), ...c, token: '' })}>{t('connectorsPage.editButton')}</button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setToDelete(c)}>{t('connectorsPage.delete')}</button>
            </span>
          </li>
        ))}
      </ul>
      {items.length === 0 && !form && <p className="cs-muted">{t('connectorsPage.empty')}</p>}
      </>
      )}
      <p className="cs-muted"><Link to="/app/chats">{t('skillsPage.back')}</Link></p>
      <ChatDeleteConfirm item={toDelete} kind="connector" onConfirm={confirmDelete} onCancel={() => setToDelete(null)} />
    </div>
  )
}
