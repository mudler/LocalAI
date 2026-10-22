import { useEffect, useMemo, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { modelsApi } from '../../utils/api'
import { useModelSuggestion } from '../../hooks/useModelSuggestion'
import { useOperations } from '../../hooks/useOperations'
import { useResources } from '../../hooks/useResources'
import { hostMemory } from '../home/memory'
import { gbLabel, cssVars } from '../../utils/modelLedger'

// What a missing model needs, shown only when the person picks that type (or a
// hand-off needs it). A model, a size, the memory it needs, the memory that is
// free, and one Install button. Nothing starts until they press it, and the
// words they typed stay where they are: this note sits inside the composer and
// the composer never unmounts while a model installs.
//
//   typeLabel  the type's display name, "Video"
//   onChanged  called while an install runs so the caller can re-read which
//              models are installed; the note goes away once one is
export default function InstallNote({ type, typeLabel, onChanged, className = '' }) {
  const { t } = useTranslation('media')
  const outlet = useOutletContext()
  const addToast = outlet?.addToast
  const { state, suggestion } = useModelSuggestion(type, true)
  const { operations } = useOperations()
  const { resources } = useResources(15000)
  const [started, setStarted] = useState('')
  const [failure, setFailure] = useState('')

  const name = suggestion?.name
  const op = useMemo(
    () => name && operations.find(o => o.name === name && !o.isBackend && !o.isDeletion),
    [operations, name],
  )
  const busy = !!op || (!!started && started === name)

  // While the install runs, ask again which models exist. The server adds the
  // model when the download ends, and this note unmounts when it does.
  useEffect(() => {
    if (!busy) return undefined
    const id = setInterval(() => onChanged?.(), 3000)
    return () => clearInterval(id)
  }, [busy, onChanged])

  const memory = hostMemory(resources)
  const free = memory ? Math.max(0, memory.total - memory.used) : 0

  const install = async () => {
    setFailure('')
    setStarted(name)
    try {
      await modelsApi.install(name)
      addToast?.(t('studio.install.started', { model: name }), 'success')
    } catch (err) {
      setStarted('')
      setFailure(err.message)
    }
  }

  if (state === 'loading' || state === 'idle') {
    return (
      <div className={`studio-note ${className}`.trim()} data-testid="studio-install-note" data-state="loading" aria-busy="true">
        <h3>{t('studio.install.title', { type: typeLabel })}</h3>
        <div className="dk-skeleton dk-skeleton--line studio-note__skeleton" />
      </div>
    )
  }

  if (state !== 'ready') {
    return (
      <div className={`studio-note ${className}`.trim()} data-testid="studio-install-note" data-state={state}>
        <h3>{t('studio.install.title', { type: typeLabel })}</h3>
        <p>{state === 'error' ? t('studio.install.galleryDown') : t('studio.install.noneListed', { type: typeLabel })}</p>
        <div className="studio-note__row">
          <Link className="dk-btn dk-btn--secondary dk-btn--sm" to="/app/models">{t('studio.install.browse')}</Link>
        </div>
      </div>
    )
  }

  const size = suggestion.sizeBytes > 0 ? gbLabel(suggestion.sizeBytes) : (suggestion.sizeDisplay || null)
  const need = suggestion.vramBytes > 0 ? gbLabel(suggestion.vramBytes) : (suggestion.vramDisplay || null)
  const progress = op && op.progress > 0 ? Math.round(op.progress) : 0

  return (
    <div className={`studio-note ${className}`.trim()} data-testid="studio-install-note" data-state={busy ? 'installing' : 'ready'}>
      <h3>{t('studio.install.title', { type: typeLabel })}</h3>
      <p>
        <code className="studio-note__model">{name}</code>
        {' '}
        {size ? t('studio.install.size', { size }) : t('studio.install.sizeUnknown')}
        {' '}
        {need ? t('studio.install.need', { need }) : t('studio.install.needUnknown')}
        {memory && ' '}
        {memory && t('studio.install.free', { free: gbLabel(free) })}
        {' '}
        {t('studio.install.nothingStarts')}
      </p>
      <div className="studio-note__row">
        {busy ? (
          <div className="studio-note__progress" role="status">
            <Icon name="spinner" spin />
            <span>{t('studio.install.installing', { model: name })}{progress > 0 ? ` ${progress}%` : ''}</span>
            {progress > 0 && (
              <span
                className="dk-meter studio-note__meter"
                role="progressbar"
                aria-label={t('studio.install.installing', { model: name })}
                aria-valuenow={progress}
                aria-valuemin={0}
                aria-valuemax={100}
              >
                <span className="dk-meter-seg" style={cssVars({ '--dk-w': `${progress}%` })} />
              </span>
            )}
          </div>
        ) : (
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={install} data-testid="studio-install">
            <Icon name="download" /> {t('studio.install.button', { model: name })}
          </button>
        )}
        {failure && <span className="studio-note__error" role="alert">{t('studio.install.failed', { message: failure })}</span>}
      </div>
    </div>
  )
}
