/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ActionMenu from '../ActionMenu'
import { AppIcon } from '../addons/AddonBits'
import { Button, EmptyState, LoadError, SkeletonRows, StatusChip } from '../ui'
import { chatConnectorsApi, chatProjectsApi } from '../../utils/api'
import { useAddonIcons } from '../../hooks/useAddonIcons'
import { bindingState, connectKinds, connectTarget, connectionHref, kindLabel } from '../../utils/projectLinks'
import { useProjectPoll } from '../../hooks/useProjectPoll'
import { kindSetupAddress } from '../../utils/agentWizard'
import './projectlinks.css'

const MENU_ID = 'pj-connect-menu'

const readConnections = async (id) => {
  const [c, a] = await Promise.all([chatProjectsApi.connections(id), chatProjectsApi.agents(id)])
  return { connections: c.connections || [], agents: a.agents || [] }
}

// The Connections tab: the messaging connections that answer as an agent of
// the project. Connect lists the kinds that can be added and opens the setup of
// the kind with a project agent chosen; the connection itself is managed on
// its own page. A token or a list of senders is never read here.
export default function ProjectConnections({ project, archived }) {
  const { t } = useTranslation('projects')
  const navigate = useNavigate()
  const { data, error, reload: load } = useProjectPoll(project.id, readConnections) // { connections, agents }
  const [kinds, setKinds] = useState(null)
  const [menuAt, setMenuAt] = useState(null)
  const [pending, setPending] = useState(null) // the kind item waiting for an agent
  const [agentId, setAgentId] = useState('')
  const closedAt = useRef(0)
  const icons = useAddonIcons(Boolean(data?.connections?.length))

  // A pick or an open menu belongs to the project it was made in.
  useEffect(() => { setPending(null); setMenuAt(null) }, [project.id])
  // The kinds are best effort: when they cannot be read Connect has nothing to
  // list and says nothing about it. A list that loaded empty says so.
  useEffect(() => {
    let live = true
    chatConnectorsApi.kinds().then((k) => { if (live) setKinds(k.kinds || []) }).catch(() => {})
    return () => { live = false }
  }, [])

  if (error?.status === 403 && !data) return <div data-testid="project-connections-denied"><EmptyState variant="unavailable" title={t('access.connections')} body={t('access.connectionsBody')} /></div>
  if (error && !data) return <LoadError title={t('load.connections')} error={error} onRetry={load} />
  if (!data) return <SkeletonRows count={3} />

  const { connections, agents } = data
  const available = agents.filter((a) => !a.unavailable)
  const items = connectKinds(kinds)
  const noAgent = agents.length === 0
  const noKinds = kinds !== null && items.length === 0
  const cannot = archived ? 'archivedHint' : available.length === 0 && !noAgent ? 'noAnswerHint' : noKinds ? 'noKindsHint' : ''

  const open = (e) => {
    if (Date.now() - closedAt.current < 250) return
    const r = e.currentTarget.getBoundingClientRect()
    setMenuAt({ x: r.left, y: r.bottom - 4, returnFocus: e.currentTarget })
  }
  const choose = (item) => {
    const target = connectTarget(item, available)
    if (target.href) navigate(target.href)
    else if (target.choose) { setPending(item); setAgentId(available[0].id) }
  }

  const connect = (
    <>
      <Button icon="plus" iconEnd="chevron-down" disabled={Boolean(cannot) || items.length === 0} onClick={open} aria-haspopup="menu" aria-expanded={Boolean(menuAt)} aria-controls={menuAt ? MENU_ID : undefined} aria-label={t('connections.connectLabel')} data-testid="project-connect">
        {t('connections.connect')}
      </Button>
      <ActionMenu
        ariaLabel={t('connections.connectLabel')}
        menuId={MENU_ID}
        at={menuAt}
        onClose={() => { closedAt.current = Date.now(); setMenuAt(null) }}
        items={items.map((it) => ({ key: it.key, icon: 'plus', label: it.title, onClick: () => choose(it) }))}
      />
    </>
  )
  const newAgent = (
    <Button variant="primary" icon="plus" disabled={archived} onClick={() => navigate(`/app/agents/new?project=${encodeURIComponent(project.id)}`)} data-testid="project-connections-new-agent">
      {t('agents.new')}
    </Button>
  )

  return (
    <section className="pj-section" data-testid="project-connections">
      <div className="pj-head">
        <h2 className="pj-h2">{t('connections.title')}</h2>
        {!noAgent && connect}
      </div>
      <p className="pj-note">{t('connections.lead')}</p>
      {cannot && <p className="pj-note" data-testid="project-connections-hint">{t(`connections.${cannot}`)}</p>}
      {error && <LoadError title={t('load.connectionsStale')} error={error} onRetry={load} />}
      {pending && (
        <div className="pjl-choose" role="group" aria-label={t('connections.chooseTitle')} data-testid="project-connect-choose">
          <p className="pjl-choose__q">{t('connections.chooseFor', { kind: pending.title })}</p>
          <label className="pj-muted" htmlFor="pj-connect-agent">{t('connections.chooseTitle')}</label>
          <select id="pj-connect-agent" className="input" value={agentId} onChange={(e) => setAgentId(e.target.value)} data-testid="project-connect-agent">
            {available.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
          </select>
          <Button onClick={() => navigate(kindSetupAddress(pending, agentId))} data-testid="project-connect-go">{t('connections.chooseGo')}</Button>
          <Button variant="ghost" onClick={() => setPending(null)}>{t('connections.chooseCancel')}</Button>
        </div>
      )}
      {noAgent ? (
        <div data-testid="project-connections-noagent">
          <EmptyState variant="no-results" icon="robot" title={t('connections.noAgentTitle')} body={t('connections.noAgentBody')} actions={newAgent} />
        </div>
      ) : connections.length === 0 ? (
        <div data-testid="project-connections-empty">
          <EmptyState variant="no-results" icon="plug" title={t('connections.noneTitle')} body={t('connections.noneBody')} />
        </div>
      ) : (
        <ul className="pj-list" data-testid="project-connection-list">
          {connections.map((c) => {
            const k = (kinds || []).find((x) => x.kind === c.kind)
            const label = kindLabel(kinds, c.kind)
            const state = bindingState(c, archived)
            return (
              <li key={c.id} className={`pjl-row${state === 'active' ? '' : ' pjl-row--paused'}`} data-testid="project-connection" data-kind={c.kind} data-state={state}>
                <AppIcon src={icons[k?.addon] || ''} id={k?.addon || c.kind} name={c.name || label} size={40} />
                <div className="pjl-main">
                  <span className="pjl-head">
                    <span className="pjl-name">{c.name}</span>
                    {state === 'active' && <StatusChip tone="ok" icon="check-circle" label={t('state.on')} testId="project-connection-state" />}
                    {state === 'paused' && <StatusChip tone="warn" icon="pause" label={t('state.paused')} testId="project-connection-state" />}
                    {state === 'off' && <StatusChip tone="idle" icon="pause" label={t('state.off')} testId="project-connection-state" />}
                  </span>
                  <span className="pjl-meta">
                    <span data-testid="project-connection-kind">{label}</span>
                    <span data-testid="project-connection-agent">{t('connections.answersAs', { agent: c.agent_name })}</span>
                    {state === 'paused' && <span data-testid="project-connection-why">{t('automations.pausedWhy')}</span>}
                  </span>
                </div>
                <div className="pjl-end">
                  <Button size="sm" iconEnd="arrow-right" to={connectionHref(kinds, c)} data-testid="project-connection-open">{t('connections.open')}</Button>
                </div>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
