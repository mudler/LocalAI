import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { modelsApi } from '../utils/api'
import { useRecommendedModels, isNvfp4Name } from '../hooks/useRecommendedModels'

// Offline CPU suggestions do not claim a measured GPU fit.
const CPU_FALLBACK = [
  { name: 'gemma-4-e2b-it-qat-q4_0', size: '~1.5 GB' },
  { name: 'qwen3.5-4b-claude-4.6-opus-reasoning-distilled', size: '~2.5 GB' },
  { name: 'gemma-4-e4b-it-qat-q4_0', size: '~3 GB' },
  { name: 'lfm2.5-1.2b-instruct', size: '~0.8 GB' },
]

export default function StarterModels({ addToast, onInstallStarted }) {
  const { t } = useTranslation('home')
  const { recommended, tier, loading } = useRecommendedModels({ count: 4 })
  const [installing, setInstalling] = useState(() => new Set())

  // While the hardware probe + gallery query are in flight, render nothing
  // rather than flashing fallback content that may be replaced a moment later.
  if (loading) return null

  // Prefer live recommendations; fall back to the static list only when the
  // gallery yielded nothing on a CPU host. Static GPU picks have no measured
  // fit and must not replace an empty set of fitting recommendations.
  const items = (recommended && recommended.length > 0)
    ? recommended.map(r => ({ name: r.name, size: r.sizeDisplay }))
    : tier.id === 'cpu' ? CPU_FALLBACK : []

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
              {isNvfp4Name(c.name) && <span className="badge badge-info home-starters-badge">NVFP4</span>}
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
