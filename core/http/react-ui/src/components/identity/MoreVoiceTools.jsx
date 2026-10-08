import { useMemo, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import LoadingSpinner from '../LoadingSpinner'
// eslint-disable-next-line no-unused-vars
import ErrorWithTraceLink from '../ErrorWithTraceLink'
// eslint-disable-next-line no-unused-vars
import TabSwitch from '../biometrics/TabSwitch'
// eslint-disable-next-line no-unused-vars
import MediaInput from '../biometrics/MediaInput'
// eslint-disable-next-line no-unused-vars
import WaveformStrip from '../biometrics/WaveformStrip'
// eslint-disable-next-line no-unused-vars
import DistributionBars from '../biometrics/DistributionBars'
// eslint-disable-next-line no-unused-vars
import EmbeddingInspector from '../biometrics/EmbeddingInspector'
import { voiceApi } from '../../utils/api'
import Icon from '../Icon'

// The two developer tools that sit behind the voice workbench: what a clip
// sounds like (age, gender, emotion are guesses) and the raw voiceprint.
const TABS = [
  { id: 'analyze', icon: 'waveform', label: 'Analyze' },
  { id: 'embed', icon: 'code', label: 'Embedding' },
]

const TONE_FOR_SEGMENT = ['accent', 'info', 'success', 'warning', 'data1', 'data2']

export default function MoreVoiceTools({ model, addToast }) {
  const [tab, setTab] = useState('analyze')
  return (
    <div className="idn-more__body">
      <TabSwitch tabs={TABS} value={tab} onChange={setTab} />
      <div className="biometrics-page__body">
        {tab === 'analyze' && <AnalyzeTab model={model} addToast={addToast} />}
        {tab === 'embed' && <EmbedTab model={model} addToast={addToast} />}
      </div>
    </div>
  )
}

// eslint-disable-next-line no-unused-vars
function AnalyzeTab({ model, addToast }) {
  const [audio, setAudio] = useState(null)
  const [actions, setActions] = useState({ age: false, gender: false, emotion: false })
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
          <legend>Guess attributes <small>(off by default, often wrong)</small></legend>
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
          {loading ? <><LoadingSpinner size="sm" /> Analyzing…</> : <><Icon name="sparkles" /> Analyze</>}
        </button>
      </aside>

      <section className="biometrics-results">
        {loading && <div className="biometrics-empty"><LoadingSpinner size="lg" /></div>}
        {error && <ErrorWithTraceLink message={error} />}
        {!loading && !error && !result && (
          <EmptyState icon="waveform"
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
                      <h3><Icon name="user" /> Segment {focusIdx + 1}
                        <small>· {focus.start.toFixed(2)}s – {focus.end.toFixed(2)}s</small>
                      </h3>
                    </div>
                    <dl className="biometrics-summary__grid">
                      {focus.age != null && <><dt>Age</dt><dd>~{Math.round(focus.age)}</dd></>}
                      {focus.dominant_gender && <><dt>Gender</dt><dd>{focus.dominant_gender}</dd></>}
                      {focus.dominant_emotion && <><dt>Emotion</dt><dd>{focus.dominant_emotion}</dd></>}
                    </dl>
                  </div>
                  <DistributionBars title="Gender" icon="gender" distribution={focus.gender} dominant={focus.dominant_gender} />
                  <DistributionBars title="Emotion" icon="smile" distribution={focus.emotion} dominant={focus.dominant_emotion} />
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

// eslint-disable-next-line no-unused-vars
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
          {loading ? <><LoadingSpinner size="sm" /> Embedding…</> : <><Icon name="code" /> Extract vector</>}
        </button>
      </aside>
      <section className="biometrics-results">
        {loading && <div className="biometrics-empty"><LoadingSpinner size="lg" /></div>}
        {error && <ErrorWithTraceLink message={error} />}
        {!loading && !error && !result && (
          <EmptyState icon="code"
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

// eslint-disable-next-line no-unused-vars
function EmptyState({ icon, title, body }) {
  return (
    <div className="biometrics-empty">
      <Icon name={icon} aria-hidden="true" />
      <h3>{title}</h3>
      <p>{body}</p>
    </div>
  )
}

// eslint-disable-next-line no-unused-vars
function ResponseDetails({ data }) {
  return (
    <details className="biometrics-response">
      <summary><Icon name="chevron-right" /> Raw response</summary>
      <pre>{JSON.stringify(data, null, 2)}</pre>
    </details>
  )
}
