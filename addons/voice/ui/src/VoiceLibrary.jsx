// The speech voices page: the reference recordings that text-to-speech can use.
// Ported from pages/VoiceLibrary.jsx. The gallery description of a profile is
// drawn as plain text (white-space: pre-wrap) instead of Markdown, because the
// Markdown renderer (marked, dompurify) is a host dependency the bundle must
// not import. The detail opens in a side panel of the shell (ctx.openPanel).
import { copyToClipboard } from './lib/clipboard.js'

const CAP_TTS = 'FLAG_TTS'
const ROW_BARS = [35, 58, 78, 44, 68, 92, 55, 72, 38, 82, 64, 46, 74, 52, 88, 42, 66, 48]

function formatDuration(milliseconds) {
  const seconds = Math.max(0, Math.round((milliseconds || 0) / 1000))
  const minutes = Math.floor(seconds / 60)
  const rest = seconds % 60
  return minutes ? `${minutes}:${String(rest).padStart(2, '0')}` : `${rest}s`
}

export function createVoiceLibrary(ctx, api) {
  const { h, Fragment, t, Icon, ui, viewer } = ctx
  const { useCallback, useEffect, useMemo, useRef, useState } = ctx.React
  const language = () => ctx.sdk.locale.language()

  function CompactWaveform() {
    return (
      <span className="voice-row__waveform" aria-hidden="true">
        {ROW_BARS.map((height, index) => <span key={index} style={{ height: `${height}%` }} />)}
      </span>
    )
  }

  // The profile list: one fetch, refetched without blanking the list.
  function useProfiles() {
    const [state, setState] = useState({ profiles: [], loading: true, error: null })
    const load = useCallback(async ({ silent = false } = {}) => {
      if (!silent) setState(s => ({ ...s, loading: true }))
      try {
        const response = await api.voiceProfiles.list()
        setState({ profiles: response?.data || [], loading: false, error: null })
      } catch (err) {
        setState(s => ({ ...s, loading: false, error: err?.message || 'Could not load voice profiles' }))
      }
    }, [])
    useEffect(() => { load() }, [load])
    const refetch = useCallback(() => load({ silent: true }), [load])
    return { ...state, refetch }
  }

  // The voice-cloning models that are installed, the ones the gallery offers
  // when none is, and the installs in progress.
  function useModelSetup() {
    const [installed, setInstalled] = useState({ models: [], loading: true })
    const [gallery, setGallery] = useState({ models: [], loading: false, error: null })
    const [operations, setOperations] = useState([])
    const [installing, setInstalling] = useState(() => new Map())
    const live = useRef(true)
    useEffect(() => { live.current = true; return () => { live.current = false } }, [])

    const refetchModels = useCallback(async () => {
      try {
        const body = await api.models.listCapabilities()
        const list = (body?.data || []).filter(m => !m.disabled && m.capabilities?.includes(CAP_TTS) && m.voice_cloning)
        if (live.current) setInstalled({ models: list, loading: false })
      } catch {
        if (live.current) setInstalled({ models: [], loading: false })
      }
    }, [])
    useEffect(() => { refetchModels() }, [refetchModels])

    const cloningModels = installed.models
    const wantGallery = !installed.loading && cloningModels.length === 0
    const refetchGallery = useCallback(async () => {
      try {
        const response = await api.models.list({ capability: 'voice_cloning', items: 4, page: 1, sort: 'name', order: 'asc' })
        if (live.current) setGallery({ models: response?.models || [], loading: false, error: null })
      } catch (err) {
        if (live.current) setGallery({ models: [], loading: false, error: err?.message || 'Could not load compatible models' })
      }
    }, [])
    useEffect(() => {
      if (!wantGallery) return
      setGallery(g => ({ ...g, loading: true }))
      refetchGallery()
    }, [wantGallery, refetchGallery])

    // The operations feed is admin-only on the server, so it is polled only
    // while an install this page started is running.
    const polling = installing.size > 0 && viewer.isAdmin()
    useEffect(() => {
      if (!polling) return undefined
      let stop = false
      const tick = async () => {
        try {
          const data = await api.operations.list()
          if (!stop) setOperations(data?.operations || (Array.isArray(data) ? data : []))
        } catch { /* the next tick tries again */ }
      }
      tick()
      const timer = setInterval(tick, 2000)
      return () => { stop = true; clearInterval(timer) }
    }, [polling])

    useEffect(() => {
      if (installing.size === 0) return
      refetchModels()
      setInstalling(previous => {
        const next = new Map(previous)
        let changed = false
        for (const [modelID, startedAt] of previous) {
          const active = operations.some(op => op.name === modelID && !op.completed && !op.error)
          const finished = operations.some(op => op.name === modelID && (op.completed || op.error))
          if (finished || (!active && Date.now() - startedAt > 5000)) { next.delete(modelID); changed = true }
        }
        return changed ? next : previous
      })
      refetchGallery()
    }, [operations, installing.size, refetchModels, refetchGallery])

    const install = async (model) => {
      setInstalling(previous => new Map(previous).set(model.id, Date.now()))
      try {
        await api.models.install(model.id)
        ctx.toast(t('voiceLibrary.toasts.installStarted', { name: model.name }), 'success')
      } catch (err) {
        setInstalling(previous => { const next = new Map(previous); next.delete(model.id); return next })
        ctx.toast(t('voiceLibrary.toasts.installFailed', { message: err.message }), 'error')
      }
    }

    const installableModels = useMemo(() => gallery.models.filter(m => !m.installed).slice(0, 3), [gallery.models])
    return {
      cloningModels, modelsLoading: installed.loading,
      installableModels, galleryLoading: gallery.loading, galleryError: gallery.error,
      installing, operations, install,
    }
  }

  function VoiceModelSetup({ setup }) {
    const { galleryLoading, installableModels, galleryError, installing, operations, install } = setup
    const admin = viewer.isAdmin()
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
          <div className="voice-detail__model-loading"><ui.Spinner size="sm" /><span>{t('voiceLibrary.modelSetup.loading')}</span></div>
        )}
        {!galleryLoading && installableModels.length > 0 && (
          <ul className="voice-detail__model-list">
            {installableModels.map(model => {
              const busy = installing.has(model.id) || operations.some(op => op.name === model.id && !op.completed && !op.error)
              return (
                <li key={model.id}>
                  <span>
                    <strong>{model.name}</strong>
                    <small>{model.backend || t('voiceLibrary.modelSetup.backendUnknown')}</small>
                  </span>
                  {admin && (
                    <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={busy} onClick={() => install(model)}>
                      <Icon name={busy ? 'spinner' : 'download'} spin={Boolean(busy)} />{' '}
                      {t(busy ? 'voiceLibrary.modelSetup.installing' : 'voiceLibrary.modelSetup.install')}
                    </button>
                  )}
                </li>
              )
            })}
          </ul>
        )}
        {!galleryLoading && installableModels.length === 0 && (
          <p className="voice-detail__model-fallback">
            {galleryError ? t('voiceLibrary.modelSetup.loadFailed') : t('voiceLibrary.modelSetup.noneAvailable')}{' '}
            <a href={`${ctx.basePath}/app/models`}>{t('voiceLibrary.actions.browseModels')}</a>
          </p>
        )}
        <p className="voice-detail__capability-note">
          <Icon name="check-circle" /> {t('voiceLibrary.modelSetup.capabilityNote')}
        </p>
      </section>
    )
  }

  // The body of the side panel of one profile.
  function ProfileDetail({ profile, close, onDeleted }) {
    const setup = useModelSetup()
    const { cloningModels, modelsLoading, installableModels } = setup
    const [confirmDelete, setConfirmDelete] = useState(false)
    const [deleting, setDeleting] = useState(false)
    const admin = viewer.isAdmin()
    const dateFmt = (value, dateStyle) => new Intl.DateTimeFormat(language(), { dateStyle }).format(new Date(value))
    const exampleModel = cloningModels[0]?.id || installableModels[0]?.name || '<voice-cloning-model>'
    const apiExample = `curl -X POST "$LOCALAI_URL/v1/audio/speech" \\
  -H "Authorization: Bearer $LOCALAI_API_KEY" \\
  -H "Content-Type: application/json" \\
  --data-binary @- \\
  --output speech.wav <<'JSON'
${JSON.stringify({
    model: exampleModel,
    input: 'Text to synthesize with this saved voice.',
    voice: profile.voice,
  }, null, 2)}
JSON`

    const remove = async () => {
      setDeleting(true)
      try {
        await api.voiceProfiles.remove(profile.id)
        setConfirmDelete(false)
        ctx.toast(t('voiceLibrary.toasts.deleted', { name: profile.name }), 'success')
        close()
        await onDeleted()
      } catch (err) {
        ctx.toast(err.message, 'error')
      } finally {
        setDeleting(false)
      }
    }
    const copyExample = async () => {
      const copied = await copyToClipboard(apiExample)
      ctx.toast(t(copied ? 'voiceLibrary.toasts.apiCopied' : 'voiceLibrary.toasts.copyFailed'), copied ? 'success' : 'error')
    }

    return (
      <div className="voice-library-detail" data-testid="speech-voice-sheet">
        {profile.description && (
          <div className="voice-detail__description" style={{ whiteSpace: 'pre-wrap' }}>{profile.description}</div>
        )}

        <div className="voice-detail__player">
          <ui.WaveformPlayer
            src={api.voiceProfiles.audioUrl(profile.id)}
            height={88}
            label={t('voiceLibrary.detail.referenceAudio')}
            audioTestId="voice-profile-audio"
          />
        </div>

        <div className="voice-detail__section">
          <h3 className="dk-eyebrow">{t('voiceLibrary.detail.transcript')}</h3>
          <blockquote>{profile.transcript}</blockquote>
        </div>

        <dl className="dk-kv voice-detail__metadata">
          <dt>{t('voiceLibrary.metadata.language')}</dt><dd>{profile.language || t('voiceLibrary.metadata.languageUnknown')}</dd>
          <dt>{t('voiceLibrary.metadata.duration')}</dt><dd>{formatDuration(profile.audio?.duration_ms)}</dd>
          <dt>{t('voiceLibrary.metadata.sampleRate')}</dt><dd>{Math.round((profile.audio?.sample_rate || 0) / 1000)} kHz</dd>
          <dt>{t('voiceLibrary.metadata.created')}</dt><dd>{dateFmt(profile.created_at, 'long')}</dd>
          <dt>{t('voiceLibrary.consent.label')}</dt>
          <dd><span className="dk-badge dk-badge--ok"><Icon name="check" /> {t('voiceLibrary.consent.confirmed')}</span> <span className="dk-hint">{t('voiceLibrary.consent.confirmedAt', { date: dateFmt(profile.consent_confirmed_at, 'medium') })}</span></dd>
        </dl>

        {!modelsLoading && cloningModels.length === 0 && <VoiceModelSetup setup={setup} />}

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
              <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={copyExample}>
                <Icon name="copy" /> {t('voiceLibrary.api.copy')}
              </button>
            </div>
            <pre><code>{apiExample}</code></pre>
            <p className="dk-hint">{t('voiceLibrary.api.endpointNote')}</p>
          </div>
        </details>

        <div className="voice-detail__actions">
          {admin && (
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--danger voice-delete" onClick={() => setConfirmDelete(true)}>
              <Icon name="trash" /> {t('voiceLibrary.actions.delete')}
            </button>
          )}
          <button
            type="button"
            className="dk-btn dk-btn--primary"
            disabled={cloningModels.length === 0}
            onClick={() => { close(); ctx.sdk.navigation.navigate(`speech?voice=${encodeURIComponent(profile.id)}`) }}
          >
            <Icon name="headphones" /> {t('voiceLibrary.actions.useInTTS')}
          </button>
        </div>

        <ui.ConfirmDialog
          open={confirmDelete}
          title={t('voiceLibrary.deleteDialog.title')}
          message={t('voiceLibrary.deleteDialog.message', { name: profile.name })}
          confirmLabel={deleting ? t('voiceLibrary.deleteDialog.deleting') : t('voiceLibrary.actions.delete')}
          danger
          onConfirm={remove}
          onCancel={() => !deleting && setConfirmDelete(false)}
        />
      </div>
    )
  }

  function VoiceLibrary() {
    const { profiles, loading, error, refetch } = useProfiles()
    const setup = useModelSetup()
    const { cloningModels, modelsLoading } = setup
    const [search, setSearch] = useState('')
    const [languageFilter, setLanguageFilter] = useState('all')
    const admin = viewer.isAdmin()
    const selectedParam = ctx.useSearch().get('selected') || ''
    const opened = useRef('')

    const languages = useMemo(() => [...new Set(profiles.map(p => p.language).filter(Boolean))].sort(), [profiles])
    const filteredProfiles = useMemo(() => {
      const needle = search.trim().toLocaleLowerCase(language())
      return profiles.filter(profile => {
        if (languageFilter !== 'all' && profile.language !== languageFilter) return false
        if (!needle) return true
        return [profile.name, profile.description, profile.transcript, profile.language]
          .some(value => value?.toLocaleLowerCase(language()).includes(needle))
      })
    }, [profiles, search, languageFilter])

    const openProfile = useCallback((profile) => {
      // The shell draws a panel outside the page root, so the detail carries
      // the add-on scope itself (the sheet is scoped to [data-addon]).
      ctx.sdk.layers.openPanel({
        title: profile.name,
        description: profile.description ? undefined : t('voiceLibrary.detail.eyebrow'),
        wide: true,
        render: ({ close }) => <div data-addon={ctx.sdk.addonId}><ProfileDetail profile={profile} close={close} onDeleted={refetch} /></div>,
      })
    }, [refetch])

    // A link such as ?selected=<id> (the create page sends it) opens that profile once.
    useEffect(() => {
      if (!selectedParam || opened.current === selectedParam) return
      const profile = profiles.find(p => p.id === selectedParam)
      if (!profile) return
      opened.current = selectedParam
      openProfile(profile)
    }, [selectedParam, profiles, openProfile])

    const newButton = (className) => admin && (
      <ctx.Link page="create" className={className} data-testid="voice-library-new">
        <Icon name="plus" /> {t('voiceLibrary.actions.create')}
      </ctx.Link>
    )

    return (
      <main className="page voice-library-page" data-testid="voice-library">
        <ui.PageHeader
          title={<>{t('voiceLibrary.title')} <span className="dk-hubtab-count">{profiles.length}</span></>}
          supporting={t('voiceLibrary.subtitle')}
          actions={(
            <div className="idn-speech__acts">
              {cloningModels.length > 0
                ? <span className="dk-badge dk-badge--ok"><Icon name="check" /> {t(cloningModels.length === 1 ? 'voiceLibrary.summary.modelsReady_one' : 'voiceLibrary.summary.modelsReady_other', { count: cloningModels.length })}</span>
                : <span className="dk-badge dk-badge--warn">{t('voiceLibrary.summary.noModels')}</span>}
              {newButton('dk-btn dk-btn--secondary')}
            </div>
          )}
        />

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
              <select className="dk-select voice-library-language" aria-label={t('voiceLibrary.filters.language')} value={languageFilter} onChange={(event) => setLanguageFilter(event.target.value)}>
                <option value="all">{t('voiceLibrary.filters.allLanguages')}</option>
                {languages.map(value => <option key={value} value={value}>{value}</option>)}
              </select>
            </span>
          </div>
        )}

        <div className="voice-library-list" role="group" aria-label={t('voiceLibrary.listLabel')}>
          {loading && <div className="voice-library-loading"><ui.Spinner size="lg" /><span>{t('voiceLibrary.loading')}</span></div>}
          {!loading && !error && profiles.length === 0 && (
            <ui.EmptyState icon="mic" title={t('voiceLibrary.empty.title')} text={t('voiceLibrary.empty.body')}>
              {newButton('btn btn-primary')}
            </ui.EmptyState>
          )}
          {!loading && !error && profiles.length > 0 && filteredProfiles.length === 0 && (
            <ui.EmptyState icon="filter-off" title={t('voiceLibrary.noResults.title')} text={t('voiceLibrary.noResults.body')}>
              <button type="button" className="btn btn-secondary" onClick={() => { setSearch(''); setLanguageFilter('all') }}>{t('voiceLibrary.actions.clearFilters')}</button>
            </ui.EmptyState>
          )}
          {!loading && !error && filteredProfiles.length > 0 && (
            <div className="dk-list idn-speech__list">
              {filteredProfiles.map(profile => (
                <button
                  type="button"
                  className="dk-row voice-row"
                  key={profile.id}
                  onClick={() => openProfile(profile)}
                >
                  <span className="dk-row-lead idn-avatar" aria-hidden="true">{profile.name.slice(0, 2).toLocaleUpperCase(language())}</span>
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

        {!loading && !error && profiles.length === 0 && !modelsLoading && cloningModels.length === 0 && <VoiceModelSetup setup={setup} />}
      </main>
    )
  }

  return { VoiceLibrary }
}
