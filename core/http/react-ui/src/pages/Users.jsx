/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useEffect } from 'react'
import { Navigate, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { apiUrl } from '../utils/basePath'
import UsersPanel from '../components/users/UsersPanel'
import InviteSheet from '../components/users/InviteSheet'
import AdminPage from '../components/admin/AdminPage'
import { Button } from '../components/ui'
import './access.css'

// People who can sign in, and what each of them can use. "Invite someone" is
// the page's one primary action and opens a side sheet with the invite links.
// The old tab addresses keep working: ?tab=keys leads to API keys and
// ?tab=invites opens the sheet.
export default function Users() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('admin')
  const [params, setParams] = useSearchParams()
  const [registrationMode, setRegistrationMode] = useState('')
  const [inviting, setInviting] = useState(params.get('tab') === 'invites' || params.get('invite') === '1')

  useEffect(() => {
    let live = true
    fetch(apiUrl('/api/auth/status')).then(r => r.json()).then(d => { if (live) setRegistrationMode(d.registrationMode || '') }).catch(() => {})
    return () => { live = false }
  }, [])

  // The address that opened the sheet is spent: the page's own address stays.
  useEffect(() => {
    if (params.get('tab') === 'invites' || params.get('invite')) {
      const next = new URLSearchParams(params)
      next.delete('tab'); next.delete('invite')
      setParams(next, { replace: true })
    }
  }, [params, setParams])

  if (params.get('tab') === 'keys') return <Navigate to="/app/api-keys" replace />

  return (
    <AdminPage
      width="full"
      className="us-page"
      data-testid="users-page"
      title={t('users.title')}
      supporting={t('users.subtitle')}
      action={<Button variant="primary" icon="plus" onClick={() => setInviting(true)} data-testid="users-invite">{t('users.invite')}</Button>}
      sheet={inviting ? <InviteSheet addToast={addToast} registrationMode={registrationMode} onClose={() => setInviting(false)} /> : null}
    >
      <UsersPanel addToast={addToast} registrationMode={registrationMode} initialQuery={params.get('q') || ''} onInvite={() => setInviting(true)} />
    </AdminPage>
  )
}
