import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { modelsApi } from '../utils/api'
import { useRecommendedModels, isNvfp4Name } from '../hooks/useRecommendedModels'

// Page-scoped storage keys, matching the Models page convention
// (localai-models-*). The underscore key is the pre-rename one: reading it
// keeps an existing dismissal honoured instead of resurrecting the panel for
// everyone who already closed it.
const DISMISS_KEY = 'localai-models-recommended-dismissed'
const LEGACY_DISMISS_KEY = 'localai_rec_models_dismissed'
const COLLAPSE_KEY = 'localai-models-recommended-collapsed'
const CONTENT_ID = 'rec-models-content'

function readDismissed() {
  try {
    return localStorage.getItem(DISMISS_KEY) === '1' || localStorage.getItem(LEGACY_DISMISS_KEY) === '1'
  } catch {
    return false
  }
}

// null means "the user has never chosen", which is what lets the installed
// count pick the default instead of overriding an explicit preference.
function readCollapsePref() {
  try {
    const raw = localStorage.getItem(COLLAPSE_KEY)
    if (raw === '1') return true
    if (raw === '0') return false
  } catch { /* ignore */ }
  return null
}

// "Recommended for your hardware" strip at the top of the Models gallery. Shares
// the hardware-fit ranking with the empty-state starter widget via
// useRecommendedModels, but styled for the gallery page.
//
// Prominence tracks need: someone with nothing installed is exactly who this is
// for and gets it expanded, while a stocked instance gets a one-line summary
// that still expands on demand. Both the collapse choice and the dismissal
// persist, so the gallery stops re-litigating the decision on every visit.
export default function RecommendedModels({ addToast, installedCount = null }) {
  const { t } = useTranslation('models')
  const { recommended, tier, loading } = useRecommendedModels({ count: 4 })
  const [installing, setInstalling] = useState(() => new Set())
  const [dismissed, setDismissed] = useState(readDismissed)
  const [collapsePref, setCollapsePref] = useState(readCollapsePref)

  if (loading || dismissed) return null
  if (!recommended || recommended.length === 0) return null
  // Shown by default, whatever is already installed. These are the page's one
  // opinion about this host, and an opinion folded away by default is one the
  // reader never gets. Someone who disagrees can still collapse it, and that
  // choice is remembered; the difference is that we no longer make it for them.
  const collapsed = collapsePref === true

  const dismiss = () => {
    try { localStorage.setItem(DISMISS_KEY, '1') } catch { /* ignore */ }
    setDismissed(true)
  }

  const toggle = () => {
    const next = !collapsed
    try { localStorage.setItem(COLLAPSE_KEY, next ? '1' : '0') } catch { /* ignore */ }
    setCollapsePref(next)
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
    <div className={`rec-models card${collapsed ? ' rec-models--collapsed' : ''}`} data-testid="recommended-models">
      <div className="rec-models-head">
        {/* The accessible name is the visible title alone; the note sits
            outside the control so the name stays short and matches the label a
            voice-control user would speak. State comes from aria-expanded. */}
        <button
          type="button"
          className="rec-models-toggle"
          onClick={toggle}
          aria-expanded={!collapsed}
          aria-controls={CONTENT_ID}
          data-testid="recommended-models-toggle"
        >
          <i className="fas fa-chevron-down rec-models-chevron" aria-hidden="true" />
          <i className={`fas ${isGpu ? 'fa-microchip' : 'fa-memory'}`} aria-hidden="true" />
          <strong>{t('recommended.title')}</strong>
        </button>
        <span className="rec-models-note">
          {collapsed
            ? t('recommended.summary', { n: recommended.length })
            : (isGpu ? t('recommended.gpuNote') : t('recommended.cpuNote'))}
        </span>
        <button type="button" className="rec-models-dismiss" onClick={dismiss} aria-label={t('recommended.dismiss')} title={t('recommended.dismiss')}>
          <i className="fas fa-times" aria-hidden="true" />
        </button>
      </div>
      {/* Lanes, not tiles. These are ranked candidates read in order — the list
          is already sorted by fit — and a grid of equal cards throws that order
          away. The leader is called out in amber because it is the one opinion
          the page is offering; the rest are alternatives, not runners-up worth
          their own colour. */}
      <ul className="lanes lanes--recommended" id={CONTENT_ID} hidden={collapsed}>
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
    </div>
  )
}
