// The entry of the Memory add-on. Its work happens in the chats: the host
// loads the index of the person's notes into the prompt and the skills tell the
// model to read them. This page says how that works, lets the person choose
// how a new note is saved, and opens the notes, which the person edits on the
// Memory tab of their account.
//
// The choice is a per-person setting that the host keeps (manifest
// userSettings, key save-mode); the add-on does not store it and the host
// validates it. The rules that make it safe are the host's: this page only
// states them.
import { locales } from './i18n/index.js'

export const NOTES_ADDRESS = '/app/account?tab=memory'
// Memory is built in; the save level lives in Settings > Memory.
export const SETTINGS_ADDRESS = '/app/account?tab=memory'
export const SAVE_MODE_KEY = 'save-mode'
export const SAVE_MODES = ['ask', 'auto', 'never']

export function createAddon(sdk) {
  const { React } = sdk
  const h = React.createElement
  for (const [language, bundle] of Object.entries(locales)) sdk.locale.define(language, bundle)
  const t = (key) => sdk.locale.t(key, key)

  // The picker. It shows the choice the host reports, never one it assumed, and
  // keeps the earlier choice on screen when a save fails.
  function SavePicker() {
    const { useEffect, useState } = React
    const [state, setState] = useState({ phase: 'loading', value: 'ask', declared: true })
    const [saving, setSaving] = useState(false)
    const [notice, setNotice] = useState('')
    const [failed, setFailed] = useState(false)

    useEffect(() => {
      let live = true
      sdk.userSettings.get().then((out) => {
        if (!live) return
        const s = (out?.settings || []).find((x) => x.key === SAVE_MODE_KEY)
        // An add-on package that declares no such setting has nothing to show.
        setState(s ? { phase: 'ready', value: SAVE_MODES.includes(s.value) ? s.value : 'ask', declared: true } : { phase: 'ready', value: 'ask', declared: false })
      }).catch(() => { if (live) setState({ phase: 'error', value: 'ask', declared: true }) })
      return () => { live = false }
    }, [])

    const choose = (value) => {
      if (saving || value === state.value) return
      setSaving(true)
      setFailed(false)
      setNotice(t('memory.save.saving'))
      sdk.userSettings.set(SAVE_MODE_KEY, value).then((out) => {
        const s = (out?.settings || []).find((x) => x.key === SAVE_MODE_KEY)
        setState({ phase: 'ready', value: s && SAVE_MODES.includes(s.value) ? s.value : value, declared: true })
        setNotice(t('memory.save.saved'))
      }).catch(() => {
        setFailed(true)
        setNotice('')
      }).finally(() => setSaving(false))
    }

    if (state.phase === 'loading') return h('p', { 'data-testid': 'memory-save-loading', role: 'status' }, t('memory.save.loading'))
    if (state.phase === 'error') return h(sdk.ui.Callout, { tone: 'warning', role: 'alert', testId: 'memory-save-unreadable' }, t('memory.save.unreadable'))
    if (!state.declared) return null
    const options = SAVE_MODES.map((m) => ({ value: m, label: t(`memory.save.${m}`), description: t(`memory.save.${m}.desc`) }))
    return h(sdk.ui.Section, { title: t('memory.save.title'), testId: 'memory-save' },
      h(sdk.ui.Choice, { legend: t('memory.save.legend'), value: state.value, options, disabled: saving, onChange: choose, testId: 'memory-save-mode' }),
      h('p', { className: 'memory-page__exception', 'data-testid': 'memory-save-exception' }, t('memory.save.exception')),
      h('p', { className: 'memory-page__status', role: 'status', 'aria-live': 'polite', 'data-testid': 'memory-save-status' }, notice),
      failed ? h(sdk.ui.Callout, { tone: 'warning', role: 'alert', testId: 'memory-save-failed' }, t('memory.save.failed')) : null)
  }

  // Memory is part of the host now. Older hosts have no Notice; they get the
  // Callout with the same words.
  function BuiltinNotice() {
    const open = () => sdk.navigation.openApp(SETTINGS_ADDRESS)
    if (sdk.ui.Notice) {
      return h(sdk.ui.Notice, { severity: 'info', title: t('memory.builtin.title'), reason: t('memory.builtin.reason'), fixLabel: t('memory.builtin.fix'), onFix: open, testId: 'memory-builtin-notice' })
    }
    return h(sdk.ui.Callout, { tone: 'info', title: t('memory.builtin.title'), testId: 'memory-builtin-notice' }, t('memory.builtin.reason'))
  }

  function Component({ page }) {
    if (page !== 'memory') return null
    return (
      <section data-testid="memory-page" className="memory-page">
        <h1 className="page-title memory-page__title">{t('memory.title')}</h1>
        <BuiltinNotice />
        <p>{t('memory.intro')}</p>
        <ul className="memory-page__rules">
          <li>{t('memory.rules.confirm')}</li>
          <li>{t('memory.rules.where')}</li>
          <li>{t('memory.rules.scopes')}</li>
          <li>{t('memory.rules.limits')}</li>
        </ul>
        <SavePicker />
        <div>
          <button type="button" className="btn btn-primary" data-testid="memory-open" onClick={() => sdk.navigation.openApp(NOTES_ADDRESS)}>
            {t('memory.open')}
          </button>
        </div>
      </section>
    )
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}
