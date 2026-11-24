/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import BuiltinToolsSection from '../settings/BuiltinToolsSection'

// The tool-providers control on an add-on's Settings tab: the Settings > Tools
// rows of the built-in tools the add-on replaces, as the apps list names them.
export default function ToolProvidersSettings({ app, settings, onChange }) {
  return (
    <div className="ad-host-control" data-testid="host-control-tool-providers">
      <BuiltinToolsSection settings={settings} onChange={onChange} only={app.shadows || []} providersOnly />
    </div>
  )
}
