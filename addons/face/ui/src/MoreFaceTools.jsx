// The two developer tools behind the face workbench: detect faces and guess
// attributes (guesses, often wrong), and the raw faceprint. Ported from
// components/identity/MoreFaceTools.jsx; the shared widgets come from the SDK.
import { createBoundingBoxCanvas } from '../../../internal/identity-ui/src/BoundingBoxCanvas.jsx'

const TABS = [
  { id: 'analyze', icon: 'chart-bar', label: 'Detect and analyze' },
  { id: 'embed', icon: 'code', label: 'Embedding' },
]

export function createMoreFaceTools(ctx, api) {
  const { h, Fragment, Icon, ui } = ctx
  const { useMemo, useState } = ctx.React
  const { BoundingBoxCanvas } = createBoundingBoxCanvas(ctx)

  function MoreFaceTools({ model }) {
    const [tab, setTab] = useState('analyze')
    return (
      <div className="idn-more__body">
        <ui.TabSwitch tabs={TABS} value={tab} onChange={setTab} />
        <div className="biometrics-page__body">
          {tab === 'analyze' && <AnalyzeTab model={model} />}
          {tab === 'embed' && <EmbedTab model={model} />}
        </div>
      </div>
    )
  }

  function AnalyzeTab({ model }) {
    const [img, setImg] = useState(null)
    const [actions, setActions] = useState({ age: false, gender: false, emotion: false, race: false })
    const [antiSpoofing, setAntiSpoofing] = useState(false)
    const [loading, setLoading] = useState(false)
    const [error, setError] = useState(null)
    const [result, setResult] = useState(null)
    const [focusIdx, setFocusIdx] = useState(0)

    const submit = async (e) => {
      e.preventDefault()
      if (!model) { ctx.toast('Select a face model first', 'warning'); return }
      if (!img) { ctx.toast('Add an image to analyze', 'warning'); return }
      setLoading(true); setError(null); setResult(null); setFocusIdx(0)
      try {
        const data = await api.face.analyze({
          model,
          img: img.dataUrl,
          actions: Object.entries(actions).filter(([, v]) => v).map(([k]) => k),
          anti_spoofing: antiSpoofing,
        })
        setResult(data)
        if (!data?.faces?.length) ctx.toast('No face detected in the image', 'warning')
      } catch (err) {
        setError(err.message)
      } finally {
        setLoading(false)
      }
    }

    const boxes = useMemo(() => (result?.faces || []).map((f, i) => ({
      x: f.region.x, y: f.region.y, w: f.region.w, h: f.region.h,
      label: f.dominant_emotion || f.dominant_gender || `Face ${i + 1}`,
      sublabel: f.age ? `~${Math.round(f.age)}y` : null,
      tone: i === focusIdx ? 'accent' : 'default',
    })), [result, focusIdx])

    const faces = result?.faces || []
    const focus = faces[focusIdx]

    return (
      <form className="biometrics-twocol" onSubmit={submit}>
        <aside className="biometrics-panel">
          <h2 className="biometrics-panel__title">Analyze a face</h2>
          <ui.MediaInput mode="image" label="Source image" value={img} onChange={setImg} idPrefix="face-analyze" />

          <fieldset className="biometrics-fieldset">
            <legend>Guess attributes <small>(off by default, often wrong)</small></legend>
            <div className="biometrics-chipset" role="group">
              {['age', 'gender', 'emotion', 'race'].map(k => (
                <label key={k} className={`biometrics-chip ${actions[k] ? 'active' : ''}`}>
                  <input type="checkbox" checked={actions[k]} onChange={(e) => setActions(a => ({ ...a, [k]: e.target.checked }))} />
                  <span>{k}</span>
                </label>
              ))}
            </div>
          </fieldset>

          <div className="form-row">
            <div className="form-row__label">
              <span className="form-row__label-text">Anti-spoofing</span>
              <span className="form-row__hint">Reject photos-of-photos (requires model support).</span>
            </div>
            <label className="biometrics-switch">
              <input type="checkbox" checked={antiSpoofing} onChange={(e) => setAntiSpoofing(e.target.checked)} />
              <span aria-hidden="true" />
            </label>
          </div>

          <button type="submit" className="btn btn-primary btn-full" disabled={loading || !img}>
            {loading ? <><ui.Spinner size="sm" /> Analyzing…</> : <><Icon name="sparkles" /> Analyze</>}
          </button>
        </aside>

        <section className="biometrics-results">
          {loading && <div className="biometrics-empty"><ui.Spinner size="lg" /></div>}
          {error && <ui.ErrorWithTraceLink message={error} />}
          {!loading && !error && !result && (
            <EmptyState icon="smile"
              title="Drop a portrait to analyze"
              body="The backend will detect each face and return age, gender, emotion, and race distributions — with an optional liveness check." />
          )}
          {result && img && (
            <>
              <div className="biometrics-split">
                <div className="biometrics-split__media">
                  <BoundingBoxCanvas src={img.dataUrl} boxes={boxes} alt="Analyzed source" />
                  {faces.length > 1 && (
                    <div className="biometrics-facepicker" role="tablist" aria-label="Select face">
                      {faces.map((_, i) => (
                        <button key={i} type="button"
                          className={`biometrics-facepicker__chip ${i === focusIdx ? 'active' : ''}`}
                          onClick={() => setFocusIdx(i)}
                          aria-pressed={i === focusIdx}>
                          Face {i + 1}
                        </button>
                      ))}
                    </div>
                  )}
                </div>
                <div className="biometrics-split__aside">
                  {focus && (
                    <>
                      <div className="biometrics-summary card">
                        <div className="biometrics-summary__head">
                          <h3><Icon name="user" /> Face {focusIdx + 1}</h3>
                          {antiSpoofing && <LivenessPill isReal={focus.is_real} score={focus.antispoof_score} />}
                        </div>
                        <dl className="biometrics-summary__grid">
                          {focus.age != null && <><dt>Age</dt><dd>~{Math.round(focus.age)}</dd></>}
                          {focus.dominant_gender && <><dt>Gender</dt><dd>{focus.dominant_gender}</dd></>}
                          {focus.dominant_emotion && <><dt>Emotion</dt><dd>{focus.dominant_emotion}</dd></>}
                          {focus.dominant_race && <><dt>Race</dt><dd>{focus.dominant_race}</dd></>}
                          {focus.face_confidence != null && <><dt>Detection</dt><dd>{(focus.face_confidence * 100).toFixed(1)}%</dd></>}
                        </dl>
                      </div>
                      <ui.DistributionBars title="Gender" icon="gender" distribution={focus.gender} dominant={focus.dominant_gender} />
                      <ui.DistributionBars title="Emotion" icon="smile" distribution={focus.emotion} dominant={focus.dominant_emotion} />
                      <ui.DistributionBars title="Race" icon="globe" distribution={focus.race} dominant={focus.dominant_race} />
                    </>
                  )}
                </div>
              </div>
              <ResponseDetails data={result} />
            </>
          )}
        </section>
      </form>
    )
  }

  function EmbedTab({ model }) {
    const [img, setImg] = useState(null)
    const [loading, setLoading] = useState(false)
    const [error, setError] = useState(null)
    const [result, setResult] = useState(null)
    const [elapsedMs, setElapsedMs] = useState(null)

    const submit = async (e) => {
      e.preventDefault()
      if (!model) { ctx.toast('Select a face model first', 'warning'); return }
      if (!img) { ctx.toast('Add an image', 'warning'); return }
      setLoading(true); setError(null); setResult(null)
      const started = performance.now()
      try {
        const data = await api.face.embed({ model, img: img.dataUrl })
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
          <h2 className="biometrics-panel__title">Get a raw embedding</h2>
          <p className="biometrics-panel__note">
            Returns a single face embedding vector. This is the same representation the backend uses internally for verify, identify, and compare.
          </p>
          <ui.MediaInput mode="image" label="Image" value={img} onChange={setImg} idPrefix="face-embed" />
          <button type="submit" className="btn btn-primary btn-full" disabled={loading || !img}>
            {loading ? <><ui.Spinner size="sm" /> Embedding…</> : <><Icon name="code" /> Extract vector</>}
          </button>
        </aside>
        <section className="biometrics-results">
          {loading && <div className="biometrics-empty"><ui.Spinner size="lg" /></div>}
          {error && <ui.ErrorWithTraceLink message={error} />}
          {!loading && !error && !result && (
            <EmptyState icon="code"
              title="Get a face embedding"
              body="For developers — retrieve the raw vector for a face to store, search, or cluster outside of LocalAI." />
          )}
          {result && (
            <ui.EmbeddingInspector embedding={result.embedding} dim={result.dim} model={result.model} elapsedMs={elapsedMs} />
          )}
        </section>
      </form>
    )
  }

  function LivenessPill({ isReal, score }) {
    if (isReal == null) {
      return <span className="biometrics-pill muted"><Icon name="help-circle" /> Not checked</span>
    }
    return (
      <span className={`biometrics-pill ${isReal ? 'good' : 'bad'}`}>
        <Icon name={isReal ? 'user-shield' : 'mask'} />
        {isReal ? 'Real' : 'Spoof'}
        {score != null && <small>{(score * 100).toFixed(0)}%</small>}
      </span>
    )
  }

  // Not ui.EmptyState: this is the biometrics empty card of the core page.
  function EmptyState({ icon, title, body }) {
    return (
      <div className="biometrics-empty">
        <Icon name={icon} aria-hidden="true" />
        <h3>{title}</h3>
        <p>{body}</p>
      </div>
    )
  }

  function ResponseDetails({ data }) {
    return (
      <details className="biometrics-response">
        <summary><Icon name="chevron-right" /> Raw response</summary>
        <pre>{JSON.stringify(data, null, 2)}</pre>
      </details>
    )
  }

  return { MoreFaceTools }
}
