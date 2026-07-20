import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ConfirmDialog from '../components/ConfirmDialog'
import EmptyState from '../components/EmptyState'
import ErrorWithTraceLink from '../components/ErrorWithTraceLink'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'
import StatusPill from '../components/StatusPill'
import WaveformPlayer from '../components/audio/WaveformPlayer'
import { useModels } from '../hooks/useModels'
import { useOperations } from '../hooks/useOperations'
import { useVoiceCloningGallery, useVoiceProfiles } from '../hooks/useVoiceProfiles'
import { CAP_TTS } from '../utils/capabilities'
import { modelsApi, voiceProfilesApi } from '../utils/api'
import { copyToClipboard } from '../utils/clipboard'
import { renderMarkdown } from '../utils/markdown'

const ROW_BARS = [35, 58, 78, 44, 68, 92, 55, 72, 38, 82, 64, 46, 74, 52, 88, 42, 66, 48]

function formatDuration(milliseconds) {
  const seconds = Math.max(0, Math.round((milliseconds || 0) / 1000))
  const minutes = Math.floor(seconds / 60)
  const rest = seconds % 60
  return minutes ? `${minutes}:${String(rest).padStart(2, '0')}` : `${rest}s`
}

function CompactWaveform() {
  return (
    <span className="voice-row__waveform" aria-hidden="true">
      {ROW_BARS.map((height, index) => <span key={index} style={{ height: `${height}%` }} />)}
    </span>
  )
}

function VoiceModelSetup({ t, galleryLoading, installableModels, galleryError, installing, operations, onInstall }) {
  return (
    <section className="voice-detail__model-setup" aria-labelledby="voice-model-setup-title">
      <div className="voice-detail__model-setup-heading">
        <i className="fas fa-cube" aria-hidden="true" />
        <div>
          <h3 id="voice-model-setup-title">{t('voiceLibrary.modelSetup.title')}</h3>
          <p>{t('voiceLibrary.modelSetup.body')}</p>
        </div>
      </div>

      {galleryLoading && (
        <div className="voice-detail__model-loading"><LoadingSpinner size="sm" /><span>{t('voiceLibrary.modelSetup.loading')}</span></div>
      )}
      {!galleryLoading && installableModels.length > 0 && (
        <ul className="voice-detail__model-list">
          {installableModels.map(model => {
            const busy = installing.has(model.id) || operations.some(operation => operation.name === model.id && !operation.completed && !operation.error)
            return (
              <li key={model.id}>
                <span>
                  <strong>{model.name}</strong>
                  <small>{model.backend || t('voiceLibrary.modelSetup.backendUnknown')}</small>
                </span>
                <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => onInstall(model)}>
                  <i className={`fas ${busy ? 'fa-spinner fa-spin' : 'fa-download'}`} aria-hidden="true" />{' '}
                  {t(busy ? 'voiceLibrary.modelSetup.installing' : 'voiceLibrary.modelSetup.install')}
                </button>
              </li>
            )
          })}
        </ul>
      )}
      {!galleryLoading && installableModels.length === 0 && (
        <p className="voice-detail__model-fallback">
          {galleryError ? t('voiceLibrary.modelSetup.loadFailed') : t('voiceLibrary.modelSetup.noneAvailable')}{' '}
          <Link to="/app/models">{t('voiceLibrary.actions.browseModels')}</Link>
        </p>
      )}
      <p className="voice-detail__capability-note">
        <i className="fas fa-circle-check" aria-hidden="true" /> {t('voiceLibrary.modelSetup.capabilityNote')}
      </p>
    </section>
  )
}

export default function VoiceLibrary() {
  const { t, i18n } = useTranslation('media')
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const selectedID = searchParams.get('selected') || ''
  const [search, setSearch] = useState('')
  const [language, setLanguage] = useState('all')
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [installing, setInstalling] = useState(() => new Map())
  const { profiles, loading, error, refetch } = useVoiceProfiles()
  const { models, loading: modelsLoading, refetch: refetchModels } = useModels(CAP_TTS)
  const { operations } = useOperations()

  const cloningModels = useMemo(() => models.filter(model => model.voice_cloning), [models])
  const {
    models: galleryModels,
    loading: galleryLoading,
    error: galleryError,
    refetch: refetchGalleryModels,
  } = useVoiceCloningGallery({ enabled: !modelsLoading && cloningModels.length === 0 })
  const installableModels = useMemo(() => galleryModels.filter(model => !model.installed).slice(0, 3), [galleryModels])
  const languages = useMemo(() => [...new Set(profiles.map(profile => profile.language).filter(Boolean))].sort(), [profiles])
  const filteredProfiles = useMemo(() => {
    const needle = search.trim().toLocaleLowerCase(i18n.language)
    return profiles.filter(profile => {
      if (language !== 'all' && profile.language !== language) return false
      if (!needle) return true
      return [profile.name, profile.description, profile.transcript, profile.language]
        .some(value => value?.toLocaleLowerCase(i18n.language).includes(needle))
    })
  }, [profiles, search, language, i18n.language])

  const selected = profiles.find(profile => profile.id === selectedID) || null
  const exampleModel = cloningModels[0]?.id || installableModels[0]?.name || '<voice-cloning-model>'
  const apiExample = selected ? `curl -X POST "$LOCALAI_URL/v1/audio/speech" \\
  -H "Authorization: Bearer $LOCALAI_API_KEY" \\
  -H "Content-Type: application/json" \\
  --data-binary @- \\
  --output speech.wav <<'JSON'
${JSON.stringify({
    model: exampleModel,
    input: 'Text to synthesize with this saved voice.',
    voice: selected.voice,
  }, null, 2)}
JSON` : ''

  useEffect(() => {
    if (loading || profiles.length === 0) return
    if (!selected) setSearchParams({ selected: profiles[0].id }, { replace: true })
  }, [loading, profiles, selected, setSearchParams])

  // Installation progress lives in the shared operations feed. Refresh the
  // installed capability view as that operation advances so this page becomes
  // usable as soon as the new model is ready.
  useEffect(() => {
    if (installing.size === 0) return
    refetchModels()
    setInstalling(previous => {
      const next = new Map(previous)
      let changed = false
      for (const [modelID, startedAt] of previous) {
        const active = operations.some(operation => operation.name === modelID && !operation.completed && !operation.error)
        const finished = operations.some(operation => operation.name === modelID && (operation.completed || operation.error))
        if (finished || (!active && Date.now() - startedAt > 5000)) {
          next.delete(modelID)
          changed = true
        }
      }
      return changed ? next : previous
    })
    refetchGalleryModels()
  }, [operations, installing.size, refetchGalleryModels, refetchModels])

  const selectProfile = (id) => setSearchParams({ selected: id })

  const deleteSelected = async () => {
    if (!selected) return
    setDeleting(true)
    try {
      await voiceProfilesApi.delete(selected.id)
      setConfirmDelete(false)
      addToast(t('voiceLibrary.toasts.deleted', { name: selected.name }), 'success')
      await refetch()
    } catch (err) {
      addToast(err.message, 'error')
    } finally {
      setDeleting(false)
    }
  }

  const installModel = async (model) => {
    setInstalling(previous => new Map(previous).set(model.id, Date.now()))
    try {
      await modelsApi.install(model.id)
      addToast(t('voiceLibrary.toasts.installStarted', { name: model.name }), 'success')
    } catch (err) {
      setInstalling(previous => {
        const next = new Map(previous)
        next.delete(model.id)
        return next
      })
      addToast(t('voiceLibrary.toasts.installFailed', { message: err.message }), 'error')
    }
  }

  const copyAPIExample = async () => {
    const copied = await copyToClipboard(apiExample)
    addToast(t(copied ? 'voiceLibrary.toasts.apiCopied' : 'voiceLibrary.toasts.copyFailed'), copied ? 'success' : 'error')
  }

  return (
    <main className="voice-library-page">
      <PageHeader
        title={<><i className="fas fa-wave-square" aria-hidden="true" /> {t('voiceLibrary.title')}</>}
        supporting={t('voiceLibrary.subtitle')}
        actions={(
          <Link className="btn btn-primary" to="/app/voice-library/new">
            <i className="fas fa-plus" aria-hidden="true" /> {t('voiceLibrary.actions.create')}
          </Link>
        )}
      />

      <div className="voice-library-summary" aria-label={t('voiceLibrary.summary.label')}>
        <span><strong>{profiles.length}</strong> {t('voiceLibrary.summary.profiles', { count: profiles.length })}</span>
        <span className="voice-library-summary__divider" aria-hidden="true" />
        {cloningModels.length > 0 ? (
          <StatusPill status="healthy" label={t('voiceLibrary.summary.modelsReady', { count: cloningModels.length })} />
        ) : (
          <StatusPill status="warning" label={t('voiceLibrary.summary.noModels')} />
        )}
      </div>

      {error && (
        <div className="voice-library-error">
          <ErrorWithTraceLink message={error} />
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => refetch()}>{t('voiceLibrary.actions.retry')}</button>
        </div>
      )}

      <section className="voice-library-shell" aria-label={t('voiceLibrary.title')}>
        <div className="voice-library-master">
          <div className="voice-library-toolbar">
            <label className="voice-library-search">
              <span className="sr-only">{t('voiceLibrary.search.label')}</span>
              <i className="fas fa-magnifying-glass" aria-hidden="true" />
              <input
                type="search"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder={t('voiceLibrary.search.placeholder')}
              />
            </label>
            <label>
              <span className="sr-only">{t('voiceLibrary.filters.language')}</span>
              <select className="input voice-library-language" value={language} onChange={(event) => setLanguage(event.target.value)}>
                <option value="all">{t('voiceLibrary.filters.allLanguages')}</option>
                {languages.map(value => <option key={value} value={value}>{value}</option>)}
              </select>
            </label>
          </div>

          <div className="voice-library-list" role="listbox" aria-label={t('voiceLibrary.listLabel')}>
            {loading && (
              <div className="voice-library-loading"><LoadingSpinner size="lg" /><span>{t('voiceLibrary.loading')}</span></div>
            )}
            {!loading && !error && profiles.length === 0 && (
              <EmptyState
                icon="fa-microphone-lines"
                title={t('voiceLibrary.empty.title')}
                body={t('voiceLibrary.empty.body')}
                actions={<Link className="btn btn-primary" to="/app/voice-library/new">{t('voiceLibrary.actions.createFirst')}</Link>}
                className="voice-library-empty"
              />
            )}
            {!loading && !error && profiles.length > 0 && filteredProfiles.length === 0 && (
              <EmptyState
                icon="fa-filter-circle-xmark"
                title={t('voiceLibrary.noResults.title')}
                body={t('voiceLibrary.noResults.body')}
                actions={<button type="button" className="btn btn-secondary" onClick={() => { setSearch(''); setLanguage('all') }}>{t('voiceLibrary.actions.clearFilters')}</button>}
                className="voice-library-empty"
              />
            )}
            {!loading && !error && filteredProfiles.map(profile => (
              <button
                type="button"
                role="option"
                aria-selected={profile.id === selected?.id}
                className={`voice-row${profile.id === selected?.id ? ' voice-row--selected' : ''}`}
                key={profile.id}
                onClick={() => selectProfile(profile.id)}
              >
                <span className="voice-row__avatar">{profile.name.slice(0, 2).toLocaleUpperCase(i18n.language)}</span>
                <span className="voice-row__content">
                  <span className="voice-row__head">
                    <strong>{profile.name}</strong>
                    <span>{formatDuration(profile.audio?.duration_ms)}</span>
                  </span>
                  <CompactWaveform />
                  <span className="voice-row__meta">
                    {profile.language || t('voiceLibrary.metadata.languageUnknown')}
                    <span aria-hidden="true">·</span>
                    {new Intl.DateTimeFormat(i18n.language, { dateStyle: 'medium' }).format(new Date(profile.created_at))}
                  </span>
                </span>
                <i className="fas fa-chevron-right voice-row__chevron" aria-hidden="true" />
              </button>
            ))}
          </div>
        </div>

        <div className="voice-library-detail">
          {!selected ? (
            <>
              <EmptyState
                icon="fa-wave-square"
                title={t('voiceLibrary.detail.emptyTitle')}
                body={t('voiceLibrary.detail.emptyBody')}
              />
              {!modelsLoading && cloningModels.length === 0 && (
                <VoiceModelSetup
                  t={t}
                  galleryLoading={galleryLoading}
                  installableModels={installableModels}
                  galleryError={galleryError}
                  installing={installing}
                  operations={operations}
                  onInstall={installModel}
                />
              )}
            </>
          ) : (
            <>
              <div className="voice-detail__header">
                <div>
                  <span className="voice-detail__eyebrow">{t('voiceLibrary.detail.eyebrow')}</span>
                  <h2>{selected.name}</h2>
                  {selected.description && (
                    <div className="markdown-body" dangerouslySetInnerHTML={{ __html: renderMarkdown(selected.description) }} />
                  )}
                </div>
                <StatusPill status="healthy" label={t('voiceLibrary.status.ready')} />
              </div>

              <div className="voice-detail__player">
                <WaveformPlayer
                  src={voiceProfilesApi.audioUrl(selected.id)}
                  height={88}
                  label={t('voiceLibrary.detail.referenceAudio')}
                  audioTestId="voice-profile-audio"
                />
              </div>

              <div className="voice-detail__section">
                <h3>{t('voiceLibrary.detail.transcript')}</h3>
                <blockquote>{selected.transcript}</blockquote>
              </div>

              <dl className="voice-detail__metadata">
                <div><dt>{t('voiceLibrary.metadata.language')}</dt><dd>{selected.language || t('voiceLibrary.metadata.languageUnknown')}</dd></div>
                <div><dt>{t('voiceLibrary.metadata.duration')}</dt><dd>{formatDuration(selected.audio?.duration_ms)}</dd></div>
                <div><dt>{t('voiceLibrary.metadata.sampleRate')}</dt><dd>{Math.round((selected.audio?.sample_rate || 0) / 1000)} kHz</dd></div>
                <div><dt>{t('voiceLibrary.metadata.created')}</dt><dd>{new Intl.DateTimeFormat(i18n.language, { dateStyle: 'long' }).format(new Date(selected.created_at))}</dd></div>
              </dl>

              <div className="voice-detail__consent">
                <i className="fas fa-shield-halved" aria-hidden="true" />
                <div><strong>{t('voiceLibrary.consent.confirmed')}</strong><span>{t('voiceLibrary.consent.confirmedAt', { date: new Intl.DateTimeFormat(i18n.language, { dateStyle: 'medium' }).format(new Date(selected.consent_confirmed_at)) })}</span></div>
              </div>

              {!modelsLoading && cloningModels.length === 0 && (
                <VoiceModelSetup
                  t={t}
                  galleryLoading={galleryLoading}
                  installableModels={installableModels}
                  galleryError={galleryError}
                  installing={installing}
                  operations={operations}
                  onInstall={installModel}
                />
              )}

              <details className="voice-detail__api">
                <summary>
                  <span><strong>{t('voiceLibrary.api.title')}</strong><small>{t('voiceLibrary.api.summary')}</small></span>
                  <i className="fas fa-chevron-down" aria-hidden="true" />
                </summary>
                <div className="voice-detail__api-body">
                  <p>{t('voiceLibrary.api.body')}</p>
                  <div className="voice-detail__compatible-models">
                    <strong>{t('voiceLibrary.api.compatibleModels')}</strong>
                    {cloningModels.length > 0 ? (
                      <ul>
                        {cloningModels.map(model => (
                          <li key={model.id}><span>{model.id}</span><small>{model.backend}</small></li>
                        ))}
                      </ul>
                    ) : (
                      <span>{t('voiceLibrary.api.noInstalledModels')}</span>
                    )}
                  </div>
                  <div className="voice-detail__code-heading">
                    <span>{t('voiceLibrary.api.curlExample')}</span>
                    <button type="button" className="btn btn-ghost btn-sm" onClick={copyAPIExample}>
                      <i className="far fa-copy" aria-hidden="true" /> {t('voiceLibrary.api.copy')}
                    </button>
                  </div>
                  <pre><code>{apiExample}</code></pre>
                  <p className="voice-detail__api-note">{t('voiceLibrary.api.endpointNote')}</p>
                </div>
              </details>

              <div className="voice-detail__actions">
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={cloningModels.length === 0}
                  onClick={() => navigate(`/app/tts?voice=${encodeURIComponent(selected.id)}`)}
                >
                  <i className="fas fa-headphones" aria-hidden="true" /> {t('voiceLibrary.actions.useInTTS')}
                </button>
                <button type="button" className="btn btn-danger" onClick={() => setConfirmDelete(true)}>
                  <i className="fas fa-trash" aria-hidden="true" /> {t('voiceLibrary.actions.delete')}
                </button>
              </div>
            </>
          )}
        </div>
      </section>

      <ConfirmDialog
        open={confirmDelete}
        title={t('voiceLibrary.deleteDialog.title')}
        message={selected ? t('voiceLibrary.deleteDialog.message', { name: selected.name }) : ''}
        confirmLabel={deleting ? t('voiceLibrary.deleteDialog.deleting') : t('voiceLibrary.actions.delete')}
        danger
        onConfirm={deleteSelected}
        onCancel={() => !deleting && setConfirmDelete(false)}
      />
    </main>
  )
}
