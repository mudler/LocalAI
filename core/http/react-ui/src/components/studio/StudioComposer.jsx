import { useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
// eslint-disable-next-line no-unused-vars
import InstallNote from './InstallNote'
import { useResources } from '../../hooks/useResources'
import { useModelNeed } from '../../hooks/useModelSuggestion'
import { modelBudget } from '../../utils/modelBudget'
import { fitFor, gbLabel, gbNumber } from '../../utils/modelLedger'
import {
  NEXT_STEPS, TYPE_ICON, TYPE_INFO, TYPE_ORDER, handoffPath, suggestType,
} from '../../utils/studioWork'

// The edge a hand-off carries when the chosen source feeds this type.
const EDGE_FOR = { images: 'variation', video: 'animate', threed: 'to-3d', transform: 'transform', diarization: 'diarize' }

// Which results can start which type.
function eligibleSources(type, items) {
  const wanted = TYPE_INFO[type].source
  if (!wanted) return []
  const from = Object.keys(NEXT_STEPS).filter(k => NEXT_STEPS[k].some(s => s.supported && s.to === type && s.edge === EDGE_FOR[type]))
  return items.filter(i => from.includes(i.type) && i.url).slice(0, 8)
}

function isTyping(el) {
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}

// "What do you want to make?"
//
// One box and a row of types. The types are the seven workspaces; a type with a
// model installed is a solid chip and a type without one is dashed, and only
// picking the dashed one opens what it needs. Typing suggests a type from the
// words and never switches by itself. Generate does not run anything here: it
// opens the workspace with the prompt and the options the workspace accepts.
export default function StudioComposer({
  draft, setDraft, modalities, items, onHandoff, onModelsChanged, defaultModel,
}) {
  const { t } = useTranslation('media')
  const textRef = useRef(null)
  const { resources } = useResources(30000)

  const types = useMemo(() => TYPE_ORDER.filter(k => modalities.some(m => m.key === k)), [modalities])
  const infoFor = (key) => modalities.find(m => m.key === key)
  // Until one is picked, open on the first type that has a model, so a machine
  // that can only read aloud does not open on a type it cannot run.
  const firstReady = types.find(k => (infoFor(k)?.installed.length || 0) > 0)
  const type = types.includes(draft.type) ? draft.type : (firstReady || types[0])
  const modality = infoFor(type)
  const info = TYPE_INFO[type]
  const installed = modality?.installed || []
  const missing = installed.length === 0
  const model = installed.includes(draft.models[type]) ? draft.models[type] : (defaultModel(type, installed) || '')
  const label = (key) => t(`studio.tabs.${key}`)

  const guess = useMemo(() => {
    const g = suggestType(draft.text)
    return g && g !== type && types.includes(g) ? g : null
  }, [draft.text, type, types])

  const sources = useMemo(() => eligibleSources(type, items), [type, items])
  const source = sources.find(s => s.id === draft.sourceId) || null

  const need = useModelNeed(missing ? '' : model)
  const fit = need ? fitFor(need, modelBudget(resources)) : null

  const textLike = info.input === 'text'
  const canGo = !missing && (!textLike || draft.text.trim().length > 0)
  const why = missing ? t('studio.composer.whyModel', { type: label(type) })
    : (textLike && !draft.text.trim()) ? t('studio.composer.whyPrompt') : ''

  const set = (patch) => setDraft(d => ({ ...d, ...patch }))
  const pickType = (key) => setDraft(d => ({ ...d, type: key, sourceId: '' }))

  const go = () => {
    if (!canGo) return
    onHandoff(handoffPath(type, {
      prompt: textLike ? draft.text : '',
      model,
      size: info.sizes ? (draft.sizes[type] || info.defaultSize) : '',
      count: info.maxCount ? draft.count : 0,
      from: source?.id || '',
      edge: source ? EDGE_FOR[type] : '',
    }))
  }

  // Keys: "/" to the prompt, Alt+1..7 to a type, Ctrl+Enter to generate, Alt+Enter
  // to take the suggestion. Held in a ref so one listener sees the latest state.
  const keyState = useRef({})
  keyState.current = { go, guess, types, textLike }
  useEffect(() => {
    const onKey = (e) => {
      const s = keyState.current
      if (e.key === '/' && !e.ctrlKey && !e.metaKey && !e.altKey && !isTyping(e.target)) {
        e.preventDefault()
        textRef.current?.focus()
        return
      }
      if (e.altKey && !e.ctrlKey && !e.metaKey && /^Digit[1-7]$/.test(e.code)) {
        const key = s.types[Number(e.code.slice(5)) - 1]
        if (key) { e.preventDefault(); pickType(key) }
        return
      }
      if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') { e.preventDefault(); s.go(); return }
      if (e.altKey && e.key === 'Enter' && s.guess) { e.preventDefault(); pickType(s.guess) }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // pickType only calls setDraft, which is stable.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // The box grows with what is typed, up to a limit.
  useEffect(() => {
    const el = textRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(220, el.scrollHeight)}px`
  }, [draft.text, type])

  const starters = (t(`studio.starters.${type}`, { returnObjects: true }))
  const starterList = Array.isArray(starters) ? starters : []
  const guessMissing = guess && (infoFor(guess)?.installed.length || 0) === 0
  const noneInstalled = modalities.every(m => m.installed.length === 0)

  return (
    <section className="studio-composer" aria-labelledby="studio-composer-title" data-testid="studio-composer" data-type={type}>
      <div className="studio-composer__head">
        <h2 id="studio-composer-title">{t('studio.composer.title')}</h2>
        <span>{t('studio.composer.lede')}</span>
      </div>

      <div className="studio-types" role="group" aria-label={t('studio.composer.types')}>
        {types.map((key, i) => {
          const isMissing = (infoFor(key)?.installed.length || 0) === 0
          return (
            <button
              key={key}
              type="button"
              className="dk-chip studio-type"
              aria-pressed={type === key}
              data-type={key}
              data-missing={isMissing || undefined}
              title={isMissing ? t('studio.composer.chipMissing', { type: label(key) }) : t('studio.composer.chipKey', { n: i + 1 })}
              onClick={() => pickType(key)}
            >
              <Icon name={TYPE_ICON[key]} />
              {label(key)}
            </button>
          )
        })}
      </div>

      <div className="studio-composer__box">
        {textLike ? (
          <textarea
            ref={textRef}
            className="studio-prompt"
            rows={2}
            value={draft.text}
            spellCheck={false}
            aria-label={t('studio.composer.prompt')}
            placeholder={t(`studio.composer.placeholder.${type}`)}
            onChange={(e) => set({ text: e.target.value })}
          />
        ) : (
          <p className="studio-composer__file" data-testid="studio-file-lead">{t(`studio.composer.fileLead.${type}`)}</p>
        )}

        <div className="studio-hint" aria-live="polite" data-testid="studio-hint">
          {guess && !guessMissing && (
            <>
              <span>{t('studio.composer.soundsLike', { type: label(guess) })}</span>
              <button type="button" className="studio-link" data-testid="studio-switch" onClick={() => pickType(guess)}>{t('studio.composer.switchTo', { type: label(guess) })}</button>
              <kbd className="dk-kbd">Alt Enter</kbd>
            </>
          )}
          {guess && guessMissing && (
            <>
              <span>{t('studio.composer.soundsLikeMissing', { type: label(guess) })}</span>
              <button type="button" className="studio-link" data-testid="studio-switch" onClick={() => pickType(guess)}>{t('studio.composer.seeNeeds')}</button>
            </>
          )}
        </div>
      </div>

      {textLike && !draft.text && starterList.length > 0 && (
        <div className="studio-starters" data-testid="studio-starters">
          <span className="studio-eyebrow">{t('studio.composer.starters')}</span>
          {starterList.map(s => (
            <button key={s.label} type="button" className="dk-chip dk-chip--sm studio-starter" onClick={() => { set({ text: s.text }); textRef.current?.focus() }}>{s.label}</button>
          ))}
        </div>
      )}

      {missing && <InstallNote key={type} type={type} typeLabel={label(type)} onChanged={onModelsChanged} className={noneInstalled ? 'studio-note--first' : ''} />}
      {missing && noneInstalled && <p className="studio-firstrun" data-testid="studio-first-run">{t('studio.composer.firstRun')}</p>}

      <div className="studio-options">
        <span className="dk-select-wrap studio-opt">
          <select
            className="dk-select dk-input--mono"
            aria-label={t('studio.composer.model')}
            value={model}
            disabled={missing}
            data-testid="studio-model"
            onChange={(e) => setDraft(d => ({ ...d, models: { ...d.models, [type]: e.target.value } }))}
          >
            {missing ? <option value="">{t('studio.composer.noModel')}</option> : installed.map(id => <option key={id} value={id}>{id}</option>)}
          </select>
        </span>

        {info.sizes && (
          <span className="dk-select-wrap studio-opt">
            <select
              className="dk-select dk-input--mono"
              aria-label={t('studio.composer.size')}
              value={draft.sizes[type] || info.defaultSize}
              data-testid="studio-size"
              onChange={(e) => setDraft(d => ({ ...d, sizes: { ...d.sizes, [type]: e.target.value } }))}
            >
              {info.sizes.map(s => <option key={s} value={s}>{s.replace('x', ' × ')}</option>)}
            </select>
          </span>
        )}

        {info.maxCount && (
          <span className="dk-select-wrap studio-opt">
            <select
              className="dk-select"
              aria-label={t('studio.composer.count')}
              value={draft.count}
              data-testid="studio-count"
              onChange={(e) => set({ count: Number(e.target.value) })}
            >
              {Array.from({ length: info.maxCount }, (_, i) => i + 1).map(n => <option key={n} value={n}>{t('studio.composer.countOption', { count: n })}</option>)}
            </select>
          </span>
        )}

        {info.source && (
          <span className="dk-select-wrap studio-opt studio-opt--source">
            <select
              className="dk-select"
              aria-label={t('studio.composer.startFrom')}
              value={draft.sourceId}
              data-testid="studio-source"
              onChange={(e) => set({ sourceId: e.target.value })}
            >
              <option value="">{info.needsSource ? t('studio.composer.sourceOnPage') : t('studio.composer.sourceNone')}</option>
              {sources.map(s => <option key={s.id} value={s.id}>{(s.title || t(`studio.work.untitled.${s.type}`)).slice(0, 48)}</option>)}
            </select>
          </span>
        )}

        {fit && (
          <span className="studio-fit" data-fit={fit.state} data-testid="studio-fit">
            {fit.state === 'fits' && t('studio.composer.fit.fits', { need: gbLabel(fit.need), amount: gbNumber(fit.amount) })}
            {fit.state === 'spill' && t('studio.composer.fit.spill', { need: gbLabel(fit.need), amount: gbNumber(fit.amount) })}
            {fit.state === 'over' && t('studio.composer.fit.over', { need: gbLabel(fit.need), amount: gbNumber(fit.amount) })}
          </span>
        )}

        <span className="studio-go">
          {why && <span className="studio-go__why" data-testid="studio-why">{why}</span>}
          <button type="button" className="dk-btn dk-btn--primary" disabled={!canGo} onClick={go} data-testid="studio-generate">
            <Icon name="sparkles" />
            {textLike ? t('studio.composer.generate') : t('studio.composer.openType', { type: label(type) })}
            <kbd className="dk-kbd">Ctrl Enter</kbd>
          </button>
        </span>
      </div>
    </section>
  )
}
