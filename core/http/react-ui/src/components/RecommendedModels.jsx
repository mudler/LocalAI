import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { modelsApi } from '../utils/api'
import { formatBytes } from '../utils/format'
import { installRequest, windowLabel } from '../utils/recommendedModels'
import { useRecommendedModels } from '../hooks/useRecommendedModels'
import Icon from './Icon'

const CONTENT_ID = 'rec-models-content'

// "Best for this machine": a short shelf in the Models page's empty inspector.
// It shares the server's recommendation with the empty-state starter widget
// via useRecommendedModels: the server picks the models, the window each gets
// and whether it fits, and the shelf renders nothing when it has no answer.
//
// It is a section rather than a dismissable card: the one thing the page has
// to say about the machine it runs on is not an interruption to be closed.
// Once something is installed the person has started, so the shelf narrows to
// the best fit and keeps the rest one click away (progressive disclosure).
export default function RecommendedModels({ addToast, installedCount = 0 }) {
  const { t } = useTranslation('models')
  const { primary, alternatives, tier, loading } = useRecommendedModels()
  const recommended = primary ? [primary, ...alternatives] : []
  const [installing, setInstalling] = useState(() => new Set())
  const [expanded, setExpanded] = useState(false)

  if (loading) return null
  if (recommended.length === 0) return null

  const install = async (model) => {
    setInstalling(prev => new Set(prev).add(model.id))
    try {
      await modelsApi.install(...installRequest(model))
      addToast?.(t('recommended.installStarted', { model: model.name }), 'success')
    } catch (err) {
      addToast?.(t('recommended.installFailed', { message: err.message }), 'error')
      setInstalling(prev => {
        const next = new Set(prev)
        next.delete(model.id)
        return next
      })
    }
  }

  const isGpu = tier !== 'cpu'
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
          const busy = installing.has(m.id)
          const window = windowLabel(m.context_size)
          return (
            <li key={m.id} className="lane">
              <div className="lane__main">
                <span className={`lane__tag${i === 0 ? ' lane__tag--evidence' : ''}`}>
                  {i === 0 ? t('recommended.bestFit') : t('recommended.alternative')}
                </span>
                <span className="lane__name lane__name--id" title={m.id}>{m.name}</span>
                <span className="lane__num rec-models__facts">
                  {m.size_bytes > 0 && <span>{formatBytes(m.size_bytes)}</span>}
                  {window && <span>{t('recommended.window', { window })}</span>}
                  {m.memory_bytes > 0 && <span>{t('recommended.memory', { memory: formatBytes(m.memory_bytes) })}</span>}
                </span>
              </div>
              <button
                type="button"
                className={i === 0 ? 'btn btn-primary btn-sm' : 'btn btn-secondary btn-sm'}
                disabled={busy}
                onClick={() => install(m)}
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
