import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { useOutletContext, useNavigate, useLocation } from 'react-router-dom'
import { realtimeApi } from '../utils/api'
import { fromState } from '../utils/editorNav'
import ModelSelector from '../components/ModelSelector'
import VoiceVisualizer from '../components/VoiceVisualizer'
import ClientMCPDropdown from '../components/ClientMCPDropdown'
import { useMCPClient } from '../hooks/useMCPClient'
import { loadClientMCPServers } from '../utils/mcpClientStorage'
import { useAuth } from '../context/AuthContext'

const STATUS_STYLES = {
  disconnected: { icon: 'fa-solid fa-circle', color: 'var(--color-text-secondary)', bg: 'transparent' },
  connecting:   { icon: 'fa-solid fa-spinner fa-spin', color: 'var(--color-primary)', bg: 'var(--color-primary-light)' },
  connected:    { icon: 'fa-solid fa-circle', color: 'var(--color-success)', bg: 'var(--color-success-light)' },
  listening:    { icon: 'fa-solid fa-microphone', color: 'var(--color-success)', bg: 'var(--color-success-light)' },
  thinking:     { icon: 'fa-solid fa-brain fa-beat', color: 'var(--color-primary)', bg: 'var(--color-primary-light)' },
  speaking:     { icon: 'fa-solid fa-volume-high fa-beat-fade', color: 'var(--color-accent)', bg: 'var(--color-accent-light)' },
  error:        { icon: 'fa-solid fa-circle', color: 'var(--color-error)', bg: 'var(--color-error-light)' },
}

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

export default function Talk() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const location = useLocation()

  // Pipeline models
  const [pipelineModels, setPipelineModels] = useState([])
  const pipelineModelNames = useMemo(() => pipelineModels.map(m => m.name), [pipelineModels])
  const [selectedModel, setSelectedModel] = useState('')
  const [modelsLoading, setModelsLoading] = useState(true)

  // Connection state
  const [status, setStatus] = useState('disconnected')
  const [statusText, setStatusText] = useState('Disconnected')
  const [isConnected, setIsConnected] = useState(false)

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
      .catch(err => addToast(`Failed to load realtime models: ${err.message}`, 'error', 5000, { link: { href: '/app/traces?tab=backend', text: 'View traces' } }))
      .finally(() => setModelsLoading(false))
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
  const updateStatus = useCallback((state, text) => {
    setStatus(state)
    setStatusText(text || state)
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
    updateStatus('thinking', `Running tool ${name}...`)
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
        updateStatus('listening', 'Listening...')
        break
      case 'session.updated':
        break
      case 'input_audio_buffer.speech_started':
        updateStatus('listening', 'Hearing you speak...')
        break
      case 'input_audio_buffer.speech_stopped':
        updateStatus('thinking', 'Processing...')
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
        updateStatus('thinking', 'Generating response...')
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
        updateStatus('speaking', 'Speaking...')
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
          try { preview = JSON.stringify(JSON.parse(preview), null, 2) } catch (_) { /* keep raw */ }
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
        // doesn't show up as a second assistant message.
        if (event.response?.status === 'cancelled' && inProgressIdRef.current) {
          const id = inProgressIdRef.current
          inProgressIdRef.current = null
          setTranscript(prev => prev.filter(e => e.id !== id))
        }
        updateStatus('listening', 'Listening...')
        break
      }
      case 'error':
        hasErrorRef.current = true
        updateStatus('error', 'Error: ' + (event.error?.message || 'Unknown error'))
        break
    }
  }, [sendSessionUpdate, updateStatus, handleFunctionCall])

  // ── Connect ──
  const connect = useCallback(async () => {
    if (!selectedModel) {
      addToast('Please select a realtime model first.', 'warning')
      return
    }
    if (!navigator.mediaDevices?.getUserMedia) {
      updateStatus('error', 'Microphone access requires HTTPS or localhost.')
      return
    }

    updateStatus('connecting', 'Connecting...')
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
          updateStatus('connected', 'Connected, waiting for session...')
        } else if (pc.connectionState === 'failed' || pc.connectionState === 'closed') {
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
      updateStatus('error', 'Connection failed: ' + err.message)
      disconnect()
    }
  }, [selectedModel, manageMode, diagVisible, handleServerEvent, updateStatus, addToast])

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

    if (!hasErrorRef.current) updateStatus('disconnected', 'Disconnected')
    hasErrorRef.current = false
    setIsConnected(false)
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
    setTranscript(prev => [...prev, { role: 'assistant', text: '(Test tone requested)' }])
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

    diagFrameRef.current = requestAnimationFrame(drawDiagnostics)

    // Waveform
    const waveCanvas = waveCanvasRef.current
    if (waveCanvas) {
      const wCtx = waveCanvas.getContext('2d')
      const timeData = new Float32Array(analyser.fftSize)
      analyser.getFloatTimeDomainData(timeData)
      const w = waveCanvas.width, h = waveCanvas.height
      wCtx.fillStyle = '#000'; wCtx.fillRect(0, 0, w, h)
      wCtx.strokeStyle = '#0f0'; wCtx.lineWidth = 1; wCtx.beginPath()
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
      sCtx.fillStyle = '#000'; sCtx.fillRect(0, 0, sw, sh)

      const sampleRate = audioCtxRef.current.sampleRate
      const binHz = sampleRate / analyser.fftSize
      const maxFreqDisplay = 4000
      const maxBin = Math.min(Math.ceil(maxFreqDisplay / binHz), freqData.length)
      const barWidth = sw / maxBin

      sCtx.fillStyle = '#0cf'
      let peakBin = 0, peakVal = -Infinity
      for (let i = 0; i < maxBin; i++) {
        const db = freqData[i]
        if (db > peakVal) { peakVal = db; peakBin = i }
        const barH = Math.max(0, ((db + 100) / 100) * sh)
        sCtx.fillRect(i * barWidth, sh - barH, Math.max(1, barWidth - 0.5), barH)
      }

      // Frequency labels
      sCtx.fillStyle = '#888'; sCtx.font = '10px monospace'
      for (let f = 500; f <= maxFreqDisplay; f += 500) {
        sCtx.fillText(f + '', (f / binHz) * barWidth - 10, sh - 2)
      }

      // 440 Hz marker
      const bin440 = Math.round(440 / binHz)
      const x440 = bin440 * barWidth
      sCtx.strokeStyle = '#f00'; sCtx.lineWidth = 1
      sCtx.beginPath(); sCtx.moveTo(x440, 0); sCtx.lineTo(x440, sh); sCtx.stroke()
      sCtx.fillStyle = '#f00'; sCtx.fillText('440', x440 + 2, 10)

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
    } catch (_e) { /* stats polling error */ }
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

  const statusStyle = STATUS_STYLES[status] || STATUS_STYLES.disconnected

  // ── Render ──
  return (
    <div className="page page--narrow talk-page">
      <div className="talk-col">
        <div className="text-center mb-lg">
          <h1 className="page-title">Talk</h1>
          <p className="page-subtitle">Real-time voice conversation via WebRTC</p>
        </div>

        <div className="card pad-lg mb-md">
          {/* Voice visualizer (hero) */}
          <VoiceVisualizer audioRef={audioRef} micStreamRef={localStreamRef} status={status} active={isConnected} />

          {/* Connection status */}
          <div
            className="talk-status mb-md"
            style={{
              background: statusStyle.bg,
              border: '1px solid color-mix(in srgb, ' + statusStyle.color + ' 30%, transparent)',
            }}
          >
            <i className={statusStyle.icon} style={{ color: statusStyle.color }} />
            <span className="fw-medium" style={{ color: statusStyle.color }}>{statusText}</span>
            {status === 'error' && (
              <a href="/app/traces?tab=backend" className="chat-error-trace-link ml-auto">
                <i className="fas fa-wave-square" /> View traces
              </a>
            )}
          </div>

          {/* Info note */}
          <div className="talk-hint mb-md">
            <i className="fas fa-info-circle text-primary mt-xs shrink-0" />
            <p className="text-sub m-0">
              <strong className="text-primary">Note:</strong> Select a pipeline model and click Connect.
              Your microphone streams continuously; the server detects speech and responds automatically.
            </p>
          </div>

          {/* Pipeline model selector */}
          <div className="mb-md">
            <label className="form-label text-sm">
              <i className="fas fa-brain text-primary icon-before" /> Pipeline Model
            </label>
            <ModelSelector
              value={selectedModel}
              onChange={(v) => {
                setSelectedModel(v)
                const m = pipelineModels.find(p => p.name === v)
                if (m && !voiceEdited) setVoice(m.voice || '')
              }}
              options={pipelineModelNames}
              loading={modelsLoading}
              disabled={isConnected}
              searchPlaceholder="Search pipeline models..."
            />
            <button className="btn btn-secondary btn-sm mt-xs" onClick={() => navigate('/app/model-editor?template=pipeline', { state: fromState(location, 'Talk') })}>
              <i className="fas fa-plus icon-before" /> Create Pipeline Model
            </button>
          </div>

          {/* Tools (client-side MCP servers, mirroring the chat page) */}
          <div className="mb-md">
            <label className="form-label text-sm">
              <i className="fas fa-screwdriver-wrench text-primary icon-before" /> Tools
            </label>
            <ClientMCPDropdown
              activeServerIds={activeMCPIds}
              onToggleServer={handleClientMCPToggle}
              onServerAdded={handleClientMCPServerAdded}
              onServerRemoved={handleClientMCPServerRemoved}
              connectionStatuses={connectionStatuses}
              getConnectedTools={getConnectedTools}
            />
            {isAdmin && (
              <label className={`talk-check mt-xs${isConnected ? ' talk-check--locked' : ''}`}>
                <input
                  type="checkbox"
                  checked={manageMode}
                  disabled={isConnected}
                  onChange={(e) => setManageMode(e.target.checked)}
                />
                <i className="fas fa-user-shield text-primary" />
                Manage Mode
                <span className="text-secondary text-xs">
                  — let the model query LocalAI (models, backends, system info)
                </span>
              </label>
            )}
          </div>

          {/* Pipeline details */}
          {selectedModelInfo && selectedModelInfo.self_contained && (
            <div className="talk-chip mb-xs">
              <i className="fas fa-tower-broadcast text-primary" />
              <span className="text-secondary">Self-contained any-to-any —</span>
              <span className="text-mono cell-clip">
                {selectedModelInfo.name}
              </span>
              <span className="text-secondary ml-auto">handles VAD · STT · LLM · TTS</span>
            </div>
          )}
          {selectedModelInfo && !selectedModelInfo.self_contained && (
            <div className="talk-slots mb-xs">
              {[
                { label: 'VAD', value: selectedModelInfo.vad },
                { label: 'Transcription', value: selectedModelInfo.transcription },
                { label: 'LLM', value: selectedModelInfo.llm },
                { label: 'TTS', value: selectedModelInfo.tts },
              ].map(item => (
                <div key={item.label} className="talk-slot">
                  <div className="text-secondary nowrap">{item.label}</div>
                  {/* full width for the value; wrap rather than overflow when the
                      model name is long (minWidth:0 lets the flex item shrink) */}
                  <div className="talk-slot__value">{item.value || '—'}</div>
                </div>
              ))}
            </div>
          )}
          {selectedModelInfo && !isConnected && (
            <div className="mb-md">
              <button className="btn btn-secondary btn-sm" onClick={() => navigate(`/app/model-editor/${encodeURIComponent(selectedModel)}`, { state: fromState(location, 'Talk') })}>
                <i className="fas fa-pen-to-square icon-before" />
                {selectedModelInfo.self_contained ? ' Edit Model Config' : ' Edit Pipeline'}
              </button>
            </div>
          )}

          {/* Session settings */}
          <details className="talk-details mb-md">
            <summary>
              <i className="fas fa-sliders text-primary icon-before" />
              Session Settings
            </summary>
            <div className="talk-details__body">
              <div className="form-group m-0">
                <label className="form-label text-xs">Instructions</label>
                <textarea
                  className="textarea text-sm"
                  rows={3}
                  value={instructions}
                  onChange={e => setInstructions(e.target.value)}
                  placeholder="System instructions for the model"
                />
              </div>
              <div className="form-group m-0">
                <label className="form-label text-xs">Voice</label>
                <input
                  className="input text-sm"
                  value={voice}
                  onChange={e => { setVoice(e.target.value); setVoiceEdited(true) }}
                  placeholder="Voice name (leave blank for model default)"
                />
              </div>
              <div className="form-group m-0">
                <label className="form-label text-xs">Transcription Language</label>
                <input
                  className="input text-sm"
                  value={language}
                  onChange={e => setLanguage(e.target.value)}
                  placeholder="Language code (e.g. 'en') — leave blank for auto-detect"
                />
              </div>
            </div>
          </details>

          {/* Transcript */}
          <div className="talk-transcript mb-md">
            {transcript.length === 0 && (
              <p className="text-secondary text-italic m-0">
                Conversation will appear here...
              </p>
            )}
            {transcript.map((entry, i) => {
              const isToolCall = entry.role === 'tool_call'
              const isToolResult = entry.role === 'tool_result'
              const isUser = entry.role === 'user'
              const iconClass = isToolCall ? 'fa-solid fa-screwdriver-wrench'
                              : isToolResult ? 'fa-solid fa-clipboard-list'
                              : isUser ? 'fa-solid fa-user' : 'fa-solid fa-robot'
              const iconColor = isToolCall || isToolResult ? 'var(--color-text-secondary)'
                              : isUser ? 'var(--color-primary)' : 'var(--color-accent)'
              return (
                <div key={entry.id || i} className="talk-line">
                  <i className={`${iconClass} talk-line__icon`} style={{ color: iconColor }} />
                  <p className={`talk-line__text${(isToolCall || isToolResult) ? ' talk-line__text--tool' : ''}${isToolResult ? ' talk-line__text--result' : ''}`}>{entry.text}</p>
                </div>
              )
            })}
            <div ref={transcriptEndRef} />
          </div>

          {/* Buttons */}
          <div className="hstack hstack--between">
            <div className="hstack">
              {!isConnected ? (
                <button className="btn btn-primary" onClick={connect} disabled={modelsLoading || !selectedModel}>
                  <i className="fas fa-plug icon-before" /> Connect
                </button>
              ) : (
                <>
                  <button className="btn btn--accent" onClick={sendTestTone}>
                    <i className="fas fa-wave-square icon-before" /> Test Tone
                  </button>
                  <button className="btn btn-secondary" onClick={toggleDiagnostics}>
                    <i className="fas fa-chart-line icon-before" /> Diag
                  </button>
                </>
              )}
            </div>
            {isConnected && (
              <button className="btn btn--error" onClick={disconnect}>
                <i className="fas fa-plug-circle-xmark icon-before" /> Disconnect
              </button>
            )}
          </div>

          {/* Hidden audio element for WebRTC playback */}
          <audio ref={audioRef} autoPlay className="hidden" />

          {/* Diagnostics panel */}
          {diagVisible && (
            <div className="talk-diag mt-md">
              <h3 className="text-base fw-semibold mb-sm">
                <i className="fas fa-chart-line text-primary icon-before" />
                Audio Diagnostics
              </h3>

              <div className="talk-split mb-sm">
                <div>
                  <p className="text-xs text-secondary mb-xs">Waveform</p>
                  <canvas ref={waveCanvasRef} width={400} height={120}
                    className="talk-canvas" />
                </div>
                <div>
                  <p className="text-xs text-secondary mb-xs">Spectrum (FFT)</p>
                  <canvas ref={specCanvasRef} width={400} height={120}
                    className="talk-canvas" />
                </div>
              </div>

              <div className="talk-diag__grid mb-sm">
                {[
                  { label: 'Peak Freq', value: diagStats.peakFreq },
                  { label: 'THD', value: diagStats.thd },
                  { label: 'RMS Level', value: diagStats.rms },
                  { label: 'Sample Rate', value: diagStats.sampleRate },
                  { label: 'Packets Recv', value: diagStats.packetsRecv },
                  { label: 'Packets Lost', value: diagStats.packetsLost },
                  { label: 'Jitter', value: diagStats.jitter },
                  { label: 'Concealed', value: diagStats.concealed },
                ].map(item => (
                  <div key={item.label} className="talk-diag__cell">
                    <div className="text-secondary text-xs">{item.label}</div>
                    <div className="text-mono">{item.value}</div>
                  </div>
                ))}
              </div>

              <pre className="talk-diag__raw">
                {diagStats.raw || 'Waiting for stats...'}
              </pre>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
