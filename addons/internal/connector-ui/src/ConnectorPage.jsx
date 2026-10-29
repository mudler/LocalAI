/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The page of a connector add-on (Telegram, Slack): the bindings of one kind
// of bot to the saved agents. The bindings, the sealed token and the sender
// log live in core; this page edits them through the core API. An add-on
// gives its own words (an i18n namespace), the fields of its token, and its
// allowlists; the rest is the same for every platform.
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import ConfirmDialog from '../../host-compat/src/components/ConfirmDialog'
import Icon from '../../host-compat/src/components/Icon'
import AnswerFields, { answerSummary } from './AnswerFields'
import NameList from './NameList'
import { agentsApi, connectorsApi } from './api'
import { canSave, connectorPayload, emptyConnector, listEntries, replaceList, senderLine } from './connector'
import './connector.css'

function Senders({ id, onAllowed, t }) {
  const [list, setList] = useState(null)
  const [error, setError] = useState('')
  const load = useCallback(() => {
    connectorsApi.senders(id).then((r) => setList(r.senders || [])).catch((e) => setError(e?.message || ''))
  }, [id])
  useEffect(() => { load() }, [load])
  if (error) return <p className="cn-error" role="alert">{error}</p>
  if (list === null) return null
  if (list.length === 0) return <p className="cn-muted">{t('noSenders')}</p>
  return (
    <ul className="cn-runs" aria-label={t('senders')} data-testid="sender-log">
      {list.map((s) => (
        <li key={s.id}>
          <span className={`cn-status cn-status--${s.allowed ? 'done' : 'skipped'}`}>{s.allowed ? t('accepted') : t('ignored')}</span>
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

// The default fields of a token: one password field.
function SingleToken({ form, setForm, t }) {
  return (
    <label className="cn-field">
      <span>{t('token')}</span>
      <input className="input" type="password" autoComplete="off" value={form.token || ''} required={!form.id}
        placeholder={form.id ? t('tokenSaved') : ''} onChange={(e) => setForm({ ...form, token: e.target.value })} />
      <span className="cn-muted">{t('tokenHelp')}</span>
    </label>
  )
}

const ONE_LIST = [{ key: 'allow', label: 'allow', hint: 'allowHint', testId: 'connector-allow' }]

/**
 * @param {object} props
 * @param {string} props.kind          the kind of the bindings, such as telegram
 * @param {string} props.ns            the i18n namespace of the add-on
 * @param {string} props.testId        the test id of the page
 * @param {Function} [props.TokenFields] the fields of the token ({ form, setForm, t })
 * @param {Function} [props.tokenOf]   the secret that the fields make, '' when not typed
 * @param {Array} [props.lists]        the allowlists: { key, label, hint, testId, match }
 * @param {Function} [props.Help]      a block under the intro ({ t })
 * @param {string} [props.notifyMode]  the inputMode of the notification field
 */
export default function ConnectorPage({ kind, ns, testId, TokenFields = SingleToken, tokenOf = (f) => f.token || '', lists = ONE_LIST, Help, notifyMode = 'text' }) {
  const { t } = useTranslation(ns)
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
      setItems((res.connectors || []).filter((c) => c.kind === kind))
      setOff('')
    } catch (e) { fail(e) }
  }, [fail, kind])

  useEffect(() => { load() }, [load])
  useEffect(() => { agentsApi.list().then((r) => setAgents(r.agents || [])).catch(() => {}) }, [])

  const agentName = (id) => agents.find((a) => a.id === id)?.name || id
  const open = (c) => { setForm(c); setConfirmed(Boolean(c.accept_any)); setNote('') }

  // The server refuses a token that the platform refuses. After a save with
  // a new token, the page names the bot the token belongs to.
  const save = async (e) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    setNote('')
    try {
      const token = tokenOf(form).trim()
      const body = connectorPayload(form, kind, token)
      const saved = form.id ? await connectorsApi.update(form.id, body) : await connectorsApi.create(body)
      setForm(null)
      await load()
      if (token && saved?.id) {
        try {
          const r = await connectorsApi.test(saved.id)
          setNote(t('testOk', { bot: r.bot_username }))
        } catch (err) { setError(t('checkFailed', { reason: err?.message || '' })) }
      }
    } catch (err) { fail(err) } finally { setBusy(false) }
  }

  const act = async (fn) => {
    setError('')
    try { await fn(); await load() } catch (e) { fail(e) }
  }

  if (off) {
    return (
      <div className="cn-page" data-testid={testId}>
        <h1 className="page-title cn-title">{t('title')}</h1>
        <div className="cn-notice" role="note">{off}</div>
      </div>
    )
  }

  return (
    <div className="cn-page" data-testid={testId}>
      <header className="cn-head">
        <h1 className="page-title cn-title">{t('title')}</h1>
        <button type="button" className="btn btn-primary btn-sm" onClick={() => open(emptyConnector())}>
          <Icon name="plus" /> {t('new')}
        </button>
      </header>
      <p className="cn-muted">{t('intro')}</p>
      {Help && <Help t={t} />}

      {error && (
        <div className="cn-error" role="alert">
          <span>{error}</span>
          <button type="button" className="cn-link" onClick={() => setError('')}>×</button>
        </div>
      )}
      {note && <p className="cn-muted" role="status">{note}</p>}

      {form && (
        <form className="cn-form" onSubmit={save} aria-label={form.id ? t('edit') : t('new')}>
          <label className="cn-field">
            <span>{t('name')}</span>
            <input className="input" value={form.name} required maxLength={80} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </label>
          <TokenFields form={form} setForm={setForm} t={t} />
          <label className="cn-field">
            <span>{t('agent')}</span>
            <select className="input" aria-label={t('agent')} value={form.agent_id} required onChange={(e) => setForm({ ...form, agent_id: e.target.value })}>
              <option value="">{t('agentNone')}</option>
              {agents.map((a) => (
                <option key={a.id} value={a.id} disabled={!a.model}>{a.model ? a.name : t('agentNoModel', { name: a.name })}</option>
              ))}
            </select>
          </label>
          <fieldset className="cn-field">
            <legend>{t('who')}</legend>
            <label className="cn-radio">
              <input type="radio" name="who" checked={!form.accept_any} onChange={() => { setForm({ ...form, accept_any: false }); setConfirmed(false) }} />
              <span>{t('onlyListed')}</span>
            </label>
            <label className="cn-radio">
              <input type="radio" name="who" checked={form.accept_any} onChange={() => setForm({ ...form, accept_any: true })} />
              <span>{t('anyone')}</span>
            </label>
          </fieldset>
          {form.accept_any && (
            <div className="cn-error cn-warning" role="alert" data-testid="anyone-warning">
              <span>{t('anyoneWarning')}</span>
              <label className="cn-radio">
                <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} />
                <span>{t('anyoneConfirm')}</span>
              </label>
            </div>
          )}
          {!form.accept_any && lists.map((l, i) => (
            <NameList key={l.key} label={t(l.label)} hint={t(l.hint)} value={listEntries(form.allow, lists, i)}
              onChange={(v) => setForm({ ...form, allow: replaceList(form.allow, lists, i, v) })} testId={l.testId} />
          ))}
          <label className="cn-field">
            <span>{t('notifyChat')}</span>
            <input className="input" inputMode={notifyMode} value={form.notify_chat} onChange={(e) => setForm({ ...form, notify_chat: e.target.value })} />
            <span className="cn-muted">{t('notifyHelp')}</span>
          </label>
          <AnswerFields form={form} setForm={setForm} t={t} />
          <div className="cn-actions">
            <button type="submit" className="btn btn-primary btn-sm" disabled={busy || !canSave(form, confirmed, Boolean(tokenOf(form).trim()))}>{t('save')}</button>
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => setForm(null)}>{t('cancel')}</button>
          </div>
        </form>
      )}

      <ul className="cn-list">
        {items.map((c) => (
          <li key={c.id} className="cn-row" data-testid="connector-row">
            <div className="cn-row__text">
              <span className="cn-row__name">{c.name}</span>
              <span className="cn-muted">
                {t('agentOf', { name: agentName(c.agent_id) })}
                {' · '}
                {t('accepts', { who: c.accept_any ? t('modes.anyone') : (c.allow || []).length === 0 ? t('modes.nobody') : t('modes.listed', { count: c.allow.length }) })}
              </span>
              <span className="cn-muted" data-testid="connector-answer-summary">{answerSummary(c, t)}</span>
              {c.active === false && <span className="cn-muted" data-testid="connector-inactive">{t('inactive', { reason: c.reason })}</span>}
              {openSenders === c.id && <Senders id={c.id} onAllowed={load} t={t} />}
            </div>
            <span className="cn-row__actions">
              <label className="cn-radio">
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
      {items.length === 0 && !form && <p className="cn-muted">{t('empty')}</p>}
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
