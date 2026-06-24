import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { modelsApi } from '../utils/api'
import { useRecommendedModels, isNvfp4Name } from '../hooks/useRecommendedModels'

const CONTENT_ID = 'rec-models-content'

// "Recommended for your hardware" at the top of Models Explore's zero state. Shares
// the hardware-fit ranking with the empty-state starter widget via
// useRecommendedModels.
//
// It is a section rather than a dismissable card. This is the one thing the
// page has to say about the machine it is running on: hiding it behind a close
// button treated it as an interruption, and a box with its own border read as
// something bolted onto a pane that is otherwise hairline sections. The
// collapse and dismissal state, and their storage keys, are gone with it.
export default function RecommendedModels({ addToast }) {
  const { t } = useTranslation('models')
  const { recommended, tier, loading } = useRecommendedModels({ count: 4 })
  const [installing, setInstalling] = useState(() => new Set())

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

  return (
    // A section, not a card. This sits inside a pane that is otherwise hairline
    // sections, so a bordered, dismissable box read as something bolted on —
    // and the one thing the page has to say about this host is not an
    // interruption to be closed.
    <section className="rec-models" data-testid="recommended-models">
      <div className="zero-pane__shelf-head">
        <h3 className="zero-pane__shelf-title">
          <i className={`fas ${isGpu ? 'fa-microchip' : 'fa-memory'}`} aria-hidden="true" /> {t('recommended.title')}
        </h3>
        <span className="zero-pane__shelf-meta">
          {isGpu ? t('recommended.gpuNote') : t('recommended.cpuNote')}
        </span>
      </div>
      <ul className="lanes lanes--recommended" id={CONTENT_ID}>
        {recommended.map((m, i) => {
          const busy = installing.has(m.name)
          return (
            <li key={m.name} className="lane">
              <span className={`lane__tag${i === 0 ? ' lane__tag--evidence' : ''}`}>
                {i === 0 ? t('recommended.bestFit') : t('recommended.alternative')}
              </span>
              <span className="lane__name lane__name--id">{m.name}</span>
              <span className="lane__num">
                {isNvfp4Name(m.name) && <span className="badge badge-info">NVFP4</span>}
                {m.sizeDisplay}
              </span>
              <span className="lane__num">
                {isGpu && m.vramDisplay ? m.vramDisplay : ''}
              </span>
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
            </li>
          )
        })}
      </ul>
    </section>
  )
}
