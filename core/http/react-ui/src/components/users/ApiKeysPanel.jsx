/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { apiKeysApi } from '../../utils/api'
import { copyToClipboard } from '../../utils/clipboard'
import { keyState, timeUntil, KEY_EXPIRIES } from '../../utils/access'
import { useDelayedAction, UNDO_WINDOW_MS } from '../../hooks/useDelayedAction'
import LoadingSpinner from '../LoadingSpinner'
import Icon from '../Icon'

function formatDate(d) {
  if (!d) return ''
  return new Date(d).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function Seconds({ until }) {
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 500)
    return () => clearInterval(id)
  }, [])
  return Math.max(0, Math.ceil((until - now) / 1000))
}

// The caller's own API keys: create one, see it once, pause or resume it, and
// revoke it with ten seconds to change your mind. LocalAI lists a person's
// keys only to that person, so this panel is the same for an admin and a user.
//
// A key is shown once, in full, when it is created; the list holds only its
// prefix. Revoking is a call made after the undo window ends, so Undo sends
// nothing.
export default function ApiKeysPanel({ addToast, intro }) {
  const { t } = useTranslation('auth')
  const [keys, setKeys] = useState([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [expiresIn, setExpiresIn] = useState('')
  const [reveal, setReveal] = useState(null)
  const [pauseFormId, setPauseFormId] = useState(null)
  const [pauseMode, setPauseMode] = useState('indefinite')
  const [pauseUntil, setPauseUntil] = useState('')
  const [pauseBusyId, setPauseBusyId] = useState(null)

  const fetchKeys = useCallback(async () => {
    setLoading(true)
    try {
      const data = await apiKeysApi.list()
      setKeys(data.keys || [])
    } catch (err) {
      addToast(t('account.apiKeys.loadFailed', { message: err.message }), 'error')
    } finally {
      setLoading(false)
    }
  }, [addToast, t])

  useEffect(() => { fetchKeys() }, [fetchKeys])

  const revokeNow = useCallback(async (id) => {
    try {
      await apiKeysApi.revoke(id)
      setKeys(prev => prev.filter(k => k.id !== id))
      addToast(t('account.apiKeys.revoked'), 'success')
    } catch (err) {
      addToast(t('account.apiKeys.revokeFailed', { message: err.message }), 'error')
    }
  }, [addToast, t])
  const revoking = useDelayedAction(revokeNow)

  const handleCreate = async (e) => {
    e.preventDefault()
    if (!name.trim()) return
    setCreating(true)
    try {
      const data = await apiKeysApi.create(name.trim(), expiresIn)
      setReveal({ key: data.key, name: data.name || name.trim(), copied: false })
      setName('')
      await fetchKeys()
      addToast(t('account.apiKeys.createdToast'), 'success')
    } catch (err) {
      addToast(t('account.apiKeys.createFailed', { message: err.message }), 'error')
    } finally {
      setCreating(false)
    }
  }

  const copyKey = async () => {
    const ok = await copyToClipboard(reveal.key)
    if (ok) setReveal(r => ({ ...r, copied: true }))
    addToast(ok ? t('account.apiKeys.copiedToast') : t('account.apiKeys.copyFailed'), ok ? 'success' : 'error')
  }

  const isPaused = (k) => k.disabled || (k.pausedUntil && new Date(k.pausedUntil) > new Date())

  const applyPause = async (id, disabled, pausedUntil) => {
    setPauseBusyId(id)
    try {
      await apiKeysApi.setPause(id, disabled, pausedUntil)
      setPauseFormId(null)
      setPauseUntil('')
      await fetchKeys()
      addToast(t(disabled || pausedUntil ? 'account.apiKeys.pausedToast' : 'account.apiKeys.resumedToast'), 'success')
    } catch (err) {
      addToast(t('account.apiKeys.pauseFailed', { message: err.message }), 'error')
    } finally {
      setPauseBusyId(null)
    }
  }

  const submitPause = (id) => {
    if (pauseMode === 'until') {
      if (!pauseUntil) return
      applyPause(id, false, new Date(pauseUntil).toISOString())
    } else {
      applyPause(id, true, null)
    }
  }

  const openPauseForm = (id) => {
    setPauseMode('indefinite')
    setPauseUntil('')
    setPauseFormId(id)
  }

  return (
    <div className="ak" data-testid="api-keys-panel">
      {intro && <p className="ak-intro">{intro}</p>}

      <form className="ak-create" onSubmit={handleCreate}>
        <div className="dk-field">
          <label className="dk-label" htmlFor="ak-name">{t('account.apiKeys.create')}</label>
          <input
            id="ak-name" className="dk-input" type="text" maxLength={64} value={name} disabled={creating}
            placeholder={t('account.apiKeys.namePlaceholder')} onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div className="dk-field">
          <label className="dk-label" htmlFor="ak-expiry">{t('account.apiKeys.expires')}</label>
          <div className="dk-select-wrap">
            <select id="ak-expiry" className="dk-select" value={expiresIn} disabled={creating} onChange={(e) => setExpiresIn(e.target.value)}>
              {KEY_EXPIRIES.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
            </select>
          </div>
        </div>
        <button type="submit" className="dk-btn dk-btn--primary" disabled={creating || !name.trim()}>
          {creating ? <LoadingSpinner size="sm" /> : <><Icon name="plus" /> {t('account.apiKeys.createButton')}</>}
        </button>
      </form>

      {reveal && (
        <div className="ak-reveal" role="alert" data-testid="api-key-reveal">
          <p className="ak-reveal__head"><Icon name="warning" aria-hidden="true" /> {t('account.apiKeys.copyNow')}</p>
          <div className="ak-reveal__row">
            <code className="ak-reveal__key" data-testid="api-key-secret">{reveal.key}</code>
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={copyKey}>
              <Icon name={reveal.copied ? 'check' : 'copy'} /> {reveal.copied ? t('account.apiKeys.copied') : t('account.apiKeys.copy')}
            </button>
          </div>
          <div className="ak-reveal__foot">
            <span>{t('account.apiKeys.revealNote', { name: reveal.name })}</span>
            <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" onClick={() => setReveal(null)}>{t('account.apiKeys.copiedIt')}</button>
          </div>
        </div>
      )}

      {loading ? (
        <div className="ak-loading"><LoadingSpinner size="sm" /></div>
      ) : keys.length === 0 ? (
        <div className="dk-empty">
          <div className="dk-empty-icon"><Icon name="key" /></div>
          <p className="dk-empty-text">{t('account.apiKeys.empty')}</p>
        </div>
      ) : (
        <ul className="ak-list">
          {keys.map((k) => {
            const waiting = revoking.waiting[k.id]
            const state = keyState(k)
            return (
              <li key={k.id} className="apikey-item ak-item" data-revoking={waiting ? 'true' : undefined} data-state={state}>
                <div className="ak-row">
                  <Icon name="key" className="ak-row__icon" aria-hidden="true" />
                  <div className="ak-row__main">
                    <div className="ak-row__name">
                      <span className="ak-name">{k.name}</span>
                      {waiting && <span className="ak-badge" data-tone="error">{t('account.apiKeys.revokedBadge')}</span>}
                      {!waiting && isPaused(k) && (
                        <span className="apikey-paused-badge ak-badge" data-tone="warn">
                          {k.pausedUntil && !k.disabled
                            ? t('account.apiKeys.pausedUntil', { date: formatDate(k.pausedUntil) })
                            : t('account.apiKeys.paused')}
                        </span>
                      )}
                      {!waiting && state === 'expired' && <span className="ak-badge" data-tone="error">{t('account.apiKeys.expired')}</span>}
                    </div>
                    <div className="ak-row__meta">
                      <span className="dk-mono">{k.keyPrefix}…</span>
                      <span>{t('account.apiKeys.created', { date: formatDate(k.createdAt) })}</span>
                      <span>{k.lastUsed ? t('account.apiKeys.lastUsed', { date: formatDate(k.lastUsed) }) : t('account.apiKeys.neverUsed')}</span>
                      {k.expiresAt && state !== 'expired' && <span>{t('account.apiKeys.expiresIn', { when: timeUntil(k.expiresAt) })}</span>}
                    </div>
                    {waiting && (
                      <div className="ak-row__undo" role="status">
                        {t('account.apiKeys.revokingIn')} <span className="dk-mono"><Seconds until={waiting.until} />s</span>. {t('account.apiKeys.revokingNote')}
                      </div>
                    )}
                  </div>
                  <div className="ak-row__acts">
                    {waiting ? (
                      <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => revoking.cancel(k.id)}>
                        <Icon name="undo" /> {t('account.apiKeys.undo')}
                      </button>
                    ) : (
                      <>
                        {isPaused(k) ? (
                          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => applyPause(k.id, false, null)} disabled={pauseBusyId === k.id}>
                            {pauseBusyId === k.id ? <LoadingSpinner size="sm" /> : <><Icon name="play" /> {t('account.apiKeys.resume')}</>}
                          </button>
                        ) : (
                          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" aria-expanded={pauseFormId === k.id} onClick={() => (pauseFormId === k.id ? setPauseFormId(null) : openPauseForm(k.id))}>
                            <Icon name="pause" /> {t('account.apiKeys.pause')}
                          </button>
                        )}
                        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm ak-revoke" onClick={() => revoking.schedule(k.id, k)} title={t('account.apiKeys.revokeKey')}>
                          <Icon name="trash" /> {t('account.apiKeys.revoke')}
                        </button>
                      </>
                    )}
                  </div>
                </div>
                {pauseFormId === k.id && !isPaused(k) && !waiting && (
                  <div className="ak-pause">
                    <label className="dk-choice">
                      <input type="radio" className="dk-radio" name={`pause-mode-${k.id}`} checked={pauseMode === 'indefinite'} onChange={() => setPauseMode('indefinite')} />
                      {t('account.apiKeys.pauseIndefinitely')}
                    </label>
                    <label className="dk-choice">
                      <input type="radio" className="dk-radio" name={`pause-mode-${k.id}`} checked={pauseMode === 'until'} onChange={() => setPauseMode('until')} />
                      {t('account.apiKeys.pauseUntil')}
                    </label>
                    {pauseMode === 'until' && (
                      <input type="datetime-local" className="dk-input ak-pause__date" aria-label={t('account.apiKeys.pauseUntil')} value={pauseUntil} onChange={(e) => setPauseUntil(e.target.value)} />
                    )}
                    <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" onClick={() => submitPause(k.id)} disabled={pauseBusyId === k.id || (pauseMode === 'until' && !pauseUntil)}>
                      {t('account.apiKeys.pauseConfirm')}
                    </button>
                  </div>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
