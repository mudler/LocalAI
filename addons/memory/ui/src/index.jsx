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
export const NOTES_ADDRESS = '/app/account?tab=memory'
export const SAVE_MODE_KEY = 'save-mode'
export const SAVE_MODES = ['ask', 'auto', 'never']

export function createAddon(sdk) {
  const { React } = sdk
  const h = React.createElement
  sdk.locale.define('en', {
    'memory.title': 'Short memory',
    'memory.intro': 'Your chats keep short notes about you: where you live, what you like, how you want answers. Before a reply, the model reads the note that matches the question. You do not have to ask it to.',
    'memory.rules.confirm': 'By default a new note is saved only after you confirm it. A proposal waits for one day, then it expires. You can change this below.',
    'memory.rules.where': 'The notes are plain files in the Memory folder of your files. You can read, edit and delete them there, or on the Memory tab of your account.',
    'memory.rules.scopes': 'There are three kinds of notes: yours for every chat, one for each saved agent, and one for each conversation that comes from a messaging app. A saved agent reads notes only when you allow this add-on for it.',
    'memory.rules.limits': 'The model reads a short list, at most 40 lines for each kind, and opens a topic when it needs the details.',
    'memory.open': 'Open my notes',
    'memory.save.title': 'Saving a note',
    'memory.save.legend': 'When the assistant wants to save something to my memory',
    'memory.save.ask': 'Ask me first',
    'memory.save.ask.desc': 'The assistant proposes the note and waits. You confirm it in the chat, or answer yes or no in a messaging app. This is the default.',
    'memory.save.auto': 'Save it and tell me',
    'memory.save.auto.desc': 'In your own chats the note is saved at once, and the chat shows "Saved to memory" with an Undo. A reply saves at most 5 notes. A long note, or one that looks like it holds a secret, still waits for you.',
    'memory.save.never': 'Never',
    'memory.save.never.desc': 'The assistant cannot propose a note. It says that saving is off if you ask it to remember something.',
    'memory.save.exception': 'Chats that come from a messaging app always ask first, whatever you choose here, because the person who wrote the message is not always you. Scheduled and background runs cannot save notes. A saved agent cannot widen your choice: if you choose to be asked, it asks, even when you let it write to memory.',
    'memory.save.loading': 'Reading your choice',
    'memory.save.saving': 'Saving your choice',
    'memory.save.saved': 'Your choice is saved.',
    'memory.save.failed': 'Your choice could not be saved. The earlier choice is still in force. Try again.',
    'memory.save.unreadable': 'Your choice could not be read. Reload the page to try again.',
  })
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

  function Component({ page }) {
    if (page !== 'memory') return null
    return (
      <section data-testid="memory-page" className="memory-page">
        <h1 className="page-title memory-page__title">{t('memory.title')}</h1>
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
