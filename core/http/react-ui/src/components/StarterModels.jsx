import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { modelsApi } from '../utils/api'
import { formatBytes } from '../utils/format'
import { installRequest, windowLabel, fitTone } from '../utils/recommendedModels'
import { useRecommendedModels } from '../hooks/useRecommendedModels'
import { useOperations } from '../hooks/useOperations'
import { useAuth } from '../context/AuthContext'
import { fillStyle } from './home/memory'
// eslint-disable-next-line no-unused-vars
import Button from './ui/Button'
// eslint-disable-next-line no-unused-vars
import Callout from './ui/Callout'
// eslint-disable-next-line no-unused-vars
import StatusChip from './ui/StatusChip'
import Icon from './Icon'

const MORE_ID = 'home-starters-more'

// Reasons the page already says in another way: the window is on the row, and
// an unreadable machine has its own notice above the list.
const SHOWN_ELSEWHERE = new Set(['window_reduced', 'hardware_unknown'])

// What a model costs, in the words a person weighs it by: the download, the
// window it will get and, when the server could say, the memory that window
// needs.
// eslint-disable-next-line no-unused-vars
function Facts({ model, t }) {
  const window = windowLabel(model.context_size)
  return (
    <small className="home-starters-meta">
      {model.size_bytes > 0 && <span>{t('starters.facts.size', { size: formatBytes(model.size_bytes) })}</span>}
      {window && <span>{t('starters.facts.window', { window })}</span>}
      {model.memory_bytes > 0 && <span>{t('starters.facts.memory', { memory: formatBytes(model.memory_bytes) })}</span>}
    </small>
  )
}

// First run: the model recommended for this machine, with a disclosure for the
// two alternatives. An administrator can install; everyone else sees the same
// names and is told to ask one. The server decides what is listed and what
// window an install gets, and refuses an install from anyone else.
export default function StarterModels({ addToast, onInstallStarted }) {
  const { t } = useTranslation('home')
  const { isAdmin } = useAuth()
  const { primary, alternatives, tier, detected, scope, nodeName, nothingFits, loading } = useRecommendedModels()
  const { operations } = useOperations()
  const [installing, setInstalling] = useState(() => new Set())
  const [more, setMore] = useState(false)

  // Draw nothing while the answer is on its way, and when there is none, rather
  // than flash content that may be replaced a moment later.
  if (loading || !primary) return null

  const install = async (model) => {
    setInstalling(prev => new Set(prev).add(model.id))
    try {
      await modelsApi.install(...installRequest(model))
      addToast?.(t('starters.installStarted', { model: model.name }), 'success')
      onInstallStarted?.(model.id)
    } catch (err) {
      addToast?.(t('starters.installFailed', { message: err.message }), 'error')
      setInstalling(prev => {
        const next = new Set(prev)
        next.delete(model.id)
        return next
      })
    }
  }

  const row = (model, isPrimary) => {
    // The install shows up in the operations list once the server has taken
    // it. Its progress is the only progress there is to show.
    const op = operations.find(o => o.name === model.id && !o.isBackend && !o.isDeletion)
    const busy = installing.has(model.id) || !!op
    return (
      <li key={model.id} className={`home-starters-item${isPrimary ? ' home-starters-item--primary' : ''}`} data-testid={isPrimary ? 'starter-primary' : 'starter-alt'}>
        <div className="home-starters-info">
          <span className="home-starters-name" title={model.id}>{model.name}</span>
          {isPrimary && <span className="home-starters-reason">{model.reason}</span>}
          <Facts model={model} t={t} />
          {model.fit !== 'fits' && model.fit !== 'unknown' && (
            <StatusChip tone={fitTone(model.fit)} label={t(`starters.fit.${model.fit}`)} />
          )}
          {(model.fit_reasons || []).filter(k => !SHOWN_ELSEWHERE.has(k)).map(k => (
            <span key={k} className="home-starters-why">{t(`starters.reasons.${k}`)}</span>
          ))}
        </div>
        {isAdmin && (
          <Button
            variant={isPrimary ? 'primary' : 'secondary'}
            size="sm"
            icon="download"
            busy={busy}
            onClick={() => install(model)}
            aria-label={t('starters.installModel', { model: model.name })}
          >
            {busy ? t('starters.installing') : t('starters.install')}
          </Button>
        )}
        {op && op.progress > 0 && (
          <span
            className="home-bar home-bar--thin home-starters-progress"
            role="progressbar"
            aria-valuenow={Math.round(op.progress)}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label={t('strip.progress', { name: model.id })}
          >
            <span className="home-bar__fill" style={fillStyle(op.progress)} />
          </span>
        )}
      </li>
    )
  }

  return (
    <section className="home-starters" aria-label={t('starters.title')} data-testid="starter-models">
      <div className="home-starters-head">
        <span className="home-starters-title">{t('starters.title')}</span>
        <span className="home-starters-tier" data-testid="starter-tier">
          <Icon name={tier === 'cpu' ? 'memory' : 'cpu'} />
          {t(`starters.tier.${tier}`)}
        </span>
      </div>
      {!detected && (
        <Callout tone="info" testId="starter-undetected">{t('starters.undetected')}</Callout>
      )}
      {detected && (
        <p className="home-starters-sub">
          {scope === 'cluster' ? t('starters.cluster', { node: nodeName }) : tier === 'cpu' ? t('starters.cpuNote') : t('starters.gpuNote')}
        </p>
      )}
      {nothingFits && (
        <Callout tone="warn" testId="starter-nothing-fits">{t('starters.nothingFits')}</Callout>
      )}
      <ul className="home-starters-list">{row(primary, true)}</ul>
      {!isAdmin && <p className="home-starters-ask" data-testid="starter-ask-admin">{t('starters.askAdmin')}</p>}
      {alternatives.length > 0 && (
        <>
          <Button
            variant="ghost"
            size="sm"
            icon={more ? 'chevron-up' : 'chevron-down'}
            className="home-starters-toggle"
            data-testid="starter-more-toggle"
            aria-expanded={more}
            aria-controls={MORE_ID}
            onClick={() => setMore(v => !v)}
          >
            {more ? t('starters.fewer') : t('starters.more')}
          </Button>
          {more && <ul className="home-starters-list" id={MORE_ID}>{alternatives.map(m => row(m, false))}</ul>}
        </>
      )}
    </section>
  )
}
