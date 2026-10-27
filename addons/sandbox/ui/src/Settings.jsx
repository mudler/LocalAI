import { settingsModel } from './lib/model.js'

// The settings section of the Sandbox add-on, drawn at the end of the Settings
// page for an administrator. It shows which backend runs the code, why the
// others do not, the choice, and, for the host backend, the plain warning and
// the phrase that has to be typed.
export function createSettings(sdk, t) {
  const { React } = sdk
  const h = React.createElement
  // eslint-disable-next-line no-unused-vars
  const { useEffect, useState, useCallback } = React

  const call = (args, options) => sdk.api.read({ arguments: args }, options)

  function SandboxSettingsView({ model, draft, phrase, busy, error, onDraft, onPhrase, onSave }) {
    if (model.hidden) return null
    return (
      <div className="sandbox-settings" data-testid="sandbox-settings">
        {model.hostNotice && (
          <p className="sandbox-settings__notice" role="alert" data-testid="sandbox-host-notice">{t('sandbox.settings.hostNotice')}</p>
        )}
        <p>{model.canRun ? t('sandbox.settings.runsOn').replace('{{backend}}', model.active) : t('sandbox.settings.cannotRun')}</p>
        <ul className="sandbox-settings__backends">
          {model.backends.map(b => (
            <li key={b.name} data-testid="sandbox-backend">
              <strong>{b.name}</strong>: {b.usable ? t('sandbox.settings.usable') : b.reason}
            </li>
          ))}
        </ul>
        <label className="sandbox-settings__field">
          <span>{t('sandbox.settings.choice')}</span>
          <select value={draft} onChange={e => onDraft(e.target.value)} data-testid="sandbox-choice" disabled={busy}>
            {model.options.map(o => <option key={o.value} value={o.value}>{t(`sandbox.settings.option.${o.value}`)}</option>)}
          </select>
        </label>
        {draft === 'host' && (
          <div className="sandbox-settings__warning" data-testid="sandbox-host-warning">
            <p>{t('sandbox.settings.hostWarning')}</p>
            {model.needsHostConfirmation && (
              <label className="sandbox-settings__field">
                <span>{t('sandbox.settings.hostPhrase')}</span>
                <input type="text" value={phrase} onChange={e => onPhrase(e.target.value)} data-testid="sandbox-host-phrase" autoComplete="off" disabled={busy} />
              </label>
            )}
          </div>
        )}
        {error && <p className="sandbox-settings__error" role="alert" data-testid="sandbox-error">{error}</p>}
        <button type="button" className="btn btn-primary btn-sm" onClick={onSave} disabled={busy || draft === model.setting} data-testid="sandbox-save">{t('sandbox.settings.save')}</button>
      </div>
    )
  }

  function SandboxSettings() {
    const [model, setModel] = useState(null)
    const [draft, setDraft] = useState('auto')
    const [phrase, setPhrase] = useState('')
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState('')
    const refresh = useCallback(async () => {
      try {
        const m = settingsModel(await call({ tool: 'status' }))
        setModel(m)
        if (!m.hidden) setDraft(m.setting)
      } catch {
        setModel({ hidden: true })
      }
    }, [])
    useEffect(() => { refresh() }, [refresh])
    if (model === null) return <p aria-busy="true" data-testid="sandbox-loading">{t('sandbox.settings.loading')}</p>
    const save = async () => {
      setBusy(true)
      setError('')
      try {
        if (draft === 'host' && model.needsHostConfirmation) {
          const ok = await call({ tool: 'confirm-host', phrase })
          if (ok?.ok !== true) throw new Error(ok?.error || t('sandbox.settings.failed'))
        }
        const done = await call({ tool: 'set-backend', backend: draft })
        if (done?.ok !== true) throw new Error(done?.error || t('sandbox.settings.failed'))
        setPhrase('')
        await refresh()
      } catch (e) {
        setError(e?.message || t('sandbox.settings.failed'))
      } finally {
        setBusy(false)
      }
    }
    return <SandboxSettingsView model={model} draft={draft} phrase={phrase} busy={busy} error={error} onDraft={setDraft} onPhrase={setPhrase} onSave={save} />
  }

  return { SandboxSettings, SandboxSettingsView }
}
