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
import AnswerFields, { answerSummary, answerT } from './AnswerFields'
import AccessView from './AccessView'
import ApprovalFields from './ApprovalFields'
import FilesFields from './FilesFields'
import { Check, Field, Notice, RadioCards, Select, Switch, TextInput } from './controls'
import NameList from './NameList'
import PermissionFields, { NoCapsNotice } from './PermissionFields'
import { agentsApi, connectorsApi } from './api'
import { canSave, connectorForm, hasNoCaps, connectorPayload, emptyConnector, listEntries, replaceList, senderLine, usualCaps } from './connector'
import './connector.css'

function Senders({ id, onAllowed, t }) {
  const [list, setList] = useState(null)
  const [error, setError] = useState('')
  const load = useCallback(() => {
    connectorsApi.senders(id).then((r) => setList(r.senders || [])).catch((e) => setError(e?.message || ''))
  }, [id])
  useEffect(() => { load() }, [load])
  if (error) return <Notice tone="error" role="alert">{error}</Notice>
  if (list === null) return null
  if (list.length === 0) return <p className="cn-muted">{t('noSenders')}</p>
  return (
    <ul className="cn-runs" aria-label={t('senders')} data-testid="sender-log">
      {list.map((s) => (
        <li key={s.id}>
          <span className={`cn-status cn-status--${s.allowed ? 'done' : 'skipped'}`}>{s.allowed ? t('accepted') : t('ignored')}</span>
          <span>{senderLine(s)}</span>
          {!s.allowed && (
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm"
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
    <Field label={t('token')} hint={t('tokenHelp')}>
      {(p) => (
        <TextInput {...p} mono type="password" autoComplete="off" value={form.token || ''} required={!form.id}
          placeholder={form.id ? t('tokenSaved') : ''} onChange={(e) => setForm({ ...form, token: e.target.value })} />
      )}
    </Field>
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
 * @param {boolean} [props.messaging]  false for a kind that carries no messages: the page then hides the sender,
 *                                     reply, file and notification settings and the switch of the binding
 * @param {Function} [props.ExtraFields] more fields of the add-on's own ({ form, setForm, t })
 * @param {Function} [props.extraBody] the request fields they make: (form) => object
 * @param {Function} [props.extraForm] their values from a saved binding: (binding) => object
 * @param {Function} [props.extraValid] whether they allow saving: (form) => boolean
 * @param {Function} [props.testExtra] more checks after the Test button: async (binding, t) => string
 * @param {Function} [props.RowInfo]   a line under a binding in the list ({ c, t })
 */
export default function ConnectorPage({ kind, ns, testId, TokenFields = SingleToken, tokenOf = (f) => f.token || '', lists = ONE_LIST, Help, notifyMode = 'text',
  messaging = true, ExtraFields, extraBody, extraForm, extraValid, testExtra, RowInfo }) {
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
  const [kindInfo, setKindInfo] = useState(null)
  const [capabilities, setCapabilities] = useState([])
  const [accessVersion, setAccessVersion] = useState(0)
  const kindFiles = kindInfo?.files || null
  // A kind that declares agent_optional lets a binding hold credentials only.
  const agentOptional = Boolean(kindInfo?.agent_optional)
  const s = answerT(t)
  const pairedChanged = useCallback(() => setAccessVersion((v) => v + 1), [])

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
  // What the add-on of this kind offers: the choices of its options.
  useEffect(() => {
    connectorsApi.kinds().then((r) => {
      setKindInfo((r.kinds || []).find((k) => k.kind === kind) || null)
      setCapabilities(r.capabilities || [])
    }).catch(() => {})
  }, [kind])
  useEffect(() => { agentsApi.list().then((r) => setAgents(r.agents || [])).catch(() => {}) }, [])

  const agentName = (id) => agents.find((a) => a.id === id)?.name || id
  const open = (c) => { setForm(c); setConfirmed(Boolean(c.accept_any)); setNote('') }
  const extra = (form) => ({ ...(messaging ? {} : { enabled: false, accept_any: false }), ...(extraBody ? extraBody(form) : {}) })
  const check = async (c) => {
    const r = await connectorsApi.test(c.id)
    let text = t('testOk', { bot: r.bot_username })
    if (testExtra) text += ` ${await testExtra(c, t)}`
    return text
  }

  // The server refuses a token that the platform refuses. After a save with
  // a new token, the page names the bot the token belongs to.
  const save = async (e) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    setNote('')
    try {
      const token = tokenOf(form).trim()
      const body = connectorPayload(form, kind, token, extra(form))
      const saved = form.id ? await connectorsApi.update(form.id, body) : await connectorsApi.create(body)
      setForm(null)
      await load()
      if (token && saved?.id) {
        try {
          setNote(await check(saved))
        } catch (err) { setError(t('checkFailed', { reason: err?.message || '' })) }
      }
    } catch (err) { fail(err) } finally { setBusy(false) }
  }

  // The preset for a saved binding that has no permission on: the usual set,
  // saved at once, with every other setting as it was.
  const allowUsual = async (c) => {
    setBusy(true)
    setError('')
    setNote('')
    try {
      const f = { ...connectorForm(c), ...(extraForm ? extraForm(c) : {}) }
      f.caps = usualCaps(capabilities)
      await connectorsApi.update(c.id, connectorPayload(f, kind, '', extra(f)))
      setNote(s('perm.usualSaved', { name: c.name }))
      await load()
    } catch (e) { fail(e) } finally { setBusy(false) }
  }

  const act = async (fn) => {
    setError('')
    try { await fn(); await load() } catch (e) { fail(e) }
  }

  if (off) {
    return (
      <div className="cn-page" data-testid={testId}>
        <h1 className="cn-title">{t('title')}</h1>
        <Notice tone="info" role="note">{off}</Notice>
      </div>
    )
  }

  return (
    <div className="cn-page" data-testid={testId}>
      <header className="cn-head">
        <h1 className="cn-title">{t('title')}</h1>
        <button type="button" className="dk-btn dk-btn--primary" onClick={() => open({ ...emptyConnector(), ...(extraForm ? extraForm({}) : {}) })}>
          <Icon name="plus" /> {t('new')}
        </button>
      </header>
      <p className="cn-muted">{t('intro')}</p>
      {Help && <Help t={t} />}

      {error && (
        <Notice tone="error" role="alert">
          <span>{error}</span>
          <button type="button" className="cn-link" aria-label={t('dismiss', { defaultValue: 'Dismiss' })} onClick={() => setError('')}>×</button>
        </Notice>
      )}
      {note && <Notice tone="ok" role="status">{note}</Notice>}

      {form && (
        <div className="cn-formgrid">
          <form className="cn-form" onSubmit={save} aria-label={form.id ? t('edit') : t('new')}>
            <Field label={t('name')}>
              {(p) => <TextInput {...p} value={form.name} required maxLength={80} onChange={(e) => setForm({ ...form, name: e.target.value })} />}
            </Field>
            <TokenFields form={form} setForm={setForm} t={t} />
            <Field label={t('agent')} hint={agentOptional ? t('agentOptionalHint', { defaultValue: 'Optional. Without an agent this connection only holds the token, the repository list and the write mode for your chats. It answers no messages. Choose an agent to let it listen.' }) : undefined} optional={agentOptional}>
              {(p) => (
                <Select {...p} aria-label={t('agent')} value={form.agent_id} required={!agentOptional} onChange={(e) => setForm({ ...form, agent_id: e.target.value })}>
                  <option value="">{agentOptional ? t('agentCredentialsOnly', { defaultValue: 'No agent (credentials only)' }) : t('agentNone')}</option>
                  {agents.map((a) => (
                    <option key={a.id} value={a.id} disabled={!a.model}>{a.model ? a.name : t('agentNoModel', { name: a.name })}</option>
                  ))}
                </Select>
              )}
            </Field>
            {ExtraFields && <ExtraFields form={form} setForm={setForm} t={t} />}
            {messaging && (<>
            <fieldset className="cn-group">
              <legend>{t('who')}</legend>
              <RadioCards name="who" legend={t('who')} value={form.accept_any} onChange={(any) => { setForm({ ...form, accept_any: any }); if (!any) setConfirmed(false) }}
                options={[{ value: false, label: t('onlyListed') }, { value: true, label: t('anyone') }]} />
              {form.accept_any && (
                <Notice tone="warn" role="alert" testId="anyone-warning">
                  <p className="cn-notice__text">{t('anyoneWarning')}</p>
                  <Check checked={confirmed} onChange={setConfirmed}>{t('anyoneConfirm')}</Check>
                </Notice>
              )}
              {!form.accept_any && lists.map((l, i) => (
                <NameList key={l.key} label={t(l.label)} hint={t(l.hint)} value={listEntries(form.allow, lists, i)} addLabel={answerT(t)('add')}
                  onChange={(v) => setForm({ ...form, allow: replaceList(form.allow, lists, i, v) })} testId={l.testId} />
              ))}
            </fieldset>
            <Field label={t('notifyChat')} hint={t('notifyHelp')} optional>
              {(p) => <TextInput {...p} mono inputMode={notifyMode} value={form.notify_chat} onChange={(e) => setForm({ ...form, notify_chat: e.target.value })} />}
            </Field>
            <AnswerFields form={form} setForm={setForm} t={t} kindInfo={kindInfo} />
            <FilesFields form={form} setForm={setForm} kind={kindFiles} saved={form.files} s={answerT(t)} />
            <PermissionFields form={form} setForm={setForm} kind={kindInfo} capabilities={capabilities} s={s} onPaired={pairedChanged} />
            <ApprovalFields form={form} setForm={setForm} s={s} triggersOff={capabilities.length > 0 && !(form.caps || []).includes('triggers.propose')} />
            {capabilities.length > 0 && <AccessView binding={form} capabilities={capabilities} s={s} version={accessVersion} />}
            </>)}
            <div className="cn-actions">
              <button type="submit" className="dk-btn dk-btn--primary" aria-busy={busy ? 'true' : undefined} disabled={busy || !canSave(form, confirmed, Boolean(tokenOf(form).trim()), kindFiles, agentOptional) || (extraValid ? !extraValid(form) : false)}>{t('save')}</button>
              <button type="button" className="dk-btn dk-btn--secondary" onClick={() => setForm(null)}>{t('cancel')}</button>
            </div>
          </form>
          {messaging && <aside className="cn-aside" aria-label={answerT(t)('aside.title')}>
            <h2 className="cn-aside__title">{answerT(t)('aside.title')}</h2>
            <ol className="cn-aside__steps">
              <li>{answerT(t)('aside.step1')}</li>
              <li>{answerT(t)('aside.step2')}</li>
              <li>{answerT(t)('aside.step3')}</li>
            </ol>
            <h2 className="cn-aside__title">{answerT(t)('aside.testTitle')}</h2>
            <p className="cn-muted">{answerT(t)('aside.testText')}</p>
          </aside>}
        </div>
      )}

      <ul className="cn-list">
        {items.map((c) => (
          <li key={c.id} className="cn-row" data-testid="connector-row">
            <div className="cn-row__text">
              <span className="cn-row__name">{c.name}</span>
              {messaging ? (<>
              <span className="cn-muted">
                {c.credentials_only ? t('credentialsOnly', { defaultValue: 'Credentials only' }) : t('agentOf', { name: agentName(c.agent_id) })}
                {' · '}
                {t('accepts', { who: c.accept_any ? t('modes.anyone') : (c.allow || []).length === 0 ? t('modes.nobody') : t('modes.listed', { count: c.allow.length }) })}
              </span>
              <span className="cn-muted" data-testid="connector-answer-summary">{answerSummary(c, t)}</span>
              </>) : <span className="cn-muted" data-testid={c.credentials_only ? 'connector-credentials-only' : undefined}>
                {c.credentials_only ? t('credentialsOnly', { defaultValue: 'Credentials only' }) : t('agentOf', { name: agentName(c.agent_id) })}
              </span>}
              {RowInfo && <RowInfo c={c} t={t} />}
              {messaging && capabilities.length > 0 && hasNoCaps(c) && (
                <NoCapsNotice capabilities={capabilities} s={s} busy={busy} name={c.name} onUsual={() => allowUsual(c)} />
              )}
              {c.active === false && <span className="cn-muted" data-testid="connector-inactive">{t('inactive', { reason: c.reason })}</span>}
              {openSenders === c.id && <Senders id={c.id} onAllowed={load} t={t} />}
            </div>
            <span className="cn-row__actions">
              {messaging && <Switch checked={c.enabled} label={t('onFor', { name: c.name })}
                onChange={(on) => {
                  setItems((list) => list.map((x) => (x.id === c.id ? { ...x, enabled: on } : x)))
                  act(() => (on ? connectorsApi.enable(c.id) : connectorsApi.disable(c.id)))
                }} />}
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm"
                onClick={async () => { setError(''); try { setNote(await check(c)) } catch (e) { fail(e) } }}>
                {t('test')}
              </button>
              {messaging && <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => setOpenSenders(openSenders === c.id ? '' : c.id)}>{t('senders')}</button>}
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => open({ ...connectorForm(c), ...(extraForm ? extraForm(c) : {}) })}>{t('editButton')}</button>
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => setToDelete(c)}>{t('delete')}</button>
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
