import { useState, useEffect, useCallback } from 'react'
import { useOutletContext } from 'react-router-dom'
import { settingsApi, resourcesApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'
import { formatBytes, percentColor } from '../utils/format'

function Toggle({ checked, onChange, disabled }) {
  return (
    <label style={{
      position: 'relative', display: 'inline-block', width: 40, height: 22, cursor: disabled ? 'not-allowed' : 'pointer',
      opacity: disabled ? 0.5 : 1,
    }}>
      <input
        type="checkbox"
        checked={checked || false}
        onChange={(e) => onChange(e.target.checked)}
        disabled={disabled}
        style={{ display: 'none' }}
      />
      <span style={{
        position: 'absolute', inset: 0, borderRadius: 22,
        background: checked ? 'var(--color-primary)' : 'var(--color-bg-tertiary)',
        transition: 'background 200ms',
      }}>
        <span style={{
          position: 'absolute', top: 2, left: checked ? 20 : 2,
          width: 18, height: 18, borderRadius: '50%',
          background: '#fff', transition: 'left 200ms',
        }} />
      </span>
    </label>
  )
}

function SettingRow({ label, description, children }) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', justifyContent: 'space-between',
      padding: 'var(--spacing-sm) 0',
      borderBottom: '1px solid var(--color-border-subtle)',
    }}>
      <div style={{ flex: 1, marginRight: 'var(--spacing-md)' }}>
        <div style={{ fontSize: '0.875rem', fontWeight: 500 }}>{label}</div>
        {description && <div style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)', marginTop: 2 }}>{description}</div>}
      </div>
      <div style={{ flexShrink: 0 }}>{children}</div>
    </div>
  )
}

function Section({ icon, iconColor, title, children, defaultOpen = true }) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
      <div
        onClick={() => setOpen(!open)}
        style={{
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          cursor: 'pointer', userSelect: 'none',
        }}
      >
        <h3 style={{ fontSize: '1rem', fontWeight: 600, display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
          <i className={`fas ${icon}`} style={{ color: iconColor }} /> {title}
        </h3>
        <i className={`fas fa-chevron-${open ? 'up' : 'down'}`} style={{ color: 'var(--color-text-muted)', fontSize: '0.75rem' }} />
      </div>
      {open && <div style={{ marginTop: 'var(--spacing-md)' }}>{children}</div>}
    </div>
  )
}

export default function Settings() {
  const { addToast } = useOutletContext()
  const [settings, setSettings] = useState(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [resources, setResources] = useState(null)

  useEffect(() => {
    fetchSettings()
  }, [])

  const fetchSettings = async () => {
    try {
      const data = await settingsApi.get()
      setSettings(data)
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
      addToast('Settings saved successfully', 'success')
    } catch (err) {
      addToast(`Save failed: ${err.message}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  const update = (key, value) => {
    setSettings(prev => ({ ...prev, [key]: value }))
  }

  if (loading) return <div className="page" style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}><LoadingSpinner size="lg" /></div>

  if (!settings) return <div className="page"><div className="empty-state"><p className="empty-state-text">Settings not available</p></div></div>

  const watchdogEnabled = settings.watchdog_idle || settings.watchdog_busy

  return (
    <div className="page" style={{ maxWidth: '800px' }}>
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        marginBottom: 'var(--spacing-lg)',
      }}>
        <div>
          <h1 className="page-title">Settings</h1>
          <p className="page-subtitle">Configure LocalAI runtime settings</p>
        </div>
        <button className="btn btn-primary" onClick={handleSave} disabled={saving}>
          {saving ? <><LoadingSpinner size="sm" /> Saving...</> : <><i className="fas fa-save" /> Save Settings</>}
        </button>
      </div>

      {/* Watchdog Settings */}
      <Section icon="fa-shield-halved" iconColor="var(--color-primary)" title="Watchdog Settings">
        <SettingRow label="Enable Watchdog" description="Automatically monitor and manage backend processes">
          <Toggle checked={settings.watchdog_idle || settings.watchdog_busy} onChange={(v) => { update('watchdog_idle', v); update('watchdog_busy', v) }} />
        </SettingRow>
        <SettingRow label="Enable Idle Check" description="Automatically stop backends that have been idle too long">
          <Toggle checked={settings.watchdog_idle} onChange={(v) => update('watchdog_idle', v)} disabled={!watchdogEnabled} />
        </SettingRow>
        <SettingRow label="Idle Timeout" description="Time before an idle backend is stopped (e.g. 15m, 1h)">
          <input
            className="input"
            style={{ width: 120 }}
            value={settings.watchdog_idle_timeout || ''}
            onChange={(e) => update('watchdog_idle_timeout', e.target.value)}
            placeholder="15m"
            disabled={!settings.watchdog_idle}
          />
        </SettingRow>
        <SettingRow label="Enable Busy Check" description="Stop stuck/busy processes that exceed timeout">
          <Toggle checked={settings.watchdog_busy} onChange={(v) => update('watchdog_busy', v)} disabled={!watchdogEnabled} />
        </SettingRow>
        <SettingRow label="Busy Timeout" description="Time before a busy backend is stopped (e.g. 5m)">
          <input
            className="input"
            style={{ width: 120 }}
            value={settings.watchdog_busy_timeout || ''}
            onChange={(e) => update('watchdog_busy_timeout', e.target.value)}
            placeholder="5m"
            disabled={!settings.watchdog_busy}
          />
        </SettingRow>
        <SettingRow label="Check Interval" description="How often the watchdog checks backends (e.g. 2s)">
          <input
            className="input"
            style={{ width: 120 }}
            value={settings.watchdog_check_interval || ''}
            onChange={(e) => update('watchdog_check_interval', e.target.value)}
            placeholder="2s"
          />
        </SettingRow>
        <SettingRow label="Force Eviction When Busy" description="Allow model eviction even during active API calls">
          <Toggle checked={settings.force_eviction} onChange={(v) => update('force_eviction', v)} />
        </SettingRow>
        <SettingRow label="LRU Eviction Max Retries" description="Maximum retries waiting for busy models before eviction">
          <input
            className="input"
            type="number"
            style={{ width: 120 }}
            value={settings.lru_retries ?? ''}
            onChange={(e) => update('lru_retries', parseInt(e.target.value) || 0)}
            placeholder="30"
          />
        </SettingRow>
        <SettingRow label="LRU Eviction Retry Interval" description="Wait between eviction retries (e.g. 1s)">
          <input
            className="input"
            style={{ width: 120 }}
            value={settings.lru_retry_interval || ''}
            onChange={(e) => update('lru_retry_interval', e.target.value)}
            placeholder="1s"
          />
        </SettingRow>

        {/* Memory Reclaimer sub-section */}
        <div style={{
          marginTop: 'var(--spacing-md)', paddingTop: 'var(--spacing-md)',
          borderTop: '1px solid var(--color-border-subtle)',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-sm)' }}>
            <h4 style={{ fontSize: '0.875rem', fontWeight: 600, display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
              <i className="fas fa-memory" style={{ color: 'var(--color-accent)' }} /> Memory Reclaimer
            </h4>
            <button
              className="btn btn-secondary btn-sm"
              onClick={fetchResources}
              title="Refresh resource status"
            >
              <i className="fas fa-sync-alt" />
            </button>
          </div>

          {/* Live resource preview */}
          {resources && (
            <div style={{
              background: 'var(--color-bg-tertiary)', borderRadius: 'var(--radius-md)',
              padding: 'var(--spacing-sm)', marginBottom: 'var(--spacing-sm)', fontSize: '0.75rem',
            }}>
              {resources.gpus?.length > 0 ? resources.gpus.map((gpu, i) => {
                const usedPct = gpu.total > 0 ? Math.round((gpu.used / gpu.total) * 100) : 0
                return (
                  <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)', marginBottom: i < resources.gpus.length - 1 ? 4 : 0 }}>
                    <span style={{ color: 'var(--color-text-muted)', minWidth: 60 }}>GPU {i}</span>
                    <div style={{ flex: 1, height: 6, background: 'var(--color-bg-primary)', borderRadius: 3, overflow: 'hidden' }}>
                      <div style={{ width: `${usedPct}%`, height: '100%', background: percentColor(usedPct), borderRadius: 3 }} />
                    </div>
                    <span style={{ color: percentColor(usedPct), minWidth: 40, textAlign: 'right' }}>{usedPct}%</span>
                    <span style={{ color: 'var(--color-text-muted)' }}>{formatBytes(gpu.used)} / {formatBytes(gpu.total)}</span>
                  </div>
                )
              }) : (
                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
                  <span style={{ color: 'var(--color-text-muted)', minWidth: 60 }}>RAM</span>
                  {resources.ram && (() => {
                    const usedPct = resources.ram.total > 0 ? Math.round((resources.ram.used / resources.ram.total) * 100) : 0
                    return (
                      <>
                        <div style={{ flex: 1, height: 6, background: 'var(--color-bg-primary)', borderRadius: 3, overflow: 'hidden' }}>
                          <div style={{ width: `${usedPct}%`, height: '100%', background: percentColor(usedPct), borderRadius: 3 }} />
                        </div>
                        <span style={{ color: percentColor(usedPct), minWidth: 40, textAlign: 'right' }}>{usedPct}%</span>
                        <span style={{ color: 'var(--color-text-muted)' }}>{formatBytes(resources.ram.used)} / {formatBytes(resources.ram.total)}</span>
                      </>
                    )
                  })()}
                </div>
              )}
            </div>
          )}

          <SettingRow label="Enable Memory Reclaimer" description="Evict backends when memory usage exceeds threshold">
            <Toggle checked={settings.memory_reclaimer} onChange={(v) => update('memory_reclaimer', v)} />
          </SettingRow>
          <SettingRow label="Memory Threshold (%)" description="Eviction triggers when usage exceeds this percentage">
            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
              <input
                type="range"
                min="50"
                max="100"
                value={settings.memory_threshold || 80}
                onChange={(e) => update('memory_threshold', parseInt(e.target.value))}
                disabled={!settings.memory_reclaimer}
                style={{ width: 120 }}
              />
              <span style={{
                fontSize: '0.875rem', fontWeight: 600, minWidth: 40, textAlign: 'right',
                color: percentColor(settings.memory_threshold || 80),
              }}>
                {settings.memory_threshold || 80}%
              </span>
            </div>
          </SettingRow>
        </div>
      </Section>

      {/* Backend Request Settings */}
      <Section icon="fa-cogs" iconColor="var(--color-accent)" title="Backend Request Settings">
        <SettingRow label="Max Active Backends" description="Maximum models to keep loaded simultaneously (0 = unlimited, 1 = single model mode)">
          <input
            className="input"
            type="number"
            style={{ width: 120 }}
            value={settings.max_active_backends ?? ''}
            onChange={(e) => update('max_active_backends', parseInt(e.target.value) || 0)}
            placeholder="0"
          />
        </SettingRow>
        <SettingRow label="Parallel Backend Requests" description="Enable parallel request handling per backend">
          <Toggle checked={settings.parallel_backend_requests} onChange={(v) => update('parallel_backend_requests', v)} />
        </SettingRow>
      </Section>

      {/* Performance Settings */}
      <Section icon="fa-gauge-high" iconColor="var(--color-success)" title="Performance Settings">
        <SettingRow label="Default Threads" description="CPU threads for inference (0 = auto-detect)">
          <input
            className="input"
            type="number"
            style={{ width: 120 }}
            value={settings.threads ?? ''}
            onChange={(e) => update('threads', parseInt(e.target.value) || 0)}
            placeholder="0"
          />
        </SettingRow>
        <SettingRow label="Default Context Size" description="Default context window size for models">
          <input
            className="input"
            type="number"
            style={{ width: 120 }}
            value={settings.context_size ?? ''}
            onChange={(e) => update('context_size', parseInt(e.target.value) || 0)}
            placeholder="2048"
          />
        </SettingRow>
        <SettingRow label="F16 Precision" description="Use 16-bit floating point for reduced memory usage">
          <Toggle checked={settings.f16} onChange={(v) => update('f16', v)} />
        </SettingRow>
        <SettingRow label="Debug Mode" description="Enable verbose debug logging">
          <Toggle checked={settings.debug} onChange={(v) => update('debug', v)} />
        </SettingRow>
        <SettingRow label="Enable Tracing" description="Enable request/response tracing for debugging">
          <Toggle checked={settings.enable_tracing} onChange={(v) => update('enable_tracing', v)} />
        </SettingRow>
        <SettingRow label="Tracing Max Items" description="Maximum number of trace items to retain">
          <input
            className="input"
            type="number"
            style={{ width: 120 }}
            value={settings.tracing_max_items ?? ''}
            onChange={(e) => update('tracing_max_items', parseInt(e.target.value) || 0)}
            placeholder="100"
            disabled={!settings.enable_tracing}
          />
        </SettingRow>
      </Section>

      {/* API Settings */}
      <Section icon="fa-globe" iconColor="var(--color-warning)" title="API Settings">
        <SettingRow label="Enable CORS" description="Enable Cross-Origin Resource Sharing">
          <Toggle checked={settings.cors} onChange={(v) => update('cors', v)} />
        </SettingRow>
        <SettingRow label="CORS Allow Origins" description="Comma-separated list of allowed origins">
          <input
            className="input"
            style={{ width: 200 }}
            value={settings.cors_allow_origins || ''}
            onChange={(e) => update('cors_allow_origins', e.target.value)}
            placeholder="*"
            disabled={!settings.cors}
          />
        </SettingRow>
        <SettingRow label="Enable CSRF Protection" description="Enable Cross-Site Request Forgery protection">
          <Toggle checked={settings.csrf} onChange={(v) => update('csrf', v)} />
        </SettingRow>
      </Section>

      {/* P2P Settings */}
      <Section icon="fa-network-wired" iconColor="var(--color-accent)" title="P2P Settings" defaultOpen={false}>
        <SettingRow label="P2P Token" description="Authentication token for P2P network (set to '0' to generate new)">
          <input
            className="input"
            style={{ width: 200 }}
            value={settings.p2p_token || ''}
            onChange={(e) => update('p2p_token', e.target.value)}
            placeholder="Enter P2P token"
          />
        </SettingRow>
        <SettingRow label="P2P Network ID" description="Network identifier for P2P connections">
          <input
            className="input"
            style={{ width: 200 }}
            value={settings.p2p_network_id || ''}
            onChange={(e) => update('p2p_network_id', e.target.value)}
            placeholder="Network ID"
          />
        </SettingRow>
        <SettingRow label="Federated Mode" description="Enable federated instance mode for load balancing">
          <Toggle checked={settings.federated} onChange={(v) => update('federated', v)} />
        </SettingRow>
      </Section>

      {/* Agent Jobs Settings */}
      <Section icon="fa-tasks" iconColor="var(--color-primary)" title="Agent Jobs Settings" defaultOpen={false}>
        <SettingRow label="Job Retention Days" description="Number of days to keep job history">
          <input
            className="input"
            type="number"
            style={{ width: 120 }}
            value={settings.agent_job_retention_days ?? ''}
            onChange={(e) => update('agent_job_retention_days', parseInt(e.target.value) || 0)}
            placeholder="30"
          />
        </SettingRow>
      </Section>

      {/* Open Responses Settings */}
      <Section icon="fa-database" iconColor="var(--color-accent)" title="Open Responses Settings" defaultOpen={false}>
        <SettingRow label="Response Store TTL" description="Time-to-live for stored responses (e.g. 1h, 30m, 0 = no expiration)">
          <input
            className="input"
            style={{ width: 120 }}
            value={settings.open_responses_store_ttl || ''}
            onChange={(e) => update('open_responses_store_ttl', e.target.value)}
            placeholder="1h"
          />
        </SettingRow>
      </Section>

      {/* API Keys */}
      <Section icon="fa-key" iconColor="var(--color-error)" title="API Keys" defaultOpen={false}>
        <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', marginBottom: 'var(--spacing-sm)' }}>
          <i className="fas fa-exclamation-triangle" style={{ color: 'var(--color-warning)', marginRight: 'var(--spacing-xs)' }} />
          API keys are sensitive. One key per line or comma-separated.
        </div>
        <textarea
          className="textarea"
          value={settings.api_keys?.join('\n') || (typeof settings.api_keys_text === 'string' ? settings.api_keys_text : '')}
          onChange={(e) => update('api_keys_text', e.target.value)}
          rows={4}
          placeholder="sk-key-1&#10;sk-key-2"
          style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: '0.8125rem' }}
        />
      </Section>

      {/* Gallery Settings */}
      <Section icon="fa-images" iconColor="var(--color-accent)" title="Gallery Settings" defaultOpen={false}>
        <SettingRow label="Autoload Galleries" description="Automatically load model galleries on startup">
          <Toggle checked={settings.autoload_galleries} onChange={(v) => update('autoload_galleries', v)} />
        </SettingRow>
        <SettingRow label="Autoload Backend Galleries" description="Automatically load backend galleries on startup">
          <Toggle checked={settings.autoload_backend_galleries} onChange={(v) => update('autoload_backend_galleries', v)} />
        </SettingRow>
        <div style={{ marginTop: 'var(--spacing-sm)' }}>
          <label className="form-label">Model Galleries (JSON)</label>
          <textarea
            className="textarea"
            value={settings.galleries_json || (settings.galleries ? JSON.stringify(settings.galleries, null, 2) : '')}
            onChange={(e) => update('galleries_json', e.target.value)}
            rows={4}
            placeholder={'[\n  { "url": "https://...", "name": "my-gallery" }\n]'}
            style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: '0.8125rem' }}
          />
        </div>
        <div style={{ marginTop: 'var(--spacing-sm)' }}>
          <label className="form-label">Backend Galleries (JSON)</label>
          <textarea
            className="textarea"
            value={settings.backend_galleries_json || (settings.backend_galleries ? JSON.stringify(settings.backend_galleries, null, 2) : '')}
            onChange={(e) => update('backend_galleries_json', e.target.value)}
            rows={4}
            placeholder={'[\n  { "url": "https://...", "name": "my-backends" }\n]'}
            style={{ fontFamily: "'JetBrains Mono', monospace", fontSize: '0.8125rem' }}
          />
        </div>
      </Section>

      {/* Bottom save button */}
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 'var(--spacing-md)', paddingBottom: 'var(--spacing-xl)' }}>
        <button className="btn btn-primary" onClick={handleSave} disabled={saving}>
          {saving ? <><LoadingSpinner size="sm" /> Saving...</> : <><i className="fas fa-save" /> Save Settings</>}
        </button>
      </div>
    </div>
  )
}
