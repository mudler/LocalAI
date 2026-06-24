import { useState, useEffect, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { modelsApi } from '../utils/api'
import { useResources } from '../hooks/useResources'

// Curated, hardware-tiered starter models for the empty-state onboarding. Names
// are real gallery entries (gallery/index.yaml); we intersect them against the
// live gallery at render time so a custom/trimmed gallery degrades gracefully
// (unmatched entries simply don't render).
//
// The guiding rule the maintainer asked for: CPU-only machines should be
// steered to genuinely small models (1-4B, Q4) that stay responsive without a
// GPU. GPU tiers scale the suggestion up with available VRAM.
const SMALL = [
  { name: 'llama-3.2-1b-instruct:q4_k_m', size: '~0.8 GB' },
  { name: 'llama-3.2-3b-instruct:q4_k_m', size: '~2 GB' },
  { name: 'qwen3-1.7b', size: '~1.4 GB' },
  { name: 'gemma-3-1b-it', size: '~0.8 GB' },
]
const MID = [
  { name: 'qwen3-4b', size: '~2.5 GB' },
  { name: 'gemma-3-4b-it', size: '~3 GB' },
  { name: 'llama-3.2-3b-instruct:q4_k_m', size: '~2 GB' },
]
const LARGE = [
  { name: 'meta-llama-3.1-8b-instruct', size: '~5 GB' },
  { name: 'qwen3-4b', size: '~2.5 GB' },
  { name: 'mistral-7b-instruct-v0.3', size: '~4 GB' },
]

const GB = 1024 * 1024 * 1024

// Pick a tier from detected hardware. total_memory is GPU VRAM in bytes (0 when
// CPU-only). Thresholds are deliberately conservative so a suggestion that
// "fits" really does.
function pickTier(resources) {
  const isGpu = resources?.type === 'gpu'
  const vram = resources?.aggregate?.total_memory || 0
  if (!isGpu || vram <= 0) return { id: 'cpu', list: SMALL }
  if (vram < 8 * GB) return { id: 'gpu-small', list: MID }
  return { id: 'gpu-large', list: LARGE }
}

export default function StarterModels({ addToast, onInstallStarted }) {
  const { t } = useTranslation('home')
  const { resources } = useResources()
  const [available, setAvailable] = useState(null) // Set of gallery names, or null while loading
  const [installing, setInstalling] = useState(() => new Set())

  const tier = useMemo(() => pickTier(resources), [resources])
  const candidates = tier.list

  // Verify candidates exist in the live gallery. One search per name (the tier
  // has at most a handful) keeps this resilient to gallery customization.
  useEffect(() => {
    let cancelled = false
    const names = [...new Set(candidates.map(c => c.name))]
    Promise.all(names.map(name =>
      modelsApi.list({ search: name, page: 1 })
        .then(data => (data?.models || []).some(m => (m.name || m.id) === name) ? name : null)
        .catch(() => null)
    )).then(found => {
      if (cancelled) return
      const hits = found.filter(Boolean)
      // If verification yielded nothing (e.g. gallery unreachable), fall back to
      // showing the curated list rather than an empty widget.
      setAvailable(hits.length > 0 ? new Set(hits) : null)
    })
    return () => { cancelled = true }
  }, [candidates])

  const visible = available === null
    ? candidates
    : candidates.filter(c => available.has(c.name))

  if (visible.length === 0) return null

  const install = async (name) => {
    setInstalling(prev => new Set(prev).add(name))
    try {
      await modelsApi.install(name)
      addToast?.(t('starters.installStarted', { model: name }), 'success')
      onInstallStarted?.(name)
    } catch (err) {
      addToast?.(t('starters.installFailed', { message: err.message }), 'error')
      setInstalling(prev => {
        const next = new Set(prev)
        next.delete(name)
        return next
      })
    }
  }

  return (
    <section className="home-starters card">
      <div className="home-starters-head">
        <strong>{t('starters.title')}</strong>
        <span className="home-starters-tier">
          <i className={`fas ${tier.id === 'cpu' ? 'fa-memory' : 'fa-microchip'}`} aria-hidden="true" />
          {t(`starters.tier.${tier.id}`)}
        </span>
      </div>
      <p className="home-starters-sub">
        {tier.id === 'cpu' ? t('starters.cpuNote') : t('starters.gpuNote')}
      </p>
      <ul className="home-starters-list">
        {visible.map(c => {
          const busy = installing.has(c.name)
          return (
            <li key={c.name} className="home-starters-item">
              <span className="home-starters-name">{c.name}</span>
              <span className="home-starters-size">{c.size}</span>
              <button
                type="button"
                className="btn btn-primary btn-sm"
                disabled={busy}
                onClick={() => install(c.name)}
              >
                {busy
                  ? (<><i className="fas fa-spinner fa-spin" aria-hidden="true" /> {t('starters.installing')}</>)
                  : (<><i className="fas fa-download" aria-hidden="true" /> {t('starters.install')}</>)}
              </button>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
