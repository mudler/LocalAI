/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import ConfirmDialog from '../../../../internal/host-compat/src/components/ConfirmDialog'
import Icon from '../../../../internal/host-compat/src/components/Icon'
import NameList from '../components/NameList'
import { agentsApi, connectorsApi } from '../api'
import { canSave, connectorPayload, emptyConnector, modeText, senderLine } from '../utils/connector'

function Senders({ id, onAllowed, t }) {
  const [list, setList] = useState(null)
  const [error, setError] = useState('')
  const load = useCallback(() => {
    connectorsApi.senders(id).then((r) => setList(r.senders || [])).catch((e) => setError(e?.message || ''))
  }, [id])
  useEffect(() => { load() }, [load])
  if (error) return <p className="tg-error" role="alert">{error}</p>
  if (list === null) return null
  if (list.length === 0) return <p className="tg-muted">{t('noSenders')}</p>
  return (
    <ul className="tg-runs" aria-label={t('senders')} data-testid="sender-log">
      {list.map((s) => (
        <li key={s.id}>
          <span className={`tg-status tg-status--${s.allowed ? 'done' : 'skipped'}`}>{s.allowed ? t('accepted') : t('ignored')}</span>
          <span>{senderLine(s)}</span>
          {!s.allowed && (
            <button type="button" className="btn btn-secondary btn-sm"
              onClick={async () => {
                setError('')
                try { await connectorsApi.allow(id, s.id); load(); onAllowed() } catch (e) { setError(e?.message || '') }
              }}>{t('allowButton')}</button>
          )}
        </li>
      ))}
    </ul>
  )
}

// Bindings of Telegram bots to saved agents. The bindings, the sealed token and
// the sender log live in core; this page edits them through the core API.
export default function Telegram() {
  const { t } = useTranslation('telegram')
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
    setError(e?.message || '')
  }, [])

  const load = useCallback(async () => {
    try {
      const res = await connectorsApi.list()
      setItems((res.connectors || []).filter((c) => c.kind === 'telegram'))
      setOff('')
    } catch (e) { fail(e) }
  }, [fail])

  useEffect(() => { load() }, [load])
  useEffect(() => { agentsApi.list().then((r) => setAgents(r.agents || [])).catch(() => {}) }, [])

  const agentName = (id) => agents.find((a) => a.id === id)?.name || id
  const open = (c) => { setForm(c); setConfirmed(Boolean(c.accept_any)); setNote('') }

  const save = async (e) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const body = connectorPayload(form)
      if (form.id) await connectorsApi.update(form.id, body)
      else await connectorsApi.create(body)
      setForm(null)
      await load()
    } catch (err) { fail(err) } finally { setBusy(false) }
  }

  const act = async (fn) => {
    setError('')
    try { await fn(); await load() } catch (e) { fail(e) }
  }

  if (off) {
    return (
      <div className="tg-page" data-testid="telegram-page">
        <h1 className="page-title tg-title">{t('title')}</h1>
        <div className="tg-notice" role="note">{off}</div>
      </div>
    )
  }

  return (
    <div className="tg-page" data-testid="telegram-page">
      <header className="tg-head">
        <h1 className="page-title tg-title">{t('title')}</h1>
        <button type="button" className="btn btn-primary btn-sm" onClick={() => open(emptyConnector())}>
          <Icon name="plus" /> {t('new')}
        </button>
      </header>
      <p className="tg-muted">{t('intro')}</p>

      {error && (
        <div className="tg-error" role="alert">
          <span>{error}</span>
          <button type="button" className="tg-link" onClick={() => setError('')}>×</button>
        </div>
      )}
      {note && <p className="tg-muted" role="status">{note}</p>}

      {form && (
        <form className="tg-form" onSubmit={save} aria-label={form.id ? t('edit') : t('new')}>
          <label className="tg-field">
            <span>{t('name')}</span>
            <input className="input" value={form.name} required maxLength={80} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </label>
          <label className="tg-field">
            <span>{t('token')}</span>
            <input className="input" type="password" autoComplete="off" value={form.token} required={!form.id}
              placeholder={form.id ? t('tokenSaved') : ''} onChange={(e) => setForm({ ...form, token: e.target.value })} />
            <span className="tg-muted">{t('tokenHelp')}</span>
          </label>
          <label className="tg-field">
            <span>{t('agent')}</span>
            <select className="input" aria-label={t('agent')} value={form.agent_id} required onChange={(e) => setForm({ ...form, agent_id: e.target.value })}>
              <option value="">{t('agentNone')}</option>
              {agents.map((a) => (
                <option key={a.id} value={a.id} disabled={!a.model}>{a.model ? a.name : t('agentNoModel', { name: a.name })}</option>
              ))}
            </select>
          </label>
          <fieldset className="tg-field">
            <legend>{t('who')}</legend>
            <label className="tg-radio">
              <input type="radio" name="who" checked={!form.accept_any} onChange={() => { setForm({ ...form, accept_any: false }); setConfirmed(false) }} />
              <span>{t('onlyListed')}</span>
            </label>
            <label className="tg-radio">
              <input type="radio" name="who" checked={form.accept_any} onChange={() => setForm({ ...form, accept_any: true })} />
              <span>{t('anyone')}</span>
            </label>
          </fieldset>
          {form.accept_any && (
            <div className="tg-error tg-warning" role="alert" data-testid="anyone-warning">
              <span>{t('anyoneWarning')}</span>
              <label className="tg-radio">
                <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} />
                <span>{t('anyoneConfirm')}</span>
              </label>
            </div>
          )}
          {!form.accept_any && (
            <NameList label={t('allow')} hint={t('allowHint')} value={form.allow}
              onChange={(v) => setForm({ ...form, allow: v })} testId="connector-allow" />
          )}
          <label className="tg-field">
            <span>{t('notifyChat')}</span>
            <input className="input" inputMode="numeric" value={form.notify_chat} onChange={(e) => setForm({ ...form, notify_chat: e.target.value })} />
            <span className="tg-muted">{t('notifyHelp')}</span>
          </label>
          <div className="tg-actions">
            <button type="submit" className="btn btn-primary btn-sm" disabled={busy || !canSave(form, confirmed)}>{t('save')}</button>
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => setForm(null)}>{t('cancel')}</button>
          </div>
        </form>
      )}

      <ul className="tg-list">
        {items.map((c) => (
          <li key={c.id} className="tg-row" data-testid="connector-row">
            <div className="tg-row__text">
              <span className="tg-row__name">{c.name}</span>
              <span className="tg-muted">
                {t('agentOf', { name: agentName(c.agent_id) })}
                {' · '}
                {t('accepts', { who: c.accept_any ? t('modes.anyone') : (c.allow || []).length === 0 ? t('modes.nobody') : t('modes.listed', { count: c.allow.length }) })}
              </span>
              {c.active === false && <span className="tg-muted" data-testid="connector-inactive">{t('inactive', { reason: c.reason })}</span>}
              {openSenders === c.id && <Senders id={c.id} onAllowed={load} t={t} />}
            </div>
            <span className="tg-row__actions">
              <label className="tg-radio">
                <input type="checkbox" checked={c.enabled} aria-label={t('onFor', { name: c.name })}
                  onChange={(e) => {
                    const on = e.target.checked
                    setItems((list) => list.map((x) => (x.id === c.id ? { ...x, enabled: on } : x)))
                    act(() => (on ? connectorsApi.enable(c.id) : connectorsApi.disable(c.id)))
                  }} />
                <span>{t('on')}</span>
              </label>
              <button type="button" className="btn btn-secondary btn-sm"
                onClick={async () => { setError(''); try { const r = await connectorsApi.test(c.id); setNote(t('testOk', { bot: r.bot_username })) } catch (e) { fail(e) } }}>
                {t('test')}
              </button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setOpenSenders(openSenders === c.id ? '' : c.id)}>{t('senders')}</button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => open({ ...emptyConnector(), ...c, token: '' })}>{t('editButton')}</button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setToDelete(c)}>{t('delete')}</button>
            </span>
          </li>
        ))}
      </ul>
      {items.length === 0 && !form && <p className="tg-muted">{t('empty')}</p>}
      <ConfirmDialog
        open={Boolean(toDelete)}
        title={t('confirmDelete.title', { name: toDelete?.name || '' })}
        message={t('confirmDelete.message')}
        confirmLabel={t('confirmDelete.confirm')}
        danger
        onConfirm={() => { const c = toDelete; setToDelete(null); act(() => connectorsApi.remove(c.id)) }}
        onCancel={() => setToDelete(null)}
      />
    </div>
  )
}
