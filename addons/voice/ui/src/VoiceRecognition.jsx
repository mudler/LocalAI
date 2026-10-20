// The Voices page: who is speaking. Ported from pages/VoiceRecognition.jsx.
// Two unrelated things carry the word voice and are kept apart: Speakers
// (voiceprints used to recognise who is speaking) and Speech voices (reference
// recordings text-to-speech speaks in). Speech voices is the library page of
// this add-on, so its tab is a link to that page; From a recording is the
// diarization hand-off, a tab of this page.
import { createClipInput } from '../../../internal/identity-ui/src/ClipInput.jsx'
import { createWorkbench } from '../../../internal/identity-ui/src/Workbench.jsx'
import { createKnownList } from '../../../internal/identity-ui/src/KnownList.jsx'
import { createEnrolSheet } from '../../../internal/identity-ui/src/EnrolSheet.jsx'
import { createVerdict } from '../../../internal/identity-ui/src/Verdict.jsx'
import { createDistanceScale } from '../../../internal/identity-ui/src/DistanceScale.jsx'
import { createErrorNote } from '../../../internal/identity-ui/src/ErrorNote.jsx'
import { createModelNeeded } from '../../../internal/identity-ui/src/ModelNeeded.jsx'
import { createIdentityRegistry } from '../../../internal/identity-ui/src/registry.js'
import { createMoreVoiceTools } from './MoreVoiceTools.jsx'

const CAP_SPEAKER_RECOGNITION = 'FLAG_SPEAKER_RECOGNITION'

// `identity` is the shared helper module (identity.js): loadList counts the
// people this browser already knows before the workbench mounts.
export function createVoiceRecognition(ctx, api, identity) {
  const { h, t, Icon, ui, viewer } = ctx
  const { useCallback, useEffect, useMemo, useState } = ctx.React

  const { ClipInput } = createClipInput(ctx)
  const { KnownList } = createKnownList(ctx)
  const { Verdict } = createVerdict(ctx)
  const { DistanceScale } = createDistanceScale(ctx)
  const { ErrorNote } = createErrorNote(ctx)
  const { useRegistry } = createIdentityRegistry(ctx)
  const { EnrolSheet } = createEnrolSheet(ctx, { ClipInput, ErrorNote, api })
  const { ModelNeeded } = createModelNeeded(ctx, api)
  const { MoreVoiceTools } = createMoreVoiceTools(ctx, api)
  // BoundingBoxCanvas is for faces; the voice page does not pass it.
  const { Workbench } = createWorkbench(ctx, { ClipInput, KnownList, EnrolSheet, Verdict, DistanceScale, ErrorNote, useRegistry, api })

  // Shown in place of a page whose feature the signed-in user may not use. It
  // replaces a silent redirect: the person learns what is off and what turns it on.
  function FeatureOff({ feature }) {
    return (
      <main className="page idn-page idn-off" data-testid="voice-off">
        <section className="idn-needed dk-card">
          <span className="dk-badge dk-badge--warn"><Icon name="lock" /> {t('off.badge')}</span>
          <h1 className="idn-h2">{t(`off.${feature}.title`)}</h1>
          <p className="idn-needed__text">{t(`off.${feature}.text`)}</p>
          <details className="idn-disclosure" open>
            <summary><Icon name="chevron-right" /> {t('needed.whatTitle')}</summary>
            <p className="idn-disclosure__text">{t(`off.${feature}.what`)}</p>
          </details>
        </section>
      </main>
    )
  }

  // The speaker models that are installed: the capability list of the core,
  // narrowed to the speaker-recognition flag, as the core hook useModels does.
  function useSpeakerModels() {
    const [state, setState] = useState({ models: [], loading: true })
    const load = useCallback(async (silent) => {
      if (!silent) setState(s => ({ ...s, loading: true }))
      try {
        const data = await api.models.listCapabilities()
        const models = (data?.data || []).filter(m => !m.disabled && m.capabilities?.includes(CAP_SPEAKER_RECOGNITION))
        setState({ models, loading: false })
      } catch {
        setState({ models: [], loading: false })
      }
    }, [])
    useEffect(() => { load(false) }, [load])
    return { ...state, refetch: useCallback(() => load(true), [load]) }
  }

  function VoicesPage({ path }) {
    const tab = ctx.useSearch().get('tab') === 'recording' ? 'recording' : 'speakers'
    const isAdmin = viewer.isAdmin()
    const [model, setModel] = useState(() => ctx.modelFrom(path) || '')
    const { models, loading: modelsLoading, refetch } = useSpeakerModels()
    const names = useMemo(() => models.map(m => m.id), [models])
    const [library, setLibrary] = useState({ loading: true, count: 0 })
    const [savedCount, setSavedCount] = useState(() => identity.loadList('localai_voice_enrollments').length)
    const needModel = !modelsLoading && names.length === 0

    // The speech voices count on the tab. Only an administrator sees the tab,
    // so only an administrator's page asks for the list.
    useEffect(() => {
      if (!isAdmin) return undefined
      let live = true
      api.voiceProfiles.list()
        .then((body) => { if (live) setLibrary({ loading: false, count: (body?.data || []).length }) })
        .catch(() => { if (live) setLibrary({ loading: false, count: 0 }) })
      return () => { live = false }
    }, [isAdmin])

    return (
      <main className="page idn-page" data-testid="voices-page">
        <ui.PageHeader
          title={t('voices.title')}
          supporting={t('voices.lede')}
          actions={tab === 'speakers' && !needModel ? (
            <div className="idn-modelpick">
              <ui.ModelSelector value={model} onChange={setModel} capability={CAP_SPEAKER_RECOGNITION} options={names} loading={modelsLoading} triggerClassName="idn-model" />
            </div>
          ) : null}
        />

        <nav className="dk-tabs idn-tabs" role="tablist" aria-label={t('voices.tabs')}>
          <ctx.Link className="dk-tab" role="tab" page="recognition" aria-selected={tab === 'speakers'} aria-current={tab === 'speakers' ? 'page' : undefined}>{t('voices.speakers')} <span className="dk-hubtab-count">{savedCount}</span></ctx.Link>
          {isAdmin && (
            <ctx.Link className="dk-tab" role="tab" page="library" aria-selected="false">
              {t('voices.speech')} <span className="dk-hubtab-count">{library.loading ? '' : library.count}</span>
            </ctx.Link>
          )}
          <ctx.Link className="dk-tab" role="tab" page="recognition" query="tab=recording" aria-selected={tab === 'recording'} aria-current={tab === 'recording' ? 'page' : undefined}>{t('voices.recording')}</ctx.Link>
        </nav>

        {tab === 'speakers' && (
          <Workbench
            kind="voice" model={model} canSpeech={isAdmin} onCount={setSavedCount}
            needed={needModel ? <ModelNeeded kind="voice" isAdmin={isAdmin} onInstalled={refetch} /> : null}
            more={needModel ? null : (
              <details className="idn-disclosure idn-disclosure--page idn-more">
                <summary><Icon name="chevron-right" /> {t('voice.moreTitle')} <span className="dk-hint">{t('voice.moreHint')}</span></summary>
                <MoreVoiceTools model={model} />
              </details>
            )}
          />
        )}

        {tab === 'recording' && (
          <section className="idn-recording dk-card" data-testid="recording-tab">
            <h2 className="idn-h2">{t('recording.title')}</h2>
            <p className="idn-recording__text">{t('recording.text')}</p>
            <ol className="idn-recording__steps">
              <li>{t('recording.step1')}</li>
              <li>{t('recording.step2')}</li>
              <li>{t('recording.step3')}</li>
            </ol>
            {viewer.hasFeature('audio_diarization')
              ? <a className="dk-btn dk-btn--primary" href={`${ctx.basePath}/app/studio/diarization`}>{t('recording.open')} <Icon name="arrow-right" /></a>
              : <p className="dk-hint">{t('recording.off')}</p>}
            <p className="dk-hint">{t('recording.warning')}</p>
          </section>
        )}
      </main>
    )
  }

  // The page is for a viewer who holds voice_recognition (spec, "Classification":
  // the gate of the page is the gate of the core route). The server checks every
  // /v1/voice/* call again. A viewer without it gets the notice and no call.
  function VoiceRecognition(props) {
    if (!viewer.hasFeature('voice_recognition')) return <FeatureOff feature="voice" />
    return <VoicesPage {...props} />
  }

  return { VoiceRecognition }
}
