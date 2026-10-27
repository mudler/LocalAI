// The entry of the Sandbox add-on. Its work happens in the chats: the tools run
// commands and code in an isolated sandbox. This page says so and, for an
// administrator, shows the state of the sandbox.
export function createAddon(sdk) {
  const { React } = sdk
  // eslint-disable-next-line no-unused-vars
  const h = React.createElement
  sdk.locale.define('en', {
    'sandbox.title': 'Sandbox',
    'sandbox.intro': 'Your chats can run commands and code here. Each chat gets its own sandbox with your files mounted. Changes the code makes to your files are saved as one change you can undo.',
    'sandbox.isolated': 'The code runs in an isolated environment (gVisor or a virtual machine) with no network.',
    'sandbox.limits': 'A sandbox sleeps after 10 minutes without use and is removed after 24 hours.',
  })
  const t = (key) => sdk.locale.t(key, key)

  function Component({ page }) {
    if (page !== 'sandbox') return null
    return (
      <section data-testid="sandbox-page" className="sandbox-page">
        <h1 className="page-title sandbox-page__title">{t('sandbox.title')}</h1>
        <p>{t('sandbox.intro')}</p>
        <ul className="sandbox-page__rules">
          <li>{t('sandbox.isolated')}</li>
          <li>{t('sandbox.limits')}</li>
        </ul>
      </section>
    )
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}
