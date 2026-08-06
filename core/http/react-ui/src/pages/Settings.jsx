import { useState, useEffect, useCallback, useRef } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { settingsApi, resourcesApi, brandingApi } from '../utils/api'
import { useBranding } from '../contexts/BrandingContext'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'
import UnsavedChangesGuard from '../components/UnsavedChangesGuard'
import SearchableModelSelect from '../components/SearchableModelSelect'
import { CAP_CHAT } from '../utils/capabilities'
import Toggle from '../components/Toggle'
import SettingRow from '../components/SettingRow'
import { formatBytes, percentColor } from '../utils/format'

const SECTIONS = [
  { id: 'branding', icon: 'fa-palette', color: 'var(--color-primary)' },
  { id: 'watchdog', icon: 'fa-shield-halved', color: 'var(--color-primary)' },
  { id: 'memory', icon: 'fa-memory', color: 'var(--color-accent)' },
  { id: 'backends', icon: 'fa-cogs', color: 'var(--color-accent)' },
  { id: 'performance', icon: 'fa-gauge-high', color: 'var(--color-success)' },
  { id: 'tracing', icon: 'fa-bug', color: 'var(--color-warning)' },
  { id: 'api', icon: 'fa-globe', color: 'var(--color-warning)' },
  { id: 'p2p', icon: 'fa-network-wired', color: 'var(--color-accent)' },
  { id: 'galleries', icon: 'fa-images', color: 'var(--color-accent)' },
  { id: 'apikeys', icon: 'fa-key', color: 'var(--color-error)' },
  { id: 'agents', icon: 'fa-tasks', color: 'var(--color-primary)' },
  { id: 'agentpool', icon: 'fa-robot', color: 'var(--color-primary)' },
  { id: 'assistant', icon: 'fa-user-shield', color: 'var(--color-accent)' },
  { id: 'distributed', icon: 'fa-server', color: 'var(--color-accent)' },
  { id: 'responses', icon: 'fa-database', color: 'var(--color-accent)' },
]

const BRANDING_ASSETS = [
  { kind: 'logo', label: 'Square Logo', description: 'Used as the icon-sized logo in the sidebar and on small screens.' },
  { kind: 'logo_horizontal', label: 'Horizontal Logo', description: 'Wide logo shown in the sidebar header on desktop.' },
  { kind: 'favicon', label: 'Favicon', description: 'Browser tab icon. PNG, SVG, or ICO. Browsers cache the favicon — a hard reload may be needed.' },
]

export default function Settings() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('admin')
  const [settings, setSettings] = useState(null)
  const [initialSettings, setInitialSettings] = useState(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [resources, setResources] = useState(null)
  const [activeSection, setActiveSection] = useState('branding')
  const branding = useBranding()
  const [brandingBusy, setBrandingBusy] = useState(null) // null | kind for asset ops in flight
  const contentRef = useRef(null)
  const sectionRefs = useRef({})

  useEffect(() => { fetchSettings() }, [])

  const fetchSettings = async () => {
    try {
      const data = await settingsApi.get()
      setSettings(data)
      setInitialSettings(structuredClone(data))
    } catch (err) {
      addToast(`Failed to load settings: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }

  const fetchResources = useCallback(async () => {
    try {
      const data = await resourcesApi.get()
      setResources(data)
    } catch (_e) { /* ignore */ }
  }, [])

  const handleSave = async () => {
    setSaving(true)
    try {
      await settingsApi.save(settings)
      setInitialSettings(structuredClone(settings))
      // Refresh branding context so name/tagline updates propagate to the
      // sidebar, footer, and document title without a full reload.
      branding.refresh()
      addToast('Settings saved successfully', 'success')
    } catch (err) {
      addToast(`Save failed: ${err.message}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  const handleBrandingUpload = async (kind, file) => {
    if (!file) return
    setBrandingBusy(kind)
    try {
      await brandingApi.uploadAsset(kind, file)
      await branding.refresh()
      addToast('Asset uploaded', 'success')
    } catch (err) {
      addToast(`Upload failed: ${err.message}`, 'error')
    } finally {
      setBrandingBusy(null)
    }
  }

  const handleBrandingReset = async (kind) => {
    setBrandingBusy(kind)
    try {
      await brandingApi.deleteAsset(kind)
      await branding.refresh()
      addToast('Reset to default', 'success')
    } catch (err) {
      addToast(`Reset failed: ${err.message}`, 'error')
    } finally {
      setBrandingBusy(null)
    }
  }

  const brandingAssetUrl = (kind) => {
    switch (kind) {
      case 'logo': return branding.logoUrl
      case 'logo_horizontal': return branding.logoHorizontalUrl
      case 'favicon': return branding.faviconUrl
      default: return ''
    }
  }

  const update = (key, value) => {
    setSettings(prev => ({ ...prev, [key]: value }))
  }

  const scrollTo = (id) => {
    setActiveSection(id)
    sectionRefs.current[id]?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  // Track which section is visible on scroll
  useEffect(() => {
    const container = contentRef.current
    if (!container) return
    const onScroll = () => {
      const containerTop = container.getBoundingClientRect().top
      let closest = SECTIONS[0].id
      let closestDist = Infinity
      for (const s of SECTIONS) {
        const el = sectionRefs.current[s.id]
        if (el) {
          const dist = Math.abs(el.getBoundingClientRect().top - containerTop - 8)
          if (dist < closestDist) { closestDist = dist; closest = s.id }
        }
      }
      setActiveSection(closest)
    }
    container.addEventListener('scroll', onScroll, { passive: true })
    return () => container.removeEventListener('scroll', onScroll)
  }, [loading])

  if (loading) return <div className="page page--medium loading-center"><LoadingSpinner size="lg" /></div>
  if (!settings) return <div className="page page--medium"><div className="empty-state"><p className="empty-state-text">Settings not available</p></div></div>

  const isDirty = settings && initialSettings && JSON.stringify(settings) !== JSON.stringify(initialSettings)
  const watchdogEnabled = settings.watchdog_idle_enabled || settings.watchdog_busy_enabled

  return (
    <div className="page page--medium p-0">
      <UnsavedChangesGuard when={isDirty} />
      {/* Header */}
      <div className="set-head">
        <PageHeader
          title={t('settings.title')}
          supporting={t('settings.subtitle')}
          actions={
            <button className={`btn ${isDirty ? 'btn-primary' : 'btn-secondary'} fas fa-save`} onClick={handleSave} disabled={saving || !isDirty}>
              {saving ? <><LoadingSpinner size="sm" /> Saving...</> : <><i className="fas fa-floppy-disk" aria-hidden="true" /> {isDirty ? 'Save Changes' : 'Saved'}</>}
            </button>
          }
        />
      </div>

      {/* Two-column layout */}
      <div className="set-layout">
        {/* Sidebar nav */}
        <nav className="set-rail">
          {SECTIONS.map(s => (
            <button
              key={s.id}
              onClick={() => scrollTo(s.id)}
              className={`set-rail__item${activeSection === s.id ? ' set-rail__item--on' : ''}`}
            >
              <i
                className={`fas ${s.icon} set-rail__icon`}
                style={activeSection === s.id ? { color: s.color } : undefined}
              />
              {t(`settings.sections.${s.id}`)}
            </button>
          ))}
        </nav>

        {/* Content area */}
        <div
          ref={contentRef}
          className="set-content"
        >
          {/* Branding / Whitelabeling */}
          <div ref={el => sectionRefs.current.branding = el} className="mb-xl">
            <h3 className="panel-title">
              <i className="fas fa-palette text-primary" /> Branding
            </h3>
            <div className="card">
              <SettingRow label="Instance Name" description="Replaces &quot;LocalAI&quot; in the sidebar, footer, and browser tab. Visible on the login screen.">
                <input
                  className="input col-w-240"
                  value={settings.instance_name || ''}
                  onChange={(e) => update('instance_name', e.target.value)}
                  placeholder="LocalAI"
                />
              </SettingRow>
              <SettingRow label="Tagline" description="Optional short subtitle shown beneath the instance name.">
                <input
                  className="input col-w-240"
                  value={settings.instance_tagline || ''}
                  onChange={(e) => update('instance_tagline', e.target.value)}
                  placeholder="(none)"
                />
              </SettingRow>
              {BRANDING_ASSETS.map(asset => {
                const url = brandingAssetUrl(asset.kind)
                const isCustom = url && url.startsWith('/branding/asset/')
                const busy = brandingBusy === asset.kind
                return (
                  <SettingRow key={asset.kind} label={asset.label} description={asset.description}>
                    <div className="hstack">
                      <div className="set-asset">
                        {url ? (
                          <img src={url} alt="" />
                        ) : (
                          <i className="fas fa-image text-muted" />
                        )}
                      </div>
                      <label className="btn btn-secondary m-0" style={{ cursor: busy ? 'wait' : 'pointer' }}>
                        <i className="fas fa-upload" /> {busy ? 'Uploading…' : 'Upload'}
                        <input
                          type="file"
                          accept="image/png,image/jpeg,image/svg+xml,image/webp,image/x-icon,.ico"
                          className="hidden"
                          disabled={busy}
                          onChange={(e) => {
                            const file = e.target.files?.[0]
                            e.target.value = ''
                            if (file) handleBrandingUpload(asset.kind, file)
                          }}
                        />
                      </label>
                      {isCustom && (
                        <button
                          type="button"
                          className="btn btn-secondary"
                          onClick={() => handleBrandingReset(asset.kind)}
                          disabled={busy}
                          title="Revert to bundled default"
                        >
                          <i className="fas fa-undo" /> Reset
                        </button>
                      )}
                    </div>
                  </SettingRow>
                )
              })}
            </div>
          </div>

          {/* Watchdog */}
          <div ref={el => sectionRefs.current.watchdog = el} className="mb-xl">
            <h3 className="panel-title">
              <i className="fas fa-shield-halved text-primary" /> Watchdog
            </h3>
            <div className="card">
              <SettingRow label="Enable Watchdog" description="Automatically monitor and manage backend processes">
                <Toggle checked={settings.watchdog_idle_enabled || settings.watchdog_busy_enabled} onChange={(v) => { update('watchdog_idle_enabled', v); update('watchdog_busy_enabled', v); update('watchdog_enabled', v) }} />
              </SettingRow>
              <SettingRow label="Enable Idle Check" description="Automatically stop backends that have been idle too long">
                <Toggle checked={settings.watchdog_idle_enabled} onChange={(v) => update('watchdog_idle_enabled', v)} disabled={!watchdogEnabled} />
              </SettingRow>
              <SettingRow label="Idle Timeout" description="Time before an idle backend is stopped (e.g. 15m, 1h)">
                <input className="input col-w-120" value={settings.watchdog_idle_timeout || ''} onChange={(e) => update('watchdog_idle_timeout', e.target.value)} placeholder="15m" disabled={!settings.watchdog_idle_enabled} />
              </SettingRow>
              <SettingRow label="Enable Busy Check" description="Stop stuck/busy processes that exceed timeout">
                <Toggle checked={settings.watchdog_busy_enabled} onChange={(v) => update('watchdog_busy_enabled', v)} disabled={!watchdogEnabled} />
              </SettingRow>
              <SettingRow label="Busy Timeout" description="Time before a busy backend is stopped (e.g. 5m)">
                <input className="input col-w-120" value={settings.watchdog_busy_timeout || ''} onChange={(e) => update('watchdog_busy_timeout', e.target.value)} placeholder="5m" disabled={!settings.watchdog_busy_enabled} />
              </SettingRow>
              <SettingRow label="Check Interval" description="How often the watchdog checks backends (e.g. 2s)">
                <input className="input col-w-120" value={settings.watchdog_interval || ''} onChange={(e) => update('watchdog_interval', e.target.value)} placeholder="2s" />
              </SettingRow>
              <SettingRow label="Force Eviction When Busy" description="Allow model eviction even during active API calls">
                <Toggle checked={settings.force_eviction_when_busy} onChange={(v) => update('force_eviction_when_busy', v)} />
              </SettingRow>
              <SettingRow label="Size-Aware Eviction" description="Evict the largest loaded model first instead of the least-recently-used one">
                <Toggle checked={settings.size_aware_eviction} onChange={(v) => update('size_aware_eviction', v)} />
              </SettingRow>
              <SettingRow label="LRU Eviction Max Retries" description="Maximum retries waiting for busy models before eviction">
                <input className="input col-w-120" type="number" value={settings.lru_eviction_max_retries ?? ''} onChange={(e) => update('lru_eviction_max_retries', parseInt(e.target.value) || 0)} placeholder="30" />
              </SettingRow>
              <SettingRow label="LRU Eviction Retry Interval" description="Wait between eviction retries (e.g. 1s)">
                <input className="input col-w-120" value={settings.lru_eviction_retry_interval || ''} onChange={(e) => update('lru_eviction_retry_interval', e.target.value)} placeholder="1s" />
              </SettingRow>
            </div>
          </div>

          {/* Memory Reclaimer */}
          <div ref={el => sectionRefs.current.memory = el} className="mb-xl">
            <div className="hstack hstack--between mb-md">
              <h3 className="hstack text-lg fw-semibold">
                <i className="fas fa-memory text-accent" /> Memory Reclaimer
              </h3>
              <button className="btn btn-secondary btn-sm" onClick={fetchResources} title="Refresh resource status">
                <i className="fas fa-sync-alt" />
              </button>
            </div>
            <div className="card">
              {resources && (
                <div className="set-meters mb-sm">
                  {resources.gpus?.length > 0 ? resources.gpus.map((gpu, i) => {
                    const usedPct = gpu.total > 0 ? Math.round((gpu.used / gpu.total) * 100) : 0
                    return (
                      <div key={i} className="set-meter">
                        <span className="set-meter__label">GPU {i}</span>
                        <div className="set-meter__track">
                          <div className="set-meter__fill" style={{ width: `${usedPct}%`, background: percentColor(usedPct) }} />
                        </div>
                        <span className="set-meter__value" style={{ color: percentColor(usedPct) }}>{usedPct}%</span>
                        <span className="text-muted">{formatBytes(gpu.used)} / {formatBytes(gpu.total)}</span>
                      </div>
                    )
                  }) : (
                    <div className="hstack hstack--xs">
                      <span className="set-meter__label">RAM</span>
                      {resources.ram && (() => {
                        const usedPct = resources.ram.total > 0 ? Math.round((resources.ram.used / resources.ram.total) * 100) : 0
                        return (
                          <>
                            <div className="set-meter__track">
                              <div className="set-meter__fill" style={{ width: `${usedPct}%`, background: percentColor(usedPct) }} />
                            </div>
                            <span className="set-meter__value" style={{ color: percentColor(usedPct) }}>{usedPct}%</span>
                            <span className="text-muted">{formatBytes(resources.ram.used)} / {formatBytes(resources.ram.total)}</span>
                          </>
                        )
                      })()}
                    </div>
                  )}
                </div>
              )}
              <SettingRow label="Enable Memory Reclaimer" description="Evict backends when memory usage exceeds threshold">
                <Toggle checked={settings.memory_reclaimer_enabled} onChange={(v) => update('memory_reclaimer_enabled', v)} />
              </SettingRow>
              <SettingRow label="Memory Threshold (%)" description="Eviction triggers when usage exceeds this percentage">
                <div className="hstack">
                  <input type="range" min="50" max="100" value={Math.round((settings.memory_reclaimer_threshold || 0.8) * 100)} onChange={(e) => update('memory_reclaimer_threshold', parseInt(e.target.value) / 100)} disabled={!settings.memory_reclaimer_enabled} className="col-w-120" />
                  <span className="set-pct" style={{ color: percentColor(Math.round((settings.memory_reclaimer_threshold || 0.8) * 100)) }}>
                    {Math.round((settings.memory_reclaimer_threshold || 0.8) * 100)}%
                  </span>
                </div>
              </SettingRow>
            </div>
          </div>

          {/* Backends */}
          <div ref={el => sectionRefs.current.backends = el} className="mb-xl">
            <h3 className="panel-title">
              <i className="fas fa-cogs text-accent" /> Backend Management
            </h3>
            <div className="card">
              <SettingRow label="Max Active Backends" description="Maximum models to keep loaded simultaneously (0 = unlimited)">
                <input className="input col-w-120" type="number" value={settings.max_active_backends ?? ''} onChange={(e) => update('max_active_backends', parseInt(e.target.value) || 0)} placeholder="0" />
              </SettingRow>
              <SettingRow label="Auto-upgrade Backends" description="Automatically upgrade backends when new versions are detected">
                <Toggle checked={settings.auto_upgrade_backends} onChange={(v) => update('auto_upgrade_backends', v)} />
              </SettingRow>
              <SettingRow label="Prefer Development Backends" description="Default to showing development backend versions in the backends gallery">
                <Toggle checked={settings.prefer_development_backends} onChange={(v) => update('prefer_development_backends', v)} />
              </SettingRow>
            </div>
          </div>

          {/* Performance */}
          <div ref={el => sectionRefs.current.performance = el} className="mb-xl">
            <h3 className="panel-title">
              <i className="fas fa-gauge-high text-success" /> Performance
            </h3>
            <div className="card">
              <SettingRow label="Default Threads" description="CPU threads for inference (0 = auto-detect)">
                <input className="input col-w-120" type="number" value={settings.threads ?? ''} onChange={(e) => update('threads', parseInt(e.target.value) || 0)} placeholder="0" />
              </SettingRow>
              <SettingRow label="Default Context Size" description="Default context window size for models">
                <input className="input col-w-120" type="number" value={settings.context_size ?? ''} onChange={(e) => update('context_size', parseInt(e.target.value) || 0)} placeholder="2048" />
              </SettingRow>
              <SettingRow label="VRAM Budget" description="Cap VRAM used for model allocation on this node. Percentage (e.g. 80%) or absolute (e.g. 12GB). Empty uses all detected VRAM.">
                <input className="input col-w-120" type="text" value={settings.vram_budget ?? ''} onChange={(e) => update('vram_budget', e.target.value)} placeholder="e.g. 80% or 12GB" />
              </SettingRow>
              <SettingRow label="F16 Precision" description="Use 16-bit floating point for reduced memory usage">
                <Toggle checked={settings.f16} onChange={(v) => update('f16', v)} />
              </SettingRow>
              <SettingRow label="Debug Mode" description="Enable verbose debug logging">
                <Toggle checked={settings.debug} onChange={(v) => update('debug', v)} />
              </SettingRow>
            </div>
          </div>

          {/* Tracing */}
          <div ref={el => sectionRefs.current.tracing = el} className="mb-xl">
            <h3 className="panel-title">
              <i className="fas fa-bug text-warning" /> Tracing
            </h3>
            <div className="card">
              <SettingRow label="Enable Tracing" description="Record API requests, responses, and backend operations for debugging">
                <Toggle checked={settings.enable_tracing} onChange={(v) => update('enable_tracing', v)} />
              </SettingRow>
              <SettingRow label="Max Items" description="Maximum number of trace items to retain (0 = unlimited)">
                <input className="input col-w-120" type="number" value={settings.tracing_max_items ?? ''} onChange={(e) => update('tracing_max_items', parseInt(e.target.value) || 0)} placeholder="100" disabled={!settings.enable_tracing} />
              </SettingRow>
              <SettingRow label="Max Body Bytes" description="Per-field cap (bytes) for captured request/response bodies and backend trace Data fields. Prevents large LLM histories or TTS audio snippets from locking the Traces UI. 0 = uncapped.">
                <input className="input col-w-120" type="number" value={settings.tracing_max_body_bytes ?? ''} onChange={(e) => update('tracing_max_body_bytes', parseInt(e.target.value) || 0)} placeholder="65536" disabled={!settings.enable_tracing} />
              </SettingRow>
              <SettingRow label="Enable Backend Logging" description="Capture backend process output per model (without requiring debug mode)">
                <Toggle checked={settings.enable_backend_logging} onChange={(v) => update('enable_backend_logging', v)} />
              </SettingRow>
            </div>
          </div>

          {/* API & CORS */}
          <div ref={el => sectionRefs.current.api = el} className="mb-xl">
            <h3 className="panel-title">
              <i className="fas fa-globe text-warning" /> API & CORS
            </h3>
            <div className="card">
              <SettingRow label="Enable CORS" description="Enable Cross-Origin Resource Sharing">
                <Toggle checked={settings.cors} onChange={(v) => update('cors', v)} />
              </SettingRow>
              <SettingRow label="CORS Allow Origins" description="Comma-separated list of allowed origins">
                <input className="input col-w-200" value={settings.cors_allow_origins || ''} onChange={(e) => update('cors_allow_origins', e.target.value)} placeholder="*" disabled={!settings.cors} />
              </SettingRow>
              <SettingRow label="Enable CSRF Protection" description="Enable Cross-Site Request Forgery protection">
                <Toggle checked={settings.csrf} onChange={(v) => update('csrf', v)} />
              </SettingRow>
            </div>
          </div>

          {/* P2P */}
          <div ref={el => sectionRefs.current.p2p = el} className="mb-xl">
            <h3 className="panel-title">
              <i className="fas fa-network-wired text-accent" /> P2P Network
            </h3>
            <div className="card">
              <SettingRow label="P2P Token" description="Generate a new token or paste an existing one to join a network">
                <div className="hstack hstack--xs">
                  <input className="input col-w-200" value={settings.p2p_token || ''} onChange={(e) => update('p2p_token', e.target.value)} placeholder="No token set" />
                  <button className="btn btn-primary btn-sm" onClick={() => update('p2p_token', '0')} title="Generate a new P2P token (applied on save)">
                    <i className="fas fa-rotate" /> Generate
                  </button>
                  {settings.p2p_token && (
                    <button className="btn btn-secondary btn-sm text-error" onClick={() => update('p2p_token', '')} title="Clear token (disables P2P on save)">
                      <i className="fas fa-times" />
                    </button>
                  )}
                </div>
              </SettingRow>
              <SettingRow label="P2P Network ID" description="Network identifier for grouping instances">
                <input className="input col-w-200" value={settings.p2p_network_id || ''} onChange={(e) => update('p2p_network_id', e.target.value)} placeholder="Network ID" />
              </SettingRow>
              <SettingRow label="Federated Mode" description="Enable federated instance mode for load balancing">
                <Toggle checked={settings.federated} onChange={(v) => update('federated', v)} />
              </SettingRow>
            </div>
          </div>

          {/* Galleries */}
          <div ref={el => sectionRefs.current.galleries = el} className="mb-xl">
            <h3 className="panel-title">
              <i className="fas fa-images text-accent" /> Galleries
            </h3>
            <div className="card">
              <SettingRow label="Autoload Galleries" description="Automatically load model galleries on startup">
                <Toggle checked={settings.autoload_galleries} onChange={(v) => update('autoload_galleries', v)} />
              </SettingRow>
              <SettingRow label="Autoload Backend Galleries" description="Automatically load backend galleries on startup">
                <Toggle checked={settings.autoload_backend_galleries} onChange={(v) => update('autoload_backend_galleries', v)} />
              </SettingRow>
              <div className="mt-sm">
                <label className="form-label">Model Galleries (JSON)</label>
                <textarea
                  className="textarea text-mono text-sm"
                  value={settings.galleries_json || (settings.galleries ? JSON.stringify(settings.galleries, null, 2) : '')}
                  onChange={(e) => update('galleries_json', e.target.value)}
                  rows={4}
                  placeholder={'[\n  { "url": "https://...", "name": "my-gallery", "mirrors": ["https://fallback/..."] }\n]'}
                />
              </div>
              <div className="mt-sm">
                <label className="form-label">Backend Galleries (JSON)</label>
                <textarea
                  className="textarea text-mono text-sm"
                  value={settings.backend_galleries_json || (settings.backend_galleries ? JSON.stringify(settings.backend_galleries, null, 2) : '')}
                  onChange={(e) => update('backend_galleries_json', e.target.value)}
                  rows={4}
                  placeholder={'[\n  { "url": "https://...", "name": "my-backends", "mirrors": ["https://fallback/..."] }\n]'}
                />
              </div>
            </div>
          </div>

          {/* API Keys */}
          <div ref={el => sectionRefs.current.apikeys = el} className="mb-xl">
            <h3 className="panel-title">
              <i className="fas fa-key text-error" /> API Keys
            </h3>
            <div className="card">
              <div className="text-note mb-sm">
                <i className="fas fa-exclamation-triangle text-warning icon-before" />
                API keys are sensitive. One key per line or comma-separated.
              </div>
              <textarea
                className="textarea text-mono text-sm"
                value={settings.api_keys?.join('\n') || (typeof settings.api_keys_text === 'string' ? settings.api_keys_text : '')}
                onChange={(e) => update('api_keys_text', e.target.value)}
                rows={4}
                placeholder="sk-key-1&#10;sk-key-2"
              />
            </div>
          </div>

          {/* Agent Jobs */}
          <div ref={el => sectionRefs.current.agents = el} className="mb-xl">
            <h3 className="panel-title">
              <i className="fas fa-tasks text-primary" /> Agent Jobs
            </h3>
            <div className="card">
              <SettingRow label="Job Retention Days" description="Number of days to keep job history">
                <input className="input col-w-120" type="number" value={settings.agent_job_retention_days ?? ''} onChange={(e) => update('agent_job_retention_days', parseInt(e.target.value) || 0)} placeholder="30" />
              </SettingRow>
            </div>
          </div>

          {/* Agent Pool */}
          <div ref={el => sectionRefs.current.agentpool = el} className="mb-xl">
            <h3 className="panel-title">
              <i className="fas fa-robot text-primary" /> Agent Pool
            </h3>
            <div className="card">
              <SettingRow label="Enabled" description="Enable or disable the agent pool feature (requires restart)">
                <Toggle checked={settings.agent_pool_enabled ?? true} onChange={(v) => update('agent_pool_enabled', v)} />
              </SettingRow>
              <SettingRow label="Default Model" description="Default LLM model for agents">
                <SearchableModelSelect value={settings.agent_pool_default_model || ''} onChange={(v) => update('agent_pool_default_model', v)} capability={CAP_CHAT} placeholder="e.g. gpt-4" />
              </SettingRow>
              <SettingRow label="Embedding Model" description="Model used for knowledge base embeddings">
                <SearchableModelSelect value={settings.agent_pool_embedding_model || ''} onChange={(v) => update('agent_pool_embedding_model', v)} placeholder="granite-embedding-107m-multilingual" />
              </SettingRow>
              <SettingRow label="Max Chunking Size" description="Maximum chunk size for knowledge base documents (default: 400)">
                <input className="input col-w-120" type="number" value={settings.agent_pool_max_chunking_size ?? 400} onChange={(e) => update('agent_pool_max_chunking_size', parseInt(e.target.value, 10) || 0)} min={0} />
              </SettingRow>
              <SettingRow label="Chunk Overlap" description="Overlap between chunks for knowledge base documents (default: 0)">
                <input className="input col-w-120" type="number" value={settings.agent_pool_chunk_overlap ?? 0} onChange={(e) => update('agent_pool_chunk_overlap', parseInt(e.target.value, 10) || 0)} min={0} />
              </SettingRow>
              <SettingRow label="Enable Logs" description="Enable agent logging (requires restart)">
                <Toggle checked={settings.agent_pool_enable_logs ?? false} onChange={(v) => update('agent_pool_enable_logs', v)} />
              </SettingRow>
              <SettingRow label="Collection DB Path" description="Database path for agent collections">
                <input className="input w-280" value={settings.agent_pool_collection_db_path || ''} onChange={(e) => update('agent_pool_collection_db_path', e.target.value)} placeholder="Leave empty for default" />
              </SettingRow>
              <SettingRow label="Vector Engine" description="Backend store for collection embeddings. chromem is in-memory; postgres uses pgvector and requires Database URL.">
                <select
                  className="input w-160"
                  value={settings.agent_pool_vector_engine || 'chromem'}
                  onChange={(e) => update('agent_pool_vector_engine', e.target.value)}
                >
                  <option value="chromem">chromem</option>
                  <option value="postgres">postgres</option>
                </select>
              </SettingRow>
              <SettingRow label="Database URL" description="PostgreSQL DSN used when Vector Engine is postgres (e.g. postgres://user:pass@host:5432/db).">
                <input
                  className="input col-w-320"
                  value={settings.agent_pool_database_url || ''}
                  onChange={(e) => update('agent_pool_database_url', e.target.value)}
                  placeholder="postgres://..."
                  disabled={(settings.agent_pool_vector_engine || 'chromem') !== 'postgres'}
                />
              </SettingRow>
              <SettingRow label="Agent Hub URL" description="Override the default https://agenthub.localai.io endpoint (custom or self-hosted hub).">
                <input
                  className="input col-w-320"
                  value={settings.agent_pool_agent_hub_url || ''}
                  onChange={(e) => update('agent_pool_agent_hub_url', e.target.value)}
                  placeholder="https://agenthub.localai.io"
                />
              </SettingRow>
            </div>
          </div>

          {/* LocalAI Assistant */}
          <div ref={el => sectionRefs.current.assistant = el} className="mb-xl">
            <h3 className="panel-title">
              <i className="fas fa-user-shield text-accent" /> LocalAI Assistant
            </h3>
            <div className="card">
              <SettingRow label="Enabled" description="Allow admins to opt chat sessions into the in-process admin tool surface. Disabling refuses new requests with the localai_assistant flag; takes effect without restart.">
                <Toggle checked={settings.localai_assistant_enabled ?? true} onChange={(v) => update('localai_assistant_enabled', v)} />
              </SettingRow>
            </div>
          </div>

          {/* Distributed mode */}
          <div ref={el => sectionRefs.current.distributed = el} className="mb-xl">
            <h3 className="panel-title">
              <i className="fas fa-server text-accent" /> {t('settings.sections.distributed')}
            </h3>
            <div className="card">
              <SettingRow label="Disk headroom check" description="Reject worker nodes that lack free space to store the model, at scheduling time rather than partway through staging. Free space is measured on each worker's models filesystem and compared against the model's own size plus a small margin. Turning this off restores selection that ignores free disk; the check still runs and warns when it would have rejected every node. Takes effect without restart.">
                <Toggle checked={settings.distributed_disk_headroom_check ?? true} onChange={(v) => update('distributed_disk_headroom_check', v)} />
              </SettingRow>
            </div>
          </div>

          {/* Open Responses */}
          <div ref={el => sectionRefs.current.responses = el} className="mb-xl">
            <h3 className="panel-title">
              <i className="fas fa-database text-accent" /> Open Responses
            </h3>
            <div className="card">
              <SettingRow label="Response Store TTL" description="Time-to-live for stored responses (e.g. 1h, 30m, 0 = no expiration)">
                <input className="input col-w-120" value={settings.open_responses_store_ttl || ''} onChange={(e) => update('open_responses_store_ttl', e.target.value)} placeholder="1h" />
              </SettingRow>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
