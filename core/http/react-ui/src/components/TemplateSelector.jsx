import MODEL_TEMPLATES from '../utils/modelTemplates'
import Icon from './Icon'

// The ways to start a model config, read top to bottom. A list rather than a
// grid of equal cards: these are alternatives to scan, and the field names
// each one pre-fills sit on one muted line instead of a cloud of chips.
export default function TemplateSelector({ onSelect }) {
  return (
    <div className="template-picker">
      <p className="template-picker__lede">
        Choose a template to get started. You can add or remove fields in the next step.
      </p>
      <ul className="lanes lanes--templates">
        {MODEL_TEMPLATES.map(t => (
          <li key={t.id}>
            <button type="button" className="lane" onClick={() => onSelect(t)}>
              <Icon name={t.icon} className="template-picker__icon" />
              <span className="lane__main">
                <span className="lane__name">{t.label}</span>
                <span className="lane__desc">{t.description}</span>
                <span className="template-picker__fields">
                  {Object.keys(t.fields).filter(k => k !== 'name').join(', ')}
                </span>
              </span>
              <span className="lane__go" aria-hidden="true">→</span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}
