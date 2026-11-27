/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../context/AuthContext'
import { Callout, LoadError, SkeletonRows } from '../components/ui'
import { HubHeader } from '../components/hub/HubHeader'
import P2PTabs from '../components/p2p/P2PTabs'
import { p2pRelaysApi, settingsApi } from '../utils/api'
import NetworkSection from '../components/p2psettings/NetworkSection'
import RelaysSection from '../components/p2psettings/RelaysSection'
import IdentitySection from '../components/p2psettings/IdentitySection'
import UserPairingSection from '../components/p2psettings/UserPairingSection'
import { p2pSettingsFrom } from '../components/p2psettings/p2pSettingsModel'
import './settings.css'
// The relay commands use the command block of the Swarm pages.
import './swarm.css'
import '../components/p2p/p2p.css'
import '../components/p2psettings/p2psettings.css'

// The sections of the page and the modes that show them: relays exist only
// while this server shares, and the identity only while P2P is on.
const SECTIONS = [
  { id: 'network', modes: ['off', 'connect', 'share'] },
  { id: 'relays', modes: ['share'] },
  // What a user's own phone invite carries and the limits of each user. Users
  // exist only with authentication, which mode share needs anyway.
  { id: 'users', modes: ['share'], auth: true },
  { id: 'identity', modes: ['connect', 'share'] },
]

// P2P settings: the mode (off, connect only, connect and share), the network
// options, the relays this server uses and its identity. Every change applies
// at once; the server restarts the P2P layer when a P2P setting changes.
export default function P2PSettings() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('p2psettings')
  const { authEnabled } = useAuth()
  const [params, setParams] = useSearchParams()
  const [settings, setSettings] = useState(null)
  const [status, setStatus] = useState(null)
  const [relays, setRelays] = useState(null)
  const [relaysError, setRelaysError] = useState(null)
  const [error, setError] = useState(null)
  const [saving, setSaving] = useState('')

  const mode = settings?.p2p_mode || 'off'

  const loadRelays = useCallback(async (m) => {
    if (m !== 'share') { setRelays(null); setRelaysError(null); return }
    try {
      const data = await p2pRelaysApi.list()
      setRelays(data?.relays || [])
      setRelaysError(null)
    } catch (err) {
      setRelaysError(err)
    }
  }, [])

  const load = useCallback(async () => {
    try {
      const [all, st] = await Promise.all([settingsApi.get(), p2pRelaysApi.status()])
      const s = p2pSettingsFrom(all)
      setSettings(s)
      setStatus(st)
      setError(null)
      await loadRelays(s.p2p_mode)
    } catch (err) {
      setError(err)
    }
  }, [loadRelays])

  useEffect(() => { load() }, [load])

  const reloadRelays = useCallback(async () => {
    try { setStatus(await p2pRelaysApi.status()) } catch { /* the relays list still loads */ }
    await loadRelays(mode)
  }, [loadRelays, mode])

  const save = async (patch) => {
    const key = Object.keys(patch)[0]
    setSaving(key)
    try {
      const res = await settingsApi.save(patch)
      if (res && res.success === false) throw new Error(res.error || t('saveFailed'))
      addToast(t('saved'), 'success', 2500)
      await load()
    } catch (err) {
      addToast(err.message, 'error')
    } finally {
      setSaving('')
    }
  }

  const visible = SECTIONS.filter(s => s.modes.includes(mode) && (!s.auth || authEnabled))
  const requested = params.get('section')
  const section = visible.some(s => s.id === requested) ? requested : 'network'
  const pick = (id) => setParams(id === 'network' ? {} : { section: id }, { replace: true })

  if (error) {
    return (
      <div className="page page--wide st-page" data-testid="p2p-settings-page">
        <h1 className="dk-sr-only">{t('title')}</h1>
        <P2PTabs />
        <LoadError title={t('loadError')} error={error} onRetry={load} />
      </div>
    )
  }
  if (!settings) return <div className="page page--wide st-page"><SkeletonRows /></div>

  return (
    <div className="page page--wide st-page" data-testid="p2p-settings-page" data-mode={mode}>
      <h1 className="dk-sr-only">{t('title')}</h1>
      <HubHeader supporting={<p className="st-lede">{t(`lede.${mode}`)}</p>} />
      <P2PTabs />
      {status?.error && <Callout tone="error" role="alert" title={t('notRunning')} testId="p2p-status-error">{status.error}</Callout>}
      <div className="st-layout">
        <nav className="st-groups" aria-label={t('sections.label')}>
          {visible.map(s => (
            <button
              key={s.id} type="button" className="st-group" data-group={s.id}
              aria-current={section === s.id ? 'true' : undefined} onClick={() => pick(s.id)}
            >
              <span className="st-group__label">{t(`sections.${s.id}`)}</span>
            </button>
          ))}
        </nav>
        <div className="st-content">
          {section === 'network' && <NetworkSection settings={settings} authEnabled={authEnabled} saving={saving} onSave={save} />}
          {section === 'relays' && <RelaysSection status={status} relays={relays} error={relaysError} onReload={reloadRelays} addToast={addToast} />}
          {section === 'users' && <UserPairingSection addToast={addToast} />}
          {section === 'identity' && <IdentitySection status={status} onChanged={load} addToast={addToast} />}
        </div>
      </div>
    </div>
  )
}
