/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { adminUsersApi } from '../../utils/api'
import { featureState, followDefault, initialOf, permissionPayload, setFeature, storedPermissions } from '../../utils/access'
import { relativeTime } from '../../utils/p2p'
import SideSheet from '../SideSheet'
import Icon from '../Icon'
import { Button, Switch } from '../ui'

const WINDOW_OPTIONS = [
  { value: '1m', label: '1 minute' },
  { value: '5m', label: '5 minutes' },
  { value: '1h', label: '1 hour' },
  { value: '6h', label: '6 hours' },
  { value: '1d', label: '1 day' },
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' },
]

// A feature whose default the administrator may change (configurable) gets a
// second switch under it: "On by default for users". That switch is saved at
// once for every user whose access does not name the feature, so it is not
// part of the Save of this sheet. Under it the sheet says whether this user
// follows that default or has their own value, with "Use default" to go back.
// stateOf(feature) is { on, explicit }.
function FeatureGroup({ title, features, stateOf, onToggle, onAll, onDefault, onFollow, savingDefault }) {
  const { t } = useTranslation('admin')
  if (!features.length) return null
  return (
    <section className="us-section" aria-label={title}>
      <div className="us-section__head">
        <h3 className="us-h3">{title}</h3>
        <div className="us-section__acts">
          <button type="button" className="us-reset" onClick={() => onAll(features, true)}>All</button>
          <button type="button" className="us-reset" onClick={() => onAll(features, false)}>None</button>
        </div>
      </div>
      <ul className="us-switches">
        {features.map(f => {
          const state = stateOf(f)
          return (
          <li key={f.key} className={f.configurable && onDefault ? 'us-switch us-switch--has-default' : 'us-switch'}>
            <span id={`perm-${f.key}`}>{f.label}</span>
            <button
              type="button" role="switch" className="dk-switch" aria-checked={state.on}
              aria-labelledby={`perm-${f.key}`} onClick={() => onToggle(f)}
            />
            {f.configurable && onDefault && (
              <div className="us-switch us-switch--default" data-testid={`feature-default-${f.key}`}>
                <span>
                  <span id={`perm-default-${f.key}`}>{t('users.sheet.defaultOn')}</span>
                  <span className="us-hint" id={`perm-default-${f.key}-hint`}>{t('users.sheet.defaultOnHint')}</span>
                </span>
                <Switch
                  checked={!!f.default} onChange={(v) => onDefault(f.key, v)} disabled={savingDefault === f.key}
                  labelledBy={`perm-default-${f.key}`} describedBy={`perm-default-${f.key}-hint`} testId={`feature-default-switch-${f.key}`}
                />
              </div>
            )}
            {f.configurable && onDefault && (
              <div className="us-switch us-switch--default" data-testid={`feature-source-${f.key}`}>
                <span className="us-hint">{state.explicit ? t('users.sheet.explicit') : t('users.sheet.followsDefault')}</span>
                {state.explicit && (
                  <Button size="sm" variant="ghost" onClick={() => onFollow(f.key)} aria-label={t('users.sheet.useDefaultFor', { feature: f.label })}>
                    {t('users.sheet.useDefault')}
                  </Button>
                )}
              </div>
            )}
          </li>
          )
        })}
      </ul>
    </section>
  )
}

// What one user may use: features, models and limits. Saving makes the same
// calls as before: permissions (the stored keys plus the ones changed here),
// the model allow-list, then the quota rules that were removed, then those
// that changed or are new.
//
// Account actions that do not belong to the access form (reset password, role,
// delete) sit at the bottom and are handled by the page.
//   onFeatureDefault(key, enabled)  saves the default of a configurable
//                                   feature at once; returns a promise
export default function AccessSheet({ user, featureMeta, availableModels, onClose, onSave, addToast, actions, onFeatureDefault }) {
  const { t, i18n } = useTranslation('admin')
  // The stored map is what PUT replaces; edits are the changes made here. The
  // switch of a feature the user does not name shows the default in force,
  // read from featureMeta, so it moves when the default changes in this sheet.
  const [stored] = useState(() => storedPermissions(user))
  const [edits, setEdits] = useState({})
  const [allowedModels, setAllowedModels] = useState(user.allowed_models || { enabled: false, models: [] })
  const [quotas, setQuotas] = useState((user.quotas || []).map(q => ({ ...q, _dirty: false })))
  const [deletedQuotaIds, setDeletedQuotaIds] = useState([])
  const [saving, setSaving] = useState(false)
  const [savingDefault, setSavingDefault] = useState('')
  const setDefault = onFeatureDefault ? async (key, enabled) => {
    setSavingDefault(key)
    try {
      await onFeatureDefault(key, enabled)
    } finally {
      setSavingDefault('')
    }
  } : null

  const apiFeatures = featureMeta?.api_features || []
  const agentFeatures = featureMeta?.agent_features || []
  const generalFeatures = featureMeta?.general_features || []

  const stateOf = (f) => featureState(stored, edits, f.key, f.default)
  const toggleFeature = (f) => setEdits(prev => setFeature(stored, prev, f.key, !featureState(stored, prev, f.key, f.default).on, f.default))
  const setAllFeatures = (features, value) => {
    setEdits(prev => features.reduce((acc, f) => setFeature(stored, acc, f.key, value, f.default), prev))
  }
  const followFeatureDefault = (key) => setEdits(prev => followDefault(stored, prev, key))
  const toggleModel = (model) => {
    setAllowedModels(prev => {
      const models = prev.models || []
      const has = models.includes(model)
      return { ...prev, models: has ? models.filter(m => m !== model) : [...models, model] }
    })
  }
  const setAllModels = (value) => setAllowedModels(prev => ({ ...prev, models: value ? [...(availableModels || [])] : [] }))

  const addQuota = () => {
    setQuotas(prev => [...prev, {
      id: null, model: '', max_requests: null, max_total_tokens: null, window: '1h',
      current_requests: 0, current_total_tokens: 0, _dirty: true, _new: true,
    }])
  }
  const updateQuota = (idx, field, value) => setQuotas(prev => prev.map((q, i) => i === idx ? { ...q, [field]: value, _dirty: true } : q))
  const removeQuota = (idx) => {
    const q = quotas[idx]
    if (q.id && !q._new) setDeletedQuotaIds(prev => [...prev, q.id])
    setQuotas(prev => prev.filter((_, i) => i !== idx))
  }

  const handleSave = async () => {
    setSaving(true)
    try {
      const explicit = permissionPayload(stored, edits)
      await adminUsersApi.setPermissions(user.id, explicit)
      await adminUsersApi.setModels(user.id, allowedModels)
      for (const qid of deletedQuotaIds) await adminUsersApi.deleteQuota(user.id, qid)
      for (const q of quotas) {
        if (q._dirty || q._new) {
          await adminUsersApi.setQuota(user.id, {
            model: q.model,
            max_requests: q.max_requests || null,
            max_total_tokens: q.max_total_tokens || null,
            window: q.window,
          })
        }
      }
      // Refetch so the page holds server-assigned ids and current usage.
      let freshQuotas = []
      try {
        const qData = await adminUsersApi.getQuotas(user.id)
        freshQuotas = Array.isArray(qData) ? qData : qData.quotas || []
      } catch {
        freshQuotas = quotas.map(q => ({ ...q, _dirty: false, _new: false }))
      }
      const effective = {}
      for (const f of [...apiFeatures, ...agentFeatures, ...generalFeatures]) effective[f.key] = featureState(explicit, {}, f.key, f.default).on
      onSave(user.id, effective, allowedModels, freshQuotas, explicit)
      addToast(`Permissions updated for ${user.name || user.email}`, 'success')
      onClose()
    } catch (err) {
      addToast(`Failed to update permissions: ${err.message}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  const who = user.name || user.email
  return (
    <SideSheet
      title={`Access for ${who}`} description={user.name ? user.email : undefined}
      onClose={onClose} closeLabel="Close access sheet" testId="access-sheet" labelId="access-sheet-title"
      footer={<>
        <button type="button" className="dk-btn dk-btn--secondary" onClick={onClose}>Cancel</button>
        <button type="button" className="dk-btn dk-btn--primary" onClick={handleSave} disabled={saving} aria-busy={saving || undefined}>
          {saving ? 'Saving...' : 'Save access'}
        </button>
      </>}
    >
      <div className="us-sheet">
        <p className="us-eyebrow">{t('users.sheet.activity')}</p>
        <dl className="us-stats" data-testid="access-sheet-activity">
          <div className="us-stat">
            <dt>{t('users.columns.lastActive')}</dt>
            <dd>{user.lastActiveAt ? relativeTime(user.lastActiveAt, i18n.language) : t('users.never')}</dd>
          </div>
          <div className="us-stat">
            <dt>{t('users.sheet.apiKeys')}</dt>
            <dd>{user.apiKeyCount ?? 0}</dd>
          </div>
        </dl>

        <p className="us-eyebrow">What they can use</p>
        <FeatureGroup title="API endpoints" features={apiFeatures} stateOf={stateOf} onToggle={toggleFeature} onAll={setAllFeatures} onDefault={setDefault} onFollow={followFeatureDefault} savingDefault={savingDefault} />
        <FeatureGroup title="Agent features" features={agentFeatures} stateOf={stateOf} onToggle={toggleFeature} onAll={setAllFeatures} onDefault={setDefault} onFollow={followFeatureDefault} savingDefault={savingDefault} />
        <FeatureGroup title="Features" features={generalFeatures} stateOf={stateOf} onToggle={toggleFeature} onAll={setAllFeatures} onDefault={setDefault} onFollow={followFeatureDefault} savingDefault={savingDefault} />

        <p className="us-eyebrow">Models</p>
        <section className="us-section" aria-label="Model access">
          <div className="us-switch">
            <span id="perm-restrict">
              Restrict to specific models
              <span className="us-hint">{allowedModels.enabled ? `${(allowedModels.models || []).length} allowed` : 'All models are accessible'}</span>
            </span>
            <button
              type="button" role="switch" className="dk-switch" aria-checked={!!allowedModels.enabled} aria-labelledby="perm-restrict"
              onClick={() => setAllowedModels(prev => ({ ...prev, enabled: !prev.enabled }))}
            />
          </div>
          {allowedModels.enabled && (
            <>
              <div className="us-section__acts us-section__acts--row">
                <button type="button" className="us-reset" onClick={() => setAllModels(true)}>All</button>
                <button type="button" className="us-reset" onClick={() => setAllModels(false)}>None</button>
              </div>
              <ul className="us-models">
                {(availableModels || []).map(m => (
                  <li key={m}>
                    <label className="dk-choice">
                      <input type="checkbox" className="dk-check" checked={(allowedModels.models || []).includes(m)} onChange={() => toggleModel(m)} />
                      <span className="dk-mono us-model-name">{m}</span>
                    </label>
                  </li>
                ))}
                {(!availableModels || availableModels.length === 0) && <li className="us-hint">No models available</li>}
              </ul>
            </>
          )}
        </section>

        <p className="us-eyebrow">Limits</p>
        <section className="us-section" aria-label="Limits">
          {quotas.length === 0 ? (
            <p className="us-hint us-hint--block"><Icon name="infinity" aria-hidden="true" /> No limits: unlimited access</p>
          ) : (
            <ul className="us-quotas">
              {quotas.map((q, idx) => {
                const reqPct = (q.max_requests && !q._new) ? Math.min(100, Math.round(((q.current_requests ?? 0) / q.max_requests) * 100)) : null
                const tokPct = (q.max_total_tokens && !q._new) ? Math.min(100, Math.round(((q.current_total_tokens ?? 0) / q.max_total_tokens) * 100)) : null
                return (
                  <li key={q.id || `new-${idx}`} className="us-quota">
                    <div className="us-quota__head">
                      <div className="dk-select-wrap us-quota__model">
                        <select className="dk-select" aria-label="Model" value={q.model} onChange={e => updateQuota(idx, 'model', e.target.value)}>
                          <option value="">All models</option>
                          {(availableModels || []).map(m => <option key={m} value={m}>{m}</option>)}
                        </select>
                      </div>
                      <div className="dk-select-wrap">
                        <select className="dk-select" aria-label="Window" value={q.window} onChange={e => updateQuota(idx, 'window', e.target.value)}>
                          {WINDOW_OPTIONS.map(w => <option key={w.value} value={w.value}>per {w.label}</option>)}
                        </select>
                      </div>
                      <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" title="Remove rule" aria-label="Remove quota rule" onClick={() => removeQuota(idx)}>
                        <Icon name="trash" />
                      </button>
                    </div>
                    <div className="us-quota__fields">
                      <div className="dk-field">
                        <label className="dk-label" htmlFor={`quota-${idx}-requests`}>Max requests</label>
                        <input
                          id={`quota-${idx}-requests`} type="number" className="dk-input" placeholder="Unlimited" min="0" value={q.max_requests ?? ''}
                          onChange={e => updateQuota(idx, 'max_requests', e.target.value ? parseInt(e.target.value, 10) : null)}
                        />
                        {reqPct !== null && (
                          <span className="us-quota__use">
                            <span className={`dk-progress${reqPct >= 90 ? ' dk-progress--error' : ''}`} role="progressbar" aria-label="Requests used" aria-valuemin="0" aria-valuemax="100" aria-valuenow={reqPct} style={{ '--dk-value': `${reqPct}%` }}><span className="dk-progress-bar" /></span>
                            <span className="dk-mono">{q.current_requests ?? 0} / {q.max_requests}</span>
                          </span>
                        )}
                      </div>
                      <div className="dk-field">
                        <label className="dk-label" htmlFor={`quota-${idx}-tokens`}>Max tokens</label>
                        <input
                          id={`quota-${idx}-tokens`} type="number" className="dk-input" placeholder="Unlimited" min="0" value={q.max_total_tokens ?? ''}
                          onChange={e => updateQuota(idx, 'max_total_tokens', e.target.value ? parseInt(e.target.value, 10) : null)}
                        />
                        {tokPct !== null && (
                          <span className="us-quota__use">
                            <span className={`dk-progress${tokPct >= 90 ? ' dk-progress--error' : ''}`} role="progressbar" aria-label="Tokens used" aria-valuemin="0" aria-valuemax="100" aria-valuenow={tokPct} style={{ '--dk-value': `${tokPct}%` }}><span className="dk-progress-bar" /></span>
                            <span className="dk-mono">{(q.current_total_tokens ?? 0).toLocaleString()} / {q.max_total_tokens.toLocaleString()}</span>
                          </span>
                        )}
                      </div>
                    </div>
                  </li>
                )
              })}
            </ul>
          )}
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm us-add" onClick={addQuota}><Icon name="plus" /> Add rule</button>
        </section>

        {actions}
      </div>
    </SideSheet>
  )
}
