import { useCallback, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useModels } from './useModels'
import { FAVOURITES_KEY } from './useMediaHistory'
import { readAllMediaHistory } from './useMediaHistory'
import {
  CAP_3D, CAP_3D_ANIMATION, CAP_AUDIO_TRANSFORM, CAP_DIARIZATION, CAP_IMAGE, CAP_SOUND_GENERATION, CAP_TTS, CAP_VIDEO,
} from '../utils/capabilities'
import {
  NEXT_STEPS, TYPE_INFO, TYPE_ORDER, canTake, collectWork, handoffPath, toggleId,
} from '../utils/studioWork'

// The model capabilities that make each Studio type usable. 3D accepts either
// the mesh or the animation flag, as the Studio tab dots do.
const TYPE_CAPABILITIES = {
  images: [CAP_IMAGE],
  video: [CAP_VIDEO],
  threed: [CAP_3D, CAP_3D_ANIMATION],
  tts: [CAP_TTS],
  sound: [CAP_SOUND_GENERATION],
  transform: [CAP_AUDIO_TRANSFORM],
  diarization: [CAP_DIARIZATION],
}

function readFavourites() {
  try {
    const stored = JSON.parse(localStorage.getItem(FAVOURITES_KEY) || '[]')
    return Array.isArray(stored) ? stored.filter(id => typeof id === 'string') : []
  } catch {
    return []
  }
}

function writeFavourites(ids) {
  try {
    if (ids.length === 0) localStorage.removeItem(FAVOURITES_KEY)
    else localStorage.setItem(FAVOURITES_KEY, JSON.stringify(ids))
  } catch { /* quota or private mode: the star simply does not stick */ }
}

// Which Studio types have a model installed, from one unfiltered read of the
// capabilities list. `loading` is true until the first answer, so a workspace
// does not flash its "no model" note while the list is still on its way.
export function useInstalledTypes() {
  const { models, loading, error, refetch } = useModels()
  const byType = useMemo(() => {
    const out = {}
    for (const type of TYPE_ORDER) {
      out[type] = models
        .filter(m => !m.disabled && m.capabilities?.some(cap => TYPE_CAPABILITIES[type].includes(cap)))
        .map(m => m.id)
    }
    return out
  }, [models])
  return { byType, loading, error, refetch }
}

// What the fold names when it is closed: the settings that hold a value, else
// what is inside.
export function foldSummary(set, all) {
  return (set.length > 0 ? set : all).join(', ')
}

// How one value changed since the take it was started from, in words a person
// would use: a short value shows both sides, a long one just says it was edited.
export function describeChange(label, from, to) {
  const a = String(from ?? '')
  const b = String(to ?? '')
  if (a.length > 24 || b.length > 24) return { key: label, text: label, long: true }
  return { key: label, text: `${label} ${a || 'empty'} to ${b || 'empty'}` }
}

// Everything the shared workspace frame needs from one page: its recent
// results as work items, favourites, the "Use in" targets, the lineage link
// and the "Re-run with edits" comparison.
//
//   type     the Studio key ('images', 'video', ...)
//   entries  the page's own history entries, newest first (live, so a result
//            made a moment ago is already there)
//
// Favourites are the same list the Studio front page keeps, so a star set here
// shows there.
export function useWorkspace({ type, entries }) {
  const { t } = useTranslation('media')
  const navigate = useNavigate()
  const info = TYPE_INFO[type]
  const [favourites, setFavourites] = useState(readFavourites)
  const installed = useInstalledTypes()
  const composeRef = useRef(null)
  const [base, setBase] = useState(null)

  const items = useMemo(
    () => collectWork(info.media ? { [info.media]: entries } : {}, info.media ? [] : entries, { favourites }),
    [entries, favourites, info.media],
  )
  const itemById = useCallback((id) => (id ? items.find(i => i.id === id) || null : null), [items])

  const toggleFavourite = useCallback((id) => {
    setFavourites((prev) => {
      const next = toggleId(prev, id)
      writeFavourites(next)
      return next
    })
  }, [])

  const openLineage = useCallback((item) => navigate(`/app/studio?work=${encodeURIComponent(item.id)}`), [navigate])

  // The steps this kind of result can go on to, each marked with whether the
  // destination can start from it and whether a model for it is installed.
  const steps = useCallback((item) => (NEXT_STEPS[type] || []).map(step => ({
    ...step,
    title: t(`studio.steps.${step.edge}.title`),
    type: step.to,
    missingModel: step.supported && (installed.byType[step.to]?.length || 0) === 0 && !installed.loading,
    blocked: item ? !step.supported : true,
  })), [type, t, installed.byType, installed.loading])

  const sendTo = useCallback((item, step) => {
    if (!item || !step?.supported) return
    const textLike = TYPE_INFO[step.to].input === 'text'
    const prompt = !textLike ? ''
      : step.edge === 'variation' ? item.title
        : t(`studio.steps.${step.edge}.template`, { defaultValue: '' })
    navigate(handoffPath(step.to, { prompt, from: item.id, edge: step.edge }))
  }, [navigate, t])

  // Re-run with edits: the take's values are loaded into the form and kept as
  // the comparison. `begin` is called by the page after it has set its own
  // state; `changes` is computed from what the form holds now.
  const begin = useCallback((values) => {
    setBase(values)
    composeRef.current?.scrollIntoView?.({ block: 'start', behavior: 'smooth' })
  }, [])
  const end = useCallback(() => setBase(null), [])
  const changes = useCallback((current, labels) => {
    if (!base) return []
    return Object.keys(labels)
      .filter(key => String(base[key] ?? '') !== String(current[key] ?? ''))
      .map(key => ({ ...describeChange(labels[key], base[key], current[key]), field: key }))
  }, [base])

  return {
    type, items, itemById, favourites, toggleFavourite, openLineage, steps, sendTo, canTake,
    installed, composeRef, rerunning: !!base, begin, end, changes,
  }
}

// The result a hand-off or a lineage link names, looked up across all stores,
// for the "from ..." line under a result.
export function findParent(item) {
  if (!item?.parentId) return null
  const all = collectWork(readAllMediaHistory(), [])
  return all.find(i => i.id === item.parentId) || null
}
