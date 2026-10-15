/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useEffect, useRef } from 'react'
import { useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { apiUrl } from '../utils/basePath'
import UsersPanel from '../components/users/UsersPanel'
import InvitesPanel from '../components/users/InvitesPanel'
import ApiKeysPanel from '../components/users/ApiKeysPanel'
import './access.css'

const TABS = [
  { id: 'users', labelKey: 'tabs.users' },
  { id: 'invites', labelKey: 'tabs.invites' },
  { id: 'keys', labelKey: 'tabs.keys' },
]

// People, invites and API keys, on one page under the Settings tab. The tab is
// kept in the address (?tab=invites) so a link opens it.
export default function Users() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('admin')
  const { t: ta } = useTranslation('auth')
  const [params, setParams] = useSearchParams()
  const tab = TABS.some(x => x.id === params.get('tab')) ? params.get('tab') : 'users'
  const [registrationMode, setRegistrationMode] = useState('')
  const refs = useRef({})

  useEffect(() => {
    let live = true
    fetch(apiUrl('/api/auth/status')).then(r => r.json()).then(d => { if (live) setRegistrationMode(d.registrationMode || '') }).catch(() => {})
    return () => { live = false }
  }, [])

  const select = (id, focus = false) => {
    setParams(id === 'users' ? {} : { tab: id }, { replace: true })
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
    <div className="page page--wide us-page" data-testid="users-page">
      <header className="us-head">
        <h1 className="us-title">{t('users.title')}</h1>
        <p className="us-lede">{t('users.subtitle')}</p>
      </header>

      <div className="dk-tabs" role="tablist" aria-label={t('users.title')} onKeyDown={onKey}>
        {TABS.map(x => (
          <button
            key={x.id} ref={el => { refs.current[x.id] = el }} type="button" role="tab" id={`us-tab-${x.id}`}
            className="dk-tab" aria-selected={tab === x.id} aria-controls={`us-panel-${x.id}`} tabIndex={tab === x.id ? 0 : -1}
            onClick={() => select(x.id)}
          >
            {t(x.labelKey)}
          </button>
        ))}
      </div>

      <div className="dk-tabpanel" role="tabpanel" id={`us-panel-${tab}`} aria-labelledby={`us-tab-${tab}`}>
        {tab === 'users' && <UsersPanel addToast={addToast} onInvite={() => select('invites')} registrationMode={registrationMode} />}
        {tab === 'invites' && <InvitesPanel addToast={addToast} registrationMode={registrationMode} />}
        {tab === 'keys' && <ApiKeysPanel addToast={addToast} intro={ta('keys.adminIntro')} />}
      </div>
    </div>
  )
}
