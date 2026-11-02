// SPDX-License-Identifier: MIT
/* eslint-disable no-unused-vars -- JSX components */
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAddonRegistry } from './useAddonRegistry'
import { projectModelActions } from './modelActions'
import ActionMenu from '../components/ActionMenu'

// Shared by the inspector, gallery Open and actual model detail renderer.
// Acquiring the list does not hold or load any addon module.
export default function ModelLaunch({ useCase, model, label, className = 'dk-chip' }) {
  const registry = useAddonRegistry()
  const navigate = useNavigate()
  const { acquire, allowed } = registry
  const [refused, setRefused] = useState(false)
  useEffect(() => allowed ? acquire() : undefined, [acquire, allowed])
  useEffect(() => setRefused(false), [model, useCase.cap])
  if (useCase.route) return <button type="button" className={className} onClick={() => navigate(useCase.route(model))}>{label}</button>
  const projection = projectModelActions(registry, useCase.cap, model)
  const launch = async choice => { if (!await registry.navigateModelAction(choice)) setRefused(true) }
  const status = refused ? 'stale' : projection.status
  return <span data-model-action={useCase.cap} data-action-status={status}>
    {projection.choices.length === 1
      ? <button type="button" className={className} onClick={() => launch(projection.choices[0])}>{label}</button>
      : projection.choices.length > 1
        ? <><span className="dk-badge">{label}</span><ActionMenu ariaLabel={label} items={projection.choices.map(c => ({ key: c.addonId, label: c.label, onClick: () => launch(c) }))} /></>
        : <span className="dk-badge" title={projection.states.map(s => `${s.addonId}: ${s.status}`).join(', ') || status}>{label}</span>}
  </span>
}
