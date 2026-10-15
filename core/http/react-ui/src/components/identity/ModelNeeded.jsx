import { useCallback, useEffect, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useOperations } from '../../hooks/useOperations'
import { modelsApi } from '../../utils/api'
import Icon from '../Icon'

// The disabled state: recognition runs on a model, and none is installed.
// It says so, offers the gallery models that carry the right tag, and says
// what turns the feature on. `kind` is 'voice' or 'face'.
export default function ModelNeeded({ kind, onInstalled, addToast, isAdmin }) {
  const { t } = useTranslation('biometrics')
  const { operations } = useOperations()
  const [models, setModels] = useState([])
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)
  const [started, setStarted] = useState(() => new Set())

  const load = useCallback(async () => {
    try {
      const res = await modelsApi.list({ tag: kind === 'face' ? 'face-recognition' : 'voice-recognition', items: 4, page: 1, sort: 'name', order: 'asc' })
      setModels((res?.models || []).filter(m => !m.installed).slice(0, 3))
      setFailed(false)
    } catch {
      setFailed(true)
    } finally {
      setLoading(false)
    }
  }, [kind])

  useEffect(() => { load() }, [load])

  // When an install finishes, tell the page to look for the model again.
  useEffect(() => {
    if (started.size === 0) return
    const done = [...started].some(id => operations.some(op => op.name === id && (op.completed || op.error)))
    if (done) { onInstalled?.(); load() }
  }, [operations, started, onInstalled, load])

  const install = async (model) => {
    setStarted(prev => new Set(prev).add(model.id))
    try {
      await modelsApi.install(model.id)
      addToast(t('needed.started', { name: model.name }), 'success')
    } catch (err) {
      setStarted(prev => { const next = new Set(prev); next.delete(model.id); return next })
      addToast(err.message, 'error')
    }
  }

  const busy = (model) => started.has(model.id) || operations.some(op => op.name === model.id && !op.completed && !op.error)

  return (
    <section className="idn-needed dk-card" data-testid="model-needed" aria-labelledby={`${kind}-needed-title`}>
      <span className="dk-badge dk-badge--warn"><Icon name="plug-off" /> {t('needed.badge')}</span>
      <h2 className="idn-h2" id={`${kind}-needed-title`}>{t(`${kind}.neededTitle`)}</h2>
      <p className="idn-needed__text">{t(`${kind}.neededText`)}</p>
      {isAdmin && !loading && models.length > 0 && (
        <ul className="idn-needed__list">
          {models.map(model => (
            <li key={model.id}>
              <span><strong className="dk-mono">{model.name}</strong><span className="dk-hint">{model.backend ? t('needed.backend', { backend: model.backend }) : ''}</span></span>
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={busy(model)} onClick={() => install(model)}>
                <Icon name={busy(model) ? 'spinner' : 'download'} spin={busy(model)} /> {busy(model) ? t('needed.installing') : t('needed.install')}
              </button>
            </li>
          ))}
        </ul>
      )}
      {isAdmin && !loading && models.length === 0 && (
        <p className="dk-hint">{failed ? t('needed.loadFailed') : t('needed.noneListed')}{' '}<Link className="dk-link" to="/app/models">{t('needed.browse')}</Link></p>
      )}
      {!isAdmin && <p className="dk-hint">{t('needed.askAdmin')}</p>}
      <details className="idn-disclosure">
        <summary><Icon name="chevron-right" /> {t('needed.whatTitle')}</summary>
        <p className="idn-disclosure__text">{t(`${kind}.neededWhat`)}</p>
      </details>
    </section>
  )
}
