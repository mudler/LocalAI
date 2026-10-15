import { useMemo, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useOutletContext, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import ModelSelector from '../components/ModelSelector'
// eslint-disable-next-line no-unused-vars
import PageHeader from '../components/PageHeader'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import MoreVoiceTools from '../components/identity/MoreVoiceTools'
// eslint-disable-next-line no-unused-vars
import ModelNeeded from '../components/identity/ModelNeeded'
// eslint-disable-next-line no-unused-vars
import Workbench from '../components/identity/Workbench'
import { useAuth } from '../context/AuthContext'
import { useModels } from '../hooks/useModels'
import { useVoiceProfiles } from '../hooks/useVoiceProfiles'
import { CAP_SPEAKER_RECOGNITION } from '../utils/capabilities'
import { loadList } from '../utils/identity'
// eslint-disable-next-line no-unused-vars
import SpeechVoices from './VoiceLibrary'
import './identity.css'

// The Voices page. Two unrelated things carry the word voice and are kept
// apart: Speakers (voiceprints used to recognise who is speaking) and Speech
// voices (reference recordings text-to-speech speaks in). The tab is the route:
// /app/voice is Speakers, /app/voice?tab=recording the diarization link, and
// /app/voice-library Speech voices.
export default function VoiceRecognition({ tab: routeTab }) {
  const { t } = useTranslation('biometrics')
  const { model: urlModel } = useParams()
  const { addToast } = useOutletContext()
  const { isAdmin, hasFeature } = useAuth()
  const [params] = useSearchParams()
  const tab = routeTab || (params.get('tab') === 'recording' ? 'recording' : 'speakers')
  const [model, setModel] = useState(urlModel || '')
  const { models, loading: modelsLoading, refetch } = useModels(CAP_SPEAKER_RECOGNITION)
  const names = useMemo(() => models.map(m => m.id), [models])
  const library = useVoiceProfiles({ enabled: isAdmin })
  const [savedCount, setSavedCount] = useState(() => loadList('localai_voice_enrollments').length)
  const needModel = !modelsLoading && names.length === 0

  return (
    <main className="page idn-page" data-testid="voices-page">
      <PageHeader
        title={t('voices.title')}
        supporting={t('voices.lede')}
        actions={tab === 'speakers' && !needModel ? (
          <div className="idn-modelpick">
            <ModelSelector value={model} onChange={setModel} capability={CAP_SPEAKER_RECOGNITION} options={names} loading={modelsLoading} triggerClassName="idn-model" />
          </div>
        ) : null}
      />

      <nav className="dk-tabs idn-tabs" role="tablist" aria-label={t('voices.tabs')}>
        <Link className="dk-tab" role="tab" to="/app/voice" aria-selected={tab === 'speakers'} aria-current={tab === 'speakers' ? 'page' : undefined}>{t('voices.speakers')} <span className="dk-hubtab-count">{savedCount}</span></Link>
        {isAdmin && (
          <Link className="dk-tab" role="tab" to="/app/voice-library" aria-selected={tab === 'speech'} aria-current={tab === 'speech' ? 'page' : undefined}>
            {t('voices.speech')} <span className="dk-hubtab-count">{library.loading ? '' : library.profiles.length}</span>
          </Link>
        )}
        <Link className="dk-tab" role="tab" to="/app/voice?tab=recording" aria-selected={tab === 'recording'} aria-current={tab === 'recording' ? 'page' : undefined}>{t('voices.recording')}</Link>
      </nav>

      {tab === 'speakers' && (
        <Workbench
          kind="voice" model={model} addToast={addToast} canSpeech={isAdmin} onCount={setSavedCount}
          needed={needModel ? <ModelNeeded kind="voice" addToast={addToast} isAdmin={isAdmin} onInstalled={refetch} /> : null}
          more={needModel ? null : (
            <details className="idn-disclosure idn-disclosure--page idn-more">
              <summary><Icon name="chevron-right" /> {t('voice.moreTitle')} <span className="dk-hint">{t('voice.moreHint')}</span></summary>
              <MoreVoiceTools model={model} addToast={addToast} />
            </details>
          )}
        />
      )}

      {tab === 'speech' && <SpeechVoices library={library} />}

      {tab === 'recording' && (
        <section className="idn-recording dk-card" data-testid="recording-tab">
          <h2 className="idn-h2">{t('recording.title')}</h2>
          <p className="idn-recording__text">{t('recording.text')}</p>
          <ol className="idn-recording__steps">
            <li>{t('recording.step1')}</li>
            <li>{t('recording.step2')}</li>
            <li>{t('recording.step3')}</li>
          </ol>
          {hasFeature('audio_diarization')
            ? <Link className="dk-btn dk-btn--primary" to="/app/studio/diarization">{t('recording.open')} <Icon name="arrow-right" /></Link>
            : <p className="dk-hint">{t('recording.off')}</p>}
          <p className="dk-hint">{t('recording.warning')}</p>
        </section>
      )}
    </main>
  )
}
