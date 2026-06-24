import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { modelsApi } from '../utils/api'
import { useRecommendedModels, isNvfp4Name } from '../hooks/useRecommendedModels'

const DISMISS_KEY = 'localai_rec_models_dismissed'

// "Recommended for your hardware" strip at the top of the Models gallery. Shares
// the hardware-fit ranking with the empty-state starter widget via
// useRecommendedModels, but styled for the gallery page and dismissible (the
// gallery is a repeat-visit surface, so it shouldn't nag).
export default function RecommendedModels({ addToast }) {
  const { t } = useTranslation('models')
  const { recommended, tier, loading } = useRecommendedModels({ count: 4 })
  const [installing, setInstalling] = useState(() => new Set())
  const [dismissed, setDismissed] = useState(() => {
    try { return localStorage.getItem(DISMISS_KEY) === '1' } catch { return false }
  })

  if (loading || dismissed) return null
  if (!recommended || recommended.length === 0) return null

  const dismiss = () => {
    try { localStorage.setItem(DISMISS_KEY, '1') } catch { /* ignore */ }
    setDismissed(true)
  }

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

  return (
    <div className="rec-models card">
      <div className="rec-models-head">
        <div className="rec-models-title">
          <i className={`fas ${isGpu ? 'fa-microchip' : 'fa-memory'}`} aria-hidden="true" />
          <strong>{t('recommended.title')}</strong>
          <span className="rec-models-note">{isGpu ? t('recommended.gpuNote') : t('recommended.cpuNote')}</span>
        </div>
        <button type="button" className="rec-models-dismiss" onClick={dismiss} aria-label={t('recommended.dismiss')} title={t('recommended.dismiss')}>
          <i className="fas fa-times" aria-hidden="true" />
        </button>
      </div>
      <div className="rec-models-grid">
        {recommended.map(m => {
          const busy = installing.has(m.name)
          return (
            <div key={m.name} className="rec-models-item">
              <div className="rec-models-item-name">{m.name}</div>
              <div className="rec-models-item-meta">
                {isNvfp4Name(m.name) && <span className="badge badge-info">NVFP4</span>}
                {m.sizeDisplay && <span>{m.sizeDisplay}</span>}
                {isGpu && m.vramDisplay && (
                  <span className="rec-models-item-fit"><i className="fas fa-microchip" aria-hidden="true" /> {m.vramDisplay}</span>
                )}
              </div>
              <button
                type="button"
                className="btn btn-primary btn-sm"
                disabled={busy}
                onClick={() => install(m.name)}
              >
                {busy
                  ? (<><i className="fas fa-spinner fa-spin" aria-hidden="true" /> {t('recommended.installing')}</>)
                  : (<><i className="fas fa-download" aria-hidden="true" /> {t('recommended.install')}</>)}
              </button>
            </div>
          )
        })}
      </div>
    </div>
  )
}
