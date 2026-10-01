// SPDX-License-Identifier: MIT
import { useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import PageHeader from '../components/PageHeader'
import ModelSelector from '../components/ModelSelector'
import Modal from '../components/Modal'
import { useAuth } from '../context/AuthContext'
import useObjectUrl from '../hooks/useObjectUrl'
import { CAP_DIARIZATION } from '../utils/capabilities'
import { diarizationApi, voiceApi } from '../utils/api'
import { rememberEnrollment } from '../utils/voiceEnrollments'

export default function Diarization() {
  const { t } = useTranslation('media')
  const text = (key, values) => t(`diarization.${key}`, values)
  const { model: initialModel } = useParams()
  const { hasFeature } = useAuth()
  const canRemember = hasFeature('voice_recognition')
  const [model, setModel] = useState(initialModel || '')
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
    setError(''); setSaveError(''); saveLock.current = false
  }
  useEffect(() => {
    const player = audio.current
    return () => { playback.current++; clearTimeout(timer.current); player?.pause() }
  }, [url])
  useEffect(() => () => { generation.current++ }, [])
  useEffect(() => {
    if (!canRemember) { setOptIn(false); setSelected(null) }
  }, [canRemember])

  async function submit(event) {
    event.preventDefault()
    if (!file || !model || busy) return
    invalidate()
    const token = generation.current
    const requested = canRemember && optIn
    setBusy(true)
    try {
      const data = await diarizationApi.run({ file, model, profiles: requested })
      if (token !== generation.current) return
      if (requested && !data.speaker_profiles) throw new Error(text('missingProfiles'))
      // Keep the actual inference model with this export, not a later selection.
      setResult({ ...data, inferenceModel: model, speaker_profiles: requested ? data.speaker_profiles : undefined })
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
  const summaries = result?.speakers || Array.from(new Map((result?.segments || []).map(s => [String(s.label), { ...s, id: s.speaker }])).values())
  return (
    <div className="page-pad">
      <PageHeader title={text('title')} supporting={text('subtitle')} />
      <form onSubmit={submit} className="card stack">
        <div className="form-group" role="group" aria-label={text('model')}>
          <span className="form-label">{text('model')}</span>
          <ModelSelector value={model} capability={CAP_DIARIZATION} onChange={value => { if (value !== model) { invalidate(); setModel(value) } }} />
        </div>
        <div className="form-group">
          <label className="form-label" htmlFor="diarization-file">{text('recording')}</label>
          <input id="diarization-file" className="input" type="file" accept="audio/*,video/*" onChange={e => { invalidate(); setFile(e.target.files?.[0] || null) }} />
        </div>
        {canRemember && <div className="form-group">
          <label><input type="checkbox" checked={optIn} onChange={e => { invalidate(); setOptIn(e.target.checked) }} /> {text('optIn')}</label>
          <p className="form-help">{text('warning')} <Link to="/app/voice">{text('manage')}</Link></p>
        </div>}
        <button className="btn btn-primary" disabled={busy || !file || !model}>{text(busy ? 'running' : 'run')}</button>
      </form>
      {error && <p role="alert">{error}</p>}
      {url && <audio ref={audio} src={url} preload="metadata" onTimeUpdate={() => { if (end.current !== null && audio.current.currentTime >= end.current) stop() }} />}
      {result && <>
        <div className="hstack"><h2>{text('speakers')}</h2>
          {canRemember && result.speaker_profiles && <button type="button" className="btn btn-secondary" onClick={stop}>{text('stop')}</button>}
        </div>
        <ul className="lanes">
          {summaries.map(summary => {
            const profile = result.speaker_profiles?.speakers.find(p => String(p.speaker) === String(summary.label))
            const knownName = summary.name || result.segments?.find(s => String(s.label) === String(summary.label) && s.name)?.name
            const usable = profile?.embedding?.length > 0 && !profile.unavailable_reason
            return <li className="card stack" key={summary.label} data-testid={`speaker-${summary.label}`}>
              <h3>{knownName || summary.id}</h3>
              {profile && canRemember && <>
                <p>{text('duration', { seconds: profile.clean_duration })}</p>
                <div className="hstack">{profile.intervals.map((interval, i) => <button key={i} type="button" className="btn btn-secondary" onClick={() => preview(interval)}>{text('preview', { number: i + 1 })}</button>)}</div>
                {!usable && <p>{text('insufficient')} {profile.unavailable_reason && <small>({profile.unavailable_reason})</small>}</p>}
                {!knownName && <button type="button" className="btn btn-primary" disabled={!usable} onClick={() => { stop(); setSelected(profile.speaker); setName(''); setSaveError('') }}>{text('nameAndRemember')}</button>}
              </>}
            </li>
          })}
        </ul>
        <h2>{text('segments')}</h2>
        <ol data-testid="segments" className="lanes">
          {result.segments?.map((segment, i) => <li key={segment.id ?? i} className="card"><strong>{segment.name || segment.speaker}</strong> <span>{segment.start}–{segment.end}s</span><p>{segment.text}</p></li>)}
        </ol>
      </>}
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
    </div>
  )
}
