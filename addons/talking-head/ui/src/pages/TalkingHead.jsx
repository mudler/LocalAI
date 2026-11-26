import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { realtimeApi } from '../utils/api'
import { useModels } from '../hooks/useModels'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import PortraitPicker from '../components/PortraitPicker'
// eslint-disable-next-line no-unused-vars
import HeadSettings from '../components/HeadSettings'
// eslint-disable-next-line no-unused-vars
import TurnMode from '../modes/TurnMode'
import { chooseModels, fromPipeline, missingParts, modelGroups, splitPipelines } from '../lib/models'
import { addPortrait, loadPortraits, removePortrait, savePortraits } from '../lib/portrait'
import { DEFAULT_NFE } from '../lib/turn'
// The layout, buttons, sheet and transcript styles are the Talk page's own.
import '../../../../talk/ui/src/pages/talk.css'
import './head.css'

const SETTINGS_KEY = 'localai.talking-head.settings'
const SELECTED_KEY = 'localai.talking-head.selected'

export const DEFAULT_INSTRUCTIONS = 'You are a friendly talking face. Answer in one or two short sentences. Your reply is spoken aloud and turned into a video, so use plain words with no markdown, lists or emoji.'

// The ways to talk to the face. Each mode draws the stage and the transcript;
// the page owns what they share: the portrait, the models and the settings.
// A live mode over WebRTC joins this list next to the turn-based one.
const MODES = [{ id: 'turns', Component: TurnMode }]

function readJSON(key, fallback) {
  try {
    const raw = globalThis.localStorage?.getItem(key)
    return raw ? JSON.parse(raw) : fallback
  } catch {
    return fallback
  }
}

function writeJSON(key, value) {
  try { globalThis.localStorage?.setItem(key, JSON.stringify(value)) } catch { /* storage refused: keep it in memory */ }
}

export default function TalkingHead() {
  const { t } = useTranslation('head')
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const wanted = searchParams.get('model') || ''

  const { models, loading: modelsLoading, error: modelsError } = useModels()
  const [pipelineList, setPipelineList] = useState([])
  useEffect(() => {
    realtimeApi.pipelineModels().then((list) => setPipelineList(list || [])).catch(() => setPipelineList([]))
  }, [])

  const groups = useMemo(() => modelGroups(models), [models])
  const pipelines = useMemo(() => splitPipelines(pipelineList), [pipelineList])

  const [settings, setSettings] = useState(() => ({
    pipeline: '', chat: '', tts: '', stt: '', avatar: '', voice: '', instructions: DEFAULT_INSTRUCTIONS, nfe: DEFAULT_NFE, seed: '',
    ...readJSON(SETTINGS_KEY, {}),
  }))
  useEffect(() => { writeJSON(SETTINGS_KEY, settings) }, [settings])
  const update = (patch) => setSettings((s) => ({ ...s, ...patch }))

  // A chosen pipeline names the chat, speech and transcription models; the
  // avatar model is always chosen on its own.
  const pipeline = pipelines.find((p) => p.name === settings.pipeline)
  const choice = useMemo(() => {
    const picked = chooseModels(settings, groups, wanted)
    return pipeline ? { ...picked, chat: pipeline.llm, tts: pipeline.tts, stt: pipeline.transcription } : picked
  }, [settings, groups, wanted, pipeline])
  const voice = settings.voice || (pipeline?.voice ?? '')

  // A link from a video model that is not a talking-head model says so once.
  useEffect(() => {
    if (!wanted || modelsLoading || groups.avatar.includes(wanted)) return
    addToast(t('notices.notAvatar', { model: wanted }), 'warning')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wanted, modelsLoading])

  const [portraits, setPortraits] = useState(() => loadPortraits())
  const [selectedId, setSelectedId] = useState(() => readJSON(SELECTED_KEY, ''))
  const portrait = portraits.find((p) => p.id === selectedId) || portraits[0] || null
  useEffect(() => { writeJSON(SELECTED_KEY, portrait?.id || '') }, [portrait?.id])
  const keepPortraits = (next) => {
    setPortraits(next)
    if (!savePortraits(next)) addToast(t('notices.notStored'), 'warning')
  }

  const [sheetOpen, setSheetOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [modeId] = useState(MODES[0].id)
  const Mode = (MODES.find((m) => m.id === modeId) || MODES[0]).Component

  const missing = modelsLoading ? [] : missingParts(choice)

  const chips = (
    <div className="talk-chips">
      <button type="button" className="home-chip talk-chip" onClick={() => setSheetOpen(true)} title={t('settings.role.avatar')} data-testid="head-avatar-chip">
        <Icon name="video" />
        <span className="home-chip__text">{choice.avatar || t('chips.noAvatar')}</span>
      </button>
      <button type="button" className="home-chip talk-chip" onClick={() => setSheetOpen(true)} title={t('settings.role.chat')}>
        <Icon name="sparkles" />
        <span className="home-chip__text">{pipeline ? pipeline.name : (choice.chat || t('chips.noChat'))}</span>
      </button>
      <button type="button" className="home-chip talk-chip" onClick={() => setSheetOpen(true)} title={t('settings.voice')}>
        <Icon name="volume" />
        <span className="home-chip__text">{voice || t('chips.defaultVoice')}</span>
      </button>
    </div>
  )

  const blocked = modelsError ? (
    <section className="talk-card" role="alert" data-testid="head-models-error">
      <h2>{t('empty.errorTitle')}</h2>
      <p>{t('empty.errorBody', { message: modelsError })}</p>
    </section>
  ) : missing.length > 0 ? (
    <section className="talk-card" data-testid="head-missing" aria-labelledby="head-missing-title">
      <h2 id="head-missing-title">{t('empty.title')}</h2>
      <ul className="head-missing">
        {missing.map((part) => <li key={part}>{t(`empty.${part}`)}</li>)}
      </ul>
      <p>{t('empty.licence')}</p>
      <div className="talk-card__acts">
        <button type="button" className="home-primary" onClick={() => navigate(`/app/models?q=${missing.includes('avatar') ? 'avatarforcing' : 'tts'}`)}>
          <Icon name="store" /> {t('empty.gallery')}
        </button>
      </div>
    </section>
  ) : null

  return (
    <div className="talk-page head-page" data-mode={modeId}>
      <header className="talk-hd">
        <h1 className="page-title talk-hd__title">{t('title')}</h1>
        <span className="talk-hd__sub">{t('subtitle')}</span>
        <div className="talk-hd__acts">
          <button
            type="button"
            className="talk-icobtn"
            onClick={() => setSheetOpen(true)}
            title={t('settings.title')}
            aria-label={t('settings.title')}
            data-testid="head-settings-button"
          >
            <Icon name="sliders" />
          </button>
        </div>
      </header>

      <Mode
        choice={choice}
        voice={voice}
        settings={settings}
        portrait={portrait}
        chips={chips}
        blocked={blocked}
        loading={modelsLoading}
        onBusy={setBusy}
        portraitPicker={(
          <PortraitPicker
            portraits={portraits}
            selectedId={portrait?.id || ''}
            disabled={busy}
            onSelect={setSelectedId}
            onAdd={(p) => { keepPortraits(addPortrait(portraits, p)); setSelectedId(p.id) }}
            onRemove={(id) => keepPortraits(removePortrait(portraits, id))}
          />
        )}
      />

      {sheetOpen && (
        <HeadSettings
          settings={{ ...settings, voice }}
          choice={choice}
          groups={groups}
          pipelines={pipelines}
          onChange={update}
          onPipeline={(name) => {
            const p = pipelines.find((x) => x.name === name)
            update(p ? fromPipeline(p) : { pipeline: '' })
          }}
          onClose={() => setSheetOpen(false)}
        />
      )}
    </div>
  )
}
