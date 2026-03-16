import { useState, useRef, useEffect, useCallback } from 'react'
import { useOutletContext } from 'react-router-dom'
import { realtimeApi } from '../utils/api'

const STATUS_STYLES = {
  disconnected: { icon: 'fa-solid fa-circle', color: 'var(--color-text-secondary)', bg: 'transparent' },
  connecting:   { icon: 'fa-solid fa-spinner fa-spin', color: 'var(--color-primary)', bg: 'var(--color-primary-light)' },
  connected:    { icon: 'fa-solid fa-circle', color: 'var(--color-success)', bg: 'var(--color-success-light)' },
  listening:    { icon: 'fa-solid fa-microphone', color: 'var(--color-success)', bg: 'var(--color-success-light)' },
  thinking:     { icon: 'fa-solid fa-brain fa-beat', color: 'var(--color-primary)', bg: 'var(--color-primary-light)' },
  speaking:     { icon: 'fa-solid fa-volume-high fa-beat-fade', color: 'var(--color-accent)', bg: 'var(--color-accent-light)' },
  error:        { icon: 'fa-solid fa-circle', color: 'var(--color-error)', bg: 'var(--color-error-light)' },
}

export default function Talk() {
  const { addToast } = useOutletContext()

  // Pipeline models
  const [pipelineModels, setPipelineModels] = useState([])
  const [selectedModel, setSelectedModel] = useState('')
  const [modelsLoading, setModelsLoading] = useState(true)

  // Connection state
  const [status, setStatus] = useState('disconnected')
  const [statusText, setStatusText] = useState('Disconnected')
  const [isConnected, setIsConnected] = useState(false)

  // Transcript
  const [transcript, setTranscript] = useState([])
  const streamingRef = useRef(null) // tracks the index of the in-progress assistant message

  // Session settings
  const [instructions, setInstructions] = useState(
    'You are a helpful voice assistant. Your responses will be spoken aloud using text-to-speech, so keep them concise and conversational. Do not use markdown formatting, bullet points, numbered lists, code blocks, or special characters. Speak naturally as you would in a phone conversation.'
  )
  const [voice, setVoice] = useState('')
  const [voiceEdited, setVoiceEdited] = useState(false)
  const [language, setLanguage] = useState('')

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
      .catch(err => addToast(`Failed to load pipeline models: ${err.message}`, 'error'))
      .finally(() => setModelsLoading(false))
  }, [])

  // Auto-scroll transcript
  useEffect(() => {
    transcriptEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [transcript])

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
    if (!instructions.trim() && !voice.trim() && !language.trim()) return

    const session = {}
    if (instructions.trim()) session.instructions = instructions.trim()
    if (voice.trim() || language.trim()) {
      session.audio = {}
      if (voice.trim()) session.audio.output = { voice: voice.trim() }
      if (language.trim()) session.audio.input = { transcription: { language: language.trim() } }
    }

    dc.send(JSON.stringify({ type: 'session.update', session }))
  }, [instructions, voice, language])

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
      case 'conversation.item.input_audio_transcription.completed':
        if (event.transcript) {
          streamingRef.current = null
          setTranscript(prev => [...prev, { role: 'user', text: event.transcript }])
        }
        updateStatus('thinking', 'Generating response...')
        break
      case 'response.output_audio_transcript.delta':
        if (event.delta) {
          setTranscript(prev => {
            if (streamingRef.current !== null) {
              const updated = [...prev]
              updated[streamingRef.current] = {
                ...updated[streamingRef.current],
                text: updated[streamingRef.current].text + event.delta,
              }
              return updated
            }
            streamingRef.current = prev.length
            return [...prev, { role: 'assistant', text: event.delta }]
          })
        }
        break
      case 'response.output_audio_transcript.done':
        if (event.transcript) {
          setTranscript(prev => {
            if (streamingRef.current !== null) {
              const updated = [...prev]
              updated[streamingRef.current] = { ...updated[streamingRef.current], text: event.transcript }
              return updated
            }
            return [...prev, { role: 'assistant', text: event.transcript }]
          })
        }
        streamingRef.current = null
        break
      case 'response.output_audio.delta':
        updateStatus('speaking', 'Speaking...')
        break
      case 'response.done':
        updateStatus('listening', 'Listening...')
        break
      case 'error':
        hasErrorRef.current = true
        updateStatus('error', 'Error: ' + (event.error?.message || 'Unknown error'))
        break
    }
  }, [sendSessionUpdate, updateStatus])

  // ── Connect ──
  const connect = useCallback(async () => {
    if (!selectedModel) {
      addToast('Please select a pipeline model first.', 'warning')
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
      })

      await pc.setRemoteDescription({ type: 'answer', sdp: data.sdp })
    } catch (err) {
      hasErrorRef.current = true
      updateStatus('error', 'Connection failed: ' + err.message)
      disconnect()
    }
  }, [selectedModel, diagVisible, handleServerEvent, updateStatus, addToast])

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
    <div className="page" style={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
      <div style={{ width: '100%', maxWidth: '48rem' }}>
        <div style={{ textAlign: 'center', marginBottom: 'var(--spacing-lg)' }}>
          <h1 className="page-title">Talk</h1>
          <p className="page-subtitle">Real-time voice conversation via WebRTC</p>
        </div>

        <div className="card" style={{ padding: 'var(--spacing-lg)', marginBottom: 'var(--spacing-md)' }}>
          {/* Connection status */}
          <div style={{
            display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)',
            padding: 'var(--spacing-sm) var(--spacing-md)',
            borderRadius: 'var(--radius-md)',
            background: statusStyle.bg,
            border: '1px solid color-mix(in srgb, ' + statusStyle.color + ' 30%, transparent)',
            marginBottom: 'var(--spacing-md)',
          }}>
            <i className={statusStyle.icon} style={{ color: statusStyle.color }} />
            <span style={{ fontWeight: 500, color: statusStyle.color }}>{statusText}</span>
          </div>

          {/* Info note */}
          <div style={{
            background: 'var(--color-primary-light)',
            border: '1px solid color-mix(in srgb, var(--color-primary) 20%, transparent)',
            borderRadius: 'var(--radius-md)',
            padding: 'var(--spacing-sm) var(--spacing-md)',
            marginBottom: 'var(--spacing-md)',
            display: 'flex', alignItems: 'flex-start', gap: 'var(--spacing-sm)',
          }}>
            <i className="fas fa-info-circle" style={{ color: 'var(--color-primary)', marginTop: 2, flexShrink: 0 }} />
            <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.8125rem', margin: 0 }}>
              <strong style={{ color: 'var(--color-primary)' }}>Note:</strong> Select a pipeline model and click Connect.
              Your microphone streams continuously; the server detects speech and responds automatically.
            </p>
          </div>

          {/* Pipeline model selector */}
          <div style={{ marginBottom: 'var(--spacing-md)' }}>
            <label className="form-label" style={{ fontSize: '0.8125rem' }}>
              <i className="fas fa-brain" style={{ color: 'var(--color-primary)', marginRight: 4 }} /> Pipeline Model
            </label>
            <select
              className="model-selector"
              value={selectedModel}
              onChange={(e) => {
                setSelectedModel(e.target.value)
                const m = pipelineModels.find(p => p.name === e.target.value)
                if (m && !voiceEdited) setVoice(m.voice || '')
              }}
              disabled={modelsLoading || isConnected}
              style={{ width: '100%' }}
            >
              {modelsLoading && <option>Loading models...</option>}
              {!modelsLoading && pipelineModels.length === 0 && <option>No pipeline models available</option>}
              {pipelineModels.map(m => (
                <option key={m.name} value={m.name}>{m.name}</option>
              ))}
            </select>
          </div>

          {/* Pipeline details */}
          {selectedModelInfo && (
            <div style={{
              display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 'var(--spacing-xs)',
              marginBottom: 'var(--spacing-md)', fontSize: '0.75rem',
            }}>
              {[
                { label: 'VAD', value: selectedModelInfo.vad },
                { label: 'Transcription', value: selectedModelInfo.transcription },
                { label: 'LLM', value: selectedModelInfo.llm },
                { label: 'TTS', value: selectedModelInfo.tts },
              ].map(item => (
                <div key={item.label} style={{
                  background: 'var(--color-bg-secondary)', borderRadius: 'var(--radius-sm)',
                  padding: 'var(--spacing-xs)', border: '1px solid var(--color-border)',
                }}>
                  <div style={{ color: 'var(--color-text-secondary)', marginBottom: 2 }}>{item.label}</div>
                  <div style={{ fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{item.value}</div>
                </div>
              ))}
            </div>
          )}

          {/* Session settings */}
          <details style={{
            marginBottom: 'var(--spacing-md)', border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-md)',
          }}>
            <summary style={{
              cursor: 'pointer', padding: 'var(--spacing-sm) var(--spacing-md)',
              fontWeight: 500, color: 'var(--color-text-secondary)', fontSize: '0.875rem',
            }}>
              <i className="fas fa-sliders" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} />
              Session Settings
            </summary>
            <div style={{ padding: 'var(--spacing-md)', paddingTop: 'var(--spacing-xs)', display: 'flex', flexDirection: 'column', gap: 'var(--spacing-sm)' }}>
              <div className="form-group" style={{ margin: 0 }}>
                <label className="form-label" style={{ fontSize: '0.75rem' }}>Instructions</label>
                <textarea
                  className="textarea"
                  rows={3}
                  value={instructions}
                  onChange={e => setInstructions(e.target.value)}
                  placeholder="System instructions for the model"
                  style={{ fontSize: '0.8125rem' }}
                />
              </div>
              <div className="form-group" style={{ margin: 0 }}>
                <label className="form-label" style={{ fontSize: '0.75rem' }}>Voice</label>
                <input
                  className="input"
                  value={voice}
                  onChange={e => { setVoice(e.target.value); setVoiceEdited(true) }}
                  placeholder="Voice name (leave blank for model default)"
                  style={{ fontSize: '0.8125rem' }}
                />
              </div>
              <div className="form-group" style={{ margin: 0 }}>
                <label className="form-label" style={{ fontSize: '0.75rem' }}>Transcription Language</label>
                <input
                  className="input"
                  value={language}
                  onChange={e => setLanguage(e.target.value)}
                  placeholder="Language code (e.g. 'en') — leave blank for auto-detect"
                  style={{ fontSize: '0.8125rem' }}
                />
              </div>
            </div>
          </details>

          {/* Transcript */}
          <div style={{
            marginBottom: 'var(--spacing-md)',
            maxHeight: '24rem', overflowY: 'auto', minHeight: '6rem',
            padding: 'var(--spacing-sm)',
            background: 'var(--color-bg-secondary)',
            border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-md)',
            display: 'flex', flexDirection: 'column', gap: 'var(--spacing-xs)',
          }}>
            {transcript.length === 0 && (
              <p style={{ color: 'var(--color-text-secondary)', fontStyle: 'italic', margin: 0 }}>
                Conversation will appear here...
              </p>
            )}
            {transcript.map((entry, i) => (
              <div key={i} style={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--spacing-xs)' }}>
                <i className={entry.role === 'user' ? 'fa-solid fa-user' : 'fa-solid fa-robot'}
                  style={{
                    color: entry.role === 'user' ? 'var(--color-primary)' : 'var(--color-accent)',
                    marginTop: 3, flexShrink: 0, fontSize: '0.75rem',
                  }} />
                <p style={{ margin: 0 }}>{entry.text}</p>
              </div>
            ))}
            <div ref={transcriptEndRef} />
          </div>

          {/* Buttons */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
              {!isConnected ? (
                <button className="btn btn-primary" onClick={connect} disabled={modelsLoading || !selectedModel}>
                  <i className="fas fa-plug" style={{ marginRight: 'var(--spacing-xs)' }} /> Connect
                </button>
              ) : (
                <>
                  <button className="btn" onClick={sendTestTone}
                    style={{ background: 'var(--color-accent)', color: '#fff', border: 'none' }}>
                    <i className="fas fa-wave-square" style={{ marginRight: 'var(--spacing-xs)' }} /> Test Tone
                  </button>
                  <button className="btn btn-secondary" onClick={toggleDiagnostics}>
                    <i className="fas fa-chart-line" style={{ marginRight: 'var(--spacing-xs)' }} /> Diag
                  </button>
                </>
              )}
            </div>
            {isConnected && (
              <button className="btn" onClick={disconnect}
                style={{ background: 'var(--color-error)', color: '#fff', border: 'none' }}>
                <i className="fas fa-plug-circle-xmark" style={{ marginRight: 'var(--spacing-xs)' }} /> Disconnect
              </button>
            )}
          </div>

          {/* Hidden audio element for WebRTC playback */}
          <audio ref={audioRef} autoPlay style={{ display: 'none' }} />

          {/* Diagnostics panel */}
          {diagVisible && (
            <div style={{
              marginTop: 'var(--spacing-md)',
              border: '1px solid var(--color-border)',
              borderRadius: 'var(--radius-md)',
              padding: 'var(--spacing-md)',
            }}>
              <h3 style={{ fontSize: '0.875rem', fontWeight: 600, marginBottom: 'var(--spacing-sm)' }}>
                <i className="fas fa-chart-line" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} />
                Audio Diagnostics
              </h3>

              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-sm)' }}>
                <div>
                  <p style={{ fontSize: '0.6875rem', color: 'var(--color-text-secondary)', marginBottom: 2 }}>Waveform</p>
                  <canvas ref={waveCanvasRef} width={400} height={120}
                    style={{ width: '100%', border: '1px solid var(--color-border)', borderRadius: 'var(--radius-sm)', background: '#000' }} />
                </div>
                <div>
                  <p style={{ fontSize: '0.6875rem', color: 'var(--color-text-secondary)', marginBottom: 2 }}>Spectrum (FFT)</p>
                  <canvas ref={specCanvasRef} width={400} height={120}
                    style={{ width: '100%', border: '1px solid var(--color-border)', borderRadius: 'var(--radius-sm)', background: '#000' }} />
                </div>
              </div>

              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 'var(--spacing-xs)', marginBottom: 'var(--spacing-sm)', fontSize: '0.75rem' }}>
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
                  <div key={item.label} style={{
                    background: 'var(--color-bg-secondary)', borderRadius: 'var(--radius-sm)', padding: 'var(--spacing-xs)',
                  }}>
                    <div style={{ color: 'var(--color-text-secondary)', fontSize: '0.6875rem' }}>{item.label}</div>
                    <div style={{ fontFamily: 'monospace' }}>{item.value}</div>
                  </div>
                ))}
              </div>

              <pre style={{
                fontSize: '0.6875rem', color: 'var(--color-text-secondary)',
                background: 'var(--color-bg-secondary)', borderRadius: 'var(--radius-sm)',
                padding: 'var(--spacing-xs)', maxHeight: '8rem', overflowY: 'auto',
                fontFamily: 'monospace', whiteSpace: 'pre-wrap', margin: 0,
              }}>
                {diagStats.raw || 'Waiting for stats...'}
              </pre>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
