import Icon from '../Icon'

export default function TabSwitch({ tabs, value, onChange }) {
  return (
    <div className="biometrics-tabs" role="tablist">
      {tabs.map(t => {
        const active = t.id === value
        return (
          <button
            key={t.id}
            role="tab"
            type="button"
            aria-selected={active}
            className={`biometrics-tab ${active ? 'active' : ''}`}
            onClick={() => onChange(t.id)}
          >
            {t.icon && <Icon name={t.icon} aria-hidden="true" />}
            <span>{t.label}</span>
          </button>
        )
      })}
    </div>
  )
}
