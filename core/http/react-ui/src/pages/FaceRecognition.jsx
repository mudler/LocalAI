import { useMemo, useState } from 'react'
import { useOutletContext, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import ModelSelector from '../components/ModelSelector'
// eslint-disable-next-line no-unused-vars
import PageHeader from '../components/PageHeader'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import ModelNeeded from '../components/identity/ModelNeeded'
// eslint-disable-next-line no-unused-vars
import MoreFaceTools from '../components/identity/MoreFaceTools'
// eslint-disable-next-line no-unused-vars
import Workbench from '../components/identity/Workbench'
import { useAuth } from '../context/AuthContext'
import { useModels } from '../hooks/useModels'
import { CAP_FACE_RECOGNITION } from '../utils/capabilities'
import './identity.css'

// The Faces page, in the same family as Voices: who is this, same person, the
// people this browser knows, what is stored. Detect and analyze, and the raw
// faceprint, sit behind a disclosure.
export default function FaceRecognition() {
  const { t } = useTranslation('biometrics')
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const { isAdmin } = useAuth()
  const [model, setModel] = useState(urlModel || '')
  const { models, loading, refetch } = useModels(CAP_FACE_RECOGNITION)
  const names = useMemo(() => models.map(m => m.id), [models])
  const needModel = !loading && names.length === 0

  return (
    <main className="page idn-page" data-testid="faces-page">
      <PageHeader
        title={t('faces.title')}
        supporting={t('faces.lede')}
        actions={!needModel ? (
          <div className="idn-modelpick">
            <ModelSelector value={model} onChange={setModel} capability={CAP_FACE_RECOGNITION} options={names} loading={loading} triggerClassName="idn-model" />
          </div>
        ) : null}
      />
      <Workbench
        kind="face" model={model} addToast={addToast}
        needed={needModel ? <ModelNeeded kind="face" addToast={addToast} isAdmin={isAdmin} onInstalled={refetch} /> : null}
        more={needModel ? null : (
          <details className="idn-disclosure idn-disclosure--page idn-more">
            <summary><Icon name="chevron-right" /> {t('face.moreTitle')} <span className="dk-hint">{t('face.moreHint')}</span></summary>
            <MoreFaceTools model={model} addToast={addToast} />
          </details>
        )}
      />
    </main>
  )
}
