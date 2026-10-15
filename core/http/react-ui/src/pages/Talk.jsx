import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { useOutletContext, useNavigate, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { realtimeApi } from '../utils/api'
import { fromState } from '../utils/editorNav'
import { copyToClipboard } from '../utils/clipboard'
// eslint-disable-next-line no-unused-vars
import HomeModelPicker from '../components/home/HomeModelPicker'
// eslint-disable-next-line no-unused-vars
import SessionSheet from '../components/talk/SessionSheet'
// eslint-disable-next-line no-unused-vars
import VoiceVisualizer from '../components/VoiceVisualizer'
import { useMCPClient } from '../hooks/useMCPClient'
import { loadClientMCPServers } from '../utils/mcpClientStorage'
import { useAuth } from '../context/AuthContext'
import Icon from '../components/Icon'
import './talk.css'

// upsertEntry merges a streamed transcript fragment into the entry identified
// by the server's item_id, or appends a new entry (with the given role) if
// none exists yet. Keying by item_id (not a mutable index tracked across
// handler/updater boundaries) makes streamed deltas idempotent and
// order-independent, so React's batching of non-React data-channel events
// cannot produce a duplicate bubble. mode 'append' adds to the running text;
// 'replace' sets the final transcript — the server sends a completed event
// whose authoritative text supersedes any live captions (e.g. the
// semantic_vad retranscribe gate's batch decode).
function upsertEntry(prev, itemId, role, text, mode) {
  // The streaming entry is almost always the newest — search from the tail
  // so per-delta cost stays constant.
  const i = prev.findLastIndex(e => e.id === itemId)
  if (i === -1) {
    return [...prev, { role, id: itemId, text }]
  }
  const next = [...prev]
  next[i] = { ...next[i], text: mode === 'append' ? next[i].text + text : text }
  return next
}

function upsertAssistant(prev, itemId, text, mode) {
  return upsertEntry(prev, itemId, 'assistant', text, mode)
}

// The colours the diagnostics canvases draw with, read from the theme.
function diagColors() {
  const cs = getComputedStyle(document.documentElement)
  const get = (name, fallback) => cs.getPropertyValue(name).trim() || fallback
  return {
    bg: get('--dk-inset', '#111'),
    line: get('--dk-accent-text', '#3a8'),
    bar: get('--dk-series-1', '#38c'),
    muted: get('--dk-muted', '#888'),
    error: get('--dk-error', '#c33'),
  }
}

// What the page is showing, from what the connection is doing. These are the
// states the code can reach: nothing is simulated.
//   nopipe       no pipeline model is installed
//   idle         no session
//   connecting   the call is being set up (microphone, offer, answer, session)
//   listening    the session is open and the server is waiting for you
//   thinking     you stopped; the model is working or a tool is running
//   speaking     the reply is playing
//   blocked      the browser refused the microphone
//   lost         the WebRTC link failed during a session
//   error        anything else that stopped the session
function viewOf(status, noPipeline) {
  if (noPipeline) return 'nopipe'
  switch (status) {
    case 'connecting':
    case 'connected': return 'connecting'
    case 'listening': return 'listening'
    case 'thinking': return 'thinking'
    case 'speaking': return 'speaking'
    case 'blocked': return 'blocked'
    case 'lost': return 'lost'
    case 'error': return 'error'
    default: return 'idle'
  }
}

export default function Talk() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const location = useLocation()
  const { t } = useTranslation('talk')

  // Pipeline models
  const [pipelineModels, setPipelineModels] = useState([])
  const pickerModels = useMemo(() => pipelineModels.map(m => ({ id: m.name })), [pipelineModels])
  const [selectedModel, setSelectedModel] = useState('')
  const [modelsLoading, setModelsLoading] = useState(true)

  // Connection state. `detail` is a small, translated line under the status
  // (a tool that is running, the reason a call failed).
  const [status, setStatus] = useState('disconnected')
  const [detail, setDetail] = useState(null)
  const [isConnected, setIsConnected] = useState(false)
  // True after a reply was cut off, until you speak again or the next reply plays.
  const [interrupted, setInterrupted] = useState(false)
  const [hearing, setHearing] = useState(false)
  const [sheetOpen, setSheetOpen] = useState(false)

  // Transcript
  const [transcript, setTranscript] = useState([])
  // item_id of the assistant message currently streaming — used only to remove
  // its partial bubble when a response is cancelled (barge-in). The transcript
  // itself is keyed by item_id via upsertAssistant, not by this ref.
  const inProgressIdRef = useRef(null)

  // Session settings
  const [instructions, setInstructions] = useState(
    'You are a helpful voice assistant. Your responses will be spoken aloud using text-to-speech, so keep them concise and conversational. Do not use markdown formatting, bullet points, numbered lists, code blocks, or special characters. Speak naturally as you would in a phone conversation.'
  )
  const [voice, setVoice] = useState('')
  const [voiceEdited, setVoiceEdited] = useState(false)
  const [language, setLanguage] = useState('')

  // Client MCP — mirrors the chat page's wiring (useMCPClient + ClientMCPDropdown).
  // Talk has a single ephemeral session, so the active server set lives in component
  // state rather than per-chat config.
  const [clientMCPServers, setClientMCPServers] = useState(() => loadClientMCPServers())
  const [activeMCPIds, setActiveMCPIds] = useState([])
  const {
    connect: mcpConnect,
    disconnect: mcpDisconnect,
    getToolsForLLM,
    isClientTool,
    executeTool,
    connectionStatuses,
    getConnectedTools,
  } = useMCPClient()

  // LocalAI Assistant ("Manage Mode") — mirrors the chat-page toggle.
  // Admin-only; the realtime endpoint enforces the gate too. When on, the
  // backend mounts the in-process MCP admin tool surface for this session.
  const { isAdmin } = useAuth()
  const [manageMode, setManageMode] = useState(false)

  // Diagnostics
  const [diagVisible, setDiagVisible] = useState(false)

  // Refs for WebRTC / audio
  const pcRef = useRef(null)
  const dcRef = useRef(null)
  const localStreamRef = useRef(null)
  const audioRef = useRef(null)
  const hasErrorRef = useRef(false)

  // Diagnostics refs
  const audioCtxRef = useRef(null)
  const analyserRef = useRef(null)
  const diagFrameRef = useRef(null)
  const statsIntervalRef = useRef(null)
  const waveCanvasRef = useRef(null)
  const specCanvasRef = useRef(null)
  const transcriptEndRef = useRef(null)

  // Diagnostics stats (not worth re-rendering for every frame)
  const [diagStats, setDiagStats] = useState({
    peakFreq: '--', thd: '--', rms: '--', sampleRate: '--',
    packetsRecv: '--', packetsLost: '--', jitter: '--', concealed: '--', raw: '',
  })

  // Fetch pipeline models on mount
  useEffect(() => {
    realtimeApi.pipelineModels()
      .then(models => {
        setPipelineModels(models || [])
        if (models?.length > 0) {
          setSelectedModel(models[0].name)
          if (!voiceEdited) setVoice(models[0].voice || '')
        }
      })
      .catch(err => addToast(t('toasts.modelsFailed', { message: err.message }), 'error', 5000, { link: { href: '/app/traces?tab=backend', text: t('toasts.viewTraces') } }))
      .finally(() => setModelsLoading(false))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Auto-scroll the transcript's own overflow container. scrollIntoView bubbles
  // to every scrollable ancestor (incl. the window), which yanked the whole
  // page down to the transcript box on mount; scoping to the box avoids it.
  useEffect(() => {
    const box = transcriptEndRef.current?.parentElement
    box?.scrollTo({ top: box.scrollHeight, behavior: 'smooth' })
  }, [transcript])

  // Mirror Chat.jsx: connect / disconnect client MCP servers as the user toggles them.
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeMCPIds.join(','), clientMCPServers, connectionStatuses, mcpConnect, mcpDisconnect])

  const handleClientMCPToggle = useCallback((serverId) => {
    setActiveMCPIds(prev => prev.includes(serverId) ? prev.filter(s => s !== serverId) : [...prev, serverId])
  }, [])
  const handleClientMCPServerAdded = useCallback((server) => {
    setClientMCPServers(loadClientMCPServers())
    setActiveMCPIds(prev => prev.includes(server.id) ? prev : [...prev, server.id])
  }, [])
  const handleClientMCPServerRemoved = useCallback(async (id) => {
    await mcpDisconnect(id)
    setClientMCPServers(loadClientMCPServers())
    setActiveMCPIds(prev => prev.filter(s => s !== id))
  }, [mcpDisconnect])

  const selectedModelInfo = pipelineModels.find(m => m.name === selectedModel)

  // ── Status helper ──
  const updateStatus = useCallback((state, line) => {
    setStatus(state)
    setDetail(line || null)
  }, [])

  // ── Session update ──
  const sendSessionUpdate = useCallback(() => {
    const dc = dcRef.current
    if (!dc || dc.readyState !== 'open') return

    const tools = getToolsForLLM()
    if (!instructions.trim() && !voice.trim() && !language.trim() && tools.length === 0) return

    const session = {}
    if (instructions.trim()) session.instructions = instructions.trim()
    if (voice.trim() || language.trim()) {
      session.audio = {}
      if (voice.trim()) session.audio.output = { voice: voice.trim() }
      if (language.trim()) session.audio.input = { transcription: { language: language.trim() } }
    }
    // Pass MCP-server-advertised tools straight through. Server-side they
    // get rendered into the model's prompt via the function:/argument_regex
    // pair on the model config (gallery/lfm.yaml for LFM2.5-Audio).
    if (tools.length > 0) session.tools = tools

    dc.send(JSON.stringify({ type: 'session.update', session }))
  }, [instructions, voice, language, getToolsForLLM])

  // Re-send session.update whenever the tool set changes mid-session so the
  // model sees newly-toggled MCP servers without a reconnect.
  useEffect(() => {
    if (isConnected) sendSessionUpdate()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeMCPIds.join(',')])

  // ── Function-call dispatcher ──
  // Mirrors the chat-page agentic loop: collect args from the model's
  // function_call_arguments.done event, hand them to the MCP client's
  // executeTool, then echo the result back via conversation.item.create +
  // response.create so the model can complete its turn with the tool output.
  const handleFunctionCall = useCallback(async (event) => {
    const dc = dcRef.current
    if (!dc || dc.readyState !== 'open') return
    const { call_id: callId, name, arguments: argsJson } = event
    if (!callId || !name) return
    if (!isClientTool(name)) {
      // No MCP server advertises this tool — let the model know so it can
      // recover instead of hanging.
      dc.send(JSON.stringify({
        type: 'conversation.item.create',
        item: { type: 'function_call_output', call_id: callId, output: `Error: unknown tool "${name}"` },
      }))
      dc.send(JSON.stringify({ type: 'response.create' }))
      return
    }
    updateStatus('thinking', { key: 'tool', params: { name } })
    try {
      const result = await executeTool(name, argsJson)
      dc.send(JSON.stringify({
        type: 'conversation.item.create',
        item: { type: 'function_call_output', call_id: callId, output: typeof result === 'string' ? result : JSON.stringify(result) },
      }))
      dc.send(JSON.stringify({ type: 'response.create' }))
    } catch (err) {
      dc.send(JSON.stringify({
        type: 'conversation.item.create',
        item: { type: 'function_call_output', call_id: callId, output: `Error: ${err?.message || err}` },
      }))
      dc.send(JSON.stringify({ type: 'response.create' }))
    }
  }, [executeTool, isClientTool, updateStatus])

  // ── Server event handler ──
  const handleServerEvent = useCallback((event) => {
    switch (event.type) {
      case 'session.created':
        sendSessionUpdate()
        updateStatus('listening')
        break
      case 'session.updated':
        break
      case 'input_audio_buffer.speech_started':
        setHearing(true)
        setInterrupted(false)
        updateStatus('listening')
        break
      case 'input_audio_buffer.speech_stopped':
        setHearing(false)
        updateStatus('thinking', { key: 'processing' })
        break
      case 'conversation.item.input_audio_transcription.delta':
        // Live captions: semantic_vad streams the user's words while they
        // are still speaking, keyed by the item id the commit will reuse.
        if (event.delta && event.item_id) {
          setTranscript(prev => upsertEntry(prev, event.item_id, 'user', event.delta, 'append'))
        }
        break
      case 'conversation.item.input_audio_transcription.completed':
        if (event.transcript) {
          if (event.item_id) {
            // Replaces any live captions with the authoritative transcript
            // (which may differ, e.g. the retranscribe gate's batch decode);
            // creates the entry when there were none (server_vad).
            setTranscript(prev => upsertEntry(prev, event.item_id, 'user', event.transcript, 'replace'))
          } else {
            setTranscript(prev => [...prev, { role: 'user', text: event.transcript }])
          }
        }
        updateStatus('thinking', { key: 'generating' })
        break
      case 'conversation.item.input_audio_transcription.failed':
        // The turn was discarded after captions were shown (e.g. the buffer
        // was cleared as silence) — retract the partial entry.
        if (event.item_id) {
          setTranscript(prev => prev.filter(e => e.id !== event.item_id))
        }
        break
      case 'response.output_audio_transcript.delta':
        if (event.delta) {
          inProgressIdRef.current = event.item_id
          setTranscript(prev => upsertAssistant(prev, event.item_id, event.delta, 'append'))
        }
        break
      case 'response.output_audio_transcript.done':
        if (event.transcript) {
          setTranscript(prev => upsertAssistant(prev, event.item_id, event.transcript, 'replace'))
        }
        inProgressIdRef.current = null
        break
      case 'response.output_audio.delta':
        setInterrupted(false)
        updateStatus('speaking')
        break
      case 'response.output_item.done': {
        // Server-executed tools (Manage Mode) surface as output items —
        // FunctionCall when the model invokes a tool, FunctionCallOutput
        // once the server has run it. Render both on `done` so we get
        // each transcript entry exactly once.
        const item = event.item
        if (!item) break
        if (item.FunctionCall) {
          setTranscript(prev => [...prev, {
            role: 'tool_call',
            text: `${item.FunctionCall.name}(${item.FunctionCall.arguments || ''})`,
          }])
        } else if (item.FunctionCallOutput) {
          let preview = item.FunctionCallOutput.output || ''
          // Pretty-print JSON for readability; fall back to raw string.
          try { preview = JSON.stringify(JSON.parse(preview), null, 2) } catch { /* keep raw */ }
          setTranscript(prev => [...prev, { role: 'tool_result', text: preview }])
          inProgressIdRef.current = null // tool result ends the current assistant text run
        }
        break
      }
      case 'response.function_call_arguments.done':
        // Don't await — keep the event loop free; handleFunctionCall sends
        // conversation.item.create + response.create when it's done.
        handleFunctionCall(event)
        break
      case 'response.done': {
        // A cancelled response (barge-in / interruption) leaves a partial,
        // incrementally-streamed assistant bubble behind. The server discards
        // the interrupted item from history; mirror that here (remove the
        // in-progress assistant entry by item_id) so the regenerated reply
        // doesn't show up as a second assistant message. A quiet note marks
        // where the reply was cut.
        if (event.response?.status === 'cancelled') {
          const id = inProgressIdRef.current
          inProgressIdRef.current = null
          setTranscript(prev => {
            const kept = id ? prev.filter(e => e.id !== id) : prev
            return [...kept, { role: 'note', text: 'interrupted' }]
          })
          setInterrupted(true)
        }
        updateStatus('listening')
        break
      }
      case 'error':
        hasErrorRef.current = true
        updateStatus('error', { key: 'server', params: { message: event.error?.message || t('detail.unknown') } })
        break
    }
  }, [sendSessionUpdate, updateStatus, handleFunctionCall, t])

  // ── Connect ──
  const connect = useCallback(async () => {
    if (!selectedModel) {
      addToast(t('toasts.selectModel'), 'warning')
      return
    }
    if (!navigator.mediaDevices?.getUserMedia) {
      updateStatus('error', { key: 'insecure' })
      return
    }

    hasErrorRef.current = false
    setInterrupted(false)
    setHearing(false)
    updateStatus('connecting')
    setIsConnected(true)

    try {
      const localStream = await navigator.mediaDevices.getUserMedia({ audio: true })
      localStreamRef.current = localStream

      const pc = new RTCPeerConnection({})
      pcRef.current = pc

      for (const track of localStream.getAudioTracks()) {
        pc.addTrack(track, localStream)
      }

      pc.ontrack = (event) => {
        if (audioRef.current) audioRef.current.srcObject = event.streams[0]
        if (diagVisible) startDiagnostics()
      }

      const dc = pc.createDataChannel('oai-events')
      dcRef.current = dc
      dc.onmessage = (msg) => {
        try {
          const text = typeof msg.data === 'string' ? msg.data : new TextDecoder().decode(msg.data)
          handleServerEvent(JSON.parse(text))
        } catch (e) {
          console.error('Failed to parse server event:', e)
        }
      }
      dc.onclose = () => console.log('Data channel closed')

      pc.onconnectionstatechange = () => {
        if (pc.connectionState === 'connected') {
          updateStatus('connected')
        } else if (pc.connectionState === 'failed') {
          // The link dropped under a session that was running.
          hasErrorRef.current = true
          updateStatus('lost')
          disconnect()
        } else if (pc.connectionState === 'closed') {
          disconnect()
        }
      }

      const offer = await pc.createOffer()
      await pc.setLocalDescription(offer)

      await new Promise((resolve) => {
        if (pc.iceGatheringState === 'complete') return resolve()
        pc.onicegatheringstatechange = () => {
          if (pc.iceGatheringState === 'complete') resolve()
        }
        setTimeout(resolve, 5000)
      })

      const data = await realtimeApi.call({
        sdp: pc.localDescription.sdp,
        model: selectedModel,
        localai_assistant: manageMode,
      })

      await pc.setRemoteDescription({ type: 'answer', sdp: data.sdp })
    } catch (err) {
      hasErrorRef.current = true
      if (err?.name === 'NotAllowedError' || err?.name === 'SecurityError' || err?.name === 'PermissionDeniedError') {
        updateStatus('blocked')
      } else if (err?.name === 'NotFoundError') {
        updateStatus('error', { key: 'noMic' })
      } else {
        updateStatus('error', { key: 'failed', params: { message: err?.message || t('detail.unknown') } })
      }
      disconnect()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedModel, manageMode, diagVisible, handleServerEvent, updateStatus, addToast, t])

  // ── Disconnect ──
  const disconnect = useCallback(() => {
    stopDiagnostics()
    if (dcRef.current) { dcRef.current.close(); dcRef.current = null }
    if (pcRef.current) { pcRef.current.close(); pcRef.current = null }
    if (localStreamRef.current) {
      localStreamRef.current.getTracks().forEach(t => t.stop())
      localStreamRef.current = null
    }
    if (audioRef.current) audioRef.current.srcObject = null

    if (!hasErrorRef.current) updateStatus('disconnected')
    hasErrorRef.current = false
    setIsConnected(false)
    setHearing(false)
    setInterrupted(false)
  }, [updateStatus])

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      stopDiagnostics()
      if (dcRef.current) dcRef.current.close()
      if (pcRef.current) pcRef.current.close()
      if (localStreamRef.current) localStreamRef.current.getTracks().forEach(t => t.stop())
    }
  }, [])

  // ── Test tone ──
  const sendTestTone = useCallback(() => {
    const dc = dcRef.current
    if (!dc || dc.readyState !== 'open') return
    dc.send(JSON.stringify({ type: 'test_tone' }))
    setTranscript(prev => [...prev, { role: 'note', text: 'tone' }])
  }, [])

  // ── Diagnostics ──
  function startDiagnostics() {
    const audioEl = audioRef.current
    if (!audioEl?.srcObject) return

    if (!audioCtxRef.current) {
      const ctx = new AudioContext()
      const source = ctx.createMediaStreamSource(audioEl.srcObject)
      const analyser = ctx.createAnalyser()
      analyser.fftSize = 8192
      analyser.smoothingTimeConstant = 0.3
      source.connect(analyser)
      audioCtxRef.current = ctx
      analyserRef.current = analyser
      setDiagStats(prev => ({ ...prev, sampleRate: ctx.sampleRate + ' Hz' }))
    }

    if (!diagFrameRef.current) drawDiagnostics()
    if (!statsIntervalRef.current) {
      pollWebRTCStats()
      statsIntervalRef.current = setInterval(pollWebRTCStats, 1000)
    }
  }

  function stopDiagnostics() {
    if (diagFrameRef.current) { cancelAnimationFrame(diagFrameRef.current); diagFrameRef.current = null }
    if (statsIntervalRef.current) { clearInterval(statsIntervalRef.current); statsIntervalRef.current = null }
    if (audioCtxRef.current) { audioCtxRef.current.close(); audioCtxRef.current = null; analyserRef.current = null }
  }

  function drawDiagnostics() {
    const analyser = analyserRef.current
    if (!analyser) { diagFrameRef.current = null; return }
    const colors = diagColors()

    diagFrameRef.current = requestAnimationFrame(drawDiagnostics)

    // Waveform
    const waveCanvas = waveCanvasRef.current
    if (waveCanvas) {
      const wCtx = waveCanvas.getContext('2d')
      const timeData = new Float32Array(analyser.fftSize)
      analyser.getFloatTimeDomainData(timeData)
      const w = waveCanvas.width, h = waveCanvas.height
      wCtx.fillStyle = colors.bg; wCtx.fillRect(0, 0, w, h)
      wCtx.strokeStyle = colors.line; wCtx.lineWidth = 1; wCtx.beginPath()
      const sliceWidth = w / timeData.length
      let x = 0
      for (let i = 0; i < timeData.length; i++) {
        const y = (1 - timeData[i]) * h / 2
        i === 0 ? wCtx.moveTo(x, y) : wCtx.lineTo(x, y)
        x += sliceWidth
      }
      wCtx.stroke()

      let sumSq = 0
      for (let i = 0; i < timeData.length; i++) sumSq += timeData[i] * timeData[i]
      const rms = Math.sqrt(sumSq / timeData.length)
      const rmsDb = rms > 0 ? (20 * Math.log10(rms)).toFixed(1) : '-Inf'
      setDiagStats(prev => ({ ...prev, rms: rmsDb + ' dBFS' }))
    }

    // Spectrum
    const specCanvas = specCanvasRef.current
    if (specCanvas && audioCtxRef.current) {
      const sCtx = specCanvas.getContext('2d')
      const freqData = new Float32Array(analyser.frequencyBinCount)
      analyser.getFloatFrequencyData(freqData)
      const sw = specCanvas.width, sh = specCanvas.height
      sCtx.fillStyle = colors.bg; sCtx.fillRect(0, 0, sw, sh)

      const sampleRate = audioCtxRef.current.sampleRate
      const binHz = sampleRate / analyser.fftSize
      const maxFreqDisplay = 4000
      const maxBin = Math.min(Math.ceil(maxFreqDisplay / binHz), freqData.length)
      const barWidth = sw / maxBin

      sCtx.fillStyle = colors.bar
      let peakBin = 0, peakVal = -Infinity
      for (let i = 0; i < maxBin; i++) {
        const db = freqData[i]
        if (db > peakVal) { peakVal = db; peakBin = i }
        const barH = Math.max(0, ((db + 100) / 100) * sh)
        sCtx.fillRect(i * barWidth, sh - barH, Math.max(1, barWidth - 0.5), barH)
      }

      // Frequency labels
      sCtx.fillStyle = colors.muted; sCtx.font = '10px ui-monospace, monospace'
      for (let f = 500; f <= maxFreqDisplay; f += 500) {
        sCtx.fillText(f + '', (f / binHz) * barWidth - 10, sh - 2)
      }

      // 440 Hz marker
      const bin440 = Math.round(440 / binHz)
      const x440 = bin440 * barWidth
      sCtx.strokeStyle = colors.error; sCtx.lineWidth = 1
      sCtx.beginPath(); sCtx.moveTo(x440, 0); sCtx.lineTo(x440, sh); sCtx.stroke()
      sCtx.fillStyle = colors.error; sCtx.fillText('440', x440 + 2, 10)

      const peakFreq = peakBin * binHz
      const fundamentalBin = Math.round(440 / binHz)
      const fundamentalPower = Math.pow(10, freqData[fundamentalBin] / 10)
      let harmonicPower = 0
      for (let h = 2; h <= 10; h++) {
        const hBin = Math.round(440 * h / binHz)
        if (hBin < freqData.length) harmonicPower += Math.pow(10, freqData[hBin] / 10)
      }
      const thd = fundamentalPower > 0
        ? (Math.sqrt(harmonicPower / fundamentalPower) * 100).toFixed(1) + '%'
        : '--%'

      setDiagStats(prev => ({
        ...prev,
        peakFreq: peakFreq.toFixed(0) + ' Hz (' + peakVal.toFixed(1) + ' dB)',
        thd,
      }))
    }
  }

  async function pollWebRTCStats() {
    const pc = pcRef.current
    if (!pc) return
    try {
      const stats = await pc.getStats()
      const raw = []
      stats.forEach((report) => {
        if (report.type === 'inbound-rtp' && report.kind === 'audio') {
          setDiagStats(prev => ({
            ...prev,
            packetsRecv: report.packetsReceived ?? '--',
            packetsLost: report.packetsLost ?? '--',
            jitter: report.jitter !== undefined ? (report.jitter * 1000).toFixed(1) + ' ms' : '--',
            concealed: report.concealedSamples ?? '--',
          }))
          raw.push('-- inbound-rtp (audio) --')
          raw.push('  packetsReceived: ' + report.packetsReceived)
          raw.push('  packetsLost: ' + report.packetsLost)
          raw.push('  jitter: ' + (report.jitter !== undefined ? (report.jitter * 1000).toFixed(2) + ' ms' : 'N/A'))
          raw.push('  bytesReceived: ' + report.bytesReceived)
          raw.push('  concealedSamples: ' + report.concealedSamples)
          raw.push('  totalSamplesReceived: ' + report.totalSamplesReceived)
        }
      })
      setDiagStats(prev => ({ ...prev, raw: raw.join('\n') }))
    } catch { /* stats polling error */ }
  }

  const toggleDiagnostics = useCallback(() => {
    setDiagVisible(prev => {
      const next = !prev
      if (next) {
        setTimeout(startDiagnostics, 0)
      } else {
        stopDiagnostics()
      }
      return next
    })
  }, [])

  const noPipeline = !modelsLoading && pipelineModels.length === 0
  const view = viewOf(status, noPipeline)
  const showInterrupted = interrupted && view === 'listening'

  // The heading and the sentence under it. Failures keep the reason the code
  // has (a server message, the browser's error) in the detail line.
  const headline = showInterrupted ? t('view.interrupted.title') : t(`view.${view}.title`)
  let sentence = showInterrupted ? t('view.interrupted.body') : t(`view.${view}.body`)
  if (view === 'listening' && hearing && !showInterrupted) sentence = t('view.listening.hearing')
  if (detail) sentence = t(`detail.${detail.key}`, detail.params)

  const failed = view === 'blocked' || view === 'lost' || view === 'error'
  const busy = view === 'connecting' || view === 'thinking'

  const copyTranscript = async () => {
    const lines = transcript
      .filter(e => e.role !== 'note')
      .map(e => `${t(`transcript.${e.role}`)}: ${e.text}`)
    const ok = await copyToClipboard(lines.join('\n'))
    addToast(ok ? t('toasts.copied') : t('toasts.copyFailed'), ok ? 'success' : 'error', ok ? 2000 : 3000)
  }

  const openEditor = () => {
    if (!selectedModel) return
    navigate(`/app/model-editor/${encodeURIComponent(selectedModel)}`, { state: fromState(location, 'Talk') })
  }

  return (
    <div className="talk-page" data-view={view}>
      <header className="talk-hd">
        <h1>{t('title')}</h1>
        <span className="talk-hd__sub">{t('subtitle')}</span>
        <div className="talk-hd__acts">
          {isConnected && (
            <button
              type="button"
              className="talk-icobtn"
              onClick={toggleDiagnostics}
              aria-pressed={diagVisible}
              title={t('controls.diagnostics')}
              aria-label={t('controls.diagnostics')}
              data-testid="talk-diag-toggle"
            >
              <Icon name="gauge" />
            </button>
          )}
          <button
            type="button"
            className="talk-icobtn"
            onClick={() => setSheetOpen(true)}
            title={t('settings.title')}
            aria-label={t('settings.title')}
            data-testid="talk-settings-button"
          >
            <Icon name="sliders" />
          </button>
        </div>
      </header>

      <div className="talk-grid">
        <section className="talk-stage" aria-label={t('stage')}>
          <div className="talk-chips">
            <HomeModelPicker
              value={selectedModel}
              onChange={(v) => {
                setSelectedModel(v)
                const m = pipelineModels.find(p => p.name === v)
                if (m && !voiceEdited) setVoice(m.voice || '')
              }}
              models={pickerModels}
              loading={modelsLoading}
              disabled={isConnected || noPipeline}
              showWarm={false}
              placeholder={noPipeline ? t('picker.none') : undefined}
              labels={{ title: t('picker.title'), heading: t('picker.heading'), label: t('picker.label'), none: t('picker.none') }}
            />
            {!noPipeline && (
              <>
                <button type="button" className="home-chip talk-chip" onClick={() => setSheetOpen(true)} title={t('settings.voice')}>
                  <Icon name="volume" />
                  <span className="home-chip__text">{voice || t('picker.defaultVoice')}</span>
                </button>
                <button type="button" className="home-chip talk-chip" onClick={() => setSheetOpen(true)} title={t('settings.language')}>
                  <Icon name="globe" />
                  <span className="home-chip__text">{language || t('picker.auto')}</span>
                </button>
              </>
            )}
          </div>

          {view === 'nopipe' ? (
            <section className="talk-card" data-testid="talk-no-pipeline" aria-labelledby="talk-nopipe-title">
              <h2 id="talk-nopipe-title">{t('view.nopipe.title')}</h2>
              <p>{t('view.nopipe.body')}</p>
              <div className="talk-card__acts">
                <button type="button" className="home-primary" onClick={() => navigate('/app/model-editor?template=pipeline', { state: fromState(location, 'Talk') })}>
                  <Icon name="plus" /> {t('view.nopipe.create')}
                </button>
                <button type="button" className="home-secondary" onClick={() => navigate('/app/models')}>
                  <Icon name="store" /> {t('view.nopipe.gallery')}
                </button>
              </div>
            </section>
          ) : (
            <>
              <div className="talk-orb" data-view={view}>
                <VoiceVisualizer audioRef={audioRef} micStreamRef={localStreamRef} status={status} active={isConnected} interrupted={showInterrupted} />
              </div>

              <div className="talk-status" role="status" aria-live="polite" data-testid="talk-status" data-state={showInterrupted ? 'interrupted' : view}>
                <h2>
                  {busy && <Icon name="spinner" spin />}
                  <span>{headline}</span>
                </h2>
                <p>{sentence}</p>
              </div>

              {view === 'blocked' && (
                <div className="talk-alert" role="alert" data-testid="talk-blocked">
                  <Icon name="alert-circle" />
                  <div>
                    <h3>{t('view.blocked.cardTitle')}</h3>
                    <p>{t('view.blocked.card')}</p>
                  </div>
                </div>
              )}
              {view === 'lost' && (
                <div className="talk-alert" role="alert" data-testid="talk-lost">
                  <Icon name="alert-circle" />
                  <div>
                    <h3>{t('view.lost.cardTitle')}</h3>
                    <p>{t('view.lost.card')}</p>
                  </div>
                </div>
              )}

              <div className="talk-ctl">
                {!isConnected ? (
                  <button
                    type="button"
                    className="talk-start"
                    onClick={connect}
                    disabled={modelsLoading || !selectedModel}
                    data-testid="talk-start"
                  >
                    <span>{failed ? t(`controls.retry.${view}`) : t('controls.start')}</span>
                    <span className="talk-start__cell"><Icon name="mic" /></span>
                  </button>
                ) : (
                  <>
                    <button type="button" className="talk-btn talk-btn--end" onClick={disconnect} data-testid="talk-end">
                      <Icon name="plug-off" /> {t('controls.end')}
                    </button>
                    {view !== 'connecting' && (
                      <button type="button" className="talk-btn" onClick={sendTestTone} data-testid="talk-test-tone">
                        <Icon name="waveform" /> {t('controls.testTone')}
                      </button>
                    )}
                  </>
                )}
                {(view === 'error' || view === 'lost') && (
                  <a href="/app/traces?tab=backend" className="chat-error-trace-link">
                    <Icon name="waveform" /> {t('controls.viewTraces')}
                  </a>
                )}
              </div>
              {!isConnected && view === 'idle' && <p className="talk-note">{t('view.idle.note')}</p>}

              {isConnected && diagVisible && (
                <div className="talk-diag" data-testid="talk-diag">
                  <h3>{t('diag.title')}</h3>
                  <div className="talk-split">
                    <div>
                      <p className="talk-diag__label">{t('diag.waveform')}</p>
                      <canvas ref={waveCanvasRef} width={400} height={120} className="talk-canvas" />
                    </div>
                    <div>
                      <p className="talk-diag__label">{t('diag.spectrum')}</p>
                      <canvas ref={specCanvasRef} width={400} height={120} className="talk-canvas" />
                    </div>
                  </div>
                  <dl className="talk-diag__grid">
                    {[
                      ['peakFreq', diagStats.peakFreq],
                      ['thd', diagStats.thd],
                      ['rms', diagStats.rms],
                      ['sampleRate', diagStats.sampleRate],
                      ['packetsRecv', diagStats.packetsRecv],
                      ['packetsLost', diagStats.packetsLost],
                      ['jitter', diagStats.jitter],
                      ['concealed', diagStats.concealed],
                    ].map(([key, value]) => (
                      <div key={key} className="talk-diag__cell">
                        <dt>{t(`diag.${key}`)}</dt>
                        <dd>{value}</dd>
                      </div>
                    ))}
                  </dl>
                  <pre className="talk-diag__raw">{diagStats.raw || t('diag.waiting')}</pre>
                </div>
              )}
            </>
          )}
        </section>

        <section className="talk-transcript" aria-label={t('transcript.title')}>
          <div className="talk-transcript__head">
            <h2>{t('transcript.title')}</h2>
            <button type="button" className="talk-btn talk-btn--ghost" onClick={copyTranscript} disabled={transcript.length === 0} data-testid="talk-copy">
              <Icon name="copy" /> {t('transcript.copy')}
            </button>
          </div>
          <div className="talk-transcript__body" data-testid="talk-transcript">
            {transcript.length === 0 && (
              <p className="talk-transcript__empty">{noPipeline ? t('transcript.emptyNoPipeline') : t('transcript.empty')}</p>
            )}
            {transcript.map((entry, i) => {
              if (entry.role === 'note') {
                return <p key={entry.id || i} className="talk-turn talk-turn--note" data-role="note"><span className="talk-tag">{entry.text === 'tone' ? t('transcript.toneRequested') : t('transcript.interrupted')}</span></p>
              }
              const tool = entry.role === 'tool_call' || entry.role === 'tool_result'
              return (
                <div key={entry.id || i} className="talk-turn" data-role={entry.role}>
                  <b>{t(`transcript.${entry.role}`)}</b>
                  <p className={tool ? `talk-turn__tool${entry.role === 'tool_result' ? ' talk-turn__tool--result' : ''}` : undefined}>{entry.text}</p>
                </div>
              )
            })}
            <div ref={transcriptEndRef} />
          </div>
        </section>
      </div>

      {/* Hidden audio element for WebRTC playback */}
      <audio ref={audioRef} autoPlay className="hidden" />

      {sheetOpen && (
        <SessionSheet
          connected={isConnected}
          isAdmin={isAdmin}
          instructions={instructions}
          onInstructions={setInstructions}
          voice={voice}
          onVoice={(v) => { setVoice(v); setVoiceEdited(true) }}
          language={language}
          onLanguage={setLanguage}
          manageMode={manageMode}
          onManageMode={setManageMode}
          pipeline={selectedModelInfo}
          onEditPipeline={openEditor}
          activeServerIds={activeMCPIds}
          onToggleServer={handleClientMCPToggle}
          onServerAdded={handleClientMCPServerAdded}
          onServerRemoved={handleClientMCPServerRemoved}
          connectionStatuses={connectionStatuses}
          getConnectedTools={getConnectedTools}
          onClose={() => setSheetOpen(false)}
        />
      )}
    </div>
  )
}
