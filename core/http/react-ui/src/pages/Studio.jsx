import { useEffect, useMemo } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ImageGen from './ImageGen'
import VideoGen from './VideoGen'
import ThreeDGen from './ThreeDGen'
import TTS from './TTS'
import Sound from './Sound'
import AudioTransform from './AudioTransform'
import StudioOverview from './StudioOverview'
import { useAuth } from '../context/AuthContext'
import { useModels } from '../hooks/useModels'
import { useOperations } from '../hooks/useOperations'
import { readAllMediaHistory } from '../hooks/useMediaHistory'
import { use3DHistory } from '../hooks/use3DHistory'
import {
  CAP_IMAGE, CAP_VIDEO, CAP_3D, CAP_TTS, CAP_SOUND_GENERATION, CAP_AUDIO_TRANSFORM,
} from '../utils/capabilities'

// One table for the six generators: the capability that makes a modality
// usable, the feature flag that can remove it entirely, and the group it reads
// under. Studio owns this so the tab strip and the overview cannot disagree
// about what exists.
const MODALITIES = [
  { key: 'images', capability: CAP_IMAGE, icon: 'fas fa-image', group: 'create', history: 'image' },
  { key: 'video', capability: CAP_VIDEO, icon: 'fas fa-video', group: 'create', history: 'video' },
  { key: 'threed', capability: CAP_3D, icon: 'fas fa-cube', group: 'create', feature: '3d' },
  { key: 'tts', capability: CAP_TTS, icon: 'fas fa-headphones', group: 'voice', history: 'tts' },
  { key: 'sound', capability: CAP_SOUND_GENERATION, icon: 'fas fa-music', group: 'voice', history: 'sound' },
  { key: 'transform', capability: CAP_AUDIO_TRANSFORM, icon: 'fas fa-wave-square', group: 'transform', feature: 'audio_transform' },
]

const OVERVIEW_TAB = { key: 'overview', icon: 'fas fa-compass' }

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
  const navigate = useNavigate()
  const { tab: pathTab } = useParams()
  const [searchParams] = useSearchParams()
  const { operations } = useOperations()

  // Once, unfiltered. useModels(capability) fetches the whole list and filters
  // in the browser, so a hook per modality would be six identical requests to
  // /api/models/capabilities on every mount.
  const { models } = useModels()

  // A modality whose feature is off is not listed at all. That is a different
  // thing from having no model, and the two must not look alike.
  const available = useMemo(
    () => MODALITIES.filter(m => !m.feature || hasFeature(m.feature)),
    [hasFeature],
  )

  // Read once and share. 3D is deliberately separate: its history is IndexedDB
  // (the entries carry GLB blobs), so it arrives asynchronously and cannot come
  // from the same synchronous read as the other five.
  const history = useMemo(() => readAllMediaHistory(), [])
  const { entries: threeDEntries } = use3DHistory()

  const modalities = useMemo(() => available.map(m => ({
    ...m,
    installed: models
      .filter(model => model.capabilities?.includes(m.capability))
      .map(model => model.id),
    typical: typicalCost(m.key === 'threed' ? threeDEntries : history[m.history]),
  })), [available, models, history, threeDEntries])

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
              <i className={tab.icon} />
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
          recent={recentAcross(available, history, threeDEntries)}
          running={operations.filter(isGeneration)}
          onPick={setTab}
        />
      )}
    </div>
  )
}

// Median rather than mean: one cold first run on a model that was still loading
// would otherwise set the expectation for every run after it.
function typicalCost(entries) {
  const times = (entries || []).map(e => e.elapsedMs).filter(ms => ms > 0).sort((a, b) => a - b)
  if (times.length === 0) return null
  const median = times[Math.floor(times.length / 2)]
  return `~${(median / 1000).toFixed(median < 10_000 ? 1 : 0)}s`
}

function recentAcross(available, history, threeDEntries) {
  const fromLocalStorage = available
    .filter(m => m.history)
    .flatMap(m => (history[m.history] || []).map(e => ({ ...e, modality: m.key })))
  const from3D = available.some(m => m.key === 'threed')
    ? (threeDEntries || []).map(e => ({ ...e, modality: 'threed' }))
    : []
  return [...fromLocalStorage, ...from3D]
    .sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0))
    .slice(0, 6)
}

// Media generation only. A backend install is an operation too, and it belongs
// on Activity rather than in a page about making things.
function isGeneration(op) {
  return ['image', 'video', 'tts', 'sound', 'transform', '3d'].includes(op.type)
}
