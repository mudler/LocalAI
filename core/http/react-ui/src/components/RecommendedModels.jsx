import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { modelsApi } from '../utils/api'
import { useRecommendedModels, isNvfp4Name } from '../hooks/useRecommendedModels'
import Icon from './Icon'

const CONTENT_ID = 'rec-models-content'

// "Best for this machine": a short shelf in the Models page's empty inspector.
// It shares the hardware-fit ranking with the empty-state starter widget via
// useRecommendedModels.
//
// There is no recommendation endpoint. The hook ranks the chat gallery against
// the host's resources and the per-model estimate, so the shelf is only as
// good as those two reads and renders nothing when they give no fit.
//
// It is a section rather than a dismissable card: the one thing the page has
// to say about the machine it runs on is not an interruption to be closed.
// Once something is installed the person has started, so the shelf narrows to
// the best fit and keeps the rest one click away (progressive disclosure).
export default function RecommendedModels({ addToast, installedCount = 0 }) {
  const { t } = useTranslation('models')
  const { recommended, tier, loading } = useRecommendedModels({ count: 4 })
  const [installing, setInstalling] = useState(() => new Set())
  const [expanded, setExpanded] = useState(false)

  if (loading) return null
  if (!recommended || recommended.length === 0) return null

  const install = async (name) => {
    setInstalling(prev => new Set(prev).add(name))
    try {
      await modelsApi.install(name)
      addToast?.(t('recommended.installStarted', { model: name }), 'success')
    } catch (err) {
      addToast?.(t('recommended.installFailed', { message: err.message }), 'error')
      setInstalling(prev => {
        const next = new Set(prev)
        next.delete(name)
        return next
      })
    }
  }

  const isGpu = tier.id !== 'cpu'
  const narrowed = installedCount > 0 && recommended.length > 1
  const visible = narrowed && !expanded ? recommended.slice(0, 1) : recommended

  return (
    <section className="rec-models" data-testid="recommended-models">
      <div className="rec-models__head">
        <h3 className="zero-pane__shelf-title">{t('recommended.title')}</h3>
        <p className="rec-models__note">
          {isGpu ? t('recommended.gpuNote') : t('recommended.cpuNote')}
        </p>
      </div>
      <ul className="lanes lanes--recommended" id={CONTENT_ID}>
        {visible.map((m, i) => {
          const busy = installing.has(m.name)
          const vram = isGpu && m.vramDisplay ? m.vramDisplay : ''
          return (
            <li key={m.name} className="lane">
              <div className="lane__main">
                <span className={`lane__tag${i === 0 ? ' lane__tag--evidence' : ''}`}>
                  {i === 0 ? t('recommended.bestFit') : t('recommended.alternative')}
                </span>
                <span className="lane__name lane__name--id">{m.name}</span>
                <span className="lane__num rec-models__facts">
                  {isNvfp4Name(m.name) && <span className="badge badge-info">NVFP4</span>}
                  <span>{m.sizeDisplay}</span>
                  {vram && <span>{t('recommended.needs', { vram })}</span>}
                </span>
              </div>
              <button
                type="button"
                className={i === 0 ? 'btn btn-primary btn-sm' : 'btn btn-secondary btn-sm'}
                disabled={busy}
                onClick={() => install(m.name)}
              >
                {busy
                  ? (<><Icon name="spinner" spin /> {t('recommended.installing')}</>)
                  : (<><Icon name="download" /> {t('recommended.install')}</>)}
              </button>
            </li>
          )
        })}
      </ul>
      {narrowed && (
        <button
          type="button"
          className="rec-models__more"
          data-testid="recommended-models-toggle"
          aria-expanded={expanded}
          aria-controls={CONTENT_ID}
          onClick={() => setExpanded(v => !v)}
        >
          {expanded
            ? t('recommended.fewer')
            : t('recommended.more', { count: recommended.length - 1 })}
        </button>
      )}
    </section>
  )
}
