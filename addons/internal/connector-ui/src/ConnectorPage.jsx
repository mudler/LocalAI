/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The page of a connector add-on (Telegram, Slack): the bindings of one kind
// of bot to the saved agents. The bindings, the sealed token and the sender
// log live in core; this page edits them through the core API. An add-on
// gives its own words (an i18n namespace), the fields of its token, and its
// allowlists; the rest is the same for every platform.
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import ConfirmDialog from '../../host-compat/src/components/ConfirmDialog'
import Icon from '../../host-compat/src/components/Icon'
import AnswerFields, { answerSummary, answerT } from './AnswerFields'
import AccessView from './AccessView'
import CapabilitiesPanel, { useAccessPreview, useIsAdmin } from './CapabilitiesPanel'
import ReadinessList from './ReadinessList'
import { useAuth } from '../../host-compat/src/context/AuthContext'
import ApprovalOptions from './ApprovalFields'
import FilesFields from './FilesFields'
import { Check, Field, HintLabels, Notice, RadioCards, Select, Switch, TextInput } from './controls'
import NameList from './NameList'
import ChannelTrust from './ChannelTrust'
import PermissionFields, { NoCapsNotice } from './PermissionFields'
import { agentsApi, connectorsApi, setApiLanguage } from './api'
import { DUPLICATE_CONFIRMATION, canSave, channelOnly, duplicateOther, duplicateRefusalName, isDuplicateRefusal, connectorForm, hasNoCaps, connectorPayload, emptyConnector, listEntries, replaceList, senderLine, trustAfter, usualCaps } from './connector'
import { registerConnectorLocales } from './i18n'
import './connector.css'

// The shared words, in every language of the web UI, under the namespace "connector".
registerConnectorLocales()

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
          <span>{senderLine(s, answerT(t))}</span>
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

// Two enabled connections that are the same bot: both answer every message.
// The warning names the other one, offers to switch this one off and opens the
// other. The host decides what is a duplicate (c.duplicate); this only shows it.
function SameBotNotice({ c, s, busy, onOff, onOpen }) {
  const other = duplicateOther(c)
  if (!other) return null
  return (
    <Notice tone="warn" role="status" testId="duplicate-warning">
      <p className="cn-notice__text">{s('dup.text', { name: other.name })}</p>
      <span className="cn-row__actions">
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="duplicate-off" disabled={busy} onClick={onOff}>{s('dup.turnOff')}</button>
        <button type="button" className="cn-link cn-link--text" data-testid="duplicate-open" onClick={() => onOpen(other.id)}>{s('dup.openOther', { name: other.name })}</button>
      </span>
    </Notice>
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
 * @param {Function} [props.isChannelEntry] whether an allowlist entry names a channel or group: (entry) => boolean
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
  messaging = true, isChannelEntry: channelPredicate, ExtraFields, extraBody, extraForm, extraValid, testExtra, RowInfo }) {
  const { t, i18n } = useTranslation([ns, 'connector'])
  const uiLanguage = i18n?.resolvedLanguage || i18n?.language || ''
  setApiLanguage(uiLanguage)
  const [items, setItems] = useState([])
  const [agents, setAgents] = useState([])
  const [off, setOff] = useState('')
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  const [form, setForm] = useState(null)
  const [confirmed, setConfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [toDelete, setToDelete] = useState(null)
  // The question after a save that would make this connection the same bot as another: { other }.
  const [dupAsk, setDupAsk] = useState(null)
  const [openSenders, setOpenSenders] = useState('')
  const [kindInfo, setKindInfo] = useState(null)
  const [capabilities, setCapabilities] = useState([])
  const [accessVersion, setAccessVersion] = useState(0)
  // The last token test of each saved binding in this session: { ok, bot, reason }.
  const [tokenChecks, setTokenChecks] = useState({})
  // The pairings of the open binding, as pairing here changes them.
  const [pairedList, setPairedList] = useState(null)
  const kindFiles = kindInfo?.files || null
  // A kind that declares agent_optional lets a binding hold credentials only.
  const agentOptional = Boolean(kindInfo?.agent_optional)
  const s = answerT(t)
  // Which entries of the allowlist name a channel or group, where people are trusted by the setting of the channel. Without a rule of the kind, the last of several lists holds the channels.
  const isChannelEntry = channelPredicate || ((x) => lists.length > 1 && listEntries([x], lists, lists.length - 1).length === 1)
  const pairedChanged = useCallback((list) => { if (Array.isArray(list)) setPairedList(list); setAccessVersion((v) => v + 1) }, [])
  const isAdmin = useIsAdmin(useAuth)

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
  }, [kind, uiLanguage])
  // The permission texts come from the host in the language of the page: ask again when it changes.
  const languageSeen = useRef(uiLanguage)
  useEffect(() => {
    if (languageSeen.current === uiLanguage) return
    languageSeen.current = uiLanguage
    setAccessVersion((v) => v + 1)
  }, [uiLanguage])
  useEffect(() => { agentsApi.list().then((r) => setAgents(r.agents || [])).catch(() => {}) }, [])

  // A link with ?binding=<id> (from the Tools list of an agent) opens that
  // binding and brings its permissions into view, once.
  const linked = useRef(false)
  useEffect(() => {
    if (linked.current || items.length === 0) return
    const id = new URLSearchParams(window.location.search).get('binding')
    const c = id && items.find((x) => x.id === id)
    if (!c) return
    linked.current = true
    open({ ...connectorForm(c), ...(extraForm ? extraForm(c) : {}) })
    setTimeout(() => document.querySelector('[data-testid="connector-permissions"]')?.scrollIntoView?.({ block: 'start' }), 50)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items])

  const agentName = (id) => agents.find((a) => a.id === id)?.name || id
  const open = (c) => { setForm(c); setConfirmed(Boolean(c.accept_any)); setNote(''); setPairedList(null); setDupAsk(null) }
  // The question sits by the Save button, at the end of a long form: bring it into view.
  useEffect(() => {
    if (dupAsk) document.querySelector('[data-testid="duplicate-confirm"]')?.scrollIntoView?.({ block: 'nearest' })
  }, [dupAsk])
  const openById = (id) => {
    const c = items.find((x) => x.id === id)
    if (c) open({ ...connectorForm(c), ...(extraForm ? extraForm(c) : {}) })
  }
  const extra = (form) => ({ ...(messaging ? {} : { enabled: false, accept_any: false }), ...(extraBody ? extraBody(form) : {}) })
  const check = async (c) => {
    try {
      const r = await connectorsApi.test(c.id)
      let text = t('testOk', { bot: r.bot_username })
      if (testExtra) text += ` ${await testExtra(c, t)}`
      setTokenChecks((m) => ({ ...m, [c.id]: { ok: true, bot: text } }))
      return text
    } catch (e) {
      setTokenChecks((m) => ({ ...m, [c.id]: { ok: false, reason: e?.message || '' } }))
      throw e
    }
  }

  // The server refuses a token that the platform refuses. After a save with
  // a new token, the page names the bot the token belongs to.
  const save = (e) => { e.preventDefault(); return doSave(false) }
  // confirmDuplicate answers the question of a save that makes the connection a twin of another.
  const doSave = async (confirmDuplicate) => {
    setBusy(true)
    setError('')
    setNote('')
    setDupAsk(null)
    try {
      const token = tokenOf(form).trim()
      const body = connectorPayload(form, kind, token, { ...extra(form), ...(confirmDuplicate ? { confirm_duplicate: DUPLICATE_CONFIRMATION } : {}) })
      const saved = form.id ? await connectorsApi.update(form.id, body) : await connectorsApi.create(body)
      setForm(null)
      await load()
      if (token && saved?.id) {
        try {
          setNote(await check(saved))
        } catch (err) { setError(t('checkFailed', { reason: err?.message || '' })) }
      }
    } catch (err) {
      if (isDuplicateRefusal(err)) setDupAsk({ other: duplicateRefusalName(err) })
      else fail(err)
    } finally { setBusy(false) }
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

  // What the form will do, asked of the host for the form as it is now.
  const preview = useAccessPreview({ form, kind, enabled: Boolean(form) && messaging && capabilities.length > 0, version: accessVersion })
  const savedItem = form?.id ? items.find((x) => x.id === form.id) : null
  const savedForm = savedItem ? { ...connectorForm(savedItem), ...(extraForm ? extraForm(savedItem) : {}) } : null
  const tokenTyped = form ? Boolean(tokenOf(form).trim()) : false
  const dirty = form ? !savedForm || tokenTyped || JSON.stringify(connectorPayload(form, kind, '', extra(form))) !== JSON.stringify(connectorPayload(savedForm, kind, '', extra(savedForm))) : false
  const people = form ? (lists.length > 1 ? listEntries(form.allow, lists, 0).length : (form.allow || []).length) : 0
  const fixCtx = { agentId: form?.agent_id || '', isAdmin, lists, form: form || {}, people }
  const readyInput = form && { form, saved: savedForm, kindInfo, capabilities, agents, lists, tokenTyped, token: form.id ? tokenChecks[form.id] : null, preview, paired: pairedList || form.paired || [], agentOptional, messaging, isChannel: isChannelEntry }

  if (off) {
    return (
      <div className="cn-page" data-testid={testId}>
        <h1 className="cn-title">{t('title')}</h1>
        <Notice tone="info" role="note">{off}</Notice>
      </div>
    )
  }

  return (
    <HintLabels.Provider value={{ more: s('more'), less: s('less'), optional: s('optional') }}>
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
          <button type="button" className="cn-link" aria-label={answerT(t)('dismiss')} onClick={() => setError('')}>×</button>
        </Notice>
      )}
      {note && <Notice tone="ok" role="status">{note}</Notice>}

      {form && (
        <div className="cn-formgrid">
          <form className="cn-form" onSubmit={save} aria-label={form.id ? t('edit') : t('new')} data-testid="connector-form">
            {messaging && form.id && (
              <SameBotNotice c={savedItem} s={s} busy={busy} onOff={() => { setForm(null); act(() => connectorsApi.disable(form.id)) }} onOpen={openById} />
            )}
            {messaging && capabilities.length > 0 && (
              <ReadinessList s={s} ctx={fixCtx} rowsInput={readyInput} />
            )}
            <Field label={t('name')}>
              {(p) => <TextInput {...p} value={form.name} required maxLength={80} onChange={(e) => setForm({ ...form, name: e.target.value })} />}
            </Field>
            <TokenFields form={form} setForm={setForm} t={t} />
            <Field label={t('agent')} hint={agentOptional ? answerT(t)('agentOptionalHint') : undefined} optional={agentOptional}>
              {(p) => (
                <Select {...p} data-testid="connector-agent" aria-label={t('agent')} value={form.agent_id} required={!agentOptional} onChange={(e) => setForm({ ...form, agent_id: e.target.value })}>
                  <option value="">{agentOptional ? answerT(t)('agentCredentialsOnly') : t('agentNone')}</option>
                  {agents.map((a) => (
                    <option key={a.id} value={a.id} disabled={!a.model}>{a.model ? a.name : t('agentNoModel', { name: a.name })}</option>
                  ))}
                </Select>
              )}
            </Field>
            {ExtraFields && <ExtraFields form={form} setForm={setForm} t={t} />}
            {messaging && (<>
            <fieldset className="cn-group" data-testid="connector-who" tabIndex={-1}>
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
                <NameList key={l.key} label={t(l.label)} hint={t(l.hint)} help={l.help ? t(l.help) : undefined} value={listEntries(form.allow, lists, i)} addLabel={answerT(t)('add')}
                  onChange={(v) => { const allow = replaceList(form.allow, lists, i, v); setForm({ ...form, allow, allow_trust: trustAfter(form, allow, isChannelEntry) }) }} testId={l.testId} />
              ))}
              <ChannelTrust form={form} setForm={setForm} t={s} isChannel={isChannelEntry} />
            </fieldset>
            <Field label={t('notifyChat')} hint={t('notifyHelp')} optional>
              {(p) => <TextInput {...p} mono inputMode={notifyMode} data-testid="connector-notify" value={form.notify_chat} onChange={(e) => setForm({ ...form, notify_chat: e.target.value })} />}
            </Field>
            <AnswerFields form={form} setForm={setForm} t={t} kindInfo={kindInfo} />
            <FilesFields form={form} setForm={setForm} kind={kindFiles} saved={form.files} s={answerT(t)} />
            <PermissionFields form={form} setForm={setForm} kind={kindInfo} capabilities={capabilities} s={s} onPaired={pairedChanged} agent={agents.find((a) => a.id === form.agent_id)} channelsOnly={channelOnly(form, lists)} />
            {/* The approval of proposed triggers sits under the permission Propose triggers; an older host with no capability list has no such row, so it shows here. */}
            {capabilities.length === 0 && <ApprovalOptions form={form} setForm={setForm} s={s} />}
            {capabilities.length > 0 && (<>
              <CapabilitiesPanel form={form} kindInfo={kindInfo} capabilities={capabilities} lists={lists} preview={preview} dirty={dirty} s={s} ctx={fixCtx} />
              <AccessView binding={form} capabilities={capabilities} s={s} version={accessVersion} onAllowed={(sender) => { setForm((f) => (f && !(f.allow || []).includes(sender) ? { ...f, allow: [...(f.allow || []), sender] } : f)); load() }} />
            </>)}
            </>)}
            {dupAsk && (
              <Notice tone="warn" role="alert" testId="duplicate-confirm">
                <p className="cn-notice__text">{s('dup.text', { name: dupAsk.other })}</p>
                <p className="cn-notice__text">{s('dup.confirmHelp')}</p>
                <span className="cn-row__actions">
                  <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" data-testid="duplicate-save-anyway" disabled={busy} onClick={() => doSave(true)}>{s('dup.saveAnyway')}</button>
                  <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => setDupAsk(null)}>{s('dup.goBack')}</button>
                </span>
              </Notice>
            )}
            <div className="cn-actions">
              <button type="submit" className="dk-btn dk-btn--primary" data-testid="connector-save" aria-busy={busy ? 'true' : undefined} disabled={busy || !canSave(form, confirmed, Boolean(tokenOf(form).trim()), kindFiles, agentOptional) || (extraValid ? !extraValid(form) : false)}>{t('save')}</button>
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
                {c.credentials_only ? answerT(t)('credentialsOnly') : t('agentOf', { name: agentName(c.agent_id) })}
                {' · '}
                {t('accepts', { who: c.accept_any ? t('modes.anyone') : (c.allow || []).length === 0 ? t('modes.nobody') : t('modes.listed', { count: c.allow.length }) })}
              </span>
              <span className="cn-muted" data-testid="connector-answer-summary">{answerSummary(c, t)}</span>
              </>) : <span className="cn-muted" data-testid={c.credentials_only ? 'connector-credentials-only' : undefined}>
                {c.credentials_only ? answerT(t)('credentialsOnly') : t('agentOf', { name: agentName(c.agent_id) })}
              </span>}
              {RowInfo && <RowInfo c={c} t={t} />}
              {messaging && form?.id !== c.id && <SameBotNotice c={c} s={s} busy={busy} onOff={() => act(() => connectorsApi.disable(c.id))} onOpen={openById} />}
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
    </HintLabels.Provider>
  )
}
