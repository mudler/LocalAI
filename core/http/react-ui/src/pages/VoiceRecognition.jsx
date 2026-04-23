import { useEffect, useMemo, useState } from 'react'
import { useOutletContext, useParams } from 'react-router-dom'
import ModelSelector from '../components/ModelSelector'
import LoadingSpinner from '../components/LoadingSpinner'
import ErrorWithTraceLink from '../components/ErrorWithTraceLink'
import TabSwitch from '../components/biometrics/TabSwitch'
import MediaInput from '../components/biometrics/MediaInput'
import WaveformStrip from '../components/biometrics/WaveformStrip'
import MatchGauge from '../components/biometrics/MatchGauge'
import DistributionBars from '../components/biometrics/DistributionBars'
import EnrollmentList from '../components/biometrics/EnrollmentList'
import EmbeddingInspector from '../components/biometrics/EmbeddingInspector'
import { CAP_SPEAKER_RECOGNITION } from '../utils/capabilities'
import { voiceApi } from '../utils/api'

const TABS = [
  { id: 'analyze', icon: 'fas fa-wave-square',  label: 'Analyze' },
  { id: 'compare', icon: 'fas fa-people-arrows',    label: 'Compare' },
  { id: 'enroll',  icon: 'fas fa-id-badge',         label: 'Enrollment' },
  { id: 'embed',   icon: 'fas fa-code',             label: 'Embedding' },
]

const ENROLL_KEY = 'localai_voice_enrollments'

function loadEnrollments() {
  try {
    const raw = localStorage.getItem(ENROLL_KEY)
    if (!raw) return []
    const p = JSON.parse(raw)
    return Array.isArray(p) ? p : []
  } catch (_) { return [] }
}

function saveEnrollments(list) {
  try { localStorage.setItem(ENROLL_KEY, JSON.stringify(list.slice(0, 50))) } catch (_) { /* quota */ }
}

function parseLabels(text) {
  const out = {}
  if (!text) return out
  for (const line of text.split('\n')) {
    const idx = line.indexOf(':')
    if (idx === -1) continue
    const k = line.slice(0, idx).trim()
    const v = line.slice(idx + 1).trim()
    if (k) out[k] = v
  }
  return out
}

const TONE_FOR_SEGMENT = ['accent', 'info', 'success', 'warning', 'data1', 'data2']

export default function VoiceRecognition() {
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const [model, setModel] = useState(urlModel || '')
  const [tab, setTab] = useState('analyze')

  return (
    <div className="biometrics-page">
      <header className="biometrics-page__header">
        <div>
          <h1 className="page-title"><i className="fas fa-microphone-lines" aria-hidden="true" /> Voice Recognition</h1>
          <p className="page-subtitle">
            Compare, identify, and analyze speakers — the audio analog to face recognition. Record directly from your microphone or upload a clip.
          </p>
        </div>
        <div className="biometrics-page__model">
          <label className="form-label">Model</label>
          <ModelSelector value={model} onChange={setModel} capability={CAP_SPEAKER_RECOGNITION} />
        </div>
      </header>

      <TabSwitch tabs={TABS} value={tab} onChange={setTab} />

      <div className="biometrics-page__body">
        {tab === 'analyze' && <AnalyzeTab model={model} addToast={addToast} />}
        {tab === 'compare' && <CompareTab model={model} addToast={addToast} />}
        {tab === 'enroll' && <EnrollTab model={model} addToast={addToast} />}
        {tab === 'embed' && <EmbedTab model={model} addToast={addToast} />}
      </div>
    </div>
  )
}

// ──────────────────────────── Analyze ────────────────────────────

function AnalyzeTab({ model, addToast }) {
  const [audio, setAudio] = useState(null)
  const [actions, setActions] = useState({ age: true, gender: true, emotion: true })
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [result, setResult] = useState(null)
  const [focusIdx, setFocusIdx] = useState(0)

  const submit = async (e) => {
    e.preventDefault()
    if (!model) { addToast('Select a speaker model first', 'warning'); return }
    if (!audio) { addToast('Add an audio clip', 'warning'); return }
    setLoading(true); setError(null); setResult(null); setFocusIdx(0)
    try {
      const data = await voiceApi.analyze({
        model,
        audio: audio.dataUrl,
        actions: Object.entries(actions).filter(([, v]) => v).map(([k]) => k),
      })
      setResult(data)
      if (!data?.segments?.length) addToast('No speech segments detected', 'warning')
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  const segments = useMemo(() => result?.segments || [], [result])
  const focus = segments[focusIdx]
  const waveformSegments = useMemo(() => segments.map((s, i) => ({
    start: s.start, end: s.end,
    label: s.dominant_emotion || s.dominant_gender || `#${i + 1}`,
    tone: i === focusIdx ? 'accent' : TONE_FOR_SEGMENT[i % TONE_FOR_SEGMENT.length],
  })), [segments, focusIdx])

  return (
    <form className="biometrics-twocol" onSubmit={submit}>
      <aside className="biometrics-panel">
        <h2 className="biometrics-panel__title">Analyze a speaker</h2>
        <MediaInput mode="audio" label="Audio clip" value={audio} onChange={setAudio} idPrefix="voice-analyze" />
        <fieldset className="biometrics-fieldset">
          <legend>Attributes</legend>
          <div className="biometrics-chipset" role="group">
            {['age', 'gender', 'emotion'].map(k => (
              <label key={k} className={`biometrics-chip ${actions[k] ? 'active' : ''}`}>
                <input type="checkbox" checked={actions[k]} onChange={(e) => setActions(a => ({ ...a, [k]: e.target.checked }))} />
                <span>{k}</span>
              </label>
            ))}
          </div>
        </fieldset>
        <button type="submit" className="btn btn-primary btn-full" disabled={loading || !audio}>
          {loading ? <><LoadingSpinner size="sm" /> Analyzing…</> : <><i className="fas fa-wand-magic-sparkles" /> Analyze</>}
        </button>
      </aside>

      <section className="biometrics-results">
        {loading && <div className="biometrics-empty"><LoadingSpinner size="lg" /></div>}
        {error && <ErrorWithTraceLink message={error} />}
        {!loading && !error && !result && (
          <EmptyState icon="fas fa-wave-square"
            title="Record or upload a clip to analyze"
            body="The backend will segment the audio by speaker turn and infer age, gender, and emotion per segment." />
        )}
        {result && audio && (
          <>
            <WaveformStrip src={audio.dataUrl} segments={waveformSegments} />
            {segments.length > 1 && (
              <div className="biometrics-facepicker" role="tablist" aria-label="Select segment">
                {segments.map((s, i) => (
                  <button key={i} type="button"
                    className={`biometrics-facepicker__chip ${i === focusIdx ? 'active' : ''}`}
                    onClick={() => setFocusIdx(i)}
                    aria-pressed={i === focusIdx}>
                    #{i + 1} <small>{s.start.toFixed(1)}s–{s.end.toFixed(1)}s</small>
                  </button>
                ))}
              </div>
            )}
            {focus && (
              <div className="biometrics-split">
                <div className="biometrics-split__aside" style={{ gridColumn: '1 / -1' }}>
                  <div className="biometrics-summary card">
                    <div className="biometrics-summary__head">
                      <h3><i className="fas fa-user" /> Segment {focusIdx + 1}
                        <small>· {focus.start.toFixed(2)}s – {focus.end.toFixed(2)}s</small>
                      </h3>
                    </div>
                    <dl className="biometrics-summary__grid">
                      {focus.age != null && <><dt>Age</dt><dd>~{Math.round(focus.age)}</dd></>}
                      {focus.dominant_gender && <><dt>Gender</dt><dd>{focus.dominant_gender}</dd></>}
                      {focus.dominant_emotion && <><dt>Emotion</dt><dd>{focus.dominant_emotion}</dd></>}
                    </dl>
                  </div>
                  <DistributionBars title="Gender" icon="fas fa-venus-mars" distribution={focus.gender} dominant={focus.dominant_gender} />
                  <DistributionBars title="Emotion" icon="fas fa-face-smile-beam" distribution={focus.emotion} dominant={focus.dominant_emotion} />
                </div>
              </div>
            )}
            <ResponseDetails data={result} />
          </>
        )}
      </section>
    </form>
  )
}

// ──────────────────────────── Compare ────────────────────────────

function CompareTab({ model, addToast }) {
  const [audio1, setAudio1] = useState(null)
  const [audio2, setAudio2] = useState(null)
  const [threshold, setThreshold] = useState(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [result, setResult] = useState(null)

  const submit = async (e) => {
    e.preventDefault()
    if (!model) { addToast('Select a speaker model first', 'warning'); return }
    if (!audio1 || !audio2) { addToast('Add both clips to compare', 'warning'); return }
    setLoading(true); setError(null); setResult(null)
    try {
      const body = { model, audio1: audio1.dataUrl, audio2: audio2.dataUrl }
      if (threshold != null) body.threshold = threshold
      const data = await voiceApi.verify(body)
      setResult(data)
      if (threshold == null && data?.threshold) setThreshold(data.threshold)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  const effective = useMemo(() => {
    if (!result) return null
    const t = threshold ?? result.threshold
    const verified = result.distance <= t
    const confidence = Math.max(0, Math.min(100, 100 * (1 - result.distance / t)))
    return { verified, confidence, threshold: t, distance: result.distance }
  }, [result, threshold])

  return (
    <form className="biometrics-twocol" onSubmit={submit}>
      <aside className="biometrics-panel">
        <h2 className="biometrics-panel__title">Compare two voices</h2>
        <MediaInput mode="audio" label="First clip" value={audio1} onChange={setAudio1} idPrefix="voice-cmp-1" />
        <MediaInput mode="audio" label="Second clip" value={audio2} onChange={setAudio2} idPrefix="voice-cmp-2" />
        <button type="submit" className="btn btn-primary btn-full" disabled={loading || !audio1 || !audio2}>
          {loading ? <><LoadingSpinner size="sm" /> Comparing…</> : <><i className="fas fa-equals" /> Compare</>}
        </button>
      </aside>

      <section className="biometrics-results">
        {loading && <div className="biometrics-empty"><LoadingSpinner size="lg" /></div>}
        {error && <ErrorWithTraceLink message={error} />}
        {!loading && !error && !result && (
          <EmptyState icon="fas fa-people-arrows"
            title="Drop two clips to compare"
            body="We extract a speaker embedding for each clip and report the cosine distance — a match is declared when the distance is below the threshold." />
        )}
        {result && effective && (
          <>
            <div className="biometrics-compare biometrics-compare--voice">
              <div className="biometrics-compare__panel">
                <div className="biometrics-compare__label">Clip 1</div>
                <WaveformStrip src={audio1?.dataUrl} height={80} />
              </div>
              <div className="biometrics-compare__center">
                <MatchGauge
                  distance={effective.distance}
                  threshold={effective.threshold}
                  confidence={effective.confidence}
                  verified={effective.verified}
                />
                <div className="biometrics-compare__threshold">
                  <label htmlFor="voice-threshold">Threshold <code>{effective.threshold.toFixed(3)}</code></label>
                  <input id="voice-threshold" type="range" min="0" max="1" step="0.005"
                    value={effective.threshold}
                    onChange={(e) => setThreshold(parseFloat(e.target.value))} />
                  <p className="biometrics-compare__hint">
                    Drag to see how the verdict changes. The backend default is <code>{result.threshold?.toFixed(3)}</code>.
                  </p>
                </div>
              </div>
              <div className="biometrics-compare__panel">
                <div className="biometrics-compare__label">Clip 2</div>
                <WaveformStrip src={audio2?.dataUrl} height={80} />
              </div>
            </div>
            <ResponseDetails data={result} />
          </>
        )}
      </section>
    </form>
  )
}

// ──────────────────────────── Enrollment ────────────────────────────

function EnrollTab({ model, addToast }) {
  const [enrolled, setEnrolled] = useState(loadEnrollments)
  const [enrollName, setEnrollName] = useState('')
  const [enrollLabels, setEnrollLabels] = useState('')
  const [enrollAudio, setEnrollAudio] = useState(null)
  const [enrolling, setEnrolling] = useState(false)
  const [enrollErr, setEnrollErr] = useState(null)
  const [lastEnrolled, setLastEnrolled] = useState(null)

  const [probeAudio, setProbeAudio] = useState(null)
  const [topK, setTopK] = useState(5)
  const [threshold, setThreshold] = useState(0.25)
  const [identifying, setIdentifying] = useState(false)
  const [identifyErr, setIdentifyErr] = useState(null)
  const [identifyResult, setIdentifyResult] = useState(null)

  useEffect(() => { saveEnrollments(enrolled) }, [enrolled])

  const enroll = async (e) => {
    e.preventDefault()
    if (!model) { addToast('Select a speaker model first', 'warning'); return }
    if (!enrollName.trim()) { addToast('Give this speaker a name', 'warning'); return }
    if (!enrollAudio) { addToast('Add a sample clip', 'warning'); return }
    setEnrolling(true); setEnrollErr(null)
    try {
      const data = await voiceApi.register({
        model,
        name: enrollName.trim(),
        audio: enrollAudio.dataUrl,
        labels: parseLabels(enrollLabels),
      })
      const entry = {
        id: data.id,
        name: data.name,
        labels: parseLabels(enrollLabels),
        sampleUrl: enrollAudio.dataUrl,
        registeredAt: data.registered_at || new Date().toISOString(),
      }
      setEnrolled(prev => [entry, ...prev])
      setLastEnrolled(entry.id)
      setEnrollName(''); setEnrollLabels(''); setEnrollAudio(null)
      addToast(`Enrolled ${entry.name}`, 'success')
    } catch (err) {
      setEnrollErr(err.message)
    } finally {
      setEnrolling(false)
    }
  }

  const forget = async (entry) => {
    try {
      await voiceApi.forget({ id: entry.id })
      setEnrolled(prev => prev.filter(e => e.id !== entry.id))
      addToast(`Removed ${entry.name}`, 'info')
    } catch (err) {
      if (err.status === 404) {
        setEnrolled(prev => prev.filter(e => e.id !== entry.id))
        addToast(`${entry.name} was already gone from the backend store`, 'warning')
      } else {
        addToast(err.message, 'error')
      }
    }
  }

  const identify = async (e) => {
    e.preventDefault()
    if (!model) { addToast('Select a speaker model first', 'warning'); return }
    if (!probeAudio) { addToast('Add a probe clip', 'warning'); return }
    setIdentifying(true); setIdentifyErr(null); setIdentifyResult(null)
    try {
      const data = await voiceApi.identify({
        model,
        audio: probeAudio.dataUrl,
        top_k: topK,
        threshold,
      })
      setIdentifyResult(data)
      if (!data?.matches?.length) addToast('No matches above threshold', 'info')
    } catch (err) {
      setIdentifyErr(err.message)
    } finally {
      setIdentifying(false)
    }
  }

  return (
    <div className="biometrics-enrollgrid">
      <section className="biometrics-enrollgrid__register card">
        <h2 className="biometrics-panel__title"><i className="fas fa-user-plus" /> Enroll a voice</h2>
        <form onSubmit={enroll}>
          <div className="form-group">
            <label className="form-label" htmlFor="voice-enroll-name">Name</label>
            <input id="voice-enroll-name" className="input" value={enrollName}
              onChange={(e) => setEnrollName(e.target.value)} placeholder="e.g. Alice Johnson" />
          </div>
          <div className="form-group">
            <label className="form-label" htmlFor="voice-enroll-labels">Labels <span className="form-label__hint">(optional, one per line)</span></label>
            <textarea id="voice-enroll-labels" className="textarea" rows={2}
              placeholder={"team: engineering\nrole: lead"}
              value={enrollLabels} onChange={(e) => setEnrollLabels(e.target.value)} />
          </div>
          <MediaInput mode="audio" label="Sample clip" value={enrollAudio} onChange={setEnrollAudio} idPrefix="voice-enroll" />
          <button type="submit" className="btn btn-primary btn-full" disabled={enrolling}>
            {enrolling ? <><LoadingSpinner size="sm" /> Enrolling…</> : <><i className="fas fa-plus" /> Enroll</>}
          </button>
          {enrollErr && <div className="biometrics-enrollgrid__err"><ErrorWithTraceLink message={enrollErr} /></div>}
        </form>
      </section>

      <section className="biometrics-enrollgrid__identify card">
        <h2 className="biometrics-panel__title"><i className="fas fa-magnifying-glass" /> Identify a speaker</h2>
        <form onSubmit={identify}>
          <MediaInput mode="audio" label="Probe clip" value={probeAudio} onChange={setProbeAudio} idPrefix="voice-probe" />
          <div className="form-grid-2col">
            <div className="form-group">
              <label className="form-label" htmlFor="voice-topk">Top-K</label>
              <input id="voice-topk" type="number" min="1" max="25" className="input"
                value={topK} onChange={(e) => setTopK(parseInt(e.target.value) || 1)} />
            </div>
            <div className="form-group">
              <label className="form-label" htmlFor="voice-threshold-id">Threshold</label>
              <input id="voice-threshold-id" type="number" min="0" max="1" step="0.01" className="input"
                value={threshold} onChange={(e) => setThreshold(parseFloat(e.target.value) || 0)} />
            </div>
          </div>
          <button type="submit" className="btn btn-primary btn-full" disabled={identifying || !probeAudio}>
            {identifying ? <><LoadingSpinner size="sm" /> Searching…</> : <><i className="fas fa-magnifying-glass" /> Identify</>}
          </button>
          {identifyErr && <div className="biometrics-enrollgrid__err"><ErrorWithTraceLink message={identifyErr} /></div>}
          {identifyResult && <MatchesList matches={identifyResult.matches || []} enrolled={enrolled} />}
        </form>
      </section>

      <section className="biometrics-enrollgrid__list">
        <div className="biometrics-enroll__head">
          <h2 className="biometrics-panel__title"><i className="fas fa-id-badge" /> Enrolled <span className="biometrics-enroll__count">{enrolled.length}</span></h2>
        </div>
        <EnrollmentList entries={enrolled} onDelete={forget} mode="audio" highlightId={lastEnrolled} />
      </section>
    </div>
  )
}

function MatchesList({ matches, enrolled }) {
  if (!matches.length) {
    return <div className="biometrics-matches__empty">No candidates above threshold.</div>
  }
  return (
    <ul className="biometrics-matches" aria-label="Matches">
      {matches.map((m, i) => {
        const record = enrolled.find(e => e.id === m.id)
        const conf = Math.max(0, Math.min(100, m.confidence ?? 0))
        return (
          <li key={m.id} className={`biometrics-matches__row ${m.match ? 'match' : 'miss'}`}>
            <div className="biometrics-matches__rank">#{i + 1}</div>
            <div className="biometrics-matches__avatar">
              <span>{(m.name || '?').slice(0, 2).toUpperCase()}</span>
            </div>
            <div className="biometrics-matches__body">
              <div className="biometrics-matches__name">
                <strong>{m.name || m.id}</strong>
                {m.match ? <span className="biometrics-matches__badge match"><i className="fas fa-check" /> match</span>
                         : <span className="biometrics-matches__badge miss">below threshold</span>}
              </div>
              {record?.sampleUrl && (
                <audio controls src={record.sampleUrl} className="biometrics-matches__preview" />
              )}
              <div className="biometrics-matches__meter" aria-hidden="true">
                <div className="biometrics-matches__fill" style={{ width: `${conf}%` }} />
              </div>
              <div className="biometrics-matches__meta">
                <span>distance <code>{m.distance?.toFixed?.(4) ?? '—'}</code></span>
                <span>confidence <code>{conf.toFixed(1)}%</code></span>
              </div>
            </div>
          </li>
        )
      })}
    </ul>
  )
}

// ──────────────────────────── Embedding ────────────────────────────

function EmbedTab({ model, addToast }) {
  const [audio, setAudio] = useState(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [result, setResult] = useState(null)
  const [elapsedMs, setElapsedMs] = useState(null)

  const submit = async (e) => {
    e.preventDefault()
    if (!model) { addToast('Select a speaker model first', 'warning'); return }
    if (!audio) { addToast('Add an audio clip', 'warning'); return }
    setLoading(true); setError(null); setResult(null)
    const started = performance.now()
    try {
      const data = await voiceApi.embed({ model, audio: audio.dataUrl })
      setElapsedMs(performance.now() - started)
      setResult(data)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <form className="biometrics-twocol" onSubmit={submit}>
      <aside className="biometrics-panel">
        <h2 className="biometrics-panel__title">Get a raw speaker embedding</h2>
        <p className="biometrics-panel__note">
          Returns a speaker-encoder vector — the same representation the backend uses internally for verify and identify.
        </p>
        <MediaInput mode="audio" label="Audio clip" value={audio} onChange={setAudio} idPrefix="voice-embed" />
        <button type="submit" className="btn btn-primary btn-full" disabled={loading || !audio}>
          {loading ? <><LoadingSpinner size="sm" /> Embedding…</> : <><i className="fas fa-code" /> Extract vector</>}
        </button>
      </aside>
      <section className="biometrics-results">
        {loading && <div className="biometrics-empty"><LoadingSpinner size="lg" /></div>}
        {error && <ErrorWithTraceLink message={error} />}
        {!loading && !error && !result && (
          <EmptyState icon="fas fa-code"
            title="Get a speaker embedding"
            body="For developers — retrieve the raw vector for a voice to store, search, or cluster outside of LocalAI." />
        )}
        {result && (
          <EmbeddingInspector embedding={result.embedding} dim={result.dim} model={result.model} elapsedMs={elapsedMs} />
        )}
      </section>
    </form>
  )
}

function EmptyState({ icon, title, body }) {
  return (
    <div className="biometrics-empty">
      <i className={icon} aria-hidden="true" />
      <h3>{title}</h3>
      <p>{body}</p>
    </div>
  )
}

function ResponseDetails({ data }) {
  return (
    <details className="biometrics-response">
      <summary><i className="fas fa-angle-right" aria-hidden="true" /> Raw response</summary>
      <pre>{JSON.stringify(data, null, 2)}</pre>
    </details>
  )
}
