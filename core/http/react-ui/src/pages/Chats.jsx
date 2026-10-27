/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import Icon from '../components/Icon'
import ChatInbox from '../components/ChatInbox'
import ConfirmDialog from '../components/ConfirmDialog'
import ModelSelector from '../components/ModelSelector'
import { useInbox } from '../hooks/useInbox'
import { CAP_CHAT } from '../utils/capabilities'
import { useServerChat } from '../hooks/useServerChat'
import { addonChange } from '../utils/addonChange'
import { chatAddonsApi, chatAgentsApi, chatSkillsApi } from '../utils/api'
import './chats.css'
import './chatauto.css'

// First line of a tool entry: the transcript keeps the summary and a capped output.
const firstLine = (text) => (text || '').split('\n')[0]


// The skills a chat can use, with a switch each. Only the skill that wins is
// listed: one that another overrides cannot be used anyway.
function SkillsPanel({ chatId, filesOn, t }) {
  const [skills, setSkills] = useState([])
  const [error, setError] = useState('')
  useEffect(() => {
    if (!chatId || !filesOn) { setSkills([]); return undefined }
    let live = true
    chatSkillsApi.list(chatId)
      .then((res) => { if (live) setSkills((res.skills || []).filter((s) => !s.shadowed && !s.invalid)) })
      .catch(() => { if (live) setSkills([]) })
    return () => { live = false }
  }, [chatId, filesOn])
  if (!chatId || !filesOn) return null
  const toggle = async (name, on) => {
    const next = skills.map((s) => (s.name === name ? { ...s, enabled: on } : s))
    const off = next.filter((s) => !s.enabled).map((s) => s.name).sort()
    const before = skills
    setSkills(next)
    setError('')
    try {
      await chatSkillsApi.setOff(chatId, off)
    } catch (e) {
      setSkills(before)
      setError(e.message || t('errors.send'))
    }
  }
  const on = skills.filter((s) => s.enabled).length
  return (
    <details className="ch-skills">
      <summary>{t('skills.summary', { on, total: skills.length })}</summary>
      {skills.length === 0 ? <p className="ch-head__meta">{t('skills.none')}</p> : (
        <ul className="ch-skills__list">
          {skills.map((s) => (
            <li key={`${s.layer}-${s.name}`}>
              <label title={s.description}>
                <input type="checkbox" checked={s.enabled} aria-label={s.name} onChange={(e) => toggle(s.name, e.target.checked)} />
                <span>{s.name}</span>
              </label>
            </li>
          ))}
        </ul>
      )}
      {error && <p className="ch-error" role="alert">{error}</p>}
      <Link className="ch-link" to="/app/chats/skills">{t('skills.manage')}</Link>
    </details>
  )
}

// The add-ons whose tools the chat can use, with a switch each. A user who has
// none sees no panel.
function AddonsPanel({ chatId, t }) {
  const [addons, setAddons] = useState([])
  const [error, setError] = useState('')
  useEffect(() => {
    if (!chatId) { setAddons([]); return undefined }
    let live = true
    chatAddonsApi.list(chatId)
      .then((res) => { if (live) setAddons(res.addons || []) })
      .catch(() => { if (live) setAddons([]) })
    return () => { live = false }
  }, [chatId])
  if (!chatId || addons.length === 0) return null
  const toggle = async (id, on) => {
    const next = addons.map((a) => (a.id === id ? { ...a, off: !on } : a))
    const off = next.filter((a) => a.off).map((a) => a.id).sort()
    const before = addons
    setAddons(next)
    setError('')
    try {
      const res = await chatAddonsApi.setOff(chatId, off)
      if (res?.addons) setAddons(res.addons)
    } catch (e) {
      setAddons(before)
      setError(e.message || t('errors.send'))
    }
  }
  const on = addons.filter((a) => !a.off).length
  return (
    <details className="ch-skills">
      <summary>{t('addons.summary', { on, total: addons.length })}</summary>
      <ul className="ch-skills__list">
        {addons.map((a) => (
          <li key={a.id}>
            <label title={t('addons.tools', { count: a.tools })}>
              <input type="checkbox" checked={!a.off} aria-label={a.id} onChange={(e) => toggle(a.id, e.target.checked)} />
              <span>{a.id}</span>
            </label>
          </li>
        ))}
      </ul>
      {error && <p className="ch-error" role="alert">{error}</p>}
    </details>
  )
}

function Transcript({ messages, t }) {
  return messages.map((m) => {
    if (m.role === 'user') {
      return <div key={m.seq} className="ch-msg ch-msg--user">{m.text}</div>
    }
    if (m.role === 'assistant') {
      return <div key={m.seq} className="ch-msg">{m.text}</div>
    }
    if (m.role === 'reasoning') {
      return (
        <details key={m.seq} className="ch-thought">
          <summary>{t('reasoning')}{m.dur_ms ? ` ${Math.max(1, Math.round(m.dur_ms / 1000))} s` : ''}</summary>
          <div className="ch-thought__text">{m.text}</div>
        </details>
      )
    }
    if (m.role === 'tool') {
      return (
        <div key={m.seq} className="ch-tools">
          <div className="ch-tool"><span>{firstLine(m.text)}</span></div>
          <ChangeNote text={m.text} t={t} />
        </div>
      )
    }
    return null // checkpoints are for the sandbox's undo, not for reading
  })
}

// What a change tool of an add-on did, under its tool entry. The add-on's switch
// for the chat is the consent, so a change is made at once and there is no
// Confirm: the line says what was done and whether it can be undone.
function ChangeNote({ text, t }) {
  const c = addonChange(text)
  if (!c) return null
  if (c.kind === 'proposed') return <div className="ch-tool__change">{t('tool.waiting', { addon: c.addonId })}</div>
  return (
    <div className="ch-tool__change">
      {c.count > 0 ? t('tool.applied', { addon: c.addonId, count: c.count }) : t('tool.appliedNone', { addon: c.addonId })}
      {' '}
      {c.undoable ? t('tool.undoable') : t('tool.notUndoable')}
    </div>
  )
}

function LiveTurn({ live, t }) {
  return (
    <>
      {live.reasoning && (
        <details className="ch-thought">
          <summary>{t('reasoning')}</summary>
          <div className="ch-thought__text">{live.reasoning}</div>
        </details>
      )}
      {live.tools.length > 0 && (
        <div className="ch-tools">
          {live.tools.map((tool, i) => (
            <div key={i} className={`ch-tool${tool.state === 'failed' ? ' ch-tool--failed' : ''}`}>
              <span>{tool.name}{tool.summary ? ` ${tool.summary}` : ''}</span>
              <span className="ch-tool__state">
                {tool.state === 'running' ? t('tool.running') : tool.state === 'failed' ? t('tool.failed') : ''}
              </span>
            </div>
          ))}
        </div>
      )}
      {live.text && <div className="ch-msg">{live.text}</div>}
    </>
  )
}

function PendingList({ chat, t }) {
  const [editing, setEditing] = useState(null) // { id, text }
  const { pending, running, lastTurn, activeId } = chat
  if (pending.length === 0) return null
  const waiting = !running
  const reason = lastTurn === 'stopped' ? t('pending.afterStop') : t('pending.afterFailure')

  return (
    <div className="ch-pending" data-testid="pending-list">
      <div className="ch-pending__head">
        <span>{t('pending.header', { count: pending.length })}{running ? ` · ${t('pending.note')}` : ''}</span>
        {waiting && (
          <button type="button" className="btn btn-primary btn-sm" onClick={() => chat.sendNow(activeId)}>
            {t('composer.sendNow')}
          </button>
        )}
      </div>
      {waiting && lastTurn !== 'done' && lastTurn !== '' && <div className="ch-note">{reason}</div>}
      {pending.map((p) => (
        <div key={p.id} className="ch-pending__row" data-testid="pending-row">
          {editing?.id === p.id ? (
            <>
              <input
                className="input ch-pending__edit"
                aria-label={t('pending.edit')}
                value={editing.text}
                onChange={(e) => setEditing({ id: p.id, text: e.target.value })}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') { chat.editPending(activeId, p.id, editing.text); setEditing(null) }
                  if (e.key === 'Escape') setEditing(null)
                }}
                autoFocus
              />
              <span className="ch-pending__actions">
                <button type="button" className="ch-link" onClick={() => { chat.editPending(activeId, p.id, editing.text); setEditing(null) }}>{t('pending.save')}</button>
                <button type="button" className="ch-link" onClick={() => setEditing(null)}>{t('pending.discard')}</button>
              </span>
            </>
          ) : (
            <>
              <span className="ch-pending__text">{p.text}</span>
              <span className="ch-pending__actions">
                <button type="button" className="ch-link" onClick={() => setEditing({ id: p.id, text: p.text })}>{t('pending.edit')}</button>
                <button type="button" className="ch-link ch-link--danger" onClick={() => chat.cancelPending(activeId, p.id)}>{t('pending.cancel')}</button>
              </span>
            </>
          )}
        </div>
      ))}
    </div>
  )
}

export default function Chats() {
  const { t } = useTranslation('chats')
  const { id: routeId } = useParams()
  const navigate = useNavigate()
  const chat = useServerChat()
  const inbox = useInbox()
  const { select, activeId } = chat
  const location = useLocation()
  // An old chat link or the Home page names a model with ?model=; it is only a hint.
  const [searchParams] = useSearchParams()
  const [model, setModel] = useState(searchParams.get('model') || '')
  const [agents, setAgents] = useState([])
  // An agent chosen on the agents page arrives in the location state.
  const [agentId, setAgentId] = useState(location.state?.agentId || '')
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const [localError, setLocalError] = useState('')
  const [attached, setAttached] = useState([])
  const fileInput = useRef(null)
  // The chat asked to delete (waiting for the person to confirm) and whether the
  // delete is on its way. Under 720px the list of chats is a panel the header opens.
  const [toDelete, setToDelete] = useState(null)
  const [deleting, setDeleting] = useState(false)
  const [railOpen, setRailOpen] = useState(false)

  // The address names the open chat. Opening one by id selects it, unless it is
  // already open (creating a chat selects it before the address changes).
  useEffect(() => {
    const want = routeId || null
    // Opening a chat marks its notifications read on the server: read the inbox again after.
    if (want !== activeId) Promise.resolve(select(want)).finally(() => inbox.refresh())
  // activeId is read, not a trigger: only a change of address selects.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [routeId])

  // Opening a chat or starting a new one closes the panel of the list.
  useEffect(() => { setRailOpen(false) }, [routeId])

  // The agents are best effort: a server without them leaves the picker out.
  useEffect(() => {
    let live = true
    chatAgentsApi.list().then((r) => { if (live) setAgents(r.agents || []) }).catch(() => { if (live) setAgents([]) })
    return () => { live = false }
  }, [])
  const pickedAgent = agents.find((a) => a.id === agentId)
  const needsModel = !pickedAgent?.model

  const submit = useCallback(async () => {
    const text = draft.trim()
    const sending = attached
    // Only the message that starts a chat must go once: it creates the chat. In
    // a chat that exists, every message goes, even while the last one is still
    // on its way, and the hook keeps them in order.
    if ((!text && sending.length === 0) || (!activeId && busy)) return
    setLocalError('')
    try {
      let target = activeId
      if (!target) {
        if (!model && needsModel) return
        setBusy(true)
        target = await chat.createChat(model, agentId)
        await select(target)
        navigate(`/app/chats/${target}`)
      }
      setDraft('')
      setAttached([])
      await chat.send(target, text, sending)
    } catch (e) {
      // The text and files were cleared to make room for the next message: give
      // them back, unless the user has already typed or attached something else.
      setDraft(d => d || text)
      setAttached(a => (a.length ? a : sending))
      setLocalError(e.message || t('errors.send'))
    } finally {
      setBusy(false)
    }
  }, [draft, attached, busy, activeId, model, needsModel, agentId, chat, select, navigate, t])

  // The Home page hands over the first message in the location state. It goes
  // once, when a model is known; without one the text waits in the box.
  const startRef = useRef(location.state?.start || '')
  useEffect(() => {
    if (startRef.current) setDraft(startRef.current)
  }, [])
  useEffect(() => {
    const start = startRef.current
    if (!start || draft !== start || activeId || busy || (!model && needsModel)) return
    startRef.current = ''
    // Clear the state first: Back must not send the message again.
    navigate(`${location.pathname}${location.search}`, { replace: true, state: null })
    submit()
  }, [draft, activeId, busy, model, needsModel, submit, navigate, location.pathname, location.search])

  const onKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      submit()
    }
  }

  const shownError = localError || chat.error
  const dismissError = () => { setLocalError(''); chat.clearError() }
  const confirmDelete = async () => {
    const target = toDelete
    setDeleting(true)
    try {
      await chat.remove(target.id)
      // The address named the chat that is gone.
      if (target.id === activeId) navigate('/app/chats')
    } catch (e) {
      setLocalError(e.message || t('errors.delete'))
    } finally {
      setDeleting(false)
      setToDelete(null)
    }
  }
  const activeChat = chat.chats.find((c) => c.id === activeId)
  const title = activeChat?.title || t('list.untitled')
  const chatAgent = agents.find((a) => a.id === activeChat?.agent_id)

  return (
    <div className={`ch-page${railOpen ? ' ch-page--rail' : ''}`} data-testid="chats-page">
      <aside className="ch-list" id="ch-list" aria-label={t('list.label')}>
        <button type="button" className="btn btn-primary btn-full" onClick={() => navigate('/app/chats')}>
          {t('list.new')}
        </button>
        <ChatInbox inbox={inbox} />
        <div className="ch-list__items">
          {chat.chats.length === 0 && <div className="ch-list__empty">{t('list.empty')}</div>}
          {chat.chats.map((c) => (
            <div key={c.id} className="ch-list__item" aria-current={c.id === activeId ? 'true' : undefined}>
              <button type="button" className="ch-list__open" onClick={() => navigate(`/app/chats/${c.id}`)}>
                {c.origin === 'trigger' && <span title={t('list.automatic')} role="img" aria-label={t('list.automatic')}><Icon name="clock" /></span>}
                <span className="ch-list__title">{c.title || t('list.untitled')}</span>
                {c.running && <span className="ch-dot" role="img" aria-label={t('list.working')} />}
                {inbox.unreadChatIds.has(c.id) && <span className="ch-dot ch-dot--unread" role="img" aria-label={t('list.unread')} data-testid="unread-dot" />}
              </button>
              <button type="button" className="ch-link ch-link--danger" aria-label={t('list.deleteNamed', { title: c.title || t('list.untitled') })} onClick={() => setToDelete(c)}>×</button>
            </div>
          ))}
        </div>
        <nav className="ca-rail" aria-label={t('list.library')}>
          <p className="ca-rail__group">{t('list.library')}</p>
          {chat.files.on && <Link className="ch-link ch-list__skills" to="/app/files">{t('list.files')}</Link>}
          {chat.files.on && <Link className="ch-link ch-list__skills" to="/app/chats/skills">{t('skills.manage')}</Link>}
          <p className="ca-rail__group">{t('list.automation')}</p>
          <Link className="ch-link ch-list__skills" to="/app/chats/agents">{t('list.agents')}</Link>
          <Link className="ch-link ch-list__skills" to="/app/chats/triggers">{t('list.triggers')}</Link>
          <Link className="ch-link ch-list__skills" to="/app/chats/connectors">{t('list.connectors')}</Link>
        </nav>
      </aside>

      <section className="ch-main">
        <header className="ch-head">
          <button type="button" className="btn btn-secondary btn-sm ch-railbtn" aria-expanded={railOpen} aria-controls="ch-list"
            onClick={() => setRailOpen((o) => !o)}>
            {t('list.toggle')}
          </button>
          <h1 className="ch-head__title">{activeId ? title : t('title')}</h1>
          {chatAgent && <span className="ch-head__meta" data-testid="chat-agent">{t('title_agent', { name: chatAgent.name })}</span>}
          {activeChat?.model && <span className="ch-head__meta">{activeChat.model}</span>}
        </header>
        <SkillsPanel chatId={activeId} filesOn={chat.files.on} t={t} />
        <AddonsPanel chatId={activeId} t={t} />

        <div className="ch-scroll">
          <div className="ch-flow" role="region" aria-label={t('transcript')}>
            {!activeId && (
              <div className="ch-empty">
                <h2 className="ch-empty__title">{t('empty.title')}</h2>
                <p className="ch-empty__body">{t('empty.body')}</p>
                <label className="ch-empty__body" htmlFor="chats-model">{t('model')}</label>
                <ModelSelector id="chats-model" value={model} onChange={setModel} capability={CAP_CHAT} />
                {agents.length > 0 && (
                  <>
                    <label className="ch-empty__body" htmlFor="chats-agent">{t('list.agentPicker')}</label>
                    <select id="chats-agent" className="input" value={agentId} onChange={(e) => setAgentId(e.target.value)}>
                      <option value="">{t('list.noAgent')}</option>
                      {agents.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
                    </select>
                  </>
                )}
              </div>
            )}
            {activeId && <Transcript messages={chat.messages} t={t} />}
            {activeId && <LiveTurn live={chat.live} t={t} />}
            {/* The one live region: it announces that a turn runs or ended, not every streamed word. */}
            <div className="ch-status" role="status" aria-live="polite">
              {chat.running ? t('status.working')
                : activeId && chat.lastTurn === 'failed' ? t('status.failed')
                  : activeId && chat.lastTurn === 'interrupted' ? t('status.interrupted') : ''}
            </div>
            {chat.notes.map((n, i) => (
              <div key={i} className="ch-note">
                {n.kind === 'compacted' ? t('notes.compacted', { before: n.before, after: n.after }) : t('notes.subAgentDone', { title: n.title })}
              </div>
            ))}
          </div>
        </div>

        <div className="ch-composer-wrap">
          {!chat.files.on && (
            <div className="ch-notice" role="note">{chat.files.reason || t('attach.off')}</div>
          )}
          {!chat.persistent && (
            <div className="ch-notice" role="note">{t('memoryNotice')}</div>
          )}
          {shownError && (
            <div className="ch-error" role="alert">
              <span>{shownError}</span>
              <button type="button" className="ch-link" onClick={dismissError}>{t('errors.dismiss')}</button>
            </div>
          )}
          <div className="ch-composer">
            {activeId && <PendingList chat={chat} t={t} />}
            {attached.length > 0 && (
              <ul className="ch-attached" aria-label={t('attach.list')}>
                {attached.map((f, i) => (
                  <li key={`${f.name}-${i}`} className="ch-attached__item" data-testid="attachment">
                    <Icon name="file" />
                    <span className="ch-attached__name">{f.name}</span>
                    <button type="button" className="ch-link" aria-label={t('attach.remove', { name: f.name })}
                      onClick={() => setAttached(a => a.filter((_, j) => j !== i))}>
                      <Icon name="close" />
                    </button>
                  </li>
                ))}
              </ul>
            )}
            <textarea
              className="ch-input"
              aria-label={t('composer.label')}
              placeholder={t('composer.placeholder')}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={onKeyDown}
              rows={2}
            />
            <div className="ch-actions">
              <span className="ch-head__meta">
                {chat.files.on && (
                  <>
                    <button type="button" className="ch-link" disabled={chat.running}
                      aria-label={t('attach.button')} title={chat.running ? t('attach.busy') : t('attach.button')}
                      onClick={() => fileInput.current?.click()}>
                      <Icon name="plus" />
                    </button>
                    <input ref={fileInput} type="file" multiple hidden data-testid="chat-file-input"
                      aria-label={t('attach.button')}
                      onChange={(e) => {
                        // Copy the files before the input is cleared: its list is live.
                        const picked = Array.from(e.target.files)
                        e.target.value = ''
                        setAttached(a => [...a, ...picked])
                      }} />
                  </>
                )}
                {!activeId && (model || pickedAgent?.model) ? (model || pickedAgent.model) : ''}
              </span>
              <span className="ch-actions__right">
                {chat.running && (
                  <button type="button" className="btn btn-secondary" onClick={() => chat.stop(activeId)}>
                    {t('composer.stop')}
                  </button>
                )}
                <button type="button" className="btn btn-primary" disabled={(!activeId && busy) || (!draft.trim() && attached.length === 0) || (!activeId && !model && needsModel)} onClick={submit}>
                  {t('composer.send')}
                </button>
              </span>
            </div>
          </div>
        </div>
      </section>
      <ConfirmDialog
        open={Boolean(toDelete)}
        title={t('confirmDelete.title', { name: toDelete?.title || t('list.untitled') })}
        message={t('confirmDelete.chat')}
        confirmLabel={t('confirmDelete.confirm')}
        pendingLabel={t('confirmDelete.pending')}
        danger
        pending={deleting}
        onConfirm={confirmDelete}
        onCancel={() => setToDelete(null)}
      />
    </div>
  )
}
