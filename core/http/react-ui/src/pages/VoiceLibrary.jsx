import { useEffect, useMemo, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useNavigate, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import ConfirmDialog from '../components/ConfirmDialog'
// eslint-disable-next-line no-unused-vars
import EmptyState from '../components/EmptyState'
// eslint-disable-next-line no-unused-vars
import LoadingSpinner from '../components/LoadingSpinner'
// eslint-disable-next-line no-unused-vars
import SideSheet from '../components/SideSheet'
// eslint-disable-next-line no-unused-vars
import WaveformPlayer from '../components/audio/WaveformPlayer'
import { useModels } from '../hooks/useModels'
import { useOperations } from '../hooks/useOperations'
import { useVoiceCloningGallery } from '../hooks/useVoiceProfiles'
import { CAP_TTS } from '../utils/capabilities'
import { modelsApi, voiceProfilesApi } from '../utils/api'
import { copyToClipboard } from '../utils/clipboard'
import { renderMarkdown } from '../utils/markdown'
import Icon from '../components/Icon'

const ROW_BARS = [35, 58, 78, 44, 68, 92, 55, 72, 38, 82, 64, 46, 74, 52, 88, 42, 66, 48]

function formatDuration(milliseconds) {
  const seconds = Math.max(0, Math.round((milliseconds || 0) / 1000))
  const minutes = Math.floor(seconds / 60)
  const rest = seconds % 60
  return minutes ? `${minutes}:${String(rest).padStart(2, '0')}` : `${rest}s`
}

// eslint-disable-next-line no-unused-vars
function CompactWaveform() {
  return (
    <span className="voice-row__waveform" aria-hidden="true">
      {ROW_BARS.map((height, index) => <span key={index} style={{ height: `${height}%` }} />)}
    </span>
  )
}

// eslint-disable-next-line no-unused-vars
function VoiceModelSetup({ t, galleryLoading, installableModels, galleryError, installing, operations, onInstall }) {
  return (
    <section className="voice-detail__model-setup" aria-labelledby="voice-model-setup-title">
      <div className="voice-detail__model-setup-heading">
        <Icon name="cube" />
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
                <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={busy} onClick={() => onInstall(model)}>
                  <Icon name={busy ? 'spinner' : 'download'} spin={Boolean(busy)} />{' '}
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
        <Icon name="check-circle" /> {t('voiceLibrary.modelSetup.capabilityNote')}
      </p>
    </section>
  )
}

// The speech voices tab of the Voices page: the reference recordings that
// text-to-speech can use. The page hands in the profile list so the tab count
// and the list come from one fetch.
export default function SpeechVoices({ library }) {
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
  const { profiles, loading, error, refetch } = library
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
  const closeProfile = () => setSearchParams({}, { replace: true })

  const deleteSelected = async () => {
    if (!selected) return
    setDeleting(true)
    try {
      await voiceProfilesApi.delete(selected.id)
      setConfirmDelete(false)
      addToast(t('voiceLibrary.toasts.deleted', { name: selected.name }), 'success')
      closeProfile()
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

  const dateFmt = (value, dateStyle) => new Intl.DateTimeFormat(i18n.language, { dateStyle }).format(new Date(value))

  return (
    <section className="voice-library-page" aria-labelledby="speech-voices-title">
      <div className="idn-speech__head">
        <div>
          <h2 className="idn-h2" id="speech-voices-title">{t('voiceLibrary.title')} <span className="dk-hubtab-count">{profiles.length}</span></h2>
          <p className="dk-hint idn-speech__lede">{t('voiceLibrary.subtitle')}</p>
        </div>
        <div className="idn-speech__acts">
          {cloningModels.length > 0
            ? <span className="dk-badge dk-badge--ok"><Icon name="check" /> {t('voiceLibrary.summary.modelsReady', { count: cloningModels.length })}</span>
            : <span className="dk-badge dk-badge--warn">{t('voiceLibrary.summary.noModels')}</span>}
          <Link className="dk-btn dk-btn--secondary" to="/app/voice-library/new" data-testid="speech-create">
            <Icon name="plus" /> {t('voiceLibrary.actions.create')}
          </Link>
        </div>
      </div>

      {error && (
        <div className="idn-error" role="alert">
          <span className="idn-error__icon" aria-hidden="true"><Icon name="alert-circle" /></span>
          <div className="idn-error__body">
            <h3 className="idn-error__title">{t('voiceLibrary.loadFailed')}</h3>
            <p className="idn-error__raw dk-mono">{error}</p>
            <div className="idn-error__acts"><button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => refetch()}>{t('voiceLibrary.actions.retry')}</button></div>
          </div>
        </div>
      )}

      {profiles.length > 0 && (
        <div className="voice-library-toolbar">
          <span className="dk-input-icon voice-library-search">
            <Icon name="search" className="dk-icon" />
            <input className="dk-input" type="search" aria-label={t('voiceLibrary.search.label')} value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t('voiceLibrary.search.placeholder')} />
          </span>
          <span className="dk-select-wrap">
            <select className="dk-select voice-library-language" aria-label={t('voiceLibrary.filters.language')} value={language} onChange={(event) => setLanguage(event.target.value)}>
              <option value="all">{t('voiceLibrary.filters.allLanguages')}</option>
              {languages.map(value => <option key={value} value={value}>{value}</option>)}
            </select>
          </span>
        </div>
      )}

      <div className="voice-library-list" role="group" aria-label={t('voiceLibrary.listLabel')}>
        {loading && <div className="voice-library-loading"><LoadingSpinner size="lg" /><span>{t('voiceLibrary.loading')}</span></div>}
        {!loading && !error && profiles.length === 0 && (
          <EmptyState
            className="voice-library-empty"
            icon="mic"
            title={t('voiceLibrary.empty.title')}
            body={t('voiceLibrary.empty.body')}
            actions={<Link className="btn btn-primary" to="/app/voice-library/new">{t('voiceLibrary.actions.createFirst')}</Link>}
          />
        )}
        {!loading && !error && profiles.length > 0 && filteredProfiles.length === 0 && (
          <EmptyState
            className="voice-library-empty"
            icon="filter-off"
            title={t('voiceLibrary.noResults.title')}
            body={t('voiceLibrary.noResults.body')}
            actions={<button type="button" className="btn btn-secondary" onClick={() => { setSearch(''); setLanguage('all') }}>{t('voiceLibrary.actions.clearFilters')}</button>}
          />
        )}
        {!loading && !error && filteredProfiles.length > 0 && (
          <div className="dk-list idn-speech__list">
            {filteredProfiles.map(profile => (
              <button
                type="button"
                className={`dk-row voice-row${profile.id === selected?.id ? ' voice-row--selected' : ''}`}
                aria-selected={profile.id === selected?.id}
                key={profile.id}
                onClick={() => selectProfile(profile.id)}
              >
                <span className="dk-row-lead idn-avatar" aria-hidden="true">{profile.name.slice(0, 2).toLocaleUpperCase(i18n.language)}</span>
                <span className="dk-row-main">
                  <span className="dk-row-title">{profile.name}</span>
                  <span className="dk-row-meta">{profile.description || t('voiceLibrary.metadata.languageUnknown')}</span>
                </span>
                <span className="dk-row-end voice-row__end">
                  <CompactWaveform />
                  <span className="dk-mono voice-row__meta">
                    {profile.language || t('voiceLibrary.metadata.languageUnknown')}
                    <span aria-hidden="true"> · </span>
                    {formatDuration(profile.audio?.duration_ms)}
                  </span>
                  <Icon name="chevron-right" className="voice-row__chevron" />
                </span>
              </button>
            ))}
          </div>
        )}
      </div>

      {!loading && !error && profiles.length > 0 && (
        <p className="idn-privacy"><Icon name="lock" /> {t('voiceCreate.privacy.body')}</p>
      )}

      {!loading && !error && profiles.length === 0 && !modelsLoading && cloningModels.length === 0 && (
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

      {selected && (
        <SideSheet
          title={selected.name}
          description={selected.description ? null : t('voiceLibrary.detail.eyebrow')}
          onClose={closeProfile}
          closeLabel={t('voiceLibrary.detail.close')}
          wide
          testId="speech-voice-sheet"
          labelId="speech-voice-title"
          footer={(
            <>
              <button type="button" className="dk-btn dk-btn--ghost dk-btn--danger voice-delete" onClick={() => setConfirmDelete(true)}>
                <Icon name="trash" /> {t('voiceLibrary.actions.delete')}
              </button>
              <button
                type="button"
                className="dk-btn dk-btn--primary"
                disabled={cloningModels.length === 0}
                onClick={() => navigate(`/app/tts?voice=${encodeURIComponent(selected.id)}`)}
              >
                <Icon name="headphones" /> {t('voiceLibrary.actions.useInTTS')}
              </button>
            </>
          )}
        >
          <div className="voice-library-detail">
            {selected.description && (
              <div className="markdown-body voice-detail__description" dangerouslySetInnerHTML={{ __html: renderMarkdown(selected.description) }} />
            )}

            <div className="voice-detail__player">
              <WaveformPlayer
                src={voiceProfilesApi.audioUrl(selected.id)}
                height={88}
                label={t('voiceLibrary.detail.referenceAudio')}
                audioTestId="voice-profile-audio"
              />
            </div>

            <div className="voice-detail__section">
              <h3 className="dk-eyebrow">{t('voiceLibrary.detail.transcript')}</h3>
              <blockquote>{selected.transcript}</blockquote>
            </div>

            <dl className="dk-kv voice-detail__metadata">
              <dt>{t('voiceLibrary.metadata.language')}</dt><dd>{selected.language || t('voiceLibrary.metadata.languageUnknown')}</dd>
              <dt>{t('voiceLibrary.metadata.duration')}</dt><dd>{formatDuration(selected.audio?.duration_ms)}</dd>
              <dt>{t('voiceLibrary.metadata.sampleRate')}</dt><dd>{Math.round((selected.audio?.sample_rate || 0) / 1000)} kHz</dd>
              <dt>{t('voiceLibrary.metadata.created')}</dt><dd>{dateFmt(selected.created_at, 'long')}</dd>
              <dt>{t('voiceLibrary.consent.label')}</dt>
              <dd><span className="dk-badge dk-badge--ok"><Icon name="check" /> {t('voiceLibrary.consent.confirmed')}</span> <span className="dk-hint">{t('voiceLibrary.consent.confirmedAt', { date: dateFmt(selected.consent_confirmed_at, 'medium') })}</span></dd>
            </dl>

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

            <details className="voice-detail__api idn-disclosure">
              <summary>
                <Icon name="chevron-right" />
                <span><strong>{t('voiceLibrary.api.title')}</strong> <small className="dk-hint">{t('voiceLibrary.api.summary')}</small></span>
              </summary>
              <div className="voice-detail__api-body">
                <p>{t('voiceLibrary.api.body')}</p>
                <div className="voice-detail__compatible-models">
                  <strong>{t('voiceLibrary.api.compatibleModels')}</strong>
                  {cloningModels.length > 0 ? (
                    <ul>
                      {cloningModels.map(model => (
                        <li key={model.id}><span className="dk-mono">{model.id}</span><small>{model.backend}</small></li>
                      ))}
                    </ul>
                  ) : (
                    <span>{t('voiceLibrary.api.noInstalledModels')}</span>
                  )}
                </div>
                <div className="voice-detail__code-heading">
                  <span className="dk-eyebrow">{t('voiceLibrary.api.curlExample')}</span>
                  <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={copyAPIExample}>
                    <Icon name="copy" /> {t('voiceLibrary.api.copy')}
                  </button>
                </div>
                <pre><code>{apiExample}</code></pre>
                <p className="dk-hint">{t('voiceLibrary.api.endpointNote')}</p>
              </div>
            </details>
          </div>
        </SideSheet>
      )}

      <ConfirmDialog
        open={confirmDelete}
        title={t('voiceLibrary.deleteDialog.title')}
        message={selected ? t('voiceLibrary.deleteDialog.message', { name: selected.name }) : ''}
        confirmLabel={deleting ? t('voiceLibrary.deleteDialog.deleting') : t('voiceLibrary.actions.delete')}
        danger
        onConfirm={deleteSelected}
        onCancel={() => !deleting && setConfirmDelete(false)}
      />
    </section>
  )
}
