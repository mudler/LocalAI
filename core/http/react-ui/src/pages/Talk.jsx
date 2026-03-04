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

  return (
    <div className="page" style={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
      <div style={{ width: '100%', maxWidth: '48rem' }}>
        {/* Hero */}
        <div style={{ textAlign: 'center', marginBottom: 'var(--spacing-xl)' }}>
          <h1 className="page-title">Talk Interface</h1>
          <p className="page-subtitle">Have a voice conversation with AI using speech-to-text and text-to-speech</p>
        </div>

        {/* Main card */}
        <div className="card" style={{ padding: 'var(--spacing-lg)' }}>
          {/* Recording status banner */}
          {isRecording && (
            <div style={{
              background: 'rgba(239, 68, 68, 0.15)', border: '1px solid rgba(239, 68, 68, 0.3)',
              borderRadius: 'var(--radius-md)', padding: 'var(--spacing-sm)',
              display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)',
              marginBottom: 'var(--spacing-md)', color: 'var(--color-error)',
            }}>
              <i className="fas fa-microphone" style={{ animation: 'pulse 1s infinite' }} />
              <span style={{ fontSize: '0.875rem', fontWeight: 500 }}>Recording in progress...</span>
            </div>
          )}

          {/* Loader */}
          {loading && (
            <div style={{ textAlign: 'center', padding: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
              <LoadingSpinner size="lg" />
            </div>
          )}

          {/* Status */}
          <p style={{
            textAlign: 'center', color: 'var(--color-text-secondary)', fontSize: '0.875rem',
            marginBottom: 'var(--spacing-md)',
          }}>
            {status}
          </p>

          {/* Info box */}
          <div style={{
            background: 'var(--color-info-light)', border: '1px solid rgba(56, 189, 248, 0.2)',
            borderRadius: 'var(--radius-md)', padding: 'var(--spacing-sm) var(--spacing-md)',
            marginBottom: 'var(--spacing-lg)', fontSize: '0.8125rem', color: 'var(--color-text-secondary)',
          }}>
            <i className="fas fa-info-circle" style={{ color: 'var(--color-info)', marginRight: 'var(--spacing-xs)' }} />
            This interface requires three models: an LLM for conversation, a Whisper model for speech recognition, and a TTS model for voice synthesis.
          </div>

          {/* Model selectors - 3 column grid */}
          <div style={{
            display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)',
            gap: 'var(--spacing-md)', marginBottom: 'var(--spacing-lg)',
          }}>
            <div className="form-group" style={{ margin: 0 }}>
              <label className="form-label">
                <i className="fas fa-brain" style={{ color: 'var(--color-primary)' }} /> LLM Model
              </label>
              <ModelSelector value={llmModel} onChange={setLlmModel} />
            </div>
            <div className="form-group" style={{ margin: 0 }}>
              <label className="form-label">
                <i className="fas fa-ear-listen" style={{ color: 'var(--color-accent)' }} /> Whisper Model
              </label>
              <ModelSelector value={whisperModel} onChange={setWhisperModel} />
            </div>
            <div className="form-group" style={{ margin: 0 }}>
              <label className="form-label">
                <i className="fas fa-volume-high" style={{ color: 'var(--color-success)' }} /> TTS Model
              </label>
              <ModelSelector value={ttsModel} onChange={setTtsModel} />
            </div>
          </div>

          {/* Action buttons */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <button
              className={`btn ${isRecording ? 'btn-danger' : 'btn-primary'}`}
              onClick={isRecording ? stopRecording : startRecording}
              disabled={loading || !llmModel || !whisperModel || !ttsModel}
            >
              <i className={`fas ${isRecording ? 'fa-stop' : 'fa-microphone'}`} />
              {isRecording ? 'Stop Recording' : 'Talk'}
            </button>
            <button
              className="btn btn-secondary"
              onClick={resetConversation}
              style={{ fontSize: '0.8125rem' }}
            >
              Reset conversation
            </button>
          </div>

          {/* Audio playback */}
          {audioUrl && (
            <div style={{ marginTop: 'var(--spacing-lg)' }}>
              <audio ref={audioRef} controls src={audioUrl} style={{ width: '100%' }} />
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
