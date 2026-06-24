import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { modelsApi } from '../utils/api'
import { useRecommendedModels } from '../hooks/useRecommendedModels'

// Static fallback used only when the live gallery / estimates can't be reached
// (offline, trimmed gallery). The hook is the primary, data-driven path; these
// are real gallery names kept as a safety net so onboarding never shows nothing.
const FALLBACK = {
  cpu: [
    { name: 'gemma-4-e2b-it', size: '~1.5 GB' },
    { name: 'lfm2.5-1.2b-instruct', size: '~0.8 GB' },
    { name: 'gemma-4-e4b-it', size: '~3 GB' },
    { name: 'lfm2-1.2b', size: '~0.8 GB' },
  ],
  'gpu-small': [
    { name: 'gemma-4-e4b-it', size: '~3 GB' },
    { name: 'lfm2.5-8b-a1b', size: '~5 GB' },
    { name: 'gemma-4-12b-it-qat-q4_0', size: '~7 GB' },
  ],
  'gpu-large': [
    { name: 'qwen3.6-27b', size: '~16 GB' },
    { name: 'qwen3.6-35b-a3b', size: '~20 GB' },
    { name: 'gemma-4-26b-a4b-it', size: '~16 GB' },
    { name: 'gemma-4-31b-it', size: '~18 GB' },
  ],
}

export default function StarterModels({ addToast, onInstallStarted }) {
  const { t } = useTranslation('home')
  const { recommended, tier, loading } = useRecommendedModels({ count: 4 })
  const [installing, setInstalling] = useState(() => new Set())

  // While the hardware probe + gallery query are in flight, render nothing
  // rather than flashing fallback content that may be replaced a moment later.
  if (loading) return null

  // Prefer live recommendations; fall back to the static list only when the
  // gallery yielded nothing.
  const items = (recommended && recommended.length > 0)
    ? recommended.map(r => ({ name: r.name, size: r.sizeDisplay }))
    : (FALLBACK[tier.id] || FALLBACK.cpu)

  if (items.length === 0) return null

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
        {items.map(c => {
          const busy = installing.has(c.name)
          return (
            <li key={c.name} className="home-starters-item">
              <span className="home-starters-name">{c.name}</span>
              {c.size && <span className="home-starters-size">{c.size}</span>}
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
