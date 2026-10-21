// The Faces page: who is this, same person, the people this browser knows and
// what is stored. Ported from pages/FaceRecognition.jsx. The known-faces list
// and the enrol sheet are the shared views inside the shared Workbench.
// Detect and analyze, and the raw faceprint, sit behind a disclosure.
import { createClipInput } from '../../../internal/identity-ui/src/ClipInput.jsx'
import { createWorkbench } from '../../../internal/identity-ui/src/Workbench.jsx'
import { createKnownList } from '../../../internal/identity-ui/src/KnownList.jsx'
import { createEnrolSheet } from '../../../internal/identity-ui/src/EnrolSheet.jsx'
import { createVerdict } from '../../../internal/identity-ui/src/Verdict.jsx'
import { createDistanceScale } from '../../../internal/identity-ui/src/DistanceScale.jsx'
import { createErrorNote } from '../../../internal/identity-ui/src/ErrorNote.jsx'
import { createModelNeeded } from '../../../internal/identity-ui/src/ModelNeeded.jsx'
import { createBoundingBoxCanvas } from '../../../internal/identity-ui/src/BoundingBoxCanvas.jsx'
import { createIdentityRegistry } from '../../../internal/identity-ui/src/registry.js'
import { createMoreFaceTools } from './MoreFaceTools.jsx'

const CAP_FACE_RECOGNITION = 'FLAG_FACE_RECOGNITION'

// `identity` is the shared helper module (identity.js), passed as in the Voice
// add-on. The Faces page has no tab counts, so it reads nothing from it.
// eslint-disable-next-line no-unused-vars
export function createFaceRecognition(ctx, api, identity) {
  const { h, t, Icon, ui, viewer } = ctx
  const { useCallback, useEffect, useMemo, useState } = ctx.React

  const { ClipInput } = createClipInput(ctx)
  const { KnownList } = createKnownList(ctx)
  const { Verdict } = createVerdict(ctx)
  const { DistanceScale } = createDistanceScale(ctx)
  const { ErrorNote } = createErrorNote(ctx)
  const { useRegistry } = createIdentityRegistry(ctx)
  const { BoundingBoxCanvas } = createBoundingBoxCanvas(ctx)
  const { EnrolSheet } = createEnrolSheet(ctx, { ClipInput, ErrorNote, api })
  const { ModelNeeded } = createModelNeeded(ctx, api)
  const { MoreFaceTools } = createMoreFaceTools(ctx, api)
  const { Workbench } = createWorkbench(ctx, { ClipInput, KnownList, EnrolSheet, Verdict, DistanceScale, ErrorNote, useRegistry, BoundingBoxCanvas, api })

  // Shown in place of a page whose feature the signed-in user may not use. It
  // replaces a silent redirect: the person learns what is off and what turns it on.
  function FeatureOff() {
    return (
      <main className="page idn-page idn-off" data-testid="face-off">
        <section className="idn-needed dk-card">
          <span className="dk-badge dk-badge--warn"><Icon name="lock" /> {t('off.badge')}</span>
          <h1 className="idn-h2">{t('off.face.title')}</h1>
          <p className="idn-needed__text">{t('off.face.text')}</p>
          <details className="idn-disclosure" open>
            <summary><Icon name="chevron-right" /> {t('needed.whatTitle')}</summary>
            <p className="idn-disclosure__text">{t('off.face.what')}</p>
          </details>
        </section>
      </main>
    )
  }

  // The face models that are installed: the capability list of the core,
  // narrowed to the face-recognition flag, as the core hook useModels does.
  function useFaceModels() {
    const [state, setState] = useState({ models: [], loading: true })
    const load = useCallback(async (silent) => {
      if (!silent) setState(s => ({ ...s, loading: true }))
      try {
        const data = await api.models.listCapabilities()
        const models = (data?.data || []).filter(m => !m.disabled && m.capabilities?.includes(CAP_FACE_RECOGNITION))
        setState({ models, loading: false })
      } catch {
        setState({ models: [], loading: false })
      }
    }, [])
    useEffect(() => { load(false) }, [load])
    return { ...state, refetch: useCallback(() => load(true), [load]) }
  }

  function FacesPage({ path }) {
    const isAdmin = viewer.isAdmin()
    const [model, setModel] = useState(() => ctx.modelFrom(path) || '')
    const { models, loading, refetch } = useFaceModels()
    const names = useMemo(() => models.map(m => m.id), [models])
    const needModel = !loading && names.length === 0

    return (
      <main className="page idn-page" data-testid="faces-page">
        <ui.PageHeader
          title={t('faces.title')}
          supporting={t('faces.lede')}
          actions={!needModel ? (
            <div className="idn-modelpick">
              <ui.ModelSelector value={model} onChange={setModel} capability={CAP_FACE_RECOGNITION} options={names} loading={loading} triggerClassName="idn-model" />
            </div>
          ) : null}
        />
        <Workbench
          kind="face" model={model}
          needed={needModel ? <ModelNeeded kind="face" isAdmin={isAdmin} onInstalled={refetch} /> : null}
          more={needModel ? null : (
            <details className="idn-disclosure idn-disclosure--page idn-more">
              <summary><Icon name="chevron-right" /> {t('face.moreTitle')} <span className="dk-hint">{t('face.moreHint')}</span></summary>
              <MoreFaceTools model={model} />
            </details>
          )}
        />
      </main>
    )
  }

  // The page is for a viewer who holds face_recognition (spec, "Classification":
  // the gate of the page is the gate of the core route, and the library inside
  // it has no stricter gate). The server checks every /v1/face/* call again. A
  // viewer without the feature gets the notice and no call.
  function FaceRecognition(props) {
    if (!viewer.hasFeature('face_recognition')) return <FeatureOff />
    return <FacesPage {...props} />
  }

  return { FaceRecognition }
}
