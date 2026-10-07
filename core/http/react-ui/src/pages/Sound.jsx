import { useState } from 'react'
// eslint-disable-next-line no-unused-vars
import RequestPanel from '../components/RequestPanel'
import { useParams, useOutletContext } from 'react-router-dom'
import { CAP_SOUND_GENERATION } from '../utils/capabilities'
// eslint-disable-next-line no-unused-vars
import WaveformPlayer from '../components/audio/WaveformPlayer'
import { soundApi } from '../utils/api'
import { useMediaHistory } from '../hooks/useMediaHistory'
import { useStudioHandoff } from '../hooks/useStudioHandoff'
import { useTranslation } from 'react-i18next'
import {
  // eslint-disable-next-line no-unused-vars
  Workspace, ComposeCard, ChipInput, ToggleChip, ModelChip, Field, Fold, Starters, RunArea, JobCard, FailedCard, ResultCard, ResultsStrip, EmptyRun,
} from '../components/studio/Workspace'
import { foldSummary, useWorkspace } from '../hooks/useWorkspace'

export default function Sound() {
  const { t } = useTranslation('media')
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  // Opened from the Studio front page, the form starts from what it sent.
  const handoff = useStudioHandoff()
  const [model, setModel] = useState(urlModel || handoff.model || '')
  const [mode, setMode] = useState('simple')
  const [text, setText] = useState(handoff.prompt)
  const [instrumental, setInstrumental] = useState(false)
  const [vocalLanguage, setVocalLanguage] = useState('')
  const [caption, setCaption] = useState('')
  const [lyrics, setLyrics] = useState('')
  const [think, setThink] = useState(false)
  const [bpm, setBpm] = useState('')
  const [duration, setDuration] = useState('')
  const [keyscale, setKeyscale] = useState('')
  const [language, setLanguage] = useState('')
  const [timesignature, setTimesignature] = useState('')
  const [showMore, setShowMore] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  // What was actually sent, so the panel records rather than predicts.
  const [lastRequest, setLastRequest] = useState(null)
  const [audioUrl, setAudioUrl] = useState(null)
  const { addEntry, selectEntry, selectedEntry, historyProps } = useMediaHistory('sound')
  const ws = useWorkspace({ type: 'sound', entries: historyProps.entries })
  const [lastId, setLastId] = useState(null)

  const activeEntry = selectedEntry || (lastId ? historyProps.entries.find(e => e.id === lastId) : null) || null
  const item = ws.itemById(activeEntry?.id)

  const run = async () => {
    if (!model) { addToast(t('sound.toasts.noModel'), 'warning'); return }

    const body = { model_id: model }

    if (mode === 'simple') {
      if (!text.trim()) { addToast(t('sound.toasts.noPrompt'), 'warning'); return }
      body.text = text.trim()
      body.instrumental = instrumental
      if (vocalLanguage.trim()) body.vocal_language = vocalLanguage.trim()
    } else {
      if (!caption.trim() && !lyrics.trim()) { addToast(t('studio.workspace.sound.noCaption'), 'warning'); return }
      if (caption.trim()) body.caption = caption.trim()
      if (lyrics.trim()) body.lyrics = lyrics.trim()
      body.think = think
      if (bpm) body.bpm = parseInt(bpm)
      if (duration) body.duration_seconds = parseFloat(duration)
      if (keyscale.trim()) body.keyscale = keyscale.trim()
      if (language.trim()) body.language = language.trim()
      if (timesignature.trim()) body.timesignature = timesignature.trim()
    }

    setLoading(true)
    setAudioUrl(null)
    setError(null)
    setLastId(null)

    setLastRequest(body)

    try {
      const { blob, serverUrl } = await soundApi.generate(body)
      const url = URL.createObjectURL(blob)
      setAudioUrl(url)
      addToast(t('studio.workspace.sound.generated'), 'success')
      const promptText = mode === 'simple' ? text.trim() : (caption.trim() || lyrics.trim())
      if (serverUrl) {
        // Every field of the form is kept, so Re-run with edits can put it back.
        const params = mode === 'simple'
          ? { mode, instrumental, vocalLanguage: vocalLanguage.trim() || undefined }
          : { mode, caption: caption.trim() || undefined, lyrics: lyrics.trim() || undefined, think, bpm, duration, keyscale, language, timesignature }
        setLastId(addEntry({ prompt: promptText, model, params, results: [{ url: serverUrl }], parentId: handoff.from || undefined, edge: handoff.edge || undefined }))
      }
      selectEntry(null)
      ws.end()
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  const rerun = (it) => {
    const entry = historyProps.entries.find(e => e.id === it.id)
    if (!entry) return
    const p = entry.params || {}
    const advanced = p.mode === 'advanced'
    const next = {
      model: entry.model || model, mode: advanced ? 'advanced' : 'simple',
      prompt: advanced ? (p.caption || '') : (entry.prompt || ''), lyrics: p.lyrics || '',
      instrumental: p.instrumental ? 'on' : '', vocal: p.vocalLanguage || '', think: p.think ? 'on' : '',
      bpm: p.bpm ? String(p.bpm) : '', duration: p.duration ? String(p.duration) : '', key: p.keyscale || '',
      language: p.language || '', timesignature: p.timesignature || '',
    }
    setModel(next.model); setMode(next.mode); setInstrumental(!!next.instrumental); setVocalLanguage(next.vocal); setThink(!!next.think)
    setBpm(next.bpm); setDuration(next.duration); setKeyscale(next.key); setLanguage(next.language); setTimesignature(next.timesignature)
    if (advanced) { setCaption(next.prompt); setLyrics(next.lyrics) } else setText(next.prompt)
    if (next.vocal || next.bpm || next.key || next.language || next.timesignature || next.think) setShowMore(true)
    selectEntry(null)
    ws.begin(next)
  }

  const f = (key) => t(`studio.workspace.fields.${key}`)
  const labels = {
    prompt: f('prompt'), model: f('model'), mode: f('mode'), lyrics: f('lyrics'), instrumental: f('instrumental'), vocal: f('language'),
    think: f('think'), bpm: f('bpm'), duration: f('duration'), key: f('key'), language: f('language'), timesignature: f('timesignature'),
  }
  const current = {
    model, mode, prompt: mode === 'simple' ? text : caption, lyrics: mode === 'simple' ? '' : lyrics,
    instrumental: mode === 'simple' && instrumental ? 'on' : '', vocal: mode === 'simple' ? vocalLanguage : '',
    think: mode === 'advanced' && think ? 'on' : '', bpm: mode === 'advanced' ? bpm : '', duration: mode === 'advanced' ? duration : '',
    key: mode === 'advanced' ? keyscale : '', language: mode === 'advanced' ? language : '', timesignature: mode === 'advanced' ? timesignature : '',
  }
  const changes = ws.changes(current, labels)
  const changed = new Set(changes.map(c => c.field))

  const installed = ws.installed.byType.sound
  const noModel = !ws.installed.loading && !ws.installed.error && installed.length === 0
  const hasWords = mode === 'simple' ? !!text.trim() : !!(caption.trim() || lyrics.trim())
  const why = noModel ? t('studio.composer.whyModel', { type: t('studio.tabs.sound') })
    : !hasWords ? t('studio.composer.whyPrompt') : ''

  const moreSet = mode === 'simple'
    ? [vocalLanguage && `${labels.vocal} ${vocalLanguage}`]
    : [bpm && `${labels.bpm} ${bpm}`, keyscale && `${labels.key} ${keyscale}`, language && `${labels.language} ${language}`, timesignature && labels.timesignature, think && labels.think]
  const moreAll = mode === 'simple' ? [labels.vocal] : [labels.bpm, labels.key, labels.language, labels.timesignature, labels.think]

  return (
    <Workspace type="sound">
      <ComposeCard
        ws={ws}
        icon="music"
        title={t('sound.title')}
        lede={t('studio.workspace.lede.sound')}
        onSubmit={(e) => { e.preventDefault(); run() }}
        model={model}
        noModel={noModel ? { type: 'sound', label: t('studio.tabs.sound'), onChanged: ws.installed.refetch } : null}
        options={<>
          <ModelChip value={model} onChange={setModel} capability={CAP_SOUND_GENERATION} changed={changed.has('model')} />
          <div className="dk-segmented ws-mode" role="tablist" aria-label={t('studio.workspace.fields.mode')} data-changed={changed.has('mode') || undefined}>
            <button type="button" role="tab" className="dk-seg" aria-selected={mode === 'simple'} onClick={() => setMode('simple')}>{t('sound.labels.simple')}</button>
            <button type="button" role="tab" className="dk-seg" aria-selected={mode === 'advanced'} onClick={() => setMode('advanced')}>{t('sound.labels.advanced')}</button>
          </div>
          {mode === 'simple'
            ? <ToggleChip label={t('sound.labels.instrumental')} on={instrumental} onChange={setInstrumental} changed={changed.has('instrumental')} testId="ws-instrumental" />
            : <ChipInput label={t('sound.labels.duration')} type="number" step="0.1" value={duration} onChange={setDuration} placeholder={t('studio.workspace.auto')} changed={changed.has('duration')} testId="ws-duration" />}
        </>}
        fold={
          <Fold
            label={t('studio.workspace.sound.more')}
            summary={foldSummary(moreSet.filter(Boolean), moreAll)}
            open={showMore}
            onToggle={() => setShowMore(v => !v)}
            id="sound-more-options"
          >
            {mode === 'simple' ? (
              <Field label={t('sound.labels.vocalLanguage')} htmlFor="sound-vocal" changed={changed.has('vocal')}>
                <input id="sound-vocal" className="dk-input" value={vocalLanguage} onChange={(e) => setVocalLanguage(e.target.value)} placeholder={t('sound.labels.vocalLanguagePlaceholder')} />
              </Field>
            ) : (
              <>
                <div className="ws-grid">
                  <Field label={t('sound.labels.bpm')} htmlFor="sound-bpm" changed={changed.has('bpm')}><input id="sound-bpm" className="dk-input" type="number" value={bpm} onChange={(e) => setBpm(e.target.value)} /></Field>
                  <Field label={t('sound.labels.keyscale')} htmlFor="sound-key" changed={changed.has('key')}><input id="sound-key" className="dk-input" value={keyscale} onChange={(e) => setKeyscale(e.target.value)} /></Field>
                  <Field label={t('sound.labels.language')} htmlFor="sound-language" changed={changed.has('language')}><input id="sound-language" className="dk-input" value={language} onChange={(e) => setLanguage(e.target.value)} /></Field>
                  <Field label={t('sound.labels.timesignature')} htmlFor="sound-timesig" changed={changed.has('timesignature')}><input id="sound-timesig" className="dk-input" value={timesignature} onChange={(e) => setTimesignature(e.target.value)} /></Field>
                </div>
                <label className="ws-check" data-changed={changed.has('think') || undefined}>
                  <input type="checkbox" checked={think} onChange={(e) => setThink(e.target.checked)} />
                  <span>{t('sound.labels.thinkMode')}</span>
                </label>
              </>
            )}
          </Fold>
        }
        changes={changes}
        submit={{ label: t('sound.actions.generate'), busyLabel: t('sound.actions.generating'), busy: loading, disabled: !model || !hasWords || noModel, why, icon: 'music' }}
      >
        {mode === 'simple' ? (
          <>
            <textarea
              className="ws-prompt textarea"
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder={t('sound.labels.promptPlaceholder')}
              aria-label={t('sound.labels.prompt')}
              rows={3}
              data-changed={changed.has('prompt') || undefined}
            />
            {!text && <Starters type="sound" onPick={setText} />}
          </>
        ) : (
          <>
            <Field label={t('sound.labels.caption')} htmlFor="sound-caption" changed={changed.has('prompt')}>
              <textarea id="sound-caption" className="ws-prompt textarea" value={caption} onChange={(e) => setCaption(e.target.value)} rows={2} />
            </Field>
            <Field label={t('sound.labels.lyrics')} htmlFor="sound-lyrics" changed={changed.has('lyrics')}>
              <textarea id="sound-lyrics" className="ws-prompt textarea" value={lyrics} onChange={(e) => setLyrics(e.target.value)} placeholder={t('sound.labels.lyricsPlaceholder')} rows={3} />
            </Field>
          </>
        )}
      </ComposeCard>

      <RunArea>
        {loading ? (
          <JobCard label={t('sound.actions.generating')} detail={[model, mode === 'advanced' && duration ? `${duration} s` : ''].filter(Boolean).join(' · ')} />
        ) : error ? (
          <FailedCard message={error} onRetry={run} />
        ) : (selectedEntry || audioUrl) ? (
          <ResultCard
            ws={ws}
            item={item}
            title={item?.title || (mode === 'simple' ? text : (caption || lyrics))}
            meta={[item?.model || model]}
            download={{ href: selectedEntry ? selectedEntry.results[0]?.url : audioUrl, name: `sound-${new Date().toISOString().slice(0, 10)}.wav` }}
            onRerun={rerun}
          >
            <div className="ws-audio">
              <WaveformPlayer src={selectedEntry ? selectedEntry.results[0]?.url : audioUrl} height={96} />
              {selectedEntry && <p className="ws-quote">&ldquo;{selectedEntry.prompt}&rdquo;</p>}
            </div>
          </ResultCard>
        ) : (
          <EmptyRun icon="music" text={t('sound.empty')} />
        )}
        <RequestPanel endpoint="/sound-generation" body={lastRequest} />
      </RunArea>

      <ResultsStrip
        ws={ws}
        selectedId={historyProps.selectedId}
        activeId={activeEntry?.id}
        onSelect={historyProps.onSelect}
        onDelete={historyProps.onDelete}
        onClear={historyProps.onClearAll}
      />
    </Workspace>
  )
}
