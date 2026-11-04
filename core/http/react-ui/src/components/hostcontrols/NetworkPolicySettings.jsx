/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import EgressSection from '../settings/EgressSection'
import SettingField from '../settings/SettingField'
import { FIELD_BY_KEY } from '../../utils/settingsSchema'
import { NETWORK_POLICY_FIELDS } from '../../addons/hostControls'

// The network-policy control on an add-on's Settings tab: the network of the
// commands the add-on runs, named from the title it declares (app.network). The same section and rows the Settings page drew
// before, bound to the same runtime settings and saved with the form around
// them.
export default function NetworkPolicySettings({ app, settings, initial, onChange, pending }) {
  return (
    <div className="ad-host-control" data-testid="host-control-network-policy">
      <p className="st-note">Allowed hosts accept exact names, *.example.com (descendants only; add example.com separately for the apex), or * (public destinations only). Wildcards still obey the ceiling, allowed ports and private-address guard. Extra registries require exact names. The owner validates every change.</p>
      <EgressSection settings={settings} initial={initial} onChange={onChange} showKey={false} provider={app?.network?.title || app?.title || ''} />
      <div className="st-list">
        {NETWORK_POLICY_FIELDS.filter(key => key !== 'chats_egress_audit_days').map(key => FIELD_BY_KEY[key] && (
          <SettingField key={key} field={FIELD_BY_KEY[key]} settings={settings} onChange={onChange} pending={pending.has(key)} showKey={false} />
        ))}
      </div>
    </div>
  )
}
