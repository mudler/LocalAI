import { useEffect, useRef, useState } from 'react'
import { useMediaCapture } from '../../hooks/useMediaCapture'
import { fileToBase64 } from '../../utils/api'

// MediaInput — one control, three ways to supply a sample.
// mode: 'image' | 'audio'. onChange receives null | { base64, dataUrl, mime, source }.
function UnsupportedNotice({ mode }) {
  // Detect the likely cause so we can tell the user what to do, instead of just "not supported".
  const isSecure = typeof window !== 'undefined' && (window.isSecureContext ?? true)
  const hostname = typeof window !== 'undefined' ? window.location.hostname : ''
  const origin = typeof window !== 'undefined' ? window.location.origin : ''
  const thing = mode === 'image' ? 'webcam' : 'microphone'

  if (!isSecure) {
    return (
      <div className="biometrics-mediainput__notice">
        <i className="fas fa-lock" aria-hidden="true" />
        <div>
          <strong>{thing} needs a secure origin</strong>
          <p>
            Your browser only exposes <code>getUserMedia</code> over HTTPS, <code>localhost</code>,
            or <code>127.0.0.1</code>. You're on <code>{origin || hostname}</code>. Reach the UI
            via <code>http://localhost:&lt;port&gt;</code> (or put a TLS terminator in front) and the
            live {thing} will light up. Upload still works fine from here.
          </p>
        </div>
      </div>
    )
  }
  return (
    <div className="biometrics-mediainput__notice">
      <i className="fas fa-circle-info" aria-hidden="true" />
      <div>
        <strong>Live {thing} not available</strong>
        <p>
          This browser doesn't expose <code>navigator.mediaDevices.getUserMedia</code>. Try another
          browser, or use the upload tab — the backend accepts either.
        </p>
      </div>
    </div>
  )
}

export default function MediaInput({ mode, label, value, onChange, idPrefix = 'media' }) {
  const [tab, setTab] = useState('file') // 'file' | 'live'
  const fileRef = useRef(null)
  const cap = useMediaCapture(mode)

  // Release the device when switching away from the live tab.
  useEffect(() => {
    if (tab !== 'live' && cap.active) cap.stop()
  }, [tab]) // eslint-disable-line react-hooks/exhaustive-deps

  const handleFile = async (e) => {
    const f = e.target.files?.[0]
    if (!f) { onChange(null); return }
    const base64 = await fileToBase64(f)
    const dataUrl = await new Promise((resolve) => {
      const reader = new FileReader()
      reader.onload = () => resolve(reader.result)
      reader.readAsDataURL(f)
    })
    onChange({ base64, dataUrl, mime: f.type, source: 'file', name: f.name })
  }

  const handleSnap = () => {
    const shot = cap.snap()
    if (shot) onChange({ ...shot, source: 'live' })
  }

  const handleRecordToggle = async () => {
    if (cap.recording) {
      cap.stopRecording()
    } else {
      const pending = cap.startRecording()
      if (!pending) return
      const result = await pending
      onChange({ ...result, source: 'live' })
    }
  }

  const clear = () => {
    onChange(null)
    if (fileRef.current) fileRef.current.value = ''
  }

  const inputId = `${idPrefix}-${mode}-file`

  return (
    <div className="biometrics-mediainput">
      {label && <label className="form-label" htmlFor={inputId}>{label}</label>}

      <div className="biometrics-mediainput__tabs" role="tablist" aria-label={`${label || 'Media'} source`}>
        <button type="button" role="tab" aria-selected={tab === 'file'}
          className={`biometrics-mediainput__tab ${tab === 'file' ? 'active' : ''}`}
          onClick={() => setTab('file')}>
          <i className="fas fa-upload" aria-hidden="true" /> Upload
        </button>
        <button type="button" role="tab" aria-selected={tab === 'live'}
          className={`biometrics-mediainput__tab ${tab === 'live' ? 'active' : ''}`}
          onClick={() => setTab('live')}>
          <i className={`fas ${mode === 'image' ? 'fa-camera' : 'fa-microphone'}`} aria-hidden="true" />
          {mode === 'image' ? ' Webcam' : ' Record'}
        </button>
      </div>

      {tab === 'file' && (
        <div className="biometrics-mediainput__body">
          <input
            ref={fileRef}
            id={inputId}
            type="file"
            className="input"
            accept={mode === 'image' ? 'image/*' : 'audio/*'}
            onChange={handleFile}
          />
        </div>
      )}

      {tab === 'live' && (
        <div className="biometrics-mediainput__body">
          {!cap.supported && <UnsupportedNotice mode={mode} />}
          {cap.supported && !cap.active && (
            <button type="button" className="btn btn-secondary btn-full" onClick={cap.start}>
              <i className={`fas ${mode === 'image' ? 'fa-camera' : 'fa-microphone'}`} aria-hidden="true" />
              {mode === 'image' ? ' Start webcam' : ' Enable microphone'}
            </button>
          )}
          {cap.active && mode === 'image' && (
            <div className="biometrics-mediainput__live">
              <video ref={cap.videoRef} autoPlay muted playsInline className="biometrics-mediainput__video" />
              <div className="biometrics-mediainput__controls">
                <button type="button" className="btn btn-primary" onClick={handleSnap}>
                  <i className="fas fa-circle-dot" aria-hidden="true" /> Capture
                </button>
                <button type="button" className="btn btn-secondary" onClick={cap.stop}>Stop</button>
              </div>
            </div>
          )}
          {cap.active && mode === 'audio' && (
            <div className="biometrics-mediainput__live">
              <div className={`biometrics-mediainput__meter ${cap.recording ? 'recording' : ''}`}>
                <i className="fas fa-microphone" aria-hidden="true" />
                <span>{cap.recording ? `Recording… ${cap.elapsed.toFixed(1)}s` : 'Microphone ready'}</span>
              </div>
              <div className="biometrics-mediainput__controls">
                <button type="button" className={`btn ${cap.recording ? 'btn-secondary' : 'btn-primary'}`} onClick={handleRecordToggle}>
                  <i className={`fas ${cap.recording ? 'fa-stop' : 'fa-circle'}`} aria-hidden="true" />
                  {cap.recording ? ' Stop' : ' Record'}
                </button>
                <button type="button" className="btn btn-secondary" onClick={cap.stop} disabled={cap.recording}>Close</button>
              </div>
            </div>
          )}
          {cap.error && (
            <p className="biometrics-mediainput__error" role="alert">{cap.error}</p>
          )}
        </div>
      )}

      {value && (
        <div className="biometrics-mediainput__preview">
          {mode === 'image'
            ? <img src={value.dataUrl} alt="" />
            : <audio controls src={value.dataUrl} />}
          <div className="biometrics-mediainput__preview-meta">
            <span className="biometrics-mediainput__source-pill">
              <i className={`fas ${value.source === 'live' ? (mode === 'image' ? 'fa-camera' : 'fa-microphone') : 'fa-file'}`} aria-hidden="true" />
              {value.source === 'live' ? ' Captured' : ` ${value.name || 'Uploaded'}`}
            </span>
            <button type="button" className="biometrics-mediainput__clear" onClick={clear} aria-label="Remove sample">
              <i className="fas fa-xmark" aria-hidden="true" />
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
