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
import { answerSummary, answerT } from './AnswerFields'
import { useAccessPreview, useIsAdmin } from './accessHooks'
import { useAuth } from '../../host-compat/src/context/AuthContext'
import { Field, HintLabels, Notice, TextInput } from './controls'
import { PaneLayout } from '../../host-compat/src/components/ui/SidePane'
import ConnectionRow, { Tile } from './ConnectionRow'
import ConnectionPane from './ConnectionPane'
import ConnectionSheet from './ConnectionSheet'
import { applyPreset, checkRecord, connectionState, dropCheck, fixOf, presetOf, readChecks, writeChecks } from './connection'
import { agentsApi, connectorsApi, setApiLanguage, triggersApi } from './api'
import { sharedOfKind, shareAvailable } from './share'
import { getSdk } from '../../host-compat/src/runtime.js'
import { changedParts, droppedActs, removedActs } from './access'
import { basePath } from '../../host-compat/src/utils/basePath.js'
import { DUPLICATE_CONFIRMATION, canSave, duplicateOther, duplicateRefusalName, isDuplicateRefusal, connectorForm, connectorPayload, emptyConnector, agentHeld, listEntries, senderLine } from './connector'
import { loginPlan } from './login'
import { registerConnectorLocales } from './i18n'
import { chatNamesFrom, chatText, peopleFrom } from './people'
import { PeopleContext } from './Person'
import './connector.css'

// The shared words, in every language of the web UI, under the namespace "connector".
registerConnectorLocales()

// The SDK of the running add-on, or null when the page runs without one.
function sdkOrNull() {
  try { return getSdk() } catch { return null }
}

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
          {s.chat && <span className="cn-muted">{answerT(t)('sender.chat', { chat: chatText(s.chat, s.chat_name) })}</span>}
          {!s.allowed && s.reason && <span className="cn-muted" data-testid="sender-reason" data-code={s.reason}>{answerT(t)(`sender.reason.${s.reason}`, { defaultValue: s.reason })}</span>}
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
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={busy} onClick={onOff}>{s('dup.turnOff')}</button>
        <button type="button" className="cn-link cn-link--text" data-testid="duplicate-open" onClick={() => onOpen(other.id)}>{s('dup.openOther', { name: other.name })}</button>
      </span>
    </Notice>
  )
}

// What a save did, in words: the parts it changed, and the triggers that lost an act because the connection no longer allows it.
export function SaveNotice({ notice, s }) {
  const { created, parts, dropped } = notice
  const what = created ? s('saved.created') : parts.length === 0 ? s('saved.nothing') : s('saved.changed', { what: parts.map((p) => s(`saved.part.${p}`)).join(', ') })
  return (
    <>
      <Notice tone="ok" role="status" testId="connector-saved">{what}</Notice>
      {dropped.length > 0 && (
        <Notice tone="warn" role="status" testId="connector-acts-dropped">
          <p className="cn-notice__text">{s('saved.dropped.title')}</p>
          <ul>
            {dropped.map((d) => (
              <li key={d.id} data-testid="connector-acts-dropped-item">{s('saved.dropped.item', { name: d.name, acts: d.acts.map((n) => s(`approval.actShort.${n}`)).join(', ') })}</li>
            ))}
          </ul>
        </Notice>
      )}
    </>
  )
}

const ONE_LIST = [{ key: 'allow', hint: 'allowHint', testId: 'connector-allow' }]

// The bindings that other people shared with the person: the name, the state
// and who shared it. There is nothing to change here: only their owner can.
function SharedConnections({ rows, s }) {
  const Badge = sdkOrNull()?.ui?.SharedByBadge
  return (
    <section className="cn-shared" aria-labelledby="cn-shared-title" data-testid="connector-shared">
      <h2 className="cn-section__title" id="cn-shared-title">{s('shared.title')}</h2>
      <p className="cn-muted">{s('shared.text')}</p>
      <ul className="cn-conns">
        {rows.map((c) => (
          <li key={c.id} className="cn-conn" data-testid="connector-shared-row" data-state={c.enabled ? 'running' : 'off'}>
            <div className="cn-conn__body">
              <Tile c={c} />
              <span className="cn-conn__text">
                <span className="cn-conn__name">{c.name}</span>
                <span className="cn-conn__facts">{c.enabled ? s('shared.on') : s('shared.off')}</span>
                {Badge
                  ? <Badge entry={{ role: c.role, owner: c.owner, everyone: false }} roleLabel={s('share.roleUse')} />
                  : <span className="cn-muted">{s('shared.by', { name: c.owner || '' })}</span>}
              </span>
            </div>
          </li>
        ))}
      </ul>
    </section>
  )
}

// The host's Share dialog for one binding. The host makes the binding
// shareable and calls the sharing routes; this page passes the binding only.
function ShareConnection({ share, s, onChanged, close }) {
  const Dialog = sdkOrNull()?.ui?.ConnectionShareDialog
  if (!Dialog) return null
  return (
    <Dialog
      binding={share.binding}
      name={share.name}
      roleLabel={s('share.roleUse')}
      roleHelp={s('share.roleUseHelp')}
      intro={s('share.intro')}
      onChanged={onChanged}
      onLost={() => { close(); onChanged() }}
      close={close}
      testId="connector-share-dialog"
    />
  )
}

/**
 * @param {object} props
 * @param {string} props.kind          the kind of the bindings, such as telegram
 * @param {string} props.ns            the i18n namespace of the add-on
 * @param {string} props.testId        the test id of the page
 * @param {Function} [props.TokenFields] the fields of the token ({ form, setForm, t })
 * @param {Function} [props.tokenOf]   the secret that the fields make, '' when not typed
 * @param {Array} [props.lists]        the allowlists: { key, hint, help, testId, match }
 * @param {Function} [props.Help]      a block under the intro ({ t })
 * @param {string} [props.notifyMode]  the inputMode of the notification field
 * @param {Function} [props.isChannelEntry] whether an allowlist entry names a channel or group: (entry) => boolean.
 *                                     The kind knows its IDs: the page puts such an entry in the table of channels.
 * @param {boolean} [props.mixedList]  a kind that cannot tell a person from a channel by its ID keeps one list of people
 *                                     and channels, with the level of a channel on every entry
 * @param {boolean} [props.messaging]  false for a kind that carries no messages: the page then hides the sender,
 *                                     reply, file and notification settings and the switch of the binding
 * @param {Function} [props.ExtraFields] more fields of the add-on's own ({ form, setForm, t })
 * @param {Function} [props.extraBody] the request fields they make: (form) => object
 * @param {Function} [props.extraForm] their values from a saved binding: (binding) => object
 * @param {Function} [props.extraValid] whether they allow saving: (form) => boolean
 * @param {Function} [props.testExtra] more checks after the Test button: async (binding, t) => string
 * @param {Function} [props.RowInfo]   a line under a binding in the list ({ c, t })
 * @param {boolean} [props.canAdd]     false for a kind whose connections are made elsewhere: the page then edits
 *                                     the saved ones and has no New button
 * @param {boolean} [props.login]      a kind whose add-on logs in with OAuth: the page draws the host's login
 *                                     (sdk.ui.ConnectionLogin) in place of the New button and its token sheet.
 *                                     The add-on's text has `connect` (the button) and `defaultName`
 */
export default function ConnectorPage({ kind, ns, testId, TokenFields = SingleToken, tokenOf = (f) => f.token || '', lists = ONE_LIST, Help, notifyMode = 'text',
  messaging: messagingProp = true, isChannelEntry: channelPredicate, mixedList = false, ExtraFields, extraBody, extraForm, extraValid, testExtra, RowInfo, canAdd = true, login = false }) {
  const { t, i18n } = useTranslation([ns, 'connector'])
  const uiLanguage = i18n?.resolvedLanguage || i18n?.language || ''
  setApiLanguage(uiLanguage)
  const [items, setItems] = useState([])
  // The bindings of this kind that other people shared with the person (name,
  // state and owner only), whether the host shares here, and the open dialog.
  const [shared, setShared] = useState([])
  const [canShare, setCanShare] = useState(false)
  const [share, setShare] = useState(null)
  const [agents, setAgents] = useState([])
  const [agentsReady, setAgentsReady] = useState(false)
  const [off, setOff] = useState('')
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  // What the last save changed: { created, parts, dropped }. Cleared when a form opens or a save starts.
  const [saveNotice, setSaveNotice] = useState(null)
  // The open form of the sheet, or null.
  const [form, setForm] = useState(null)
  const [fixedAgent, setFixedAgent] = useState(false)
  const [focusToken, setFocusToken] = useState(0)
  const [deepPermissions, setDeepPermissions] = useState(false)
  // The permission preset the person chose; null follows what the permissions are.
  const [presetChoice, setPresetChoice] = useState(null)
  const [formError, setFormError] = useState('')
  const [askDiscard, setAskDiscard] = useState(false)
  const [confirmed, setConfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  // True while the sheet tests a connection it has just saved, and the failure of that test: { reason }.
  const [testStep, setTestStep] = useState(false)
  const [testFail, setTestFail] = useState(null)
  const [toDelete, setToDelete] = useState(null)
  // The question after a save that would make this connection the same bot as another: { other }.
  const [dupAsk, setDupAsk] = useState(null)
  const [detailId, setDetailId] = useState('')
  const [rowBusy, setRowBusy] = useState({})
  const [testing, setTesting] = useState({})
  const [kindInfo, setKindInfo] = useState(null)
  const [kindsReady, setKindsReady] = useState(false)
  const [capabilities, setCapabilities] = useState([])
  const [accessVersion, setAccessVersion] = useState(0)
  // The names of the people who wrote to the open binding, to show beside their IDs.
  const [directory, setDirectory] = useState(() => new Map())
  // The sender log of the open binding: the recent people to offer, and the names of the chats.
  const [senderLog, setSenderLog] = useState([])
  // The last token test of each saved binding, kept in this browser: { ok, text, reason, at }.
  const [checks, setChecks] = useState(() => readChecks())
  // The pairings of the open binding, as pairing here changes them.
  const [pairedList, setPairedList] = useState(null)
  // The form as it was when the sheet opened, to ask before closing over a change.
  const startPrint = useRef('')
  // The open form, for an answer that comes back after the person closed the sheet.
  const formRef = useRef(null)
  formRef.current = form
  // The control that opened the sheet. A menu item is gone once the menu closes, so focus goes back to the menu button.
  const openerRef = useRef(null)
  const kindFiles = kindInfo?.files || null
  // A kind that declares agent_optional lets a binding hold credentials only.
  const agentOptional = Boolean(kindInfo?.agent_optional)
  // A tool-only kind (tool_only) has no agent: many agents use one connection through their own sets, so the form
  // shows no agent and the rows say how many agents use it. It never carries messages, whatever the add-on passes.
  const toolOnly = Boolean(kindInfo?.tool_only)
  const messaging = messagingProp && !toolOnly
  const plan = loginPlan({ login, canAdd, sdk: sdkOrNull(), kindInfo, kindsReady })
  const sheetAdd = plan.sheetAdd
  const s = answerT(t)
  // Which entries of the allowlist name a channel or group, where people are trusted by the setting of the channel. Without a rule of the kind, the last of several lists holds the channels.
  const isChannelEntry = channelPredicate || ((x) => lists.length > 1 && listEntries([x], lists, lists.length - 1).length === 1)
  const pairedChanged = useCallback((list) => { if (Array.isArray(list)) setPairedList(list); setAccessVersion((v) => v + 1) }, [])
  const isAdmin = useIsAdmin(useAuth)
  // A login kind makes its connections in the host's login, not in the token sheet.
  const Login = login ? sdkOrNull()?.ui?.ConnectionLogin : null

  const fail = useCallback((e) => {
    if (e?.status === 409 && e.body?.error?.type === 'connectors_off') { setOff(e.body.error.message); return }
    setError(e?.message || '')
  }, [])

  const load = useCallback(async () => {
    try {
      const res = await connectorsApi.list()
      const list = (res.connectors || []).filter((c) => c.kind === kind)
      setItems(list)
      setShared(sharedOfKind(res, kind))
      setCanShare(shareAvailable(sdkOrNull(), res))
      setOff('')
      return list
    } catch (e) { fail(e); return null }
  }, [fail, kind])

  useEffect(() => { load() }, [load])
  // Share is offered only where the host draws the dialog and the server
  // shares; the row menu and the pane then open the host's dialog.
  const shareOf = canShare ? (c) => { setError(''); setShare({ binding: c.id, name: c.name }) } : undefined
  // What the add-on of this kind offers: the choices of its options.
  useEffect(() => {
    connectorsApi.kinds().then((r) => {
      setKindInfo((r.kinds || []).find((k) => k.kind === kind) || null)
      setCapabilities(r.capabilities || [])
    }).catch(() => {}).finally(() => setKindsReady(true))
  }, [kind, uiLanguage])
  // The permission texts come from the host in the language of the page: ask again when it changes.
  const languageSeen = useRef(uiLanguage)
  useEffect(() => {
    if (languageSeen.current === uiLanguage) return
    languageSeen.current = uiLanguage
    setAccessVersion((v) => v + 1)
  }, [uiLanguage])
  useEffect(() => { agentsApi.list().then((r) => setAgents(r.agents || [])).catch(() => {}).finally(() => setAgentsReady(true)) }, [])

  const agentOf = (id) => agents.find((a) => a.id === id)
  const agentName = (id) => agentOf(id)?.name || id
  const extra = (f) => ({ ...(messaging ? {} : { enabled: false, accept_any: false }), ...(extraBody ? extraBody(f) : {}) })
  const savedFormOf = (c) => ({ ...connectorForm(c), ...(extraForm ? extraForm(c) : {}) })
  // What the sheet would save, with the token typed: the same text means nothing changed.
  const printOf = (f) => JSON.stringify(connectorPayload(f, kind, tokenOf(f).trim(), extra(f)))

  // A new connection starts with the usual permissions for its agent. While the
  // preset is Standard, choosing another agent ticks the usual set of that agent.
  const withAgent = (f, agentId) => {
    if (toolOnly) return f
    const standard = presetChoice ? presetChoice === 'standard' : presetOf(f, capabilities, agentOf(f.agent_id)) === 'standard'
    const next = { ...f, agent_id: agentId }
    return standard && capabilities.length > 0 ? applyPreset(next, 'standard', capabilities, agentOf(agentId)) : next
  }

  const openForm = (f, opts = {}) => {
    openerRef.current = opts.opener || document.activeElement
    setForm(f)
    startPrint.current = printOf(f)
    setConfirmed(Boolean(f.accept_any))
    setNote('')
    setError('')
    setFormError('')
    setTestFail(null)
    setTestStep(false)
    setSaveNotice(null)
    setPairedList(null)
    setDupAsk(null)
    setAskDiscard(false)
    setFixedAgent(Boolean(opts.fixedAgent))
    setPresetChoice(opts.preset || null)
    setDeepPermissions(Boolean(opts.permissions))
    setFocusToken(opts.focusToken ? Date.now() : 0)
  }
  const openNew = (agentId = '') => {
    const picked = agentId && !toolOnly ? agents.find((a) => a.id === agentId && a.model) : null
    const held = agentHeld(picked || (!toolOnly && agents.length === 1 ? agents[0] : null))
    const f = { ...emptyConnector(capabilities, held), ...(extraForm ? extraForm({}) : {}) }
    openForm(picked ? { ...f, agent_id: picked.id } : f, { fixedAgent: Boolean(picked) })
  }
  const openEdit = (c, opts) => openForm(savedFormOf(c), opts)
  // The menu of a row passes its button as the opener.
  const editFromRow = (c, trigger) => openEdit(c, { opener: trigger instanceof HTMLElement ? trigger : null })
  const openById = (id) => {
    const c = items.find((x) => x.id === id)
    if (c) openEdit(c)
  }

  // The links into this page, handled once. ?binding=<id> (from the agent page
  // and its Tools list) opens the settings of that connection with its
  // permissions in view. ?new=1&agent=<id> opens the add sheet at once with that
  // agent chosen and fixed, so a screen elsewhere can send a person to set it up.
  const linked = useRef(false)
  useEffect(() => {
    if (linked.current || !kindsReady) return
    const q = new URLSearchParams(window.location.search)
    // A kind whose connections are made elsewhere (canAdd false) has no add sheet.
    if (sheetAdd && q.get('new') === '1') {
      if (!agentsReady) return
      linked.current = true
      openNew(q.get('agent') || '')
      return
    }
    const id = q.get('binding')
    if (!id) { linked.current = true; return }
    const c = items.find((x) => x.id === id)
    if (!c) return
    linked.current = true
    openEdit(c, { permissions: true, preset: capabilities.length > 0 && messaging ? 'custom' : null })
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, kindsReady, agentsReady])

  const openId = form?.id || ''
  useEffect(() => {
    if (!openId || !messaging) { setDirectory(new Map()); setSenderLog([]); return undefined }
    let live = true
    connectorsApi.senders(openId).then((r) => { if (live) { setDirectory(peopleFrom(r?.senders)); setSenderLog(r?.senders || []) } }).catch(() => { if (live) { setDirectory(new Map()); setSenderLog([]) } })
    return () => { live = false }
  }, [openId, messaging, accessVersion])
  // The question sits by the Save button, at the end of a long form: bring it into view.
  useEffect(() => {
    if (dupAsk) document.querySelector('[data-testid="duplicate-confirm"]')?.scrollIntoView?.({ block: 'nearest' })
  }, [dupAsk])

  const remember = (id, record) => {
    setChecks((m) => {
      const next = { ...m, [id]: record }
      writeChecks(next)
      return next
    })
  }
  // Tests a saved connection and keeps the result. Returns the text to show; throws when the test failed.
  const check = async (c) => {
    try {
      const r = await connectorsApi.test(c.id)
      let text = t('testOk', { bot: r.bot_username })
      if (testExtra) text += ` ${await testExtra(c, t)}`
      remember(c.id, checkRecord({ ok: true, text }))
      return text
    } catch (e) {
      remember(c.id, checkRecord({ ok: false, reason: e?.message || '' }))
      throw e
    }
  }
  const runTest = async (c) => {
    setError('')
    setTesting((m) => ({ ...m, [c.id]: true }))
    try { setNote(await check(c)) } catch (e) { fail(e) } finally { setTesting((m) => ({ ...m, [c.id]: false })) }
  }

  // Tests a connection from the open sheet. The sheet stays open while the test
  // runs: on success it closes, on failure it stays and says why, next to the
  // buttons, so the person can test again or change the token. A sheet that the
  // person closed meanwhile gets the answer on the page, as a row test does.
  // fresh is the saved connection as the list now has it: the sheet shows that, without the token typed.
  const testInSheet = async (c, fresh = null) => {
    setTestStep(true)
    setTesting((m) => ({ ...m, [c.id]: true }))
    try {
      const text = await check(c)
      setTestFail(null)
      setNote(text)
      setForm(null)
    } catch (err) {
      const reason = err?.message || ''
      if (formRef.current) {
        // The connection is saved: the form now edits it, and nothing is left to discard.
        // The token that was typed is saved and sealed; the form no longer holds it.
        const next = fresh ? savedFormOf(fresh) : { ...formRef.current, id: c.id }
        startPrint.current = printOf(next)
        setForm(next)
        setTestFail({ reason })
      } else setError(t('checkFailed', { reason }))
    } finally {
      setTesting((m) => ({ ...m, [c.id]: false }))
      setTestStep(false)
    }
  }
  const testAgain = async () => {
    const c = items.find((x) => x.id === form?.id)
    if (!c) return
    setBusy(true)
    try { await testInSheet(c) } finally { setBusy(false) }
  }

  // The server refuses a token that the platform refuses. After a save the
  // page tests the connection and names the bot the token belongs to.
  const save = (e) => { e.preventDefault(); return doSave(false) }
  // confirmDuplicate answers the question of a save that makes the connection a twin of another.
  const doSave = async (confirmDuplicate) => {
    setBusy(true)
    setFormError('')
    setTestFail(null)
    setNote('')
    setError('')
    setSaveNotice(null)
    setDupAsk(null)
    try {
      const token = tokenOf(form).trim()
      const body = connectorPayload(form, kind, token, { ...extra(form), ...(confirmDuplicate ? { confirm_duplicate: DUPLICATE_CONFIRMATION } : {}) })
      // What the save changes, read from the form and the binding as they are now.
      const before = form.id ? savedForm : null
      const removed = removedActs(before, form)
      const saved = form.id ? await connectorsApi.update(form.id, body) : await connectorsApi.create(body)
      const wasNew = !form.id
      const snapshot = form
      const list = await load()
      // The triggers that held an act the connection no longer allows; a failed read only leaves the list out.
      const dropped = snapshot.id && removed.length > 0 ? droppedActs((await triggersApi.list().catch(() => null))?.triggers, snapshot.id, removed) : []
      setSaveNotice({ created: wasNew, parts: changedParts(snapshot, before), dropped })
      if (saved?.id) await testInSheet(saved, list?.find((x) => x.id === saved.id) || null)
      else setForm(null)
    } catch (err) {
      if (isDuplicateRefusal(err)) setDupAsk({ other: duplicateRefusalName(err) })
      else setFormError(err?.message || '')
      if (err?.status === 409 && err.body?.error?.type === 'connectors_off') { setForm(null); fail(err) }
    } finally { setBusy(false) }
  }

  const act = async (fn) => {
    setError('')
    try { await fn(); await load() } catch (e) { fail(e) }
  }
  const toggle = async (c, on) => {
    setRowBusy((m) => ({ ...m, [c.id]: true }))
    setItems((list) => list.map((x) => (x.id === c.id ? { ...x, enabled: on } : x)))
    await act(() => (on ? connectorsApi.enable(c.id) : connectorsApi.disable(c.id)))
    setRowBusy((m) => ({ ...m, [c.id]: false }))
  }
  const fixRow = (c, what) => {
    if (what === 'token') openEdit(c, { focusToken: true })
    else if (what === 'duplicate') act(() => connectorsApi.disable(c.id))
  }

  // When the sheet closes, focus goes back to the control that opened it.
  const wasOpen = useRef(false)
  useEffect(() => {
    if (wasOpen.current && !form) {
      const el = openerRef.current
      if (el && el.isConnected) requestAnimationFrame(() => el.focus?.())
    }
    wasOpen.current = Boolean(form)
  }, [form])

  // Closing the sheet over a change asks first.
  const savedItem = form?.id ? items.find((x) => x.id === form.id) : null
  const savedForm = savedItem ? savedFormOf(savedItem) : null
  const changed = form ? printOf(form) !== startPrint.current : false
  const closeSheet = () => { if (changed) setAskDiscard(true); else setForm(null) }

  // What the form will do, asked of the host for the form as it is now.
  const preview = useAccessPreview({ form, kind, enabled: Boolean(form) && messaging && capabilities.length > 0, version: accessVersion })
  const tokenTyped = form ? Boolean(tokenOf(form).trim()) : false
  const dirty = form ? !savedForm || tokenTyped || JSON.stringify(connectorPayload(form, kind, '', extra(form))) !== JSON.stringify(connectorPayload(savedForm, kind, '', extra(savedForm))) : false
  const people = form ? (lists.length > 1 ? listEntries(form.allow, lists, 0).length : (form.allow || []).length) : 0
  const fixCtx = { agentId: form?.agent_id || '', isAdmin, lists, form: form || {}, people }
  const tokenCheck = form?.id && checks[form.id] ? { ok: checks[form.id].ok, bot: checks[form.id].text, reason: checks[form.id].reason } : null
  const readyInput = form && { form, saved: savedForm, kindInfo, capabilities, agents, lists, tokenTyped, token: tokenCheck, preview, paired: pairedList || form.paired || [], agentOptional, toolOnly, messaging, isChannel: isChannelEntry }
  const preset = form ? presetChoice || presetOf(form, capabilities, agentOf(form.agent_id)) : 'answer'

  if (off) {
    return (
      <div className="cn-page" data-testid={testId}>
        <h1 className="cn-title">{t('title')}</h1>
        <Notice tone="info" role="note">{off}</Notice>
      </div>
    )
  }

  // ---- The rows --------------------------------------------------------------
  const kindTitle = kindInfo?.title || kind
  const whoText = (c) => (c.accept_any ? t('modes.anyone') : (c.allow || []).length === 0 ? t('modes.nobody') : t('modes.listed', { count: c.allow.length }))
  const usedByText = (c) => (c.used_by > 0 ? s('usedBy.count', { count: c.used_by }) : s('usedBy.none'))
  // Without a count (the agents could not be read) a tool-only row says nothing about agents.
  const agentText = (c) => (toolOnly ? (typeof c.used_by === 'number' ? usedByText(c) : '') : c.credentials_only ? s('credentialsOnly') : t('agentOf', { name: agentName(c.agent_id) }))
  const factsOf = (c) => [kindTitle, agentText(c), ...(messaging && !c.credentials_only ? [t('accepts', { who: whoText(c) })] : [])].filter(Boolean).join(' · ')
  const reasonOf = (a) => {
    if (!a) return ''
    if (a.cause === 'inactive') return t('inactive', { reason: a.reason })
    if (a.cause === 'noToken') return s('conn.reason.noToken')
    if (a.cause === 'duplicate') return s('conn.reason.duplicate', { name: a.reason })
    return s('conn.reason.testFailed', { reason: a.reason })
  }
  const fixLabel = (what) => (what === 'token' ? s('conn.fix.token') : s('dup.turnOff'))
  const viewOf = (c) => {
    const { state, attention } = connectionState(c, checks[c.id], { messaging })
    const kindOfFix = fixOf(attention)
    return { state, attention, fix: kindOfFix ? { kind: kindOfFix, label: fixLabel(kindOfFix) } : null }
  }
  const detail = items.find((x) => x.id === detailId) || null
  const paneFacts = (c) => [
    [s('conn.kv.service'), kindTitle],
    ...(toolOnly ? (typeof c.used_by === 'number' ? [[s('conn.kv.agents'), <span key="u" data-testid="connector-used-by">{usedByText(c)}</span>]] : [])
      : [[t('agent'), c.credentials_only ? s('credentialsOnly') : agentName(c.agent_id)]]),
    ...(messaging && !c.credentials_only ? [
      [s('conn.kv.accepts'), whoText(c)],
      [s('conn.kv.replies'), <span key="r" data-testid="connector-answer-summary">{answerSummary(c, t)}</span>],
    ] : []),
    [s('conn.kv.token'), c.has_token ? s('conn.kv.saved') : s('conn.kv.notSaved')],
  ]

  const dupNode = dupAsk && (
    <Notice tone="warn" role="alert" testId="duplicate-confirm">
      <p className="cn-notice__text">{s('dup.text', { name: dupAsk.other })}</p>
      <p className="cn-notice__text">{s('dup.confirmHelp')}</p>
      <span className="cn-row__actions">
        <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" data-testid="duplicate-save-anyway" disabled={busy} onClick={() => doSave(true)}>{s('dup.saveAnyway')}</button>
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => setDupAsk(null)}>{s('dup.goBack')}</button>
      </span>
    </Notice>
  )

  const sheetProps = form && {
    t, s, form, setForm, messaging, capabilities, kindInfo, kindFiles, agents, agentOptional, toolOnly, TokenFields, ExtraFields, savedItem, busy, error: formError, fixedAgent,
    testStep, testFail, onTestAgain: testAgain,
    onSubmit: save, onClose: closeSheet, canSubmit: canSave(form, confirmed, tokenTyped, kindFiles, agentOptional) && !(extraValid && !extraValid(form)),
    agentName, toolOnly, agentHref: savedItem && !toolOnly ? `${basePath}/app/agents/${encodeURIComponent(savedItem.agent_id)}` : '', dirty,
    sameBot: messaging && form.id && savedItem ? <SameBotNotice c={savedItem} s={s} busy={busy} onOff={() => { setForm(null); act(() => connectorsApi.disable(form.id)) }} onOpen={openById} /> : null,
    fixCtx, readyInput, withAgent, preset, setPreset: setPresetChoice, confirmed, setConfirmed, isChannelEntry, mixedList, pairedChanged, pairedList,
    senderLog, lists, preview, accessVersion, notifyMode, onAllowed: (sender) => { setForm((f) => (f && !(f.allow || []).includes(sender) ? { ...f, allow: [...(f.allow || []), sender] } : f)); setAccessVersion((v) => v + 1); load() },
    dupAsk: dupNode, focusToken, scrollToPermissions: deepPermissions, openCheck: deepPermissions,
  }

  return (
    <HintLabels.Provider value={{ more: s('more'), less: s('less'), optional: s('optional') }}>
    <PeopleContext.Provider value={directory}>
    <div className="cn-page" data-testid={testId}>
      <header className="cn-head">
        <h1 className="cn-title">{t('title')}</h1>
        {sheetAdd && (
          <button type="button" className="dk-btn dk-btn--primary" data-testid="connector-new" disabled={!kindsReady} onClick={() => openNew()}>
            <Icon name="plus" /> {t('new')}
          </button>
        )}
      </header>
      <p className="cn-muted">{t('intro')}</p>
      {Help && <Help t={t} />}
      {plan.show === 'host' && (
        <Login kind={kindInfo} bindings={items} onChanged={load} connectLabel={t('connect')} defaultName={t('defaultName')} />
      )}
      {plan.show === 'oldHost' && <p className="cn-muted" data-testid="login-needs-host">{t('connector:loginNeedsHost')}</p>}

      {error && (
        <Notice tone="error" role="alert">
          <span>{error}</span>
          <button type="button" className="cn-link" aria-label={answerT(t)('dismiss')} onClick={() => setError('')}>×</button>
        </Notice>
      )}
      {saveNotice && <SaveNotice notice={saveNotice} s={s} />}
      {note && <Notice tone="ok" role="status">{note}</Notice>}

      <PaneLayout className="cn-layout">
        <div className="cn-listcol">
          <ul className="cn-conns" aria-label={t('title')}>
            {items.map((c) => {
              const v = viewOf(c)
              return (
                <ConnectionRow key={c.id} c={c} state={v.state} attention={v.attention} fix={v.fix} facts={factsOf(c)}
                  line={v.attention ? { text: reasonOf(v.attention), testId: v.attention.cause === 'inactive' ? 'connector-inactive' : 'connector-attention' } : null}
                  selected={c.id === detailId} testing={Boolean(testing[c.id])} busy={Boolean(rowBusy[c.id])} messaging={messaging} t={t} s={s}
                  onOpen={setDetailId} onToggle={toggle} onTest={runTest} onFix={fixRow} onEdit={editFromRow} onDelete={setToDelete} onShare={shareOf} />
              )
            })}
          </ul>
          {items.length === 0 && !form && <p className="cn-muted">{t('empty')}</p>}
        </div>
        {detail && (() => {
          const v = viewOf(detail)
          return (
            <ConnectionPane key={detail.id} c={detail} state={v.state} attention={v.attention} fix={v.fix} check={checks[detail.id]} testing={Boolean(testing[detail.id])}
              busy={Boolean(rowBusy[detail.id])} messaging={messaging} facts={paneFacts(detail)} RowInfo={RowInfo} t={t} s={s}
              Senders={<Senders id={detail.id} onAllowed={load} t={t} />}
              onClose={() => setDetailId('')} onToggle={toggle} onTest={runTest} onFix={fixRow} onEdit={(x) => openEdit(x)} onDelete={setToDelete} onShare={shareOf} />
          )
        })()}
      </PaneLayout>

      {canShare && shared.length > 0 && <SharedConnections rows={shared} s={s} />}
      {share && canShare && <ShareConnection share={share} s={s} onChanged={load} close={() => setShare(null)} />}
      {form && <ConnectionSheet p={sheetProps} />}
      <ConfirmDialog
        open={askDiscard}
        title={s('conn.discard.title')}
        message={s('conn.discard.message')}
        confirmLabel={s('conn.discard.confirm')}
        danger
        onConfirm={() => { setAskDiscard(false); setForm(null) }}
        onCancel={() => setAskDiscard(false)}
      />
      <ConfirmDialog
        open={Boolean(toDelete)}
        title={t('confirmDelete.title', { name: toDelete?.name || '' })}
        message={t('confirmDelete.message')}
        confirmLabel={t('confirmDelete.confirm')}
        danger
        onConfirm={() => {
          const c = toDelete
          setToDelete(null)
          if (detailId === c.id) setDetailId('')
          setChecks((m) => { const next = dropCheck(m, c.id); writeChecks(next); return next })
          act(() => connectorsApi.remove(c.id))
        }}
        onCancel={() => setToDelete(null)}
      />
    </div>
    </PeopleContext.Provider>
    </HintLabels.Provider>
  )
}
