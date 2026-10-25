/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useEffect, useRef } from 'react'
import { Link, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../context/AuthContext'
import { profileApi, usageApi } from '../utils/api'
import { totalsOf, groupUsage, compactCount } from '../utils/traffic'
import { timeUntil, initialOf } from '../utils/access'
import LoadingSpinner from '../components/LoadingSpinner'
import ApiKeysPanel from '../components/users/ApiKeysPanel'
import Icon from '../components/Icon'
import './access.css'

const TABS = [
  { id: 'profile', labelKey: 'account.tabs.profile' },
  { id: 'security', labelKey: 'account.tabs.security' },
  { id: 'keys', labelKey: 'account.tabs.apiKeys' },
  { id: 'usage', labelKey: 'account.tabs.usage' },
]

const WINDOW_WORDS = { '1m': 'minute', '5m': '5 minutes', '1h': 'hour', '6h': '6 hours', '1d': 'day', '7d': 'week', '30d': '30 days' }

function ProfileTab({ addToast }) {
  const { t } = useTranslation('auth')
  const { user, refresh } = useAuth()
  const [name, setName] = useState(user?.name || '')
  const [avatarUrl, setAvatarUrl] = useState(user?.avatarUrl || '')
  const [saving, setSaving] = useState(false)
  const [avatarOk, setAvatarOk] = useState(true)

  useEffect(() => { if (user?.name) setName(user.name) }, [user?.name])
  useEffect(() => { setAvatarUrl(user?.avatarUrl || '') }, [user?.avatarUrl])
  useEffect(() => { setAvatarOk(true) }, [avatarUrl])

  const hasChanges = (name.trim() && name.trim() !== user?.name) || (avatarUrl.trim() !== (user?.avatarUrl || ''))

  const handleSave = async (e) => {
    e.preventDefault()
    if (!name.trim() || !hasChanges) return
    setSaving(true)
    try {
      await profileApi.updateProfile(name.trim(), avatarUrl.trim())
      addToast(t('account.profile.updated'), 'success')
      refresh()
    } catch (err) {
      addToast(t('account.profile.updateFailed', { message: err.message }), 'error')
    } finally {
      setSaving(false)
    }
  }

  const shown = avatarUrl.trim() || user?.avatarUrl
  return (
    <form className="ac-form" onSubmit={handleSave} data-testid="account-profile">
      <div className="dk-field">
        <label className="dk-label" htmlFor="ac-name">{t('account.profile.displayName')}</label>
        <input id="ac-name" type="text" className="dk-input" value={name} maxLength={100} disabled={saving} onChange={(e) => setName(e.target.value)} aria-describedby="ac-name-hint" />
        <p className="dk-hint" id="ac-name-hint">{t('account.profile.displayNameDescription')}</p>
      </div>
      <div className="dk-field">
        <label className="dk-label" htmlFor="ac-avatar">{t('account.profile.avatarUrl')}</label>
        <input id="ac-avatar" type="url" className="dk-input" value={avatarUrl} maxLength={512} disabled={saving} placeholder={t('account.profile.avatarUrlPlaceholder')} onChange={(e) => setAvatarUrl(e.target.value)} aria-describedby="ac-avatar-hint" />
        <p className="dk-hint" id="ac-avatar-hint">{t('account.profile.avatarUrlDescription')}</p>
      </div>
      <div className="ac-who">
        <span className="us-avatar us-avatar--lg">
          {shown && avatarOk
            ? <img src={shown} alt="" onError={() => setAvatarOk(false)} />
            : <span aria-hidden="true">{initialOf(user || {})}</span>}
        </span>
        <span className="ac-who__text">
          <strong>{user?.email}</strong>
          <span>{user?.role} · {user?.provider || 'local'} {t('account.profile.signIn')}</span>
        </span>
      </div>
      <div className="ac-actions">
        <button type="submit" className="dk-btn dk-btn--primary" disabled={saving || !name.trim() || !hasChanges}>
          {saving ? <><LoadingSpinner size="sm" /> {t('account.profile.saving')}</> : <><Icon name="save" /> {t('account.profile.save')}</>}
        </button>
      </div>
    </form>
  )
}

function SecurityTab({ addToast }) {
  const { t } = useTranslation('auth')
  const { user } = useAuth()
  const isLocal = user?.provider === 'local'
  const [currentPw, setCurrentPw] = useState('')
  const [newPw, setNewPw] = useState('')
  const [confirmPw, setConfirmPw] = useState('')
  const [saving, setSaving] = useState(false)
  const [weakWarning, setWeakWarning] = useState('')
  const [acknowledgeWeak, setAcknowledgeWeak] = useState(false)

  if (!isLocal) {
    return (
      <div className="ac-note" data-testid="account-oauth-only">
        <Icon name="shield" aria-hidden="true" />
        <p>{t('account.security.oauthOnly', { provider: user?.provider || 'OAuth' })}</p>
      </div>
    )
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (newPw !== confirmPw) {
      addToast(t('account.security.passwordsDoNotMatch'), 'error')
      return
    }
    setSaving(true)
    try {
      await profileApi.changePassword(currentPw, newPw, acknowledgeWeak)
      addToast(t('account.security.changed'), 'success')
      setCurrentPw(''); setNewPw(''); setConfirmPw(''); setWeakWarning(''); setAcknowledgeWeak(false)
    } catch (err) {
      if (err.body?.overridable) setWeakWarning(err.body.error || err.message)
      else addToast(err.message, 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <form className="ac-form" onSubmit={handleSubmit} data-testid="account-security">
      <div className="dk-field">
        <label className="dk-label" htmlFor="ac-cur">{t('account.security.currentPassword')}</label>
        <input id="ac-cur" type="password" className="dk-input" value={currentPw} autoComplete="current-password" required disabled={saving} placeholder={t('account.security.currentPasswordPlaceholder')} onChange={(e) => setCurrentPw(e.target.value)} />
        <p className="dk-hint">{t('account.security.currentPasswordDescription')}</p>
      </div>
      <div className="dk-field">
        <label className="dk-label" htmlFor="ac-new">{t('account.security.newPassword')}</label>
        <input
          id="ac-new" type="password" className="dk-input" value={newPw} autoComplete="new-password" required disabled={saving}
          placeholder={t('account.security.newPasswordPlaceholder')}
          onChange={(e) => { setNewPw(e.target.value); setWeakWarning(''); setAcknowledgeWeak(false) }}
        />
        <p className="dk-hint">{t('account.security.newPasswordDescription')}</p>
      </div>
      <div className="dk-field">
        <label className="dk-label" htmlFor="ac-confirm">{t('account.security.confirmPassword')}</label>
        <input id="ac-confirm" type="password" className="dk-input" value={confirmPw} autoComplete="new-password" required disabled={saving} placeholder={t('account.security.confirmPasswordPlaceholder')} onChange={(e) => setConfirmPw(e.target.value)} />
        <p className="dk-hint">{t('account.security.confirmPasswordDescription')}</p>
      </div>
      {weakWarning && (
        <div role="alert" className="us-weak">
          <p className="us-weak__text">{weakWarning}</p>
          <label className="dk-choice">
            <input type="checkbox" className="dk-check" checked={acknowledgeWeak} disabled={saving} onChange={(e) => setAcknowledgeWeak(e.target.checked)} />
            {t('account.security.useAnyway')}
          </label>
        </div>
      )}
      <div className="ac-actions">
        <button type="submit" className="dk-btn dk-btn--primary" disabled={saving || !currentPw || !newPw || !confirmPw}>
          {saving ? <><LoadingSpinner size="sm" /> {t('account.security.changing')}</> : t('account.security.changePassword')}
        </button>
      </div>
    </form>
  )
}

// The caller's own use over the last 30 days and the limits an admin set on
// them. The full page, with every window and the cost estimate, is Usage.
function UsageTab() {
  const { t } = useTranslation('auth')
  const [state, setState] = useState({ loading: true, error: null, usage: null, quotas: [] })

  useEffect(() => {
    let live = true
    Promise.all([
      usageApi.getMyUsage('month'),
      usageApi.getMyQuotas().catch(() => ({ quotas: [] })),
    ]).then(([usage, q]) => {
      if (live) setState({ loading: false, error: null, usage, quotas: q?.quotas || [] })
    }).catch(err => {
      if (live) setState({ loading: false, error: err, usage: null, quotas: [] })
    })
    return () => { live = false }
  }, [])

  if (state.loading) return <div className="ak-loading"><LoadingSpinner size="sm" /></div>
  if (state.error) {
    return <div className="ac-note" role="alert"><Icon name="alert-circle" aria-hidden="true" /><p>{t('account.usage.loadFailed', { message: state.error.message })}</p></div>
  }
  const buckets = state.usage?.usage || []
  const totals = totalsOf(buckets)
  const byModel = groupUsage(buckets, 'model').sort((a, b) => b.total - a.total).slice(0, 8)
  const max = Math.max(1, ...byModel.map(r => r.total))

  return (
    <div className="ac-usage" data-testid="account-usage">
      <p className="ac-usage__lede">
        {t('account.usage.lede')} <Link className="dk-link" to="/app/usage">{t('account.usage.openFull')}</Link>
      </p>
      <dl className="ac-figures">
        <div className="ac-figure"><dt>{t('account.usage.requests')}</dt><dd data-testid="usage-requests">{compactCount(totals.requests)}</dd></div>
        <div className="ac-figure"><dt>{t('account.usage.tokensIn')}</dt><dd>{compactCount(totals.prompt)}</dd></div>
        <div className="ac-figure"><dt>{t('account.usage.tokensOut')}</dt><dd>{compactCount(totals.completion)}</dd></div>
      </dl>

      {byModel.length === 0 ? (
        <div className="dk-empty"><p className="dk-empty-text">{t('account.usage.none')}</p></div>
      ) : (
        <ul className="dk-hbars" aria-label={t('account.usage.byModel')}>
          {byModel.map(r => (
            <li key={r.id} className="dk-hbar">
              <span className="dk-hbar-name dk-mono" title={r.name}>{r.name}</span>
              <span className="dk-hbar-track"><span className="dk-hbar-fill" style={{ '--dk-w': `${Math.max(2, (r.total / max) * 100)}%` }} /></span>
              <span className="dk-hbar-value">{compactCount(r.total)}</span>
            </li>
          ))}
        </ul>
      )}

      <h2 className="ac-h2">{t('account.usage.limits')} <span className="ac-sub">{t('account.usage.limitsSub')}</span></h2>
      {state.quotas.length === 0 ? (
        <p className="ac-hint">{t('account.usage.noLimits')}</p>
      ) : (
        <ul className="ac-limits" data-testid="account-limits">
          {state.quotas.flatMap(q => {
            const where = `${q.model || t('account.usage.allModels')}, `
            const per = `per ${WINDOW_WORDS[q.window] || q.window}`
            const rows = []
            if (q.max_total_tokens != null) rows.push({ id: `${q.id}-t`, text: `${where}${t('account.usage.tokens')} ${per}`, cur: q.current_total_tokens || 0, max: q.max_total_tokens, q })
            if (q.max_requests != null) rows.push({ id: `${q.id}-r`, text: `${where}${t('account.usage.requests').toLowerCase()} ${per}`, cur: q.current_requests || 0, max: q.max_requests, q })
            return rows
          }).map(r => {
            const pct = r.max > 0 ? Math.min(100, Math.round((r.cur / r.max) * 100)) : 0
            return (
              <li key={r.id} className="ac-limit">
                <span className="ac-limit__text">{r.text}</span>
                <span className={`dk-progress${pct >= 90 ? ' dk-progress--error' : ''}`} role="progressbar" aria-label={r.text} aria-valuemin="0" aria-valuemax="100" aria-valuenow={pct} style={{ '--dk-value': `${pct}%` }}>
                  <span className="dk-progress-bar" />
                </span>
                <span className="ac-limit__value dk-mono">{compactCount(r.cur)} of {compactCount(r.max)}</span>
                {r.q.resets_at && <span className="ac-limit__reset">{t('account.usage.resets', { when: timeUntil(r.q.resets_at) })}</span>}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

export default function Account() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('auth')
  const { authEnabled, user } = useAuth()
  const [params, setParams] = useSearchParams()
  const tab = TABS.some(x => x.id === params.get('tab')) ? params.get('tab') : 'profile'
  const refs = useRef({})

  if (!authEnabled) {
    return (
      <div className="page page--narrow">
        <div className="dk-empty">
          <div className="dk-empty-icon"><Icon name="user-settings" /></div>
          <h2 className="dk-empty-title">{t('account.unavailable')}</h2>
          <p className="dk-empty-text">{t('account.unavailableText')}</p>
        </div>
      </div>
    )
  }

  const select = (id, focus = false) => {
    setParams(id === 'profile' ? {} : { tab: id }, { replace: true })
    if (focus) refs.current[id]?.focus()
  }
  const onKey = (e) => {
    const i = TABS.findIndex(x => x.id === tab)
    if (e.key === 'ArrowRight') { e.preventDefault(); select(TABS[(i + 1) % TABS.length].id, true) }
    else if (e.key === 'ArrowLeft') { e.preventDefault(); select(TABS[(i + TABS.length - 1) % TABS.length].id, true) }
    else if (e.key === 'Home') { e.preventDefault(); select(TABS[0].id, true) }
    else if (e.key === 'End') { e.preventDefault(); select(TABS[TABS.length - 1].id, true) }
  }

  return (
    <div className="page page--narrow ac-page" data-testid="account-page">
      <header className="ac-head">
        <div>
          <h1 className="ac-title">{t('account.title')}</h1>
          <p className="ac-lede">{t('account.subtitle')}</p>
        </div>
        <span className="ac-me">{user?.name || user?.email} · {user?.role}</span>
      </header>

      <div className="dk-tabs" role="tablist" aria-label={t('account.title')} onKeyDown={onKey}>
        {TABS.map(x => (
          <button
            key={x.id} ref={el => { refs.current[x.id] = el }} type="button" role="tab" id={`ac-tab-${x.id}`}
            className="dk-tab" aria-selected={tab === x.id} aria-controls={`ac-panel-${x.id}`} tabIndex={tab === x.id ? 0 : -1}
            onClick={() => select(x.id)}
          >
            {t(x.labelKey)}
          </button>
        ))}
      </div>

      <div className="dk-tabpanel" role="tabpanel" id={`ac-panel-${tab}`} aria-labelledby={`ac-tab-${tab}`}>
        {tab === 'profile' && <ProfileTab addToast={addToast} />}
        {tab === 'security' && <SecurityTab addToast={addToast} />}
        {tab === 'keys' && <ApiKeysPanel addToast={addToast} intro={t('keys.accountIntro')} />}
        {tab === 'usage' && <UsageTab />}
      </div>
    </div>
  )
}
