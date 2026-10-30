import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { useParams, useOutletContext, useNavigate, useLocation, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { fromState } from '../utils/editorNav'
import { useServerChat } from '../hooks/useServerChat'
import { useChatSwitches } from '../hooks/useChatSwitches'
import { useInbox } from '../hooks/useInbox'
import { highlightAll, enhanceCodeBlocks } from '../utils/markdown'
import { extractCodeArtifacts } from '../utils/artifacts'
import {
  threadRows, streamingParts, lastUserText, chatMarkdown, menuChats, artifactSource, downloadFrom, chatFromExport,
} from '../utils/chatThread'
import { chatFootState, showHint } from '../utils/chatFoot'
import { BROWSER_IMPORT_DISMISSED, browserChatStores, importBrowserChats } from '../utils/browserChats'
import { connectorRef } from '../utils/chatMCP'
// eslint-disable-next-line no-unused-vars
import CanvasPanel from '../components/CanvasPanel'
// eslint-disable-next-line no-unused-vars
import ChatFilesPane from '../components/chat/ChatFilesPane'
import { useChatFiles } from '../hooks/useChatFiles'
import { chatAgentsApi, chatsApi, modelsApi } from '../utils/api'
import { apiUrl } from '../utils/basePath'
import { CAP_CHAT } from '../utils/capabilities'
// eslint-disable-next-line no-unused-vars
import HomeComposer from '../components/home/HomeComposer'
// eslint-disable-next-line no-unused-vars
import HomeModelPicker from '../components/home/HomeModelPicker'
import { useModels } from '../hooks/useModels'
import { useModelFit } from '../hooks/useModelFit'
import { fillStyle, hostMemory, memoryFigure } from '../components/home/memory'
import { gbLabel, gbNumber } from '../utils/modelLedger'
import { CHAT_SLASH_GROUPS, OPEN_CHAT_FILE_EVENT, availableChatActions } from '../components/chat/chatActions'
import { isTyping } from '../components/chat/chatText'
// eslint-disable-next-line no-unused-vars
import ChatHeader from '../components/chat/ChatHeader'
// eslint-disable-next-line no-unused-vars
import ShortcutsDialog from '../components/chat/ShortcutsDialog'
// eslint-disable-next-line no-unused-vars
import ChatSettingsSheet from '../components/chat/ChatSettingsSheet'
// eslint-disable-next-line no-unused-vars
import FindBar from '../components/chat/FindBar'
// eslint-disable-next-line no-unused-vars
import LoadCard from '../components/chat/LoadCard'
// eslint-disable-next-line no-unused-vars
import SkillsMenu from '../components/chat/SkillsMenu'
// eslint-disable-next-line no-unused-vars
import ChatFoot from '../components/chat/ChatFoot'
// eslint-disable-next-line no-unused-vars
import SwitchMenu from '../components/chat/SwitchMenu'
import { CommandsChip, CommandsNotice } from '../components/chat/CommandsNotice'
import { useCommandsNotice } from '../hooks/useCommandsNotice'
// eslint-disable-next-line no-unused-vars
import AgentPicker from '../components/chat/AgentPicker'
// eslint-disable-next-line no-unused-vars
import SubAgentsMenu from '../components/chat/SubAgentsMenu'
// eslint-disable-next-line no-unused-vars
import { EmptyHead, EmptyUnder } from '../components/chat/EmptyChat'
// eslint-disable-next-line no-unused-vars
import ShortcutsRow from '../components/wellknown/ShortcutsRow'
import { isStaleFailure } from '../utils/chatFailure'
// eslint-disable-next-line no-unused-vars
import ChatMessage, { ActivityRow, StreamingTurn, ErrorCard } from '../components/chat/ChatMessage'
// eslint-disable-next-line no-unused-vars
import { AppRow } from '../components/chat/ActivityFold'
import { resumeItems } from '../utils/homeConversations'
import { applyFind, clearFind } from '../components/chat/findInThread'
// eslint-disable-next-line no-unused-vars
import HomeUndoToast from '../components/home/HomeUndoToast'
// eslint-disable-next-line no-unused-vars
import ConfirmDialog from '../components/ConfirmDialog'
// eslint-disable-next-line no-unused-vars
import ChatsMenu from '../components/ChatsMenu'
// eslint-disable-next-line no-unused-vars
import ChatInbox from '../components/ChatInbox'
import { useAuth } from '../context/AuthContext'
// eslint-disable-next-line no-unused-vars
import AddonSlot from '../addons/AddonSlot'
import { useOperations } from '../hooks/useOperations'
import { useLoadedModels } from '../hooks/useLoadedModels'
import { useSandboxStatus } from '../hooks/useSandboxStatus'
import { SANDBOX_ADDON } from '../utils/sandboxStatus'
import { copyToClipboard } from '../utils/clipboard'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import WorkspaceChangeNote from '../components/wellknown/WorkspaceChangeNote'
import { turnChanges, describeChanges, liveTurns } from '../utils/changeNotes'
import { usePages } from '../hooks/usePages'
import { CHAT_UNDO_MS, useChatDelete } from '../hooks/useChatDelete'
import { announceChats } from '../hooks/useRecentChats'
import './chat.css'
import './chats.css'

const FOCUS_MODE_KEY = 'localai_chat_focus_mode'

// The navigation state of the empty chat. /app/chats without state is an
// address and goes to Home (see ChatsRoute in router.jsx); the page keeps a
// state so that clearing a hand-off (a start message, a draft) does not.
const NEW_CHAT = Object.freeze({ newChat: true })

function browserStorage() {
  try { return window.localStorage } catch { return null }
}

// One chat state per account: the chats belong to the signed-in user, so a
// different account gets a fresh mount instead of the previous account's chats.
export default function Chat() {
  const { user } = useAuth()
  return <ChatPage key={user?.id || ''} />
}

// The chat page. The server keeps every chat: its transcript, the messages that
// wait for the model, its tools and its turn, which keeps running when the page
// closes. /app/chats/<id> opens one; /app/chats is the empty chat that a
// hand-off opens (Home's first message, an agent, a draft).
// eslint-disable-next-line no-unused-vars -- used as JSX above
function ChatPage() {
  const { id: routeId } = useParams()
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const location = useLocation()
  const [searchParams] = useSearchParams()
  const { t } = useTranslation('chat')
  const { t: tc } = useTranslation('chats')
  const { t: tw } = useTranslation('workspace')
  const { isAdmin, hasFeature } = useAuth()
  const canAgents = hasFeature('agents')
  const { operations } = useOperations()
  const chat = useServerChat()
  const inbox = useInbox()
  const { select, activeId } = chat

  // An old chat link or the Home page names a model with ?model=; it is a hint
  // for a new chat. An agent chosen on the agents page arrives in the state.
  const [draftModel, setDraftModel] = useState(searchParams.get('model') || '')
  const [agentId, setAgentId] = useState(location.state?.agentId || '')
  const [agents, setAgents] = useState([])
  const [assistantOn, setAssistantOn] = useState(false)
  const [input, setInput] = useState('')
  const [files, setFiles] = useState([])
  const [busy, setBusy] = useState(false)
  const [showSettings, setShowSettings] = useState(false)
  const [modelInfo, setModelInfo] = useState(null)
  // Whether the composer has focus: the status line shows the keyboard hint
  // only then.
  const [composerFocused, setComposerFocused] = useState(false)
  const [find, setFind] = useState({ open: false, query: '', index: 0, count: 0, token: 0 })
  const [canvasMode, setCanvasMode] = useState(false)
  const [canvasOpen, setCanvasOpen] = useState(false)
  // The files pane and the canvas share the right side: one at a time.
  const [filesOpen, setFilesOpen] = useState(false)
  // The file the pane opens at, when a file chip of the transcript asked.
  const [filesFocus, setFilesFocus] = useState(null)
  const [selectedArtifactId, setSelectedArtifactId] = useState(null)
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [renaming, setRenaming] = useState(false)
  const [showShortcuts, setShowShortcuts] = useState(false)
  const [editingPending, setEditingPending] = useState(null) // { id, text }
  // An error of this page (a send or a delete that failed); the hook has its own.
  const [localError, setLocalError] = useState('')
  // The chats the old chat page left in this browser, offered for import once.
  const [browserStores, setBrowserStores] = useState(() => {
    const st = browserStorage()
    try { if (st?.getItem(BROWSER_IMPORT_DISMISSED)) return [] } catch { return [] }
    return browserChatStores(st)
  })
  const importInputRef = useRef(null)
  const pendingArtifactRef = useRef(null)
  const { ids: loadedIds, loaded: loadedModels } = useLoadedModels()
  const messagesEndRef = useRef(null)
  const messagesRef = useRef(null)
  const textareaRef = useRef(null)
  const stickToBottomRef = useRef(true)
  const [scrolledUp, setScrolledUp] = useState(false)
  const chatsMenuRef = useRef(null)
  const pickerRef = useRef(null)
  const { models: chatModels, loading: chatModelsLoading } = useModels(CAP_CHAT)
  const [fitOpenCount, setFitOpenCount] = useState(0)
  const modelNames = useMemo(() => chatModels.map(m => m.id), [chatModels])
  const modelFit = useModelFit({ openCount: fitOpenCount, names: modelNames })
  const onPickerOpen = useCallback(() => setFitOpenCount(n => n + 1), [])

  // The address names the open chat. Opening one by id selects it, unless it is
  // already open (creating a chat selects it before the address changes).
  useEffect(() => {
    const want = routeId || null
    // Opening a chat marks its notifications read on the server: read the inbox again after.
    if (want !== activeId) Promise.resolve(select(want)).finally(() => inbox.refresh())
  // activeId is read, not a trigger: only a change of address selects.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [routeId])

  // Agents and the assistant switch are best effort: a server without them
  // leaves the picker and the /assistant action out.
  useEffect(() => {
    let live = true
    if (canAgents) chatAgentsApi.list().then((r) => { if (live) setAgents(r.agents || []) }).catch(() => { if (live) setAgents([]) })
    fetch(apiUrl('/api/features')).then(r => r.json()).then(d => { if (live) setAssistantOn(!!d.localai_assistant) }).catch(() => {})
    return () => { live = false }
  }, [canAgents])

  const activeChat = activeId ? (chat.detail || chat.chats.find(c => c.id === activeId) || null) : null
  const model = activeChat ? activeChat.model : draftModel
  const pickedAgent = agents.find((a) => a.id === (activeChat ? activeChat.agent_id : agentId))
  const switches = useChatSwitches(activeId, chat.files.on)
  const chatFiles = useChatFiles(chat.files.on ? activeId : null, chat.ticks.files)
  const sandbox = useSandboxStatus(switches.addons.some(a => a.id === SANDBOX_ADDON), activeId)
  const commands = useCommandsNotice(switches.commands.state, switches.commands.reason, isAdmin)
  // What the Tools picker says under an add-on: the Sandbox's state.
  const addonNote = (a) => {
    if (a.id !== SANDBOX_ADDON || !sandbox || sandbox.state === 'unknown') return {}
    if (sandbox.state === 'cannot') return { usable: false, reason: sandbox.reason ? tc('tools.sandbox.cannotWhy', { reason: sandbox.reason }) : tc('tools.sandbox.cannot') }
    if (sandbox.state === 'host') return { note: tc('tools.sandbox.host'), warn: true }
    return { note: tc('tools.sandbox.isolated', { backend: sandbox.backend }) }
  }
  const messages = chat.messages
  const hasThread = messages.some(m => m.role === 'user' || m.role === 'assistant')
  const modelWarm = !!model && loadedIds.has(model)
  const title = activeChat?.title || tc('list.untitled')

  // The model of a new chat comes from the agent when it names one.
  const effectiveModel = activeChat ? activeChat.model : (pickedAgent?.model || draftModel)

  const slashConfig = useMemo(() => ({
    actions: availableChatActions({ assistant: isAdmin && assistantOn, hasModels: chatModels.length > 0, hasThread, hasChat: !!activeId }),
    groups: CHAT_SLASH_GROUPS,
    label: (a) => t(`slash.${a.id}.label`),
    desc: (a) => t(`slash.${a.id}.desc`),
    groupLabel: (g) => t(`slash.group.${g}`),
  }), [isAdmin, assistantOn, chatModels.length, hasThread, activeId, t])

  // What the model is waiting for while it comes up: a byte transfer this
  // frontend is doing for it. A model that is simply not loaded yet has no
  // phases, so the card says only that.
  const loadProgress = useMemo(() => {
    if (!chat.running || !model) return null
    const op = operations.find(o => o.taskType === 'staging' && o.name === model)
    if (!op) return null
    return {
      label: op.nodeName ? t('streaming.transferringTo', { node: op.nodeName }) : t('streaming.transferring'),
      progress: op.progress || 0,
      detail: op.message || '',
    }
  }, [operations, chat.running, model, t])

  // Focus mode: once a conversation has a message we slim the surrounding
  // chrome (collapse the global app rail). Esc gives the user back the full
  // chrome for the rest of this chat. The settings sheet offers an opt-out.
  const [focusOverride, setFocusOverride] = useState(false)
  const [focusModeEnabled, setFocusModeEnabled] = useState(() => {
    try { return localStorage.getItem(FOCUS_MODE_KEY) !== 'false' } catch { return true }
  })
  const focusActive = focusModeEnabled && hasThread && !focusOverride
  const prevAppCollapseRef = useRef(null)
  const toggleFocusMode = (next) => {
    setFocusModeEnabled(next)
    try { localStorage.setItem(FOCUS_MODE_KEY, String(next)) } catch { /* private mode */ }
  }

  // What the composer slot of add-ons is told: the conversation and whether
  // sending is blocked. Never the draft or the history.
  const composerSlotContext = useMemo(() => ({ conversationId: activeId || undefined, disabled: chat.running }), [activeId, chat.running])

  const artifacts = useMemo(
    () => (canvasMode ? extractCodeArtifacts(artifactSource(messages), 'role', 'assistant') : []),
    [messages, canvasMode],
  )
  const prevArtifactCountRef = useRef(0)
  // eslint-disable-next-line react-hooks/exhaustive-deps -- reset per chat only
  useEffect(() => { prevArtifactCountRef.current = artifacts.length }, [activeId])
  useEffect(() => {
    if (artifacts.length > prevArtifactCountRef.current && artifacts.length > 0) {
      // A block opened from its own Canvas button is the one to show.
      setSelectedArtifactId(pendingArtifactRef.current || artifacts[artifacts.length - 1].id)
      pendingArtifactRef.current = null
      if (!canvasOpen) { setCanvasOpen(true); setFilesOpen(false) }
    }
    prevArtifactCountRef.current = artifacts.length
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [artifacts])

  useEffect(() => {
    const on = (e) => { if (e.target === textareaRef.current) setComposerFocused(e.type === 'focusin') }
    document.addEventListener('focusin', on)
    document.addEventListener('focusout', on)
    return () => {
      document.removeEventListener('focusin', on)
      document.removeEventListener('focusout', on)
    }
  }, [])

  // Admins read the model config for the model info of the settings sheet.
  useEffect(() => {
    if (!isAdmin || !model) { setModelInfo(null); return undefined }
    let cancelled = false
    modelsApi.getConfigJson(model).then(cfg => { if (!cancelled) setModelInfo(cfg) }).catch(() => { if (!cancelled) setModelInfo(null) })
    return () => { cancelled = true }
  }, [model, isAdmin])

  // Track whether the user is pinned to the bottom. If they scroll up while a
  // reply streams, stop forcing them back down.
  useEffect(() => {
    const el = messagesRef.current
    if (!el) return undefined
    const onScroll = () => {
      const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight
      stickToBottomRef.current = distanceFromBottom < 80
      setScrolledUp(distanceFromBottom > 160)
    }
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [])

  useEffect(() => {
    if (!stickToBottomRef.current) return
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, chat.live, chat.pending.length])

  // Switching chats snaps to the bottom and resets the focus-mode override.
  useEffect(() => {
    stickToBottomRef.current = true
    setScrolledUp(false)
    messagesEndRef.current?.scrollIntoView({ behavior: 'auto' })
    setFocusOverride(false)
    setRenaming(false)
    setEditingPending(null)
  }, [activeId])

  // Collapse the global app rail when a conversation begins, and restore it
  // when the user goes back to an empty chat or overrides focus with Esc.
  useEffect(() => {
    if (focusActive) {
      if (prevAppCollapseRef.current === null) {
        try {
          prevAppCollapseRef.current = localStorage.getItem('localai_sidebar_collapsed') === 'true'
        } catch { prevAppCollapseRef.current = false }
      }
      window.dispatchEvent(new CustomEvent('sidebar-collapse', { detail: { collapsed: true } }))
    } else if (prevAppCollapseRef.current !== null) {
      window.dispatchEvent(new CustomEvent('sidebar-collapse', { detail: { collapsed: prevAppCollapseRef.current } }))
      prevAppCollapseRef.current = null
    }
  }, [focusActive])

  // Global keys: Cmd/Ctrl+K opens the chats menu and Cmd/Ctrl+Shift+F searches
  // this chat. Esc stops a running turn, else closes the search, else closes
  // the canvas; it also exits focus mode. None of that fires while a menu or
  // dialog is open: those take their own Esc first.
  const escapeRef = useRef({})
  escapeRef.current = { running: chat.running, stop: () => chat.stop(activeId), findOpen: find.open, canvasOpen, filesOpen }
  useEffect(() => {
    const onKey = (e) => {
      const isMod = e.metaKey || e.ctrlKey
      if (isMod && (e.key === 'k' || e.key === 'K')) {
        e.preventDefault()
        chatsMenuRef.current?.toggle()
        return
      }
      if (isMod && e.shiftKey && (e.key === 'f' || e.key === 'F')) {
        e.preventDefault()
        setFind(f => ({ ...f, open: true, token: f.token + 1 }))
        return
      }
      // "/" from anywhere that is not a text field starts a command, as on Home.
      if (e.key === '/' && !isMod && !e.altKey && !isTyping(document.activeElement) && !document.querySelector('[role="dialog"], [role="alertdialog"]')) {
        const el = textareaRef.current
        if (el) {
          e.preventDefault()
          setInput('/')
          el.focus()
        }
        return
      }
      const overlay = document.querySelector('.home-menu, .cx-menu, .dk-cmdlist, .chat-mcp-dropdown-menu, [role="dialog"], [role="alertdialog"]')
      // An Escape that a popover or a sheet already used to close itself must
      // not also stop the turn.
      if (e.key === 'Escape' && !overlay && !e.defaultPrevented) {
        const cur = escapeRef.current
        if (cur.running) cur.stop()
        else if (cur.findOpen) setFind(f => ({ ...f, open: false, query: '', index: 0, count: 0 }))
        else if (cur.canvasOpen) setCanvasOpen(false)
        else if (cur.filesOpen) setFilesOpen(false)
      }
      if (e.key === 'Escape' && focusActive) setFocusOverride(true)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [focusActive])

  // Find in chat: mark the matches in the thread, then show the current one.
  useEffect(() => {
    const root = messagesRef.current
    if (!root) return
    if (!find.open || !find.query) {
      clearFind(root)
      setFind(f => (f.count === 0 ? f : { ...f, count: 0 }))
      return
    }
    const marks = applyFind(root, find.query)
    setFind(f => (f.count === marks.length ? f : { ...f, count: marks.length, index: Math.min(f.index, Math.max(0, marks.length - 1)) }))
  }, [find.open, find.query, messages, canvasMode, chat.running])

  useEffect(() => {
    const root = messagesRef.current
    if (!root) return
    root.querySelectorAll('mark.cx-hit').forEach((mark, i) => {
      if (i === find.index) {
        mark.setAttribute('data-cur', '')
        mark.scrollIntoView({ block: 'center' })
      } else {
        mark.removeAttribute('data-cur')
      }
    })
  }, [find.index, find.count, find.query])

  // Highlight code blocks and add per-block copy and Canvas buttons. The
  // observer is disconnected while we mutate so our own edits don't retrigger it.
  useEffect(() => {
    const el = messagesRef.current
    if (!el) return undefined
    let obs
    const labels = {
      copyLabel: t('actions.copy'),
      canvasLabel: canvasMode ? undefined : t('input.canvasLabel'),
      selector: '.cx-prose pre:not([data-enhanced])',
    }
    const run = () => {
      obs?.disconnect()
      highlightAll(el)
      enhanceCodeBlocks(el, labels)
      obs?.observe(el, { childList: true, subtree: true })
    }
    obs = new MutationObserver(run)
    run()
    return () => obs.disconnect()
  }, [activeId, canvasMode, t])

  // Event delegation for artifact cards and the Canvas button on a code block.
  useEffect(() => {
    const el = messagesRef.current
    if (!el) return undefined
    const handler = (e) => {
      const canvasBtn = e.target.closest('.code-canvas-btn')
      if (canvasBtn) {
        const text = canvasBtn.closest('.code-block')?.querySelector('pre code')?.textContent || ''
        const at = Number(canvasBtn.closest('[data-index]')?.dataset.index)
        const all = extractCodeArtifacts(artifactSource(messages), 'role', 'assistant')
        const same = (a) => a.code.trim() === text.trim()
        const match = all.find(a => a.messageIndex === at && same(a)) || all.find(same)
        if (!match) return
        pendingArtifactRef.current = match.id
        setSelectedArtifactId(match.id)
        setCanvasMode(true)
        setCanvasOpen(true)
        return
      }
      if (!canvasMode) return
      const openBtn = e.target.closest('.artifact-card-open')
      const downloadBtn = e.target.closest('.artifact-card-download')
      const card = e.target.closest('.artifact-card')
      if (downloadBtn) {
        e.stopPropagation()
        const artifact = artifacts.find(a => a.id === downloadBtn.dataset.artifactId)
        if (artifact?.code) {
          const blob = new Blob([artifact.code], { type: 'text/plain' })
          const url = URL.createObjectURL(blob)
          const a = document.createElement('a')
          a.href = url
          a.download = artifact.title || 'download.txt'
          a.click()
          URL.revokeObjectURL(url)
        }
        return
      }
      if (openBtn || card) {
        const id = (openBtn || card).dataset.artifactId
        if (id) {
          setSelectedArtifactId(id)
          setCanvasOpen(true)
        }
      }
    }
    el.addEventListener('click', handler)
    return () => el.removeEventListener('click', handler)
  }, [canvasMode, artifacts, messages])

  // Files go to the server with the message that starts a turn and are saved in
  // the chat's folder of the workspace.
  const attachFiles = useCallback((_kind, list) => {
    setFiles(prev => [...prev, ...list])
  }, [])

  const handlePaste = useCallback((e) => {
    if (!chat.files.on) return
    const items = e.clipboardData?.items
    if (!items) return
    const images = Array.from(items)
      .filter(item => item.kind === 'file' && item.type.startsWith('image/'))
      .map(item => item.getAsFile())
      .filter(Boolean)
    if (images.length === 0) return
    // A pasted image attaches as a file rather than inserting into the text.
    // Clipboard images arrive unnamed or as "image.png"; give each a unique name.
    e.preventDefault()
    const stamp = Date.now()
    const named = images.map((file, i) => {
      const name = (file.name && file.name !== 'image.png')
        ? file.name
        : `pasted-image-${stamp}-${i + 1}.${(file.type.split('/')[1] || 'png').replace('+xml', '')}`
      return new File([file], name, { type: file.type })
    })
    setFiles(prev => [...prev, ...named])
  }, [chat.files.on])

  // Sends the composer. The message that starts a chat creates it first. In a
  // chat that exists a message always goes: while a turn runs it waits and
  // reaches the model at its next step.
  const handleSend = useCallback(async (override) => {
    const text = (typeof override === 'string' ? override : input).trim()
    const sending = typeof override === 'string' ? [] : files
    if ((!text && sending.length === 0) || (!activeId && busy)) return
    if (!activeId && !effectiveModel) {
      addToast(t('toasts.selectModel'), 'warning')
      return
    }
    if (typeof override !== 'string') {
      setInput('')
      setFiles([])
    }
    setLocalError('')
    try {
      let target = activeId
      if (!target) {
        setBusy(true)
        target = await chat.createChat(draftModel, agentId)
        await select(target)
        navigate(`/app/chats/${target}`)
      }
      stickToBottomRef.current = true
      await chat.send(target, text, sending)
    } catch (e) {
      // Give the text and files back, unless the user typed something else.
      if (typeof override !== 'string') {
        setInput(d => d || text)
        setFiles(f => (f.length ? f : sending))
      }
      setLocalError(e.message || tc('errors.send'))
    } finally {
      setBusy(false)
    }
  }, [input, files, activeId, busy, effectiveModel, draftModel, agentId, chat, select, navigate, addToast, t, tc])

  // The Home page hands over the first message in the location state. It goes
  // once, when a model is known; without one the text waits in the box. A
  // draft only fills the box.
  const startRef = useRef(location.state?.start || '')
  const draftRef = useRef(location.state?.draft || '')
  useEffect(() => {
    if (startRef.current) setInput(startRef.current)
    else if (draftRef.current) { setInput(draftRef.current); textareaRef.current?.focus() }
  }, [])
  // A draft (for example "Ask the assistant to change this" on a page) only
  // fills the box: the person finishes the sentence and sends it.
  useEffect(() => {
    const draft = location.state?.draft
    if (typeof draft !== 'string' || !draft) return
    setInput(d => d || draft)
    navigate(`${location.pathname}${location.search}`, { replace: true, state: NEW_CHAT })
  }, [location.state?.draft]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    const start = startRef.current
    if (!start || input !== start || activeId || busy || !effectiveModel) return
    startRef.current = ''
    // Clear the state first: Back must not send the message again.
    navigate(`${location.pathname}${location.search}`, { replace: true, state: NEW_CHAT })
    handleSend()
  }, [input, activeId, busy, effectiveModel, handleSend, navigate, location.pathname, location.search])

  // A shortcut fills the composer, as the starters did; one marked send goes
  // at once when the chat can take a message.
  const pickShortcut = useCallback((s) => {
    if (s.send && effectiveModel && !(!activeId && busy)) {
      handleSend(s.prompt)
      return
    }
    setInput(s.prompt)
    textareaRef.current?.focus()
  }, [effectiveModel, activeId, busy, handleSend])

  // Regenerate answers the last message again: the server removes what the
  // model said after it and starts a new turn.
  const handleRegenerate = useCallback(() => {
    if (activeId) chat.regenerate(activeId)
  }, [activeId, chat])

  // Branch copies the chat up to and including the line into a new chat, which opens.
  const handleFork = useCallback(async (seq) => {
    if (!activeId) return
    try {
      const forked = await chatsApi.fork(activeId, seq)
      await chat.refreshList()
      navigate(`/app/chats/${encodeURIComponent(forked.id)}`)
      addToast(t('toasts.branched'), 'success', 2500)
    } catch (e) {
      addToast(e.message, 'error')
    }
  }, [activeId, chat, navigate, addToast, t])

  // Up in an empty box edits the last waiting message, or puts your last
  // message back in the box to send again.
  const onComposerKey = (e) => {
    if (e.key !== 'ArrowUp' || input || e.shiftKey || e.metaKey || e.ctrlKey || e.altKey) return false
    if (!activeId) return false
    const last = chat.pending[chat.pending.length - 1]
    if (last) {
      e.preventDefault()
      setEditingPending({ id: last.id, text: last.text })
      return true
    }
    const text = lastUserText(messages)
    if (!text) return false
    e.preventDefault()
    setInput(text)
    return true
  }

  const copyText = async (text) => {
    const ok = await copyToClipboard(text || '')
    addToast(ok ? t('toasts.copied') : t('toasts.copyFailed'), ok ? 'success' : 'error', ok ? 2000 : 3000)
  }

  // The transcript of another chat is read when it is exported from the menu.
  const transcriptOf = async (id) => (id === activeId ? messages : (await chatsApi.messages(id)).messages || [])
  const serverChat = (id) => (id === activeId && chat.detail) || chat.chats.find(c => c.id === id) || { id, title: '' }
  const exportChat = (id, format = 'markdown') => downloadFrom(chatsApi.exportUrl(id, format))
  // A JSON export (GET .../export?format=json) becomes a new chat. The file
  // keeps its model unless this server does not have it: then the chat gets
  // the model picked here.
  const importChatFile = async (e) => {
    const file = e.target.files?.[0]
    e.target.value = ''
    if (!file) return
    try {
      const exported = chatFromExport(await file.text())
      if (!exported) throw new Error(t('toasts.notAChat'))
      const known = chatModels.some(m => m.id === exported.model)
      const conv = await chatsApi.importChat(exported, known ? '' : effectiveModel)
      await chat.refreshList()
      navigate(`/app/chats/${encodeURIComponent(conv.id)}`)
      addToast(t('toasts.imported', { title: conv.title || tc('list.untitled') }), 'success', 2500)
    } catch (err) {
      addToast(t('toasts.importFailed', { message: err.message }), 'error')
    }
  }
  const runBrowserImport = async () => {
    const st = browserStorage()
    try {
      const total = await importBrowserChats(st, browserStores, (raw) => chatsApi.importBrowser(raw))
      addToast(t('browserImport.done', total), total.failed > 0 ? 'warning' : 'success', 4000)
      await chat.refreshList()
    } catch (err) {
      addToast(t('browserImport.failed', { message: err.message }), 'error')
    }
    setBrowserStores(browserChatStores(st))
  }
  const dismissBrowserImport = () => {
    try { browserStorage()?.setItem(BROWSER_IMPORT_DISMISSED, '1') } catch { /* private mode: asked again next time */ }
    setBrowserStores([])
  }
  const copyChat = async (id) => {
    try {
      const ok = await copyToClipboard(chatMarkdown(serverChat(id), await transcriptOf(id)))
      addToast(ok ? t('toasts.chatCopied') : t('toasts.copyFailed'), ok ? 'success' : 'error', ok ? 2000 : 3000)
    } catch (e) {
      addToast(e.message, 'error')
    }
  }

  // Deleting a chat is final when its undo time ends. Until then only its row
  // is hidden; if it was the open one, the empty chat opens in its place, so
  // the offer stays on screen (leaving the page would end it at once).
  const deletes = useChatDelete({
    remove: chat.remove,
    // A chat the server would not delete comes back, with the reason.
    onFailed: (e) => {
      setLocalError(e.message || tc('errors.delete'))
      chat.refreshList()
    },
  })
  const pendingDelete = deletes.pending
  const commitDelete = deletes.commit

  const requestDelete = (c) => {
    const wasActive = c.id === activeId
    deletes.request({ id: c.id, name: c.name || c.title || tc('list.untitled'), wasActive })
    if (wasActive) navigate('/app/chats', { state: NEW_CHAT })
  }
  const undoDelete = () => {
    const pending = deletes.undo()
    if (pending?.wasActive) navigate(`/app/chats/${pending.id}`)
  }

  const visibleChats = useMemo(
    () => menuChats(chat.chats).filter(c => c.id !== pendingDelete?.id),
    [chat.chats, pendingDelete],
  )
  // The sidebar's recent chats follow this list.
  useEffect(() => { announceChats(visibleChats) }, [visibleChats])

  const promptDeleteAll = () => setConfirmDialog({
    title: t('deleteAllDialog.title'),
    message: t('deleteAllDialog.message'),
    confirmLabel: t('deleteAllDialog.confirm'),
    danger: true,
    onConfirm: async () => {
      setConfirmDialog(null)
      for (const c of visibleChats) {
        try { await chat.remove(c.id) } catch (e) { addToast(e.message || tc('errors.delete'), 'error'); break }
      }
      navigate('/app')
    },
  })

  const promptDelete = () => activeChat && requestDelete({ id: activeChat.id, name: title })

  // Picking another model in a chat moves the chat to it from the next turn.
  // The server refuses while a turn runs (409) and says why.
  const pickModel = async (next) => {
    if (!activeId) { setDraftModel(next); return }
    if (!next || next === activeChat?.model) return
    try {
      await chat.update(activeId, { model: next })
      addToast(t('toasts.modelChanged', { model: next }), 'success', 2500)
    } catch (e) {
      addToast(e.message, 'error')
    }
  }

  const openAssistant = async () => {
    const m = effectiveModel || chatModels[0]?.id
    if (!m) { addToast(t('toasts.selectModel'), 'warning'); return }
    try {
      const conv = await chatsApi.create(m, t('assistant.chatTitle'), '', true)
      navigate(`/app/chats/${encodeURIComponent(conv.id)}`)
    } catch (e) {
      addToast(e.message, 'error')
    }
  }

  // The message component is memoised, so it gets one stable set of actions
  // that always call the latest handlers.
  const actionsRef = useRef(null)
  actionsRef.current = { copyText, handleRegenerate, handleFork, edit: (seq, text) => chat.editMessage(activeId, seq, text) }
  const messageActions = useMemo(() => ({
    copy: (text) => actionsRef.current.copyText(text),
    edit: (seq, text) => actionsRef.current.edit(seq, text),
    fork: (seq) => actionsRef.current.handleFork(seq),
    regenerate: (seq) => actionsRef.current.handleRegenerate(seq),
    focusRelative: (index, delta) => {
      const els = Array.from(messagesRef.current?.querySelectorAll('[data-testid="chat-message"]') || [])
      const at = els.findIndex(el => Number(el.dataset.index) === index)
      const next = els[at + delta]
      if (next) { next.focus(); next.scrollIntoView({ block: 'nearest' }) }
    },
  }), [])

  const rows = useMemo(() => threadRows(messages, chat.live), [messages, chat.live])
  // Only the last answer can be answered again (the server cuts after the last
  // message you sent).
  const lastAnswerKey = useMemo(() => {
    for (let i = rows.length - 1; i >= 0; i--) if (rows[i].kind === 'msg' && rows[i].msg.role === 'assistant') return rows[i].key
    return ''
  }, [rows])
  // What each turn changed in the well-known files and the pages: a note under
  // the last answer of the turn. Turns that end while this tab watches are
  // live and may raise a toast; a reload or a reopened chat shows the notes only.
  const { pages: pageList } = usePages(true)
  const pageTitle = useCallback((name) => pageList.find(p => p.name === name)?.title || name, [pageList])
  const changeByTurn = useMemo(() => {
    const out = new Map()
    const pagesSeen = new Set()
    for (const [turn, changes] of [...turnChanges(messages)].sort((a, b) => a[0] - b[0])) out.set(turn, describeChanges(changes, pagesSeen))
    return out
  }, [messages])
  const noteKeyByTurn = useMemo(() => {
    const out = new Map()
    for (const r of rows) if (r.kind === 'msg' && r.msg.role === 'assistant') out.set(r.msg.turn, r.key)
    return out
  }, [rows])
  const [baseTurn, setBaseTurn] = useState({ id: null, turn: null })
  useEffect(() => {
    if (!activeId || chat.loading || chat.detail?.id !== activeId || baseTurn.id === activeId) return
    const last = messages.reduce((n, m) => Math.max(n, m.turn || 0), 0)
    setBaseTurn({ id: activeId, turn: chat.running ? Math.max(0, last - 1) : last })
  }, [activeId, chat.loading, chat.detail, chat.running, messages, baseTurn.id])
  const live = useMemo(() => liveTurns(baseTurn.id === activeId ? baseTurn.turn : null, messages), [baseTurn, activeId, messages])
  const changeNoteFor = (row) => {
    if (row.kind !== 'msg' || row.msg.role !== 'assistant' || noteKeyByTurn.get(row.msg.turn) !== row.key) return null
    const desc = changeByTurn.get(row.msg.turn)
    if (!desc) return null
    return <WorkspaceChangeNote key={`wc-${row.msg.turn}`} desc={desc} live={live.has(row.msg.turn)} addToast={addToast} pageTitle={pageTitle} />
  }
  // A file chip of a step opens the Files pane at its file.
  const filesOn = chat.files.on
  useEffect(() => {
    if (!filesOn) return undefined
    const on = (e) => {
      const path = e.detail?.path
      if (!path) return
      setCanvasOpen(false)
      setFilesOpen(true)
      setFilesFocus(f => ({ path, n: (f?.n || 0) + 1 }))
    }
    window.addEventListener(OPEN_CHAT_FILE_EVENT, on)
    return () => window.removeEventListener(OPEN_CHAT_FILE_EVENT, on)
  }, [filesOn])
  useEffect(() => { setFilesFocus(null) }, [activeId])

  const streaming = streamingParts(chat.live)
  const sendLive = useCallback((text) => activeId && chat.send(activeId, text), [activeId, chat])

  const openFind = () => setFind(f => ({ ...f, open: true, token: f.token + 1 }))
  const closeFind = () => {
    clearFind(messagesRef.current)
    setFind(f => ({ ...f, open: false, query: '', index: 0, count: 0 }))
    textareaRef.current?.focus()
  }
  const stepFind = (delta) => setFind(f => (f.count === 0 ? f : { ...f, index: (f.index + delta + f.count) % f.count }))

  const isEmpty = !activeId || (messages.length === 0 && !chat.running && !chat.loading)
  const noModel = !activeId && !chatModelsLoading && chatModels.length === 0 && !pickedAgent?.model
  const conversations = isEmpty ? resumeItems(chat.chats.filter(c => c.id !== activeId && c.id !== pendingDelete?.id)).slice(0, 8) : []
  const manage = Boolean(activeChat?.assistant)

  const toggleCanvasMode = () => {
    const next = !canvasMode
    setCanvasMode(next)
    if (!next) setCanvasOpen(false)
  }

  // What a row of the model list says: whether it is loaded, what it can do
  // and, where the server can estimate it, how it fits this machine.
  const describeModel = (name, warm) => {
    const vision = chatModels.find(m => m.id === name)?.capabilities?.includes('FLAG_VISION')
    const reading = modelFit.reading(name)
    let fit = null
    if (warm) fit = { tone: 'ok', text: t('picker.readyNow') }
    else if (reading?.fit) {
      const f = reading.fit
      fit = f.state === 'fits'
        ? { tone: 'ok', text: t('picker.fits', { amount: gbNumber(f.amount) }) }
        : f.state === 'spill'
          ? { tone: 'warn', text: t('picker.spill', { amount: gbNumber(f.amount) }) }
          : { tone: 'err', text: t('picker.over', { amount: gbNumber(f.amount) }) }
    }
    return { vision: !!vision, size: reading?.bytes ? gbLabel(reading.bytes) : null, fit }
  }
  const memory = hostMemory(modelFit.resources)
  const memoryNumbers = memory ? memoryFigure(memory.used, memory.total) : null
  const picker = (
    <HomeModelPicker
      ref={pickerRef}
      value={effectiveModel}
      onChange={pickModel}
      capability={CAP_CHAT}
      models={chatModels}
      loading={chatModelsLoading}
      loadedIds={loadedIds}
      grouped
      describe={describeModel}
      onOpen={onPickerOpen}
      disabled={!activeId && !!pickedAgent?.model}
      autoSelect={!activeId && !pickedAgent?.model}
      rememberSelection={!activeId}
      footer={memoryNumbers && (
        <div className="home-menu__foot" data-testid="chat-model-memory">
          <span>{memory.isGpu ? t('picker.gpuMemory') : t('picker.memory')}</span>
          <span className="cx-ctx__bar" aria-hidden="true"><i style={fillStyle(memory.pct)} /></span>
          <span>{t('picker.memoryUsed', { used: memoryNumbers.used, total: memoryNumbers.total, unit: memoryNumbers.unit })}</span>
        </div>
      )}
    />
  )

  // The tools and skills of an open chat, each with its switch. A new chat gets
  // them when it starts; an agent's chat lists only what the agent grants.
  const toolGroups = [
    {
      // The tools LocalAI serves itself, such as web search: one switch per group.
      key: 'builtins', title: tc('tools.builtins'),
      rows: switches.builtins.map((b) => ({
        key: `b-${b.group}`, label: tc(`tools.builtin.${b.group}`, { defaultValue: b.group }),
        title: tc('tools.builtinBy', { tools: (b.tools || []).join(', ') }),
        on: !b.off, onToggle: (on) => switches.toggleBuiltin(b.group, on),
      })),
    },
    {
      key: 'addons', title: tc('tools.addons'),
      rows: switches.addons.map((a) => ({ key: `a-${a.id}`, label: a.id, title: tc('addons.tools', { count: a.tools }), on: !a.off, onToggle: (on) => switches.toggleAddon(a.id, on), ...addonNote(a) })),
    },
    {
      key: 'servers', title: tc('tools.servers'),
      rows: switches.servers.map((s) => ({ key: `s-${s.name}`, label: s.name, on: !s.off, usable: s.usable, reason: s.reason, onToggle: (on) => switches.toggleMCP(s.name, on) })),
    },
    {
      key: 'connectors', title: tc('tools.connectors'),
      rows: switches.connectors.map((c) => ({ key: `c-${c.id}`, label: c.name, on: !c.off, usable: c.usable, reason: c.reason, onToggle: (on) => switches.toggleMCP(connectorRef(c.id), on) })),
    },
  ]
  const tools = activeId && (
    <>
      <SwitchMenu
        icon="plug" label={t('tools.label')} title={t('tools.title')} heading={t('tools.heading')} note={t('tools.note')}
        groups={toolGroups} error={switches.error} testId="chat-tools" notice={commands.info ? <CommandsNotice notice={commands} /> : null}
        footer={switches.connectors.length > 0 || switches.servers.length > 0
          ? <button type="button" className="chat-mcp-select-all chat-switch-manage" onClick={() => navigate('/app/mcp-connectors')}>{tc('tools.manage')}</button>
          : null}
      />
      <CommandsChip notice={commands} />
      <SkillsMenu skills={switches.skills} onSet={switches.setSkillsOn} error={switches.skillsError} />
      <SubAgentsMenu chatId={activeId} tick={chat.ticks.subAgents} />
    </>
  )

  const composerChips = (
    <>
      <AgentPicker agents={agents} value={activeChat ? activeChat.agent_id : agentId} onChange={setAgentId} readOnly={!!activeId} />
      <span className="cx-chipset">
        <button
          type="button"
          className="home-chip cx-chip-toggle"
          aria-pressed={canvasMode}
          onClick={toggleCanvasMode}
          title={t('input.canvasTitle')}
          data-testid="chat-canvas-chip"
        >
          <Icon name="columns" />
          <span className="home-chip__text">{t('input.canvasLabel')}</span>
        </button>
        {canvasMode && artifacts.length > 0 && !canvasOpen && (
          <button
            type="button"
            className="home-chip cx-chip-count"
            title={t('input.openCanvas')}
            aria-label={t('input.openCanvas')}
            onClick={() => { setSelectedArtifactId(artifacts[0]?.id); setCanvasOpen(true); setFilesOpen(false) }}
          >
            {artifacts.length}
          </button>
        )}
      </span>
      {activeId && chat.files.on && (
        <button
          type="button"
          className="home-chip cx-chip-toggle"
          aria-pressed={filesOpen}
          aria-controls={filesOpen ? 'chat-files-pane' : undefined}
          onClick={() => { const next = !filesOpen; setFilesOpen(next); if (next) setCanvasOpen(false) }}
          title={t('files.chipTitle')}
          data-testid="chat-files-chip"
        >
          <Icon name="paperclip" />
          <span className="home-chip__text">{t('files.chip')}</span>
          {/* Keyed by the number, so a new count rises into place. */}
          {chatFiles.files.length > 0 && <span key={chatFiles.files.length} className="cx-files__count" data-testid="chat-files-count">{chatFiles.files.length}</span>}
        </button>
      )}
    </>
  )

  const moreItems = [
    { key: 'new', icon: 'plus', label: t('menu.newChat'), onClick: () => navigate('/app') },
    { key: 'rename', icon: 'pencil', label: t('menu.rename'), hidden: !activeId, onClick: () => setRenaming(true) },
    { key: 'copy', icon: 'clipboard', label: t('menu.copyChat'), hidden: !hasThread, onClick: () => copyChat(activeId) },
    { key: 'export', icon: 'export', label: t('menu.exportMarkdown'), hidden: !hasThread, onClick: () => exportChat(activeId) },
    { key: 'export-json', icon: 'download', label: t('menu.exportJson'), hidden: !hasThread, onClick: () => exportChat(activeId, 'json') },
    { key: 'import', icon: 'import', label: t('menu.importChat'), onClick: () => importInputRef.current?.click() },
    { key: 'info', icon: 'info', label: t('header.modelInfo'), hidden: !(model && isAdmin), onClick: () => setShowSettings(true) },
    { key: 'keys', icon: 'keyboard', label: t('shortcuts.title'), onClick: () => setShowShortcuts(true) },
    { divider: true },
    { key: 'delete', icon: 'trash', label: t('menu.deleteChat'), danger: true, hidden: !activeId, onClick: promptDelete },
  ]

  const runSlash = (id) => {
    switch (id) {
      case 'model': pickerRef.current?.open(); break
      case 'new': navigate('/app'); break
      case 'chats': chatsMenuRef.current?.open(); break
      case 'assistant': openAssistant(); break
      case 'canvas': toggleCanvasMode(); break
      case 'settings': setShowSettings(true); break
      case 'find': openFind(); break
      case 'export': if (activeId) exportChat(activeId); break
      case 'delete': promptDelete(); break
      default: break
    }
  }

  const status = chat.running ? tc('status.working')
    : activeId && chat.lastTurn === 'interrupted' ? tc('status.interrupted')
      : activeId && chat.lastTurn === 'stopped' ? tc('status.stopped') : ''
  // The failed turn shows as the one banner in the thread, and the footer says
  // only the state word. A failure the server reports without a reason (the
  // page was reloaded) fades after a day, so an old failure does not look new.
  const showFailure = Boolean(activeId) && !chat.running && chat.lastTurn === 'failed'
    && (Boolean(chat.failure) || !isStaleFailure(chat.detail?.updated))
  // The one state of the status line, and whether the keyboard hint shows:
  // only while the composer has focus, is empty and the chat is idle.
  const footState = chatFootState({
    activeId, noModel, running: chat.running, sending: busy, lastTurn: chat.lastTurn,
    pending: chat.pending, live: chat.live, warm: modelWarm,
  })
  const footHint = showHint({ focused: composerFocused, empty: !input.trim() && files.length === 0, state: footState })
  const footModel = chat.usage?.model || model
  const footBackend = loadedModels.find(m => m.id === footModel)?.backend || ''
  const placeholder = effectiveModel
    ? (chat.running ? t('input.placeholderQueue') : t('input.placeholderModel', { model: effectiveModel }))
    : (noModel ? t('input.placeholderNoModel') : t('input.placeholderPick'))

  return (
    <div className="cx-page" data-testid="chats-page">
      <div className="cx-conv" data-empty={isEmpty || undefined}>
        <ChatHeader
          historyMenu={(
            <ChatsMenu
              ref={chatsMenuRef}
              chats={visibleChats}
              activeChatId={activeId}
              onSelect={(id) => navigate(`/app/chats/${id}`)}
              onNew={() => navigate('/app')}
              onDelete={requestDelete}
              onDeleteAll={promptDeleteAll}
              onRename={(id, name) => chat.rename(id, name)}
              onExport={(c) => exportChat(c.id)}
              onCopyChat={(c) => copyChat(c.id)}
              onSearch={(text) => chatsApi.search(text, 50)}
            />
          )}
          manageMode={manage}
          name={activeId ? title : t('menu.newChat')}
          canRename={!!activeId}
          onRename={(name) => chat.rename(activeId, name)}
          renaming={renaming}
          setRenaming={setRenaming}
          onFind={hasThread ? openFind : undefined}
          findOpen={find.open}
          onSettings={() => setShowSettings(v => !v)}
          settingsOpen={showSettings}
          moreItems={moreItems}
          extra={<ChatInbox inbox={inbox} />}
        />

        {find.open && (
          <FindBar
            query={find.query}
            index={find.index}
            count={find.count}
            focusToken={find.token}
            onQuery={(query) => setFind(f => ({ ...f, query, index: 0 }))}
            onStep={stepFind}
            onClose={closeFind}
          />
        )}

        <div className="cx-stage">
          <div className="cx-body" ref={messagesRef}>
            {isEmpty && <EmptyHead name={activeId ? title : (pickedAgent?.name || '')} manage={manage} />}
            <div className="cx-thread" data-testid="chat-thread" role="region" aria-label={tc('transcript')} hidden={isEmpty}>
              {rows.map((row, i) => {
                if (row.kind === 'activity') return <ActivityRow key={row.key} id={`cx-act-${row.key}`} items={row.items} proposalTick={chat.ticks.proposals} />
                if (row.kind === 'app') return <AppRow key={row.key} chatId={activeId} row={row} onSend={sendLive} />
                return [
                  <ChatMessage
                    key={row.key}
                    msg={row.msg}
                    isLast={i === rows.length - 1}
                    model={model}
                    warm={modelWarm}
                    canvasMode={canvasMode}
                    busy={chat.running}
                    activityItems={row.activity}
                    actions={messageActions}
                    conversationId={activeId}
                    canRegenerate={!chat.running && row.key === lastAnswerKey}
                    proposalTick={chat.ticks.proposals}
                  />,
                  changeNoteFor(row),
                ]
              })}

              {(chat.running || streaming.text || streaming.reasoning || streaming.tools.length > 0) && (
                <StreamingTurn
                  model={model}
                  warm={modelWarm}
                  content={streaming.text}
                  reasoning={streaming.reasoning}
                  toolCalls={streaming.tools}
                  waiting={(loadProgress || !modelWarm) ? (
                    <LoadCard model={model} progress={loadProgress} />
                  ) : (
                    <span className="cx-dots" aria-label={t('streaming.waiting')}><span /><span /><span /></span>
                  )}
                />
              )}

              {showFailure && (
                <ErrorCard
                  failure={chat.failure}
                  isAdmin={isAdmin}
                  kept={false}
                  canRetry={!!lastUserText(messages)}
                  onRetry={handleRegenerate}
                />
              )}

              {chat.notes.map((n, i) => (
                <p key={i} className="cx-note" data-testid="chat-note">
                  {n.kind === 'compacted' ? tc('notes.compacted', { before: n.before, after: n.after }) : tc('notes.subAgentDone', { title: n.title })}
                </p>
              ))}
            </div>
            <div ref={messagesEndRef} />
          </div>
          {scrolledUp && (
            <button
              type="button"
              className="cx-jump"
              data-testid="chat-jump-latest"
              onClick={() => {
                stickToBottomRef.current = true
                setScrolledUp(false)
                messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
              }}
            >
              <Icon name="arrow-down" /> {t('actions.jumpToLatest')}
            </button>
          )}
        </div>

        {/* Dock: the Home command bar under the thread, with the waiting messages over it. */}
        <div className="cx-dock">
          {/* The toast stack stays above the whole dock, so a toast never
              covers the shortcut row it talks about. */}
          <div className="cx-dock__in" data-toast-anchor="">
            {!chat.persistent && <p className="cx-notice" role="note">{tc('memoryNotice')}</p>}
            {!chat.files.on && <p className="cx-notice" role="note">{chat.files.reason || tc('attach.off')}</p>}
            {browserStores.length > 0 && (
              <p className="cx-notice" role="note" data-testid="browser-import">
                <span>{t('browserImport.found', { count: browserStores.reduce((n, s) => n + s.count, 0) })}</span>
                <span className="cx-pending__acts">
                  <button type="button" className="cx-link" onClick={runBrowserImport}>{t('browserImport.import')}</button>
                  <button type="button" className="cx-link" onClick={dismissBrowserImport}>{t('browserImport.dismiss')}</button>
                </span>
              </p>
            )}
            {(localError || (chat.error && !(chat.lastTurn === 'failed' && !chat.running))) && (
              <p className="cx-notice cx-notice--error" role="alert">
                <span>{localError || chat.error}</span>
                <button type="button" className="cx-link" onClick={() => { setLocalError(''); chat.clearError() }}>{tc('errors.dismiss')}</button>
              </p>
            )}
            {activeId && chat.pending.length > 0 && (
              <PendingList
                chat={chat}
                editing={editingPending}
                setEditing={setEditingPending}
                t={tc}
              />
            )}
            {!noModel && (
              <ShortcutsRow
                starters={manage ? t('empty.suggestionsManage', { returnObjects: true }) : undefined}
                filesOn={chat.files.on && !manage}
                onPick={pickShortcut}
                onEditWithAssistant={manage ? undefined : () => { setInput(tw('wellKnown.shortcuts.assistantPrompt')); textareaRef.current?.focus() }}
              />
            )}
            <HomeComposer
              onDictateError={(m) => addToast(m, 'error')}
              message={input}
              onMessage={setInput}
              onSubmit={handleSend}
              canSend={!!effectiveModel && !(!activeId && busy) && (input.trim().length > 0 || files.length > 0)}
              sending={busy}
              sendTitle={effectiveModel ? t('input.send') : t('input.selectModelFirst')}
              textareaRef={textareaRef}
              picker={picker}
              mcp={tools}
              chips={composerChips}
              extras={<AddonSlot slot="chat.composer.actions" context={composerSlotContext} />}
              files={files}
              onRemoveFile={(f) => setFiles(prev => prev.filter(x => x !== f))}
              onAttach={chat.files.on ? attachFiles : undefined}
              attachTitle={chat.running ? tc('attach.queued') : undefined}
              fileTestId="chat-file-input"
              placeholder={placeholder}
              slash={slashConfig}
              onRunAction={runSlash}
              streaming={chat.running}
              queueWhileStreaming
              onStop={() => chat.stop(activeId)}
              stopTitle={t('input.stopGenerating')}
              onPaste={handlePaste}
              onKeyDownExtra={onComposerKey}
              fileAccept="*/*"
              rows={1}
              strictEnter
              testId="chat-composer"
              textareaTestId="chat-input"
              sendId="chat-submit-btn"
              sendTestId="chat-send"
            />
            <ChatFoot
              state={footState}
              status={status}
              model={footModel}
              backend={footBackend}
              hint={footHint}
              chat={chat}
              showMeter={!!activeId}
              quiet={!activeId}
            />
          </div>
        </div>
        {isEmpty && (
          <div className="cx-under" data-testid="chat-under">
            <EmptyUnder
              noModel={noModel}
              isAdmin={isAdmin}
              addToast={addToast}
              onInstallStarted={() => setFitOpenCount(n => n)}
              manage={manage}
              model={effectiveModel}
              warm={modelWarm}
              conversations={conversations}
              onResume={(conv) => navigate(`/app/chats/${conv.id}`)}
            />
          </div>
        )}
      </div>
      {filesOpen && activeId && chat.files.on && !(canvasOpen && artifacts.length > 0) && (
        <ChatFilesPane
          state={chatFiles}
          chatId={activeId}
          focus={filesFocus}
          onClose={() => setFilesOpen(false)}
          onDropFiles={(list) => attachFiles('file', list)}
        />
      )}
      {canvasOpen && artifacts.length > 0 && (
        <CanvasPanel
          artifacts={artifacts}
          selectedId={selectedArtifactId}
          onSelect={setSelectedArtifactId}
          onClose={() => setCanvasOpen(false)}
        />
      )}
      {showSettings && (
        <ChatSettingsSheet
          chat={activeChat ? { ...activeChat, model } : { model }}
          agent={pickedAgent}
          isAdmin={isAdmin}
          focusMode={focusModeEnabled}
          onFocusMode={toggleFocusMode}
          modelInfo={modelInfo}
          onEditConfig={() => navigate(`/app/model-editor/${encodeURIComponent(model)}`, { state: fromState(location, 'Chat') })}
          onEditAgent={() => navigate(`/app/agents/${encodeURIComponent(pickedAgent.id)}/edit`)}
          onDelete={activeId ? () => { setShowSettings(false); promptDelete() } : undefined}
          onSaveSettings={activeId && activeChat?.kind !== 'subagent' ? (settings) => chat.update(activeId, { settings }) : undefined}
          onClose={() => setShowSettings(false)}
        />
      )}
      {showShortcuts && <ShortcutsDialog onClose={() => setShowShortcuts(false)} canFind />}
      <input ref={importInputRef} type="file" accept=".json,application/json" hidden onChange={importChatFile} data-testid="chat-import-input" />
      {pendingDelete && (
        <HomeUndoToast
          key={pendingDelete.id}
          message={t('menu.deleted', { title: pendingDelete.name })}
          onUndo={undoDelete}
          onExpire={commitDelete}
          duration={CHAT_UNDO_MS}
          undoLabel={t('menu.undo')}
          dismissLabel={t('menu.dismiss')}
          testId="chat-undo-toast"
        />
      )}
      <ConfirmDialog
        open={!!confirmDialog}
        title={confirmDialog?.title}
        message={confirmDialog?.message}
        confirmLabel={confirmDialog?.confirmLabel}
        danger={confirmDialog?.danger}
        onConfirm={confirmDialog?.onConfirm}
        onCancel={() => setConfirmDialog(null)}
      />
    </div>
  )
}

// The messages that wait for the model, over the composer. While a turn runs
// they reach the model together at its next step; after a Stop or a failed
// turn they wait for Send now. Each can be changed or cancelled until then.
// eslint-disable-next-line no-unused-vars -- used as JSX above
function PendingList({ chat, editing, setEditing, t }) {
  const { pending, running, lastTurn, activeId } = chat
  const waiting = !running
  const save = () => { chat.editPending(activeId, editing.id, editing.text); setEditing(null) }
  return (
    <div className="cx-pending" data-testid="pending-list">
      <div className="cx-pending__head">
        <span>{t('pending.header', { count: pending.length })}{running ? ` · ${t('pending.note')}` : ''}</span>
        {waiting && (
          <button type="button" className="cx-btn cx-btn--primary" onClick={() => chat.sendNow(activeId)}>
            {t('composer.sendNow')}
          </button>
        )}
      </div>
      {waiting && lastTurn !== 'done' && lastTurn !== '' && (
        <p className="cx-pending__why">{lastTurn === 'stopped' ? t('pending.afterStop') : t('pending.afterFailure')}</p>
      )}
      {pending.map((p) => (
        <div key={p.id} className="cx-pending__row" data-testid="pending-row">
          {editing?.id === p.id ? (
            <>
              <input
                className="cx-pending__edit"
                aria-label={t('pending.edit')}
                value={editing.text}
                onChange={(e) => setEditing({ id: p.id, text: e.target.value })}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') { e.preventDefault(); save() }
                  if (e.key === 'Escape') { e.stopPropagation(); setEditing(null) }
                }}
                autoFocus
              />
              <span className="cx-pending__acts">
                <button type="button" className="cx-link" onClick={save}>{t('pending.save')}</button>
                <button type="button" className="cx-link" onClick={() => setEditing(null)}>{t('pending.discard')}</button>
              </span>
            </>
          ) : (
            <>
              <span className="cx-pending__text">{p.text}</span>
              <span className="cx-pending__acts">
                <button type="button" className="cx-link" onClick={() => setEditing({ id: p.id, text: p.text })}>{t('pending.edit')}</button>
                <button type="button" className="cx-link cx-link--danger" onClick={() => chat.cancelPending(activeId, p.id)}>{t('pending.cancel')}</button>
              </span>
            </>
          )}
        </div>
      ))}
    </div>
  )
}
