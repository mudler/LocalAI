// SPDX-License-Identifier: MIT
import { useEffect, useMemo, useRef, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import Modal from '../components/Modal'
import { useAuth } from '../context/AuthContext'
import useObjectUrl from '../hooks/useObjectUrl'
import { CAP_DIARIZATION } from '../utils/capabilities'
import { diarizationApi, voiceApi } from '../utils/api'
import { rememberEnrollment } from '../utils/voiceEnrollments'
import { clock, hasText, runLength, speakerRows, toRttm, toSrt } from '../utils/diarization'
import { cssVars } from '../utils/modelLedger'
import { useMediaHistory } from '../hooks/useMediaHistory'
import { useStudioHandoff, useHandoffSource, blobToFile } from '../hooks/useStudioHandoff'
import { useWorkspace } from '../hooks/useWorkspace'
// eslint-disable-next-line no-unused-vars
import HandoffNote from '../components/studio/HandoffNote'
// eslint-disable-next-line no-unused-vars
import Timeline from '../components/studio/Timeline'
import {
  // eslint-disable-next-line no-unused-vars
  Workspace, ComposeCard, ModelChip, SourceDrop, RunArea, JobCard, FailedCard, ResultCard, ResultsStrip, EmptyRun,
} from '../components/studio/Workspace'
import Icon from '../components/Icon'

// A text export built from the result in hand, handed to the browser as a file.
// eslint-disable-next-line no-unused-vars
function ExportButton({ label, build, name, type }) {
  const save = () => {
    const url = URL.createObjectURL(new Blob([build()], { type }))
    const a = document.createElement('a')
    a.href = url
    a.download = name
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return <button type="button" className="ws-tool ws-tool--text" onClick={save} data-testid={`export-${label.toLowerCase()}`}>{label}</button>
}

export default function Diarization() {
  const { t } = useTranslation('media')
  const text = (key, values) => t(`diarization.${key}`, values)
  const { model: initialModel } = useParams()
  const { hasFeature } = useAuth()
  const canRemember = hasFeature('voice_recognition')
  // Opened from the Studio front page, the audio it was made from is the input.
  const handoff = useStudioHandoff()
  const [model, setModel] = useState(initialModel || handoff.model || '')
  const [file, setFile] = useState(null)
  const [optIn, setOptIn] = useState(false)
  const [result, setResult] = useState(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState(null)
  const [name, setName] = useState('')
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')
  const generation = useRef(0)
  const saveLock = useRef(false)
  const audio = useRef(null)
  const end = useRef(null)
  const timer = useRef(null)
  const playback = useRef(0)
  const url = useObjectUrl(file)
  const { addEntry, selectEntry, selectedEntry, historyProps } = useMediaHistory('diarization')
  const ws = useWorkspace({ type: 'diarization', entries: historyProps.entries })
  const [lastId, setLastId] = useState(null)
  const wantsSource = handoff.edge === 'diarize'
  const source = useHandoffSource(handoff, wantsSource)
  useEffect(() => {
    if (source.status !== 'ready') return
    const base = (source.item?.url || '').split('?')[0].split('/').pop() || 'audio.wav'
    setFile(blobToFile(source.blob, base))
  }, [source])

  function stop() {
    playback.current++
    clearTimeout(timer.current)
    audio.current?.pause()
    end.current = null
  }
  function invalidate() {
    generation.current++
    stop()
    setResult(null); setSelected(null); setBusy(false); setSaving(false)
    setError(''); setSaveError(''); saveLock.current = false; setLastId(null)
  }
  useEffect(() => {
    const player = audio.current
    return () => { playback.current++; clearTimeout(timer.current); player?.pause() }
  }, [url])
  useEffect(() => () => { generation.current++ }, [])
  useEffect(() => {
    if (!canRemember) { setOptIn(false); setSelected(null) }
  }, [canRemember])

  async function run() {
    if (!file || !model || busy) return
    invalidate()
    selectEntry(null)
    const token = generation.current
    const requested = canRemember && optIn
    setBusy(true)
    try {
      const data = await diarizationApi.run({ file, model, profiles: requested })
      if (token !== generation.current) return
      if (requested && !data.speaker_profiles) throw new Error(text('missingProfiles'))
      // Keep the actual inference model with this export, not a later selection.
      setResult({ ...data, inferenceModel: model, speaker_profiles: requested ? data.speaker_profiles : undefined, fileName: file.name })
      // The front page lists this run. Only the file name, the model and two
      // counts are kept: not the recording, not the transcript, no voice data.
      const labels = new Set((data.segments || []).map(s => String(s.label ?? s.speaker)))
      const speakers = data.speakers?.length || labels.size
      const seconds = Math.round(Math.max(0, ...(data.segments || []).map(s => Number(s.end) || 0)))
      setLastId(addEntry({ prompt: file.name, model, params: { speakers, seconds }, results: [], parentId: handoff.from || undefined, edge: handoff.edge || undefined }))
    } catch (err) {
      if (token === generation.current) setError(`${err.message}${requested ? ` ${text('unsupported')}` : ''}`)
    } finally { if (token === generation.current) setBusy(false) }
  }

  async function preview(interval) {
    stop()
    const player = audio.current
    if (!player) return
    const token = generation.current
    const playToken = playback.current
    try {
      player.currentTime = interval.start
      end.current = interval.end
      await player.play()
      if (token !== generation.current || playToken !== playback.current) return
      timer.current = setTimeout(stop, Math.max(0, interval.end - player.currentTime) * 1000)
    } catch { if (token === generation.current) setError(text('previewError')) }
  }

  async function save(event) {
    event.preventDefault()
    if (!canRemember || !name.trim() || selected === null || saveLock.current || !result) return
    const token = generation.current
    const slot = selected
    saveLock.current = true; setSaving(true); setSaveError('')
    try {
      const registered = await voiceApi.register({
        model: result.inferenceModel, name: name.trim(), speaker_slot: slot,
        speaker_profiles: result.speaker_profiles,
      })
      // A completed registration is real even if its recording is no longer open.
      rememberEnrollment(registered)
      if (token !== generation.current) return
      const relabel = rows => rows?.map(row => String(row.label) === String(slot) ? { ...row, name: registered.name } : row)
      setResult(current => ({ ...current, speakers: relabel(current.speakers), segments: relabel(current.segments) }))
      setSelected(null)
    } catch (err) { if (token === generation.current) setSaveError(err.message) }
    finally { if (token === generation.current) { saveLock.current = false; setSaving(false) } }
  }

  // Raw labels are the only stable join key. Normalized IDs and array order can differ.
  const rows = useMemo(() => speakerRows(result), [result])
  const length = runLength(result)
  const item = ws.itemById(selectedEntry?.id || lastId)

  const installed = ws.installed.byType.diarization
  const noModel = !ws.installed.loading && !ws.installed.error && installed.length === 0
  const why = noModel ? t('studio.composer.whyModel', { type: t('studio.tabs.diarization') })
    : !file ? t('studio.workspace.diarization.whyFile') : ''
  const showResult = !!result && !selectedEntry
  const fileName = result?.fileName || file?.name || 'recording'

  return (
    <Workspace type="diarization">
      <ComposeCard
        ws={ws}
        icon="users"
        title={text('title')}
        lede={text('subtitle')}
        onSubmit={(e) => { e.preventDefault(); run() }}
        handoff={<HandoffNote source={source} handoff={handoff} wanted={wantsSource} onClear={() => { invalidate(); setFile(null) }} />}
        model={model}
        noModel={noModel ? { type: 'diarization', label: t('studio.tabs.diarization'), onChanged: ws.installed.refetch } : null}
        options={<span role="group" aria-label={text('model')} className="ws-chip-group">
          <ModelChip value={model} capability={CAP_DIARIZATION} onChange={value => { if (value !== model) { invalidate(); setModel(value) } }} />
        </span>}
        submit={{ label: text('run'), busyLabel: text('running'), busy, disabled: !file || !model || noModel, why, icon: 'users' }}
      >
        <SourceDrop inputId="diarization-file" label={text('recording')} accept="audio/*,video/*" file={file} hint={t('studio.workspace.diarization.hint')} onFile={f => { invalidate(); setFile(f) }} />
        {canRemember && <div className="ws-optin">
          <label className="ws-check"><input type="checkbox" checked={optIn} onChange={e => { invalidate(); setOptIn(e.target.checked) }} /> <span>{text('optIn')}</span></label>
          <p className="ws-hint">{text('warning')} <Link className="ws-link" to="/app/voice">{text('manage')}</Link></p>
        </div>}
      </ComposeCard>

      <RunArea>
        {busy ? (
          <JobCard label={text('running')} detail={[model, file?.name].filter(Boolean).join(' · ')} />
        ) : error ? (
          <FailedCard message={error} onRetry={run} />
        ) : showResult ? (
          <ResultCard
            ws={ws}
            item={item}
            title={fileName}
            meta={[result.inferenceModel, t('studio.lineage.speakers', { count: rows.length }), clock(length), t('studio.workspace.diarization.segmentCount', { count: result.segments?.length || 0 })]}
            actions={<>
              <ExportButton label="RTTM" name={`${fileName.replace(/\.[^.]*$/, '')}.rttm`} type="text/plain" build={() => toRttm(result, fileName)} />
              {hasText(result) && <ExportButton label="SRT" name={`${fileName.replace(/\.[^.]*$/, '')}.srt`} type="text/plain" build={() => toSrt(result)} />}
              <ExportButton label="JSON" name={`${fileName.replace(/\.[^.]*$/, '')}.json`} type="application/json" build={() => JSON.stringify(result, null, 2)} />
            </>}
            onRerun={() => {}}
          >
            <div className="ws-diar">
              {length > 0 && <Timeline rows={rows} length={length} />}
              {url && <audio ref={audio} src={url} controls preload="metadata" className="ws-diar__audio" onTimeUpdate={() => { if (end.current !== null && audio.current.currentTime >= end.current) stop() }} />}
              <div className="hstack ws-diar__head"><h3>{text('speakers')}</h3>
                {canRemember && result.speaker_profiles && <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={stop}>{text('stop')}</button>}
              </div>
              <ul className="lanes ws-speakers">
                {rows.map(row => {
                  const profile = result.speaker_profiles?.speakers.find(p => String(p.speaker) === String(row.label))
                  const usable = profile?.embedding?.length > 0 && !profile.unavailable_reason
                  return <li className="ws-speaker" key={row.label} data-testid={`speaker-${row.label}`} data-speaker={row.index % 6}>
                    <h4><i aria-hidden="true" />{row.name || row.id}</h4>
                    <p className="ws-speaker__talk">{t('studio.workspace.diarization.talk', { seconds: Math.round(row.seconds), percent: Math.round(row.share * 100) })}</p>
                    <span className="ws-speaker__bar" aria-hidden="true"><b style={cssVars({ '--w': `${Math.round(row.share * 100)}%` })} /></span>
                    {profile && canRemember && <>
                      <p>{text('duration', { seconds: profile.clean_duration })}</p>
                      <div className="hstack">{profile.intervals.map((interval, i) => <button key={i} type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => preview(interval)}>{text('preview', { number: i + 1 })}</button>)}</div>
                      {!usable && <p>{text('insufficient')} {profile.unavailable_reason && <small>({profile.unavailable_reason})</small>}</p>}
                      {!row.name && <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={!usable} onClick={() => { stop(); setSelected(profile.speaker); setName(''); setSaveError('') }}>{text('nameAndRemember')}</button>}
                    </>}
                  </li>
                })}
              </ul>
              <h3>{text('segments')}</h3>
              <ol data-testid="segments" className="lanes ws-segments">
                {result.segments?.map((segment, i) => {
                  const row = rows.find(r => r.label === String(segment.label ?? segment.speaker))
                  return <li key={segment.id ?? i} data-speaker={(row?.index ?? 0) % 6}>
                    <span className="ws-segments__time">{clock(segment.start)}</span>
                    <strong>{segment.name || segment.speaker}</strong>
                    <span className="ws-segments__range">{segment.start}–{segment.end}s</span>
                    {segment.text && <p>{segment.text}</p>}
                  </li>
                })}
              </ol>
            </div>
          </ResultCard>
        ) : selectedEntry ? (
          <ResultCard
            ws={ws}
            item={item}
            title={selectedEntry.prompt}
            meta={[selectedEntry.model, selectedEntry.params?.speakers ? t('studio.lineage.speakers', { count: selectedEntry.params.speakers }) : '', selectedEntry.params?.seconds ? clock(selectedEntry.params.seconds) : '']}
            onRerun={() => {}}
          >
            <p className="ws-quote">{t('studio.workspace.diarization.notKept')}</p>
          </ResultCard>
        ) : (
          <EmptyRun icon="users" text={t('studio.workspace.diarization.empty')} />
        )}
      </RunArea>

      <ResultsStrip
        ws={ws}
        selectedId={historyProps.selectedId}
        activeId={item?.id}
        onSelect={(id) => { if (result) invalidate(); historyProps.onSelect(id) }}
        onDelete={historyProps.onDelete}
        onClear={historyProps.onClearAll}
      />
      {selected !== null && canRemember && <Modal ariaLabel={text('nameAndRemember')} onClose={() => { if (!saving) setSelected(null) }}>
        <form className="stack" onSubmit={save} aria-label={text('nameAndRemember')}>
          <h2>{text('nameAndRemember')}</h2>
          <p>{text('warning')}</p>
          <label className="form-label" htmlFor="diarization-name">{text('name')}</label>
          <input className="input" id="diarization-name" required value={name} disabled={saving} onChange={e => setName(e.target.value)} />
          {saveError && <p role="alert">{saveError}</p>}
          <button type="submit" className="btn btn-primary" disabled={saving || !name.trim()}>{text(saving ? 'saving' : 'remember')}</button>
          <button type="button" className="btn btn-secondary" disabled={saving} onClick={() => setSelected(null)}>{text('cancel')}</button>
        </form>
      </Modal>}
    </Workspace>
  )
}
