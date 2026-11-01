/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Callout } from '../ui'
import AddonSlot from '../../addons/AddonSlot'
import { usable } from '../../addons/registryState'
import { declaresSlot } from '../../addons/slots'
import HostControlsForm from '../hostcontrols/HostControlsForm'

// The Settings tab of an add-on's details: the add-on's own settings section
// (its settings.sections contribution, drawn here and nowhere else), then the
// host controls the apps list draws for it (ui.controls), which the host
// draws with its own components and binds to its own settings. Administrators
// only: the server refuses every change to anyone else, and the tab is not
// offered to them.
export default function AddonSettings({ app, title }) {
  const { t } = useTranslation('addons')
  const context = useMemo(() => ({ addonId: app.id }), [app.id])
  const own = declaresSlot(app, 'settings.sections')
  return (
    <div className="ad-settings" data-testid="addon-settings" data-app={app.id}>
      {own && (usable(app)
        ? <AddonSlot slot="settings.sections" context={context} />
        : <Callout tone="info" testId="addon-settings-off">{t('settingsTab.off', { name: title })}</Callout>)}
      <HostControlsForm app={app} />
    </div>
  )
}
