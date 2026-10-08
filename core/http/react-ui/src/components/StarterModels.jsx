import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { modelsApi } from '../utils/api'
import { useRecommendedModels, isNvfp4Name } from '../hooks/useRecommendedModels'
import { useOperations } from '../hooks/useOperations'
import { fillStyle } from './home/memory'
import Icon from './Icon'

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
  const { operations } = useOperations()
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
    <section className="home-starters" aria-label={t('starters.title')}>
      <div className="home-starters-head">
        <span className="home-starters-title">{t('starters.title')}</span>
        <span className="home-starters-tier">
          <Icon name={tier.id === 'cpu' ? 'memory' : 'cpu'} />
          {t(`starters.tier.${tier.id}`)}
        </span>
      </div>
      <p className="home-starters-sub">
        {tier.id === 'cpu' ? t('starters.cpuNote') : t('starters.gpuNote')}
      </p>
      <ul className="home-starters-list">
        {items.map((c, i) => {
          // The install shows up in the operations list once the server has
          // taken it. Its progress is the only progress there is to show.
          const op = operations.find(o => o.name === c.name && !o.isBackend && !o.isDeletion)
          const busy = installing.has(c.name) || !!op
          return (
            <li key={c.name} className="home-starters-item">
              <div className="home-starters-info">
                <code className="home-starters-name" title={c.name}>{c.name}</code>
                <small className="home-starters-meta">
                  {c.size}
                  {isNvfp4Name(c.name) && <span className="home-starters-badge">NVFP4</span>}
                </small>
              </div>
              <button
                type="button"
                className={i === 0 ? 'home-primary home-primary--sm' : 'home-secondary home-secondary--sm'}
                disabled={busy}
                aria-busy={busy || undefined}
                onClick={() => install(c.name)}
              >
                {busy
                  ? (<><Icon name="spinner" spin /> {t('starters.installing')}</>)
                  : (<><Icon name="download" /> {t('starters.install')}</>)}
              </button>
              {op && op.progress > 0 && (
                <span
                  className="home-bar home-bar--thin home-starters-progress"
                  role="progressbar"
                  aria-valuenow={Math.round(op.progress)}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-label={t('strip.progress', { name: c.name })}
                >
                  <span className="home-bar__fill" style={fillStyle(op.progress)} />
                </span>
              )}
            </li>
          )
        })}
      </ul>
    </section>
  )
}
