import { useState, useEffect, useRef } from 'react'
// eslint-disable-next-line no-unused-vars
import RequestPanel from '../components/RequestPanel'
import { useParams, useOutletContext } from 'react-router-dom'
import { CAP_AUDIO_TRANSFORM } from '../utils/capabilities'
// eslint-disable-next-line no-unused-vars
import WaveformPlayer from '../components/audio/WaveformPlayer'
// eslint-disable-next-line no-unused-vars
import Spectrogram from '../components/audio/Spectrogram'
import { audioTransformApi } from '../utils/api'
import { useMediaCapture } from '../hooks/useMediaCapture'
import useObjectUrl from '../hooks/useObjectUrl'
import { useMediaHistory } from '../hooks/useMediaHistory'
import { useStudioHandoff, useHandoffSource, blobToFile } from '../hooks/useStudioHandoff'
import { apiUrl } from '../utils/basePath'
// eslint-disable-next-line no-unused-vars
import HandoffNote from '../components/studio/HandoffNote'
import {
  // eslint-disable-next-line no-unused-vars
  Workspace, ComposeCard, ModelChip, Field, Fold, RunArea, JobCard, FailedCard, ResultCard, ViewCard, ResultsStrip, EmptyRun,
} from '../components/studio/Workspace'
import { foldSummary, useWorkspace } from '../hooks/useWorkspace'
import { useTranslation } from 'react-i18next'
import Icon from '../components/Icon'

// AudioTransform — Studio tab for the audio_transform capability. Takes a
// primary audio file plus an optional reference (loopback for AEC, target
// speaker for voice conversion, etc.) and shows three synchronized
// waveforms: input audio / reference / output. Supports both file upload
// and direct mic recording, plus an "echo test" mode that records mic
// while playing the reference — the recorded mic picks up the speaker
// bleed of the reference, giving the user a real (mic, ref) pair to test
// echo cancellation against.
export default function AudioTransform() {
  const { t } = useTranslation('media')
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()

  // Opened from the Studio front page, the audio it was made from is the input.
  const handoff = useStudioHandoff()
  const [model, setModel] = useState(urlModel || handoff.model || '')
  const [audioFile, setAudioFile] = useState(null)
  const [referenceFile, setReferenceFile] = useState(null)
  const [outputUrl, setOutputUrl] = useState(null)
  const [paramsText, setParamsText] = useState('')
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  // What was actually sent, so the panel records rather than predicts.
  const [lastRequest, setLastRequest] = useState(null)

  const { addEntry, selectEntry, selectedEntry, historyProps } = useMediaHistory('audio-transform')
  const ws = useWorkspace({ type: 'transform', entries: historyProps.entries })
  const [lastId, setLastId] = useState(null)
  const wantsSource = handoff.edge === 'transform' || handoff.edge === 'take'
  const source = useHandoffSource(handoff, wantsSource)
  useEffect(() => {
    if (source.status !== 'ready') return
    const base = (source.item?.url || '').split('?')[0].split('/').pop() || 'audio.wav'
    setAudioFile(blobToFile(source.blob, base))
  }, [source])

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

  const activeEntry = selectedEntry || (lastId ? historyProps.entries.find(e => e.id === lastId) : null) || null
  const item = ws.itemById(activeEntry?.id)

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

  const run = async () => {
    if (!model) { addToast(t('studio.workspace.transform.noModel'), 'warning'); return }
    if (!audioFile) { addToast(t('studio.workspace.transform.noAudio'), 'warning'); return }

    setLoading(true)
    setError(null)
    setLastId(null)
    if (outputUrl) { URL.revokeObjectURL(outputUrl); setOutputUrl(null) }

    // The audio itself is multipart, not JSON, so the panel records the fields
    // that shape the request rather than the bytes.
    setLastRequest({ model, format: 'wav', params: parseParams() })

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
      addToast(t('studio.workspace.transform.done'), 'success')
      if (serverUrl) {
        // Save the persisted (input, reference, output) triple so a click
        // in the History panel can later replay all three players. The
        // server held onto the converted 16 kHz mono inputs — saving raw
        // upload bytes in localStorage would blow past quota in a few runs.
        setLastId(addEntry({
          prompt: describeRun(audioFile, referenceFile),
          model,
          params: parseParams(),
          results: [
            { kind: 'output', url: serverUrl },
            inputUrl ? { kind: 'input', url: inputUrl } : null,
            refServerUrl ? { kind: 'reference', url: refServerUrl } : null,
          ].filter(Boolean),
          parentId: handoff.from || undefined,
          edge: handoff.edge || undefined,
        }))
      }
      selectEntry(null)
      ws.end()
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  // Re-run with edits: the model and the parameters go back into the form, and
  // so do the audio and reference the server kept for that run.
  const rerun = async (it) => {
    const entry = historyProps.entries.find(e => e.id === it.id)
    if (!entry) return
    const next = {
      model: entry.model || model,
      params: Object.entries(entry.params || {}).map(([k, v]) => `${k}=${v}`).join('\n'),
    }
    setModel(next.model)
    setParamsText(next.params)
    if (next.params) setShowAdvanced(true)
    selectEntry(null)
    ws.begin(next)
    const fetchFile = async (url, fallback) => {
      const res = await fetch(url.startsWith('http') || url.startsWith('blob:') ? url : apiUrl(url))
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      return blobToFile(await res.blob(), url.split('?')[0].split('/').pop() || fallback)
    }
    const input = entry.results?.find(r => r.kind === 'input')
    const reference = entry.results?.find(r => r.kind === 'reference')
    try {
      if (input) setAudioFile(await fetchFile(input.url, 'audio.wav'))
      setReferenceFile(reference ? await fetchFile(reference.url, 'reference.wav') : null)
    } catch {
      addToast(t('studio.workspace.transform.inputGone'), 'warning')
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
      addToast(t('studio.workspace.transform.loadReference'), 'warning')
      return
    }
    if (!echoCap.supported) {
      addToast(t('studio.workspace.transform.noMic'), 'warning')
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
      addToast(t('studio.workspace.transform.recorded'), 'success')
    } catch (err) {
      detachEchoEndedListener()
      addToast(t('studio.workspace.transform.echoFailed', { message: err?.message || String(err) }), 'error')
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

  const labels = { model: t('studio.workspace.fields.model'), params: t('studio.workspace.fields.params') }
  const changes = ws.changes({ model, params: paramsText }, labels)
  const changed = new Set(changes.map(c => c.field))

  const installed = ws.installed.byType.transform
  const noModel = !ws.installed.loading && !ws.installed.error && installed.length === 0
  const why = noModel ? t('studio.composer.whyModel', { type: t('studio.tabs.transform') })
    : !audioFile ? t('studio.workspace.transform.whyAudio') : ''
  const paramCount = Object.keys(parseParams()).length

  return (
    <Workspace type="transform">
      <ComposeCard
        ws={ws}
        icon="waveform"
        title={t('audioTransform.title')}
        lede={t('studio.workspace.lede.transform')}
        onSubmit={(e) => { e.preventDefault(); run() }}
        handoff={<HandoffNote source={source} handoff={handoff} wanted={wantsSource} onClear={() => setAudioFile(null)} />}
        model={model}
        noModel={noModel ? { type: 'transform', label: t('studio.tabs.transform'), onChanged: ws.installed.refetch } : null}
        options={<ModelChip value={model} onChange={setModel} capability={CAP_AUDIO_TRANSFORM} changed={changed.has('model')} />}
        fold={
          <Fold
            label={t('audioTransform.labels.advancedParameters')}
            summary={foldSummary(paramCount ? [t('studio.workspace.transform.paramCount', { count: paramCount })] : [], [t('audioTransform.labels.advancedParametersHelp')])}
            open={showAdvanced}
            onToggle={() => setShowAdvanced(v => !v)}
            id="transform-advanced-options"
          >
            <Field label={t('audioTransform.labels.advancedParameters')} htmlFor="transform-params" changed={changed.has('params')} hint={t('audioTransform.labels.advancedParametersHelp')}>
              <textarea
                id="transform-params"
                className="textarea"
                value={paramsText}
                onChange={(e) => setParamsText(e.target.value)}
                placeholder={t('audioTransform.labels.advancedParametersPlaceholder')}
                rows={4}
              />
            </Field>
          </Fold>
        }
        changes={changes}
        submit={{ label: t('audioTransform.actions.transform'), busyLabel: t('audioTransform.actions.processing'), busy: loading, disabled: !model || !audioFile || noModel, why }}
      >
        <div className="ws-inputs">
          <AudioInput
            label={t('audioTransform.labels.audio')}
            file={audioFile}
            onChange={setAudioFile}
          />
          <AudioInput
            label={t('audioTransform.labels.reference')}
            help={t('audioTransform.labels.referenceHelp')}
            file={referenceFile}
            onChange={setReferenceFile}
          />
        </div>

        {referenceFile && (
          <div className="audio-transform-echo">
            <p className="audio-transform-echo__notice" role="note">
              <Icon name="info" />
              <span>
                {t('audioTransform.input.echoNotice')}
              </span>
            </p>
            <div className="audio-transform-echo__row">
              <button
                type="button"
                className={`dk-btn ${echoActive ? 'dk-btn--secondary' : 'dk-btn--primary'} dk-btn--sm`}
                onClick={echoActive ? stopEchoTest : startEchoTest}
              >
                {echoActive
                  ? <><Icon name="stop" /> {t('audioTransform.input.stopEchoTest')}</>
                  : <><Icon name="headphones" /> {t('audioTransform.input.echoTest')}</>}
              </button>
              {echoActive && echoCap.recording && (
                <span className="audio-transform-echo__elapsed">
                  {t('studio.workspace.transform.recording', { seconds: echoCap.elapsed.toFixed(1) })}
                </span>
              )}
            </div>
            {/* Hidden player for the reference clip during the echo test.
                Hidden because the user already has the WaveformPlayer in
                the preview pane — this is just the audible source. */}
            <audio ref={echoAudioRef} src={referenceUrl} preload="auto" hidden />
          </div>
        )}
      </ComposeCard>

      <RunArea>
        {loading ? (
          <JobCard label={t('audioTransform.actions.processing')} detail={[model, audioFile?.name].filter(Boolean).join(' · ')} />
        ) : error ? (
          <FailedCard message={error} onRetry={run} />
        ) : selectedEntry ? (
          <ResultCard
            ws={ws}
            item={item}
            title={selectedEntry.prompt}
            meta={[selectedEntry.model]}
            download={{ href: selectedEntry.results.find(r => r.kind === 'output')?.url, name: `audio-transform-${selectedEntry.model || 'output'}.wav` }}
            onRerun={rerun}
          >
            <div className="ws-audio audio-transform-stack">
              {selectedEntry.results.map((r) => (
                <WaveformPlayer
                  key={r.kind || r.url}
                  src={r.url}
                  label={resultLabel(r)}
                  height={r.kind === 'output' ? 120 : 96}
                  dimmed={r.kind === 'reference'}
                />
              ))}
            </div>
          </ResultCard>
        ) : outputUrl ? (
          <ResultCard
            ws={ws}
            item={item}
            title={describeRun(audioFile, referenceFile)}
            meta={[item?.model || model]}
            download={{ href: outputUrl, name: `audio-transform-${model || 'output'}-${new Date().toISOString().slice(0, 10)}.wav` }}
            onRerun={rerun}
          >
            <div className="ws-audio audio-transform-stack">
              <div className="audio-spectrogram-pair">
                <Spectrogram src={audioUrl} label={t('audioTransform.result.inputSpectrum')} testId="spectrogram-input" />
                <Spectrogram src={outputUrl} label={t('audioTransform.result.outputSpectrum')} testId="spectrogram-output" />
              </div>
              <WaveformPlayer src={audioUrl} label={t('audioTransform.result.audio')} height={96} />
              <WaveformPlayer src={referenceUrl} label={t('audioTransform.result.reference')} height={96} dimmed={!referenceFile} />
              <WaveformPlayer src={outputUrl} label={t('audioTransform.result.output')} height={120} />
            </div>
          </ResultCard>
        ) : audioUrl ? (
          <ViewCard title={t('audioTransform.result.audio')} sub={t('studio.workspace.transform.waiting')}>
            <div className="ws-audio audio-transform-stack">
              <div className="audio-spectrogram-pair">
                <Spectrogram src={audioUrl} label={t('audioTransform.result.inputSpectrum')} testId="spectrogram-input" />
                <div className="audio-spectrogram">
                  <div className="audio-spectrogram__label">{t('audioTransform.result.outputSpectrum')}</div>
                  <div className="audio-spectrogram__canvas-wrap audio-spectrogram__canvas-wrap--empty">
                    <span className="audio-spectrogram__hint">{t('audioTransform.result.outputSpectrumHint')}</span>
                  </div>
                </div>
              </div>
              <WaveformPlayer src={audioUrl} label={t('audioTransform.result.audio')} height={96} />
              <WaveformPlayer src={referenceUrl} label={t('audioTransform.result.reference')} height={96} dimmed={!referenceFile} />
            </div>
          </ViewCard>
        ) : (
          <EmptyRun icon="waveform" text={t('audioTransform.empty')} />
        )}
        <RequestPanel endpoint="/v1/audio/transform" body={lastRequest} />
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
// eslint-disable-next-line no-unused-vars
function AudioInput({ label, help, file, onChange }) {
  const { t } = useTranslation('media')
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
            <Icon name="upload" /> {t('audioTransform.input.upload')}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === 'record'}
            className={`audio-transform-input__tab${tab === 'record' ? ' active' : ''}`}
            onClick={() => setTab('record')}
          >
            <Icon name="mic" /> {t('audioTransform.input.record')}
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
                <Icon name="music" /> {file.name}
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => onChange(null)}>{t('audioTransform.input.clear')}</button>
              </div>
            ) : (
              <>
                <Icon name="upload" /> {t('audioTransform.input.uploadDescription')}
                <label className="audio-transform-drop__pick">
                  <input type="file" accept="audio/*" onChange={onPick} hidden />
                  {t('audioTransform.input.uploadBrowse')}
                </label>
              </>
            )}
          </div>
        )}

        {tab === 'record' && (
          <div className="audio-transform-rec">
            {!cap.supported && (
              <div className="audio-transform-rec__notice">
                <Icon name="info" /> {t('audioTransform.input.microphoneUnavailable')}
              </div>
            )}
            {cap.supported && (
              <>
                {!cap.recording && !recordPending && (
                  <button type="button" className="btn btn-primary btn-sm" onClick={startRecord}>
                    <Icon name="circle" style={{ color: 'var(--color-error)' }} /> {t('audioTransform.input.startRecording')}
                  </button>
                )}
                {cap.recording && (
                  <button type="button" className="btn btn-secondary btn-sm" onClick={stopRecord}>
                    <Icon name="stop" /> {t('audioTransform.input.stop')} ({cap.elapsed.toFixed(1)}s)
                  </button>
                )}
                {recordPending && !cap.recording && (
                  <div className="audio-transform-rec__pending">{t('audioTransform.input.encoding')}</div>
                )}
                {cap.error && (
                  <div className="audio-transform-rec__notice audio-transform-rec__notice--error">
                    {cap.error}
                  </div>
                )}
                {hasFile && !cap.recording && (
                  <div className="audio-transform-drop__file mt-sm">
                    <Icon name="music" /> {file.name}
                    <button type="button" className="btn btn-secondary btn-sm" onClick={() => onChange(null)}>{t('audioTransform.input.clear')}</button>
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
