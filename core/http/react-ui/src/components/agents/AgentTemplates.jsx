import { useTranslation } from 'react-i18next'
import './agentparts.css'

// The starting points of a new agent, as cards: what it is, and the level of
// tools it starts at. Picking one fills the form and can be undone.
export default function AgentTemplates({ templates, picked, onPick }) {
  const { t } = useTranslation('agents')
  return (
    <div className="aed-tpl" role="group" aria-label={t('create.startFrom')}>
      {templates.map((tpl) => (
        <button
          key={tpl.id} type="button" className="aed-tpl__card" aria-pressed={picked === tpl.id}
          data-template={tpl.id} onClick={() => onPick(tpl)}
        >
          <span className="aed-tpl__name">{t(`templates.${tpl.id}.label`)}</span>
          <span className="aed-tpl__body">{t(`templates.${tpl.id}.description`)}</span>
          <span className="aed-tpl__tools">{tpl.preset ? t('create.tpl.tools', { level: t(`create.preset.${tpl.preset}.title`) }) : t('create.tpl.noTools')}</span>
        </button>
      ))}
    </div>
  )
}
