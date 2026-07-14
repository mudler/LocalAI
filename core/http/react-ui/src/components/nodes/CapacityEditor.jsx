import { useState, useEffect, useCallback } from 'react'
import { nodesApi } from '../../utils/api'
import LoadingSpinner from '../LoadingSpinner'
import { formatVRAM } from './nodeStatus'

/**
 * Inline editor for a node's per-model replica capacity.
 *
 * UX intent: discoverable affordance (pencil icon) that opens an inline
 * input - never a modal for a single field. Source-of-truth note is shown
 * inline so operators understand a worker re-registration will overwrite
 * their override; surfacing this in a tooltip would hide too important a
 * caveat.
 *
 * `confirmShrink` is a hook the parent provides so the page can render its
 * own confirm dialog (it has access to all nodes and can phrase the message
 * with full context).
 */
export default function CapacityEditor({ node, loadedModelCounts, onUpdate, confirmShrink, addToast }) {
  const current = node.max_replicas_per_model || 1
  const isOverride = !!node.max_replicas_per_model_manually_set
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(String(current))
  const [saving, setSaving] = useState(false)
  const [resetting, setResetting] = useState(false)

  // Reset draft when current value changes (server response, etc.)
  useEffect(() => {
    if (!editing) setDraft(String(current))
  }, [current, editing])

  const cancel = useCallback(() => {
    setEditing(false)
    setDraft(String(current))
  }, [current])

  const save = useCallback(async () => {
    const value = parseInt(draft, 10)
    if (!Number.isFinite(value) || value < 1) {
      addToast('Replica capacity must be 1 or higher', 'error')
      return
    }
    if (value === current) {
      setEditing(false)
      return
    }
    // Reducing the cap below current loaded replicas: confirm so the operator
    // sees the consequence (running replicas keep going until idle eviction).
    const maxLoadedAcrossModels = Math.max(0, ...Object.values(loadedModelCounts || {}))
    if (value < maxLoadedAcrossModels) {
      const proceed = await confirmShrink({ node, newValue: value, currentLoaded: maxLoadedAcrossModels })
      if (!proceed) return
    }
    setSaving(true)
    try {
      await nodesApi.updateMaxReplicasPerModel(node.id, value)
      addToast(`Replica capacity set to ${value} on ${node.name}`, 'success')
      setEditing(false)
      onUpdate?.(value)
    } catch (err) {
      addToast(`Could not change replica capacity: ${err.message || err}`, 'error')
    } finally {
      setSaving(false)
    }
  }, [draft, current, node, loadedModelCounts, confirmShrink, onUpdate, addToast])

  const onKeyDown = (e) => {
    if (e.key === 'Enter') { e.preventDefault(); save() }
    else if (e.key === 'Escape') { e.preventDefault(); cancel() }
  }

  const reset = useCallback(async () => {
    setResetting(true)
    try {
      await nodesApi.resetMaxReplicasPerModel(node.id)
      addToast(`Override cleared on ${node.name}; worker flag will apply on next re-registration`, 'success')
      onUpdate?.(null)
    } catch (err) {
      addToast(`Could not reset override: ${err.message || err}`, 'error')
    } finally {
      setResetting(false)
    }
  }, [node, onUpdate, addToast])

  // VRAM allocation budget: a free-form string ("80%" or "12GB") resolved to a
  // byte ceiling server-side. Presence of vram_budget signals an active budget.
  const currentBudget = node.vram_budget || ''
  const hasBudget = !!currentBudget
  const budgetBytes = node.vram_budget_bytes || 0
  const [budgetEditing, setBudgetEditing] = useState(false)
  const [budgetDraft, setBudgetDraft] = useState(currentBudget)
  const [budgetSaving, setBudgetSaving] = useState(false)
  const [budgetClearing, setBudgetClearing] = useState(false)

  useEffect(() => {
    if (!budgetEditing) setBudgetDraft(currentBudget)
  }, [currentBudget, budgetEditing])

  const cancelBudget = useCallback(() => {
    setBudgetEditing(false)
    setBudgetDraft(currentBudget)
  }, [currentBudget])

  const saveBudget = useCallback(async () => {
    const value = budgetDraft.trim()
    if (value === currentBudget) {
      setBudgetEditing(false)
      return
    }
    setBudgetSaving(true)
    try {
      await nodesApi.updateVramBudget(node.id, value)
      addToast(
        value
          ? `VRAM budget set to ${value} on ${node.name}`
          : `VRAM budget cleared on ${node.name}`,
        'success',
      )
      setBudgetEditing(false)
      onUpdate?.(value)
    } catch (err) {
      addToast(`Could not change VRAM budget: ${err.message || err}`, 'error')
    } finally {
      setBudgetSaving(false)
    }
  }, [budgetDraft, currentBudget, node, onUpdate, addToast])

  const onBudgetKeyDown = (e) => {
    if (e.key === 'Enter') { e.preventDefault(); saveBudget() }
    else if (e.key === 'Escape') { e.preventDefault(); cancelBudget() }
  }

  const clearBudget = useCallback(async () => {
    setBudgetClearing(true)
    try {
      await nodesApi.resetVramBudget(node.id)
      addToast(`VRAM budget cleared on ${node.name}`, 'success')
      onUpdate?.(null)
    } catch (err) {
      addToast(`Could not clear VRAM budget: ${err.message || err}`, 'error')
    } finally {
      setBudgetClearing(false)
    }
  }, [node, onUpdate, addToast])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)' }}>
    <div style={{
      display: 'flex', alignItems: 'flex-start', gap: 'var(--spacing-md)',
    }}>
      <i className="fas fa-layer-group" style={{ color: 'var(--color-text-muted)', marginTop: 3 }} aria-hidden="true" />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', flexWrap: 'wrap' }}>
          <label
            htmlFor={`capacity-${node.id}`}
            style={{ fontSize: '0.8125rem', fontWeight: 600, color: 'var(--color-text-primary)' }}
          >
            Max replicas per model
          </label>
          {editing ? (
            <>
              <input
                id={`capacity-${node.id}`}
                type="number"
                min={1}
                value={draft}
                disabled={saving}
                onChange={(e) => setDraft(e.target.value)}
                onKeyDown={onKeyDown}
                autoFocus
                aria-describedby={`capacity-hint-${node.id}`}
                style={{
                  width: 72, padding: '4px 8px', borderRadius: 'var(--radius-sm)',
                  border: '1px solid var(--color-border)', background: 'var(--color-bg-primary)',
                  fontFamily: 'var(--font-mono)', fontSize: '0.8125rem',
                  color: 'var(--color-text-primary)',
                }}
              />
              <button
                className="btn btn-primary btn-sm"
                onClick={save}
                disabled={saving}
                style={{ minHeight: 32 }}
                aria-label="Save replica capacity"
              >
                {saving ? <LoadingSpinner size="xs" /> : <><i className="fas fa-check" /> Save</>}
              </button>
              <button
                className="btn btn-secondary btn-sm"
                onClick={cancel}
                disabled={saving}
                style={{ minHeight: 32 }}
                aria-label="Cancel"
              >
                Cancel
              </button>
            </>
          ) : (
            <>
              <span
                className="cell-mono"
                style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}
              >
                {current}
              </span>
              {isOverride && (
                <span
                  title="This value was set from the UI. It will persist across worker restarts until you click Reset."
                  style={{
                    display: 'inline-block', fontSize: '0.6875rem', padding: '1px 6px',
                    borderRadius: 'var(--radius-sm)', fontWeight: 500,
                    background: 'var(--color-bg-primary)',
                    border: '1px solid var(--color-warning, #d97706)',
                    color: 'var(--color-warning, #d97706)',
                  }}
                >
                  override
                </span>
              )}
              <button
                onClick={() => setEditing(true)}
                aria-label={`Edit replica capacity (currently ${current})`}
                title="Change replica capacity for this node"
                style={{
                  display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
                  minWidth: 32, minHeight: 32, padding: 4, borderRadius: 'var(--radius-sm)',
                  border: '1px solid var(--color-border-subtle)',
                  background: 'transparent', color: 'var(--color-text-muted)', cursor: 'pointer',
                }}
              >
                <i className="fas fa-pencil-alt" />
              </button>
              {isOverride && (
                <button
                  onClick={reset}
                  disabled={resetting}
                  aria-label="Clear admin override and let the worker flag apply"
                  title="Clear override; the worker's --max-replicas-per-model flag will apply on the next re-registration"
                  className="btn btn-secondary btn-sm"
                  style={{ minHeight: 32 }}
                >
                  {resetting ? <LoadingSpinner size="xs" /> : <><i className="fas fa-undo" /> Reset</>}
                </button>
              )}
            </>
          )}
        </div>
        <div
          id={`capacity-hint-${node.id}`}
          style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 4, lineHeight: 1.4 }}
        >
          {isOverride
            ? <>Set from here. <strong>Reset</strong> to use the worker's default.</>
            : <>Saved values stick across worker restarts.</>}
        </div>
      </div>
    </div>

    <div style={{
      display: 'flex', alignItems: 'flex-start', gap: 'var(--spacing-md)',
    }}>
      <i className="fas fa-microchip" style={{ color: 'var(--color-text-muted)', marginTop: 3 }} aria-hidden="true" />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', flexWrap: 'wrap' }}>
          <label
            htmlFor={`vram-budget-${node.id}`}
            style={{ fontSize: '0.8125rem', fontWeight: 600, color: 'var(--color-text-primary)' }}
          >
            VRAM budget
          </label>
          {budgetEditing ? (
            <>
              <input
                id={`vram-budget-${node.id}`}
                type="text"
                value={budgetDraft}
                disabled={budgetSaving}
                placeholder="e.g. 80% or 12GB"
                onChange={(e) => setBudgetDraft(e.target.value)}
                onKeyDown={onBudgetKeyDown}
                autoFocus
                aria-describedby={`vram-budget-hint-${node.id}`}
                style={{
                  width: 140, padding: '4px 8px', borderRadius: 'var(--radius-sm)',
                  border: '1px solid var(--color-border)', background: 'var(--color-bg-primary)',
                  fontFamily: 'var(--font-mono)', fontSize: '0.8125rem',
                  color: 'var(--color-text-primary)',
                }}
              />
              <button
                className="btn btn-primary btn-sm"
                onClick={saveBudget}
                disabled={budgetSaving}
                style={{ minHeight: 32 }}
                aria-label="Save VRAM budget"
              >
                {budgetSaving ? <LoadingSpinner size="xs" /> : <><i className="fas fa-check" /> Save</>}
              </button>
              <button
                className="btn btn-secondary btn-sm"
                onClick={cancelBudget}
                disabled={budgetSaving}
                style={{ minHeight: 32 }}
                aria-label="Cancel"
              >
                Cancel
              </button>
            </>
          ) : (
            <>
              <span
                className="cell-mono"
                style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}
              >
                {hasBudget ? currentBudget : 'none'}
              </span>
              {hasBudget && budgetBytes > 0 && (
                <span
                  className="cell-mono"
                  title="Resolved allocation ceiling out of this node's total VRAM"
                  style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }}
                >
                  {formatVRAM(budgetBytes)} / {formatVRAM(node.total_vram)}
                </span>
              )}
              <button
                onClick={() => setBudgetEditing(true)}
                aria-label={`Edit VRAM budget (currently ${hasBudget ? currentBudget : 'none'})`}
                title="Change the VRAM allocation budget for this node"
                style={{
                  display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
                  minWidth: 32, minHeight: 32, padding: 4, borderRadius: 'var(--radius-sm)',
                  border: '1px solid var(--color-border-subtle)',
                  background: 'transparent', color: 'var(--color-text-muted)', cursor: 'pointer',
                }}
              >
                <i className="fas fa-pencil-alt" />
              </button>
              {hasBudget && (
                <button
                  onClick={clearBudget}
                  disabled={budgetClearing}
                  aria-label="Clear the VRAM budget for this node"
                  title="Clear the VRAM budget; this node's full VRAM becomes available again"
                  className="btn btn-secondary btn-sm"
                  style={{ minHeight: 32 }}
                >
                  {budgetClearing ? <LoadingSpinner size="xs" /> : <><i className="fas fa-undo" /> Clear</>}
                </button>
              )}
            </>
          )}
        </div>
        <div
          id={`vram-budget-hint-${node.id}`}
          style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 4, lineHeight: 1.4 }}
        >
          Cap how much of this node's VRAM the scheduler may allocate. Accepts a percentage or a size (e.g. 80% or 12GB).
        </div>
      </div>
    </div>
    </div>
  )
}
