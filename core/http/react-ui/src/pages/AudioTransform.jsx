import { useState, useEffect, useRef } from 'react'
import { useParams, useOutletContext } from 'react-router-dom'
import ModelSelector from '../components/ModelSelector'
import { CAP_AUDIO_TRANSFORM } from '../utils/capabilities'
import LoadingSpinner from '../components/LoadingSpinner'
import ErrorWithTraceLink from '../components/ErrorWithTraceLink'
import WaveformPlayer from '../components/audio/WaveformPlayer'
import { audioTransformApi } from '../utils/api'
import { useMediaCapture } from '../hooks/useMediaCapture'
import useObjectUrl from '../hooks/useObjectUrl'
import { useMediaHistory } from '../hooks/useMediaHistory'
import MediaHistory from '../components/MediaHistory'

// AudioTransform — Studio tab for the audio_transform capability. Takes a
// primary audio file plus an optional reference (loopback for AEC, target
// speaker for voice conversion, etc.) and shows three synchronized
// waveforms: input audio / reference / output. Supports both file upload
// and direct mic recording, plus an "echo test" mode that records mic
// while playing the reference — the recorded mic picks up the speaker
// bleed of the reference, giving the user a real (mic, ref) pair to test
// echo cancellation against.
export default function AudioTransform() {
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()

  const [model, setModel] = useState(urlModel || '')
  const [audioFile, setAudioFile] = useState(null)
  const [referenceFile, setReferenceFile] = useState(null)
  const [outputUrl, setOutputUrl] = useState(null)
  const [paramsText, setParamsText] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)

  const { addEntry, selectEntry, selectedEntry, historyProps } = useMediaHistory('audio-transform')

  // Hidden <audio> element that plays the reference out the speakers while
  // the mic records — the recording captures the user's voice plus the
  // speaker-bleed echo. Headphones short-circuit the path; document only.
  const echoAudioRef = useRef(null)
  const echoCap = useMediaCapture('audio')
  const echoActive = echoCap.active || echoCap.recording

  // Blob URLs derived from File state. useObjectUrl revokes the previous
  // URL when its source changes and on unmount, so the cleanup is correct
  // without a separate effect tracking each setter.
  const audioUrl = useObjectUrl(audioFile)
  const referenceUrl = useObjectUrl(referenceFile)
  useEffect(() => {
    return () => { if (outputUrl) URL.revokeObjectURL(outputUrl) }
  }, [outputUrl])

  const parseParams = () => {
    const out = {}
    for (const raw of paramsText.split('\n')) {
      const line = raw.trim()
      if (!line || line.startsWith('#')) continue
      const eq = line.indexOf('=')
      if (eq < 0) continue
      const k = line.slice(0, eq).trim()
      const v = line.slice(eq + 1).trim()
      if (k) out[k] = v
    }
    return out
  }

  const handleProcess = async (e) => {
    e.preventDefault()
    if (!model) { addToast('Please select a model', 'warning'); return }
    if (!audioFile) { addToast('Please choose an audio file', 'warning'); return }

    setLoading(true)
    setError(null)
    if (outputUrl) { URL.revokeObjectURL(outputUrl); setOutputUrl(null) }

    try {
      const { blob, serverUrl, inputUrl, referenceUrl: refServerUrl } = await audioTransformApi.process({
        model,
        audioFile,
        referenceFile,
        format: 'wav',
        params: parseParams(),
      })
      const url = URL.createObjectURL(blob)
      setOutputUrl(url)
      addToast('Audio transformed', 'success')
      if (serverUrl) {
        // Save the persisted (input, reference, output) triple so a click
        // in the History panel can later replay all three players. The
        // server held onto the converted 16 kHz mono inputs — saving raw
        // upload bytes in localStorage would blow past quota in a few runs.
        addEntry({
          prompt: describeRun(audioFile, referenceFile),
          model,
          params: parseParams(),
          results: [
            { kind: 'output', url: serverUrl },
            inputUrl ? { kind: 'input', url: inputUrl } : null,
            refServerUrl ? { kind: 'reference', url: refServerUrl } : null,
          ].filter(Boolean),
        })
      }
      selectEntry(null)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  // The echo-test playback listener is held in a ref so stopEchoTest can
  // detach it without depending on the closure that registered it.
  const echoEndedListenerRef = useRef(null)
  const detachEchoEndedListener = () => {
    const audio = echoAudioRef.current
    const listener = echoEndedListenerRef.current
    if (audio && listener) audio.removeEventListener('ended', listener)
    echoEndedListenerRef.current = null
  }

  const startEchoTest = async () => {
    if (!referenceUrl) {
      addToast('Load a reference first', 'warning')
      return
    }
    if (!echoCap.supported) {
      addToast('Browser does not expose getUserMedia', 'warning')
      return
    }
    try {
      // Acquire the mic first so the recording covers the entire ref playback.
      await echoCap.start()
      const recPromise = echoCap.startRecording()
      const audio = echoAudioRef.current
      if (audio) {
        audio.currentTime = 0
        const onEnded = () => {
          detachEchoEndedListener()
          echoCap.stopRecording()
        }
        echoEndedListenerRef.current = onEnded
        audio.addEventListener('ended', onEnded)
        try { await audio.play() } catch (_) { /* user-gesture gate, ignore */ }
      }
      const result = await recPromise
      detachEchoEndedListener()
      echoCap.stop()
      const file = new File([result.blob], 'mic-echo-test.wav', { type: 'audio/wav' })
      setAudioFile(file)
      addToast('Recorded (mic + reference echo). Click Transform to test AEC.', 'success')
    } catch (err) {
      detachEchoEndedListener()
      addToast(`Echo test failed: ${err?.message || err}`, 'error')
    }
  }

  const stopEchoTest = () => {
    detachEchoEndedListener()
    echoCap.stopRecording()
    if (echoAudioRef.current) {
      try { echoAudioRef.current.pause() } catch (_) { /* ignore */ }
    }
    echoCap.stop()
  }

  return (
    <div className="media-layout">
      <div className="media-controls">
        <div className="page-header">
          <h1 className="page-title"><i className="fas fa-wave-square" /> Audio Transform</h1>
        </div>

        <form onSubmit={handleProcess}>
          <div className="form-group">
            <label className="form-label">Model</label>
            <ModelSelector value={model} onChange={setModel} capability={CAP_AUDIO_TRANSFORM} />
          </div>

          <AudioInput
            label="Audio (required)"
            file={audioFile}
            onChange={setAudioFile}
          />
          <AudioInput
            label="Reference (optional)"
            help="Loopback / far-end signal for echo cancellation, target speaker for voice conversion. Leave empty for unconditional transform."
            file={referenceFile}
            onChange={setReferenceFile}
          />

          {referenceFile && (
            <div className="audio-transform-echo">
              <p className="audio-transform-echo__notice" role="note">
                <i className="fas fa-circle-info" aria-hidden="true" />
                <span>
                  Browsers often apply their own WebRTC echo cancellation and
                  noise suppression by default. This usually results in worse
                  performance than running LocalVQE on the raw audio.
                </span>
              </p>
              <div className="audio-transform-echo__row">
                <button
                  type="button"
                  className={`btn ${echoActive ? 'btn-secondary' : 'btn-primary'} btn-sm`}
                  onClick={echoActive ? stopEchoTest : startEchoTest}
                >
                  {echoActive
                    ? <><i className="fas fa-stop" /> Stop echo test</>
                    : <><i className="fas fa-headphones-alt" /> Echo test (record mic while playing reference)</>}
                </button>
                {echoActive && echoCap.recording && (
                  <span className="audio-transform-echo__elapsed">
                    recording {echoCap.elapsed.toFixed(1)}s
                  </span>
                )}
              </div>
              {/* Hidden player for the reference clip during the echo test.
                  Hidden because the user already has the WaveformPlayer in
                  the preview pane — this is just the audible source. */}
              <audio ref={echoAudioRef} src={referenceUrl} preload="auto" hidden />
            </div>
          )}

          <div className="form-group">
            <label className="form-label">
              Advanced parameters
              <span className="form-help"> &mdash; backend-specific (one <code>key=value</code> per line, e.g. <code>noise_gate=true</code>)</span>
            </label>
            <textarea
              className="textarea"
              value={paramsText}
              onChange={(e) => setParamsText(e.target.value)}
              placeholder={`# Optional. For LocalVQE:\n# noise_gate=true\n# noise_gate_threshold_dbfs=-50`}
              rows={4}
            />
          </div>

          <button type="submit" className="btn btn-primary btn-full" disabled={loading}>
            {loading ? <><LoadingSpinner size="sm" /> Processing...</> : <><i className="fas fa-wand-magic-sparkles" /> Transform</>}
          </button>
        </form>
        <MediaHistory {...historyProps} />
      </div>

      <div className="media-preview">
        <div className="media-result">
          {error ? (
            <ErrorWithTraceLink message={error} />
          ) : selectedEntry ? (
            <div className="audio-transform-stack">
              {selectedEntry.results.map((r) => (
                <WaveformPlayer
                  key={r.kind || r.url}
                  src={r.url}
                  label={resultLabel(r)}
                  height={r.kind === 'output' ? 120 : 96}
                  dimmed={r.kind === 'reference'}
                  download={r.kind === 'output' ? `audio-transform-${selectedEntry.model || 'output'}.wav` : undefined}
                />
              ))}
              <div className="result-quote">{selectedEntry.prompt}</div>
            </div>
          ) : (
            <div className="audio-transform-stack">
              <WaveformPlayer src={audioUrl} label="Audio" height={96} />
              <WaveformPlayer src={referenceUrl} label="Reference" height={96} dimmed={!referenceFile} />
              {outputUrl && (
                <WaveformPlayer
                  src={outputUrl}
                  label="Output"
                  height={120}
                  download={`audio-transform-${model || 'output'}-${new Date().toISOString().slice(0, 10)}.wav`}
                />
              )}
              {!audioUrl && !outputUrl && (
                <div className="media-empty">
                  <i className="fas fa-wave-square media-empty__icon" />
                  <p>Choose an audio file (and optional reference) to transform</p>
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function describeRun(audioFile, referenceFile) {
  const parts = []
  if (audioFile?.name) parts.push(`audio: ${audioFile.name}`)
  if (referenceFile?.name) parts.push(`reference: ${referenceFile.name}`)
  return parts.join(' + ') || 'audio transform'
}

function resultLabel(r) {
  switch (r.kind) {
    case 'input': return 'Audio'
    case 'reference': return 'Reference'
    case 'output': return 'Output'
    default: return ''
  }
}

// AudioInput — drag-drop / file-pick for an audio file, with an inline
// mic-record tab. Emits a single File via onChange (recordings are wrapped
// as `File([blob], 'recording-XXX.wav', { type: 'audio/wav' })` so callers
// can treat them identically to uploaded files).
function AudioInput({ label, help, file, onChange }) {
  const [tab, setTab] = useState('upload') // 'upload' | 'record'
  const cap = useMediaCapture('audio')
  const [recordPending, setRecordPending] = useState(false)
  const [hover, setHover] = useState(false)

  const onDrop = (e) => {
    e.preventDefault()
    setHover(false)
    const f = e.dataTransfer.files?.[0]
    if (f) onChange(f)
  }
  const onPick = (e) => {
    const f = e.target.files?.[0]
    if (f) onChange(f)
  }

  const startRecord = async () => {
    await cap.start()
    if (cap.error) return
    setRecordPending(true)
    try {
      const promise = cap.startRecording()
      if (!promise) return
      const result = await promise
      const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19)
      onChange(new File([result.blob], `recording-${stamp}.wav`, { type: 'audio/wav' }))
    } finally {
      setRecordPending(false)
    }
  }

  const stopRecord = () => cap.stopRecording()

  const hasFile = !!file

  return (
    <div className="form-group">
      <label className="form-label">{label}</label>
      <div className="audio-transform-input">
        <div className="audio-transform-input__tabs" role="tablist">
          <button
            type="button"
            role="tab"
            aria-selected={tab === 'upload'}
            className={`audio-transform-input__tab${tab === 'upload' ? ' active' : ''}`}
            onClick={() => setTab('upload')}
          >
            <i className="fas fa-upload" /> Upload
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === 'record'}
            className={`audio-transform-input__tab${tab === 'record' ? ' active' : ''}`}
            onClick={() => setTab('record')}
          >
            <i className="fas fa-microphone" /> Record
          </button>
        </div>

        {tab === 'upload' && (
          <div
            className={`audio-transform-drop${hover ? ' audio-transform-drop--hover' : ''}`}
            onDragEnter={(e) => { e.preventDefault(); setHover(true) }}
            onDragOver={(e) => { e.preventDefault(); setHover(true) }}
            onDragLeave={() => setHover(false)}
            onDrop={onDrop}
          >
            {hasFile ? (
              <div className="audio-transform-drop__file">
                <i className="fas fa-file-audio" /> {file.name}
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => onChange(null)}>Clear</button>
              </div>
            ) : (
              <>
                <i className="fas fa-upload" /> Drop a file here or
                <label className="audio-transform-drop__pick">
                  <input type="file" accept="audio/*" onChange={onPick} hidden />
                  browse
                </label>
              </>
            )}
          </div>
        )}

        {tab === 'record' && (
          <div className="audio-transform-rec">
            {!cap.supported && (
              <div className="audio-transform-rec__notice">
                <i className="fas fa-circle-info" /> Microphone capture is unavailable in this browser.
              </div>
            )}
            {cap.supported && (
              <>
                {!cap.recording && !recordPending && (
                  <button type="button" className="btn btn-primary btn-sm" onClick={startRecord}>
                    <i className="fas fa-circle" style={{ color: '#e25555' }} /> Start recording
                  </button>
                )}
                {cap.recording && (
                  <button type="button" className="btn btn-secondary btn-sm" onClick={stopRecord}>
                    <i className="fas fa-stop" /> Stop ({cap.elapsed.toFixed(1)}s)
                  </button>
                )}
                {recordPending && !cap.recording && (
                  <div className="audio-transform-rec__pending">Encoding…</div>
                )}
                {cap.error && (
                  <div className="audio-transform-rec__notice audio-transform-rec__notice--error">
                    {cap.error}
                  </div>
                )}
                {hasFile && !cap.recording && (
                  <div className="audio-transform-drop__file" style={{ marginTop: 'var(--spacing-sm)' }}>
                    <i className="fas fa-file-audio" /> {file.name}
                    <button type="button" className="btn btn-secondary btn-sm" onClick={() => onChange(null)}>Clear</button>
                  </div>
                )}
              </>
            )}
          </div>
        )}
      </div>
      {help && <div className="form-help">{help}</div>}
    </div>
  )
}
