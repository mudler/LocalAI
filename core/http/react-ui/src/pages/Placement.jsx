/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { Link, Navigate, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import AdminPage from '../components/admin/AdminPage'
import Scheduling from './Scheduling'
import Failover from './Failover'
import { useHubAuth } from '../components/hub/useHubAuth'
import { Button } from '../components/ui'

const VIEWS = [
  { id: 'rules', to: '/app/scheduling', labelKey: 'placement.views.rules' },
  { id: 'failover', to: '/app/scheduling/failover', labelKey: 'placement.views.failover' },
]

// Placement: where models run. Two views of one page, the rules that say which
// model may run where and the failover chains that say what happens when a
// target stops answering. The view is the route, so a link opens it.
export default function Placement({ view = 'rules' }) {
  const { t } = useTranslation('swarm')
  // "New rule" is the page's one primary action and belongs to the Rules view.
  const [newRule, setNewRule] = useState(0)
  const action = view === 'rules'
    ? <Button variant="primary" icon="plus" onClick={() => setNewRule(n => n + 1)} data-testid="placement-new-rule">{t('rules.new')}</Button>
    : null
  return (
    <AdminPage title={t('placement.title')} supporting={t('placement.supporting')} action={action} width="full" data-testid="placement-page">
      <nav className="dk-tabs placement-tabs" aria-label={t('placement.views.label')} data-testid="placement-views">
        {VIEWS.map(v => (
          <Link key={v.id} to={v.to} className="dk-tab" data-testid={`placement-view-${v.id}`} aria-current={v.id === view ? 'page' : undefined}>
            {t(v.labelKey)}
          </Link>
        ))}
      </nav>
      {view === 'failover' ? <Failover embedded /> : <Scheduling embedded newRequest={newRule} />}
    </AdminPage>
  )
}

// The address of the failover chains before they were a Placement view. On a
// cluster it leads to that view; a single install has no Placement page, so the
// chains stay a page of their own there.
export function FailoverEntry() {
  const { features, featuresLoaded } = useHubAuth()
  const { search, hash } = useLocation()
  if (!featuresLoaded) return null
  if (features.distributed === true) return <Navigate to={`/app/scheduling/failover${search}${hash}`} replace />
  return <Failover />
}
