// The Recognition and From a recording tabs of the Voice page. Ported from
// pages/VoiceRecognition.jsx. Speakers are voiceprints used to recognise who is
// speaking; the speech voices (reference recordings text-to-speech speaks in)
// are the Library tab. The tab strip comes from VoiceHub as `tabs`.
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
  function FeatureOff({ feature, tabs }) {
    return (
      <main className="page idn-page idn-off" data-testid="voice-off">
        {tabs}
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

  function VoicesPage({ path, tab = 'speakers', tabs, onSpeakerCount }) {
    const isAdmin = viewer.isAdmin()
    const [model, setModel] = useState(() => ctx.modelFrom(path) || '')
    const { models, loading: modelsLoading, refetch } = useSpeakerModels()
    const names = useMemo(() => models.map(m => m.id), [models])
    const needModel = !modelsLoading && names.length === 0

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

        {tabs}

        {tab === 'speakers' && (
          <Workbench
            kind="voice" model={model} canSpeech={isAdmin} onCount={onSpeakerCount}
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
    if (!viewer.hasFeature('voice_recognition')) return <FeatureOff feature="voice" tabs={props.tabs} />
    return <VoicesPage {...props} />
  }

  return { VoiceRecognition }
}
