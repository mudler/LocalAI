import { useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ImageGen from './ImageGen'
import VideoGen from './VideoGen'
import ThreeDGen from './ThreeDGen'
import TTS from './TTS'
import Sound from './Sound'
import AudioTransform from './AudioTransform'
import { useAuth } from '../context/AuthContext'

const BASE_TABS = [
  { key: 'images', labelKey: 'studio.tabs.images', icon: 'fas fa-image' },
  { key: 'video', labelKey: 'studio.tabs.video', icon: 'fas fa-video' },
  { key: 'threed', labelKey: 'studio.tabs.threed', icon: 'fas fa-cube' },
  { key: 'tts', labelKey: 'studio.tabs.tts', icon: 'fas fa-headphones' },
  { key: 'sound', labelKey: 'studio.tabs.sound', icon: 'fas fa-music' },
]

const TRANSFORM_TAB = { key: 'transform', labelKey: 'studio.tabs.transform', icon: 'fas fa-wave-square' }

const TAB_COMPONENTS = {
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
  const [searchParams, setSearchParams] = useSearchParams()
  const activeTab = searchParams.get('tab') || 'images'

  // Transform is a distinct capability; only show its tab when enabled.
  const tabs = hasFeature('audio_transform') ? [...BASE_TABS, TRANSFORM_TAB] : BASE_TABS

  const setTab = (key) => {
    setSearchParams({ tab: key }, { replace: true })
  }

  const ActiveComponent =
    (activeTab === 'transform' && !hasFeature('audio_transform'))
      ? ImageGen
      : (TAB_COMPONENTS[activeTab] || ImageGen)

  return (
    <div>
      <div className="studio-tabs">
        {tabs.map(tab => (
          <button
            key={tab.key}
            className={`studio-tab${activeTab === tab.key ? ' studio-tab-active' : ''}`}
            onClick={() => setTab(tab.key)}
          >
            <i className={tab.icon} />
            <span>{t(tab.labelKey)}</span>
          </button>
        ))}
      </div>
      <ActiveComponent />
    </div>
  )
}
