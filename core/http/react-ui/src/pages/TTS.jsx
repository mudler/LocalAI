import { useEffect, useMemo, useRef, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import RequestPanel from '../components/RequestPanel'
// eslint-disable-next-line no-unused-vars
import { Link, useParams, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { CAP_TTS } from '../utils/capabilities'
// eslint-disable-next-line no-unused-vars
import WaveformPlayer from '../components/audio/WaveformPlayer'
import { ttsApi } from '../utils/api'
import { useMediaHistory } from '../hooks/useMediaHistory'
import { useStudioHandoff } from '../hooks/useStudioHandoff'
import { useModels } from '../hooks/useModels'
import { useVoiceProfiles } from '../hooks/useVoiceProfiles'
import { useAuth } from '../context/AuthContext'
import {
  // eslint-disable-next-line no-unused-vars
  Workspace, ComposeCard, ChipSelect, ChipInput, ModelChip, Field, Fold, Starters, RunArea, JobCard, FailedCard, ResultCard, ResultsStrip, EmptyRun,
} from '../components/studio/Workspace'
import { foldSummary, useWorkspace } from '../hooks/useWorkspace'
import Icon from '../components/Icon'

function formatProfileDuration(milliseconds) {
  const seconds = Math.round((milliseconds || 0) / 1000)
  return seconds >= 60 ? `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}` : `${seconds}s`
}

export default function TTS() {
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const { t } = useTranslation('media')
  const { isAdmin } = useAuth()
  const [searchParams] = useSearchParams()
  const requestedVoiceID = searchParams.get('voice') || ''
  // Opened from the Studio front page, the form starts from what it sent.
  const handoff = useStudioHandoff()
  const [model, setModel] = useState(urlModel || handoff.model || '')
  const [manualVoice, setManualVoice] = useState('')
  const [voiceProfileID, setVoiceProfileID] = useState(requestedVoiceID)
  const [text, setText] = useState(handoff.prompt)
  const [instructions, setInstructions] = useState('')
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  // What was actually sent, so the request panel records rather than predicts.
  const [lastRequest, setLastRequest] = useState(null)
  const [audioUrl, setAudioUrl] = useState(null)
  const appliedVoiceLinkRef = useRef('')
  const { addEntry, selectEntry, selectedEntry, historyProps } = useMediaHistory('tts')
  const ws = useWorkspace({ type: 'tts', entries: historyProps.entries })
  const [lastId, setLastId] = useState(null)
  const { models } = useModels(CAP_TTS)
  const selectedModel = useMemo(() => models.find(item => item.id === model), [models, model])
  const compatibleModels = useMemo(() => models.filter(item => item.voice_cloning), [models])
  const supportsVoiceProfiles = !!selectedModel?.voice_cloning
  const { profiles, loading: profilesLoading, error: profilesError } = useVoiceProfiles({ enabled: supportsVoiceProfiles })
  const selectedProfile = profiles.find(profile => profile.id === voiceProfileID) || null

  // A library deep-link should land on a compatible model even when the last
  // TTS model used was a named-speaker or non-cloning variant. Apply it once
  // per requested profile so the user can still change models afterwards.
  useEffect(() => {
    if (!requestedVoiceID || models.length === 0 || appliedVoiceLinkRef.current === requestedVoiceID) return
    const targetModel = selectedModel?.voice_cloning ? selectedModel : compatibleModels[0]
    if (!targetModel) return
    if (targetModel.id !== model) setModel(targetModel.id)
    setVoiceProfileID(requestedVoiceID)
    appliedVoiceLinkRef.current = requestedVoiceID
  }, [requestedVoiceID, models, model, selectedModel, compatibleModels])

  useEffect(() => {
    if (selectedModel && !selectedModel.voice_cloning) setVoiceProfileID('')
  }, [selectedModel])

  const activeEntry = selectedEntry || (lastId ? historyProps.entries.find(e => e.id === lastId) : null) || null
  const item = ws.itemById(activeEntry?.id)

  const run = async () => {
    if (!text.trim()) { addToast(t('tts.toasts.noText'), 'warning'); return }
    if (!model) { addToast(t('tts.toasts.noModel'), 'warning'); return }

    setLoading(true)
    setAudioUrl(null)
    setError(null)
    setLastId(null)

    try {
      const selectedVoice = supportsVoiceProfiles ? selectedProfile?.voice : manualVoice.trim()
      const request = { model, input: text.trim() }
      if (selectedVoice) request.voice = selectedVoice
      if (instructions.trim()) request.instructions = instructions.trim()
      setLastRequest(request)
      const { blob, serverUrl } = await ttsApi.generate(request)
      const url = URL.createObjectURL(blob)
      setAudioUrl(url)
      addToast(t('tts.toasts.generated'), 'success')
      if (serverUrl) {
        setLastId(addEntry({
          prompt: text.trim(),
          model,
          params: {
            ...(selectedProfile
              ? { voice: selectedProfile.name, voiceId: selectedProfile.id }
              : (!supportsVoiceProfiles && manualVoice.trim() ? { voice: manualVoice.trim() } : {})),
            ...(instructions.trim() ? { instructions: instructions.trim() } : {}),
          },
          results: [{ url: serverUrl }],
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

  const rerun = (it) => {
    const entry = historyProps.entries.find(e => e.id === it.id)
    if (!entry) return
    const p = entry.params || {}
    const cloning = !!models.find(m => m.id === entry.model)?.voice_cloning
    const byName = profiles.find(pr => pr.name === p.voice)
    const next = {
      prompt: entry.prompt || '', model: entry.model || model,
      voice: cloning ? (p.voiceId || byName?.id || '') : (p.voice || ''),
      instructions: p.instructions || '',
    }
    setText(next.prompt); setModel(next.model); setInstructions(next.instructions)
    if (cloning) { setVoiceProfileID(next.voice); setManualVoice('') } else { setManualVoice(next.voice); setVoiceProfileID('') }
    if (next.instructions) setShowAdvanced(true)
    selectEntry(null)
    ws.begin(next)
  }

  const f = (key) => t(`studio.workspace.fields.${key}`)
  const labels = { prompt: f('prompt'), model: f('model'), voice: f('voice'), instructions: f('instructions') }
  const current = { prompt: text, model, voice: supportsVoiceProfiles ? voiceProfileID : manualVoice, instructions }
  const changes = ws.changes(current, labels)
  const changed = new Set(changes.map(c => c.field))

  const installed = ws.installed.byType.tts
  const noModel = !ws.installed.loading && !ws.installed.error && installed.length === 0
  const why = noModel ? t('studio.composer.whyModel', { type: t('studio.tabs.tts') })
    : !text.trim() ? t('studio.composer.whyPrompt') : ''
  const shownSrc = selectedEntry ? selectedEntry.results[0]?.url : audioUrl

  return (
    <Workspace type="tts">
      <ComposeCard
        ws={ws}
        icon="headphones"
        title={t('tts.title')}
        lede={t('studio.workspace.lede.tts')}
        onSubmit={(e) => { e.preventDefault(); run() }}
        model={model}
        noModel={noModel ? { type: 'tts', label: t('studio.tabs.tts'), onChanged: ws.installed.refetch } : null}
        options={<>
          <ModelChip value={model} onChange={setModel} capability={CAP_TTS} changed={changed.has('model')} />
          {supportsVoiceProfiles ? (
            <ChipSelect
              label={t('tts.labels.voice')}
              value={voiceProfileID}
              onChange={setVoiceProfileID}
              disabled={profilesLoading}
              mono={false}
              changed={changed.has('voice')}
              testId="ws-voice"
              id="tts-voice"
              options={[
                { value: '', label: profilesLoading ? t('tts.voiceLibrary.loading') : t('tts.voiceLibrary.modelDefault') },
                ...profiles.map(profile => ({ value: profile.id, label: `${profile.name}${profile.language ? ` · ${profile.language}` : ''}` })),
              ]}
            />
          ) : (
            <ChipInput label={t('tts.labels.voice')} value={manualVoice} onChange={setManualVoice} placeholder={t('tts.labels.voicePlaceholder')} width="wide" changed={changed.has('voice')} testId="ws-voice" id="tts-voice" />
          )}
        </>}
        fold={
          <Fold
            label={t('tts.labels.instructions')}
            summary={foldSummary(instructions.trim() ? [instructions.trim().slice(0, 40)] : [], [t('tts.labels.instructionsHint')])}
            open={showAdvanced}
            onToggle={() => setShowAdvanced(v => !v)}
            id="tts-advanced-options"
          >
            <Field label={t('tts.labels.instructions')} htmlFor="tts-instructions" changed={changed.has('instructions')} hint={t('tts.labels.instructionsHint')}>
              <textarea id="tts-instructions" className="textarea" value={instructions} onChange={(e) => setInstructions(e.target.value)} placeholder={t('tts.labels.instructionsPlaceholder')} rows={3} />
            </Field>
          </Fold>
        }
        changes={changes}
        submit={{ label: t('tts.actions.generate'), busyLabel: t('tts.actions.generating'), busy: loading, disabled: !model || !text.trim() || noModel, why, icon: 'headphones' }}
      >
        <textarea
          className="ws-prompt textarea"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={t('tts.labels.inputPlaceholder')}
          aria-label={t('tts.labels.input')}
          rows={4}
          data-changed={changed.has('prompt') || undefined}
        />
        {!text && <Starters type="tts" onPick={setText} />}
        {supportsVoiceProfiles && (
          <div className="ws-voicebar" data-testid="ws-voicebar">
            <span className="dk-badge dk-badge--ok">{t('tts.voiceLibrary.cloningReady')}</span>
            {selectedProfile && (
              <span className="ws-voicebar__who">
                <span className="ws-avatar">{selectedProfile.name.slice(0, 2).toUpperCase()}</span>
                <strong>{selectedProfile.name}</strong>
                <small>{selectedProfile.language || t('voiceLibrary.metadata.languageUnknown')} · {formatProfileDuration(selectedProfile.audio?.duration_ms)}</small>
              </span>
            )}
            {!profilesLoading && profiles.length === 0 && (
              <span className="ws-hint">
                {t('tts.voiceLibrary.empty')}{' '}
                {isAdmin && <Link className="ws-link" to="/app/voice-library/new">{t('tts.voiceLibrary.create')}</Link>}
              </span>
            )}
            {profilesError && <span className="ws-hint ws-hint--error" role="alert">{profilesError}</span>}
            {isAdmin && profiles.length > 0 && <Link className="ws-link" to="/app/voice-library">{t('tts.voiceLibrary.manage')} <Icon name="arrow-right" /></Link>}
          </div>
        )}
        {!supportsVoiceProfiles && <p className="ws-hint">{t('tts.voiceLibrary.namedVoiceHint')}</p>}
      </ComposeCard>

      <RunArea>
        {loading ? (
          <JobCard label={t('tts.actions.generating')} detail={[model, current.voice && (selectedProfile?.name || manualVoice)].filter(Boolean).join(' · ')} />
        ) : error ? (
          <FailedCard message={error} onRetry={run} />
        ) : shownSrc ? (
          <ResultCard
            ws={ws}
            item={item}
            title={item?.title || text}
            meta={[item?.model || model, item?.params?.voice ? `${labels.voice} ${item.params.voice}` : '']}
            download={{ href: shownSrc, name: `tts-${(item?.model || model)}-${new Date().toISOString().slice(0, 10)}.mp3` }}
            onRerun={rerun}
          >
            <div className="ws-audio">
              <WaveformPlayer src={shownSrc} height={96} audioTestId={selectedEntry ? 'history-audio' : undefined} />
              <p className="ws-quote">&ldquo;{selectedEntry ? selectedEntry.prompt : text}&rdquo;</p>
            </div>
          </ResultCard>
        ) : (
          <EmptyRun icon="headphones" text={t('tts.empty')} />
        )}
        <RequestPanel endpoint="/v1/audio/speech" body={lastRequest} />
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
