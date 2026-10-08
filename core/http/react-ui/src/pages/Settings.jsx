/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { settingsApi, resourcesApi, brandingApi } from '../utils/api'
import { useBranding } from '../contexts/BrandingContext'
import LoadingSpinner from '../components/LoadingSpinner'
import UnsavedChangesGuard from '../components/UnsavedChangesGuard'
import HomeUndoToast from '../components/home/HomeUndoToast'
import SettingField from '../components/settings/SettingField'
import PendingBar from '../components/settings/PendingBar'
import HistorySheet from '../components/settings/HistorySheet'
import Icon from '../components/Icon'
import { formatBytes } from '../utils/format'
import {
  FIELDS, GROUPS, OLD_SECTIONS, FIELD_BY_KEY, getValue, setValue, isChanged, hasDefault, searchFields, normalizeLoaded,
} from '../utils/settingsSchema'
import {
  pendingChanges, visibleChanges, runChecks, payloadFor, historyEntries,
  loadHistory, appendHistory, clearHistory,
} from '../utils/settingsPending'
import './settings.css'

const UNDO_MS = 10000

// How much memory is in use now, from /api/resources, with the threshold the
// reclaimer would act on. GPU memory when there is a GPU, RAM otherwise.
function MemoryNow({ resources, settings, onRefresh }) {
  let used = 0
  let total = 0
  let label = 'Memory'
  if (resources?.gpus?.length > 0) {
    used = resources.gpus.reduce((n, g) => n + (g.used || 0), 0)
    total = resources.gpus.reduce((n, g) => n + (g.total || 0), 0)
    label = resources.gpus.length > 1 ? `GPU memory (${resources.gpus.length} GPUs)` : 'GPU memory'
  } else if (resources?.ram) {
    used = resources.ram.used || 0
    total = resources.ram.total || 0
    label = 'RAM'
  }
  const pct = total > 0 ? Math.round((used / total) * 100) : 0
  const enabled = !!settings.memory_reclaimer_enabled
  const threshold = Math.round(getValue(FIELD_BY_KEY.memory_reclaimer_threshold, settings) * 100)
  return (
    <div className="st-now" data-testid="settings-memory-now">
      <Icon name="memory" aria-hidden="true" />
      <div className="st-now__text">
        {total > 0 ? (
          <p><strong>{label} now: {formatBytes(used)} of {formatBytes(total)}.</strong>{' '}
            {enabled ? `Eviction starts at ${threshold}%.` : 'Free memory automatically is off.'}</p>
        ) : (
          <p><strong>Memory use is not available.</strong> The resources endpoint returned no reading.</p>
        )}
        {total > 0 && (
          <div className="dk-progress" role="progressbar" aria-label={`${label} in use`} aria-valuemin="0" aria-valuemax="100" aria-valuenow={pct} style={{ '--dk-value': `${pct}%` }}>
            <span className="dk-progress-bar" />
          </div>
        )}
      </div>
      <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label="Refresh memory reading" onClick={onRefresh}>
        <Icon name="refresh" />
      </button>
    </div>
  )
}

export default function Settings() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('admin')
  const branding = useBranding()
  const [settings, setSettings] = useState(null)
  const [initial, setInitial] = useState(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [resources, setResources] = useState(null)
  const [group, setGroup] = useState(GROUPS[0].id)
  const [query, setQuery] = useState('')
  const [showDiff, setShowDiff] = useState(false)
  const [historyOpen, setHistoryOpen] = useState(false)
  const [history, setHistory] = useState(loadHistory)
  const [undo, setUndo] = useState(null)
  const [busyAsset, setBusyAsset] = useState(null)
  const searchRef = useRef(null)

  const fetchResources = useCallback(async () => {
    try { setResources(await resourcesApi.get()) } catch { /* the memory line says it is unavailable */ }
  }, [])

  useEffect(() => {
    let live = true
    settingsApi.get()
      .then(data => { if (live) { const loaded = normalizeLoaded(data); setSettings(loaded); setInitial(structuredClone(loaded)) } })
      .catch(err => { if (live) addToast(t('settings.loadFailed', { message: err.message }), 'error') })
      .finally(() => { if (live) setLoading(false) })
    fetchResources()
    return () => { live = false }
  }, [addToast, t, fetchResources])

  const changes = useMemo(() => pendingChanges(initial, settings), [initial, settings])
  const visible = useMemo(() => visibleChanges(changes), [changes])
  const checks = useMemo(() => (settings ? runChecks(changes, initial, settings) : []), [changes, initial, settings])
  const pendingKeys = useMemo(() => new Set(visible.map(c => c.field.key)), [visible])
  const isDirty = visible.length > 0

  // Closing the diff when nothing is pending, so it does not reopen on the
  // next edit.
  useEffect(() => { if (!isDirty) setShowDiff(false) }, [isDirty])

  const assets = useMemo(() => ({
    state: (kind) => {
      const url = { logo: branding.logoUrl, logo_horizontal: branding.logoHorizontalUrl, favicon: branding.faviconUrl }[kind] || ''
      return { url, custom: !!url && url.startsWith('/branding/asset/'), busy: busyAsset === kind }
    },
    upload: async (kind, file) => {
      setBusyAsset(kind)
      try {
        await brandingApi.uploadAsset(kind, file)
        await branding.refresh()
        addToast('Asset uploaded', 'success')
      } catch (err) {
        addToast(`Upload failed: ${err.message}`, 'error')
      } finally { setBusyAsset(null) }
    },
    reset: async (kind) => {
      setBusyAsset(kind)
      try {
        await brandingApi.deleteAsset(kind)
        await branding.refresh()
        addToast('Reset to default', 'success')
      } catch (err) {
        addToast(`Reset failed: ${err.message}`, 'error')
      } finally { setBusyAsset(null) }
    },
  }), [branding, busyAsset, addToast])

  const apply = async () => {
    if (!isDirty || saving) return
    setSaving(true)
    try {
      const body = payloadFor(changes, settings)
      await settingsApi.save(body)
      const before = initial
      let applied = settings
      // The server makes the real token when it is sent 0; read it back so the
      // field does not keep showing the placeholder.
      if (body.p2p_token === '0') {
        try {
          const fresh = await settingsApi.get()
          applied = { ...settings, p2p_token: fresh.p2p_token || '' }
          setSettings(prev => ({ ...prev, p2p_token: fresh.p2p_token || '' }))
        } catch { /* the field keeps what was typed; a reload shows the token */ }
      }
      setInitial(structuredClone(applied))
      setHistory(appendHistory(historyEntries(changes)))
      // Name and tagline reach the sidebar, footer and tab title without a reload.
      branding.refresh()
      // The undo toast is the confirmation: a second toast would sit on it.
      setUndo({ changes, before, id: Date.now() })
    } catch (err) {
      addToast(t('settings.saveFailed', { message: err.message }), 'error')
    } finally {
      setSaving(false)
    }
  }

  // Undo is a second save of the values the server held before. It is not a
  // rollback: anything the settings changed meanwhile stays changed.
  const undoApply = async () => {
    const entry = undo
    setUndo(null)
    if (!entry) return
    const restore = (obj) => {
      let next = obj
      for (const c of entry.changes) if (!c.silent) next = setValue(c.field, next, c.from)
      if (entry.changes.some(c => c.silent)) {
        next = { ...next, watchdog_enabled: !!(next.watchdog_idle_enabled || next.watchdog_busy_enabled) }
      }
      return next
    }
    const restoredInitial = restore(initial)
    try {
      await settingsApi.save(payloadFor(entry.changes, restoredInitial))
      setInitial(restoredInitial)
      setSettings(restore)
      const reverted = entry.changes.map(c => ({ ...c, from: c.to, to: c.from }))
      setHistory(appendHistory(historyEntries(reverted)))
      branding.refresh()
      addToast('Previous values saved again', 'success')
    } catch (err) {
      addToast(t('settings.saveFailed', { message: err.message }), 'error')
    }
  }

  const discard = () => { setSettings(structuredClone(initial)); setShowDiff(false) }

  const revertEntry = (entry) => {
    const field = FIELD_BY_KEY[entry.key]
    if (!field) return
    setSettings(prev => setValue(field, prev, entry.from))
    setHistoryOpen(false)
    setQuery('')
    setGroup(field.group)
  }

  const onChange = (next) => setSettings(next)

  if (loading) return <div className="page page--wide st-page"><div className="st-loading"><LoadingSpinner size="lg" /></div></div>
  if (!settings) {
    return (
      <div className="page page--wide st-page">
        <div className="dk-empty"><h2 className="dk-empty-title">Settings not available</h2><p className="dk-empty-text">The server did not return its settings.</p></div>
      </div>
    )
  }

  const searching = query.trim().length > 0
  const results = searching ? searchFields(query, settings) : []
  const withDefaults = FIELDS.filter(hasDefault)
  const changedFromDefault = withDefaults.filter(f => isChanged(f, settings)).length
  const groupFields = FIELDS.filter(f => f.group === group)
  const active = GROUPS.find(g => g.id === group)

  const countFor = (g) => FIELDS.filter(f => f.group === g.id && isChanged(f, settings)).length
  const draftIn = (g) => FIELDS.some(f => f.group === g.id && pendingKeys.has(f.key))

  const renderField = (field, showWhere) => (
    <SettingField
      key={field.key} field={field} settings={settings} onChange={onChange} assets={assets}
      showWhere={showWhere} pending={pendingKeys.has(field.key)}
    />
  )

  let lastWas = null
  return (
    <div className="page page--wide st-page" data-testid="settings-page">
      <UnsavedChangesGuard when={isDirty} />

      <header className="st-head">
        <div className="st-head__lead">
          <h1 className="st-title">{t('settings.title')}</h1>
          <p className="st-lede">{t('settings.subtitle')}. Edits wait here until you apply them.</p>
        </div>
      </header>

      <div className="st-bar">
        <div className="dk-input-icon st-search">
          <Icon name="search" className="dk-icon" />
          <input
            ref={searchRef} className="dk-input" type="search" value={query}
            aria-label={`Search all ${FIELDS.length} settings`} placeholder={`Search all ${FIELDS.length} settings`}
            onChange={e => setQuery(e.target.value)}
            onKeyDown={e => { if (e.key === 'Escape' && query) { e.preventDefault(); setQuery('') } }}
          />
        </div>
        <p className="st-bar__count" data-testid="settings-changed-count">
          <span className="dk-mono">{changedFromDefault}</span> changed from default
        </p>
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm st-bar__history" onClick={() => setHistoryOpen(true)}>
          <Icon name="history" /> History
        </button>
      </div>

      <div className="st-layout">
        <nav className="st-groups" aria-label="Setting groups">
          {GROUPS.map(g => {
            const n = countFor(g)
            return (
              <button
                key={g.id} type="button" className="st-group"
                aria-current={!searching && group === g.id ? 'true' : undefined}
                data-group={g.id}
                onClick={() => { setQuery(''); setGroup(g.id) }}
              >
                <span className="st-group__label">{g.label}</span>
                {draftIn(g) && <span className="dk-dot dk-dot--warn" title="Has a draft" role="img" aria-label="Has a draft" />}
                {n > 0 && <span className="st-group__count" title={`${n} changed from default`}>{n}<span className="dk-sr-only"> changed from default</span></span>}
              </button>
            )
          })}
        </nav>

        <div className="st-content">
          {searching ? (
            <section aria-label="Search results">
              <h2 className="st-h2" data-testid="settings-results-title">
                {results.length === 0 ? 'No settings found' : `${results.length} ${results.length === 1 ? 'setting' : 'settings'} for “${query.trim()}”`}
                {results.length > 0 && <span className="st-sub">across all groups</span>}
              </h2>
              {results.length === 0 ? (
                <div className="dk-empty">
                  <p className="dk-empty-text">Nothing matches. Try a word from the setting&apos;s name, its description, its key, or the section it used to be in, such as {OLD_SECTIONS.watchdog}.</p>
                  <button type="button" className="dk-btn dk-btn--secondary" onClick={() => setQuery('')}>Clear search</button>
                </div>
              ) : (
                <div className="st-list" data-testid="settings-results">{results.map(f => renderField(f, true))}</div>
              )}
            </section>
          ) : (
            <section aria-labelledby="st-group-title">
              <h2 className="st-h2" id="st-group-title">{active.label}<span className="st-sub">{active.hint}</span></h2>
              {group === 'memory' && <MemoryNow resources={resources} settings={settings} onRefresh={fetchResources} />}
              {group === 'access' && (
                <p className="st-note">Sign-in, users, invites and per-user API keys are under Users and keys when authentication is on.</p>
              )}
              <div className="st-list">
                {groupFields.map(f => {
                  const head = group === 'agents' && f.was !== lastWas ? OLD_SECTIONS[f.was] : null
                  lastWas = f.was
                  return (
                    <div key={f.key} className="st-item">
                      {head && <h3 className="st-h3">{head}</h3>}
                      {renderField(f, false)}
                    </div>
                  )
                })}
              </div>
            </section>
          )}
        </div>
      </div>

      {isDirty && (
        <PendingBar
          changes={visible} checks={checks} saving={saving} showDiff={showDiff}
          onToggleDiff={() => setShowDiff(v => !v)} onApply={apply} onDiscard={discard}
        />
      )}

      {historyOpen && (
        <HistorySheet
          entries={history} onRevert={revertEntry} onClose={() => setHistoryOpen(false)}
          onClear={() => setHistory(clearHistory())}
        />
      )}

      {undo && (
        <HomeUndoToast
          key={undo.id} duration={UNDO_MS} testId="settings-undo-toast"
          message={`${t('settings.saved')} (${visibleChanges(undo.changes).length} ${visibleChanges(undo.changes).length === 1 ? 'change' : 'changes'}). Undo saves the old values again.`}
          undoLabel="Undo" dismissLabel="Dismiss"
          onUndo={undoApply} onExpire={() => setUndo(null)}
        />
      )}
    </div>
  )
}
