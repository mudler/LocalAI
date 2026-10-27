// The entry of the Sandbox add-on. Its work happens in the chats: the tools run
// commands and code in an isolated sandbox. This page says so and, for an
// administrator, shows the state of the sandbox.
import { createSettings } from './Settings.jsx'
import { createPageStatus } from './PageStatus.jsx'

export function createAddon(sdk) {
  const { React } = sdk
  // eslint-disable-next-line no-unused-vars
  const h = React.createElement
  sdk.locale.define('en', {
    'sandbox.title': 'Sandbox',
    'sandbox.intro': 'Your chats can run commands and code here. Each chat gets its own sandbox with your files mounted. Changes the code makes to your files are saved as one change you can undo.',
    'sandbox.loading': 'Checking whether this host can run commands…',
    'sandbox.unknown': 'The state of the sandbox could not be read.',
    'sandbox.isolated': 'Commands run in the {{backend}} sandbox: an isolated environment with no network.',
    'sandbox.host': 'Commands run directly on this machine, with no isolation. Anything the code does, it does to this machine. Every command is logged with the name of the chat user.',
    'sandbox.cannot': 'This host cannot run commands: no sandbox backend works. Chats that ask for one are told so. An administrator can see why in Settings.',
    'sandbox.reasons': 'Why a backend does not work',
    'sandbox.limits': 'A sandbox sleeps after 10 minutes without use and is removed after 24 hours.',
    'sandbox.settings.loading': 'Reading the sandbox state…',
    'sandbox.settings.runsOn': 'Commands run in the {{backend}} sandbox.',
    'sandbox.settings.cannotRun': 'This host cannot run commands: no sandbox backend works. Chats that ask for one are told so.',
    'sandbox.settings.usable': 'ready',
    'sandbox.settings.choice': 'Backend',
    'sandbox.settings.option.auto': 'Automatic (KVM, then gVisor)',
    'sandbox.settings.option.gvisor': 'gVisor',
    'sandbox.settings.option.kvm': 'KVM virtual machine',
    'sandbox.settings.option.off': 'Off',
    'sandbox.settings.option.host': 'Host (no isolation)',
    'sandbox.settings.hostWarning': 'The host backend runs commands directly on this machine, as the user LocalAI runs as, with no isolation. Anything the code does, it does to this machine and to everything that user can reach. Every command is logged with the name of the chat user. Use it only on a machine you do not mind losing.',
    'sandbox.settings.hostPhrase': 'To accept this, type: I understand that commands run on this host without isolation',
    'sandbox.settings.hostNotice': 'Commands now run on this machine with no isolation.',
    'sandbox.settings.save': 'Save',
    'sandbox.settings.failed': 'The change was refused.',
  })
  const t = (key) => sdk.locale.t(key, key)

  const { PageStatus, SandboxPageStatus } = createPageStatus(sdk, t)

  function Component({ page }) {
    if (page !== 'sandbox') return null
    return (
      <section data-testid="sandbox-page" className="sandbox-page">
        <h1 className="page-title sandbox-page__title">{t('sandbox.title')}</h1>
        <p>{t('sandbox.intro')}</p>
        <PageStatus />
        <ul className="sandbox-page__rules">
          <li>{t('sandbox.limits')}</li>
        </ul>
      </section>
    )
  }
  const { SandboxSettings, SandboxSettingsView } = createSettings(sdk, t)
  return { Component, components: { SandboxSettings, SandboxSettingsView, SandboxPageStatus }, chatActions: [], dispose() {} }
}
