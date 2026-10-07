import { useEffect, useMemo } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ImageGen from './ImageGen'
import VideoGen from './VideoGen'
import ThreeDGen from './ThreeDGen'
import TTS from './TTS'
import Sound from './Sound'
import AudioTransform from './AudioTransform'
import Diarization from './Diarization'
import StudioOverview from './StudioOverview'
import { useAuth } from '../context/AuthContext'
import { useModels } from '../hooks/useModels'
import {
  CAP_DIARIZATION, CAP_IMAGE, CAP_VIDEO, CAP_3D, CAP_3D_ANIMATION, CAP_TTS, CAP_SOUND_GENERATION, CAP_AUDIO_TRANSFORM,
} from '../utils/capabilities'
import Icon from '../components/Icon'
import '../components/studio/studio.css'

// One table for the six generators: the capability that makes a modality
// usable, the feature flag that can remove it entirely, and the group it reads
// under. Studio owns this so the tab strip and the overview cannot disagree
// about what exists.
const MODALITIES = [
  { key: 'diarization', capability: CAP_DIARIZATION, icon: 'users', group: 'voice', feature: 'audio_diarization' },
  { key: 'images', capability: CAP_IMAGE, icon: 'image', group: 'create' },
  { key: 'video', capability: CAP_VIDEO, icon: 'video', group: 'create' },
  { key: 'threed', capability: CAP_3D, icon: 'cube', group: 'create', feature: '3d' },
  { key: 'tts', capability: CAP_TTS, icon: 'headphones', group: 'voice' },
  { key: 'sound', capability: CAP_SOUND_GENERATION, icon: 'music', group: 'voice' },
  { key: 'transform', capability: CAP_AUDIO_TRANSFORM, icon: 'waveform', group: 'transform', feature: 'audio_transform' },
]

const OVERVIEW_TAB = { key: 'overview', icon: 'compass' }

const TAB_COMPONENTS = {
  diarization: Diarization,
  images: ImageGen,
  video: VideoGen,
  threed: ThreeDGen,
  tts: TTS,
  sound: Sound,
  transform: AudioTransform,
}

export default function Studio() {
  const { t } = useTranslation('media')
  const { hasFeature } = useAuth()
  const navigate = useNavigate()
  const { tab: pathTab } = useParams()
  const [searchParams] = useSearchParams()

  // Once, unfiltered. useModels(capability) fetches the whole list and filters
  // in the browser, so a hook per modality would be six identical requests to
  // /api/models/capabilities on every mount.
  const { models, loading: modelsLoading, error: modelsError, refetch: refetchModels } = useModels()

  // A modality whose feature is off is not listed at all. That is a different
  // thing from having no model, and the two must not look alike.
  const available = useMemo(
    () => MODALITIES.filter(m => !m.feature || hasFeature(m.feature)),
    [hasFeature],
  )

  const modalities = useMemo(() => available.map(m => ({
    ...m,
    installed: models
      .filter(model => model.capabilities?.includes(m.capability) ||
        (m.key === 'threed' && model.capabilities?.includes(CAP_3D_ANIMATION)))
      .map(model => model.id),
  })), [available, models])

  const tabs = [OVERVIEW_TAB, ...available]

  // Anything still arriving with ?tab= is sent to the path form once, replacing
  // the history entry so Back does not bounce between the two spellings.
  const legacyTab = searchParams.get('tab')
  useEffect(() => {
    if (legacyTab) navigate(`/app/studio/${legacyTab}`, { replace: true })
  }, [legacyTab, navigate])

  // Overview is the fallback for anything unrecognised or gated off. Landing on
  // Images was never a decision, only the first entry in an array.
  const activeTab = tabs.some(tab => tab.key === pathTab) ? pathTab : 'overview'

  const setTab = (key) => navigate(key === 'overview' ? '/app/studio' : `/app/studio/${key}`)

  const dotFor = (tab) => {
    if (tab.key === 'overview') return null
    const known = modalities.find(m => m.key === tab.key)
    return known?.installed.length > 0 ? 'on' : 'off'
  }

  // On a phone the tab row scrolls sideways; keep the current type in view.
  useEffect(() => {
    document.querySelector('.studio-tab-active')?.scrollIntoView?.({ inline: 'center', block: 'nearest' })
  }, [activeTab])

  const ActiveComponent = TAB_COMPONENTS[activeTab]

  return (
    <div>
      <div className="studio-tabs">
        {tabs.map(tab => {
          const dot = dotFor(tab)
          return (
            <button
              key={tab.key}
              data-tab={tab.key}
              className={`studio-tab${activeTab === tab.key ? ' studio-tab-active' : ''}`}
              onClick={() => setTab(tab.key)}
            >
              <Icon name={tab.icon} />
              <span>{tab.key === 'overview' ? t('studio.tabs.overview') : t(`studio.tabs.${tab.key}`)}</span>
              {/* Filled means a model on this machine serves the modality.
                  Decorative on its own: the overview states the same thing in
                  words, so a reader who cannot see the dot loses nothing. */}
              {dot && <span className={`studio-tab__dot studio-tab__dot--${dot}`} aria-hidden="true" />}
            </button>
          )
        })}
      </div>

      {ActiveComponent ? (
        <ActiveComponent />
      ) : (
        <StudioOverview
          modalities={modalities}
          modelsLoading={modelsLoading}
          modelsError={modelsError}
          refetchModels={refetchModels}
        />
      )}
    </div>
  )
}
