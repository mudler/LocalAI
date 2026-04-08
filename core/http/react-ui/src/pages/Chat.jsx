import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { useParams, useOutletContext, useNavigate } from 'react-router-dom'
import { useChat } from '../hooks/useChat'
import ModelSelector from '../components/ModelSelector'
import { renderMarkdown, highlightAll } from '../utils/markdown'
import { extractCodeArtifacts, renderMarkdownWithArtifacts } from '../utils/artifacts'
import CanvasPanel from '../components/CanvasPanel'
import { fileToBase64, modelsApi, mcpApi } from '../utils/api'
import { CAP_CHAT } from '../utils/capabilities'
import { useMCPClient } from '../hooks/useMCPClient'
import MCPAppFrame from '../components/MCPAppFrame'
import UnifiedMCPDropdown from '../components/UnifiedMCPDropdown'
import { loadClientMCPServers } from '../utils/mcpClientStorage'
import ConfirmDialog from '../components/ConfirmDialog'
import { useAuth } from '../context/AuthContext'
import { useOperations } from '../hooks/useOperations'
import { relativeTime } from '../utils/format'

function getLastMessagePreview(chat) {
  if (!chat.history || chat.history.length === 0) return ''
  for (let i = chat.history.length - 1; i >= 0; i--) {
    const msg = chat.history[i]
    if (msg.role === 'user' || msg.role === 'assistant') {
      const text = typeof msg.content === 'string' ? msg.content : msg.content?.[0]?.text || ''
      return text.slice(0, 40).replace(/\n/g, ' ')
    }
  }
  return ''
}

function exportChatAsMarkdown(chat) {
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
  const blob = new Blob([md], { type: 'text/markdown' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${chat.name.replace(/[^a-zA-Z0-9]/g, '_')}.md`
  a.click()
  URL.revokeObjectURL(url)
}

function formatToolContent(raw) {
  try {
    const data = JSON.parse(raw)
    const name = data.name || 'unknown'
    let params = data.arguments || data.input || data.result || data.parameters || {}
    if (typeof params === 'string') {
      try { params = JSON.parse(params) } catch (_) { /* keep as string */ }
    }
    const entries = typeof params === 'object' && params !== null ? Object.entries(params) : []
    return { name, entries, fallback: null }
  } catch (_e) {
    return { name: null, entries: [], fallback: raw }
  }
}

function ToolParams({ entries, fallback }) {
  if (fallback) {
    return <span className="chat-activity-item-text">{fallback}</span>
  }
  if (entries.length === 0) return null
  return (
    <div className="chat-activity-params">
      {entries.map(([k, v]) => {
        const val = typeof v === 'string' ? v : JSON.stringify(v, null, 2)
        const isLong = val.length > 120
        return (
          <div key={k} className="chat-activity-param">
            <span className="chat-activity-param-key">{k}:</span>
            <span className={`chat-activity-param-val${isLong ? ' chat-activity-param-val-long' : ''}`}>{val}</span>
          </div>
        )
      })}
    </div>
  )
}

function ActivityGroup({ items, updateChatSettings, activeChat, getClientForTool }) {
  const [expanded, setExpanded] = useState(false)
  const contentRef = useRef(null)

  useEffect(() => {
    if (expanded && contentRef.current) highlightAll(contentRef.current)
  }, [expanded])

  if (!items || items.length === 0) return null

  // Separate out tool_result items that have appUI — they render outside the collapsed group
  const appUIItems = items.filter(item => item.role === 'tool_result' && item.appUI)
  const regularItems = items.filter(item => !(item.role === 'tool_result' && item.appUI))

  const labels = regularItems.map(item => {
    if (item.role === 'thinking' || item.role === 'reasoning') return 'Thought'
    if (item.role === 'tool_call') {
      try { return JSON.parse(item.content)?.name || 'Tool' } catch (_e) { return 'Tool' }
    }
    if (item.role === 'tool_result') {
      try { return `${JSON.parse(item.content)?.name || 'Tool'} result` } catch (_e) { return 'Result' }
    }
    return item.role
  })
  const summary = labels.join(' → ')

  return (
    <>
      {regularItems.length > 0 && (
        <div className="chat-message chat-message-assistant">
          <div className="chat-message-avatar">
            <i className="fas fa-cogs" />
          </div>
          <div className="chat-activity-group">
            <button className="chat-activity-toggle" onClick={() => setExpanded(!expanded)}>
              <span className="chat-activity-summary">{summary}</span>
              <i className={`fas fa-chevron-${expanded ? 'up' : 'down'}`} />
            </button>
            {expanded && (
              <div className="chat-activity-details" ref={contentRef}>
                {regularItems.map((item, idx) => {
                  if (item.role === 'thinking' || item.role === 'reasoning') {
                    return (
                      <div key={idx} className="chat-activity-item chat-activity-thinking">
                        <span className="chat-activity-item-label">Thought</span>
                        <div className="chat-activity-item-content"
                          dangerouslySetInnerHTML={{ __html: renderMarkdown(item.content || '') }} />
                      </div>
                    )
                  }
                  const isCall = item.role === 'tool_call'
                  const parsed = formatToolContent(item.content)
                  return (
                    <div key={idx} className={`chat-activity-item ${isCall ? 'chat-activity-tool-call' : 'chat-activity-tool-result'}`}>
                      <span className="chat-activity-item-label">{labels[idx]}</span>
                      <ToolParams entries={parsed.entries} fallback={parsed.fallback} />
                    </div>
                  )
                })}
              </div>
            )}
          </div>
        </div>
      )}
      {appUIItems.map((item, idx) => (
        <div key={`appui-${idx}`} className="chat-message chat-message-assistant">
          <div className="chat-message-avatar">
            <i className="fas fa-puzzle-piece" />
          </div>
          <div className="chat-message-bubble">
            <span className="chat-message-model">{item.appUI.toolName}</span>
            <MCPAppFrame
              toolName={item.appUI.toolName}
              toolInput={item.appUI.toolInput}
              toolResult={item.appUI.toolResult}
              mcpClient={getClientForTool?.(item.appUI.toolName) || null}
              toolDefinition={item.appUI.toolDefinition}
              appHtml={item.appUI.html}
              resourceMeta={item.appUI.meta}
            />
          </div>
        </div>
      ))}
    </>
  )
}

function StreamingActivity({ reasoning, toolCalls, hasResponse }) {
  const hasContent = reasoning || (toolCalls && toolCalls.length > 0)
  if (!hasContent) return null

  const contentRef = useRef(null)
  const [manualCollapse, setManualCollapse] = useState(null)

  // Auto-expand while thinking or tool-calling, auto-collapse when response starts
  const autoExpanded = (reasoning || (toolCalls && toolCalls.length > 0)) && !hasResponse
  const expanded = manualCollapse !== null ? !manualCollapse : autoExpanded

  // Scroll to bottom of thinking content as it streams
  useEffect(() => {
    if (expanded && contentRef.current) {
      contentRef.current.scrollTop = contentRef.current.scrollHeight
    }
  }, [reasoning, expanded])

  // Reset manual override when streaming state changes significantly
  useEffect(() => {
    setManualCollapse(null)
  }, [hasResponse])

  const lastTool = toolCalls && toolCalls.length > 0 ? toolCalls[toolCalls.length - 1] : null
  const label = reasoning
    ? 'Thinking...'
    : lastTool
      ? (lastTool.type === 'tool_call' ? lastTool.name : `${lastTool.name} result`)
      : ''

  return (
    <div className="chat-message chat-message-assistant">
      <div className="chat-message-avatar">
        <i className="fas fa-cogs" />
      </div>
      <div className="chat-activity-group chat-activity-streaming">
        <button className="chat-activity-toggle" onClick={() => setManualCollapse(expanded)}>
          <span className={`chat-activity-summary${!expanded ? ' chat-activity-shimmer' : ''}`}>
            {label}
          </span>
          <i className={`fas fa-chevron-${expanded ? 'up' : 'down'}`} />
        </button>
        {expanded && reasoning && (
          <div className="chat-activity-details">
            <div className="chat-activity-item chat-activity-thinking">
              <div className="chat-activity-item-content chat-activity-live" ref={contentRef}
                dangerouslySetInnerHTML={{ __html: renderMarkdown(reasoning) }} />
            </div>
          </div>
        )}
        {expanded && toolCalls && toolCalls.length > 0 && (
          <div className="chat-activity-details">
            {toolCalls.map((tc, idx) => {
              if (tc.type === 'tool_result') {
                return (
                  <div key={idx} className="chat-activity-item chat-activity-tool-result">
                    <span className="chat-activity-item-label">{tc.name} result</span>
                    <div className="chat-activity-item-content"
                      dangerouslySetInnerHTML={{ __html: renderMarkdown(tc.result || '') }} />
                  </div>
                )
              }
              const parsed = formatToolContent(JSON.stringify(tc, null, 2))
              return (
                <div key={idx} className="chat-activity-item chat-activity-tool-call">
                  <span className="chat-activity-item-label">{tc.name || tc.type}</span>
                  <ToolParams entries={parsed.entries} fallback={parsed.fallback} />
                </div>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}

function UserMessageContent({ content, files }) {
  const text = typeof content === 'string' ? content : content?.[0]?.text || ''
  return (
    <>
      <div dangerouslySetInnerHTML={{ __html: text.replace(/\n/g, '<br>') }} />
      {files && files.length > 0 && (
        <div className="chat-message-files">
          {files.map((f, i) => (
            <span key={i} className="chat-file-inline">
              <i className={`fas ${f.type === 'image' ? 'fa-image' : f.type === 'audio' ? 'fa-headphones' : 'fa-file'}`} />
              {f.name}
            </span>
          ))}
        </div>
      )}
      {Array.isArray(content) && content.filter(c => c.type === 'image_url').map((img, i) => (
        <img key={i} src={img.image_url.url} alt="attached" className="chat-inline-image" />
      ))}
    </>
  )
}

export default function Chat() {
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { isAdmin } = useAuth()
  const { operations } = useOperations()
  const {
    chats, activeChat, activeChatId, isStreaming, streamingChatId, streamingContent,
    streamingReasoning, streamingToolCalls, tokensPerSecond, maxTokensPerSecond,
    addChat, switchChat, deleteChat, deleteAllChats, renameChat, updateChatSettings,
    sendMessage, stopGeneration, clearHistory, getContextUsagePercent, addMessage,
  } = useChat(urlModel || '')

  // Detect active staging operation for the current chat's model
  const stagingOp = useMemo(() => {
    if (!isStreaming || !activeChat?.model) return null
    return operations.find(op => op.taskType === 'staging' && op.name === activeChat.model) || null
  }, [operations, isStreaming, activeChat?.model])

  const [input, setInput] = useState('')
  const [files, setFiles] = useState([])
  const [showSettings, setShowSettings] = useState(false)
  const [editingName, setEditingName] = useState(null)
  const [editName, setEditName] = useState('')
  const [mcpAvailable, setMcpAvailable] = useState(false)
  const [mcpServerList, setMcpServerList] = useState([])
  const [mcpServersLoading, setMcpServersLoading] = useState(false)
  const [mcpServerCache, setMcpServerCache] = useState({})
  const [mcpPromptList, setMcpPromptList] = useState([])
  const [mcpPromptsLoading, setMcpPromptsLoading] = useState(false)
  const [mcpPromptArgsDialog, setMcpPromptArgsDialog] = useState(null)
  const [mcpPromptArgsValues, setMcpPromptArgsValues] = useState({})
  const [mcpResourceList, setMcpResourceList] = useState([])
  const [mcpResourcesLoading, setMcpResourcesLoading] = useState(false)
  const [chatSearch, setChatSearch] = useState('')
  const [modelInfo, setModelInfo] = useState(null)
  const [showModelInfo, setShowModelInfo] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [canvasMode, setCanvasMode] = useState(false)
  const [canvasOpen, setCanvasOpen] = useState(false)
  const [selectedArtifactId, setSelectedArtifactId] = useState(null)
  const [clientMCPServers, setClientMCPServers] = useState(() => loadClientMCPServers())
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [completionGlowIdx, setCompletionGlowIdx] = useState(-1)
  const prevStreamingRef = useRef(false)
  const {
    connect: mcpConnect, disconnect: mcpDisconnect, disconnectAll: mcpDisconnectAll,
    getToolsForLLM, isClientTool, executeTool, connectionStatuses, getConnectedTools,
    hasAppUI, getAppResource, getClientForTool, getToolDefinition,
  } = useMCPClient()
  const messagesEndRef = useRef(null)
  const fileInputRef = useRef(null)
  const messagesRef = useRef(null)
  const textareaRef = useRef(null)

  const artifacts = useMemo(
    () => canvasMode ? extractCodeArtifacts(activeChat?.history, 'role', 'assistant') : [],
    [activeChat?.history, canvasMode]
  )

  const prevArtifactCountRef = useRef(0)
  useEffect(() => {
    prevArtifactCountRef.current = artifacts.length
  }, [activeChat?.id])
  useEffect(() => {
    if (artifacts.length > prevArtifactCountRef.current && artifacts.length > 0) {
      setSelectedArtifactId(artifacts[artifacts.length - 1].id)
      if (!canvasOpen) setCanvasOpen(true)
    }
    prevArtifactCountRef.current = artifacts.length
  }, [artifacts])

  // Completion glow: when streaming finishes, briefly highlight last assistant message
  useEffect(() => {
    if (prevStreamingRef.current && !isStreaming && activeChat?.history?.length > 0) {
      const lastIdx = activeChat.history.length - 1
      if (activeChat.history[lastIdx]?.role === 'assistant') {
        setCompletionGlowIdx(lastIdx)
        const timer = setTimeout(() => setCompletionGlowIdx(-1), 600)
        return () => clearTimeout(timer)
      }
    }
    prevStreamingRef.current = isStreaming
  }, [isStreaming, activeChat?.history?.length])

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
    if (mcpServerCache[model]) {
      setMcpServerList(mcpServerCache[model])
      return
    }
    setMcpServersLoading(true)
    try {
      const data = await mcpApi.listServers(model)
      const servers = data?.servers || []
      setMcpServerList(servers)
      setMcpServerCache(prev => ({ ...prev, [model]: servers }))
    } catch (_e) {
      setMcpServerList([])
    } finally {
      setMcpServersLoading(false)
    }
  }, [activeChat?.model, mcpServerCache])

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
        if (data.message) {
          // Create a new chat when coming from home
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
          setInput(data.message)
          if (data.files) setFiles(data.files)
          setTimeout(() => {
            const submitBtn = document.getElementById('chat-submit-btn')
            submitBtn?.click()
          }, 100)
        }
      } catch (_e) { /* ignore */ }
    }
  }, [])

  // Auto-scroll
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [activeChat?.history, streamingContent, streamingReasoning, streamingToolCalls])

  // Highlight code blocks
  useEffect(() => {
    if (messagesRef.current) {
      highlightAll(messagesRef.current)
    }
  }, [activeChat?.history, streamingContent])

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

  // Event delegation for artifact cards
  useEffect(() => {
    const el = messagesRef.current
    if (!el || !canvasMode) return
    const handler = (e) => {
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
  }, [canvasMode, artifacts])

  const handleFileChange = useCallback(async (e) => {
    const newFiles = []
    for (const file of e.target.files) {
      const base64 = await fileToBase64(file)
      const entry = { name: file.name, type: file.type, base64 }
      if (!file.type.startsWith('image/') && !file.type.startsWith('audio/')) {
        entry.textContent = await file.text().catch(() => '')
      }
      newFiles.push(entry)
    }
    setFiles(prev => [...prev, ...newFiles])
    e.target.value = ''
  }, [])

  const handleSend = useCallback(async () => {
    const msg = input.trim()
    if (!msg && files.length === 0) return
    if (!activeChat?.model) {
      addToast('Please select a model', 'warning')
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
  }, [input, files, activeChat, sendMessage, addToast, getToolsForLLM, isClientTool, executeTool, hasAppUI, getAppResource, getToolDefinition])

  const handleRegenerate = useCallback(async () => {
    if (!activeChat || isStreaming) return
    const history = activeChat.history
    let lastUserMsg = null
    let lastUserFiles = null
    for (let i = history.length - 1; i >= 0; i--) {
      if (history[i].role === 'user') {
        lastUserMsg = typeof history[i].content === 'string' ? history[i].content : history[i].content?.[0]?.text || ''
        lastUserFiles = history[i].files || []
        break
      }
    }
    if (!lastUserMsg) return

    // Remove everything after and including the last user message
    const newHistory = []
    let foundLastUser = false
    for (let i = history.length - 1; i >= 0; i--) {
      if (!foundLastUser && history[i].role === 'user') {
        foundLastUser = true
        continue
      }
      if (foundLastUser) {
        newHistory.unshift(history[i])
      }
    }
    updateChatSettings(activeChat.id, { history: newHistory })
    await sendMessage(lastUserMsg, lastUserFiles)
  }, [activeChat, isStreaming, sendMessage, updateChatSettings])

  const handleKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  const startRename = (chatId, currentName) => {
    setEditingName(chatId)
    setEditName(currentName)
  }

  const finishRename = () => {
    if (editingName && editName.trim()) {
      renameChat(editingName, editName.trim())
    }
    setEditingName(null)
  }

  const copyMessage = (content) => {
    const text = typeof content === 'string' ? content : content?.[0]?.text || ''
    navigator.clipboard.writeText(text)
    addToast('Copied to clipboard', 'success', 2000)
  }

  // Filter chats by search
  const filteredChats = chatSearch.trim()
    ? chats.filter(c => {
      const q = chatSearch.toLowerCase()
      if ((c.name || '').toLowerCase().includes(q)) return true
      return c.history?.some(m => {
        const t = typeof m.content === 'string' ? m.content : m.content?.[0]?.text || ''
        return t.toLowerCase().includes(q)
      })
    })
    : chats

  const contextPercent = getContextUsagePercent()

  if (!activeChat) return null

  return (
    <div className={`chat-layout${sidebarOpen ? '' : ' chat-sidebar-collapsed'}`}>
      {/* Chat sidebar */}
      <div className={`chat-sidebar${sidebarOpen ? '' : ' hidden'}`}>
        <div className="chat-sidebar-header">
          <button className="btn btn-primary btn-sm" style={{ flex: 1 }} onClick={() => addChat(activeChat.model)}>
            <i className="fas fa-plus" /> New Chat
          </button>
          <button
            className="btn btn-secondary btn-sm"
            onClick={() => setConfirmDialog({
              title: 'Delete All Chats',
              message: 'Delete all chats? This cannot be undone.',
              confirmLabel: 'Delete all',
              danger: true,
              onConfirm: () => { setConfirmDialog(null); deleteAllChats() },
            })}
            title="Delete all chats"
            style={{ padding: '6px 8px' }}
          >
            <i className="fas fa-trash" />
          </button>
        </div>

        {/* Chat search */}
        <div style={{ padding: '0 var(--spacing-sm)' }}>
          <div className="chat-search-wrapper">
            <i className="fas fa-search chat-search-icon" />
            <input
              className="chat-search-input"
              type="text"
              value={chatSearch}
              onChange={(e) => setChatSearch(e.target.value)}
              placeholder="Search conversations..."
            />
            {chatSearch && (
              <button className="chat-search-clear" onClick={() => setChatSearch('')}>
                <i className="fas fa-times" />
              </button>
            )}
          </div>
        </div>

        <div className="chat-list">
          {filteredChats.map(chat => (
            <div
              key={chat.id}
              className={`chat-list-item ${chat.id === activeChatId ? 'active' : ''}`}
              onClick={() => switchChat(chat.id)}
            >
              <i className="fas fa-message" style={{ fontSize: '0.7rem', flexShrink: 0, marginTop: '2px' }} />
              {editingName === chat.id ? (
                <input
                  className="input"
                  value={editName}
                  onChange={(e) => setEditName(e.target.value)}
                  onBlur={finishRename}
                  onKeyDown={(e) => e.key === 'Enter' && finishRename()}
                  autoFocus
                  onClick={(e) => e.stopPropagation()}
                  style={{ padding: '2px 4px', fontSize: '0.8125rem' }}
                />
              ) : (
                <div className="chat-list-item-info">
                  <div className="chat-list-item-top">
                    <span
                      className="chat-list-item-name"
                      onDoubleClick={() => startRename(chat.id, chat.name)}
                    >
                      {streamingChatId === chat.id && <i className="fas fa-circle-notch fa-spin" style={{ marginRight: '6px', fontSize: '0.7rem', opacity: 0.7 }} />}
                      {chat.name}
                    </span>
                    <span className="chat-list-item-time">{relativeTime(chat.updatedAt)}</span>
                  </div>
                  <span className="chat-list-item-preview">
                    {getLastMessagePreview(chat) || 'No messages yet'}
                  </span>
                </div>
              )}
              <div className="chat-list-item-actions">
                <button
                  onClick={(e) => { e.stopPropagation(); startRename(chat.id, chat.name) }}
                  title="Rename"
                >
                  <i className="fas fa-edit" />
                </button>
                {chats.length > 1 && (
                  <button
                    className="chat-list-item-delete"
                    onClick={(e) => { e.stopPropagation(); deleteChat(chat.id) }}
                    title="Delete chat"
                  >
                    <i className="fas fa-trash" />
                  </button>
                )}
              </div>
            </div>
          ))}
          {filteredChats.length === 0 && chatSearch && (
            <div style={{ padding: 'var(--spacing-sm)', textAlign: 'center', color: 'var(--color-text-muted)', fontSize: '0.8rem' }}>
              No conversations match your search
            </div>
          )}
        </div>
      </div>

      {/* Chat main area */}
      <div className="chat-main">
        {/* Header */}
        <div className="chat-header">
          <button
            className="btn btn-secondary btn-sm"
            onClick={() => setSidebarOpen(prev => !prev)}
            title={sidebarOpen ? 'Hide chat list' : 'Show chat list'}
            style={{ flexShrink: 0 }}
          >
            <i className={`fas fa-${sidebarOpen ? 'angles-left' : 'angles-right'}`} />
          </button>
          <span className="chat-header-title">{activeChat.name}</span>
          <UnifiedMCPDropdown
            serverMCPAvailable={mcpAvailable}
            mcpServerList={mcpServerList}
            mcpServersLoading={mcpServersLoading}
            selectedServers={activeChat.mcpServers || []}
            onToggleServer={toggleMcpServer}
            onSelectAllServers={() => {
              const allNames = mcpServerList.map(s => s.name)
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
          <ModelSelector
            value={activeChat.model}
            onChange={(model) => updateChatSettings(activeChat.id, { model })}
            capability={CAP_CHAT}
            style={{ flex: '1 1 0', minWidth: 120 }}
          />
          <div className="chat-header-actions">
            {activeChat.model && isAdmin && (
              <>
                <button
                  className="btn btn-secondary btn-sm"
                  onClick={() => setShowModelInfo(!showModelInfo)}
                  title="Model info"
                >
                  <i className="fas fa-info-circle" />
                </button>
                <button
                  className="btn btn-secondary btn-sm"
                  onClick={() => navigate(`/app/model-editor/${encodeURIComponent(activeChat.model)}`)}
                  title="Edit model config"
                >
                  <i className="fas fa-edit" />
                </button>
              </>
            )}
            <label className="canvas-mode-toggle" title="Extract code blocks and media into a side panel for preview, copy, and download">
              <i className="fas fa-columns" />
              <span className="canvas-mode-label">Canvas</span>
              <span className="toggle">
                <input
                  type="checkbox"
                  checked={canvasMode}
                  onChange={(e) => {
                    setCanvasMode(e.target.checked)
                    if (!e.target.checked) setCanvasOpen(false)
                  }}
                />
                <span className="toggle-slider" />
              </span>
            </label>
            {canvasMode && artifacts.length > 0 && !canvasOpen && (
              <button
                className="btn btn-secondary btn-sm"
                onClick={() => { setSelectedArtifactId(artifacts[0]?.id); setCanvasOpen(true) }}
                title="Open canvas panel"
              >
                <i className="fas fa-layer-group" /> {artifacts.length}
              </button>
            )}
            <button
              className="btn btn-secondary btn-sm"
              onClick={() => exportChatAsMarkdown(activeChat)}
              title="Export chat as Markdown"
            >
              <i className="fas fa-download" />
            </button>
            <button
              className="btn btn-secondary btn-sm"
              onClick={() => clearHistory(activeChat.id)}
              title="Clear chat history"
            >
              <i className="fas fa-eraser" />
            </button>
            <button
              className={`btn btn-secondary btn-sm${showSettings ? ' active' : ''}`}
              onClick={() => setShowSettings(!showSettings)}
              title="Settings"
            >
              <i className="fas fa-sliders-h" />
            </button>
          </div>
        </div>

        {/* Model info panel */}
        {showModelInfo && modelInfo && (
          <div className="chat-model-info-panel">
            <div className="chat-model-info-header">
              <span>Model Info: {activeChat.model}</span>
              <button className="btn btn-secondary btn-sm" onClick={() => setShowModelInfo(false)}>
                <i className="fas fa-times" />
              </button>
            </div>
            <div className="chat-model-info-body">
              {modelInfo.backend && <div className="chat-model-info-row"><span>Backend</span><span>{modelInfo.backend}</span></div>}
              {modelInfo.parameters?.model && <div className="chat-model-info-row"><span>Model file</span><span>{modelInfo.parameters.model}</span></div>}
              {modelInfo.context_size > 0 && <div className="chat-model-info-row"><span>Context size</span><span>{modelInfo.context_size}</span></div>}
              {modelInfo.threads > 0 && <div className="chat-model-info-row"><span>Threads</span><span>{modelInfo.threads}</span></div>}
              {(modelInfo.mcp?.remote || modelInfo.mcp?.stdio) && <div className="chat-model-info-row"><span>MCP</span><span className="badge badge-success">Configured</span></div>}
              {modelInfo.template?.chat_message && <div className="chat-model-info-row"><span>Chat template</span><span>Yes</span></div>}
              {modelInfo.gpu_layers > 0 && <div className="chat-model-info-row"><span>GPU layers</span><span>{modelInfo.gpu_layers}</span></div>}
            </div>
          </div>
        )}

        {/* Context window progress bar */}
        {contextPercent !== null && (
          <div className="chat-context-bar">
            <div className="chat-context-progress"
              style={{
                width: `${contextPercent}%`,
                background: contextPercent > 90 ? 'var(--color-error)' : contextPercent > 70 ? 'var(--color-warning)' : 'var(--color-primary)',
              }}
            />
            <span className="chat-context-label">
              Context: {Math.round(contextPercent)}%
              {activeChat.tokenUsage.total > 0 && ` (${activeChat.tokenUsage.total} tokens)`}
            </span>
          </div>
        )}

        {/* Settings slide-out panel */}
        <div className={`chat-settings-overlay${showSettings ? ' open' : ''}`} onClick={() => setShowSettings(false)} />
        <div className={`chat-settings-drawer${showSettings ? ' open' : ''}`}>
          <div className="chat-settings-drawer-header">
            <span>Chat Settings</span>
            <button className="btn btn-secondary btn-sm" onClick={() => setShowSettings(false)}>
              <i className="fas fa-times" />
            </button>
          </div>
          <div className="chat-settings-drawer-body">
            <div className="form-group">
              <label className="form-label">System Prompt</label>
              <textarea
                className="textarea"
                value={activeChat.systemPrompt || ''}
                onChange={(e) => updateChatSettings(activeChat.id, { systemPrompt: e.target.value })}
                rows={3}
                placeholder="You are a helpful assistant..."
              />
            </div>
            <div className="form-group">
              <label className="form-label">
                Temperature {activeChat.temperature !== null ? `(${activeChat.temperature})` : ''}
              </label>
              <input
                type="range" min="0" max="2" step="0.1"
                value={activeChat.temperature ?? 0.7}
                onChange={(e) => updateChatSettings(activeChat.id, { temperature: parseFloat(e.target.value) })}
                className="chat-slider"
              />
              <div className="chat-slider-labels"><span>0</span><span>2</span></div>
            </div>
            <div className="form-group">
              <label className="form-label">
                Top P {activeChat.topP !== null ? `(${activeChat.topP})` : ''}
              </label>
              <input
                type="range" min="0" max="1" step="0.05"
                value={activeChat.topP ?? 0.9}
                onChange={(e) => updateChatSettings(activeChat.id, { topP: parseFloat(e.target.value) })}
                className="chat-slider"
              />
              <div className="chat-slider-labels"><span>0</span><span>1</span></div>
            </div>
            <div className="form-group">
              <label className="form-label">
                Top K {activeChat.topK !== null ? `(${activeChat.topK})` : ''}
              </label>
              <input
                type="range" min="1" max="100" step="1"
                value={activeChat.topK ?? 40}
                onChange={(e) => updateChatSettings(activeChat.id, { topK: parseInt(e.target.value) })}
                className="chat-slider"
              />
              <div className="chat-slider-labels"><span>1</span><span>100</span></div>
            </div>
            <div className="form-group">
              <label className="form-label">Context Size</label>
              <input
                type="number"
                className="input"
                value={activeChat.contextSize || ''}
                onChange={(e) => updateChatSettings(activeChat.id, { contextSize: parseInt(e.target.value) || null })}
                placeholder="2048"
              />
            </div>
          </div>
        </div>

        {/* Messages */}
        <div className="chat-messages" ref={messagesRef}>
          {activeChat.history.length === 0 && !isStreaming && (
            <div className="chat-empty-state">
              <div className="chat-empty-icon">
                <i className="fas fa-comments" />
              </div>
              <h2 className="chat-empty-title">Start a conversation</h2>
              <p className="chat-empty-text">{activeChat.model ? `Ready to chat with ${activeChat.model}` : 'Select a model above to get started'}</p>
              <div className="chat-empty-suggestions">
                {['Explain how this works', 'Help me write code', 'Summarize a document', 'Brainstorm ideas'].map((prompt) => (
                  <button
                    key={prompt}
                    className="chat-empty-suggestion"
                    onClick={() => { setInput(prompt); textareaRef.current?.focus() }}
                  >
                    {prompt}
                  </button>
                ))}
              </div>
              <div className="chat-empty-hints">
                <span><i className="fas fa-keyboard" /> Enter to send</span>
                <span><i className="fas fa-level-down-alt" /> Shift+Enter for newline</span>
                <span><i className="fas fa-paperclip" /> Attach files</span>
              </div>
            </div>
          )}
          {(() => {
            const elements = []
            let activityBuf = []
            const flushActivity = (key) => {
              if (activityBuf.length > 0) {
                elements.push(
                  <ActivityGroup key={`ag-${key}`} items={[...activityBuf]}
                    updateChatSettings={updateChatSettings} activeChat={activeChat}
                    getClientForTool={getClientForTool} />
                )
                activityBuf = []
              }
            }
            activeChat.history.forEach((msg, i) => {
              const isActivity = msg.role === 'thinking' || msg.role === 'reasoning' ||
                msg.role === 'tool_call' || msg.role === 'tool_result'
              if (isActivity) {
                activityBuf.push(msg)
                return
              }
              flushActivity(i)
              elements.push(
                <div key={i} className={`chat-message chat-message-${msg.role}${i === completionGlowIdx ? ' chat-message-new' : ''}`}>
                  <div className="chat-message-avatar">
                    <i className={`fas ${msg.role === 'user' ? 'fa-user' : 'fa-robot'}`} />
                  </div>
                  <div className="chat-message-bubble">
                    {msg.role === 'assistant' && activeChat.model && (
                      <span className="chat-message-model">{activeChat.model}</span>
                    )}
                    <div className="chat-message-content">
                      {msg.role === 'user' ? (
                        <UserMessageContent content={msg.content} files={msg.files} />
                      ) : (
                        <div dangerouslySetInnerHTML={{
                          __html: canvasMode
                            ? renderMarkdownWithArtifacts(typeof msg.content === 'string' ? msg.content : '', i)
                            : renderMarkdown(typeof msg.content === 'string' ? msg.content : '')
                        }} />
                      )}
                    </div>
                    {msg.role === 'assistant' && typeof msg.content === 'string' && msg.content.includes('Error:') && (
                      <a href="/app/traces?tab=backend" className="chat-error-trace-link">
                        <i className="fas fa-wave-square" /> View traces for details
                      </a>
                    )}
                    <div className="chat-message-actions">
                      <button onClick={() => copyMessage(msg.content)} title="Copy">
                        <i className="fas fa-copy" />
                      </button>
                      {msg.role === 'assistant' && i === activeChat.history.length - 1 && !isStreaming && (
                        <button onClick={handleRegenerate} title="Regenerate">
                          <i className="fas fa-rotate" />
                        </button>
                      )}
                    </div>
                  </div>
                </div>
              )
            })
            flushActivity('end')
            return elements
          })()}

          {/* Streaming activity (thinking + tools) */}
          {isStreaming && (streamingReasoning || streamingToolCalls.length > 0) && (
            <StreamingActivity reasoning={streamingReasoning} toolCalls={streamingToolCalls} hasResponse={!!streamingContent} />
          )}

          {/* Streaming message */}
          {isStreaming && streamingContent && (
            <div className="chat-message chat-message-assistant">
              <div className="chat-message-avatar">
                <i className="fas fa-robot" />
              </div>
              <div className="chat-message-bubble">
                {activeChat.model && (
                  <span className="chat-message-model">{activeChat.model}</span>
                )}
                <div className="chat-message-content">
                  <span dangerouslySetInnerHTML={{ __html: renderMarkdown(streamingContent) }} />
                  <span className="chat-streaming-cursor" />
                </div>
                {tokensPerSecond !== null && (
                  <div className="chat-streaming-speed">
                    <i className="fas fa-tachometer-alt" /> {tokensPerSecond} tok/s
                  </div>
                )}
              </div>
            </div>
          )}
          {isStreaming && !streamingContent && !streamingReasoning && streamingToolCalls.length === 0 && (
            <div className="chat-message chat-message-assistant">
              <div className="chat-message-avatar">
                <i className="fas fa-robot" />
              </div>
              <div className="chat-message-bubble">
                <div className="chat-message-content chat-thinking-indicator">
                  {stagingOp ? (
                    <div className="chat-staging-progress">
                      <div className="chat-staging-label">
                        <i className="fas fa-cloud-arrow-up" /> Transferring model{stagingOp.nodeName ? ` to ${stagingOp.nodeName}` : ''}...
                      </div>
                      {stagingOp.progress > 0 && (
                        <div className="chat-staging-detail">
                          <div className="chat-staging-bar-container">
                            <div className="chat-staging-bar" style={{ width: `${stagingOp.progress}%` }} />
                          </div>
                          <span className="chat-staging-pct">{Math.round(stagingOp.progress)}%</span>
                        </div>
                      )}
                      {stagingOp.message && (
                        <div className="chat-staging-file">{stagingOp.message}</div>
                      )}
                    </div>
                  ) : (
                    <span className="chat-thinking-dots">
                      <span /><span /><span />
                    </span>
                  )}
                </div>
              </div>
            </div>
          )}
          <div ref={messagesEndRef} />
        </div>

        {/* Token info bar */}
        {(tokensPerSecond || maxTokensPerSecond || activeChat.tokenUsage?.total > 0) && (
          <div className="chat-token-info">
            {tokensPerSecond !== null && <span><i className="fas fa-tachometer-alt" /> {tokensPerSecond} tok/s</span>}
            {maxTokensPerSecond !== null && !isStreaming && (
              <span className="chat-max-tps-badge">
                <i className="fas fa-bolt" /> Peak: {maxTokensPerSecond} tok/s
              </span>
            )}
            {activeChat.tokenUsage?.total > 0 && (
              <span>
                <i className="fas fa-coins" /> {activeChat.tokenUsage.prompt}p + {activeChat.tokenUsage.completion}c = {activeChat.tokenUsage.total}
              </span>
            )}
          </div>
        )}

        {/* File badges */}
        {files.length > 0 && (
          <div className="chat-files">
            {files.map((f, i) => (
              <span key={i} className="chat-file-badge">
                <i className={`fas ${f.type?.startsWith('image/') ? 'fa-image' : f.type?.startsWith('audio/') ? 'fa-headphones' : 'fa-file'}`} />
                {f.name}
                <button onClick={() => setFiles(prev => prev.filter((_, idx) => idx !== i))}>
                  <i className="fas fa-xmark" />
                </button>
              </span>
            ))}
          </div>
        )}

        {/* Input area */}
        <div className="chat-input-area">
          <div className="chat-input-wrapper">
            <button
              type="button"
              className="btn btn-secondary btn-sm chat-attach-btn"
              onClick={() => fileInputRef.current?.click()}
              title="Attach file"
            >
              <i className="fas fa-paperclip" />
            </button>
            <input
              ref={fileInputRef}
              type="file"
              multiple
              accept="image/*,audio/*,application/pdf,.txt,.md,.csv,.json"
              style={{ display: 'none' }}
              onChange={handleFileChange}
            />
            <textarea
              ref={textareaRef}
              className="chat-input"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Message..."
              rows={1}
              disabled={isStreaming}
            />
            {isStreaming ? (
              <button className="chat-stop-btn" onClick={stopGeneration} title="Stop generating">
                <i className="fas fa-stop" />
              </button>
            ) : (
              <button
                id="chat-submit-btn"
                className="chat-send-btn"
                onClick={handleSend}
                disabled={!input.trim() && files.length === 0}
              >
                <i className="fas fa-paper-plane" />
              </button>
            )}
          </div>
        </div>
      </div>
      {canvasOpen && artifacts.length > 0 && (
        <CanvasPanel
          artifacts={artifacts}
          selectedId={selectedArtifactId}
          onSelect={setSelectedArtifactId}
          onClose={() => setCanvasOpen(false)}
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
