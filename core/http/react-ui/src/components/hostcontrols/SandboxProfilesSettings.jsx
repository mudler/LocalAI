/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, Callout, Switch } from '../ui'
import { createHostSettingsClient } from '../../utils/hostSettingsClient'
import { createAddonsApi } from '../../utils/addonsApi'
import { chatSandboxProfileApi } from '../../utils/api'
import { apiUrl } from '../../utils/basePath'
import { useHostSettings } from '../../hooks/useHostSettings'
import {
  everyone, normalizePolicy, parseQuota, parseUsers, policyToSend, samePolicy, setPinned, toggleRole, withAccess,
} from '../../utils/chatSandboxProfile'
import './sandboxprofiles.css'

// The sandbox-profiles control on the Sandbox add-on's Settings tab: the
// default profile, who may use each profile, the memory and disk cap of a
// sandbox, and the pin of a profile to its previous image. It edits the owner
// policy key sandbox_profiles. Nothing is restricted until an administrator
// restricts it: an empty choice of roles and users means everyone.
export default function SandboxProfilesSettings() {
  const { t } = useTranslation('chats')
  const client = useMemo(() => createHostSettingsClient({ api: createAddonsApi({ base: apiUrl }) }), [])
  const owner = useHostSettings(client, 'sandbox-profiles')
  const saved = owner.snapshot?.policy.sandbox_profiles
  const [view, setView] = useState(null)
  const [builtinDefault, setBuiltinDefault] = useState('')
  const [loadError, setLoadError] = useState(false)
  const [draft, setDraft] = useState(null)
  const [memory, setMemory] = useState('')
  const [disk, setDisk] = useState('')
  const [users, setUsers] = useState({})

  useEffect(() => {
    let live = true
    chatSandboxProfileApi.admin().then(r => { if (live) setView(r) }).catch(() => { if (live) setLoadError(true) })
    // The default a chat gets while the policy names none: the server decides.
    chatSandboxProfileApi.list().then(r => { if (live) setBuiltinDefault(r?.default || '') }).catch(() => {})
    return () => { live = false }
  }, [saved])
  useEffect(() => {
    if (!saved) return
    const p = normalizePolicy(saved)
    setDraft(p)
    setMemory(p.memory_mib ? String(p.memory_mib) : '')
    setDisk(p.disk_mib ? String(p.disk_mib) : '')
    setUsers(Object.fromEntries(Object.entries(p.access).map(([id, a]) => [id, (a.users || []).join(', ')])))
  }, [saved])

  if (loadError) return <Callout tone="error" testId="sandbox-profiles-load-error">{t('sandboxProfile.admin.loadError')}</Callout>
  if (!view || !draft) return null
  const profiles = view.profiles || []
  const roles = view.roles || ['admin', 'user']
  const mem = parseQuota(memory), dsk = parseQuota(disk)
  const bad = Number.isNaN(mem) || Number.isNaN(dsk)
  const next = { ...draft, memory_mib: Number.isNaN(mem) ? 0 : mem, disk_mib: Number.isNaN(dsk) ? 0 : dsk }
  const dirty = !samePolicy(next, saved)
  const locked = owner.busy || !!owner.error
  const setUserText = (id, text) => {
    setUsers(u => ({ ...u, [id]: text }))
    setDraft(d => withAccess(d, id, { users: parseUsers(text) }))
  }
  return (
    <section className="sbp" aria-label={t('sandboxProfile.admin.title')} data-testid="sandbox-profiles-settings">
      <h3 className="sbp__title">{t('sandboxProfile.admin.title')}</h3>
      <p className="st-note">{t('sandboxProfile.admin.intro')}</p>
      <label className="sbp__field">
        <span>{t('sandboxProfile.admin.default')}</span>
        <select className="input" value={draft.default || builtinDefault} disabled={locked} onChange={e => setDraft({ ...draft, default: e.target.value })} data-testid="sandbox-profiles-default">
          {!draft.default && !builtinDefault && <option value="" />}
          {profiles.map(p => <option key={p.id} value={p.id}>{p.title}</option>)}
        </select>
      </label>
      <ul className="sbp__list" aria-label={t('sandboxProfile.admin.profiles')}>
        {profiles.map((p, i) => {
          const every = everyone(draft, p.id)
          const pinId = `sbp-pin-${p.id}`
          return (
            <li key={p.id} className="sbp__row" data-testid={`sandbox-profile-row-${p.id}`}>
              <div className="sbp__head">
                <strong>{p.title}</strong>
                <span className="sbp__meta">{t('sandboxProfile.admin.limits', { cpus: p.cpus, memory: p.memory_mib, disk: p.disk_mib })}</span>
              </div>
              <p className="sbp__meta">{p.description}</p>
              <p className="sbp__meta">{t('sandboxProfile.tools', { tools: (p.tools || []).join(', ') })}</p>
              {i === 0 ? <p className="sbp__meta" data-testid="sandbox-profile-base-note">{t('sandboxProfile.admin.baseAlways')}</p> : (
                <fieldset className="sbp__who" disabled={locked}>
                  <legend>{t('sandboxProfile.admin.who')}</legend>
                  <span className="sbp__meta" data-testid={`sandbox-profile-who-${p.id}`}>{every ? t('sandboxProfile.admin.everyone') : t('sandboxProfile.admin.restricted')}</span>
                  {roles.map(r => (
                    <label key={r} className="sbp__check">
                      <input type="checkbox" className="dk-check" checked={(draft.access[p.id]?.roles || []).includes(r)} onChange={() => setDraft(toggleRole(draft, p.id, r))} data-testid={`sandbox-profile-role-${p.id}-${r}`} />
                      {t(`sandboxProfile.admin.role.${r}`, { defaultValue: r })}
                    </label>
                  ))}
                  <label className="sbp__field">
                    <span>{t('sandboxProfile.admin.users')}</span>
                    <input className="input" value={users[p.id] || ''} onChange={e => setUserText(p.id, e.target.value)} placeholder={t('sandboxProfile.admin.usersPlaceholder')} data-testid={`sandbox-profile-users-${p.id}`} />
                  </label>
                </fieldset>
              )}
              <div className="sbp__pin">
                <span id={pinId}>{t('sandboxProfile.admin.pin')}</span>
                <Switch labelledBy={pinId} checked={draft.pin_previous.includes(p.id)} disabled={locked || !p.has_previous}
                  onChange={on => setDraft(setPinned(draft, p.id, on))} testId={`sandbox-profile-pin-${p.id}`} />
                {!p.has_previous && <span className="sbp__meta">{t('sandboxProfile.admin.noPrevious')}</span>}
              </div>
            </li>
          )
        })}
      </ul>
      <div className="sbp__quota">
        <label className="sbp__field">
          <span>{t('sandboxProfile.admin.memory')}</span>
          <input className="input" type="number" min="0" step="1" inputMode="numeric" value={memory} placeholder={t('sandboxProfile.admin.noLimit')} disabled={locked} onChange={e => setMemory(e.target.value)} data-testid="sandbox-profiles-memory" />
        </label>
        <label className="sbp__field">
          <span>{t('sandboxProfile.admin.disk')}</span>
          <input className="input" type="number" min="0" step="1" inputMode="numeric" value={disk} placeholder={t('sandboxProfile.admin.noLimit')} disabled={locked} onChange={e => setDisk(e.target.value)} data-testid="sandbox-profiles-disk" />
        </label>
      </div>
      <p className="st-note">{t('sandboxProfile.admin.quotaHelp')}</p>
      {bad && <p className="st-note" role="alert" data-testid="sandbox-profiles-bad">{t('sandboxProfile.admin.badQuota')}</p>}
      {owner.error && (
        <Callout tone="error" role="alert" testId="sandbox-profiles-owner-error"
          actions={<Button size="sm" variant="secondary" disabled={owner.busy} onClick={owner.reload}>{t('sandboxProfile.admin.reload')}</Button>}>
          {t('sandboxProfile.admin.ownerError', { message: owner.error.message })}
        </Callout>
      )}
      <Button variant="primary" disabled={!owner.snapshot || locked || bad || !dirty} onClick={() => owner.save({ sandbox_profiles: policyToSend(next) })} data-testid="sandbox-profiles-save">
        {t('sandboxProfile.admin.save')}
      </Button>
    </section>
  )
}
