// The entry of the Memory add-on. Its work happens in the chats: the host loads
// the index of the person's notes into the prompt and the skills tell the model
// to read them. This page says how that works and opens the notes, which the
// person edits on the Memory tab of their account.
export const NOTES_ADDRESS = '/app/account?tab=memory'

export function createAddon(sdk) {
  const { React } = sdk
  const h = React.createElement
  sdk.locale.define('en', {
    'memory.title': 'Short memory',
    'memory.intro': 'Your chats keep short notes about you: where you live, what you like, how you want answers. Before a reply, the model reads the note that matches the question. You do not have to ask it to.',
    'memory.rules.confirm': 'A new note is saved only after you confirm it. A proposal waits for one day, then it expires.',
    'memory.rules.where': 'The notes are plain files in the Memory folder of your files. You can read, edit and delete them there, or on the Memory tab of your account.',
    'memory.rules.scopes': 'There are three kinds of notes: yours for every chat, one for each saved agent, and one for each conversation that comes from a messaging app. A saved agent reads notes only when you allow this add-on for it.',
    'memory.rules.limits': 'The model reads a short list, at most 40 lines for each kind, and opens a topic when it needs the details.',
    'memory.open': 'Open my notes',
  })
  const t = (key) => sdk.locale.t(key, key)

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
