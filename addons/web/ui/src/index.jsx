// The entry of the Web add-on. Its work happens in the chats: it serves
// web_search and web_fetch in place of the built-in tools. This page says so;
// the settings are a section of the Settings page.
import { createSettings } from './Settings.jsx'

export function createAddon(sdk) {
  const { React } = sdk
  // eslint-disable-next-line no-unused-vars
  const h = React.createElement
  sdk.locale.define('en', {
    'web.title': 'Web',
    'web.intro': 'Your chats search the web and read web pages through this add-on, in place of the built-in web tools. It can search with Brave Search, Tavily or Google Programmable Search when an administrator sets a key, and it reads the text of PDF files.',
    'web.rules.order': 'The search providers are tried in the order an administrator chose. When one fails, the next one is tried.',
    'web.rules.untrusted': 'What a page says is given to the model as untrusted text. Addresses inside your network are never read.',
    'web.rules.settings': 'An administrator sets the providers and their keys in Settings, in the Web section.',
    'web.provider.brave': 'Brave Search',
    'web.provider.tavily': 'Tavily',
    'web.provider.google': 'Google Programmable Search',
    'web.provider.searxng': 'SearXNG',
    'web.provider.duckduckgo': 'DuckDuckGo',
    'web.settings.loading': 'Reading the web settings…',
    'web.settings.intro': 'Choose the search providers of web_search and the order they are tried in. A provider without its key or address is skipped. Keys are kept by the add-on and never shown again.',
    'web.settings.serving': 'Searches go to: {{list}}.',
    'web.settings.servingNone': 'No provider is ready: web_search says so in the chats.',
    'web.settings.ready': 'ready',
    'web.settings.up': 'Move {{name}} up',
    'web.settings.down': 'Move {{name}} down',
    'web.settings.keySet': 'A key is set. Type a new one to replace it.',
    'web.settings.keyNotSet': 'No key is set.',
    'web.settings.keyPlaceholder': 'API key',
    'web.settings.removeKey': 'Remove the key',
    'web.settings.googleEngine': 'Search engine ID (cx)',
    'web.settings.searxngUrl': 'SearXNG address',
    'web.settings.save': 'Save',
    'web.settings.saved': 'Saved.',
    'web.settings.test': 'Test the providers',
    'web.settings.testOk': '{{count}} results from {{host}}',
    'web.settings.failed': 'The change was refused.',
  })
  const t = (key) => sdk.locale.t(key, key)

  function Component({ page }) {
    if (page !== 'web') return null
    return (
      <section data-testid="web-page" className="web-page">
        <h1 className="page-title web-page__title">{t('web.title')}</h1>
        <p>{t('web.intro')}</p>
        <ul className="web-page__rules">
          <li>{t('web.rules.order')}</li>
          <li>{t('web.rules.untrusted')}</li>
          <li>{t('web.rules.settings')}</li>
        </ul>
      </section>
    )
  }
  const { WebSettings, WebSettingsView } = createSettings(sdk, t)
  return { Component, components: { WebSettings, WebSettingsView }, chatActions: [], dispose() {} }
}
