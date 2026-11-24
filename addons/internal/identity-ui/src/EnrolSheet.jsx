import { labelsText, parseLabels } from './identity.js'
import { createReferenceAudio } from './referenceAudio.js'

// A saved copy of a sample lives in localStorage, which is small. A larger one
// is not kept, and the sheet says so instead of failing quietly.
const KEEP_LIMIT = 1_500_000

const READ_ALOUD = 'The harbour was quiet before the ferry arrived. Two gulls argued over a crust, and somebody carried a ladder down to the water.'

// The form that enrols one person. The shell draws it in a side panel
// (ctx.openPanel), which has no footer slot, so the actions close the form.
// deps = { ClipInput, ErrorNote, api }
export function createEnrolSheet(ctx, deps) {
  const { h, Fragment, t, Icon, ui } = ctx
  const { useMemo, useState } = ctx.React
  const { ClipInput, ErrorNote, api } = deps
  const { normalizeAudioSample } = createReferenceAudio(ctx)

  // Enrol one person: a sample, who it is, and permission. For a voice, an admin
  // can also keep the same recording as a speech voice, which is a different
  // store with its own transcript and consent record.
  function EnrolSheet({ kind, model, canSpeech, prefill, onClose, onDone }) {
    const isFace = kind === 'face'
    const [sample, setSample] = useState(prefill?.sample || null)
    const [name, setName] = useState(prefill?.name || '')
    const [labels, setLabels] = useState(labelsText(prefill?.labels).replaceAll(', ', '\n'))
    const [asSpeech, setAsSpeech] = useState(false)
    const [transcript, setTranscript] = useState('')
    const [agreed, setAgreed] = useState(false)
    const [keep, setKeep] = useState(false)
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState(null)

    const needs = useMemo(() => {
      const out = []
      if (!model) out.push(t('enrol.needModel'))
      if (!sample) out.push(t(isFace ? 'enrol.needPhoto' : 'enrol.needRecording'))
      if (!name.trim()) out.push(t('enrol.needName'))
      if (asSpeech && !transcript.trim()) out.push(t('enrol.needTranscript'))
      if (!agreed) out.push(t('enrol.needPermission'))
      return out
    }, [model, sample, name, asSpeech, transcript, agreed, isFace])

    const submit = async (e) => {
      e.preventDefault()
      if (needs.length || busy) return
      setBusy(true)
      setError(null)
      let data
      try {
        const parsed = parseLabels(labels)
        data = isFace
          ? await api.identity.face.register({ model, name: name.trim(), img: sample.dataUrl, labels: parsed })
          : await api.identity.voice.register({ model, name: name.trim(), audio: sample.dataUrl, labels: parsed })
        const small = sample.dataUrl.length <= KEEP_LIMIT
        const kept = keep && small
        const entry = {
          id: data.id,
          name: data.name,
          labels: parsed,
          model,
          registeredAt: data.registered_at || new Date().toISOString(),
          ...(kept ? (isFace ? { thumbnail: sample.dataUrl } : { sampleUrl: sample.dataUrl }) : {}),
        }
        let speechError = null
        if (asSpeech && canSpeech && !isFace) {
          try {
            const wav = await normalizeAudioSample(sample)
            const form = new FormData()
            form.append('name', name.trim())
            form.append('description', '')
            form.append('language', '')
            form.append('transcript', transcript.trim())
            form.append('consent_confirmed', 'true')
            form.append('audio', wav.blob, 'reference.wav')
            await api.voiceProfiles.create(form)
          } catch (err) {
            speechError = err
          }
        }
        onDone(entry, { keptTooBig: keep && !small, speechError, asSpeech })
      } catch (err) {
        setError(err)
      } finally {
        setBusy(false)
      }
    }

    return (
      <form id="idn-enrol-form" className="idn-sheet" onSubmit={submit} data-testid="enrol-sheet">
        <p className="dk-hint">{t(`${kind}.enrolIntro`)}</p>
        <section className="idn-sheet__step">
          <h3 className="idn-sheet__h"><span className="idn-sheet__n">1</span> {t(isFace ? 'enrol.photoStep' : 'enrol.recordingStep')}</h3>
          <ClipInput kind={isFace ? 'image' : 'audio'} label={t(isFace ? 'enrol.photoLabel' : 'enrol.recordingLabel')} value={sample} onChange={setSample} idPrefix={`${kind}-enrol`} prompt={isFace ? null : READ_ALOUD} />
        </section>

        <section className="idn-sheet__step">
          <h3 className="idn-sheet__h"><span className="idn-sheet__n">2</span> {t('enrol.whoStep')}</h3>
          <div className="dk-field">
            <label className="dk-label" htmlFor={`${kind}-enrol-name`}>{t('enrol.name')}</label>
            <input id={`${kind}-enrol-name`} className="dk-input" value={name} onChange={(e) => setName(e.target.value)} placeholder={t('enrol.namePlaceholder')} autoComplete="off" />
          </div>
          <div className="dk-field">
            <label className="dk-label" htmlFor={`${kind}-enrol-labels`}>{t('enrol.labels')} <span className="dk-hint">{t('enrol.labelsHint')}</span></label>
            <textarea id={`${kind}-enrol-labels`} className="dk-textarea" rows={2} placeholder={'team: platform'} value={labels} onChange={(e) => setLabels(e.target.value)} />
          </div>
          {canSpeech && !isFace && (
            <div className="dk-field">
              <label className="dk-choice idn-sheet__choice">
                <input className="dk-check" type="checkbox" checked={asSpeech} onChange={(e) => setAsSpeech(e.target.checked)} />
                <span><strong>{t('enrol.asSpeech')}</strong><span className="dk-hint">{t('enrol.asSpeechHint')}</span></span>
              </label>
              {asSpeech && (
                <>
                  <label className="dk-label" htmlFor="voice-enrol-transcript">{t('enrol.transcript')}</label>
                  <textarea id="voice-enrol-transcript" className="dk-textarea" rows={3} maxLength={4000} value={transcript} onChange={(e) => setTranscript(e.target.value)} />
                </>
              )}
            </div>
          )}
        </section>

        <section className="idn-sheet__step">
          <h3 className="idn-sheet__h"><span className="idn-sheet__n">3</span> {t('enrol.permissionStep')}</h3>
          <label className="dk-choice idn-sheet__choice">
            <input className="dk-check" type="checkbox" checked={agreed} onChange={(e) => setAgreed(e.target.checked)} />
            <span><strong>{t('enrol.agreed')}</strong><span className="dk-hint">{t(`${kind}.agreedHint`)}</span></span>
          </label>
          <label className="dk-choice idn-sheet__choice">
            <input className="dk-check" type="checkbox" checked={keep} onChange={(e) => setKeep(e.target.checked)} />
            <span><strong>{t('enrol.keep')}</strong><span className="dk-hint">{t(`${kind}.keepHint`)}</span></span>
          </label>
        </section>

        {error && <ErrorNote error={error} kind={kind} />}

        <div className="idn-sheet__foot">
          <span className="idn-sheet__need" data-testid="enrol-needs">{needs.length ? t('enrol.stillNeeded', { list: needs.join(', ') }) : t('enrol.ready')}</span>
          <ui.Button variant="ghost" onClick={onClose}>{t('enrol.cancel')}</ui.Button>
          <ui.Button variant="primary" type="submit" busy={busy} disabled={needs.length > 0}>
            {busy ? t('enrol.working') : <>{t('enrol.submit')} <Icon name="check" /></>}
          </ui.Button>
        </div>
      </form>
    )
  }
  return { EnrolSheet }
}
