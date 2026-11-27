/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, LoadError, SkeletonRows, Switch, useLoad } from '../ui'
import { p2pNetApi } from '../../utils/api'
import {
  USER_PAIRING_RANGES, USER_PAIRING_RELAYS, userPairingForm, userPairingFrom, userPairingIssues, userPairingLimitsPatch,
} from './p2pSettingsModel'

// The setting p2p_user_pairing: what an invite carries when a user pairs their
// own phone from Account, My devices, and the limits of each user. The choices
// apply at once; the limits apply with their own Save button. A change applies
// to the next invite; paired phones and pending invites stay.
export default function UserPairingSection({ addToast }) {
  const { t } = useTranslation('p2psettings')
  const loaded = useLoad(() => p2pNetApi.userPairing(), [])
  const [saved, setSaved] = useState(null)
  const [form, setForm] = useState(null)
  const [saving, setSaving] = useState('')

  useEffect(() => {
    if (loaded.status !== 'ready') return
    const s = userPairingFrom(loaded.data)
    setSaved(s)
    setForm(userPairingForm(s))
  }, [loaded.status, loaded.data])

  const save = async (key, patch) => {
    setSaving(key)
    try {
      const s = userPairingFrom(await p2pNetApi.setUserPairing(patch))
      setSaved(s)
      setForm(userPairingForm(s))
      addToast?.(t('saved'), 'success', 2500)
    } catch (err) {
      addToast?.(err?.message || t('saveFailed'), 'error')
    } finally {
      setSaving('')
    }
  }

  if (loaded.status === 'error') return <LoadError title={t('users.loadError')} error={loaded.error} onRetry={loaded.retry} />
  if (!saved || !form) return <SkeletonRows count={3} />

  const busy = saving !== ''
  const issues = userPairingIssues(form)
  const patch = Object.keys(issues).length === 0 ? userPairingLimitsPatch(saved, form) : {}
  const changed = Object.keys(patch).length > 0
  const field = (key) => (e) => setForm(f => ({ ...f, [key]: e.target.value }))

  return (
    <section className="p2ps-section" aria-labelledby="p2ps-users-title" data-testid="p2p-user-pairing">
      <div>
        <h2 className="st-h2" id="p2ps-users-title">{t('users.title')}</h2>
        <p className="st-row__desc">{t('users.lede')}</p>
      </div>
      <div className="st-list">
        <div className="p2ps-edit" data-field="relays">
          <label className="st-row__label" htmlFor="p2ps-users-relays">{t('users.relays.title')}</label>
          <p className="st-row__desc" id="p2ps-users-relays-desc">{t('users.relays.desc')}</p>
          <div className="p2ps-edit__row">
            <div className="dk-select-wrap">
              <select
                id="p2ps-users-relays" className="dk-select" value={saved.relays} disabled={busy} aria-describedby="p2ps-users-relays-desc"
                onChange={(e) => save('relays', { relays: e.target.value })} data-testid="p2p-users-relays"
              >
                {USER_PAIRING_RELAYS.map(m => <option key={m} value={m}>{t(`users.relays.${m}`)}</option>)}
              </select>
            </div>
            <span className="p2ps-key">p2p_user_pairing.relays</span>
          </div>
        </div>
        <UserSwitchRow
          id="p2ps-users-open" settingKey="allow_open_relay" label={t('users.open.title')} desc={t('users.open.desc')}
          checked={saved.allow_open_relay} disabled={busy || saved.relays === 'none'} onChange={(v) => save('allow_open_relay', { allow_open_relay: v })}
        />
        <UserSwitchRow
          id="p2ps-users-lan" settingKey="omit_lan_addrs" label={t('users.lan.title')} desc={t('users.lan.desc')}
          checked={saved.omit_lan_addrs} disabled={busy} onChange={(v) => save('omit_lan_addrs', { omit_lan_addrs: v })}
        />
        <div className="p2ps-edit" data-field="limits">
          <span className="st-row__label">{t('users.limits.title')}</span>
          <p className="st-row__desc">{t('users.limits.desc')}</p>
          <div className="p2ps-limits">
            <LimitField id="p2ps-users-devices" label={t('users.limits.devices')} value={form.max_devices} invalid={issues.max_devices} range={USER_PAIRING_RANGES.max_devices} onChange={field('max_devices')} testId="p2p-users-max-devices" />
            <LimitField id="p2ps-users-pending" label={t('users.limits.pending')} value={form.max_pending} invalid={issues.max_pending} range={USER_PAIRING_RANGES.max_pending} onChange={field('max_pending')} testId="p2p-users-max-pending" />
            <LimitField id="p2ps-users-ttl" label={t('users.limits.ttl')} unit={t('units.minutes')} value={form.ttl_minutes} invalid={issues.ttl_minutes} range={USER_PAIRING_RANGES.ttl_minutes} onChange={field('ttl_minutes')} testId="p2p-users-max-ttl" />
          </div>
          <div className="p2ps-edit__row">
            <Button size="sm" disabled={!changed || busy} busy={saving === 'limits'} onClick={() => save('limits', patch)} data-testid="p2p-users-limits-save">
              {t('actions.save')}
            </Button>
            <span className="p2ps-key">p2p_user_pairing</span>
          </div>
        </div>
      </div>
    </section>
  )
}

function UserSwitchRow({ id, label, desc, settingKey, checked, disabled, onChange }) {
  return (
    <div className="st-row" data-bool data-field={settingKey}>
      <div className="st-row__main">
        <div className="st-row__title"><span className="st-row__label" id={`${id}-label`}>{label}</span></div>
        <p className="st-row__desc" id={`${id}-desc`}>{desc}</p>
        <p className="st-row__meta"><span className="p2ps-key">p2p_user_pairing.{settingKey}</span></p>
      </div>
      <div className="st-row__control">
        <Switch checked={checked} onChange={onChange} labelledBy={`${id}-label`} describedBy={`${id}-desc`} disabled={disabled} testId={`${id}-switch`} />
      </div>
    </div>
  )
}

function LimitField({ id, label, unit, value, invalid, range, onChange, testId }) {
  const { t } = useTranslation('p2psettings')
  const input = (
    <input
      id={id} className="dk-input p2ps-num" inputMode="numeric" value={value} aria-invalid={invalid || undefined}
      aria-describedby={`${id}-hint`} onChange={onChange} data-testid={testId}
    />
  )
  return (
    <div className="dk-field">
      <label className="dk-label" htmlFor={id}>{label}</label>
      {unit ? <span className="p2ps-unit p2ps-unit--fit">{input}<span className="p2ps-unit__label">{unit}</span></span> : input}
      <p className="dk-hint p2ps-range" data-invalid={invalid || undefined} id={`${id}-hint`}>{t('users.limits.range', { min: range[0], max: range[1] })}</p>
    </div>
  )
}
