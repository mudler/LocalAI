import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { useParams, useOutletContext, useNavigate, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { fromState } from '../utils/editorNav'
import { useChat } from '../hooks/useChat'
import { renderMarkdown, highlightAll, enhanceCodeBlocks } from '../utils/markdown'
import { extractCodeArtifacts, renderMarkdownWithArtifacts } from '../utils/artifacts'
// eslint-disable-next-line no-unused-vars
import CanvasPanel from '../components/CanvasPanel'
// eslint-disable-next-line no-unused-vars
import { fileToBase64, modelsApi, mcpApi } from '../utils/api'
import { readAttachmentText } from '../utils/pdf'
import { CAP_CHAT } from '../utils/capabilities'
import { useMCPClient } from '../hooks/useMCPClient'
// eslint-disable-next-line no-unused-vars
import UnifiedMCPDropdown from '../components/UnifiedMCPDropdown'
// eslint-disable-next-line no-unused-vars
import HomeComposer from '../components/home/HomeComposer'
// eslint-disable-next-line no-unused-vars
import HomeModelPicker from '../components/home/HomeModelPicker'
import { useModels } from '../hooks/useModels'
import { useModelFit } from '../hooks/useModelFit'
import { fillStyle, hostMemory, memoryFigure } from '../components/home/memory'
import { gbLabel, gbNumber } from '../utils/modelLedger'
import { CHAT_SLASH_GROUPS, availableChatActions } from '../components/chat/chatActions'
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
import { EmptyHead, EmptyUnder } from '../components/chat/EmptyChat'
import { conversationsFromChats } from '../utils/homeConversations'
import { applyFind, clearFind } from '../components/chat/findInThread'
// eslint-disable-next-line no-unused-vars
import HomeUndoToast from '../components/home/HomeUndoToast'
import { loadClientMCPServers } from '../utils/mcpClientStorage'
// eslint-disable-next-line no-unused-vars
import ConfirmDialog from '../components/ConfirmDialog'
// eslint-disable-next-line no-unused-vars
import ChatsMenu from '../components/ChatsMenu'
import { useAuth } from '../context/AuthContext'
import { useOperations } from '../hooks/useOperations'
import { useLoadedModels } from '../hooks/useLoadedModels'
import { copyToClipboard } from '../utils/clipboard'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import Lightbox from '../components/Lightbox'
// eslint-disable-next-line no-unused-vars
import ChatMessage, { ActivityRow, StreamingTurn } from '../components/chat/ChatMessage'
import { editableMessageText, withEditedMessageText, isActivityRole } from '../components/chat/chatText'
import './chat.css'

const FOCUS_MODE_KEY = 'localai_chat_focus_mode'

function serializeChatAsMarkdown(chat) {
  let md = `# ${chat.name}\n\n`
  md += `Model: ${chat.model || 'Unknown'}\n`
  md += `Date: ${new Date(chat.createdAt).toLocaleString()}\n\n---\n\n`
  for (const msg of chat.history) {
    if (msg.role === 'user') {
      const text = typeof msg.content === 'string' ? msg.content : msg.content?.[0]?.text || ''
      md += `## User\n\n${text}\n\n`
    } else if (msg.role === 'assistant') {
      md += `## Assistant\n\n${msg.content}\n\n`
    } else if (msg.role === 'thinking' || msg.role === 'reasoning') {
      md += `<details><summary>Thinking</summary>\n\n${msg.content}\n\n</details>\n\n`
    }
  }
  return md
}

function downloadChatAsMarkdown(chat) {
  const blob = new Blob([serializeChatAsMarkdown(chat)], { type: 'text/markdown' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${chat.name.replace(/[^a-zA-Z0-9]/g, '_')}.md`
  a.click()
  URL.revokeObjectURL(url)
}

// formatLoadEta renders the server's remaining-seconds estimate. The server
// omits it entirely until its observed transfer rate is meaningful, so anything
// arriving here is worth showing.
function formatLoadEta(seconds) {
  if (!Number.isFinite(seconds) || seconds <= 0) return ''
  if (seconds < 60) return `${Math.round(seconds)}s`
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes} min`
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`
}

export default function Chat() {
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const location = useLocation()
  const { t } = useTranslation('chat')
  const { isAdmin } = useAuth()
  const { operations } = useOperations()
  const {
    chats, activeChat, activeChatId, isStreaming, streamingChatId, streamingContent,
    streamingReasoning, streamingToolCalls, tokensPerSecond, maxTokensPerSecond, modelLoading,
    addChat, forkChat, switchChat, deleteChat, deleteAllChats, renameChat, updateChatSettings,
    sendMessage, stopGeneration, clearHistory, getContextUsagePercent, addMessage,
  } = useChat(urlModel || '')

  // Detect active staging operation for the current chat's model
  const stagingOp = useMemo(() => {
    if (!isStreaming || !activeChat?.model) return null
    return operations.find(op => op.taskType === 'staging' && op.name === activeChat.model) || null
  }, [operations, isStreaming, activeChat?.model])

  // What to show instead of the thinking dots while the model is not up yet.
  // The load job wins over the staging operation: it is authoritative across
  // frontend replicas and names the phase (installing / staging / loading),
  // where the operation only knows about a byte transfer this replica is
  // performing. The operation stays as the fallback for a transfer with no job
  // attached to this request (a reconciler scale-up, for instance).
  const loadProgress = useMemo(() => {
    if (modelLoading) {
      const eta = formatLoadEta(modelLoading.eta_seconds)
      return {
        label: t(`streaming.modelState.${modelLoading.state}`, t('streaming.transferring'))
          + (modelLoading.node ? ` ${t('streaming.onNode', { node: modelLoading.node })}` : ''),
        progress: modelLoading.progress || 0,
        detail: eta ? t('streaming.eta', { value: eta }) : '',
        sent: modelLoading.bytes_sent || 0,
        total: modelLoading.total_bytes || 0,
      }
    }
    if (stagingOp) {
      return {
        label: stagingOp.nodeName
          ? t('streaming.transferringTo', { node: stagingOp.nodeName })
          : t('streaming.transferring'),
        progress: stagingOp.progress || 0,
        detail: stagingOp.message || '',
      }
    }
    return null
  }, [modelLoading, stagingOp, t])

  const [input, setInput] = useState('')
  const [files, setFiles] = useState([])
  const [showSettings, setShowSettings] = useState(false)
  const [mcpAvailable, setMcpAvailable] = useState(false)
  const [mcpServerList, setMcpServerList] = useState([])
  const [mcpServersLoading, setMcpServersLoading] = useState(false)
  const [mcpServerListError, setMcpServerListError] = useState('')
  const [mcpPromptList, setMcpPromptList] = useState([])
  const [mcpPromptsLoading, setMcpPromptsLoading] = useState(false)
  const [mcpPromptArgsDialog, setMcpPromptArgsDialog] = useState(null)
  const [mcpPromptArgsValues, setMcpPromptArgsValues] = useState({})
  const [mcpResourceList, setMcpResourceList] = useState([])
  const [mcpResourcesLoading, setMcpResourcesLoading] = useState(false)
  const [modelInfo, setModelInfo] = useState(null)
  const [find, setFind] = useState({ open: false, query: '', index: 0, count: 0, token: 0 })
  const [canvasMode, setCanvasMode] = useState(false)
  const [canvasOpen, setCanvasOpen] = useState(false)
  const [selectedArtifactId, setSelectedArtifactId] = useState(null)
  const [clientMCPServers, setClientMCPServers] = useState(() => loadClientMCPServers())
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [lightbox, setLightbox] = useState(null)
  const [renaming, setRenaming] = useState(false)
  const [showShortcuts, setShowShortcuts] = useState(false)
  const [pendingDelete, setPendingDelete] = useState(null)
  const pendingDeleteRef = useRef(null)
  const [editingMessageIndex, setEditingMessageIndex] = useState(null)
  const [messageEditDraft, setMessageEditDraft] = useState('')
  const pendingArtifactRef = useRef(null)
  const { ids: loadedIds } = useLoadedModels()
  const {
    connect: mcpConnect, disconnect: mcpDisconnect, disconnectAll: mcpDisconnectAll,
    getToolsForLLM, isClientTool, executeTool, connectionStatuses, getConnectedTools,
    hasAppUI, getAppResource, getClientForTool, getToolDefinition,
  } = useMCPClient()
  const messagesEndRef = useRef(null)
  const fileInputRef = useRef(null)
  const messagesRef = useRef(null)
  const textareaRef = useRef(null)
  const stickToBottomRef = useRef(true)
  const [scrolledUp, setScrolledUp] = useState(false)
  const chatsMenuRef = useRef(null)
  const pickerRef = useRef(null)
  const { models: chatModels, loading: chatModelsLoading } = useModels(CAP_CHAT)
  const [fitOpenCount, setFitOpenCount] = useState(0)
  const modelNames = useMemo(() => chatModels.map(m => m.id), [chatModels])
  const modelFit = useModelFit({ openCount: fitOpenCount, names: modelNames, contextSize: activeChat?.contextSize })
  const onPickerOpen = useCallback(() => setFitOpenCount(n => n + 1), [])
  const hasThread = (activeChat?.history?.length || 0) > 0
  const slashConfig = useMemo(() => ({
    actions: availableChatActions({ isAdmin, hasModels: chatModels.length > 0, hasThread }),
    groups: CHAT_SLASH_GROUPS,
    label: (a) => t(`slash.${a.id}.label`),
    desc: (a) => t(`slash.${a.id}.desc`),
    groupLabel: (g) => t(`slash.group.${g}`),
  }), [isAdmin, chatModels.length, hasThread, t])

  // Focus mode: once a conversation has at least one message we slim the
  // surrounding chrome (collapse the global app rail, fade non-essential
  // header items). Esc gives the user back the full chrome for the rest of
  // this session. The settings drawer offers a persistent opt-out.
  const isInConversation = (activeChat?.history?.length || 0) > 0
  const [focusOverride, setFocusOverride] = useState(false)
  const [focusModeEnabled, setFocusModeEnabled] = useState(() => {
    try { return localStorage.getItem(FOCUS_MODE_KEY) !== 'false' } catch (_) { return true }
  })
  const focusActive = focusModeEnabled && isInConversation && !focusOverride
  const prevAppCollapseRef = useRef(null)

  const toggleFocusMode = (next) => {
    setFocusModeEnabled(next)
    try { localStorage.setItem(FOCUS_MODE_KEY, String(next)) } catch (_) {}
  }

  const artifacts = useMemo(
    () => canvasMode ? extractCodeArtifacts(activeChat?.history, 'role', 'assistant') : [],
    [activeChat?.history, canvasMode]
  )
  const modelWarm = !!activeChat?.model && loadedIds.has(activeChat.model)

  const prevArtifactCountRef = useRef(0)
  useEffect(() => {
    prevArtifactCountRef.current = artifacts.length
  }, [activeChat?.id])
  useEffect(() => {
    if (artifacts.length > prevArtifactCountRef.current && artifacts.length > 0) {
      // A block opened from its own Canvas button is the one to show.
      setSelectedArtifactId(pendingArtifactRef.current || artifacts[artifacts.length - 1].id)
      pendingArtifactRef.current = null
      if (!canvasOpen) setCanvasOpen(true)
    }
    prevArtifactCountRef.current = artifacts.length
  }, [artifacts])

  // Check MCP availability and fetch model config (admin-only endpoint)
  useEffect(() => {
    const model = activeChat?.model
    if (!model || !isAdmin) { setMcpAvailable(false); setModelInfo(null); return }
    let cancelled = false
    modelsApi.getConfigJson(model).then(cfg => {
      if (cancelled) return
      setModelInfo(cfg)
      if (cfg?.context_size > 0 && activeChat) {
        updateChatSettings(activeChat.id, { contextSize: cfg.context_size })
      }
      const hasMcp = !!(cfg?.mcp?.remote || cfg?.mcp?.stdio)
      setMcpAvailable(hasMcp)
      if (!hasMcp && activeChat?.mcpMode) {
        updateChatSettings(activeChat.id, { mcpMode: false, mcpServers: [] })
      }
    }).catch(() => { if (!cancelled) { setMcpAvailable(false); setModelInfo(null) } })
    return () => { cancelled = true }
  }, [activeChat?.model, isAdmin])

  const fetchMcpServers = useCallback(async () => {
    const model = activeChat?.model
    if (!model) return
    setMcpServersLoading(true)
    setMcpServerListError('')
    try {
      const data = await mcpApi.listServers(model)
      const servers = data?.servers || []
      setMcpServerList(servers)

      // A previously selected server may become unavailable between requests.
      // Remove it from request metadata while leaving it visible with its error.
      if (activeChat) {
        const unavailable = new Set(servers.filter(server => server.error).map(server => server.name))
        const current = activeChat.mcpServers || []
        const availableSelection = current.filter(name => !unavailable.has(name))
        if (availableSelection.length !== current.length) {
          updateChatSettings(activeChat.id, { mcpServers: availableSelection })
        }
      }
    } catch (e) {
      setMcpServerList([])
      setMcpServerListError(e.body?.message || e.message || 'Failed to discover MCP servers')
    } finally {
      setMcpServersLoading(false)
    }
  }, [activeChat, updateChatSettings])

  const toggleMcpServer = useCallback((serverName) => {
    if (!activeChat) return
    const current = activeChat.mcpServers || []
    const next = current.includes(serverName)
      ? current.filter(s => s !== serverName)
      : [...current, serverName]
    updateChatSettings(activeChat.id, { mcpServers: next })
  }, [activeChat, updateChatSettings])

  const fetchMcpPrompts = useCallback(async () => {
    const model = activeChat?.model
    if (!model) return
    setMcpPromptsLoading(true)
    try {
      const data = await mcpApi.listPrompts(model)
      setMcpPromptList(Array.isArray(data) ? data : [])
    } catch (_e) {
      setMcpPromptList([])
    } finally {
      setMcpPromptsLoading(false)
    }
  }, [activeChat?.model])

  const fetchMcpResources = useCallback(async () => {
    const model = activeChat?.model
    if (!model) return
    setMcpResourcesLoading(true)
    try {
      const data = await mcpApi.listResources(model)
      setMcpResourceList(Array.isArray(data) ? data : [])
    } catch (_e) {
      setMcpResourceList([])
    } finally {
      setMcpResourcesLoading(false)
    }
  }, [activeChat?.model])

  const handleSelectPrompt = useCallback(async (prompt) => {
    if (prompt.arguments && prompt.arguments.length > 0) {
      setMcpPromptArgsDialog(prompt)
      setMcpPromptArgsValues({})
      return
    }
    // No arguments, expand immediately
    const model = activeChat?.model
    if (!model) return
    try {
      const result = await mcpApi.getPrompt(model, prompt.name, {})
      if (result?.messages) {
        for (const msg of result.messages) {
          addMessage(activeChat.id, { role: msg.role || 'user', content: msg.content })
        }
      }
    } catch (e) {
      addMessage(activeChat.id, { role: 'system', content: `Failed to expand prompt: ${e.message}` })
    }

  }, [activeChat?.model, activeChat?.id, addMessage])

  const handleExpandPromptWithArgs = useCallback(async () => {
    if (!mcpPromptArgsDialog) return
    const model = activeChat?.model
    if (!model) return
    try {
      const result = await mcpApi.getPrompt(model, mcpPromptArgsDialog.name, mcpPromptArgsValues)
      if (result?.messages) {
        for (const msg of result.messages) {
          addMessage(activeChat.id, { role: msg.role || 'user', content: msg.content })
        }
      }
    } catch (e) {
      addMessage(activeChat.id, { role: 'system', content: `Failed to expand prompt: ${e.message}` })
    }
    setMcpPromptArgsDialog(null)
    setMcpPromptArgsValues({})

  }, [activeChat?.model, activeChat?.id, mcpPromptArgsDialog, mcpPromptArgsValues, addMessage])

  const toggleMcpResource = useCallback((uri) => {
    if (!activeChat) return
    const current = activeChat.mcpResources || []
    const next = current.includes(uri)
      ? current.filter(u => u !== uri)
      : [...current, uri]
    updateChatSettings(activeChat.id, { mcpResources: next })
  }, [activeChat, updateChatSettings])

  // Auto-connect/disconnect client MCP servers based on chat's active list
  const activeMCPIds = activeChat?.clientMCPServers || []
  useEffect(() => {
    const activeSet = new Set(activeMCPIds)
    for (const server of clientMCPServers) {
      const status = connectionStatuses[server.id]?.status
      if (activeSet.has(server.id) && status !== 'connected' && status !== 'connecting') {
        mcpConnect(server)
      } else if (!activeSet.has(server.id) && (status === 'connected' || status === 'connecting')) {
        mcpDisconnect(server.id)
      }
    }
  }, [activeMCPIds.join(','), clientMCPServers])

  const handleClientMCPServerAdded = useCallback((server) => {
    setClientMCPServers(loadClientMCPServers())
    const current = activeChat?.clientMCPServers || []
    if (activeChat) updateChatSettings(activeChat.id, { clientMCPServers: [...current, server.id] })
  }, [activeChat, updateChatSettings])

  const handleClientMCPServerRemoved = useCallback(async (id) => {
    await mcpDisconnect(id)
    setClientMCPServers(loadClientMCPServers())
    if (activeChat) {
      const current = activeChat.clientMCPServers || []
      updateChatSettings(activeChat.id, { clientMCPServers: current.filter(s => s !== id) })
    }
  }, [activeChat, mcpDisconnect, updateChatSettings])

  const handleClientMCPToggle = useCallback((serverId) => {
    if (!activeChat) return
    const current = activeChat.clientMCPServers || []
    const next = current.includes(serverId) ? current.filter(s => s !== serverId) : [...current, serverId]
    updateChatSettings(activeChat.id, { clientMCPServers: next })
  }, [activeChat, updateChatSettings])

  const startMessageEdit = useCallback((index, message) => {
    const text = editableMessageText(message)
    if (text === null) return
    setEditingMessageIndex(index)
    setMessageEditDraft(text)
  }, [])

  const cancelMessageEdit = useCallback(() => {
    setEditingMessageIndex(null)
    setMessageEditDraft('')
  }, [])

  const saveMessageEdit = useCallback(() => {
    if (!activeChat || isStreaming || editingMessageIndex === null || !messageEditDraft.trim()) return
    const message = activeChat.history[editingMessageIndex]
    if (!message || editableMessageText(message) === null) return
    const history = activeChat.history.map((item, index) =>
      index === editingMessageIndex ? withEditedMessageText(item, messageEditDraft) : item
    )
    updateChatSettings(activeChat.id, { history })
    cancelMessageEdit()
  }, [activeChat, isStreaming, editingMessageIndex, messageEditDraft, updateChatSettings, cancelMessageEdit])

  useEffect(() => {
    cancelMessageEdit()
  }, [activeChat?.id, isStreaming, cancelMessageEdit])

  // Load initial message from home page
  const homeDataProcessed = useRef(false)
  useEffect(() => {
    if (homeDataProcessed.current) return
    const stored = localStorage.getItem('localai_index_chat_data')
    if (stored) {
      homeDataProcessed.current = true
      try {
        const data = JSON.parse(stored)
        localStorage.removeItem('localai_index_chat_data')

        // Three entry shapes from Home:
        //   - "compose-and-send": data.message present → open new chat,
        //     prefill the composer, click submit.
        //   - "open-assistant": no message, just data.localaiAssistant → open
        //     a fresh chat already in admin mode so the wizard can fire.
        //   - "new-chat": no message, data.newChat only → open an empty chat
        //     on the chosen model (the /new action on the command bar).
        const hasMessage = !!data.message
        const wantsAssistant = !!data.localaiAssistant
        const wantsNewChat = !!data.newChat

        if (hasMessage || wantsAssistant || wantsNewChat) {
          let targetChat = activeChat
          if (data.newChat) {
            targetChat = addChat(data.model || '', '', data.mcpMode || false)
          } else {
            if (data.model && activeChat) {
              updateChatSettings(activeChat.id, { model: data.model })
            }
            if (data.mcpMode && activeChat) {
              updateChatSettings(activeChat.id, { mcpMode: true })
            }
          }
          if (data.mcpServers?.length > 0 && targetChat) {
            updateChatSettings(targetChat.id, { mcpServers: data.mcpServers })
          }
          if (data.clientMCPServers?.length > 0 && targetChat) {
            updateChatSettings(targetChat.id, { clientMCPServers: data.clientMCPServers })
          }
          if (wantsAssistant && targetChat) {
            updateChatSettings(targetChat.id, { localaiAssistant: true })
          }
          if (hasMessage) {
            setInput(data.message)
            if (data.files) setFiles(data.files)
            setTimeout(() => {
              const submitBtn = document.getElementById('chat-submit-btn')
              submitBtn?.click()
            }, 100)
          }
        }
      } catch (_e) { /* ignore */ }
    }
  }, [])

  // Track whether the user is pinned to the bottom. If they scroll up
  // while a response is streaming, stop forcing them back down.
  useEffect(() => {
    const el = messagesRef.current
    if (!el) return
    const onScroll = () => {
      const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight
      stickToBottomRef.current = distanceFromBottom < 80
      setScrolledUp(distanceFromBottom > 160)
    }
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [])

  // Auto-scroll only when the user hasn't scrolled away from the bottom.
  useEffect(() => {
    if (!stickToBottomRef.current) return
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [activeChat?.history, streamingContent, streamingReasoning, streamingToolCalls])

  // When switching chats, snap to bottom and re-pin. Also reset the
  // user's focus-mode override — each chat starts fresh.
  useEffect(() => {
    stickToBottomRef.current = true
    setScrolledUp(false)
    messagesEndRef.current?.scrollIntoView({ behavior: 'auto' })
    setFocusOverride(false)
  }, [activeChat?.id])

  // Auto-collapse the global app rail when a conversation begins, and
  // restore the previous collapsed state when the user goes back to an
  // empty chat (or overrides focus with Esc). We feed into the existing
  // sidebar-collapse event bus so App.jsx needs no awareness of focus mode.
  useEffect(() => {
    if (focusActive) {
      if (prevAppCollapseRef.current === null) {
        try {
          prevAppCollapseRef.current = localStorage.getItem('localai_sidebar_collapsed') === 'true'
        } catch (_) { prevAppCollapseRef.current = false }
      }
      window.dispatchEvent(new CustomEvent('sidebar-collapse', { detail: { collapsed: true } }))
    } else if (prevAppCollapseRef.current !== null) {
      window.dispatchEvent(new CustomEvent('sidebar-collapse', { detail: { collapsed: prevAppCollapseRef.current } }))
      prevAppCollapseRef.current = null
    }
  }, [focusActive])

  // Global keybindings: Cmd/Ctrl+K opens the chats menu and Cmd/Ctrl+Shift+F
  // searches this chat. Esc stops a reply that is streaming, else closes the
  // search, else closes the canvas; it also exits focus mode while that is
  // engaged. None of that fires while a menu or dialog is open: those take
  // their own Esc first.
  const escapeRef = useRef({})
  escapeRef.current = { streaming: isStreaming, stop: stopGeneration, findOpen: find.open, canvasOpen }
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
      const overlay = document.querySelector('.home-menu, .cx-menu, .dk-cmdlist, [role="dialog"], [role="alertdialog"]')
      if (e.key === 'Escape' && !overlay) {
        const cur = escapeRef.current
        if (cur.streaming) cur.stop()
        else if (cur.findOpen) setFind(f => ({ ...f, open: false, query: '', index: 0, count: 0 }))
        else if (cur.canvasOpen) setCanvasOpen(false)
      }
      if (e.key === 'Escape' && focusActive) {
        // Don't fight the chats menu / settings sheet / dialogs: they
        // each handle their own Esc and stop propagation when open.
        setFocusOverride(true)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [focusActive])

  // Find in chat: mark the matches in the thread, then show the current one.
  // The search is the browser's own, over what this page has loaded.
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
  }, [find.open, find.query, activeChat?.history, canvasMode, isStreaming])

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

  // Highlight code blocks + add per-block copy buttons. A MutationObserver on
  // the messages container is more reliable than render-keyed effects: it fires
  // for loaded/switched chats AND for streaming token updates, regardless of
  // render timing. The observer is disconnected while we mutate so our own
  // highlight/enhance edits don't retrigger it.
  useEffect(() => {
    const el = messagesRef.current
    if (!el) return
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
  }, [activeChat?.id, canvasMode, t])

  // Auto-grow textarea
  const autoGrowTextarea = useCallback(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 200) + 'px'
  }, [])

  useEffect(() => {
    autoGrowTextarea()
  }, [input, autoGrowTextarea])

  // Event delegation for artifact cards and the Canvas button on a code block.
  useEffect(() => {
    const el = messagesRef.current
    if (!el) return
    const handler = (e) => {
      const canvasBtn = e.target.closest('.code-canvas-btn')
      if (canvasBtn) {
        const text = canvasBtn.closest('.code-block')?.querySelector('pre code')?.textContent || ''
        const at = Number(canvasBtn.closest('[data-index]')?.dataset.index)
        const all = extractCodeArtifacts(activeChat?.history, 'role', 'assistant')
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
        const id = downloadBtn.dataset.artifactId
        const artifact = artifacts.find(a => a.id === id)
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
  }, [canvasMode, artifacts, activeChat?.history])

  // Files from any of the three attach buttons. Text and PDF files carry their
  // extracted text with them; a file that cannot be read is skipped with a toast.
  const attachFiles = useCallback(async (_kind, list) => {
    const newFiles = []
    for (const file of list) {
      const base64 = await fileToBase64(file)
      const entry = { name: file.name, type: file.type, base64 }
      if (!file.type.startsWith('image/') && !file.type.startsWith('audio/') && !file.type.startsWith('video/')) {
        try {
          entry.textContent = await readAttachmentText(file)
        } catch {
          addToast(t('toasts.pdfReadFailed', { name: file.name }), 'error')
          continue
        }
      }
      newFiles.push(entry)
    }
    setFiles(prev => [...prev, ...newFiles])
  }, [addToast, t])

  const handlePaste = useCallback(async (e) => {
    const items = e.clipboardData?.items
    if (!items) return
    const images = Array.from(items)
      .filter(item => item.kind === 'file' && item.type.startsWith('image/'))
      .map(item => item.getAsFile())
      .filter(Boolean)
    if (images.length === 0) return
    // A pasted image attaches as a file rather than inserting into the text.
    e.preventDefault()
    // Clipboard images arrive unnamed or as a generic "image.png"; give each
    // a unique, typed name so multiple pastes don't collide.
    const newFiles = await Promise.all(images.map(async (file, i) => {
      const name = (file.name && file.name !== 'image.png')
        ? file.name
        : `pasted-image-${i + 1}.${(file.type.split('/')[1] || 'png').replace('+xml', '')}`
      return { name, type: file.type, base64: await fileToBase64(file) }
    }))
    setFiles(prev => [...prev, ...newFiles])
  }, [])

  const handleSend = useCallback(async () => {
    if (isStreaming) return
    const msg = input.trim()
    if (!msg && files.length === 0) return
    if (!activeChat?.model) {
      addToast(t('toasts.selectModel'), 'warning')
      return
    }
    setInput('')
    setFiles([])
    const tools = getToolsForLLM()
    const mcpOptions = tools.length > 0 ? {
      clientMCPTools: tools,
      isClientTool: (name) => isClientTool(name),
      executeTool: (name, args) => executeTool(name, args),
      maxToolTurns: 10,
      getToolAppUI: async (toolName, toolInput, toolResultText) => {
        if (!hasAppUI(toolName)) return null
        const resource = await getAppResource(toolName)
        if (!resource) return null
        return {
          html: resource.html,
          meta: resource.meta,
          toolName,
          toolInput,
          toolDefinition: getToolDefinition(toolName),
          toolResult: { content: [{ type: 'text', text: toolResultText }] },
        }
      },
    } : {}
    await sendMessage(msg, files, mcpOptions)
  }, [isStreaming, input, files, activeChat, sendMessage, addToast, getToolsForLLM, isClientTool, executeTool, hasAppUI, getAppResource, getToolDefinition])

  const handleRegenerate = useCallback(async (targetIndex) => {
    if (!activeChat || isStreaming) return
    const history = activeChat.history
    const end = typeof targetIndex === 'number' ? targetIndex : history.length
    // Nearest user message at or before the target answer.
    let userIdx = -1
    for (let i = Math.min(end, history.length) - 1; i >= 0; i--) {
      if (history[i].role === 'user') { userIdx = i; break }
    }
    if (userIdx === -1) return
    // Reuse the original message's content verbatim (not re-extracted text):
    // it already has any file text / image_url / audio_url / video_url parts
    // embedded from when it was first sent, which display-only file metadata
    // can't reconstruct.
    const userContent = history[userIdx].content
    const userFiles = history[userIdx].files || []
    // Drop the user turn and everything after it; sendMessage re-appends it.
    // Thread the truncated history through explicitly: updateChatSettings only
    // schedules a state update, so sendMessage's closure would otherwise read
    // the stale pre-truncation history for the outbound API payload.
    const baseHistory = history.slice(0, userIdx)
    updateChatSettings(activeChat.id, { history: baseHistory })
    await sendMessage(userContent, userFiles, { baseHistory, prebuiltContent: true })
  }, [activeChat, isStreaming, sendMessage, updateChatSettings])

  // Up in an empty box edits your last message, as in most chat apps.
  const onComposerKey = (e) => {
    if (e.key !== 'ArrowUp' || input || e.shiftKey || e.metaKey || e.ctrlKey || e.altKey) return false
    if (isStreaming || !activeChat) return false
    for (let i = activeChat.history.length - 1; i >= 0; i--) {
      const msg = activeChat.history[i]
      if (msg.role === 'user' && editableMessageText(msg) !== null) {
        e.preventDefault()
        startMessageEdit(i, msg)
        return true
      }
    }
    return false
  }

  const copyMessage = async (content) => {
    const text = typeof content === 'string' ? content : content?.[0]?.text || ''
    const ok = await copyToClipboard(text)
    if (ok) {
      addToast(t('toasts.copied'), 'success', 2000)
    } else {
      addToast(t('toasts.copyFailed'), 'error', 3000)
    }
  }

  const copyChatAsMarkdown = async (chat) => {
    const ok = await copyToClipboard(serializeChatAsMarkdown(chat))
    addToast(ok ? t('toasts.chatCopied') : t('toasts.copyFailed'), ok ? 'success' : 'error', ok ? 2000 : 3000)
  }

  // The thread as rows: a run of reasoning and tool entries folds into the
  // assistant message that follows it, or stands alone when none does yet.
  const history = activeChat?.history
  const rows = useMemo(() => {
    const out = []
    let buf = []
    ;(history || []).forEach((msg, i) => {
      if (isActivityRole(msg.role)) { buf.push(msg); return }
      if (buf.length > 0 && msg.role !== 'assistant') {
        out.push({ kind: 'activity', key: i, items: buf })
        buf = []
      }
      out.push({ kind: 'msg', index: i, msg, activity: buf.length > 0 ? buf : null })
      buf = []
    })
    if (buf.length > 0) out.push({ kind: 'activity', key: 'end', items: buf })
    return out
  }, [history])

  // Deleting a chat is final when its undo time ends. Until then the chat stays
  // in storage and only its row is hidden; if it was the open one, the next
  // chat opens in its place.
  const deleteRef = useRef(deleteChat)
  deleteRef.current = deleteChat
  const commitDelete = useCallback(() => {
    const pending = pendingDeleteRef.current
    if (!pending) return
    pendingDeleteRef.current = null
    setPendingDelete(null)
    deleteRef.current(pending.id)
  }, [])
  const commitRef = useRef(commitDelete)
  commitRef.current = commitDelete
  useEffect(() => () => commitRef.current(), [])

  const visibleChats = useMemo(
    () => chats.filter(c => c.id !== pendingDelete?.id),
    [chats, pendingDelete],
  )

  const requestDelete = (chat) => {
    commitDelete()
    const wasActive = chat.id === activeChatId
    if (wasActive) {
      const next = chats.find(c => c.id !== chat.id)
      if (next) switchChat(next.id)
    }
    const pending = { id: chat.id, name: chat.name, wasActive }
    pendingDeleteRef.current = pending
    setPendingDelete(pending)
  }

  const undoDelete = () => {
    const pending = pendingDeleteRef.current
    pendingDeleteRef.current = null
    setPendingDelete(null)
    if (pending?.wasActive) switchChat(pending.id)
  }

  // The message component is memoised, so it gets one stable set of actions
  // that always call the latest handlers.
  const actionsRef = useRef(null)
  actionsRef.current = {
    copyMessage, startMessageEdit, saveMessageEdit, cancelMessageEdit,
    handleRegenerate, forkChat, addToast, t, activeChat,
  }
  const messageActions = useMemo(() => ({
    copy: (content) => actionsRef.current.copyMessage(content),
    startEdit: (index, message) => actionsRef.current.startMessageEdit(index, message),
    saveEdit: () => actionsRef.current.saveMessageEdit(),
    cancelEdit: () => actionsRef.current.cancelMessageEdit(),
    regenerate: (index) => actionsRef.current.handleRegenerate(index),
    branch: (index) => {
      const a = actionsRef.current
      a.forkChat(a.activeChat.id, index + 1)
      a.addToast(a.t('toasts.forked'), 'success', 2000)
    },
    focusRelative: (index, delta) => {
      const els = Array.from(messagesRef.current?.querySelectorAll('[data-testid="chat-message"]') || [])
      const at = els.findIndex(el => Number(el.dataset.index) === index)
      const next = els[at + delta]
      if (next) { next.focus(); next.scrollIntoView({ block: 'nearest' }) }
    },
  }), [])
  const openImage = useCallback((images, index) => setLightbox({ images, index }), [])

  const contextPercent = getContextUsagePercent()

  const promptDeleteAll = () => setConfirmDialog({
    title: t('deleteAllDialog.title'),
    message: t('deleteAllDialog.message'),
    confirmLabel: t('deleteAllDialog.confirm'),
    danger: true,
    onConfirm: () => { setConfirmDialog(null); deleteAllChats() },
  })

  const promptClear = () => setConfirmDialog({
    title: t('clearDialog.title'),
    message: t('clearDialog.message'),
    confirmLabel: t('clearDialog.confirm'),
    danger: true,
    onConfirm: () => { setConfirmDialog(null); clearHistory(activeChat.id) },
  })

  if (!activeChat) return null

  const openFind = () => setFind(f => ({ ...f, open: true, token: f.token + 1 }))
  const closeFind = () => {
    clearFind(messagesRef.current)
    setFind(f => ({ ...f, open: false, query: '', index: 0, count: 0 }))
    textareaRef.current?.focus()
  }
  const stepFind = (delta) => setFind(f => (f.count === 0 ? f : { ...f, index: (f.index + delta + f.count) % f.count }))

  const isEmpty = activeChat.history.length === 0 && !isStreaming
  const noModel = !activeChat.model && !chatModelsLoading && chatModels.length === 0
  const conversations = isEmpty
    ? conversationsFromChats(visibleChats.filter(c => c.id !== activeChatId))
    : []

  const toggleCanvasMode = () => {
    const next = !canvasMode
    setCanvasMode(next)
    if (!next) setCanvasOpen(false)
  }

  // What a row of the model list says: whether it is loaded, what it can do and,
  // where the server can estimate it, how it fits this machine. A model with no
  // estimate gets no fit text.
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
      value={activeChat.model}
      onChange={(model) => updateChatSettings(activeChat.id, { model })}
      capability={CAP_CHAT}
      models={chatModels}
      loading={chatModelsLoading}
      loadedIds={loadedIds}
      grouped
      describe={describeModel}
      onOpen={onPickerOpen}
      footer={memoryNumbers && (
        <div className="home-menu__foot" data-testid="chat-model-memory">
          <span>{memory.isGpu ? t('picker.gpuMemory') : t('picker.memory')}</span>
          <span className="cx-ctx__bar" aria-hidden="true"><i style={fillStyle(memory.pct)} /></span>
          <span>{t('picker.memoryUsed', { used: memoryNumbers.used, total: memoryNumbers.total, unit: memoryNumbers.unit })}</span>
        </div>
      )}
    />
  )

  const mcp = (
    <UnifiedMCPDropdown
      serverMCPAvailable={mcpAvailable}
      mcpServerList={mcpServerList}
      mcpServersLoading={mcpServersLoading}
      serverListError={mcpServerListError}
      selectedServers={activeChat.mcpServers || []}
      onToggleServer={toggleMcpServer}
      onSelectAllServers={() => {
        const allNames = mcpServerList.filter(s => !s.error).map(s => s.name)
        const allSelected = allNames.every(n => (activeChat.mcpServers || []).includes(n))
        updateChatSettings(activeChat.id, { mcpServers: allSelected ? [] : allNames })
      }}
      onFetchServers={fetchMcpServers}
      clientMCPActiveIds={activeChat.clientMCPServers || []}
      onClientToggle={handleClientMCPToggle}
      onClientAdded={handleClientMCPServerAdded}
      onClientRemoved={handleClientMCPServerRemoved}
      connectionStatuses={connectionStatuses}
      getConnectedTools={getConnectedTools}
      promptsAvailable={mcpAvailable}
      mcpPromptList={mcpPromptList}
      mcpPromptsLoading={mcpPromptsLoading}
      onFetchPrompts={fetchMcpPrompts}
      onSelectPrompt={handleSelectPrompt}
      promptArgsDialog={mcpPromptArgsDialog}
      promptArgsValues={mcpPromptArgsValues}
      onPromptArgsChange={(name, value) => setMcpPromptArgsValues(prev => ({ ...prev, [name]: value }))}
      onPromptArgsSubmit={handleExpandPromptWithArgs}
      onPromptArgsCancel={() => setMcpPromptArgsDialog(null)}
      resourcesAvailable={mcpAvailable}
      mcpResourceList={mcpResourceList}
      mcpResourcesLoading={mcpResourcesLoading}
      onFetchResources={fetchMcpResources}
      selectedResources={activeChat.mcpResources || []}
      onToggleResource={toggleMcpResource}
    />
  )

  const canvasChip = (
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
          onClick={() => { setSelectedArtifactId(artifacts[0]?.id); setCanvasOpen(true) }}
        >
          {artifacts.length}
        </button>
      )}
    </span>
  )

  const moreItems = [
    { key: 'rename', icon: 'pencil', label: t('menu.rename'), onClick: () => setRenaming(true) },
    { key: 'duplicate', icon: 'copy', label: t('menu.duplicate'), onClick: () => { if (forkChat(activeChat.id)) addToast(t('toasts.forked'), 'success', 2000) } },
    { key: 'copy', icon: 'clipboard', label: t('menu.copyChat'), hidden: !hasThread, onClick: () => copyChatAsMarkdown(activeChat) },
    { key: 'export', icon: 'export', label: t('menu.exportMarkdown'), hidden: !hasThread, onClick: () => downloadChatAsMarkdown(activeChat) },
    { key: 'info', icon: 'info', label: t('header.modelInfo'), hidden: !(activeChat.model && isAdmin), onClick: () => setShowSettings(true) },
    { key: 'keys', icon: 'keyboard', label: t('shortcuts.title'), onClick: () => setShowShortcuts(true) },
    { divider: true },
    { key: 'clear', icon: 'trash', label: t('clearDialog.confirm'), danger: true, hidden: !hasThread, onClick: promptClear },
  ]

  const runSlash = (id) => {
    switch (id) {
      case 'model': pickerRef.current?.open(); break
      case 'new': addChat(activeChat.model); break
      case 'chats': chatsMenuRef.current?.open(); break
      case 'assistant': updateChatSettings(activeChat.id, { localaiAssistant: !activeChat.localaiAssistant }); break
      case 'canvas': toggleCanvasMode(); break
      case 'settings': setShowSettings(true); break
      case 'find': openFind(); break
      case 'export': downloadChatAsMarkdown(activeChat); break
      case 'clear': promptClear(); break
      default: break
    }
  }

  return (
    <div className="cx-page">
      {/* Conversation column */}
      <div className="cx-conv" data-empty={isEmpty || undefined}>
        <ChatHeader
          historyMenu={(
            <ChatsMenu
              ref={chatsMenuRef}
              chats={visibleChats}
              activeChatId={activeChatId}
              streamingChatId={streamingChatId}
              onSelect={switchChat}
              onNew={() => addChat(activeChat.model)}
              onDelete={requestDelete}
              onDeleteAll={promptDeleteAll}
              onRename={renameChat}
              onExport={(chat) => downloadChatAsMarkdown(chat)}
              onCopyChat={(chat) => copyChatAsMarkdown(chat)}
              onDuplicate={(chat) => { if (forkChat(chat.id)) addToast(t('toasts.forked'), 'success', 2000) }}
            />
          )}
          manageMode={!!activeChat.localaiAssistant}
          name={activeChat.name}
          onRename={(name) => renameChat(activeChat.id, name)}
          renaming={renaming}
          setRenaming={setRenaming}
          contextPercent={contextPercent}
          contextTokens={activeChat.tokenUsage?.total || 0}
          contextSize={activeChat.contextSize}
          onFind={() => openFind()}
          findOpen={find.open}
          onSettings={() => setShowSettings(v => !v)}
          settingsOpen={showSettings}
          moreItems={moreItems}
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

        {/* Thread */}
        <div className="cx-stage">
        <div className="cx-body" ref={messagesRef}>
          {isEmpty && <EmptyHead name={activeChat.name} manage={!!activeChat.localaiAssistant} />}
          <div className="cx-thread" data-testid="chat-thread" hidden={isEmpty}>
            {rows.map((row) => (row.kind === 'activity' ? (
              <ActivityRow key={`a${row.key}`} id={`cx-act-${row.key}`} items={row.items} getClientForTool={getClientForTool} />
            ) : (
              <ChatMessage
                key={row.index}
                msg={row.msg}
                index={row.index}
                isLast={row.index === activeChat.history.length - 1}
                model={activeChat.model}
                warm={modelWarm}
                canvasMode={canvasMode}
                busy={isStreaming}
                editing={editingMessageIndex === row.index}
                draft={editingMessageIndex === row.index ? messageEditDraft : ''}
                onDraft={setMessageEditDraft}
                activityItems={row.activity}
                getClientForTool={row.activity ? getClientForTool : undefined}
                actions={messageActions}
                onOpenImage={openImage}
              />
            )))}

            {isStreaming && (
              <StreamingTurn
                model={activeChat.model}
                warm={modelWarm}
                content={streamingContent}
                reasoning={streamingReasoning}
                toolCalls={streamingToolCalls}
                waiting={(loadProgress || !modelWarm) ? (
                  <LoadCard model={activeChat.model} progress={loadProgress} />
                ) : (
                  <span className="cx-dots" aria-label={t('streaming.waiting')}><span /><span /><span /></span>
                )}
              />
            )}
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

        {/* Dock: the Home command bar under the thread */}
        <div className="cx-dock">
          <div className="cx-dock__in">
            <HomeComposer
              message={input}
              onMessage={setInput}
              onSubmit={handleSend}
              canSend={!!activeChat.model && (input.trim().length > 0 || files.length > 0)}
              sendTitle={activeChat.model ? t('input.send') : t('input.selectModelFirst')}
              textareaRef={textareaRef}
              picker={picker}
              mcp={mcp}
              chips={canvasChip}
              files={files}
              onRemoveFile={(f) => setFiles(prev => prev.filter(x => x !== f))}
              onAttach={attachFiles}
              placeholder={activeChat.model ? t('input.placeholderModel', { model: activeChat.model }) : (noModel ? t('input.placeholderNoModel') : t('input.placeholderPick'))}
              slash={slashConfig}
              onRunAction={runSlash}
              streaming={isStreaming}
              onStop={stopGeneration}
              stopTitle={t('input.stopGenerating')}
              onPaste={handlePaste}
              onKeyDownExtra={onComposerKey}
              fileAccept="video/*,application/pdf,.txt,.md,.csv,.json"
              rows={1}
              strictEnter
              testId="chat-composer"
              textareaTestId="chat-input"
              sendId="chat-submit-btn"
              sendTestId="chat-send"
            />
            <div className="cx-foot" data-testid="chat-foot">
              <span data-warn={(!isStreaming && contextPercent !== null && contextPercent > 90) || undefined}>
                {isStreaming
                  ? (tokensPerSecond !== null ? `${t('tokens.perSec', { count: tokensPerSecond })} · ${t('tokens.generating')}` : t('tokens.generating'))
                  : (contextPercent !== null && contextPercent > 90
                    ? t('context.nearlyFull')
                    : (maxTokensPerSecond !== null ? t('tokens.peak', { count: maxTokensPerSecond }) : ''))}
              </span>
              <span className="cx-foot__tokens">
                {activeChat.tokenUsage?.total > 0 && (activeChat.contextSize
                  ? t('tokens.ofContext', { used: activeChat.tokenUsage.total, size: activeChat.contextSize })
                  : t('tokens.usage', { prompt: activeChat.tokenUsage.prompt, completion: activeChat.tokenUsage.completion, total: activeChat.tokenUsage.total }))}
              </span>
            </div>
          </div>
        </div>
        {isEmpty && (
          <div className="cx-under" data-testid="chat-under">
            <EmptyUnder
              noModel={noModel}
              isAdmin={isAdmin}
              addToast={addToast}
              onInstallStarted={() => setFitOpenCount(n => n)}
              manage={!!activeChat.localaiAssistant}
              model={activeChat.model}
              warm={modelWarm}
              starters={t(activeChat.localaiAssistant ? 'empty.suggestionsManage' : 'empty.suggestionsChat', { returnObjects: true })}
              onStarter={(prompt) => { setInput(prompt); textareaRef.current?.focus() }}
              conversations={conversations}
              leavingId={null}
              onResume={(conv) => switchChat(conv.id)}
              onDelete={(conv) => { const chat = chats.find(c => c.id === conv.id); if (chat) requestDelete(chat) }}
            />
          </div>
        )}
      </div>
      {canvasOpen && artifacts.length > 0 && (
        <CanvasPanel
          artifacts={artifacts}
          selectedId={selectedArtifactId}
          onSelect={setSelectedArtifactId}
          onClose={() => setCanvasOpen(false)}
        />
      )}
      {lightbox && (
        <Lightbox
          images={lightbox.images}
          index={lightbox.index}
          onIndex={(index) => setLightbox(prev => ({ ...prev, index }))}
          onClose={() => setLightbox(null)}
        />
      )}
      {showSettings && (
        <ChatSettingsSheet
          chat={activeChat}
          isAdmin={isAdmin}
          onUpdate={(patch) => updateChatSettings(activeChat.id, patch)}
          focusMode={focusModeEnabled}
          onFocusMode={toggleFocusMode}
          modelInfo={modelInfo}
          onEditConfig={() => navigate(`/app/model-editor/${encodeURIComponent(activeChat.model)}`, { state: fromState(location, 'Chat') })}
          onClear={() => { setShowSettings(false); promptClear() }}
          onClose={() => setShowSettings(false)}
        />
      )}
      {showShortcuts && <ShortcutsDialog onClose={() => setShowShortcuts(false)} canFind />}
      {pendingDelete && (
        <HomeUndoToast
          key={pendingDelete.id}
          message={t('menu.deleted', { title: pendingDelete.name })}
          onUndo={undoDelete}
          onExpire={commitDelete}
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
