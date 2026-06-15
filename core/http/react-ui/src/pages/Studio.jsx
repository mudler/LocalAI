import { useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ImageGen from './ImageGen'
import VideoGen from './VideoGen'
import TTS from './TTS'
import Sound from './Sound'

const TABS = [
  { key: 'images', labelKey: 'studio.tabs.images', icon: 'fas fa-image' },
  { key: 'video', labelKey: 'studio.tabs.video', icon: 'fas fa-video' },
  { key: 'tts', labelKey: 'studio.tabs.tts', icon: 'fas fa-headphones' },
  { key: 'sound', labelKey: 'studio.tabs.sound', icon: 'fas fa-music' },
]

const TAB_COMPONENTS = {
  images: ImageGen,
  video: VideoGen,
  tts: TTS,
  sound: Sound,
}

export default function Studio() {
  const { t } = useTranslation('media')
  const [searchParams, setSearchParams] = useSearchParams()
  const activeTab = searchParams.get('tab') || 'images'

  const setTab = (key) => {
    setSearchParams({ tab: key }, { replace: true })
  }

  const ActiveComponent = TAB_COMPONENTS[activeTab] || ImageGen

  return (
    <div>
      <div className="studio-tabs">
        {TABS.map(tab => (
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
