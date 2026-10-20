import { buildSpeechRequest, speak } from './speechRequest.js'

const CAP_TTS = 'FLAG_TTS'

export function createSpeech(ctx) {
  const { h, t, ui, hooks, viewer } = ctx
  const { useState, useEffect, useMemo } = ctx.React

  function Speech({ path }) {
    const [model, setModel] = useState(ctx.modelFrom(path))
    const [models, setModels] = useState({ status: 'loading', list: [] })
    const [profiles, setProfiles] = useState([])
    const [voiceId, setVoiceId] = useState(ctx.useSearch().get('voice') || '')
    const [manualVoice, setManualVoice] = useState('')
    const [text, setText] = useState('')
    const [instructions, setInstructions] = useState('')
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState('')
    const [blob, setBlob] = useState(null)
    const audioUrl = hooks.useObjectUrl(blob)

    useEffect(() => {
      let live = true
      ctx.json('/api/models/capabilities')
        .then(body => { if (live) setModels({ status: 'ready', list: (body?.data || []).filter(m => !m.disabled && m.capabilities?.includes(CAP_TTS)) }) })
        .catch(() => { if (live) setModels({ status: 'error', list: [] }) })
      return () => { live = false }
    }, [])

    const selected = useMemo(() => models.list.find(m => m.id === model) || null, [models.list, model])
    const cloning = Boolean(selected?.voice_cloning)

    useEffect(() => {
      if (!cloning) { setProfiles([]); setVoiceId(''); return undefined }
      let live = true
      ctx.json('/api/voice-profiles')
        .then(body => { if (live) setProfiles(body?.data || []) })
        .catch(() => { if (live) setProfiles([]) })
      return () => { live = false }
    }, [cloning])

    const profile = profiles.find(p => p.id === voiceId) || null
    const run = async () => {
      const request = buildSpeechRequest({ model, text, voice: cloning ? profile?.voice : manualVoice, instructions })
      if (!request) { ctx.toast(t(model ? 'tts.toasts.noText' : 'tts.toasts.noModel'), 'warning'); return }
      setBusy(true); setError(''); setBlob(null)
      try {
        setBlob(await speak(ctx.core, request))
        ctx.toast(t('tts.toasts.generated'), 'success')
      } catch (e) {
        setError(e.message)
      } finally {
        setBusy(false)
      }
    }

    return (
      <main className="page vc-speech" data-testid="voice-speech">
        <ui.PageHeader title={t('speech.title')} supporting={t('speech.lede')} />
        <label className="vc-field">
          <span>{t('speech.model')}</span>
          <ui.ModelSelector value={model} onChange={setModel} options={models.list.map(m => m.id)} loading={models.status === 'loading'} />
        </label>
        {cloning ? (
          <label className="vc-field">
            <span>{t('speech.voice')}</span>
            <select className="dk-input" value={voiceId} onChange={e => setVoiceId(e.target.value)} aria-label={t('speech.voice')}>
              <option value="">{t('speech.defaultVoice')}</option>
              {profiles.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
          </label>
        ) : (
          <label className="vc-field">
            <span>{t('speech.voice')}</span>
            <input className="dk-input" value={manualVoice} onChange={e => setManualVoice(e.target.value)} />
          </label>
        )}
        <label className="vc-field">
          <span>{t('speech.text')}</span>
          <textarea className="dk-input" rows={5} value={text} onChange={e => setText(e.target.value)} />
        </label>
        <label className="vc-field">
          <span>{t('speech.instructions')}</span>
          <input className="dk-input" value={instructions} onChange={e => setInstructions(e.target.value)} />
        </label>
        <ui.Button variant="primary" busy={busy} onClick={run}>{t('speech.generate')}</ui.Button>
        {error && <ui.ErrorWithTraceLink message={error} />}
        {audioUrl && <ui.WaveformPlayer src={audioUrl} label={t('speech.result')} />}
        {!viewer.hasFeature('audio_speech') && <p className="vc-note">{t('speech.noAccess')}</p>}
      </main>
    )
  }

  return { Speech }
}
