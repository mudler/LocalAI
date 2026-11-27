/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import SideSheet from '../SideSheet'
import { Button, Callout, LoadError, SkeletonRows } from '../ui'
import { configForm, configPayload, errorKey } from './pluginState'

// The settings of a plugin for the person (its userConfig): text, numbers,
// a yes or no, and a choice of its options. They apply on Save. A sensitive
// setting is not stored by this version: it says so and has no field.
export default function PluginSettings({ install, api, onClose, onSaved }) {
  const { t } = useTranslation('addons')
  const [state, setState] = useState({ status: 'loading', options: [], error: null })
  const [form, setForm] = useState({})
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)
  const [saved, setSaved] = useState(false)
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    const ctl = new AbortController()
    setState({ status: 'loading', options: [], error: null })
    Promise.all([api.detail(install.id, ctl.signal), api.me(install.id, ctl.signal)])
      .then(([d, me]) => {
        const options = d?.model?.userConfig || []
        setState({ status: 'ready', options, error: null })
        setForm(configForm(options, me?.config))
      })
      .catch((e) => { if (!ctl.signal.aborted) setState({ status: 'error', options: [], error: e }) })
    return () => ctl.abort()
  }, [install.id, attempt]) // eslint-disable-line react-hooks/exhaustive-deps

  const set = (key, value) => { setSaved(false); setForm((f) => ({ ...f, [key]: value })) }
  const save = async (e) => {
    e?.preventDefault?.()
    setSaving(true)
    setError(null)
    try {
      await api.setMe(install.id, { config: configPayload(state.options, form) })
      setSaved(true)
      onSaved?.()
    } catch (err) {
      setError(err)
    } finally {
      setSaving(false)
    }
  }
  const plain = state.options.filter((o) => !o.sensitive)
  const secret = state.options.filter((o) => o.sensitive)

  return (
    <SideSheet
      title={t('plugins.settings.title', { name: install.name || install.plugin })}
      description={t('plugins.settings.description')}
      onClose={onClose}
      pending={saving}
      closeLabel={t('plugins.review.cancel')}
      testId="plugin-settings"
      labelId="plugin-settings-title"
      footer={(
        <>
          <Button variant="ghost" onClick={onClose} disabled={saving}>{t('plugins.review.cancel')}</Button>
          <Button variant="primary" icon="save" busy={saving} disabled={saving || state.status !== 'ready' || plain.length === 0} onClick={save} data-testid="plugin-settings-save">{t('plugins.settings.save')}</Button>
        </>
      )}
    >
      {state.status === 'loading' && <SkeletonRows count={3} />}
      {state.status === 'error' && <LoadError title={t('plugins.settings.loadFailed')} error={state.error} onRetry={() => setAttempt((n) => n + 1)} />}
      {state.status === 'ready' && (
        <form className="pl-form" onSubmit={save} noValidate>
          {state.options.length === 0 && <p className="pl-quiet">{t('plugins.settings.none')}</p>}
          {plain.map((o) => {
            const id = `pl-opt-${o.key}`
            const label = (
              <span className="pl-field__label">
                {o.title || o.key}{o.required && <span className="pl-quiet"> {t('plugins.settings.required')}</span>}
              </span>
            )
            if (o.type === 'boolean') {
              return (
                <label key={o.key} className="pl-check" htmlFor={id}>
                  <input id={id} type="checkbox" className="dk-check" checked={Boolean(form[o.key])} onChange={(e) => set(o.key, e.target.checked)} />
                  <span>{label}{o.description && <span className="pl-field__help">{o.description}</span>}</span>
                </label>
              )
            }
            return (
              <div key={o.key} className="pl-field">
                <label htmlFor={id}>{label}</label>
                {Array.isArray(o.options) && o.options.length > 0 ? (
                  <span className="dk-select-wrap">
                    <select id={id} className="dk-select" value={form[o.key] ?? ''} onChange={(e) => set(o.key, e.target.value)}>
                      {!o.required && <option value="">{t('plugins.settings.unset')}</option>}
                      {o.options.map((v) => <option key={v} value={v}>{v}</option>)}
                    </select>
                  </span>
                ) : (
                  <input id={id} className="dk-input" type={o.type === 'number' ? 'number' : 'text'} min={o.min ?? undefined} max={o.max ?? undefined}
                    value={form[o.key] ?? ''} onChange={(e) => set(o.key, e.target.value)} data-testid="plugin-settings-field" />
                )}
                {o.description && <span className="pl-field__help">{o.description}</span>}
              </div>
            )
          })}
          {secret.length > 0 && (
            <Callout tone="info" icon="lock" title={t('plugins.settings.secretTitle')}>
              {t('plugins.settings.secretBody', { names: secret.map((o) => o.title || o.key).join(', ') })}
            </Callout>
          )}
          {error && <Callout tone="error" role="alert">{errorKey(error) ? t(errorKey(error), { defaultValue: error.message }) : error.message}</Callout>}
          {saved && <Callout tone="ok" role="status" testId="plugin-settings-saved">{t('plugins.settings.saved')}</Callout>}
        </form>
      )}
    </SideSheet>
  )
}
