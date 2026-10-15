import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useResources } from '../../../hooks/useResources'
import { DEFAULT_CONTEXT, usePlacementEstimate } from '../../../hooks/usePlacementEstimate'
import { FIT_LIMIT, cssVars, gbLabel } from '../../../utils/modelLedger'
import {
  ALL_LAYERS, CONTEXT_PRESETS, contextLabel, devicesFrom, estimateLayers, formatSplit, isAllLayers,
  parseSplit, placementFit, placementMode, searchLayers, shares, splitByFree,
} from '../../../utils/placement'
import Icon from '../../Icon'
import './placement.css'
// eslint-disable-next-line no-unused-vars
import MemoryBar from '../MemoryBar'

// The Placement section: where a model runs and how much of it goes to the GPU.
// It edits three keys of the model's configuration (gpu_layers, tensor_split,
// main_gpu) and reads a fourth (context_size) because the KV cache grows with
// it. It owns no config state: the model editor and the model page each hold
// the values and hand them in.
//
//   model     the installed model's name; the estimate is asked about it.
//   values    { gpu_layers, tensor_split, main_gpu, context_size } as the
//             editor holds them. An absent key is unset.
//   onChange  (path, value) => void. A value of undefined unsets the key.
//
// Everything on screen is worked out from three things the server returns: the
// device list in /api/resources, and the vram-estimate figure for the choice.
// The estimate carries one number per request, so the per-device bars split it
// by tensor_split, and the part that grows with context comes from a second
// reading at twice the context. Nothing is guessed beyond that.
export default function PlacementSection({ model, values, onChange, headerAction, testId = 'placement' }) {
  const { t } = useTranslation('models')
  const { resources, loading: resourcesLoading, error: resourcesError } = useResources()
  const devices = useMemo(() => devicesFrom(resources), [resources])

  const mode = placementMode(values.gpu_layers)
  const layers = estimateLayers(mode, values.gpu_layers)
  const contextValue = Number(values.context_size) > 0 ? Number(values.context_size) : 0
  const estimate = usePlacementEstimate({
    model,
    contextSize: contextValue || undefined,
    layers,
    enabled: !resourcesLoading,
  })

  const gpus = devices.gpus
  const hasGpu = devices.kind === 'gpu'
  const multi = hasGpu && gpus.length > 1 && mode !== 'cpu'
  const split = parseSplit(values.tensor_split)
  const layerCount = estimate.data?.layerCount || 0

  // The last "Fit it for me", so it can be undone. Cleared by any other edit.
  const [applied, setApplied] = useState(null)
  const [fitting, setFitting] = useState(false)
  const [fitNote, setFitNote] = useState(null)

  const edit = (path, value) => {
    setApplied(null)
    setFitNote(null)
    onChange(path, value)
  }

  const choose = (next) => {
    if (next === mode) return
    if (next === 'cpu') edit('gpu_layers', 0)
    else if (next === 'auto') edit('gpu_layers', undefined)
    else edit('gpu_layers', layerCount > 0 ? layerCount : ALL_LAYERS)
  }

  // --- the layer count field -------------------------------------------
  const allLayers = isAllLayers(values.gpu_layers)
  const shownLayers = allLayers ? t('placement.layersAll') : String(values.gpu_layers ?? '')
  const [layerText, setLayerText] = useState(shownLayers)
  const typing = useRef(false)
  useEffect(() => {
    if (!typing.current) setLayerText(shownLayers)
  }, [shownLayers])

  const commitLayers = (text) => {
    const trimmed = text.trim().toLowerCase()
    if (trimmed === '') return
    if (trimmed === 'all' || trimmed === t('placement.layersAll').toLowerCase()) { edit('gpu_layers', ALL_LAYERS); return }
    if (!/^\d+$/.test(trimmed)) return
    const n = Number(trimmed)
    // Zero is a real choice, but it belongs to CPU only; keep the card honest.
    edit('gpu_layers', n)
  }

  const [contextText, setContextText] = useState(contextValue ? String(contextValue) : '')
  const typingContext = useRef(false)
  useEffect(() => {
    if (!typingContext.current) setContextText(contextValue ? String(contextValue) : '')
  }, [contextValue])

  // --- fit state -------------------------------------------------------
  const fit = placementFit({
    mode,
    gpuBytes: estimate.data?.gpuBytes,
    allBytes: estimate.data?.allBytes,
    devices,
  })
  const ready = estimate.status === 'ready'
  const effectiveContext = contextValue || DEFAULT_CONTEXT
  const need = gbLabel
  const copy = (key, extra = {}) => t(`placement.fit.${key}`, {
    need: need(fit.gpuNeed || fit.cpuNeed),
    gpu: need(fit.gpuNeed),
    cpu: need(fit.cpuNeed),
    allNeed: need(estimate.data?.allBytes || 0),
    free: need(fit.gpuFree * FIT_LIMIT),
    ramFree: need(fit.ramFree || 0),
    context: contextLabel(effectiveContext),
    ...extra,
  })

  const tone = { fits: 'ok', cpu: 'ok', nogpu: 'ok', spill: 'warn', trimmed: 'warn', toomany: 'error', 'cpu-over': 'error', 'nogpu-over': 'error', 'spill-over': 'error', 'trimmed-over': 'error' }[fit.state]

  // --- bars --------------------------------------------------------------
  const bars = []
  if (ready && devices.kind !== 'cluster') {
    const kvShare = estimate.data.parts && estimate.data.gpuBytes > 0
      ? estimate.data.parts.kv / estimate.data.gpuBytes
      : null
    if (hasGpu && mode !== 'cpu') {
      const parts = shares(gpus.length, split)
      gpus.forEach((g, i) => {
        const onDevice = fit.gpuNeed * parts[i]
        const kv = kvShare === null ? 0 : onDevice * kvShare
        bars.push({
          key: `gpu-${g.index}`,
          label: gpus.length > 1 ? t('placement.bar.gpuNamed', { index: g.index, name: g.name }) : t('placement.bar.gpu', { name: g.name }),
          capacity: g.total,
          free: g.free - onDevice,
          segments: [
            { key: 'other', label: t('placement.legend.other'), bytes: g.used, tone: 'other' },
            { key: 'model', label: kvShare === null ? t('placement.legend.model') : t('placement.legend.weights'), bytes: onDevice - kv, tone: 'model' },
            { key: 'kv', label: t('placement.legend.kv'), bytes: kv, tone: 'kv' },
          ],
        })
      })
    }
    if (devices.ram && (fit.cpuNeed > 0 || mode === 'cpu' || !hasGpu)) {
      bars.push({
        key: 'ram',
        label: t('placement.bar.ram'),
        capacity: devices.ram.total,
        free: devices.ram.free - fit.cpuNeed,
        segments: [
          { key: 'other', label: t('placement.legend.other'), bytes: devices.ram.total - devices.ram.free, tone: 'other' },
          { key: 'model', label: t('placement.legend.model'), bytes: fit.cpuNeed, tone: 'model' },
        ],
      })
    }
  }

  // --- Fit it for me -------------------------------------------------------
  const canFit = hasGpu && estimate.status !== 'unavailable'
  const fitForMe = async () => {
    setFitting(true)
    setFitNote(null)
    const previous = { gpu_layers: values.gpu_layers }
    const limit = gpus.reduce((sum, g) => sum + g.free, 0) * FIT_LIMIT
    const result = await searchLayers(estimate.bytesAt, limit)
    setFitting(false)
    if (result.kind === 'all') {
      onChange('gpu_layers', ALL_LAYERS)
      setApplied({ previous, text: t('placement.fitApplied.all') })
    } else if (result.kind === 'layers') {
      onChange('gpu_layers', result.layers)
      setApplied({ previous, text: t('placement.fitApplied.layers', { count: result.layers }) })
    } else {
      setApplied(null)
      setFitNote(t(`placement.fitFailed.${result.kind}`, { free: gbLabel(limit), need: gbLabel(result.bytes || 0) }))
    }
  }
  const undo = () => {
    if (!applied) return
    onChange('gpu_layers', applied.previous.gpu_layers)
    setApplied(null)
  }

  const noGpuReason = devices.kind === 'none' ? t('placement.noGpu') : null
  const modeWrites = {
    cpu: 'gpu_layers: 0',
    auto: t('placement.modes.autoWrites'),
    custom: `gpu_layers: ${allLayers ? ALL_LAYERS : (Number.isFinite(Number(values.gpu_layers)) && mode === 'custom' ? Number(values.gpu_layers) : 'N')}`,
  }
  const modeOptions = ['cpu', 'auto', 'custom']

  const splitPercent = split && split.length === gpus.length
    ? split.map(p => Math.round((p / split.reduce((a, b) => a + b, 0)) * 100))
    : shares(gpus.length, null).map(p => Math.round(p * 100))
  const setSplitPercent = (index, percent) => {
    const clamped = Math.max(0, Math.min(100, Number.isFinite(percent) ? percent : 0))
    if (gpus.length === 2) {
      const pair = index === 0 ? [clamped, 100 - clamped] : [100 - clamped, clamped]
      edit('tensor_split', formatSplit(pair))
      return
    }
    const next = [...splitPercent]
    next[index] = clamped
    if (next.every(p => p === 0)) return
    edit('tensor_split', formatSplit(next))
  }

  return (
    <section className="placement" data-testid={testId} aria-labelledby={`${testId}-title`} aria-busy={estimate.refreshing || undefined}>
      <div className="placement__head">
        <h3 className="placement__title" id={`${testId}-title`}>{t('placement.title')}</h3>
        <span className="placement__keys dk-mono">gpu_layers, tensor_split, main_gpu</span>
        {headerAction && <span className="placement__head-action">{headerAction}</span>}
      </div>

      <div className="placement__block">
        <span className="dk-eyebrow" id={`${testId}-run`}>{t('placement.runOn')}</span>
        <div className="placement__modes" role="radiogroup" aria-labelledby={`${testId}-run`}>
          {modeOptions.map(option => {
            const disabled = option !== 'cpu' && devices.kind === 'none'
            return (
              <button
                key={option}
                type="button"
                role="radio"
                aria-checked={mode === option}
                disabled={disabled}
                className="placement__mode"
                data-testid={`placement-mode-${option}`}
                onClick={() => choose(option)}
              >
                <span className="placement__mode-name">{t(`placement.modes.${option}`)}</span>
                <span className="placement__mode-writes dk-mono">{modeWrites[option]}</span>
              </button>
            )
          })}
        </div>
        {noGpuReason && <p className="dk-hint placement__hint" data-testid="placement-nogpu">{noGpuReason}</p>}
        {mode === 'auto' && !noGpuReason && (
          <p className="dk-hint placement__hint" data-testid="placement-auto-hint">{t('placement.autoHint')}</p>
        )}
        {resourcesError && !resources && (
          <p className="dk-hint placement__hint" role="status">{t('placement.noMachine')}</p>
        )}
      </div>

      {mode === 'custom' && (
        <div className="placement__block" data-testid="placement-custom">
          <div className="placement__row">
            <label className="dk-label" htmlFor={`${testId}-layers`}>{t('placement.layers')}</label>
            <input
              id={`${testId}-layers`}
              className="dk-input dk-input--mono placement__number"
              type="text"
              inputMode="numeric"
              autoComplete="off"
              data-testid="placement-layers"
              value={layerText}
              onFocus={() => { typing.current = true }}
              onChange={e => { setLayerText(e.target.value); commitLayers(e.target.value) }}
              onBlur={() => { typing.current = false; setLayerText(shownLayers) }}
            />
            <button
              type="button"
              className="dk-btn dk-btn--secondary dk-btn--sm"
              data-testid="placement-all-layers"
              aria-pressed={allLayers}
              onClick={() => edit('gpu_layers', ALL_LAYERS)}
            >
              {t('placement.allLayers')}
            </button>
          </div>
          {layerCount > 0 ? (
            <div className="dk-range-wrap placement__slider">
              <input
                className="dk-range"
                type="range"
                min={0}
                max={layerCount}
                step={1}
                data-testid="placement-slider"
                aria-label={t('placement.layers')}
                aria-valuetext={t('placement.sliderValue', { count: allLayers ? layerCount : Math.min(Number(values.gpu_layers) || 0, layerCount), total: layerCount })}
                value={allLayers ? layerCount : Math.min(Number(values.gpu_layers) || 0, layerCount)}
                style={cssVars({ '--dk-fill': `${((allLayers ? layerCount : Math.min(Number(values.gpu_layers) || 0, layerCount)) / layerCount) * 100}%` })}
                onChange={e => edit('gpu_layers', Number(e.target.value))}
              />
              <output className="dk-range-value">
                {t('placement.sliderValue', { count: allLayers ? layerCount : Math.min(Number(values.gpu_layers) || 0, layerCount), total: layerCount })}
              </output>
            </div>
          ) : (
            <p className="dk-hint placement__hint" data-testid="placement-no-slider">{t('placement.noSlider', { all: ALL_LAYERS })}</p>
          )}
        </div>
      )}

      <div className="placement__block">
        <div className="placement__row placement__row--wrap">
          <span className="dk-label" id={`${testId}-context`}>{t('placement.context')}</span>
          <div className="dk-segmented" role="group" aria-labelledby={`${testId}-context`}>
            {CONTEXT_PRESETS.map(size => (
              <button
                key={size}
                type="button"
                className="dk-seg"
                aria-pressed={contextValue === size}
                data-testid={`placement-context-${size}`}
                onClick={() => edit('context_size', size)}
              >
                {contextLabel(size)}
              </button>
            ))}
          </div>
          <input
            className="dk-input dk-input--mono placement__number"
            type="text"
            inputMode="numeric"
            autoComplete="off"
            aria-label={t('placement.contextTokens')}
            data-testid="placement-context-input"
            placeholder={String(DEFAULT_CONTEXT)}
            value={contextText}
            onFocus={() => { typingContext.current = true }}
            onChange={e => {
              const text = e.target.value
              setContextText(text)
              if (/^\d+$/.test(text.trim()) && Number(text) > 0) edit('context_size', Number(text))
            }}
            onBlur={() => { typingContext.current = false; setContextText(contextValue ? String(contextValue) : '') }}
          />
        </div>
        <p className="dk-hint placement__hint">
          {contextValue ? t('placement.contextHint') : t('placement.contextUnset', { context: contextLabel(DEFAULT_CONTEXT) })}
        </p>
      </div>

      {multi && (
        <div className="placement__block" data-testid="placement-split">
          <div className="placement__row placement__row--wrap">
            <span className="dk-label" id={`${testId}-split`}>{t('placement.split.title')}</span>
            <button
              type="button"
              className="dk-btn dk-btn--secondary dk-btn--sm"
              data-testid="placement-split-free"
              onClick={() => edit('tensor_split', formatSplit(splitByFree(gpus)))}
            >
              {t('placement.split.byFree')}
            </button>
          </div>
          <div className="placement__split" role="group" aria-labelledby={`${testId}-split`}>
            {gpus.map((g, i) => (
              <label key={g.index} className="placement__split-item">
                <span className="dk-small">{t('placement.split.share', { index: g.index })}</span>
                <span className="placement__split-input">
                  <input
                    className="dk-input dk-input--mono placement__number placement__number--small"
                    type="text"
                    inputMode="numeric"
                    autoComplete="off"
                    data-testid={`placement-split-${g.index}`}
                    value={splitPercent[i]}
                    onChange={e => { if (/^\d{0,3}$/.test(e.target.value)) setSplitPercent(i, Number(e.target.value || 0)) }}
                  />
                  <span className="dk-small dk-muted">%</span>
                </span>
              </label>
            ))}
          </div>
          {gpus.length === 2 && (
            <div className="dk-range-wrap placement__slider">
              <input
                className="dk-range"
                type="range"
                min={0}
                max={100}
                step={1}
                data-testid="placement-split-slider"
                aria-label={t('placement.split.sliderLabel')}
                value={splitPercent[0]}
                style={cssVars({ '--dk-fill': `${splitPercent[0]}%` })}
                onChange={e => setSplitPercent(0, Number(e.target.value))}
              />
              <output className="dk-range-value">{splitPercent[0]} / {splitPercent[1]}</output>
            </div>
          )}
          <div className="placement__row placement__row--wrap">
            <span className="dk-label" id={`${testId}-main`}>{t('placement.split.main')}</span>
            <div className="dk-segmented" role="group" aria-labelledby={`${testId}-main`}>
              {gpus.map(g => (
                <button
                  key={g.index}
                  type="button"
                  className="dk-seg"
                  aria-pressed={String(values.main_gpu ?? '') === String(g.index)}
                  data-testid={`placement-main-${g.index}`}
                  onClick={() => edit('main_gpu', String(g.index))}
                >
                  {t('placement.split.gpu', { index: g.index })}
                </button>
              ))}
            </div>
          </div>
        </div>
      )}

      <div className="placement__block placement__memory" data-testid="placement-memory">
        <span className="dk-eyebrow">{t('placement.memory')}</span>
        {devices.kind === 'cluster' ? (
          <p className="dk-hint placement__hint" data-testid="placement-cluster">{t('placement.cluster')}</p>
        ) : estimate.status === 'loading' || (resourcesLoading && !resources) ? (
          <div className="placement__skeleton" data-testid="placement-loading" role="status" aria-label={t('placement.estimateLoading')}>
            <span className="dk-skeleton dk-skeleton--line placement__skeleton-bar" />
            <span className="dk-skeleton dk-skeleton--line placement__skeleton-bar" />
          </div>
        ) : estimate.status === 'unavailable' ? (
          <div className="placement__unavailable" data-testid="placement-unavailable" role="status">
            <Icon name="info" />
            <div>
              <strong>{t('placement.unavailable.title')}</strong>
              <p>{t('placement.unavailable.text')}</p>
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={estimate.retry} data-testid="placement-retry">
                <Icon name="refresh" /> {t('placement.unavailable.retry')}
              </button>
            </div>
          </div>
        ) : (
          <>
            {estimate.data?.sizeBytes > 0 && (
              <p className="dk-hint placement__hint">
                {t('placement.weights', { size: gbLabel(estimate.data.sizeBytes), context: contextLabel(effectiveContext) })}
              </p>
            )}
            {bars.map(bar => (
              <div key={bar.key} className="placement__bar" data-testid={`placement-bar-${bar.key}`}>
                <div className="placement__bar-head">
                  <span className="placement__bar-name">{bar.label}</span>
                  <span className={`placement__bar-free${bar.free < 0 ? ' placement__bar-free--over' : ''}`}>
                    {bar.free < 0
                      ? t('placement.bar.over', { amount: gbLabel(-bar.free) })
                      : t('placement.bar.freeAfter', { amount: gbLabel(bar.free) })}
                  </span>
                </div>
                <MemoryBar
                  capacity={bar.capacity}
                  segments={bar.segments}
                  legend={false}
                  ariaLabel={t('placement.bar.aria', {
                    name: bar.label,
                    parts: bar.segments.filter(s => s.bytes > 0).map(s => `${s.label} ${gbLabel(s.bytes)}`).join(', '),
                    capacity: gbLabel(bar.capacity),
                  })}
                />
              </div>
            ))}
            {bars.length > 0 && (
              <ul className="dk-meter-legend placement__legend">
                {[...new Map(bars.flatMap(b => b.segments).filter(s => s.bytes > 0).map(s => [s.tone, s])).values()].map(s => (
                  <li key={s.tone}>
                    <span className={`dk-swatch memorybar__swatch memorybar__swatch--${s.tone}`} aria-hidden="true" />
                    {s.label}
                  </li>
                ))}
              </ul>
            )}
            {bars.length === 0 && ready && <p className="dk-hint placement__hint">{t('placement.noBars')}</p>}
          </>
        )}
      </div>

      {ready && fit.state !== 'unknown' && devices.kind !== 'cluster' && (
        <div className="placement__verdict" data-state={fit.state} data-tone={tone} data-testid="placement-verdict" role="status">
          <Icon name={tone === 'ok' ? 'check-circle' : tone === 'warn' ? 'alert-circle' : 'close-circle'} />
          <div className="placement__verdict-text">
            <strong>{t(`placement.fit.${fit.state}.title`)}</strong>
            <span>{copy(`${fit.state}.text`)}</span>
          </div>
        </div>
      )}

      {(canFit || applied || fitNote) && devices.kind !== 'cluster' && (
        <div className="placement__fitrow">
          {canFit && (
            <button
              type="button"
              className="dk-btn dk-btn--secondary dk-btn--sm"
              data-testid="placement-fit"
              disabled={fitting || estimate.status !== 'ready'}
              aria-busy={fitting || undefined}
              onClick={fitForMe}
            >
              <Icon name={fitting ? 'spinner' : 'sparkles'} spin={fitting} /> {t('placement.fitForMe')}
            </button>
          )}
          {applied && (
            <p className="placement__applied" data-testid="placement-applied" role="status">
              {applied.text}{' '}
              <button type="button" className="dk-link placement__undo" data-testid="placement-undo" onClick={undo}>{t('placement.undo')}</button>
            </p>
          )}
          {fitNote && <p className="placement__applied" data-testid="placement-fit-note" role="status">{fitNote}</p>}
        </div>
      )}
    </section>
  )
}
