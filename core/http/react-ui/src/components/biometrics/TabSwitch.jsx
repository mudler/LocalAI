import { Tabs } from '../ui'

// The views of a biometrics tool (and of the add-ons that use it through the
// host components). The kit's Tabs: one tab stop, arrow keys, Home and End.
// The caller draws the panel of the selected tab.
export default function TabSwitch({ tabs, value, onChange, label, idPrefix }) {
  return (
    <Tabs
      label={label}
      idPrefix={idPrefix}
      value={value}
      onChange={onChange}
      items={tabs.map(t => ({ id: t.id, label: t.label, icon: t.icon }))}
    />
  )
}
