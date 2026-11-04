/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The switches of the PiKVM add-on, one block for each device that has a
// connection: hard power actions, keyboard input and the length of a typed
// text. All are off or small until an administrator changes them. The add-on
// keeps them by device address and applies them on every call; everyone who
// opens the page sees them.
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getSdk } from '../../../internal/host-compat/src/runtime.js'
import { connectorsApi } from '../../../internal/connector-ui/src/api'
import { Field, Notice, Switch, TextInput } from '../../../internal/connector-ui/src/controls'
import { deviceKey } from './pikvm'
import { draftOf, draftProblem, same, valuesOf, DEFAULT_CHARS } from './settings'
import './pikvm.css'

const call = (args) => getSdk().api.read({ arguments: args })

function Device({ address, saved, editable, t, onSaved }) {
  const [draft, setDraft] = useState(() => draftOf(saved))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [ok, setOk] = useState(false)
  useEffect(() => { setDraft(draftOf(saved)) }, [saved])
  const problem = draftProblem(draft)
  const dirty = !same(draft, draftOf(saved))
  const set = (k) => (v) => { setDraft({ ...draft, [k]: v }); setOk(false) }
  const save = async (e) => {
    e.preventDefault()
    if (problem || !editable) return
    setBusy(true)
    setError('')
    try {
      const res = await call({ tool: 'set-settings', device: address, ...valuesOf(draft) })
      onSaved(address, res.data)
      setOk(true)
    } catch (err) { setError(err?.message || t('settings.saveFailed')) } finally { setBusy(false) }
  }
  return (
    <form className="pk-device" onSubmit={save} data-testid="pikvm-device" aria-label={address}>
      <h3 className="pk-device__title"><code>{address}</code></h3>
      <fieldset className="pk-fieldset" disabled={!editable || busy}>
        <Switch checked={draft.allowTyping} onChange={set('allowTyping')} testId="pikvm-allow-typing">{t('settings.typing')}</Switch>
        <p className="cn-muted">{t('settings.typingHint')}</p>
        <Field label={t('settings.maxChars')} hint={t('settings.maxCharsHint', { default: DEFAULT_CHARS })} error={problem ? t(problem) : undefined}>
          {(p) => <TextInput {...p} type="number" min="1" max="1000" inputMode="numeric" data-testid="pikvm-max-chars" value={draft.maxTypeChars} onChange={(e) => set('maxTypeChars')(e.target.value)} />}
        </Field>
        <Switch checked={draft.allowHardPower} onChange={set('allowHardPower')} testId="pikvm-allow-hard">{t('settings.hard')}</Switch>
        <p className="cn-muted">{t('settings.hardHint')}</p>
      </fieldset>
      {error && <Notice tone="error" role="alert" testId="pikvm-settings-error">{error}</Notice>}
      {ok && <Notice tone="ok" role="status" testId="pikvm-settings-saved">{t('settings.saved')}</Notice>}
      <div className="pk-actions">
        <button type="submit" className="dk-btn dk-btn--primary" data-testid="pikvm-settings-save" disabled={!editable || busy || !dirty || Boolean(problem)}>{t('settings.save')}</button>
      </div>
    </form>
  )
}

export default function SettingsEditor() {
  const { t } = useTranslation('pikvm')
  const [view, setView] = useState(null)
  const [addresses, setAddresses] = useState([])
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try {
      const [res, list] = await Promise.all([call({ tool: 'get-settings' }), connectorsApi.list().catch(() => ({ connectors: [] }))])
      setView(res.data)
      const seen = new Set()
      for (const c of list.connectors || []) {
        if (c.kind !== 'pikvm') continue
        for (const r of c.resources || []) seen.add(deviceKey(r))
      }
      setAddresses([...seen].sort())
      setError('')
    } catch (e) { setError(e?.message || t('settings.loadFailed')) }
  }, [t])
  useEffect(() => { load() }, [load])

  const onSaved = (address, data) => {
    setView((v) => {
      const devices = (v.devices || []).filter((d) => d.device !== data.device)
      devices.push({ device: data.device, allowHardPower: data.allowHardPower, allowTyping: data.allowTyping, maxTypeChars: data.maxTypeChars })
      return { ...v, devices }
    })
  }

  if (!view) {
    return (
      <section className="pk-settings" data-testid="pikvm-settings" aria-busy={!error}>
        <h2 className="pk-settings__title">{t('settings.title')}</h2>
        {error ? <Notice tone="error" role="alert">{error}</Notice> : <p className="cn-muted" role="status">{t('settings.loading')}</p>}
      </section>
    )
  }
  const editable = view.admin === true
  const saved = (address) => (view.devices || []).find((d) => d.device === address.toLowerCase()) || view.defaults
  return (
    <section className="pk-settings" data-testid="pikvm-settings" aria-labelledby="pk-settings-title">
      <h2 className="pk-settings__title" id="pk-settings-title">{t('settings.title')}</h2>
      <p className="cn-muted">{t('settings.intro')}</p>
      <Notice tone="warn" role="note" testId="pikvm-physical-warning">
        <strong>{t('settings.physicalTitle')}</strong> {t('settings.physical')}
      </Notice>
      {!editable && <Notice tone="info" role="note" testId="pikvm-settings-readonly">{t('settings.readOnly')}</Notice>}
      {view.broken && <Notice tone="error" role="alert" testId="pikvm-settings-broken">{t('settings.broken')}</Notice>}
      {error && <Notice tone="error" role="alert">{error}</Notice>}
      {addresses.length === 0
        ? <p className="cn-muted" data-testid="pikvm-settings-none">{t('settings.none')}</p>
        : addresses.map((a) => <Device key={a} address={a} saved={saved(a)} editable={editable} t={t} onSaved={onSaved} />)}
      <div className="pk-never" data-testid="pikvm-never">
        <h3 className="pk-never__title">{t('settings.neverTitle')}</h3>
        <p className="cn-muted">{t('settings.never')}</p>
        <p className="cn-muted">{t('settings.keys', { keys: (view.keys || []).join(', ') })}</p>
      </div>
    </section>
  )
}
