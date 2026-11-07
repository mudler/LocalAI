/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, StatusChip } from '../ui'
import { addonsApi } from '../../utils/addonsClient'
import { loadSetup, saveSetup, setupProgress, setupRows, setupVisible } from '../../utils/agentSetup'
import { AppIcon } from '../addons/AddonBits'
import { useAddonIcons } from '../../hooks/useAddonIcons'
import './agentwizard.css'

// "Finish setup": one row for each app the person chose in the new-agent
// wizard. The browser remembers the choice and a dismissal; whether a row is
// done is what the server says now (connections, accounts, the add-on switch).
//   agent       the saved agent
//   kinds       connector kinds of running add-ons
//   bindings    connector bindings of the person, or null while unknown
export default function AgentSetupCard({ agent, kinds, bindings }) {
  const { t } = useTranslation('agents')
  const [state, setState] = useState(() => loadSetup(agent.id))
  const [installed, setInstalled] = useState(null)
  const icons = useAddonIcons()
  const needsInstalled = state.wanted.some((w) => w.type === 'catalog')

  useEffect(() => { setState(loadSetup(agent.id)) }, [agent.id])
  useEffect(() => {
    if (!needsInstalled) return undefined
    let live = true
    addonsApi.apps.list().then((apps) => { if (live) setInstalled(apps.map((a) => a.id)) }).catch(() => {})
    return () => { live = false }
  }, [needsInstalled])

  const rows = useMemo(
    () => setupRows(state.wanted, { agent, kinds, bindings, installed }),
    [state.wanted, agent, kinds, bindings, installed],
  )
  if (!setupVisible(state, rows)) return null
  const { done, total } = setupProgress(rows)
  const dismiss = () => {
    const next = { ...state, dismissed: true }
    saveSetup(agent.id, next)
    setState(next)
  }
  const verb = { chat: 'setup.connect', account: 'setup.account', switch: 'setup.turnOn', install: 'setup.find' }
  return (
    <section className="aw-setup" aria-labelledby="aw-setup-h" data-testid="agent-setup">
      <div className="aw-setup__head">
        <h2 className="ag-h2" id="aw-setup-h">{t('setup.title')}</h2>
        <span className="aw-setup__count" data-testid="agent-setup-count">{t('setup.count', { done, total })}</span>
        <Button size="sm" variant="ghost" onClick={dismiss} data-testid="agent-setup-dismiss">{t('setup.dismiss')}</Button>
      </div>
      <progress className="aw-progress" value={done} max={total} aria-label={t('setup.progress')} data-testid="agent-setup-progress" />
      <ul className="aw-todo">
        {rows.map((r) => (
          <li key={r.key} className="aw-todo__row" data-setup={r.key} data-done={r.done || undefined} data-testid="agent-setup-row">
            <AppIcon src={icons[r.addon] || ''} id={r.addon} name={r.title} size={32} />
            <div className="aw-todo__text">
              <span className="aw-todo__title">{r.title}</span>
              <span className="aw-todo__needs">{r.installed ? '' : `${t('wizard.where.notInstalled')}. `}{r.setup || t('wizard.where.generic', { title: r.title })}</span>
            </div>
            {r.done
              ? <StatusChip tone="ok" label={t('setup.done')} />
              : <Button size="sm" iconEnd="arrow-right" to={r.href} aria-label={t('setup.actionFor', { action: t(verb[r.kind === 'chat' ? 'chat' : r.kind]), title: r.title })} data-testid="agent-setup-action">{t(verb[r.kind === 'chat' ? 'chat' : r.kind])}</Button>}
          </li>
        ))}
      </ul>
      <p className="ag-note">{t('setup.stays')}</p>
    </section>
  )
}
