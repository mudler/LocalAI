import { useState, useRef, useCallback } from 'react'
import { useOutletContext } from 'react-router-dom'
import ModelSelector from '../components/ModelSelector'
import LoadingSpinner from '../components/LoadingSpinner'
import { chatApi, ttsApi, audioApi } from '../utils/api'

export default function Talk() {
  const { addToast } = useOutletContext()
  const [llmModel, setLlmModel] = useState('')
  const [whisperModel, setWhisperModel] = useState('')
  const [ttsModel, setTtsModel] = useState('')
  const [isRecording, setIsRecording] = useState(false)
  const [loading, setLoading] = useState(false)
  const [status, setStatus] = useState('Press the record button to start talking.')
  const [audioUrl, setAudioUrl] = useState(null)
  const [conversationHistory, setConversationHistory] = useState([])
  const mediaRecorderRef = useRef(null)
  const chunksRef = useRef([])
  const audioRef = useRef(null)

  const startRecording = async () => {
    if (!navigator.mediaDevices) {
      addToast('MediaDevices API not supported', 'error')
      return
    }
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      const recorder = new MediaRecorder(stream)
      chunksRef.current = []
      recorder.ondataavailable = (e) => chunksRef.current.push(e.data)
      recorder.start()
      mediaRecorderRef.current = recorder
      setIsRecording(true)
      setStatus('Recording... Click to stop.')
    } catch (err) {
      addToast(`Microphone error: ${err.message}`, 'error')
    }
  }

  const stopRecording = useCallback(() => {
    if (!mediaRecorderRef.current) return

    mediaRecorderRef.current.onstop = async () => {
      setIsRecording(false)
      setLoading(true)

      const audioBlob = new Blob(chunksRef.current, { type: 'audio/webm' })

      try {
        // 1. Transcribe
        setStatus('Transcribing audio...')
        const formData = new FormData()
        formData.append('file', audioBlob)
        formData.append('model', whisperModel)
        const transcription = await audioApi.transcribe(formData)
        const userText = transcription.text

        setStatus(`You said: "${userText}". Generating response...`)

        // 2. Chat completion
        const newHistory = [...conversationHistory, { role: 'user', content: userText }]
        const chatResponse = await chatApi.complete({
          model: llmModel,
          messages: newHistory,
        })
        const assistantText = chatResponse?.choices?.[0]?.message?.content || ''
        const updatedHistory = [...newHistory, { role: 'assistant', content: assistantText }]
        setConversationHistory(updatedHistory)

        setStatus(`Response: "${assistantText}". Generating speech...`)

        // 3. TTS
        const ttsBlob = await ttsApi.generateV1({ input: assistantText, model: ttsModel })
        const url = URL.createObjectURL(ttsBlob)
        setAudioUrl(url)
        setStatus('Press the record button to continue.')

        // Auto-play
        setTimeout(() => audioRef.current?.play(), 100)
      } catch (err) {
        addToast(`Error: ${err.message}`, 'error')
        setStatus('Error occurred. Try again.')
      } finally {
        setLoading(false)
      }
    }

    mediaRecorderRef.current.stop()
    mediaRecorderRef.current.stream?.getTracks().forEach(t => t.stop())
  }, [whisperModel, llmModel, ttsModel, conversationHistory])

  const resetConversation = () => {
    setConversationHistory([])
    setAudioUrl(null)
    setStatus('Conversation reset. Press record to start.')
    addToast('Conversation reset', 'info')
  }

  const allModelsSet = llmModel && whisperModel && ttsModel

  return (
    <div className="page" style={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
      <div style={{ width: '100%', maxWidth: '40rem' }}>
        <div style={{ textAlign: 'center', marginBottom: 'var(--spacing-lg)' }}>
          <h1 className="page-title">Talk</h1>
          <p className="page-subtitle">Voice conversation with AI</p>
        </div>

        {/* Main interaction area */}
        <div className="card" style={{ padding: 'var(--spacing-lg)', textAlign: 'center', marginBottom: 'var(--spacing-md)' }}>
          {/* Big record button */}
          <button
            onClick={isRecording ? stopRecording : startRecording}
            disabled={loading || !allModelsSet}
            style={{
              width: 96, height: 96, borderRadius: '50%', border: 'none', cursor: loading || !allModelsSet ? 'not-allowed' : 'pointer',
              background: isRecording ? 'var(--color-error)' : 'var(--color-primary)',
              color: '#fff', fontSize: '2rem', display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
              boxShadow: isRecording ? '0 0 0 8px rgba(239,68,68,0.2)' : '0 0 0 8px var(--color-primary-light)',
              transition: 'all 200ms', opacity: loading || !allModelsSet ? 0.5 : 1,
              margin: '0 auto var(--spacing-md)',
            }}
          >
            <i className={`fas ${isRecording ? 'fa-stop' : 'fa-microphone'}`} />
          </button>

          {/* Status */}
          <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.875rem', marginBottom: 'var(--spacing-md)' }}>
            {loading ? <LoadingSpinner size="sm" /> : null}
            {' '}{status}
          </p>

          {/* Recording indicator */}
          {isRecording && (
            <div style={{
              background: 'rgba(239, 68, 68, 0.1)', border: '1px solid rgba(239, 68, 68, 0.3)',
              borderRadius: 'var(--radius-md)', padding: 'var(--spacing-xs) var(--spacing-sm)',
              display: 'inline-flex', alignItems: 'center', gap: 'var(--spacing-xs)',
              color: 'var(--color-error)', fontSize: '0.8125rem', marginBottom: 'var(--spacing-md)',
            }}>
              <i className="fas fa-circle" style={{ fontSize: '0.5rem', animation: 'pulse 1s infinite' }} />
              Recording...
            </div>
          )}

          {/* Audio playback */}
          {audioUrl && (
            <div style={{ marginTop: 'var(--spacing-sm)' }}>
              <audio ref={audioRef} controls src={audioUrl} style={{ width: '100%' }} />
            </div>
          )}
        </div>

        {/* Model selectors */}
        <div className="card" style={{ padding: 'var(--spacing-md)' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-md)' }}>
            <h3 style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--color-text-secondary)' }}>
              <i className="fas fa-sliders-h" style={{ marginRight: 'var(--spacing-xs)' }} /> Models
            </h3>
            <button className="btn btn-secondary btn-sm" onClick={resetConversation} style={{ fontSize: '0.75rem' }}>
              <i className="fas fa-rotate-right" /> Reset
            </button>
          </div>

          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-sm)' }}>
            <div className="form-group" style={{ margin: 0 }}>
              <label className="form-label" style={{ fontSize: '0.75rem' }}>
                <i className="fas fa-brain" style={{ color: 'var(--color-primary)', marginRight: 4 }} /> LLM
              </label>
              <ModelSelector value={llmModel} onChange={setLlmModel} capability="FLAG_CHAT" />
            </div>
            <div className="form-group" style={{ margin: 0 }}>
              <label className="form-label" style={{ fontSize: '0.75rem' }}>
                <i className="fas fa-ear-listen" style={{ color: 'var(--color-accent)', marginRight: 4 }} /> Speech-to-Text
              </label>
              <ModelSelector value={whisperModel} onChange={setWhisperModel} capability="FLAG_TRANSCRIPT" />
            </div>
            <div className="form-group" style={{ margin: 0 }}>
              <label className="form-label" style={{ fontSize: '0.75rem' }}>
                <i className="fas fa-volume-high" style={{ color: 'var(--color-success)', marginRight: 4 }} /> Text-to-Speech
              </label>
              <ModelSelector value={ttsModel} onChange={setTtsModel} capability="FLAG_TTS" />
            </div>
          </div>

          {!allModelsSet && (
            <div style={{
              background: 'var(--color-info-light)', border: '1px solid rgba(56, 189, 248, 0.2)',
              borderRadius: 'var(--radius-md)', padding: 'var(--spacing-xs) var(--spacing-sm)',
              marginTop: 'var(--spacing-sm)', fontSize: '0.75rem', color: 'var(--color-text-secondary)',
            }}>
              <i className="fas fa-info-circle" style={{ color: 'var(--color-info)', marginRight: 4 }} />
              Select all three models to start talking.
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
